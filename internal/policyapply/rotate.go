package policyapply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// UserIssuer issues a user JWT for a public key the sender's host made. It signs under the owner group's scoped key.
type UserIssuer interface {
	Issue(ctx context.Context, address, userPub, name string, tags []string) (userJWT string, err error)
}

// Deliverer places a credential where the sender's host picks it up (the wrangler). Success is NOT proof the
// credential is in use: only a connection with the new key is.
type Deliverer interface {
	Deliver(ctx context.Context, address, userJWT string) error
}

// Connections says whether the servers show a connection.
type Connections interface {
	// Connected reports whether any server shows an open connection authorized as the user key userPub.
	Connected(ctx context.Context, userPub string) (bool, error)
}

// GroupUserIssuer is the UserIssuer that signs with GroupSigner: no permissions of its own, tags exactly as given.
type GroupUserIssuer struct {
	Signer GroupSigner
	// Account is the account public key (the issuer account of every credential).
	Account string
}

var _ UserIssuer = GroupUserIssuer{}

// Issue implements UserIssuer. userPub is a PUBLIC user key: mash never receives a seed (D201).
func (g GroupUserIssuer) Issue(_ context.Context, address, userPub, name string, tags []string) (string, error) {
	if g.Signer == nil || !nkeys.IsValidPublicAccountKey(g.Account) {
		return "", errors.New("issuer: no signer or not an account public key")
	}
	if !nkeys.IsValidPublicUserKey(userPub) {
		return "", fmt.Errorf("issue for %s: not a user public key", address)
	}
	i := strings.LastIndex(address, ".")
	if i <= 0 || strings.Count(address, ".") < 2 {
		return "", fmt.Errorf("issue for %q: not a three-part address", address)
	}
	if name == "" {
		return "", fmt.Errorf("issue for %s: no name", address)
	}
	uc := jwt.NewUserClaims(userPub)
	uc.SetScoped(true)
	uc.Name = name
	uc.Tags.Add(tags...)
	if !slices.Equal([]string(uc.Tags), tags) {
		return "", fmt.Errorf("issue for %s: tags are not in the canonical lower-case form", address)
	}
	uc.IssuerAccount = g.Account
	tok, err := g.Signer.SignUser(address[:i], uc)
	if err != nil {
		return "", fmt.Errorf("sign credential for %s: %w", address, errNoSeed(err))
	}
	return tok, nil
}

// Default poll interval of Rotate.
const defaultRotatePoll = 100 * time.Millisecond

// RotateInput is the rotation of one narrowed sender.
type RotateInput struct {
	Address, Name string
	// Tags are the target tags.
	Tags []string
	// NewPub is the public key the sender's host made; mash never sees its seed.
	NewPub     string
	Generation uint64
	Issuer     UserIssuer
	Deliverer  Deliverer
	Conns      Connections
	Record     IssueRecord
	Revoker    Revoker
	// Deadline is when the handover ends: with no connection with NewPub seen by then, the old keys are revoked anyway.
	Deadline time.Time
	// Poll is how often Connected is asked; zero means 100 ms.
	Poll time.Duration
	// Now gives the time compared with Deadline and stamped on the record; nil means time.Now. Waiting between polls
	// always uses the real clock.
	Now func() time.Time
}

// RotationResult is what Rotate did for one sender.
type RotationResult struct {
	Address string
	NewPub  string
	// Connected is true when a connection with NewPub was seen before the deadline.
	Connected bool
	// Revoked are the keys revoked, sorted.
	Revoked []string
	// DeliverError is the text of the delivery error, if any. It does not stop the handover.
	DeliverError string
}

func revocable(e IssueEntry) bool {
	return e.Purpose == PurposeIssue || e.Purpose == PurposeRotation
}

