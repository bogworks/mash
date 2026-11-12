package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policy"
)

// connectTimeout bounds the connection to the server; applyDeadline bounds a whole apply.
const (
	connectTimeout = 5 * time.Second
	applyDeadline  = 25 * time.Second
)

type applyOpts struct {
	file, sysCreds string
	dryRun         bool
	replaceZones   []string
	removeZones    []string
	keepZones      []string
	servers        string // --servers as given, "" when not
	maxControlLine string // --max-control-line as given, "" when not
}

func applyUsage(verb string) string {
	if verb == "diff" {
		return "usage: mashctl policy diff [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]... [--remove-zone ZONE]... [--keep-zone ZONE]... [--servers N] [--max-control-line BYTES]\n"
	}
	return "usage: mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]... [--remove-zone ZONE]... [--keep-zone ZONE]... [--servers N] [--max-control-line BYTES]\n"
}

func parseApplyArgs(verb string, args []string) (applyOpts, error) {
	var o applyOpts
	val := func(i *int, a, name string) (string, error) {
		if v, has := strings.CutPrefix(a, name+"="); has && v != "" {
			return v, nil
		}
		if a == name && *i+1 < len(args) && args[*i+1] != "" {
			*i++
			return args[*i], nil
		}
		return "", fmt.Errorf("%s needs a value", name)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var err error
		switch {
		case a == "--policy" || strings.HasPrefix(a, "--policy="):
			o.file, err = val(&i, a, "--policy")
		case a == "--sys-creds" || strings.HasPrefix(a, "--sys-creds="):
			o.sysCreds, err = val(&i, a, "--sys-creds")
		case a == "--replace-zone-key" || strings.HasPrefix(a, "--replace-zone-key="):
			var z string
			if z, err = val(&i, a, "--replace-zone-key"); err == nil {
				o.replaceZones = append(o.replaceZones, z)
			}
		case a == "--remove-zone" || strings.HasPrefix(a, "--remove-zone="):
			var z string
			if z, err = val(&i, a, "--remove-zone"); err == nil {
				o.removeZones = append(o.removeZones, z)
			}
		case a == "--keep-zone" || strings.HasPrefix(a, "--keep-zone="):
			var z string
			if z, err = val(&i, a, "--keep-zone"); err == nil {
				o.keepZones = append(o.keepZones, z)
			}
		case a == "--servers" || strings.HasPrefix(a, "--servers="):
			o.servers, err = val(&i, a, "--servers")
		case a == "--max-control-line" || strings.HasPrefix(a, "--max-control-line="):
			o.maxControlLine, err = val(&i, a, "--max-control-line")
		case a == "--dry-run" && (verb == "apply" || verb == "repair"):
			o.dryRun = true
		default:
			err = fmt.Errorf("unknown argument %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// sysCredsPath is --sys-creds, else MASH_SYS_CREDS, else the sys.creds that `mashctl init --local` wrote next to the
// admin credentials (or in the default server directory under the config directory).
func sysCredsPath(flagVal string, env func(string) string, cfg config.Config) (string, error) {
	switch {
	case flagVal != "":
		return flagVal, nil
	case env("MASH_SYS_CREDS") != "":
		return env("MASH_SYS_CREDS"), nil
	}
	var cands []string
	if cfg.AdminCreds != "" {
		cands = append(cands, filepath.Join(filepath.Dir(cfg.AdminCreds), bootstrap.SysCreds))
	}
	if cfg.Dir != "" {
		cands = append(cands, filepath.Join(cfg.Dir, "server", bootstrap.SysCreds))
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("no system account credentials: pass --sys-creds FILE or set MASH_SYS_CREDS (a server `mashctl init --local` stood up has them in <server dir>/sys.creds)")
}

// connectSys connects with the system account credentials (flagVal, MASH_SYS_CREDS or the init server's).
func connectSys(cfg config.Config, flagVal string, env func(string) string) (*nats.Conn, error) {
	credsPath, err := sysCredsPath(flagVal, env, cfg)
	if err != nil {
		return nil, err
	}
	// The same mode check as admin and endpoint credentials: a file other users can read is refused, before it is used.
	raw, err := config.ReadCredsFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("system account credentials: %v", err)
	}
	if err := natsauth.CheckCreds(credsPath, raw); err != nil {
		return nil, fmt.Errorf("system account credentials: %v", err)
	}
	tlsReq, err := natsauth.RequireTLS(cfg.URL, cfg.InsecureLoopback)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(err, cfg.URL))
	}
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(raw), nats.Name("mashctl-policy"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(natsauth.TLSHint(err), cfg.URL))
	}
	return nc, nil
}

// runPolicyApply is `mashctl policy apply` and `mashctl policy diff`.
func runPolicyApply(verb string, args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	pfx := "mashctl: policy " + verb + ": "
	o, err := parseApplyArgs(verb, args)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, applyUsage(verb))
		return 2
	}
	return runPolicyApplyOpts(verb, o, nil, env, lookup, stdout, stderr)
}

