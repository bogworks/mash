// Command mash is a messaging mesh for agents: a Claude Code channel server
// (mash serve), a CLI for people and scripts, and admin tooling.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
)

const usage = `mash: a messaging mesh for agents (SAP 0.3 + mail, on NATS JetStream)

  mash serve                                   the Claude Code channel server (started by the plugin)
  mash hook                                    the plugin's hook: shows the session its spooled mail
  mash send <to> [--urgent] [--kind K] [--id KEY] [text... | --file F | -]   mail an endpoint (as MASH_NAME); --urgent
                                               wakes it at once; K is blocker|question|task (wake an idle recipient)
                                               or result|fyi (opt-in; wait for its next turn); default task;
                                               --id KEY makes the send at-most-once: re-running it with the same KEY
                                               within 2 minutes stores nothing new (a timed-out send prints its key); after 2 minutes it is a new message
  mash read <seq>                              print one message from your inbox
  mash inbox [--limit N] [--after SEQ]         recent messages, or the N after SEQ (oldest first)
  mash who [--long]                            endpoints, and who is online (--long: role, host, labels, lease)
  mash prompt <to> <text...>                   a live, signed SAP prompt; prints the reply
  mash status                                  what a session would see (name, config, creds, channel allowlist), before launch;
                                               connects to nothing; the live parts say they need a running session
  mash admin init                              create the directory and lease buckets (or raise their caps to the configured limits)
  mash admin limits [name...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mash admin limits --show                     print the effective limits
  mash admin permissions <name> [--nsc]        the NATS permissions an endpoint user needs
  mash admin provision <name> --user U --account A [--note N] [--role R] [--host H] [--label k=v]...
  mash admin set-meta <name> [--role R] [--host H] [--label k=v]...   (a value of - clears the field; --label k=- clears that label)
  mash admin deprovision <name>
  mash admin list
  mash plugin export <dir>                     write this build's Claude Code plugin and a marketplace for it into <dir>
  mash version

Configuration: config.json {"url","owner"} in $MASH_CONFIG_DIR (default ~/.config/mash, or $XDG_CONFIG_HOME/mash), overridden by
MASH_URL, MASH_OWNER, MASH_NAME, MASH_CREDS, MASH_ADMIN_CREDS, MASH_HOST (see docs/install.md).
State (spool, hook.log): $MASH_STATE_DIR (default $XDG_STATE_HOME/mash, else ~/.local/state/mash).
Slow link: "open_timeout" (default 20s) and "call_timeout" (default 15s), or MASH_OPEN_TIMEOUT and MASH_CALL_TIMEOUT.
`

func main() {
	// A session that is killed (Claude Code exiting, a closed terminal) shuts
	// down cleanly, so serve releases its lease instead of leaving it to expire.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	code := runCtx(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main with its environment injected, for tests.
func run(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runCtx(context.Background(), args, env, stdin, stdout, stderr)
}

// commit is set by -ldflags "-X main.commit=<sha>"; otherwise the VCS revision stamped by go build is used.
var commit string

// readBuildInfo is debug.ReadBuildInfo, replaceable in tests.
var readBuildInfo = debug.ReadBuildInfo

// resolveVersion is the version this binary reports: the one a release build stamped into endpoint.Version by
// ldflag (anything but the default "dev"), else the module version a `go install …@v0.1.0` build carries, else endpoint.DevVersion ("0.0.0-dev").
func resolveVersion(configured string, bi *debug.BuildInfo) string {
	if configured != "" && configured != endpoint.DevVersion {
		return configured
	}
	// Only a clean tag counts. Since Go 1.24 a plain `go build` in a checkout stamps a pseudo-version
	// (v0.0.0-<time>-<sha>, with +dirty for a modified tree) into Main.Version: that is a development build.
	if bi != nil && releaseTag.MatchString(bi.Main.Version) && !pseudoVersion.MatchString(bi.Main.Version) {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return endpoint.DevVersion
}

// pseudoVersion is Go's untagged-commit version tail: -<yyyymmddhhmmss>-<12 hex> (also after a dot, as in v0.1.1-0.<...>).
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// releaseTag is vN.N.N with an optional -prerelease that is not a pseudo-version's timestamp-and-sha tail.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// versionLine is "mash <version> (commit <rev>)". It names the commit so a pinned build can be checked.
func versionLine(version, ldflags string, bi *debug.BuildInfo) string {
	rev := buildRevision(ldflags, bi)
	if rev == "" {
		rev = "unknown"
	}
	return fmt.Sprintf("mash %s (commit %s)", version, rev)
}

// buildRevision is the commit this binary was built from: the ldflag, else the VCS revision go build stamped (with
// "+dirty" for a modified tree), else "".
func buildRevision(ldflags string, bi *debug.BuildInfo) string {
	rev := ldflags
	if rev == "" && bi != nil {
		modified := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if rev != "" && modified {
			rev += "+dirty"
		}
	}
	return rev
}

func runCtx(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Resolved before anything dispatches, so `version`, the MCP server and the SAP service all report one string.
	// Written only when it changes, which a process does once: later commands (and the tests, which run many in
	// one process beside live sessions that read it) only read.
	bi, _ := readBuildInfo()
	if v := resolveVersion(endpoint.CurrentVersion(), bi); v != endpoint.CurrentVersion() {
		endpoint.SetVersion(v)
	}
	if c := buildRevision(commit, bi); c != endpoint.CurrentCommit() {
		endpoint.SetCommit(c)
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "version" {
		bi, _ := readBuildInfo()
		fmt.Fprintln(stdout, versionLine(endpoint.CurrentVersion(), commit, bi))
		return 0
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "plugin" { // before config: an install runs it with no endpoint and no environment
		return pluginCmd(args[1:], stdout, stderr)
	}
	home := env("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cfg, err := config.Load(env, home)
	var adapter host.Adapter
	if args[0] == "hook" || args[0] == "serve" {
		// An unknown host is refused loudly, never read as claude: a hook that ran with the wrong adapter would
		// drain mail into the wrong place.
		var herr error
		if adapter, herr = host.Select(cfg.Host); herr != nil {
			fmt.Fprintln(stderr, herr)
			return 1
		}
	}
	if args[0] == "hook" { // never fails the session: a bad config is a silent no-op
		return hook(cfg, err, adapter, env, stdin, stdout, stderr)
	}
	if args[0] == "status" { // a report: a config error is a line of it
		return statusCmd(cfg, err, env, stdout)
	}
	if err != nil && args[0] != "serve" {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch args[0] {
	case "serve":
		return serve(ctx, cfg, err, adapter, env("MASH_PLUGIN_ROOT"), stdin, stdout, stderr)
	case "send", "read", "inbox", "who", "prompt":
		return cli(cfg, args, stdin, stdout, stderr)
	case "admin":
		return adminCmd(cfg, args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "mash: unknown command %q\n\n%s", args[0], usage)
	return 2
}
