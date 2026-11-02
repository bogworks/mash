// Package apply pushes a compiled policy to the MASH account (ADR 0052, 0053): one scoped signing key per zone in
// the account JWT. It looks the current account JWT up on the resolver through the system account, replaces or adds
// only the scoped signing keys of the policy's zones, signs the result with the operator key, pushes it, and reads it
// back to verify. It never removes a zone key and it is not a transaction across NATS claims, JetStream resources and
// local files; a partial result says what was and was not done. Seed text is never put in output or errors.
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
)

// NotATransaction is the one sentence every run ends with.
const NotATransaction = "apply is not a transaction across NATS claims, JetStream resources and local files: if a step fails it says what was done and what was not, and running `mashctl policy apply` again is safe."

// Resolver is the system-account view of the account resolver.
type Resolver interface {
	// Lookup returns the account's current JWT, or "" when the resolver has none.
	Lookup(ctx context.Context, account string) (string, error)
	// Push sends the account JWT as a claims update and returns the answer of every server that replied.
	Push(ctx context.Context, token string) ([]ServerAck, error)
}

// ServerAck is one server's answer to a claims update.
type ServerAck struct {
	Server  string
	Code    int
	Message string
}

// OK says the server accepted the update.
func (a ServerAck) OK() bool { return a.Code == 200 }

// Mode is what a run does.
type Mode int

const (
	// Apply pushes, creating missing zone keys first.
	Apply Mode = iota
	// DryRun reports what Apply would do and changes nothing.
	DryRun
	// Diff is DryRun under another name (`mashctl policy diff`).
	Diff
)

func (m Mode) String() string {
	switch m {
	case DryRun:
		return "policy apply --dry-run"
	case Diff:
		return "policy diff"
	}
	return "policy apply"
}

// Params is one run.
type Params struct {
	Mode     Mode
	Compiled *policy.Compiled
	Keys     *ctlkeys.Authority
	Resolver Resolver
	// Budget measures the compile; nil means policy.Budget. A seam for tests.
	Budget func(*policy.Compiled) (policy.BudgetReport, error)
	// VerifyTries and VerifyWait bound the read-back after a push (defaults 5 and 200ms): a clustered resolver
	// may take a moment to answer with the new claims everywhere.
	VerifyTries int
	VerifyWait  time.Duration
}

const (
	defaultVerifyTries = 5
	defaultVerifyWait  = 200 * time.Millisecond
)

// zoneState is how one file zone compares with the server.
type zoneState int

const (
	zoneUnchanged zoneState = iota
	zoneNew
	zoneChanged
)

type zonePlan struct {
	name      string
	state     zoneState
	pub       string
	keyIsNew  bool // no key file yet: apply creates it, a dry run would
	scope     *jwt.UserScope
	changes   []string // "pub allow  + line" for a changed or new zone
	otherDiff bool     // changed in the role or limits only
}

