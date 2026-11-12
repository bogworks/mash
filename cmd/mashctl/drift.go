package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func driftUsage() string {
	return "usage: mashctl policy drift [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]\n"
}

func repairUsage() string {
	return "usage: mashctl policy repair [--policy FILE] [--sys-creds FILE] [--servers N] [--dry-run]\n"
}

// The states a drift report compares with the policy file, in the order they are printed.
const (
	statusOK      = "ok"
	statusDrift   = "drift"
	statusUnknown = "unknown" // the state could not be read; it counts as drift for the exit code
)

// Finding kinds of the directory and issued states (the account state's are in internal/apply).
const (
	kindRecordMissing = "record-missing"
	kindRecordExtra   = "record-extra"
	kindZonedRecord   = "zoned-record"
	kindLabelDiffers  = "label-differs"
	kindCredsStale    = "creds-stale"
	kindCredsUnread   = "creds-unreadable"
)

// stateReport is one compared state. Findings is never nil, so the JSON is stable.
type stateReport struct {
	Status   string          `json:"status"`
	Findings []apply.Finding `json:"findings"`
	// Error says why the state could not be read (status "unknown").
	Error string `json:"error,omitempty"`
	// Notes are facts that are not drift (for example the endpoints that have no creds file here).
	Notes []string `json:"notes,omitempty"`
}

func newState(f []apply.Finding) stateReport {
	if f == nil {
		f = []apply.Finding{}
	}
	s := stateReport{Status: statusOK, Findings: f}
	if len(f) > 0 {
		s.Status = statusDrift
	}
	return s
}

func unknownState(err error) stateReport {
	return stateReport{Status: statusUnknown, Findings: []apply.Finding{}, Error: err.Error()}
}

// driftReport is what `mashctl policy drift --json` prints.
type driftReport struct {
	Drift      bool        `json:"drift"`
	Policy     string      `json:"policy"`
	Account    stateReport `json:"account"`
	Directory  stateReport `json:"directory"`
	Issued     stateReport `json:"issued"`
	NotCovered []string    `json:"not_covered"`
}

// driftNotCovered is the caveat printed under every report.
var driftNotCovered = []string{
	"credentials minted by other tools (for example nsc) under the account key are not seen: drift reads only the endpoints the policy file declares, their records and the creds files it finds, and a user the file does not declare can still connect until it is revoked",
	"the account state is the copy one resolver answers with; apply is what makes every server acknowledge it",
	"only creds files on this machine are compared (--creds-dir, else where mashctl add writes them); an endpoint's creds file kept elsewhere is not seen",
}

type driftOpts struct {
	file, sysCreds, credsDir string
	json                     bool
}

