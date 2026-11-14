package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/policy"
)

// maxControlLineEnv is the environment variable that gives the connect-line limit of the servers when they cannot
// be asked, the same as `policy apply --max-control-line`.
const maxControlLineEnv = "MASH_MAX_CONTROL_LINE"

// parseMaxControlLine reads --max-control-line (flagVal, "" when not given) or MASH_MAX_CONTROL_LINE: a whole number
// of bytes. 0 means none was given. It is the operator's word for every server that did not report its own limit,
// and a cap on one that did (apply.LearnLimits).
func parseMaxControlLine(flagVal string, env func(string) string) (int, error) {
	v, src := flagVal, "--max-control-line"
	if v == "" {
		v, src = env(maxControlLineEnv), maxControlLineEnv
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a whole number of bytes, 1 or more, got %q", src, v)
	}
	return n, nil
}

// credentialLimits is the limits a command that signs users (add, reissue) measures against. With system account
// credentials it asks every server for its own, as `policy apply` does, and refuses when a server cannot be asked
// and no --max-control-line says what it runs. Without system credentials there is nobody to ask, so it measures
// against the default limit and says so on stderr ("default server limit"): only `policy apply`, which has system
// access, is the every-server gate. what names the command in the note.
func credentialLimits(what string, cfg config.Config, env func(string) string, stderr io.Writer) ([]policy.ServerLimits, error) {
	override, err := parseMaxControlLine("", env)
	if err != nil {
		return nil, err
	}
	if _, serr := sysCredsPath("", env, cfg); serr != nil {
		if override > 0 {
			return apply.LearnLimits(nil, nil, override)
		}
		fmt.Fprintf(stderr, "mashctl: %s: no system account credentials to ask the servers their limits: measured against the %s of %d bytes; only `mashctl policy apply` (which has system access) measures against every server's own limit, so run it before relying on this credential\n",
			what, policy.DefaultServer, policy.DefaultMaxControlLine)
		return policy.DefaultServerLimits(), nil
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return nil, fmt.Errorf("cannot ask the servers their limits: %v; set %s to the max_control_line of your servers to measure without asking", err, maxControlLineEnv)
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	defer cancel()
	inventory, err := parseServers(env)
	if err != nil {
		return nil, err
	}
	return limitsOf(ctx, apply.NATS{Conn: nc}, inventory, override)
}

// limitsOf asks the servers r reaches for their limits and refuses unless the set that answered is complete
// (apply.CheckLimitSet): a silent member's smaller limit would be missing from the measure.
func limitsOf(ctx context.Context, r apply.Resolver, inventory, override int) ([]policy.ServerLimits, error) {
	set, err := r.Servers(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot ask the servers their limits: %v", err)
	}
	if err := apply.CheckLimitSet(set, inventory); err != nil {
		return nil, err
	}
	return apply.Limits(ctx, r, set, override)
}
