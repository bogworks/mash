// Package enroll signs the user credential of an endpoint from a PUBLIC key the endpoint made, so no private key
// reaches the central host, and records the endpoint (issue record, directory record, inbox). The same-host path makes
// the key itself and returns the seed for the caller to write 0600.
//
// Create orders its writes so that a run that is killed part-way is finished by running it again (NFR-001): sign,
// issue record, then the directory record and inbox last.
package enroll

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Deps are what enrollment needs.
type Deps struct {
	Keys    mint.Keys
	Admin   *admin.Admin
	Account string // account public key
	// Revoke revokes one user key on every server; nil revokes nothing.
	Revoke func(ctx context.Context, userPub string) error
}

// Refusal is a refusal a caller reports by its Code: "public_key_invalid", "public_key_issued" or "key_retired".
type Refusal struct{ Code, Msg string }

func (r *Refusal) Error() string { return r.Msg }

// Request is one enrollment. PublicKey "" is the same-host path: enroll makes the key.
type Request struct {
	Addr      sap.Address
	PublicKey string
	Note      string
	Meta      admin.Meta
}

// Result is what enrollment made. Seed is set only on the same-host path: it is a secret, callers write it 0600 and
// never print it.
type Result struct {
	Addr      sap.Address
	PublicKey string
	JWT       string
	Seed      []byte
	Reissued  bool
}

func invalidKey() *Refusal {
	return &Refusal{Code: "public_key_invalid", Msg: "the public key is not a NATS user public key (U, then 55 base32 characters)"}
}

// SignUser signs the user credential of addr for publicKey. It accepts only a user public key, never a seed or an
// account or operator key.
func (d Deps) SignUser(addr sap.Address, publicKey string) (string, error) {
	if !nkeys.IsValidPublicUserKey(publicKey) {
		return "", invalidKey()
	}
	if err := addr.Validate(); err != nil {
		return "", fmt.Errorf("enroll: %w", err)
	}
	tok, _, err := mint.SignUser(d.Keys, addr, publicKey)
	return tok, err
}

// key returns the key to use: the given public key, or a fresh one with its seed (same-host).
func key(publicKey string) (pub string, seed []byte, err error) {
	if publicKey != "" {
		if !nkeys.IsValidPublicUserKey(publicKey) {
			return "", nil, invalidKey()
		}
		return publicKey, nil, nil
	}
	kp, err := nkeys.CreateUser()
	if err != nil {
		return "", nil, fmt.Errorf("enroll: make user key: %w", err)
	}
	defer kp.Wipe()
	if pub, err = kp.PublicKey(); err != nil {
		return "", nil, err
	}
	s, err := kp.Seed()
	if err != nil {
		return "", nil, errors.New("enroll: user seed unavailable")
	}
	return pub, append([]byte(nil), s...), nil
}

func short(k string) string { return fmt.Sprintf("%.8s", k) }

// record writes the issue record of publicKey for addr and maps its refusals.
func (d Deps) record(ctx context.Context, addr sap.Address, publicKey, purpose string) error {
	err := d.Admin.RecordIssuedChecked(ctx, store.Issued{PublicKey: publicKey, Address: addr.Flat(), Owner: addr.Owner,
		Kind: store.IssuedEndpoint, Tags: []string{}, Purpose: purpose})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrKeyBoundElsewhere):
		return &Refusal{Code: "public_key_issued", Msg: fmt.Sprintf("the public key %s... was already issued to another address", short(publicKey))}
	case errors.Is(err, store.ErrKeyRevoked):
		return fmt.Errorf("enroll: the public key %s... has been revoked; make a new key: %w", short(publicKey), err)
	}
	return err
}

// Create enrolls the endpoint at r.Addr. A run that stopped part-way is finished by running it again with the same
// public key; the JWT is signed again. The same-host path makes a new key on every run, so a rerun after a kill leaves
// the earlier key's issue record unused (and unrevoked until retire).
func Create(ctx context.Context, d Deps, r Request) (Result, error) {
	if err := r.Addr.Validate(); err != nil {
		return Result{}, fmt.Errorf("enroll: %w", err)
	}
	pub, seed, err := key(r.PublicKey)
	if err != nil {
		return Result{}, err
	}
	fail := func(err error) (Result, error) { clear(seed); return Result{}, err }
	burned, err := d.Admin.IsBurned(ctx, r.Addr)
	if err != nil {
		return fail(err)
	}
	if burned {
		return fail(&Refusal{Code: "key_retired", Msg: fmt.Sprintf("endpoint %s was retired and its address is never reassigned", r.Addr)})
	}
	tok, err := d.SignUser(r.Addr, pub)
	if err != nil {
		return fail(err)
	}
	if err := d.record(ctx, r.Addr, pub, store.PurposeIssue); err != nil {
		return fail(err)
	}
	switch user, _, err := d.Admin.UserOf(ctx, r.Addr); {
	case errors.Is(err, admin.ErrNoRecord):
		if _, err := d.Admin.Provision(ctx, r.Addr, pub, d.Account, r.Note, r.Meta); err != nil {
			return fail(err)
		}
	case err != nil:
		return fail(err)
	case user != pub:
		return fail(fmt.Errorf("enroll: endpoint %s already exists with another user key; use reissue", r.Addr))
	}
	return Result{Addr: r.Addr, PublicKey: pub, JWT: tok, Seed: seed}, nil
}

// Reissue gives the existing endpoint at addr a new key (publicKey, or one made here when ""): the old key stays valid
// until the directory record names the new one, then it is revoked. A killed run is finished by running it again; if
// the kill came after the record changed, the old key is no longer known to a rerun that names the new key, so rerun
// without a key (a further key is made and the then-current one is revoked) or retire the endpoint.
func Reissue(ctx context.Context, d Deps, addr sap.Address, publicKey string) (Result, error) {
	if err := addr.Validate(); err != nil {
		return Result{}, fmt.Errorf("enroll: %w", err)
	}
	pub, seed, err := key(publicKey)
	if err != nil {
		return Result{}, err
	}
	fail := func(err error) (Result, error) { clear(seed); return Result{}, err }
	old, _, err := d.Admin.UserOf(ctx, addr)
	if errors.Is(err, admin.ErrNoRecord) {
		return fail(fmt.Errorf("enroll: endpoint %s does not exist", addr))
	}
	if err != nil {
		return fail(err)
	}
	tok, err := d.SignUser(addr, pub)
	if err != nil {
		return fail(err)
	}
	if err := d.record(ctx, addr, pub, store.PurposeRotation); err != nil {
		return fail(err)
	}
	// The old key is revoked before the directory record moves to the new one: a run killed between the two leaves the
	// old key revoked and the record on it, and running the same command again moves the record (revoking again is
	// safe). The other order would leave the old key valid with nothing that names it.
	if old != pub {
		if d.Revoke != nil {
			if err := d.Revoke(ctx, old); err != nil {
				return fail(fmt.Errorf("enroll: the old key %s... was not revoked, nothing else was changed: %w", short(old), err))
			}
		}
		if err := d.Admin.MarkRevoked(ctx, old, time.Now()); err != nil && !errors.Is(err, admin.ErrNotRecorded) {
			return fail(err)
		}
	}
	if _, err := d.Admin.SetUser(ctx, addr, d.Account, old, pub); err != nil {
		return fail(err)
	}
	return Result{Addr: addr, PublicKey: pub, JWT: tok, Seed: seed, Reissued: true}, nil
}