func parseDriftArgs(args []string) (driftOpts, error) {
	var o driftOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		val := func() (string, error) {
			if hasInline {
				if inline == "" {
					return "", fmt.Errorf("%s needs a value", name)
				}
				return inline, nil
			}
			if i+1 < len(args) && args[i+1] != "" {
				i++
				return args[i], nil
			}
			return "", fmt.Errorf("%s needs a value", name)
		}
		var err error
		switch name {
		case "--policy":
			o.file, err = val()
		case "--sys-creds":
			o.sysCreds, err = val()
		case "--creds-dir":
			o.credsDir, err = val()
		case "--json":
			if hasInline {
				err = fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			o.json = true
		default:
			err = fmt.Errorf("unexpected %s", config.RejectedArg(a))
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// loaded is what both verbs need before they look at any state.
type loaded struct {
	cfg  config.Config
	path string
	f    *policy.File
	c    *policy.Compiled
	keys *ctlkeys.Authority
}

// loadPolicy reads, checks and compiles the policy file and opens the key store. It prints its own errors.
func loadPolicy(pfx, file string, env func(string) string, lookup config.Lookup, stderr io.Writer) (*loaded, bool) {
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return nil, false
	}
	fail := func(format string, a ...any) (*loaded, bool) {
		fmt.Fprintf(stderr, pfx+format+"\n", a...)
		return nil, false
	}
	path, err := policyPath(file, env, cfg)
	if err != nil {
		return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail("%v", err)
	}
	f, err := policy.Parse(data)
	if err != nil {
		for _, m := range strings.Split(strings.TrimPrefix(err.Error(), "policy: "), "; ") {
			fmt.Fprintf(stderr, "%s%s: error: %s\n", pfx, path, m)
		}
		return fail("the policy file does not check")
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
	ks, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	return &loaded{cfg: cfg, path: path, f: f, c: c, keys: ks}, true
}

// credsPathFunc is where an endpoint's creds file is looked for: dir/NAME.creds, else where `mashctl add` writes it.
func credsPathFunc(cfg config.Config, dir string) func(name string) (string, error) {
	return func(name string) (string, error) {
		if dir != "" {
			return filepath.Join(dir, name+".creds"), nil
		}
		return credsFileFor(cfg, name)
	}
}

func mapKeys[M ~map[string]V, V any](m M) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// dirFix is what repair does for one endpoint's directory record.
type dirFix struct {
	Name   string
	Labels map[string]string // for admin.SetMeta: a value sets the label, "" clears it
	Lines  []string          // one line per change, the same text for a real run and --dry-run
}

// directoryFindings compares the endpoint records with the file's endpoints, and says what repair would change.
// The file is the source of truth for the labels of an endpoint it declares: a label the record has and the file lacks
// is a difference (repair clears it, so repair is not additive only). A record the file does not declare is only reported.
func directoryFindings(c *policy.Compiled, recs []store.Record) ([]apply.Finding, []dirFix) {
	var out []apply.Finding
	var fixes []dirFix
	byName := map[string]store.Record{}
	for _, r := range recs {
		if zerr := store.ZonedRecordError(r); zerr != nil { // zones are removed (ADR 0091): an error whatever the file says
			out = append(out, apply.Finding{Kind: kindZonedRecord, Subject: r.KeyOrName(), Detail: zerr.Error()})
		}
		byName[r.KeyOrName()] = r // the policy file's endpoints are legacy names: only a legacy record has that key
	}
	for _, e := range c.Endpoints {
		r, ok := byName[e.Name]
		if !ok {
			out = append(out, apply.Finding{Kind: kindRecordMissing, Subject: e.Name,
				Detail: "the policy file declares this endpoint and the directory has no record of it (provision it: repair does not create records or users)"})
			continue
		}
		fix := dirFix{Name: e.Name}
		labels := map[string]string{}
		for _, k := range slices.Sorted(mapKeys(e.Labels)) {
			if r.Labels[k] != e.Labels[k] {
				if v, has := r.Labels[k]; has {
					out = append(out, apply.Finding{Kind: kindLabelDiffers, Subject: e.Name, Detail: fmt.Sprintf("label %s is %q on the record, %q in the file", k, config.Clean(v), e.Labels[k])})
				} else {
					out = append(out, apply.Finding{Kind: kindLabelDiffers, Subject: e.Name, Detail: fmt.Sprintf("label %s=%s is in the file and not on the record", k, e.Labels[k])})
				}
				labels[k] = e.Labels[k]
				if v, has := r.Labels[k]; has {
					fix.Lines = append(fix.Lines, fmt.Sprintf("set label %s %s=%s (was %s)", e.Name, k, e.Labels[k], v))
				} else {
					fix.Lines = append(fix.Lines, fmt.Sprintf("set label %s %s=%s (was unset)", e.Name, k, e.Labels[k]))
				}
			}
		}
		for _, k := range slices.Sorted(mapKeys(r.Labels)) {
			if _, has := e.Labels[k]; !has {
				out = append(out, apply.Finding{Kind: kindLabelDiffers, Subject: e.Name, Detail: fmt.Sprintf("label %s=%s is on the record and not in the file", k, config.Clean(r.Labels[k]))})
				labels[k] = ""
				fix.Lines = append(fix.Lines, fmt.Sprintf("clear label %s %s=%s", e.Name, k, r.Labels[k]))
			}
		}
		if len(labels) > 0 {
			fix.Labels = labels
		}
		if fix.Labels != nil {
			fixes = append(fixes, fix)
		}
	}
	declared := map[string]bool{}
	for _, e := range c.Endpoints {
		declared[e.Name] = true
	}
	for _, r := range recs {
		if !declared[r.KeyOrName()] {
			out = append(out, apply.Finding{Kind: kindRecordExtra, Subject: r.KeyOrName(), Identity: shownIdentity(r.KeyOrName()),
				Detail: "a directory record the policy file does not declare (reported only: repair never deletes a record): " + orphanTextShown(sap.Display(r.KeyOrName()), r.KeyOrName())})
		}
	}
	sortFindings(out)
	return out, fixes
}

func sortFindings(f []apply.Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Subject != f[j].Subject {
			return f[i].Subject < f[j].Subject
		}
		return f[i].Kind < f[j].Kind
	})
}

// issuedFindings compares the creds files found with the compiled endpoints, with the compare `reissue --changed` uses.
// An endpoint with no creds file here is a note, not drift. The result also lists the stale endpoints, by name.
func issuedFindings(keys *ctlkeys.Authority, c *policy.Compiled, owner string, path func(string) (string, error)) (findings []apply.Finding, notes, stale []string) {
	for _, e := range c.Endpoints {
		p, err := path(e.Name)
		if err != nil {
			findings = append(findings, apply.Finding{Kind: kindCredsUnread, Subject: e.Name, Detail: err.Error()})
			continue
		}
		d, err := compareCreds(keys, p, owner, e)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			notes = append(notes, "no creds file here for "+e.Name)
		case err != nil:
			findings = append(findings, apply.Finding{Kind: kindCredsUnread, Subject: e.Name, Detail: err.Error()})
		case d.stale():
			findings = append(findings, apply.Finding{Kind: kindCredsStale, Subject: e.Name, Detail: d.text()})
			stale = append(stale, e.Name)
		}
	}
	sortFindings(findings)
	return findings, notes, stale
}

