package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyfile"
	"github.com/bogworks/mash/internal/store"
)

const addUsage = `usage: mashctl add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]
`

// afterProvision is a test seam called between the directory record and the policy-file save.
var afterProvision func()

type addOpts struct {
	name, file, note, zone string
	labels                 map[string]string
	tool                   bool
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
		case "--zone":
			if v, err = get(); err != nil {
				return o, err
			}
			o.zone = v
		case "--policy":
			if v, err = get(); err != nil {
				return o, err
			}
			o.file = v
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

// compileBytes parses and compiles policy-file bytes for owner.
func compileBytes(data []byte, owner string) (*policy.File, *policy.Compiled, error) {
	f, err := policy.Parse(data)
	if err != nil {
		return nil, nil, err
	}
	c, err := policy.Compile(f, owner)
	if err != nil {
		return nil, nil, err
	}
	return f, c, nil
}

func findEndpoint(c *policy.Compiled, name string) (policy.CompiledEndpoint, bool) {
	for _, e := range c.Endpoints {
		if e.Name == name {
			return e, true
		}
	}
	return policy.CompiledEndpoint{}, false
}

// orphanFix is the exact recovery for an add that was killed after it made a directory record or creds file.
func orphanFix(name string) string {
	return fmt.Sprintf("run mashctl retire %s --force --delete-creds, then mashctl add %s again", name, name)
}

// orphanText says an existing record or creds file for a name the policy file lacks looks like an interrupted add.
// Callers use it only after checking that the policy file has no endpoint of that name.
func orphanText(name string) string { return orphanTextShown(name, name) }

// orphanTextShown is orphanText for a directory key that people read in another form (a three-token endpoint's
// canonical agent.owner.name, ADR 0081): the name is shown, the command keeps the key.
func orphanTextShown(shown, key string) string {
	return fmt.Sprintf("%s looks like an interrupted add (a directory record or creds file exists but the policy file has no endpoint %s): %s", shown, shown, orphanFix(key))
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

// zoneSetChanged reports whether the account JWT's scoped keys must change between two compiles: a zone was added,
// removed, or any zone's permission template differs.
func zoneSetChanged(before, after *policy.Compiled) bool {
	if len(before.Zones) != len(after.Zones) {
		return true
	}
	for i := range after.Zones {
		if before.Zones[i].Name != after.Zones[i].Name || !reflect.DeepEqual(before.Zones[i].Perms, after.Zones[i].Perms) {
			return true
		}
	}
	return false
}

// consequences is what an edit from the before compile to the after compile leaves for the operator, one line each
// and none when nothing needs doing: an endpoint whose zone changed (its creds are signed by the old zone's key until
// reissued, and the directory record is in the old zone), other endpoints whose tags changed (their user JWTs are
// stale), and a changed account JWT (a zone added or removed, or a zone's permission template changed).
func consequences(before, after *policy.Compiled) []string {
	var out []string
	var stale []string
	for _, e := range before.Endpoints {
		ne, ok := findEndpoint(after, e.Name)
		switch {
		case !ok:
		case e.Zone != ne.Zone:
			out = append(out, fmt.Sprintf("%s moves from zone %s to zone %s: its mail address changes. A directory record with no zone takes `mashctl set-zone %s %s`, then `mashctl reissue %s`; a record already in zone %s cannot be moved (set-zone refuses): retire %s and add it again with --zone %s. Until it is reissued its creds are signed by zone %s's key and keep zone %s's permissions",
				e.Name, e.Zone, ne.Zone, e.Name, ne.Zone, e.Name, e.Zone, e.Name, ne.Zone, e.Zone, e.Zone))
		case !slices.Equal(e.Tags, ne.Tags):
			stale = append(stale, e.Name)
		}
	}
	if len(stale) > 0 {
		out = append(out, fmt.Sprintf("reissue needed: %s (run: mashctl reissue %s)", strings.Join(stale, ", "), strings.Join(stale, " ")))
	}
	if zoneSetChanged(before, after) {
		out = append(out, "account update needed: run mashctl policy apply")
	}
	return out
}

// runAdd is `mashctl add`.
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
	if err := policyfile.CheckName(o.name); err != nil {
		fmt.Fprintf(stderr, "mashctl: add: %v\n", err)
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The name rule comes before any key, policy edit or record: a refused name creates nothing.
	if err := admin.CheckProvision(cfg.NameRule, cfg.Owner, o.name); err != nil {
		return fail("%v", err)
	}
	path, err := policyPath(o.file, env, cfg)
	if err != nil {
		return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	if _, _, err := keys.Account(); err != nil { // before anything is touched: `mashctl init` first
		return fail("%v", err)
	}

	// (1) the policy file, locked for the whole command.
	doc, err := policyfile.Load(path)
	if err != nil {
		return fail("%v", err)
	}
	defer doc.Close()
	beforeBytes, _ := doc.Bytes()
	beforeFile, before, err := compileBytes(beforeBytes, cfg.Owner)
	if err != nil {
		return fail("%s: %v", path, err)
	}
	if _, dup := beforeFile.Endpoints[o.name]; dup {
		return fail("endpoint %q is already declared in %s", o.name, path)
	}
	labels, err := addLabels(o, before.SubjectLabel)
	if err != nil {
		return fail("%v", err)
	}
	if err := store.ValidLabels(labels); err != nil {
		return fail("%v", err)
	}

	// (2) the endpoint in memory, compiled.
	if err := doc.AddEndpoint(o.name, labels); err != nil {
		return fail("%v", err)
	}
	afterBytes, _ := doc.Bytes()
	afterFile, after, err := compileBytes(afterBytes, cfg.Owner) // Parse runs Check: a file that does not check is refused here
	if err != nil {
		return fail("the new endpoint does not make a valid policy: %v", err)
	}
	// The file's own tests gate the mint exactly as they gate apply: nothing is created, provisioned or written first.
	if failed, total := policyTestFailures(afterFile); len(failed) > 0 {
		return fail("%s", refuseFailedTests(failed, total, path, "nothing was changed, no key or creds file was created, nothing was provisioned", stdout))
	}
	ep, ok := findEndpoint(after, o.name)
	if !ok {
		return fail("internal error: the new endpoint did not compile")
	}
	// The JWTs the policy needs must fit before anything is created, provisioned or written.
	rep, berr := policy.Budget(after)
	if rerr := budgetRefusal(o.name, rep, berr); rerr != nil {
		fmt.Fprintf(stderr, "mashctl: %v\n", rerr)
		return 1
	}

	credsPath, err := credsFileFor(cfg, o.name)
	if err != nil {
		return fail("%v", err)
	}
	if _, err := os.Lstat(credsPath); err == nil {
		return fail("creds file %s exists; %s", credsPath, orphanText(o.name))
	}

	// The server connection is opened before any key or record is made, so an unreachable server changes nothing.
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()

	// (3) mint: the zone key is created only if the zone is new (no declared endpoint is in it); a zone that already
	// has endpoints must already have its key, or the new creds would not authenticate. A key made here is left in
	// place by a later failure: an unused key is not in any account JWT, and a retry reuses it.
	zoneExists := slices.ContainsFunc(before.Endpoints, func(e policy.CompiledEndpoint) bool { return e.Zone == ep.Zone })
	var mk mint.Keys = keys
	_, keyErr := keys.ZoneKey(ep.Zone, false)
	if zoneExists || keyErr == nil {
		// A key that already exists (the zone has endpoints, or an earlier failed add left it) must be published as a
		// scoped signer; only a key this call creates is exempt. The verifier connects only now, before any record.
		mk = scopeKeys{Authority: keys, cfg: cfg, env: env}
	}
	m, err := mint.Mint(mk, after, o.name, nil, !zoneExists)
	if err != nil {
		if zoneExists && errors.Is(err, ctlkeys.ErrNoZoneKey) {
			dir, _ := ctlkeys.DirFor(cfg, env)
			return fail("the signing key for zone %s is missing (existing endpoints are in this zone), so a new endpoint's creds would not authenticate: restore the key file from backup (key directory %s), or move the endpoints to another zone; nothing was changed", ep.Zone, dir)
		}
		return fail("%v", err)
	}
	defer clear(m.Creds)

	// (4) the directory record and inbox.
	if _, err := adm.Provision(ctx, o.name, m.UserPub, m.AccountPub, o.note, admin.Meta{Labels: ep.Labels, Zone: ep.Zone}); err != nil {
		if strings.Contains(err.Error(), "is already provisioned") {
			return fail("%v; %s", err, orphanText(o.name))
		}
		return fail("%v", err)
	}
	rollbackProvision := func() string {
		if err := adm.DeprovisionIfUser(ctx, o.name, m.UserPub); err != nil {
			return fmt.Sprintf("; the directory record and inbox of %s could not be removed (%v): run `mashctl deprovision %s`", o.name, err, o.name)
		}
		return ""
	}

	// (5) the creds file, never overwriting.
	undoCreds, err := writeCredsNew(credsPath, m.Creds)
	if err != nil {
		return fail("%v%s", err, rollbackProvision())
	}
	if afterProvision != nil {
		afterProvision()
	}

	// (6) the policy file.
	if err := doc.Save(); err != nil {
		undoCreds()
		msg := rollbackProvision()
		if errors.Is(err, policyfile.ErrChanged) {
			return fail("%v: nothing was added%s. Re-run: mashctl add %s", err, msg, strings.Join(args, " "))
		}
		return fail("%v: nothing was added%s", err, msg)
	}

	// (7) report.
	fmt.Fprintf(stdout, "added %s (zone %s) creds %s\n", o.name, ep.Zone, credsPath)
	for _, l := range consequences(before, after) {
		fmt.Fprintln(stdout, l)
	}
	if w := budgetWarning(rep); w != "" {
		fmt.Fprintln(stderr, w)
	}
	return 0
}

// budgetRefusal is the error for an add whose compiled policy does not fit the JWT budgets, or nil when it fits.
// err and r come from policy.Budget; the budget message names the biggest contributor.
func budgetRefusal(name string, r policy.BudgetReport, err error) error {
	if err == nil && r.OK {
		return nil
	}
	reason := "the JWT budgets are exceeded"
	if err != nil {
		reason = strings.TrimPrefix(err.Error(), "policy: ")
	}
	return fmt.Errorf("adding %s would make the policy unusable: %s; nothing was changed", name, reason)
}

// budgetWarning is a one-line warning when the budget passes but the worst user JWT or the account JWT is above 80%
// of its limit, else "".
func budgetWarning(r policy.BudgetReport) string {
	var parts []string
	if r.UserLimit > 0 && r.WorstUserJWTBytes*5 > r.UserLimit*4 {
		parts = append(parts, fmt.Sprintf("the user JWT of %s is %d of %d bytes", r.WorstUser, r.WorstUserJWTBytes, r.UserLimit))
	}
	if r.AccountLimit > 0 && r.AccountJWTBytes*5 > r.AccountLimit*4 {
		parts = append(parts, fmt.Sprintf("the account JWT is %d of %d bytes", r.AccountJWTBytes, r.AccountLimit))
	}
	if len(parts) == 0 {
		return ""
	}
	return "warning: above 80% of a JWT budget: " + strings.Join(parts, "; ")
}

// addLabels is the labels the new endpoint is declared with: --label pairs, plus kind=tool for --tool and the
// subject label for --zone. Giving one key two different values is an error.
func addLabels(o addOpts, subjectLabel string) (map[string]string, error) {
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
	if o.zone != "" {
		if err := set(subjectLabel, o.zone); err != nil {
			return nil, err
		}
	}
	return labels, nil
}
