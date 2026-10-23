// Command mash is a messaging mesh for agents: a Claude Code channel server
// (mash serve), a CLI for people and scripts, and admin tooling.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/endpoint"
)

const usage = `mash: a messaging mesh for agents (SAP 0.3 + mail, on NATS JetStream)

  mash serve                                   the Claude Code channel server (started by the plugin)
  mash send <to> [text... | --file F | -]      mail an endpoint (as MASH_NAME)
  mash read <seq>                              print one message from your inbox
  mash inbox [--limit N]                       recent messages in your inbox
  mash who                                     endpoints, and who is online
  mash prompt <to> <text...>                   a live, signed SAP prompt; prints the reply
  mash admin init                              create the directory and lease buckets
  mash admin permissions <name> [--nsc]        the NATS permissions an endpoint user needs
  mash admin provision <name> --user U --account A [--note N]
  mash admin deprovision <name>
  mash admin list
  mash version

Configuration: ~/.config/mash/config.json {"url","owner"}, overridden by MASH_URL,
MASH_OWNER, MASH_NAME, MASH_CREDS, MASH_ADMIN_CREDS (see docs/install.md).
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

// versionLine is "mash <version> (commit <rev>)". It names the commit so a pinned build can be checked.
func versionLine(version, ldflags string, bi *debug.BuildInfo) string {
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
	if rev == "" {
		rev = "unknown"
	}
	return fmt.Sprintf("mash %s (commit %s)", version, rev)
}

func runCtx(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "version" {
		bi, _ := debug.ReadBuildInfo()
		fmt.Fprintln(stdout, versionLine(endpoint.Version, commit, bi))
		return 0
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	cfg, err := config.Load(env, env("HOME"))
	if err != nil && args[0] != "serve" {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch args[0] {
	case "serve":
		return serve(ctx, cfg, err, stdin, stdout, stderr)
	case "send", "read", "inbox", "who", "prompt":
		return cli(cfg, args, stdin, stdout, stderr)
	case "admin":
		return adminCmd(cfg, args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "mash: unknown command %q\n\n%s", args[0], usage)
	return 2
}