// gather reads the four states. conn failures turn the state they affect into "unknown".
type states struct {
	account, directory, issued stateReport
	dirFixes                   []dirFix
	stale                      []string
}

func gather(ctx context.Context, l *loaded, env func(string) string, sysCreds, credsDir string, servers int) states {
	var st states
	// the account state needs the system account connection.
	nc, err := connectSys(l.cfg, sysCreds, env)
	if err != nil {
		st.account = unknownState(err)
	} else {
		defer nc.Close()
		f, err := apply.AccountFindings(ctx, apply.NATS{Conn: nc, Inventory: servers}, l.keys, servers)
		if err != nil {
			st.account = unknownState(err)
		} else {
			st.account = newState(f)
		}
	}
	st.directory, st.dirFixes = directoryState(ctx, l)
	var notes []string
	var f []apply.Finding
	f, notes, st.stale = issuedFindings(l.keys, l.c, l.cfg.Owner, credsPathFunc(l.cfg, credsDir))
	st.issued = newState(f)
	st.issued.Notes = notes
	return st
}

func directoryState(ctx context.Context, l *loaded) (stateReport, []dirFix) {
	adm, actx, closeAdmin, err := ctl.OpenAdmin("mashctl", l.cfg)
	if err != nil {
		return unknownState(err), nil
	}
	defer closeAdmin()
	_ = ctx
	recs, err := adm.List(actx)
	if err != nil {
		return unknownState(fmt.Errorf("read the directory: %w", err)), nil
	}
	f, fixes := directoryFindings(l.c, recs)
	return newState(f), fixes
}

// printState writes one block: "name: ok", or "name: DRIFT (n)" and its findings.
func printState(out io.Writer, name string, s stateReport) {
	switch s.Status {
	case statusOK:
		fmt.Fprintf(out, "%s: ok\n", name)
	case statusUnknown:
		fmt.Fprintf(out, "%s: UNKNOWN: %s\n", name, config.Escape(s.Error))
	default:
		fmt.Fprintf(out, "%s: DRIFT (%d)\n", name, len(s.Findings))
		for _, f := range s.Findings {
			if f.Subject != "" {
				shown := f.Subject
				if f.Identity != "" {
					shown = f.Identity // the text is for people (ADR 0081); the JSON keeps the flat subject
				}
				fmt.Fprintf(out, "  %s %s: %s\n", f.Kind, config.Escape(shown), config.Escape(f.Detail))
			} else {
				fmt.Fprintf(out, "  %s: %s\n", f.Kind, config.Escape(f.Detail))
			}
			for _, l := range f.Lines {
				fmt.Fprintf(out, "      %s\n", config.Escape(l))
			}
		}
	}
	for _, n := range s.Notes {
		fmt.Fprintf(out, "  note: %s\n", config.Escape(n))
	}
}

