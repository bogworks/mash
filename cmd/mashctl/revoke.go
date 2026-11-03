package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// revokeUsers revokes user keys in the account JWT; a test replaces it.
var revokeUsers = revokeUserKeys

// revokeUserKeys opens the key store (the operator and account keys), connects with the system account credentials and
// revokes each user key with apply.RevokeUsers, printing one line per key. It never prints a seed.
func revokeUserKeys(ctx context.Context, cfg config.Config, env func(string) string, servers int, users []string, stdout io.Writer) error {
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return err
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return err
	}
	if _, err := sysCredsPath("", env, cfg); err != nil {
		return errors.New("no system account credentials: set MASH_SYS_CREDS to a file of system account user creds (a server `mashctl init --local` stood up has them in <server dir>/sys.creds)")
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	res, err := apply.RevokeUsers(ctx, apply.RevokeParams{Keys: ks, Resolver: apply.NATS{Conn: nc, Inventory: servers}, Servers: servers}, users)
	if err != nil {
		return err
	}
	if res.Warning != "" {
		fmt.Fprintf(stdout, "warning: %s\n", res.Warning)
	}
	for _, u := range res.Revoked {
		fmt.Fprintf(stdout, "revoked user key %s...: %d server(s) acknowledged\n", ctl.ShortKey(u), res.Acked)
	}
	for _, u := range res.Already {
		fmt.Fprintf(stdout, "user key %s...: already revoked\n", ctl.ShortKey(u))
	}
	return nil
}

// keyRevoked reports whether the account JWT revokes the user key pub (at any time); a test replaces it.
var keyRevoked = revokedInAccount

// revokedInAccount opens the key store, connects with the system account credentials, reads the account JWT, verifies
// it against the operator trust and reports whether its revocation list names pub. Any failure to establish that is an
// error, never "not revoked".
func revokedInAccount(ctx context.Context, cfg config.Config, env func(string) string, servers int, pub string) (bool, error) {
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return false, err
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return false, err
	}
	akp, acctPub, err := ks.Account()
	if err != nil {
		return false, err
	}
	akp.Wipe()
	trust, err := apply.LoadTrust(ks)
	if err != nil {
		return false, err
	}
	if _, err := sysCredsPath("", env, cfg); err != nil {
		return false, errors.New("no system account credentials: set MASH_SYS_CREDS to a file of system account user creds (a server `mashctl init --local` stood up has them in <server dir>/sys.creds)")
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return false, err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	token, err := apply.NATS{Conn: nc, Inventory: servers}.Lookup(ctx, acctPub)
	if err != nil {
		return false, err
	}
	if token == "" {
		return false, errors.New("no server returned the account JWT")
	}
	claims, err := apply.VerifyAccountJWT(token, acctPub, trust)
	if err != nil {
		return false, err
	}
	_, revoked := claims.Revocations[pub]
	return revoked, nil
}

// parseServers reads MASH_SERVERS the way policy apply does; 0 is none given.
func parseServers(env func(string) string) (int, error) {
	sv := env("MASH_SERVERS")
	if sv == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(sv))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("MASH_SERVERS must be a whole number of servers, 1 or more, got %q", sv)
	}
	return n, nil
}