// Run executes p and writes its report to out; the result is the process exit code (0 ok, 1 refused or partial).
func Run(ctx context.Context, p Params, out io.Writer) int {
	pf := func(format string, a ...any) { fmt.Fprintf(out, format, a...) }
	fail := func(err error) int { fmt.Fprintf(out, "mashctl: %s: %v\n", p.Mode, err); return 1 }
	c := p.Compiled

	budget := p.Budget
	if budget == nil {
		budget = policy.Budget
	}
	if rep, err := budget(c); err != nil || !rep.OK {
		if err == nil {
			err = errors.New("over budget")
		}
		return fail(fmt.Errorf("refusing: the policy does not fit: %w (nothing was changed)", err))
	}

	opKP, _, err := p.Keys.Operator()
	if err != nil {
		return fail(err)
	}
	defer opKP.Wipe()
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return fail(err)
	}
	acctKP.Wipe()

	cur, err := p.Resolver.Lookup(ctx, acctPub)
	if err != nil {
		return fail(fmt.Errorf("look up the account JWT: %w (nothing was changed)", err))
	}
	if cur == "" {
		return fail(fmt.Errorf("the resolver has no JWT for the MASH account %s: is the account key in the key store the account of this server? (nothing was changed)", acctPub))
	}
	current, err := jwt.DecodeAccountClaims(cur)
	if err != nil {
		return fail(fmt.Errorf("the resolver's account JWT does not decode: %w (nothing was changed)", err))
	}
	if current.Subject != acctPub {
		return fail(fmt.Errorf("the resolver answered with the JWT of account %s, not %s (nothing was changed)", current.Subject, acctPub))
	}
	desired, err := jwt.DecodeAccountClaims(cur)
	if err != nil {
		return fail(err)
	}
	if desired.SigningKeys == nil {
		desired.SigningKeys = jwt.SigningKeys{}
	}

	// Zone keys: apply creates the missing ones, a dry run reports them and uses a throwaway public key for sizing.
	var plans []*zonePlan
	var created []string
	filePubs := map[string]string{} // zone public key -> zone name
	for _, z := range c.Zones {
		zp := &zonePlan{name: z.Name}
		kp, kerr := p.Keys.ZoneKey(z.Name, false)
		switch {
		case kerr == nil:
		case errors.Is(kerr, ctlkeys.ErrNoZoneKey):
			zp.keyIsNew = true
			if p.Mode == Apply {
				kp, kerr = p.Keys.ZoneKey(z.Name, true)
				if kerr != nil {
					return fail(kerr)
				}
				created = append(created, z.Name)
			} else if kp, kerr = nkeys.CreateAccount(); kerr != nil {
				return fail(kerr)
			}
		default:
			return fail(kerr)
		}
		zp.pub, err = kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return fail(err)
		}
		filePubs[zp.pub] = z.Name
		scope := jwt.NewUserScope()
		scope.Key = zp.pub
		scope.Role = z.Name
		scope.Template.Permissions = z.Perms
		zp.scope = scope

		have, _ := current.SigningKeys.GetScope(zp.pub)
		old, _ := have.(*jwt.UserScope)
		switch {
		case old == nil:
			zp.state = zoneNew
			zp.changes = permDiff(nil, scope)
		case scopeJSON(old) == scopeJSON(scope):
			zp.state = zoneUnchanged
		default:
			zp.state = zoneChanged
			zp.changes = permDiff(old, scope)
			zp.otherDiff = len(zp.changes) == 0
		}
		desired.SigningKeys[zp.pub] = scope
		plans = append(plans, zp)
	}

	// Scoped signers on the server that no zone of the file owns are left alone and reported.
	var extras []string
	for _, k := range sortedKeys(current.SigningKeys) {
		sc, _ := current.SigningKeys.GetScope(k)
		us, ok := sc.(*jwt.UserScope)
		if !ok || us == nil {
			continue // a plain signing key is not a zone
		}
		if _, ours := filePubs[k]; ours {
			continue
		}
		extras = append(extras, us.Role)
	}

	changed := false
	for _, zp := range plans {
		changed = changed || zp.state != zoneUnchanged
	}
	token := cur
	if changed {
		token, err = desired.Encode(opKP)
		if err != nil {
			return fail(fmt.Errorf("encode the account JWT: %w (nothing was changed)", err))
		}
		if len(token) > policy.AccountJWTLimit {
			return fail(fmt.Errorf("the account JWT would be %d bytes, over the %d byte limit: fewer zones, or fewer zone pairs granted (nothing was changed)", len(token), policy.AccountJWTLimit))
		}
	}

	report(out, p, plans, extras, len(cur), len(token), changed)

	if p.Mode != Apply {
		if changed {
			pf("\n%s: dry run, nothing was changed on the server and no key was created.\n", p.Mode)
		} else {
			pf("\n%s: already up to date.\n", p.Mode)
		}
		pf("%s\n", NotATransaction)
		return 0
	}
	for _, z := range created {
		pf("created the signing key of zone %s (a local file in the key store)\n", z)
	}
	if !changed {
		pf("\n%s: already up to date, nothing pushed.\n%s\n", p.Mode, NotATransaction)
		return 0
	}

	// Push, then read back.
	acks, perr := p.Resolver.Push(ctx, token)
	var bad []string
	for _, a := range acks {
		if !a.OK() {
			bad = append(bad, fmt.Sprintf("%s: code %d %s", serverName(a), a.Code, a.Message))
		}
	}
	if perr != nil || len(bad) > 0 || len(acks) == 0 {
		var why string
		switch {
		case perr != nil:
			why = perr.Error()
		case len(acks) == 0:
			why = "no server answered"
		default:
			why = "refused by " + strings.Join(bad, "; ")
		}
		return partial(out, p, created, fmt.Sprintf("the claims update was not acknowledged: %s", why), verifyState(ctx, p, acctPub, desired))
	}
	if msg := verifyState(ctx, p, acctPub, desired); msg != "" {
		return partial(out, p, created, fmt.Sprintf("%d server(s) acknowledged the claims update", len(acks)), msg)
	}
	pf("\npushed: %d server(s) acknowledged the claims update and the resolver now answers with the new signing keys (%d zones).\n", len(acks), len(plans))
	pf("%s\n", NotATransaction)
	return 0
}

