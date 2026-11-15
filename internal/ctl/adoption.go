package ctl

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

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
// and the admin user's key. A next submitter of a planned rotation must be none of them. Any failure
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
