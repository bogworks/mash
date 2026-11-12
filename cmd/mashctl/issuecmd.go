package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/wranglerreg"
)

const issueUsage = `usage: mashctl issue serve --test-account [--list-set FILE] [--registry FILE] [--sys-creds FILE]
  --test-account  the operator's own declaration that this account is a test account; the static wrangler registry is
                  a test-account facility, and the service starts only if the registry file says so too
`

const wranglerUsage = `usage: mashctl wrangler init --test-account
       mashctl wrangler add --host H --group G --key PUB
       mashctl wrangler list
       mashctl wrangler remove --host H --group G
`

// listSetFile is the default list set file's name in the config directory.
const listSetFile = "listset.json"

// issueServeContext is the context the service runs under; tests replace it. The default ends on SIGINT or SIGTERM.
var issueServeContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// issueServeReady is called once the service is listening; a test seam.
var issueServeReady = func() {}

// flagArgs parses "--name value" and "--name=value" for the names in want (a name maps to its value pointer); anything
// else is an error.
func flagArgs(args []string, want map[string]*string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		p, ok := want[name]
		if !ok || !strings.HasPrefix(a, "--") {
			return fmt.Errorf("unknown %s", config.RejectedArg(a))
		}
		switch {
		case hasInline && inline != "":
			*p = inline
		case !hasInline && i+1 < len(args) && args[i+1] != "":
			i++
			*p = args[i]
		default:
			return fmt.Errorf("%s needs a value", name)
		}
	}
	return nil
}

// adminRecord is the issue.Record over the admin connection.
type adminRecord struct{ a *admin.Admin }

func (r adminRecord) Lookup(ctx context.Context, key string) (*store.Issued, error) {
	return r.a.IssuedByKey(ctx, key)
}
func (r adminRecord) RecordChecked(ctx context.Context, rec store.Issued) error {
	return r.a.RecordIssuedChecked(ctx, rec)
}

// liveClaims is the issue.AccountClaims over the system account: the newest account JWT the servers hold, verified
// against the operator key and its signing keys, as policy apply reads it.
type liveClaims struct {
	res     apply.Resolver
	account string
	trust   apply.Trust
}

func (c liveClaims) Fetch(ctx context.Context) (*jwt.AccountClaims, error) {
	return apply.CurrentAccountClaims(ctx, c.res, c.account, c.trust)
}

// groupSigner is the issue.Signer over the key store: owner group "range.owner" keys (ctlkeys.GroupKey), never created
// by the service.
type groupSigner struct{ keys *ctlkeys.Authority }

func (s groupSigner) GroupKey(group string) (nkeys.KeyPair, error) {
	return s.keys.GroupKey(group, false)
}

