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

// revokedInAccount opens the key store, connects with the system account credentials and reports, with
// apply.KeyRevoked, whether the account JWT of any server revokes pub. It reads every server's copy, verifies each
// against the operator trust and refuses on an incomplete server set or a silent server. Any failure to establish the
// answer is an error, never "not revoked".
func revokedInAccount(ctx context.Context, cfg config.Config, env func(string) string, servers int, pub string) (bool, error) {
	return readAccount(ctx, cfg, env, servers, func(ctx context.Context, p apply.RevokeParams) (bool, error) {
		return apply.KeyRevoked(ctx, p, pub)
	})
}

// signerPublished reports whether the account JWT on every server lists signerPub as a scoped signing key with role
// role; a test replaces it.
var signerPublished = signerInAccount

// signerInAccount is apply.SignerPublished over the system account connection, with the same refusals as
// revokedInAccount: any failure to establish the answer is an error, never "published".
func signerInAccount(ctx context.Context, cfg config.Config, env func(string) string, servers int, signerPub, role string) (bool, error) {
	return readAccount(ctx, cfg, env, servers, func(ctx context.Context, p apply.RevokeParams) (bool, error) {
		return apply.SignerPublished(ctx, p, signerPub, role)
	})
}

// readAccount opens the key store, connects with the system account credentials and runs read over every server.
func readAccount(ctx context.Context, cfg config.Config, env func(string) string, servers int, read func(context.Context, apply.RevokeParams) (bool, error)) (bool, error) {
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return false, err
	}
	ks, err := ctlkeys.Open(keyDir)
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
	return read(ctx, apply.RevokeParams{Keys: ks, Resolver: apply.NATS{Conn: nc, Inventory: servers}, Servers: servers})
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
