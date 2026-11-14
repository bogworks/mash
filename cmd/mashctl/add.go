package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/store"
)

const addUsage = `usage: mashctl add NAME [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
`

type addOpts struct {
	name, note, describe string
	labels               map[string]string
	caps                 []string
	tool                 bool
}

func parseAddArgs(args []string) (addOpts, error) {
	o := addOpts{labels: map[string]string{}}
	val := func(i *int, flag string) (string, error) {
		if *i+1 >= len(args) || args[*i+1] == "" {
			return "", fmt.Errorf("%s needs a value", flag)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		flag, inline, hasInline := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "--") {
			flag, hasInline = a, false
		}
		get := func() (string, error) {
			if hasInline {
				if inline == "" {
					return "", fmt.Errorf("%s needs a value", flag)
				}
				return inline, nil
			}
			return val(&i, flag)
		}
		var v string
		var err error
		switch flag {
		case "--tool":
			o.tool = true
		case "--label":
			if v, err = get(); err != nil {
				return o, err
			}
			k, lv, ok := strings.Cut(v, "=")
			if !ok || k == "" || lv == "" {
				return o, errors.New("--label wants k=v (the value is not shown)")
			}
			if _, dup := o.labels[k]; dup {
				return o, fmt.Errorf("label %q given twice", k)
			}
			o.labels[k] = lv
		case "--describe":
			if v, err = get(); err != nil {
				return o, err
			}
			o.describe = v
		case "--cap":
			if v, err = get(); err != nil {
				return o, err
			}
			o.caps = append(o.caps, v)
		case "--note":
			if v, err = get(); err != nil {
				return o, err
			}
			o.note = v
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown %s", config.RejectedArg(a))
			}
			if o.name != "" {
				return o, errors.New("one endpoint name only")
			}
			o.name = a
		}
	}
	if o.name == "" {
		return o, errors.New("no endpoint name")
	}
	return o, nil
}

// loadCtlConfig is config.Load for the verbs that need the owner; offline verbs also pass ErrNoURL.
func loadCtlConfig(env func(string) string, lookup config.Lookup, needURL bool) (config.Config, error) {
	cfg, err := loadConfig(env, lookup)
	if err != nil && (needURL || !errors.Is(err, config.ErrNoURL)) {
		return cfg, err
	}
	return cfg, nil
}

func openKeys(cfg config.Config, env func(string) string) (*ctlkeys.Authority, error) {
	dir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return nil, err
	}
	return ctlkeys.Open(dir)
}

// orphanFix is the exact recovery for an add that was killed after it made a directory record or creds file.
func orphanFix(name string) string {
	return fmt.Sprintf("run mashctl retire %s --force --delete-creds, then mashctl add %s again", name, name)
}

// orphanText says an existing creds file or inbox for a name the directory has no record of looks like an interrupted
// add. Callers use it only after checking that the directory has no record of that name.
func orphanText(name string) string { return orphanTextShown(name, name) }

// orphanTextShown is orphanText for a directory key that people read in another form (a three-token endpoint's
// canonical agent.owner.name, ADR 0081): the name is shown, the command keeps the key.
func orphanTextShown(shown, key string) string {
	return fmt.Sprintf("%s looks like an interrupted add (a creds file or an inbox exists but the directory has no complete record of %s): %s", shown, shown, orphanFix(key))
}

// credsFileFor is where name's creds live: an existing file in the config directories, else <config dir>/name.creds.
func credsFileFor(cfg config.Config, name string) (string, error) {
	p := cfg.Find(name + ".creds")
	if p == "" {
		return "", config.ErrNoConfigDir
	}
	return p, nil
}

// writeCredsNew installs creds at path through ctlkeys.InstallFile (a 0600 temp file, O_EXCL, fsync, a link that
// cannot replace an existing file, a directory sync), refusing an existing file. The parent must be a real
// directory the current user owns and that group and other cannot write to; a missing one is made 0700. undo
// removes what it made.
func writeCredsNew(path string, creds []byte) (undo func(), err error) {
	dir := filepath.Dir(path)
	madeDir := false
	if _, serr := os.Lstat(dir); errors.Is(serr, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		madeDir = true
	}
	undoDir := func() {
		if madeDir {
			os.Remove(dir) // only if empty
		}
	}
	if err := ctlkeys.CheckParentDir(dir); err != nil {
		undoDir()
		return nil, err
	}
	created, err := ctlkeys.InstallFile(path, creds)
	if err != nil {
		if created {
			os.Remove(path) // linked in, but not durable: take the entry back out
		}
		undoDir()
		return nil, err
	}
	if !created {
		undoDir()
		return nil, fmt.Errorf("creds file %s exists", path)
	}
	return func() {
		os.Remove(path)
		undoDir()
	}, nil
}