// partial prints what was and was not done and returns exit code 1. observed is what the read-back found ("" is a
// match).
func partial(out io.Writer, p Params, created []string, step, observed string) int {
	fmt.Fprintf(out, "\nPARTIAL: %s\n", step)
	fmt.Fprintln(out, "done:")
	if len(created) > 0 {
		fmt.Fprintf(out, "  zone keys created locally: %s\n", strings.Join(created, ", "))
	} else {
		fmt.Fprintln(out, "  nothing that persists (no zone key was created)")
	}
	fmt.Fprintln(out, "not done or not confirmed:")
	if observed == "" {
		fmt.Fprintln(out, "  the resolver answers with the new claims, but the update was not acknowledged by every server as required")
	} else {
		fmt.Fprintf(out, "  the account JWT on the resolver: %s\n", observed)
	}
	fmt.Fprintln(out, "  user JWTs were not touched")
	fmt.Fprintln(out, "retry: mashctl policy apply   (idempotent: it pushes only what differs; mashctl policy diff shows what that is)")
	fmt.Fprintf(out, "%s\n", NotATransaction)
	return 1
}

// verifyState reads the account JWT back until it matches desired's signing keys and templates; "" is a match, else
// the description of what was found.
func verifyState(ctx context.Context, p Params, acctPub string, desired *jwt.AccountClaims) string {
	tries, wait := p.VerifyTries, p.VerifyWait
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
		tok, err := p.Resolver.Lookup(ctx, acctPub)
		if err != nil {
			last = "could not be read back: " + err.Error()
			continue
		}
		got, err := jwt.DecodeAccountClaims(tok)
		if err != nil {
			last = "does not decode: " + err.Error()
			continue
		}
		if d := signersDiff(desired, got); d != "" {
			last = d
			continue
		}
		return ""
	}
	return last
}

