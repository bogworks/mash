package policyserve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// clockTick is how often waitAfter looks at the clock.
const clockTick = 20 * time.Millisecond

// Keys is the part of the key store the service uses.
type Keys interface {
	Operator() (nkeys.KeyPair, string, error)
	GroupKey(group string, create bool) (nkeys.KeyPair, error)
	GroupPublics() (map[string]string, error)
}

var _ Keys = (*ctlkeys.Authority)(nil)

// Issuer signs and pushes account JWTs for one account with the operator key. It is the Signer of an apply and the
// Revoker the apply and the probe cleanup use. One Issuer is used for one document: Ctx bounds the reads and waits of Sign and SignPrevious (Revoke takes its own).
//
// A server keeps the newer of two JWTs and an issue time has a resolution of one second, so every JWT it makes is issued
// in a second later than the previous JWT of the account (waitAfter).
type Issuer struct {
	Ctx     context.Context
	Keys    Keys
	Cluster policyapply.Cluster
	// Resolver reads the account JWT from every server.
	Resolver apply.Resolver
	Account  string
	// Range is the range this service applies list sets for. Only the signing keys of its owner groups ("range.owner")
	// are replaced or dropped; the keys of other ranges' groups are kept as they are.
	Range string
	Trust apply.Trust
	// Servers is the declared server count (0 none given).
	Servers int
	// Now gives the time; nil means time.Now.
	Now func() time.Time

	mu   sync.Mutex
	last int64 // the issue time of the JWT this Issuer made last
}

var (
	_ policyapply.Signer  = (*Issuer)(nil)
	_ policyapply.Revoker = (*Issuer)(nil)
)

func (s *Issuer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// waitAfter waits until the clock is past every issue time in floor and past the last JWT this Issuer made.
func (s *Issuer) waitAfter(ctx context.Context, floor int64) error {
	s.mu.Lock()
	floor = max(floor, s.last)
	s.mu.Unlock()
	for s.now().Unix() <= floor {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(clockTick):
		}
	}
	return nil
}

// encode signs ac with the operator key.
func (s *Issuer) encode(ac *jwt.AccountClaims) (string, error) {
	kp, _, err := s.Keys.Operator()
	if err != nil {
		return "", fmt.Errorf("the operator key: %w", err)
	}
	defer kp.Wipe()
	tok, err := ac.Encode(kp)
	if err != nil {
		return "", errors.New("cannot sign the account JWT")
	}
	s.mu.Lock()
	s.last = max(s.last, ac.IssuedAt)
	s.mu.Unlock()
	return tok, nil
}

// current reads the newest account JWT the servers hold, verified against the operator keys; it needs every server.
func (s *Issuer) current(ctx context.Context) (*jwt.AccountClaims, error) {
	return apply.CurrentAccountClaims(ctx, s.Resolver, s.Account, s.Trust, s.Servers)
}