// policySnapshot is a policy file read, parsed and compiled once: `policy repair` hands the same snapshot to every
// step, so a file edited while repair runs cannot make one step act on a different policy than another.
type policySnapshot struct {
	path string
	f    *policy.File
	c    *policy.Compiled
}

// policyTestFailures runs the file's own tests and returns one line per failing test and the number of tests run.
// It is the one gate behind apply, add and reissue.
func policyTestFailures(f *policy.File) (failed []string, total int) {
	res := policy.RunTests(f)
	for _, r := range res {
		if !r.Pass {
			failed = append(failed, fmt.Sprintf("FAIL %s->%s expect %s got %s: %s", r.From, r.To, r.Expect, r.Got, r.Text))
		}
	}
	return failed, len(res)
}

// refuseFailedTests prints the failing test lines to stdout and returns the refusal text; what says what was left
// untouched, which differs by verb.
func refuseFailedTests(failed []string, total int, path, what string, stdout io.Writer) string {
	for _, l := range failed {
		fmt.Fprintln(stdout, l)
	}
	return fmt.Sprintf("refusing: %d of %d policy tests in %s fail (%s)", len(failed), total, path, what)
}

// runPolicyApplyOpts is runPolicyApply after argument parsing. With a nil snap it reads the policy file itself; with a
// snapshot it uses that and never reads the file again (the file's tests still run, against the snapshot).
func runPolicyApplyOpts(verb string, o applyOpts, snap *policySnapshot, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	pfx := "mashctl: policy " + verb + ": "
	var err error
	servers := 0
	if sv := o.servers; sv != "" || env("MASH_SERVERS") != "" {
		src := "--servers"
		if sv == "" {
			sv, src = env("MASH_SERVERS"), "MASH_SERVERS"
		}
		if servers, err = strconv.Atoi(strings.TrimSpace(sv)); err != nil || servers < 1 {
			fmt.Fprintf(stderr, "%s%s must be a whole number of servers, 1 or more, got %q\n%s", pfx, src, sv, applyUsage(verb))
			return 2
		}
	}
	maxControlLine, err := parseMaxControlLine(o.maxControlLine, env)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, applyUsage(verb))
		return 2
	}
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fail := func(format string, a ...any) int { fmt.Fprintf(stderr, pfx+format+"\n", a...); return 1 }
	var path string
	var f *policy.File
	if snap != nil {
		path, f = snap.path, snap.f
	} else {
		if path, err = policyPath(o.file, env, cfg); err != nil {
			return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fail("%v", err)
		}
		if f, err = policy.Parse(data); err != nil { // Parse folds the Check errors into one error
			for _, m := range strings.Split(strings.TrimPrefix(err.Error(), "policy: "), "; ") {
				fmt.Fprintf(stderr, "%s%s: error: %s\n", pfx, path, m)
			}
			return fail("refusing: the policy file does not check (nothing was changed)")
		}
	}
	// The file's own tests run before anything is read or created: a file whose tests contradict its rules is not pushed.
	failed, total := policyTestFailures(f)
	switch {
	case len(failed) > 0 && verb == "diff":
		fmt.Fprintf(stdout, "policy tests FAIL: %d of %d tests in %s do not hold; apply would refuse this file\n", len(failed), total, path)
		for _, l := range failed {
			fmt.Fprintf(stdout, "  %s\n", l)
		}
		fmt.Fprintln(stdout)
	case len(failed) > 0:
		return fail("%s", refuseFailedTests(failed, total, path, "nothing was changed, no key was created, nothing was pushed", stdout))
	case total == 0:
		fmt.Fprintf(stdout, "note: %s has no tests section, so the rules were not checked against any expectation\n", path)
	}
	if cfg.Owner == "" {
		return fail(`no owner (config.json "owner" or MASH_OWNER)`)
	}
	if cfg.URL == "" {
		return fail(`no server URL (config.json "url" or MASH_URL)`)
	}
	var c *policy.Compiled
	if snap != nil {
		c = snap.c
	} else if c, err = policy.Compile(f, cfg.Owner); err != nil {
		return fail("%v", err)
	}
	if snap == nil { // repair (which passes a snapshot) has announced already
		announceTarget(cfg, env, stderr, &sysTarget{o.sysCreds})
	}
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return fail("%v", err)
	}
	nc, err := connectSys(cfg, o.sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer nc.Close()

	mode := apply.Apply
	switch {
	case verb == "diff":
		mode = apply.Diff
	case o.dryRun:
		mode = apply.DryRun
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	defer cancel()
	return apply.Run(ctx, apply.Params{Mode: mode, Compiled: c, Keys: ks, Resolver: apply.NATS{Conn: nc, Inventory: servers}, ReplaceZoneKeys: o.replaceZones,
		RemoveZones: o.removeZones, KeepZones: o.keepZones, Servers: servers, MaxControlLine: maxControlLine}, stdout)
}