func runIssue(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprint(stderr, "mashctl: issue: serve\n"+issueUsage)
		return 2
	}
	var listFile, regFile, sysCreds string
	testAccount := false
	rest := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		if a == "--test-account" {
			testAccount = true
			continue
		}
		rest = append(rest, a)
	}
	if err := flagArgs(rest, map[string]*string{"--list-set": &listFile, "--registry": &regFile, "--sys-creds": &sysCreds}); err != nil {
		fmt.Fprintf(stderr, "mashctl: issue serve: %v\n%s", err, issueUsage)
		return 2
	}
	if !testAccount {
		fmt.Fprintf(stderr, "mashctl: issue serve: the static wrangler registry is a test-account facility and real wrangler registration is not decided yet: pass --test-account to declare this a test account\n%s", issueUsage)
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: issue serve: %s\n", fmt.Sprintf(format, a...))
		return 1
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	akp, account, err := keys.Account()
	if err != nil {
		return fail("%v", err)
	}
	akp.Wipe()
	if regFile == "" {
		keyDir, err := ctlkeys.DirFor(cfg, env)
		if err != nil {
			return fail("%v", err)
		}
		regFile = wranglerreg.Path(keyDir)
	}
	reg, err := wranglerreg.OpenStatic(regFile, account)
	if err != nil {
		return fail("%v", err)
	}
	if listFile == "" {
		if cfg.Dir == "" {
			return fail("no list set: pass --list-set FILE")
		}
		listFile = filepath.Join(cfg.Dir, listSetFile)
	}
	ls := &issue.FileListSet{Path: listFile}
	compiled, _, err := ls.InForce(context.Background())
	if err != nil {
		return fail("%v", err)
	}
	trust, err := apply.LoadTrust(keys)
	if err != nil {
		return fail("%v", err)
	}
	announceTarget(cfg, env, stderr, &sysTarget{sysCreds})
	sys, err := connectSys(cfg, sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer sys.Close()
	adm, _, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	log := slog.New(slog.NewTextHandler(stderr, nil))
	is := &issue.Issuer{Registry: reg, ListSet: ls, Signer: groupSigner{keys}, Record: adminRecord{adm}, Claims: liveClaims{apply.NATS{Conn: sys}, account, trust}, AccountPub: account, Log: log}
	stop, err := issue.Serve(adm.Conn(), is, compiled.Range, endpoint.CurrentVersion())
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := issueServeContext()
	defer cancel()
	log.Info("issue service listening", "subject", issue.SubjectPrefix+"."+compiled.Range+".*", "account", account, "registry", regFile, "list_set", listFile)
	issueServeReady()
	<-ctx.Done()
	if err := stop(); err != nil {
		return fail("stop: %v", err)
	}
	log.Info("issue service stopped")
	return 0
}

func runWrangler(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "mashctl: wrangler: init, add, list or remove\n"+wranglerUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	var host, group, key string
	testAccount := false
	switch verb {
	case "init":
		if len(rest) == 1 && rest[0] == "--test-account" {
			testAccount = true
		} else if len(rest) > 0 {
			fmt.Fprintf(stderr, "mashctl: wrangler init: unknown %s\n%s", config.RejectedArg(rest[0]), wranglerUsage)
			return 2
		}
	case "add", "remove", "list":
		want := map[string]*string{"--host": &host, "--group": &group}
		if verb == "add" {
			want["--key"] = &key
		}
		if verb == "list" {
			want = nil
		}
		if err := flagArgs(rest, want); err != nil {
			fmt.Fprintf(stderr, "mashctl: wrangler %s: %v\n%s", verb, err, wranglerUsage)
			return 2
		}
		if verb != "list" && (host == "" || group == "" || (verb == "add" && key == "")) {
			fmt.Fprintf(stderr, "mashctl: wrangler %s: missing a flag\n%s", verb, wranglerUsage)
			return 2
		}
	default:
		fmt.Fprintf(stderr, "mashctl: wrangler: unknown verb %q\n%s", verb, wranglerUsage)
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: wrangler %s: %s\n", verb, fmt.Sprintf(format, a...))
		return 1
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	akp, account, err := keys.Account()
	if err != nil {
		return fail("%v", err)
	}
	akp.Wipe()
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	path := wranglerreg.Path(keyDir)

	if verb == "init" {
		if !testAccount {
			return fail("the static registry is for test accounts only: pass --test-account to say this is one; real wrangler registration is not decided yet")
		}
		if f, err := wranglerreg.Load(path); err == nil {
			if f.Account == account && f.TestAccount {
				fmt.Fprintf(stdout, "wrangler registry %s already exists for account %s\n", path, account)
				return 0
			}
			return fail("%s exists and is for another account or not a test registry: not overwriting it", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail("%v", err)
		}
		f := &wranglerreg.File{Account: account, TestAccount: true, Entries: []wranglerreg.Entry{}}
		if err := f.Save(path); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "wrote wrangler registry %s for test account %s\n", path, account)
		return 0
	}
	f, err := wranglerreg.Load(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fail("no wrangler registry at %s: run `mashctl wrangler init --test-account` (test accounts only)", path)
	case err != nil:
		return fail("%v", err)
	case f.Account != account || !f.TestAccount:
		return fail("%v", wranglerreg.ErrNoRegistry)
	}
	switch verb {
	case "list":
		for _, e := range f.Entries {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", e.Host, e.Group, e.WranglerKey)
		}
		return 0
	case "add":
		if err := f.Add(host, group, key); err != nil {
			return fail("%v", err)
		}
		if err := f.Save(path); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "registered wrangler key %s... for host %s, group %s\n", ctl.ShortKey(key), host, group)
	case "remove":
		n := f.Remove(host, group)
		if n == 0 {
			return fail("no entry for host %s, group %s", host, group)
		}
		if err := f.Save(path); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "removed %d entr%s for host %s, group %s\n", n, map[bool]string{true: "y", false: "ies"}[n == 1], host, group)
	}
	return 0
}
