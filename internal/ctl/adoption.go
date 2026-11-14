package ctl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// Guard is a held account lock, taken by a command that changes the account's policy by a path other than the
// list-set path (REQ-016, ADR 0111). The command takes it first, before any write, signing, key creation or file
// change; it holds it through all its writes and releases it on every exit path (defer g.Release()). While it is held
// `mashctl policy adopt` cannot write the marker, and the marker it read cannot change under the command. A nil
// *Guard (no server configured) holds nothing: Held is nil and Release does nothing.
type Guard struct {
	lock       *madmin.AccountLock
	closeAdmin func() // closes the connection the guard opened; nil when the caller's connection is used
}

// guardHeldTimeout bounds one Held check.
const guardHeldTimeout = 10 * time.Second

// Held returns nil while the account lock is still this command's. A guarded command calls it before each write that
// cannot be undone and stops, changing nothing further, when it returns an error: the lock was lost or is about to
// expire, so an adoption may have been written since.
func (g *Guard) Held() error {
	if g == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardHeldTimeout)
	defer cancel()
	return g.lock.Held(ctx)
}

// Release gives up the lock (and closes the connection the guard opened). Call it once, by defer.
func (g *Guard) Release() {
	if g == nil {
		return
	}
	g.lock.Release()
	if g.closeAdmin != nil {
		g.closeAdmin()
	}
}

// GuardAccount takes the account lock over a's connection and then reads the marker: it returns the held lock, or an
// error when the account is adopted, the marker cannot be read, or another policy change or an adoption holds the
// lock for the whole wait. On an error nothing is held. Call it first, before any write; defer the lock's Release.
//
// It fails closed: a marker that cannot be read for any reason other than "there is none" refuses too. what names the
// command as it reads in "mashctl: <what> is refused", for example "add" or "policy apply".
func GuardAccount(ctx context.Context, a *madmin.Admin, what string) (*madmin.AccountLock, error) {
	return guardAccount(ctx, a, "mashctl", what)
}

func guardAccount(ctx context.Context, a *madmin.Admin, prog, what string) (*madmin.AccountLock, error) {
	lk, err := a.LockAccount(ctx)
	if err != nil {
		if errors.Is(err, madmin.ErrAccountBusy) {
			return nil, fmt.Errorf("%s: %s is refused: another policy change or an adoption is in progress; nothing was changed", prog, what)
		}
		return nil, fmt.Errorf("%s: %s is refused: cannot take the account lock: %s; nothing was changed", prog, what, config.Clean(err.Error()))
	}
	if err := refuseIfAdopted(ctx, a, prog, what); err != nil {
		lk.Release()
		return nil, err
	}
	return lk, nil
}

func refuseIfAdopted(ctx context.Context, a *madmin.Admin, prog, what string) error {
	m, found, err := a.Adoption(ctx)
	switch {
	case err != nil:
		return fmt.Errorf("%s: %s is refused: cannot tell whether this account is adopted: %s; nothing was changed", prog, what, config.Clean(err.Error()))
	case found:
		return fmt.Errorf("%s: %s is refused: this account is adopted by an outside system (submitter %s...): list sets come only through the list-set path; nothing was changed",
			prog, what, ShortKey(m.Submitter))
	}
	return nil
}

// GuardAccountFor is GuardAccount for a command that has the configuration and no connection yet: it connects with
// the admin credentials, takes the lock and reads the marker, and keeps the connection until Release. With no server
// configured (cfg.URL empty: a pure edit of the policy file, which no server can be asked about) it returns a nil
// Guard and says nothing. With a server configured, a failure to connect, to lock or to read the marker refuses.
// prog is the command prefix of the error text.
func GuardAccountFor(prog string, cfg config.Config, what string) (*Guard, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	a, _, closeAdmin, err := OpenAdmin(prog, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %s is refused: cannot tell whether this account is adopted: %s; nothing was changed", prog, what, config.Clean(err.Error()))
	}
	// The wait for the lock is its own: the connection's command deadline would cut it short, and the lock is
	// held for the command, which has no deadline of its own here.
	ctx, cancel := context.WithTimeout(context.Background(), guardAcquireTimeout)
	defer cancel()
	lk, err := guardAccount(ctx, a, prog, what)
	if err != nil {
		closeAdmin()
		return nil, err
	}
	return &Guard{lock: lk, closeAdmin: closeAdmin}, nil
}

// guardAcquireTimeout bounds taking the lock and reading the marker: the lock's own wait (30 s) plus the reads.
const guardAcquireTimeout = 60 * time.Second

// AdminUserKey is the public key of the admin user in cfg's admin credentials. Only the JWT is read; the seed is
// never used.
func AdminUserKey(cfg config.Config) (string, error) {
	raw, err := config.ReadCredsFile(cfg.AdminCreds)
	if err != nil {
		return "", fmt.Errorf("admin credentials: %w", err)
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", errors.New("admin credentials hold no readable JWT")
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", errors.New("admin credentials hold no readable user JWT")
	}
	return strings.TrimSpace(uc.Subject), nil
}

// HeldPublicKeys is every public key mash holds for the account of cfg: the key store's operator, system, account
// and zone keys and the admin user's key. A next submitter of a planned rotation must be none of them. Any failure
// to read the key store or the admin credentials is an error, so a caller refuses (fail closed).
func HeldPublicKeys(cfg config.Config, ks *ctlkeys.Authority) ([]string, error) {
	_, held, err := ks.HeldKeys()
	if err != nil {
		return nil, err
	}
	by, err := AdminUserKey(cfg)
	if err != nil {
		return nil, err
	}
	held[by] = "the admin user's key"
	out := make([]string, 0, len(held))
	for k := range held {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