// runPolicyDrift is `mashctl policy drift`: it compares the policy file with the three states and fixes nothing.
func runPolicyDrift(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	pfx := "mashctl: policy drift: "
	o, err := parseDriftArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, driftUsage())
		return 2
	}
	l, ok := loadPolicy(pfx, o.file, env, lookup, stderr)
	if !ok {
		return 1
	}
	announceTarget(l.cfg, env, stderr, &sysTarget{o.sysCreds})
	ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	defer cancel()
	servers, err := parseServers(env)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, driftUsage())
		return 2
	}
	st := gather(ctx, l, env, o.sysCreds, o.credsDir, servers)
	rep := driftReport{Policy: l.path, Account: st.account, Directory: st.directory, Issued: st.issued, NotCovered: driftNotCovered}
	for _, s := range []stateReport{st.account, st.directory, st.issued} {
		rep.Drift = rep.Drift || s.Status != statusOK
	}
	if o.json {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "%s%v\n", pfx, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		fmt.Fprintf(stdout, "policy drift: %s compared with the account, the directory and the creds files\n", l.path)
		printState(stdout, "account", st.account)
		printState(stdout, "directory", st.directory)
		printState(stdout, "issued", st.issued)
		fmt.Fprintln(stdout, "Not covered by drift:")
		for _, n := range driftNotCovered {
			fmt.Fprintln(stdout, "  - "+n)
		}
		if rep.Drift {
			fmt.Fprintln(stdout, "drift found: `mashctl policy repair` makes the repairs (additive, except that it clears labels the file does not name on declared endpoints) and lists the rest; drift itself fixes nothing.")
		} else {
			fmt.Fprintln(stdout, "no drift: the three states match the policy file.")
		}
	}
	if rep.Drift {
		return 1
	}
	return 0
}

// repairHook is a test seam: called after the policy snapshot is taken and again after apply, so a test can change the
// policy file between the steps. It does nothing in production.
var repairHook = func(stage string) {}