// Sign builds the account JWT of a generation: the account JWT the servers hold now, with the scoped signing keys
// replaced by one per owner group of c (the group's key from the key store, role = group, the template compiled from c).
// Everything else in it (limits, exports, revocations, the account's own settings) is kept. A signing key it does not
// manage is not dropped silently: the apply is refused. It implements policyapply.Signer.
func (s *Issuer) Sign(_ listset.Record, c *listset.Compiled) (string, error) {
	base, err := s.current(s.Ctx)
	if err != nil {
		return "", fmt.Errorf("read the account JWT: %w", err)
	}
	ours, err := s.Keys.GroupPublics()
	if err != nil {
		return "", fmt.Errorf("owner group keys: %w", err)
	}
	if s.Range == "" {
		return "", errors.New("the issuer has no range: cannot tell which signing keys are this range's")
	}
	groupOf := map[string]string{} // public key -> owner group
	for g, pub := range ours {
		groupOf[pub] = g
	}
	for _, k := range base.SigningKeys.Keys() {
		sc, _ := base.SigningKeys.GetScope(k)
		g, held := groupOf[k]
		if _, scoped := sc.(*jwt.UserScope); !scoped || !held {
			return "", fmt.Errorf("the account JWT carries a signing key that is not an owner group key of the key store (%.8s...): remove it before a list set can be applied", k)
		}
		if strings.HasPrefix(g, s.Range+".") {
			delete(base.SigningKeys, k) // this range's: rebuilt below from the list set
		}
	}
	for _, tpl := range c.Templates {
		kp, err := s.Keys.GroupKey(tpl.Group, false)
		if err != nil {
			return "", fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
		pub, err := kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return "", fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
		sc := jwt.NewUserScope()
		sc.Key = pub
		sc.Role = tpl.Group
		sc.Template.Permissions = tpl.Permissions
		base.SigningKeys.AddScopedSigner(sc)
	}
	if err := s.waitAfter(s.Ctx, base.IssuedAt); err != nil {
		return "", err
	}
	return s.encode(base)
}

// SignPrevious re-signs the previous generation's account JWT with every key in revoked also revoked, and nothing
// else changed. It reads no server. It implements policyapply.Signer.
func (s *Issuer) SignPrevious(prev policyapply.PreviousClaims, revoked []string) (string, error) {
	ac, err := jwt.DecodeAccountClaims(prev.JWT)
	if err != nil {
		return "", fmt.Errorf("the previous account JWT does not decode: %w", err)
	}
	if ac.Subject != s.Account {
		return "", errors.New("the previous account JWT is for another account: it is not re-signed")
	}
	okp, opub, err := s.Keys.Operator()
	if err != nil {
		return "", fmt.Errorf("the operator key: %w", err)
	}
	okp.Wipe()
	if ac.Issuer != opub {
		return "", errors.New("the previous account JWT was not issued by the operator key: it is not re-signed")
	}
	now := time.Now()
	for _, k := range revoked {
		if t, ok := prev.RevokeAt[k]; ok && t.After(now) {
			ac.RevokeAt(k, t)
		} else {
			ac.Revoke(k)
		}
	}
	if err := s.waitAfter(s.Ctx, ac.IssuedAt); err != nil {
		return "", err
	}
	return s.encode(ac)
}

// Revoke adds the user keys to the revocation list of the account JWT the servers hold, signs it with the operator
// key and pushes it to every server; every server of the complete set must acknowledge it. It returns the JWT pushed.
// It implements policyapply.Revoker.
func (s *Issuer) Revoke(ctx context.Context, pubs []string) (string, error) {
	for _, p := range pubs {
		if !nkeys.IsValidPublicUserKey(p) {
			return "", errors.New("revoke: not a user public key")
		}
	}
	base, err := s.current(ctx)
	if err != nil {
		return "", fmt.Errorf("revoke: read the account JWT: %w", err)
	}
	for _, p := range pubs {
		base.Revoke(p)
	}
	if err := s.waitAfter(ctx, base.IssuedAt); err != nil {
		return "", err
	}
	tok, err := s.encode(base)
	if err != nil {
		return "", err
	}
	set, err := s.Cluster.Servers(ctx)
	if err != nil {
		return "", fmt.Errorf("revoke: server set: %w", err)
	}
	if !set.Complete(s.Servers) {
		return "", fmt.Errorf("revoke: %s", set.Why(s.Servers))
	}
	acks, err := s.Cluster.Push(ctx, tok, set.Names)
	if err != nil {
		return "", fmt.Errorf("revoke: push: %w", err)
	}
	got := map[string]bool{}
	for _, a := range acks {
		if !a.OK() {
			return "", fmt.Errorf("revoke: server %.64s refused the update (code %d)", a.Server, a.Code)
		}
		got[a.Server] = true
	}
	for _, n := range set.Names {
		if !got[n] {
			return "", fmt.Errorf("revoke: server %.64s did not acknowledge the update", n)
		}
	}
	return tok, nil
}

// GroupSigner signs user JWTs with the owner groups' scoped keys of the key store (policyapply.GroupSigner). It never
// creates a key.
type GroupSigner struct{ Keys Keys }

var _ policyapply.GroupSigner = GroupSigner{}

// SignUser implements policyapply.GroupSigner.
func (g GroupSigner) SignUser(group string, uc *jwt.UserClaims) (string, error) {
	kp, err := g.Keys.GroupKey(group, false)
	if err != nil {
		return "", err
	}
	defer kp.Wipe()
	return uc.Encode(kp)
}