// Rotate issues the narrowed sender's new credential to NewPub, waits until the servers show a connection with that key
// or the deadline, then revokes every issue or rotation key the record holds for the address except NewPub, in one
// Revoker call, and marks each revoked in the record (REQ-014). Probes are not touched; RevokeMinted revokes those.
//
// The order is: issue, record (purpose rotation), deliver, poll Connected, revoke. A NewPub that is not a user public
// key, or is in the record already, is refused before anything is issued (ErrRevokedKey if it was revoked). A delivery
// error is reported in the result and the deadline still governs. At the deadline with no connection the old keys are
// revoked anyway and Connected is false: the caller keeps the change "not applied" for the sender until it connects.
// An error from ctx while waiting is returned and nothing is revoked.
func Rotate(ctx context.Context, in RotateInput) (RotationResult, error) {
	res := RotationResult{Address: in.Address, NewPub: in.NewPub, Revoked: []string{}}
	if in.Issuer == nil || in.Deliverer == nil || in.Conns == nil || in.Record == nil || in.Revoker == nil {
		return res, errors.New("rotate: issuer, deliverer, connections, record and revoker are all required")
	}
	if in.Deadline.IsZero() {
		return res, errors.New("rotate: no deadline")
	}
	if !nkeys.IsValidPublicUserKey(in.NewPub) {
		return res, fmt.Errorf("rotate %s: the new key is not a user public key", in.Address)
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	poll := in.Poll
	if poll <= 0 {
		poll = defaultRotatePoll
	}
	held, err := in.Record.ByAddress(ctx, in.Address)
	if err != nil {
		return res, fmt.Errorf("rotate %s: read issue record: %w", in.Address, err)
	}
	for _, e := range held {
		if e.PublicKey == in.NewPub {
			if e.RevokedAt != nil {
				return res, fmt.Errorf("rotate %s: %w", in.Address, ErrRevokedKey)
			}
			return res, fmt.Errorf("rotate %s: %w", in.Address, ErrDuplicateKey)
		}
	}

	tok, err := in.Issuer.Issue(ctx, in.Address, in.NewPub, in.Name, slices.Clone(in.Tags))
	if err != nil {
		return res, fmt.Errorf("rotate %s: issue: %w", in.Address, errNoSeed(err))
	}
	entry := IssueEntry{
		Address: in.Address, PublicKey: in.NewPub, Purpose: PurposeRotation,
		Tags: slices.Clone(in.Tags), Generation: in.Generation, IssuedAt: now(),
	}
	if uc, derr := jwt.DecodeUserClaims(tok); derr == nil && uc.Expires > 0 {
		entry.Expires = time.Unix(uc.Expires, 0)
	}
	// Record before the credential is handed out.
	if err := in.Record.Add(ctx, entry); err != nil {
		return res, fmt.Errorf("rotate %s: record: %w", in.Address, err)
	}
	if err := in.Deliverer.Deliver(ctx, in.Address, tok); err != nil {
		res.DeliverError = errNoSeed(err).Error()
	}

	for {
		seen, cerr := in.Conns.Connected(ctx, in.NewPub)
		if cerr == nil && seen {
			res.Connected = true
			break
		}
		left := in.Deadline.Sub(now())
		if left <= 0 {
			break
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(min(poll, left)):
		}
	}

	held, err = in.Record.ByAddress(ctx, in.Address)
	if err != nil {
		return res, fmt.Errorf("rotate %s: read issue record: %w", in.Address, err)
	}
	var old []string
	for _, e := range held {
		if revocable(e) && e.PublicKey != in.NewPub && e.RevokedAt == nil {
			old = append(old, e.PublicKey)
		}
	}
	revoked, err := revokeKeys(ctx, in.Revoker, in.Record, old, now)
	res.Revoked = append(res.Revoked, revoked...)
	if err != nil {
		return res, fmt.Errorf("rotate %s: %w", in.Address, err)
	}
	return res, nil
}

// revokeKeys revokes pubs in one Revoker call and marks each in the record. It returns the keys, sorted, once the
// revocation succeeded (each marked at the time it did); with no keys it does nothing.
func revokeKeys(ctx context.Context, revoker Revoker, record IssueRecord, pubs []string, now func() time.Time) ([]string, error) {
	pubs = slices.Compact(slices.Sorted(slices.Values(pubs)))
	if len(pubs) == 0 {
		return nil, nil
	}
	if _, err := revoker.Revoke(ctx, pubs); err != nil {
		return nil, fmt.Errorf("revoke: %w", err)
	}
	var errs []error
	at := now() // after the revocation succeeded
	for _, p := range pubs {
		if err := record.MarkRevoked(ctx, p, at); err != nil {
			errs = append(errs, err)
		}
	}
	return pubs, errors.Join(errs...)
}

// RemoveSender revokes every issue or rotation key the record holds for address, in one Revoker call, and marks each in
// the record (REQ-014: a sender that leaves the list set). It returns the keys revoked, sorted. now nil means
// time.Now.
func RemoveSender(ctx context.Context, address string, record IssueRecord, revoker Revoker, now func() time.Time) ([]string, error) {
	if now == nil {
		now = time.Now
	}
	held, err := record.ByAddress(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("remove %s: read issue record: %w", address, err)
	}
	var keys []string
	for _, e := range held {
		if revocable(e) && e.RevokedAt == nil {
			keys = append(keys, e.PublicKey)
		}
	}
	revoked, err := revokeKeys(ctx, revoker, record, keys, now)
	if err != nil {
		return revoked, fmt.Errorf("remove %s: %w", address, err)
	}
	return revoked, nil
}

// NarrowedSender is one sender whose tags narrowed.
type NarrowedSender struct {
	Address, Name string
	// Tags are the target tags.
	Tags []string
	// NewPub is the public key the sender's host made.
	NewPub string
}

// RotationPlan is the credential work of one change: a rotation for each narrowed sender and a revocation for each
// sender that leaves the list set. Results holds the rotation results after AfterPush ran, in the order of Narrowed.
type RotationPlan struct {
	Narrowed   []NarrowedSender
	Leaving    []string
	Generation uint64
	Issuer     UserIssuer
	Deliverer  Deliverer
	Conns      Connections
	Record     IssueRecord
	Revoker    Revoker
	Deadline   time.Time
	Poll       time.Duration
	Now        func() time.Time

	Results []RotationResult
}

// lockedRevoker serialises Revoke calls: each revocation reads, extends and pushes one account JWT, so two at once
// would lose one of them.
// It keeps the last JWT a Revoke attempted in *last, whether or not the push succeeded: each revocation extends the
// one before, so it is the one the servers hold at the end, or the one an interrupted push may have left on some.
type lockedRevoker struct {
	mu   *sync.Mutex
	r    Revoker
	last *string
	// refused is the first refusal of the BeforeWrite hook: once set, every Revoke fails with it.
	refused *error
}

func (l lockedRevoker) Revoke(ctx context.Context, pubs []string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if *l.refused != nil {
		return "", *l.refused
	}
	if err := CheckBeforeWrite(ctx, FenceRevoke); err != nil {
		*l.refused = err
		return "", err
	}
	tok, err := l.r.Revoke(ctx, pubs)
	if tok != "" {
		*l.last = tok
	}
	return tok, err
}

// RotationsAfterPush returns the RunInput.AfterPush that runs Rotate for each narrowed sender (concurrently, so the
// senders share one deadline; the revocations are serialised) and RemoveSender for each leaving sender. It returns the
// keys revoked, by address (an entry for every narrowed and leaving sender, empty when it had none to revoke; they are
// that sender's whole old-key inventory from the issue record), the senders not seen connected, sorted, and the account
// JWT the last revocation attempted (empty when nothing was attempted; with an error it may have reached only some
// servers): the JWT the servers hold. Every sender is handled
// even when one fails; the errors are joined. Each revocation is preceded by the RunInput.BeforeWrite hook of the run
// (label FenceRevoke, through the ctx AfterPush gets); a refusal stops every later revocation.
func RotationsAfterPush(p *RotationPlan) func(ctx context.Context) (revoked map[string][]string, notConnected []string, finalJWT string, err error) {
	return func(ctx context.Context) (map[string][]string, []string, string, error) {
		var mu sync.Mutex
		var final string
		var refused error
		rv := lockedRevoker{mu: &mu, r: p.Revoker, last: &final, refused: &refused}
		results := make([]RotationResult, len(p.Narrowed))
		errs := make([]error, len(p.Narrowed))
		var wg sync.WaitGroup
		for i, n := range p.Narrowed {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], errs[i] = Rotate(ctx, RotateInput{
					Address: n.Address, Name: n.Name, Tags: n.Tags, NewPub: n.NewPub, Generation: p.Generation,
					Issuer: p.Issuer, Deliverer: p.Deliverer, Conns: p.Conns, Record: p.Record, Revoker: rv,
					Deadline: p.Deadline, Poll: p.Poll, Now: p.Now,
				})
			}()
		}
		wg.Wait()
		p.Results = results
		revoked := map[string][]string{}
		var notConnected []string
		var all []error
		for i, r := range results {
			revoked[r.Address] = append(revoked[r.Address], r.Revoked...)
			if errs[i] != nil {
				all = append(all, errs[i])
			} else if !r.Connected {
				notConnected = append(notConnected, r.Address)
			}
		}
		for _, a := range p.Leaving {
			keys, err := RemoveSender(ctx, a, p.Record, rv, p.Now)
			revoked[a] = append(revoked[a], keys...)
			if err != nil {
				all = append(all, err)
			}
		}
		for a, keys := range revoked {
			revoked[a] = slices.Compact(slices.Sorted(slices.Values(keys)))
		}
		sort.Strings(notConnected)
		return revoked, notConnected, final, errors.Join(all...)
	}
}