// runPolicyRepair is `mashctl policy repair`: in three steps, additive except that it clears the labels the file does not
// name on an endpoint the file declares; never a revoke or a delete of a credential, key file or record. With --dry-run it
// writes nothing and prints what a real run would do. It is not a transaction: each step is safe to repeat and a failed one leaves the earlier ones done.
func runPolicyRepair(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	pfx := "mashctl: policy repair: "
	o, err := parseApplyArgs("repair", args)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, repairUsage())
		return 2
	}
	if sv := o.servers; sv != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(sv)); err != nil || n < 1 {
			fmt.Fprintf(stderr, "%s--servers must be a whole number of servers, 1 or more (the value is not shown)\n%s", pfx, repairUsage())
			return 2
		}
	}
	l, ok := loadPolicy(pfx, o.file, env, lookup, stderr)
	if !ok {
		return 1
	}
	pf := func(format string, a ...any) { fmt.Fprintf(stdout, format, a...) }
	pf("policy repair: %s\n", l.path)
	if o.dryRun {
		pf("%s\n", repairHeader)
		pf("dry run: nothing is written (no directory change); these are the changes a real run would make.\n")
	} else {
		pf("%s\n", repairHeader)
	}

	// Step 1 is one code path with apply, with the same flags, so every refusal holds (an applied zone policy, a zoned
	// record, a failing policy test).
	var applyOut bytes.Buffer
	repairHook("snapshot")
	code := runPolicyApplyOpts("apply", o, &policySnapshot{path: l.path, f: l.f, c: l.c}, env, lookup, &applyOut, stderr)
	repairHook("applied")
	pf("step 1, account (the same code path as `mashctl policy apply`, same flags):\n")
	for _, line := range strings.Split(strings.TrimRight(applyOut.String(), "\n"), "\n") {
		pf("  %s\n", line)
	}
	if code != 0 {
		pf("repair stopped at step 1: apply did not complete (exit %d). Nothing was revoked or deleted; steps 2 and 3 were not run. Fix what apply says and run repair again.\n", code)
		pf("%s\n", repairNotATransaction)
		return 1
	}

	// Step 2: directory records of endpoints present in both the file and the directory.
	exit := 0
	pf("step 2, directory records:\n")
	adm, actx, closeAdmin, err := ctl.OpenAdmin("mashctl", l.cfg)
	if err != nil {
		pf("  could not open the directory: %v\n", err)
		exit = 1
	} else {
		recs, lerr := adm.List(actx)
		if lerr != nil {
			pf("  could not read the directory: %v\n", lerr)
			exit = 1
		} else {
			findings, fixes := directoryFindings(l.c, recs)
			if rc := repairDirectory(actx, adm, fixes, o.dryRun, pf); rc != 0 {
				exit = 1
			}
			fixed := map[string]bool{}
			for _, f := range fixes {
				fixed[f.Name] = true
			}
			var left []apply.Finding
			for _, f := range findings {
				if !(fixed[f.Subject] && f.Kind == kindLabelDiffers) {
					left = append(left, f)
				}
			}
			if len(left) > 0 {
				pf("  reported, not repaired:\n")
				for _, f := range left {
					pf("    %s %s: %s\n", f.Kind, f.Subject, f.Detail)
				}
			}
		}
		closeAdmin()
	}

	// Step 3: stale creds are listed with the command, never run: a reissue replaces a credential file.
	pf("step 3, issued credentials:\n")
	findings, _, stale := issuedFindings(l.keys, l.c, l.cfg.Owner, credsPathFunc(l.cfg, ""))
	if len(stale) == 0 && len(findings) == 0 {
		pf("  nothing to do\n")
	}
	for _, f := range findings {
		if f.Kind == kindCredsStale {
			pf("  %s is stale: %s\n", f.Subject, f.Detail)
		} else {
			pf("  %s: %s\n", f.Subject, f.Detail)
		}
	}
	if len(stale) > 0 {
		cmd := "mashctl reissue " + strings.Join(stale, " ")
		if o.file != "" {
			cmd += " --policy " + shellQuote(o.file)
		}
		pf("  not executed; to reissue them run: %s\n", cmd)
	}
	pf("%s\n", repairNotATransaction)
	pf("run `mashctl policy drift` to see what is left.\n")
	return exit
}

// repairHeader is printed at the start of every run, so the one removal repair makes is never a surprise.
const repairHeader = "repair is additive EXCEPT that it clears every label the file does not name on an endpoint the file declares (the file is the source of truth for a declared endpoint); every change is listed below. It never touches an endpoint the file does not declare."

const repairNotATransaction = "repair is not a transaction across NATS claims, JetStream resources and local files: each step is idempotent, a step that fails leaves the earlier ones done, and running repair again is safe. It never revokes a credential, deletes a key file, a record or a creds file, ; the only thing it removes is a directory label the file does not name on a declared endpoint."

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+=:,@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// repairDirectory applies fixes, or with dryRun only prints them: SetMeta for labels.
// Each change is one line, the same text either way, and a line is printed when the write succeeded (a real run) or would
// be made (a dry run). It returns 1 when a write failed. A dry run makes no call that writes.
func repairDirectory(ctx context.Context, adm *admin.Admin, fixes []dirFix, dryRun bool, pf func(string, ...any)) int {
	if len(fixes) == 0 {
		pf("  nothing to do\n")
		return 0
	}
	rc := 0
	for _, f := range fixes {
		if len(f.Labels) > 0 {
			if !dryRun {
				if _, err := adm.SetMeta(ctx, f.Name, admin.MetaUpdate{Labels: f.Labels}); err != nil {
					pf("  %s: set labels failed: %v\n", f.Name, err)
					rc = 1
					continue
				}
			}
			for _, l := range f.Lines {
				pf("  %s\n", l)
			}
		}
	}
	return rc
}

// shownIdentity is the canonical agent.owner.name of a three-token directory key, and "" for anything else.
func shownIdentity(key string) string {
	if d := sap.Display(key); d != key {
		return d
	}
	return ""
}
