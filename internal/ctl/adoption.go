package ctl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nats-io/jwt/v2"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
)

// RefuseIfAdopted returns an error unless a's account is known not to be adopted. It is the guard of every command
// that would change the account's policy by a path other than the list-set path (REQ-016): call it first, before any
// write, signing, key creation or file change. The error says what was refused and that nothing was changed.
//
// It fails closed: a marker that cannot be read for any reason other than "there is none" refuses too. what names the
// command as it reads in "mashctl: <what> is refused", for example "add" or "policy apply".
func RefuseIfAdopted(ctx context.Context, a *madmin.Admin, what string) error {
	return refuseIfAdopted(ctx, a, "mashctl", what)
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

// RefuseIfAdoptedFor is RefuseIfAdopted for a command that has the configuration and no connection yet: it connects
// with the admin credentials, asks, and disconnects. With no server configured (cfg.URL empty: a pure edit of the
// policy file, which no server can be asked about) it allows and says nothing. With a server configured, a failure to
// connect or to read the marker refuses. prog is the command prefix of the error text.
func RefuseIfAdoptedFor(prog string, cfg config.Config, what string) error {
	if cfg.URL == "" {
		return nil
	}
	a, ctx, closeAdmin, err := OpenAdmin(prog, cfg)
	if err != nil {
		return fmt.Errorf("%s: %s is refused: cannot tell whether this account is adopted: %s; nothing was changed", prog, what, config.Clean(err.Error()))
	}
	defer closeAdmin()
	return refuseIfAdopted(ctx, a, prog, what)
}

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
