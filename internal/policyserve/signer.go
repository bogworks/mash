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

	"github.com/bogworks/mash/internal/admin"
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
	// AdminUser and AdminIssuer are the public key and the `iss` of the admin credentials' user JWT, and ExemptIssuers
	// reads the issuer of every connection of every connected exempt user (the adoption marker's exempt set, CONNZ's
	// issuer keys): user public key -> issuers. All optional, and only together: when AdminIssuer is "" or ExemptIssuers
	// is nil, Sign refuses an account JWT that carries a signing key that is not an owner group key of the key store. When
	// they are set, Sign drops such a key instead, provided admin.CheckIssuersKept shows that the admin user and every
	// connected exempt user stay signed by a key that is kept; ExemptIssuers returns an error when it cannot say (an
	// incomplete scan), and that refuses too.
	AdminUser, AdminIssuer string
	ExemptIssuers          func(ctx context.Context) (map[string][]string, error)

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

// canDrop is true when the issuer may drop the non-mash signing keys of an account JWT (after the issuer check): the
// admin issuer and the source of the exempt users' issuers are set, and generation, the one being signed, is the
// first (ADR 0124): a non-mash key that appears later is refused, not dropped by a routine update.
func (s *Issuer) canDrop(generation uint64) bool {
	return generation == 1 && s.AdminIssuer != "" && s.ExemptIssuers != nil
}

// errNonMashKey is the refusal of a non-mash signing key by an Issuer that may not drop it.
func errNonMashKey(k string) error {
	return fmt.Errorf("the account JWT carries a signing key that is not an owner group key of the key store (%.8s...): remove it before a list set can be applied", k)
}

// keptFrom splits the signing keys of base. kept is the other ranges' group keys of the key store (group -> public
// key) that base also carries: the ones the next account JWT retains, and the ones the generation read must find. A
// key store key of another range that base does not carry is in neither. mine are this range's keys in base, which are
// rebuilt from the list set. nonMash are the signing keys of base that are not a scoped owner group key of the key
// store: Sign drops them after the issuer check, or refuses when it may not.
func (s *Issuer) keptFrom(base *jwt.AccountClaims) (kept map[string]string, mine, nonMash []string, err error) {
	ours, err := s.Keys.GroupPublics()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("owner group keys: %w", err)
	}
	if s.Range == "" {
		return nil, nil, nil, errors.New("the issuer has no range: cannot tell which signing keys are this range's")
	}
	groupOf := map[string]string{} // public key -> owner group
	for g, pub := range ours {
		groupOf[pub] = g
	}
	kept = map[string]string{}
	for _, k := range base.SigningKeys.Keys() {
		sc, _ := base.SigningKeys.GetScope(k)
		g, held := groupOf[k]
		if _, scoped := sc.(*jwt.UserScope); !scoped || !held {
			nonMash = append(nonMash, k)
			continue
		}
		if strings.HasPrefix(g, s.Range+".") {
			mine = append(mine, k)
		} else {
			kept[g] = k
		}
	}
	return kept, mine, nonMash, nil
}

// Kept is the scoped signing keys the next account JWT keeps beside this range's, as Sign retains them: the owner
// group keys of other ranges that the account JWT the servers hold now carries. It maps group to public key and is
// never nil. A non-mash signing key is an error unless the issuer may drop it (see AdminIssuer): only when generation,
// the one about to be signed, is the first.
func (s *Issuer) Kept(ctx context.Context, generation uint64) (map[string]string, error) {
	base, err := s.current(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the account JWT: %w", err)
	}
	kept, _, nonMash, err := s.keptFrom(base)
	if err != nil {
		return nil, err
	}
	if len(nonMash) > 0 && !s.canDrop(generation) {
		return nil, errNonMashKey(nonMash[0])
	}
	return kept, nil
}

// Sign builds the account JWT of a generation: the account JWT the servers hold now, with the scoped signing keys
// replaced by one per owner group of c (the group's key from the key store, role = group, the template compiled from c).
// The other ranges' signing keys it carries are kept (see Kept). Everything else in it (limits, exports, revocations,
// the account's own settings) is kept. A signing key that is not an owner group key of the key store is not dropped
// silently: without AdminIssuer and ExemptIssuers the apply is refused; with them it is dropped once admin.CheckIssuersKept
// shows that the admin user and the connected exempt users stay signed by a kept key (the account key, an owner group
// key of another range that the JWT carries, or a key of the list set), and refused with that check's error otherwise.
// Only the first generation's apply drops (ADR 0124): from the second on, a non-mash key is refused as without the
// fields. It implements policyapply.Signer.
func (s *Issuer) Sign(rec listset.Record, c *listset.Compiled) (string, error) {
	base, err := s.current(s.Ctx)
	if err != nil {
		return "", fmt.Errorf("read the account JWT: %w", err)
	}
	kept, mine, nonMash, err := s.keptFrom(base)
	if err != nil {
		return "", err
	}
	if len(nonMash) > 0 && !s.canDrop(rec.Generation) {
		return "", errNonMashKey(nonMash[0])
	}
	pubs := make([]string, len(c.Templates))
	for i, tpl := range c.Templates {
		kp, err := s.Keys.GroupKey(tpl.Group, false)
		if err != nil {
			return "", fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
		pubs[i], err = kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return "", fmt.Errorf("owner group %s: %w", tpl.Group, err)
		}
	}
	if len(nonMash) > 0 {
		// The keys that stay: the account's own key (CheckIssuersKept counts it), the other ranges' keys the JWT
		// carries and the target's. This range's keys that the list set no longer names go, so a user under one is not kept.
		stay := map[string]bool{}
		for _, k := range kept {
			stay[k] = true
		}
		for _, p := range pubs {
			stay[p] = true
		}
		exempt, err := s.ExemptIssuers(s.Ctx)
		if err != nil {
			return "", fmt.Errorf("the non-mash signing key is not dropped: the connected exempt users' issuers: %w", err)
		}
		if err := admin.CheckIssuersKept(s.Account, s.AdminUser, s.AdminIssuer, exempt, stay); err != nil {
			return "", fmt.Errorf("the non-mash signing key is not dropped: %w", err)
		}
		for _, k := range nonMash {
			delete(base.SigningKeys, k)
		}
	}
	for _, k := range mine {
		delete(base.SigningKeys, k) // this range's: rebuilt below from the list set
	}
	for i, tpl := range c.Templates {
		pub := pubs[i]
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
	for _, k := range revoked {
		ac.Revoke(k)
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
