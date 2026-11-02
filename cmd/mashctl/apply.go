package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
}

func applyUsage(verb string) string {
	if verb == "diff" {
		return "usage: mashctl policy diff [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]...\n"
	}
	return "usage: mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]...\n"
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
		case a == "--dry-run" && verb == "apply":
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

// runPolicyApply is `mashctl policy apply` and `mashctl policy diff`.
func runPolicyApply(verb string, args []string, env func(string) string, stdout, stderr io.Writer) int {
	pfx := "mashctl: policy " + verb + ": "
	o, err := parseApplyArgs(verb, args)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, applyUsage(verb))
		return 2
	}
	cfg, err := config.Load(env, env("HOME"))
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fail := func(format string, a ...any) int { fmt.Fprintf(stderr, pfx+format+"\n", a...); return 1 }
	path, err := policyPath(o.file, env, cfg)
	if err != nil {
		return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail("%v", err)
	}
	f, err := policy.Parse(data)
	if err != nil { // Parse folds the Check errors into one error
		for _, m := range strings.Split(strings.TrimPrefix(err.Error(), "policy: "), "; ") {
			fmt.Fprintf(stderr, "%s%s: error: %s\n", pfx, path, m)
		}
		return fail("refusing: the policy file does not check (nothing was changed)")
	}
	if cfg.Owner == "" {
		return fail(`no owner (config.json "owner" or MASH_OWNER)`)
	}
	if cfg.URL == "" {
		return fail(`no server URL (config.json "url" or MASH_URL)`)
	}
	c, err := policy.Compile(f, cfg.Owner)
	if err != nil {
		return fail("%v", err)
	}
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return fail("%v", err)
	}
	credsPath, err := sysCredsPath(o.sysCreds, env, cfg)
	if err != nil {
		return fail("%v", err)
	}
	raw, err := os.ReadFile(credsPath)
	if err != nil {
		return fail("system account credentials: %v", err)
	}
	if err := natsauth.CheckCreds(credsPath, raw); err != nil {
		return fail("system account credentials: %v", err)
	}
	opts := append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey),
		natsauth.UserCredentialBytes(raw), nats.Name("mashctl-policy"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return fail("connect %s with the system account credentials: %v", cfg.URL, natsauth.TLSHint(err))
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
	return apply.Run(ctx, apply.Params{Mode: mode, Compiled: c, Keys: ks, Resolver: apply.NATS{Conn: nc}, ReplaceZoneKeys: o.replaceZones}, stdout)
}