// signersDiff compares the signing keys (scoped ones with their role and template) of want and got.
func signersDiff(want, got *jwt.AccountClaims) string {
	var miss, extra, differ []string
	for _, k := range sortedKeys(want.SigningKeys) {
		ws, _ := want.SigningKeys.GetScope(k)
		gs, ok := got.SigningKeys.GetScope(k)
		if !ok {
			miss = append(miss, label(k, ws))
			continue
		}
		wu, _ := ws.(*jwt.UserScope)
		gu, _ := gs.(*jwt.UserScope)
		if (wu == nil) != (gu == nil) || (wu != nil && scopeJSON(wu) != scopeJSON(gu)) {
			differ = append(differ, label(k, ws))
		}
	}
	for _, k := range sortedKeys(got.SigningKeys) {
		if _, ok := want.SigningKeys.GetScope(k); !ok {
			gs, _ := got.SigningKeys.GetScope(k)
			extra = append(extra, label(k, gs))
		}
	}
	var parts []string
	if len(miss) > 0 {
		parts = append(parts, "missing signing keys: "+strings.Join(miss, ", "))
	}
	if len(differ) > 0 {
		parts = append(parts, "templates differ for: "+strings.Join(differ, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "unexpected signing keys: "+strings.Join(extra, ", "))
	}
	return strings.Join(parts, "; ")
}

func label(key string, s jwt.Scope) string {
	if us, ok := s.(*jwt.UserScope); ok && us != nil {
		return "zone " + us.Role
	}
	return key
}

func serverName(a ServerAck) string {
	if a.Server == "" {
		return "a server"
	}
	return a.Server
}

func sortedKeys(sk jwt.SigningKeys) []string {
	k := sk.Keys()
	sort.Strings(k)
	return k
}

// scopeJSON is the JSON of a copy of s with its permission lists sorted, so equal sets compare equal.
func scopeJSON(s *jwt.UserScope) string {
	cp := *s
	sortList := func(l jwt.StringList) jwt.StringList {
		out := append(jwt.StringList(nil), l...)
		sort.Strings(out)
		return out
	}
	cp.Template.Pub.Allow, cp.Template.Pub.Deny = sortList(s.Template.Pub.Allow), sortList(s.Template.Pub.Deny)
	cp.Template.Sub.Allow, cp.Template.Sub.Deny = sortList(s.Template.Sub.Allow), sortList(s.Template.Sub.Deny)
	b, err := json.Marshal(cp)
	if err != nil {
		return fmt.Sprintf("%#v", cp)
	}
	return string(b)
}

// permDiff lists the added ("+") and removed ("-") permission lines going from old (nil: none) to now.
func permDiff(old, now *jwt.UserScope) []string {
	lists := func(s *jwt.UserScope) map[string][]string {
		m := map[string][]string{}
		if s == nil {
			return m
		}
		m["pub allow"] = s.Template.Pub.Allow
		m["pub deny"] = s.Template.Pub.Deny
		m["sub allow"] = s.Template.Sub.Allow
		m["sub deny"] = s.Template.Sub.Deny
		return m
	}
	o, n := lists(old), lists(now)
	var out []string
	for _, k := range []string{"pub allow", "pub deny", "sub allow", "sub deny"} {
		oset, nset := toSet(o[k]), toSet(n[k])
		for _, l := range sortedSet(nset) {
			if !oset[l] {
				out = append(out, fmt.Sprintf("%-9s + %s", k, l))
			}
		}
		for _, l := range sortedSet(oset) {
			if !nset[l] {
				out = append(out, fmt.Sprintf("%-9s - %s", k, l))
			}
		}
	}
	return out
}

func toSet(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, s := range l {
		m[s] = true
	}
	return m
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// report prints the diff shared by apply, --dry-run and diff.
func report(out io.Writer, p Params, plans []*zonePlan, extras []string, before, after int, changed bool) {
	fmt.Fprintf(out, "%s: %d zone(s) in the file\n", p.Mode, len(plans))
	for _, zp := range plans {
		switch zp.state {
		case zoneUnchanged:
			fmt.Fprintf(out, "  zone %s: unchanged\n", zp.name)
		case zoneNew:
			note := ""
			if zp.keyIsNew {
				if p.Mode == Apply {
					note = " (its signing key is created now)"
				} else {
					note = " (would create its signing key)"
				}
			}
			fmt.Fprintf(out, "  zone %s: new%s\n", zp.name, note)
		case zoneChanged:
			fmt.Fprintf(out, "  zone %s: changed\n", zp.name)
		}
		for _, l := range zp.changes {
			fmt.Fprintf(out, "      %s\n", l)
		}
		if zp.otherDiff {
			fmt.Fprintln(out, "      the permission lines are the same; the role or limits of the key differ")
		}
	}
	for _, z := range extras {
		fmt.Fprintf(out, "  zone %s has a key on the server but is not in the file: apply never removes a zone key, that would lock out its users\n", z)
	}
	fmt.Fprintf(out, "account JWT: %d bytes now, %d after, limit %d\n", before, after, policy.AccountJWTLimit)
	tagged := 0
	for _, e := range p.Compiled.Endpoints {
		if len(e.Tags) > 0 {
			tagged++
		}
	}
	fmt.Fprintf(out, "endpoints: %d in the file, %d carry exception tags\n", len(p.Compiled.Endpoints), tagged)
	if changed && tagged > 0 {
		for _, zp := range plans {
			if zp.state != zoneUnchanged {
				fmt.Fprintln(out, "user JWTs carrying changed exception tags must be re-issued with mashctl add")
				break
			}
		}
	}
}