// runAdd is `mashctl add`.
// afterProvision is a test seam called between the directory record and the creds file.
var afterProvision func()

func runAdd(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	o, err := parseAddArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: add: %v\n%s", err, addUsage)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: add %s: %s\n", o.name, fmt.Sprintf(format, a...))
		return 1
	}
	if !config.ValidName(o.name) {
		fmt.Fprintf(stderr, "mashctl: %s: NAME is not a valid endpoint name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)\n", "add")
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	guard, ok := accountGuard(cfg, "add", stderr)
	if !ok {
		return 1
	}
	defer guard.Release()
	// The name rule comes before any key or record: a refused name creates nothing.
	if err := admin.CheckProvision(cfg.NameRule, cfg.Owner, o.name); err != nil {
		return fail("%v", err)
	}
	if cfg.Owner == "" {
		return fail(`no owner (config.json "owner" or MASH_OWNER)`)
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	if _, _, err := keys.Account(); err != nil { // before anything is touched: `mashctl init` first
		return fail("%v", err)
	}
	labels, err := addLabels(o)
	if err != nil {
		return fail("%v", err)
	}
	if err := store.ValidLabels(labels); err != nil {
		return fail("%v", err)
	}
	credsPath, err := credsFileFor(cfg, o.name)
	if err != nil {
		return fail("%v", err)
	}

	// The server connection is opened before any key or record is made, so an unreachable server changes nothing.
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()

	// A name the directory already holds is refused before a key is made or an issue entry written.
	recs, err := adm.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if _, dup := findRecord(recs, o.name); dup {
		return fail("endpoint %q already exists in the directory; nothing was changed (to rotate its user, run: mashctl reissue %s)", o.name, o.name)
	}

	if _, err := os.Lstat(credsPath); err == nil {
		return fail("creds file %s exists; %s", credsPath, orphanText(o.name))
	}

	// An applied zone policy is refused before any key, record or file is made (ADR 0091): the owner-wide permissions
	// minted below would bypass its mail restrictions. The check only reads.
	if err := zoneGuard(cfg, "", env); err != nil {
		return fail("%v", err)
	}

	// (1) mint: the user is signed by the account signing key and carries its own permissions, the owner-line set
	// admin.Permissions(owner, name) (ADR 0091).
	m, err := mint.Mint(keys, cfg.Owner, o.name, nil)
	if err != nil {
		return fail("%v", err)
	}
	defer clear(m.Creds)

	// (2) The issue record comes before the directory record and the creds file: a user that can connect is always in
	// it. A failure here issues nothing (the creds are in memory only).
	if err := guard.Before("the issue record"); err != nil {
		return fail("%v; nothing was issued", err)
	}
	if err := adm.RecordIssued(ctx, issuedOf(m, cfg.Owner, store.PurposeIssue)); err != nil {
		return fail("%v; nothing was issued", err)
	}

	// (3) the directory record and inbox.
	if err := guard.Before("the directory record and inbox"); err != nil {
		return fail("%v; nothing was issued", err)
	}
	meta := admin.Meta{Labels: labels, Description: o.describe, Caps: o.caps}
	if _, err := adm.Provision(ctx, o.name, m.UserPub, m.AccountPub, o.note, meta); err != nil {
		if strings.Contains(err.Error(), "is already provisioned") {
			return fail("%v; nothing was changed", err)
		}
		return fail("%v", err)
	}
	rollbackProvision := func() string {
		if err := adm.DeprovisionIfUser(ctx, o.name, m.UserPub); err != nil {
			return fmt.Sprintf("; the directory record and inbox of %s could not be removed (%v): run `mashctl deprovision %s`", o.name, err, o.name)
		}
		return ""
	}

	if afterProvision != nil {
		afterProvision()
	}

	// (4) the creds file, never overwriting.
	if err := guard.Before("the creds file"); err != nil {
		return fail("%v%s", err, rollbackProvision())
	}
	if _, err := writeCredsNew(credsPath, m.Creds); err != nil {
		return fail("%v%s", err, rollbackProvision())
	}

	// (5) report.
	fmt.Fprintf(stdout, "added %s creds %s\n", o.name, credsPath)
	return 0
}

// addLabels is the labels the new endpoint is declared with: --label pairs, plus kind=tool for --tool. Giving one key two different values is an error.
func addLabels(o addOpts) (map[string]string, error) {
	labels := map[string]string{}
	for k, v := range o.labels {
		labels[k] = v
	}
	set := func(k, v string) error {
		if old, ok := labels[k]; ok && old != v {
			return fmt.Errorf("label %s=%s conflicts with %s=%s", k, v, k, old)
		}
		labels[k] = v
		return nil
	}
	if o.tool {
		if err := set("kind", "tool"); err != nil {
			return nil, err
		}
	}
	return labels, nil
}
