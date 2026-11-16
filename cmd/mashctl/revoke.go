package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// noSysCredsMsg is the refusal when no system account credentials are configured.
const noSysCredsMsg = "no system account credentials: pass --sys-creds FILE or set MASH_SYS_CREDS to a file of system account user creds (a server `mashctl init --local` stood up has them in <server dir>/sys.creds)"

// revokeUsers revokes user keys in the account JWT; a test replaces it.
var revokeUsers = revokeUserKeys

// revokeReport is what a revocation learned about the servers beyond its error: how many acknowledged against how
// many are declared, the write quorum it needed, and which declared servers have not acknowledged (they converge when
// they rejoin, ADR 0142). A caller that wants it puts one in the context with withRevokeReport.
type revokeReport struct {
	Quorum, Declared, Acked int
	Pending                 []string
}

type revokeReportKey struct{}

func withRevokeReport(ctx context.Context) (context.Context, *revokeReport) {
	r := &revokeReport{}
	return context.WithValue(ctx, revokeReportKey{}, r), r
}

// pendingLine is the one line that names the servers a revocation has not reached yet. It is plain text for people and
// starts with pending_servers: so a script can find it.
func pendingLine(pending []string) string {
	return fmt.Sprintf("pending_servers: %s (they did not acknowledge the revocation and will converge when they rejoin: the account resolver gives them the account JWT then; `mashctl audit` reports revocation_pending until they hold it)", strings.Join(pending, ", "))
}

// withDeclared is servers, or the config's declared server count when the environment gave none.
func withDeclared(cfg config.Config, servers int) int {
	if servers == 0 {
		return cfg.Servers
	}
	return servers
}

// revokeUserKeys opens the key store (the operator and account keys), connects with the system account credentials and
// revokes each user key with apply.RevokeUsers, printing one line per key. It never prints a seed. A non-nil now is the
// revocation time; nil means the current time.
func revokeUserKeys(ctx context.Context, cfg config.Config, env func(string) string, servers int, users []string, stdout io.Writer, now func() time.Time) error {
	servers = withDeclared(cfg, servers)
	quorum, err := config.WriteQuorum(cfg.WriteQuorum, servers)
	if err != nil {
		return err
	}
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return err
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return err
	}
	if _, err := sysCredsPath("", env, cfg); err != nil {
		return errors.New(noSysCredsMsg)
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	res, err := apply.RevokeUsers(ctx, apply.RevokeParams{Keys: ks, Resolver: apply.NATS{Conn: nc, Inventory: servers}, Servers: servers, Quorum: quorum, Now: now}, users)
	if err != nil {
		return err
	}
	if rep, ok := ctx.Value(revokeReportKey{}).(*revokeReport); ok {
		rep.Quorum, rep.Declared, rep.Acked, rep.Pending = res.Quorum, res.Declared, res.Acked, res.Pending
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
	if len(res.Pending) > 0 {
		fmt.Fprintln(stdout, pendingLine(res.Pending))
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

// readAccount opens the key store, connects with the system account credentials and runs read over every server.
func readAccount(ctx context.Context, cfg config.Config, env func(string) string, servers int, read func(context.Context, apply.RevokeParams) (bool, error)) (bool, error) {
	servers = withDeclared(cfg, servers)
	quorum, err := config.WriteQuorum(cfg.WriteQuorum, servers)
	if err != nil {
		return false, err
	}
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return false, err
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return false, err
	}
	if _, err := sysCredsPath("", env, cfg); err != nil {
		return false, errors.New(noSysCredsMsg)
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return false, err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	return read(ctx, apply.RevokeParams{Keys: ks, Resolver: apply.NATS{Conn: nc, Inventory: servers}, Servers: servers, Quorum: quorum})
}

// parseServers reads MASH_SERVERS, the server count the revocation check waits for; 0 is none given.
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

// refuseFutureIssued refuses to revoke a user whose current JWT claims it was issued after now, the time the
// revocation would carry: NATS honours a revocation only for JWTs issued at or before it, so that JWT would stay valid.
// Nothing has been changed when it returns an error.
func refuseFutureIssued(name string, claims *jwt.UserClaims, now time.Time) error {
	if claims == nil {
		return nil
	}
	if iat := time.Unix(claims.IssuedAt, 0).UTC(); iat.After(now) {
		return fmt.Errorf("%s's current creds JWT claims it was issued in the future (%s): the revocation would not cover it; fix this machine's clock (or the machine that minted it) and run again; nothing was changed", name, iat.Format(time.RFC3339))
	}
	return nil
}

// credsIssuedClaims returns the claims of the creds file at path for user. A missing file is no JWT to check (nil,
// nil). A file that exists but cannot be read, parsed or decoded is an error: the check cannot be made, so the caller
// must refuse rather than revoke blind. A symlink is followed and its target checked; a dangling one is missing. The error never carries file content. A file that decodes but names another
// subject is deliberately skipped (nil, nil): it is not this user's JWT, so its issue time says nothing about the
// revocation.
func credsIssuedClaims(path, user string) (*jwt.UserClaims, error) {
	// Stat follows a symlink (creds on a secret mount are links), so the target is what is checked. A dangling link
	// has no JWT. A target that is not a regular file is refused here, before any open that could block on it.
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("creds file %s exists but cannot be read: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("creds file %s exists but is not a regular file", path)
	}
	raw, err := readCreds(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil // removed between the two calls
		}
		return nil, fmt.Errorf("creds file %s exists but cannot be read: %w", path, err)
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return nil, fmt.Errorf("creds file %s exists but holds no readable JWT", path)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return nil, fmt.Errorf("creds file %s exists but its JWT cannot be decoded", path)
	}
	if uc.Subject != user {
		return nil, nil
	}
	return uc, nil
}
