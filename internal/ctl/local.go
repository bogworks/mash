package ctl

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// Seams for tests.
var (
	startLocalServer = bootstrap.Start
	initLocalBuckets func(prog string, c config.Config, stdout, stderr io.Writer) int
)

func init() {
	initLocalBuckets = func(prog string, c config.Config, stdout, stderr io.Writer) int {
		return Run(prog, c, []string{"init"}, stdout, stderr)
	}
}

func initLocalUsage(prog string) string {
	return "usage: " + prog + " init --local [--dir D] [--owner O] [--port P] [--start]\n  --start starts the new loopback-only server (127.0.0.1) and creates the buckets over a plain connection to it; init --local without --start connects to nothing"
}

// runInitLocal is `init --local`: it writes a local operator-mode NATS server's configuration (args are the
// arguments after "init"). It needs no config.json and no running server.
func runInitLocal(prog string, cfg config.Config, args []string, stdout, stderr io.Writer) int {
	pfx, _, _ := strings.Cut(prog, " ")
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	fs := flag.NewFlagSet(prog+" init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		local, start bool
		dir, owner   string
		port         = bootstrap.DefaultPort
	)
	fs.BoolVar(&local, "local", false, "")
	fs.BoolVar(&start, "start", false, "")
	fs.StringVar(&dir, "dir", "", "")
	fs.StringVar(&owner, "owner", "", "")
	fs.IntVar(&port, "port", bootstrap.DefaultPort, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, initLocalUsage(prog))
		return 2
	}
	if dir == "" {
		if cfg.Dir == "" {
			return fail(errors.New(pfx + ": no server directory: pass --dir, or set HOME or XDG_CONFIG_HOME"))
		}
		dir = filepath.Join(cfg.Dir, "server")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fail(err)
	}
	if owner == "" {
		owner = cfg.Owner
	}
	if owner == "" {
		return fail(errors.New(pfx + ": init --local needs an owner: pass --owner O (or set \"owner\" in config.json)"))
	}
	if !config.ValidName(owner) {
		return fail(fmt.Errorf("%s: owner %q is not a valid name: use lower-case letters, digits and single hyphens, starting with a letter, up to 48 characters", pfx, owner))
	}
	if port < 1 || port > 65535 {
		return fail(fmt.Errorf("%s: port %d is out of range", pfx, port))
	}
	keyDir, err := ctlkeys.DirFor(cfg, os.Getenv)
	if err != nil {
		return fail(err)
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return fail(err)
	}
	p := bootstrap.Params{Dir: abs, Owner: owner, Port: port}
	if cfg.Dir != "" {
		p.PolicyPath = cfg.Find("policy.yaml")
		p.ConfigPath = cfg.Find("config.json")
	}
	pl, err := bootstrap.Build(p, ks)
	if err != nil {
		return fail(err)
	}
	if err := pl.Write(); err != nil {
		return fail(err)
	}

	if created, err := ks.InstallOperatorJWT(pl.OperatorJWT); err != nil && !errors.Is(err, ctlkeys.ErrDifferentOperatorJWT) {
		return fail(err)
	} else if created {
		fmt.Fprintf(stdout, "%s: recorded the operator JWT in %s (mashctl accepts account JWTs issued by the keys it declares)\n", pfx, filepath.Join(keyDir, ctlkeys.OperatorJWTFile))
	}
	fmt.Fprintf(stdout, "%s: local NATS server configured in %s\n", pfx, abs)
	fmt.Fprintf(stdout, "  server config   %s (listens on 127.0.0.1:%d, loopback only, no TLS)\n", pl.ConfPath(), port)
	fmt.Fprintf(stdout, "  admin creds     %s\n", pl.Creds)
	fmt.Fprintf(stdout, "  system creds    %s\n", filepath.Join(abs, bootstrap.SysCreds))
	fmt.Fprintf(stdout, "  signing keys    %s (operator.nk, system.nk, account.nk; never copy them off this machine)\n", keyDir)
	if pl.Policy != nil {
		fmt.Fprintf(stdout, "  policy          %s (open: everything allowed)\n", pl.Policy.Path)
	} else if p.PolicyPath != "" {
		fmt.Fprintf(stdout, "  policy          %s exists, left as it is\n", p.PolicyPath)
	}
	if pl.Config != nil {
		fmt.Fprintf(stdout, "  config          %s\n", pl.Config.Path)
	} else {
		fmt.Fprintf(stdout, "  config          %s exists and was not touched; add to it:\n", orNone(p.ConfigPath))
		fmt.Fprintf(stdout, "    \"url\": %q, \"owner\": %q, \"admin_creds\": %q, \"insecure_loopback\": true\n", pl.URL, owner, pl.Creds)
	}

	// Later commands use the loaded configuration (or, when this run wrote config.json, what that file now says,
	// unless a MASH_* variable overrides it): does that name the server just configured?
	same := func(have, want string) bool { return have == want || (have == "" && pl.Config != nil) }
	elsewhere := !same(cfg.URL, pl.URL) || !same(cfg.AdminCreds, pl.Creds) || !same(cfg.Owner, owner)

	if !start {
		if elsewhere {
			fmt.Fprintf(stdout, "\nNote: your configuration points elsewhere: %s commands will not use this server until config.json (or MASH_URL, MASH_OWNER, MASH_ADMIN_CREDS) has the three settings above.\n", prog)
		}
		fmt.Fprintf(stdout, "\nNext:\n  1. start the server:      nats-server -c %s\n  2. create the buckets:    %s init\n  3. add an endpoint:       mashctl add NAME   (declares it in the policy, mints its creds and provisions it)\n", pl.ConfPath(), prog)
		return 0
	}
	pid, err := startLocalServer(pl.Params, pl.URL, true) // pl was generated by this run
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "  server          started, pid %d (log %s)\n", pid, pl.LogPath())
	c := cfg
	c.URL, c.Owner, c.AdminCreds = pl.URL, owner, pl.Creds
	c.TLSCA, c.TLSCert, c.TLSKey = "", "", "" // the local server has no TLS
	c.InsecureLoopback = true                 // the server was generated and started by this run on 127.0.0.1: plain by construction
	if code := initLocalBuckets(prog, c, stdout, stderr); code != 0 {
		fmt.Fprintf(stderr, "%s: the server is running (pid %d) but its buckets were not created; run: %s init\n", pfx, pid, prog)
		return code
	}
	if elsewhere {
		fmt.Fprintf(stdout, "\nthe local server is running, but your configuration points elsewhere\n")
		fmt.Fprintf(stdout, "  %s commands use url %q, admin_creds %q, owner %q (from config.json or MASH_* variables),\n  not this server, and will keep using that other server until you set these three:\n", prog, cfg.URL, cfg.AdminCreds, cfg.Owner)
		fmt.Fprintf(stdout, "    \"url\": %q, \"owner\": %q, \"admin_creds\": %q, \"insecure_loopback\": true\n", pl.URL, owner, pl.Creds)
		fmt.Fprintf(stdout, "  (or MASH_URL, MASH_OWNER, MASH_ADMIN_CREDS)\n")
		return 0
	}
	fmt.Fprintf(stdout, "\nReady: the local server is up at %s. Add an endpoint with: mashctl add NAME   (declares it in the policy, mints its creds and provisions it)\n", pl.URL)
	return 0
}

func orNone(s string) string {
	if s == "" {
		return "(no config directory)"
	}
	return s
}
