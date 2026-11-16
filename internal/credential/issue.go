package credential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policydirect"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/store"
)

// Records is the issue record as Show reads it.
type Records interface {
	IssuedByKey(ctx context.Context, publicKey string) (store.Issued, bool, error)
	IssuedHistoryByKey(ctx context.Context, publicKey string) (admin.IssuedTimes, bool, error)
}

// Config is everything the caller holds, as the seams Issue and Show read through. FromDirect makes one from the pieces
// the direct policy driver already uses to reach the cluster. A seam a call does not use may be left nil: Issue needs
// Account, ListSet, Signer, Record and Claims; Show needs Records, Revoked and Scan.
type Config struct {
	// Account is the account's public key.
	Account string
	// ListSet is the list set in force: the ledger the direct driver writes.
	ListSet issue.ListSetInForce
	// Signer holds the owner groups' scoped signing keys.
	Signer issue.Signer
	// Record is the issue record as a checked compare-and-set.
	Record issue.Record
	// Claims reads the account JWT, verified, from every server.
	Claims issue.AccountClaims

	// Records, Revoked and Scan are what Show reads: the record and its history, whether the account JWT revokes a key
	// on any server, and the connections of the account on every server.
	Records Records
	Revoked func(ctx context.Context, publicKey string) (bool, error)
	Scan    func(ctx context.Context) (policyserve.ConnScan, error)

	Log *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (c Config) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

var errConfig = errors.New("credential: invalid configuration")

func (c Config) checkIssue() error {
	switch {
	case !nkeys.IsValidPublicAccountKey(c.Account):
		return fmt.Errorf("%w: no account key", errConfig)
	case c.ListSet == nil:
		return fmt.Errorf("%w: no list set in force reader", errConfig)
	case c.Signer == nil:
		return fmt.Errorf("%w: no key store", errConfig)
	case c.Record == nil:
		return fmt.Errorf("%w: no issue record", errConfig)
	case c.Claims == nil:
		return fmt.Errorf("%w: no account JWT reader", errConfig)
	}
	return nil
}

// FromDirect makes a Config from the Config of the direct policy driver: the list set in force is the ledger that driver
// writes (policydirect.Config.InForce), the signing keys are its key store's, the record is its admin connection's, and
// the account JWT and the connections are read through its system-account connection, verified against its trust. It
// opens no connection and closes none: the caller's own are used.
func FromDirect(d policydirect.Config) (Config, error) {
	switch {
	case !nkeys.IsValidPublicAccountKey(d.Account):
		return Config{}, fmt.Errorf("%w: no account key", errConfig)
	case d.Admin == nil:
		return Config{}, fmt.Errorf("%w: no admin connection", errConfig)
	case d.Sys == nil:
		return Config{}, fmt.Errorf("%w: no system-account connection", errConfig)
	case d.Keys == nil:
		return Config{}, fmt.Errorf("%w: no key store", errConfig)
	case d.Servers < 1:
		return Config{}, fmt.Errorf("%w: the declared server count must be 1 or more", errConfig)
	}
	cl := &clusterClaims{res: apply.NATS{Conn: d.Sys, Inventory: d.Servers}, account: d.Account, servers: d.Servers, trust: d.Trust}
	return Config{
		Account: d.Account,
		ListSet: listSetInForce{d},
		Signer:  groupSigner{d.Keys},
		Record:  adminRecord{d.Admin},
		Claims:  cl,
		Records: d.Admin,
		Revoked: cl.revokes,
		Scan: func(ctx context.Context) (policyserve.ConnScan, error) {
			return policyserve.ScanConnections(ctx, d.Sys, d.Account, d.Servers)
		},
		Log: d.Log,
	}, nil
}

// listSetInForce is the issue path's list set in force: the direct driver's ledger.
type listSetInForce struct{ d policydirect.Config }

func (l listSetInForce) InForce(ctx context.Context) (*listset.Compiled, int, error) {
	return l.d.InForce(ctx)
}

// groupSigner gives the issue path the owner groups' scoped keys, never creating one.
type groupSigner struct{ keys policyserve.Keys }

func (g groupSigner) GroupKey(group string) (nkeys.KeyPair, error) {
	return g.keys.GroupKey(group, false)
}

// adminRecord is the issue record over the admin connection.
type adminRecord struct{ a *admin.Admin }

func (r adminRecord) Lookup(ctx context.Context, publicKey string) (*store.Issued, error) {
	rec, found, err := r.a.IssuedByKey(ctx, publicKey)
	if err != nil || !found {
		return nil, err
	}
	return &rec, nil
}

func (r adminRecord) RecordChecked(ctx context.Context, rec store.Issued) error {
	return r.a.RecordIssuedChecked(ctx, rec)
}

// clusterClaims reads the account JWT from every server and verifies each copy against the operator trust. It answers
// only when it has seen every server (the server set is complete, every server answers and holds a JWT, every copy
// verifies); anything less is an error, never an answer from the servers that came.
type clusterClaims struct {
	res     apply.NATS
	account string
	servers int
	trust   apply.Trust
}

// copies is every distinct verified copy of the account JWT.
func (c *clusterClaims) copies(ctx context.Context) ([]*jwt.AccountClaims, error) {
	set, err := c.res.Servers(ctx)
	if errors.Is(err, apply.ErrServerSetRefused) {
		return nil, fmt.Errorf("the server set is refused: %w", err)
	}
	if !set.Complete(c.servers) {
		why := set.Why(c.servers)
		if err != nil {
			why = err.Error()
		}
		return nil, fmt.Errorf("the server set is not complete: %s", why)
	}
	expect := max(set.Expected, c.servers, len(set.Names))
	toks, err := c.res.LookupAll(ctx, c.account, expect)
	if err != nil && len(toks) == 0 {
		return nil, fmt.Errorf("look up the account JWT: %w", err)
	}
	if len(toks) < expect {
		return nil, fmt.Errorf("only %d of %d servers answered the account lookup", len(toks), expect)
	}
	var out []*jwt.AccountClaims
	seen := map[string]bool{}
	for _, t := range toks {
		if t == "" {
			return nil, errors.New("a server holds no account JWT")
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		claims, err := apply.VerifyAccountJWT(t, c.account, c.trust)
		if err != nil {
			return nil, fmt.Errorf("an account JWT does not verify: %w", err)
		}
		out = append(out, claims)
	}
	return out, nil
}

// Fetch implements issue.AccountClaims. The servers may hold different copies for a moment after a push: the signing
// keys must agree on every one (else it is an error and nothing is signed), and a key any copy revokes is revoked.
func (c *clusterClaims) Fetch(ctx context.Context) (*jwt.AccountClaims, error) {
	cs, err := c.copies(ctx)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, errors.New("no account JWT")
	}
	first := cs[0]
	want := signingKeysKey(first)
	revs := jwt.RevocationList{}
	for _, cc := range cs {
		if signingKeysKey(cc) != want {
			return nil, errors.New("the servers hold different signing keys in the account JWT: wait for the last push to reach every server")
		}
		for k, v := range cc.Revocations {
			if old, ok := revs[k]; !ok || v > old {
				revs[k] = v
			}
		}
	}
	merged := *first
	merged.Revocations = revs
	return &merged, nil
}

// revokes reports whether any server's copy of the account JWT revokes publicKey.
func (c *clusterClaims) revokes(ctx context.Context, publicKey string) (bool, error) {
	cs, err := c.copies(ctx)
	if err != nil {
		return false, err
	}
	for _, cc := range cs {
		if _, ok := cc.Revocations[publicKey]; ok {
			return true, nil
		}
	}
	return false, nil
}

// Issue signs a user JWT for req.PublicKey at req.Address and records it, or refuses and signs and records nothing. The
// caller's trust is holding the owner group's scoped signing key (cfg.Signer). The error is not nil only when cfg is
// unusable (the Result's outcome is then error); every refusal is a Result.
//
// A call makes its own signing core, so what it reads of the account JWT (a revocation, a changed template) is read
// again for each call. The call never revokes: rotation is an Issue for the new key and a revoke of the old one by the
// caller.
func Issue(ctx context.Context, cfg Config, req Request) (Result, error) {
	if err := cfg.checkIssue(); err != nil {
		return Result{Outcome: OutcomeError, Refusal: &Refusal{Code: "not_configured", Detail: err.Error()}}, err
	}
	res := Result{
		Address:   shaped(req.Address, validAddress(req.Address)),
		PublicKey: shaped(req.PublicKey, nkeys.IsValidPublicUserKey(req.PublicKey)),
	}
	refused := func(rf *Refusal) (Result, error) {
		res.Outcome, res.Refusal = OutcomeRefused, rf
		cfg.log().Warn("credential refused", "code", rf.Code, "address", orInvalid(res.Address), "public_key", orInvalid(res.PublicKey), "purpose", purposeForLog(req))
		return res, nil
	}
	if rf := req.Validate(); rf != nil {
		return refused(rf)
	}
	a := &issue.Authority{ListSet: cfg.ListSet, Signer: cfg.Signer, Record: cfg.Record, Claims: cfg.Claims, AccountPub: cfg.Account, Log: cfg.Log, Now: cfg.Now}
	out, rf := a.Sign(ctx, req.Address, req.Endpoint, req.PublicKey, req.EffectivePurpose())
	if rf != nil {
		return refused(&Refusal{Code: rf.Code, Detail: rf.Msg})
	}
	res.Outcome, res.JWT, res.Tags = OutcomeIssued, out.JWT, out.Tags
	res.Generation, res.IssuedAt = out.Record.Generation, out.Record.IssuedAt
	cfg.log().Info("credential issued", "address", res.Address, "public_key", res.PublicKey, "purpose", req.EffectivePurpose(), "generation", res.Generation)
	return res, nil
}

func validAddress(a string) bool {
	_, _, err := issue.Request{Address: a}.AddressParts()
	return err == nil
}

func orInvalid(s string) string {
	if s == "" {
		return "(invalid)"
	}
	return s
}

// purposeForLog is the request's purpose when it is one of the two, so free text in the field cannot reach a log.
func purposeForLog(r Request) string {
	if p := r.EffectivePurpose(); p == PurposeIssue || p == PurposeRotation {
		return p
	}
	return "(invalid)"
}

// signingKeysKey is a comparable form of the signing keys an account JWT publishes, with their scopes.
func signingKeysKey(c *jwt.AccountClaims) string {
	b, err := json.Marshal(c.SigningKeys)
	if err != nil {
		return "?"
	}
	return string(b)
}
