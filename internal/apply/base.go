package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
)

// baseJWT is the account JWT a writer (apply, revoke) builds its update from: the newest copy any server holds.
type baseJWT struct {
	token  string // "" when no server has a JWT for the account
	claims *jwt.AccountClaims
	// warn is "" when every answering server holds the base, else a neutral line naming how many hold an older copy.
	warn string
	// short is "" when at least expect servers answered the lookup, else the neutral sentence saying how many did. A
	// writer refuses on it; a read-only run prints it as a warning.
	short string
}

// serverExpect is the number of servers a lookup should wait for: what the ping and the inventory say, or 0 (unknown,
// so the lookup ends when no new reply arrived for a settle period).
func serverExpect(set ServerSet, inventory int) int {
	return max(set.need(inventory), len(set.Names))
}

func isoTime(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05Z") }

// lookupBase asks every server for the account JWT (LookupAll; expect is the known server count, 0 when unknown) and
// returns the copy with the highest iat, never "the first reply". Every reply must pass VerifyAccountJWT: one that does
// not is refused, not skipped. Two different copies with the same highest iat are refused as ambiguous. Older copies
// do not decide anything: they are reported in warn, and the push that follows brings every server to the newest. A
// resolver that answers with no JWT at all yields an empty token. what names the step in a transport error.
func lookupBase(ctx context.Context, r Resolver, acct string, trust Trust, expect int, what string) (baseJWT, error) {
	toks, err := r.LookupAll(ctx, acct, expect)
	if err != nil && len(toks) == 0 {
		return baseJWT{}, fmt.Errorf("%s: %w", what, err)
	}
	type copyT struct {
		claims *jwt.AccountClaims
		n      int
	}
	copies := map[string]*copyT{}
	var empty int
	for _, t := range toks {
		if t == "" {
			empty++
			continue
		}
		if c, ok := copies[t]; ok {
			c.n++
			continue
		}
		claims, verr := VerifyAccountJWT(t, acct, trust)
		if verr != nil {
			return baseJWT{}, fmt.Errorf("refusing the resolver's account JWT: %w", verr)
		}
		copies[t] = &copyT{claims: claims, n: 1}
	}
	short := ""
	if expect > 0 && len(toks) < expect {
		short = fmt.Sprintf("only %d of %d servers answered the account lookup", len(toks), expect)
	}
	if len(copies) == 0 {
		return baseJWT{short: short}, nil
	}
	var newest int64
	for _, c := range copies {
		newest = max(newest, c.claims.IssuedAt)
	}
	var top []string
	for t, c := range copies {
		if c.claims.IssuedAt == newest {
			top = append(top, t)
		}
	}
	if len(top) > 1 {
		sort.Strings(top)
		var parts []string
		for _, t := range top {
			sum := sha256.Sum256([]byte(t))
			parts = append(parts, fmt.Sprintf("sha %s on %d server(s)", hex.EncodeToString(sum[:4]), copies[t].n))
		}
		return baseJWT{}, fmt.Errorf("the servers hold %d distinct account JWTs issued at the same time (iat %s; %s): which one is current cannot be told, so none is used; make the servers agree (for example with nsc push or by restoring the lagging server), then run the command again", len(top), isoTime(newest), strings.Join(parts, ", "))
	}
	b := baseJWT{token: top[0], claims: copies[top[0]].claims, short: short}
	older := empty
	var iats []string
	seen := map[int64]bool{}
	for t, c := range copies {
		if t == top[0] {
			continue
		}
		older += c.n
		if !seen[c.claims.IssuedAt] {
			seen[c.claims.IssuedAt] = true
		}
	}
	var keys []int64
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		iats = append(iats, isoTime(k))
	}
	if empty > 0 {
		iats = append(iats, "no JWT")
	}
	if older > 0 {
		b.warn = fmt.Sprintf("%d of %d servers hold an older copy (iat %s); building from the newest (iat %s)", older, len(toks), strings.Join(iats, ", "), isoTime(newest))
	}
	return b, nil
}

// shortRefusal is the refusal of a write whose lookup was not answered by every known server: the silent one may hold a
// newer copy than the one a write would build from.
func shortRefusal(short string) string {
	return short + "; mashctl will not write from a copy that may be older than the newest one a silent server holds. Bring the silent server back (or fix --servers) and run it again"
}

// notCompleteRefusal is the refusal of a write whose server set could not be confirmed complete before anything is
// created or pushed.
func notCompleteRefusal(why string) string {
	return fmt.Sprintf("the server set could not be verified (%s); a write needs a complete, confirmed server set. Fix the cause (or pass --servers N) and run it again", why)
}

// readBack asks every server for the account JWT until each one that answers holds want, at least need of them
// answer, and check finds nothing wrong with any copy; "" is a match, else what was found. check is called with the
// verified claims of a copy that is not want itself and returns what is missing ("" when that copy is fine).
func readBack(ctx context.Context, r Resolver, acct string, trust Trust, need int, tries int, wait time.Duration, want string, differs func(*jwt.AccountClaims) string) string {
	if tries <= 0 {
		tries = defaultVerifyTries
	}
	if wait <= 0 {
		wait = defaultVerifyWait
	}
	last := ""
	for i := 0; i < tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "the check was interrupted: " + ctx.Err().Error()
			case <-time.After(wait):
			}
		}
		toks, err := r.LookupAll(ctx, acct, need)
		if err != nil && len(toks) == 0 {
			last = "could not be read back: " + err.Error()
			continue
		}
		last = ""
		stale := 0
		var detail string
		for _, t := range toks {
			if t == want {
				continue
			}
			stale++
			if detail != "" {
				continue
			}
			got, verr := VerifyAccountJWT(t, acct, trust)
			switch {
			case t == "":
				detail = "a server holds no JWT for the account"
			case verr != nil:
				detail = "is not acceptable: " + verr.Error()
			default:
				detail = differs(got)
			}
		}
		switch {
		case stale > 0:
			if detail == "" {
				detail = "holds a different JWT than the one pushed"
			}
			last = fmt.Sprintf("%d of %d answering servers do not hold the pushed JWT: %s", stale, len(toks), detail)
		case len(toks) < need:
			last = fmt.Sprintf("only %d of %d servers answered the account JWT lookup (a server may be down or partitioned)", len(toks), need)
		default:
			return ""
		}
	}
	return last
}
