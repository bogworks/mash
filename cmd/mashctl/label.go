package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/policyfile"
	"github.com/bogworks/mash/internal/store"
)

const labelUsage = `usage: mashctl label NAME k=v [k=v]... [--policy FILE] [--dry-run] [--sync]
       mashctl unlabel NAME k [k]... [--policy FILE] [--dry-run] [--sync]
`

type labelOpts struct {
	name, file string
	dry, sync  bool
	set        map[string]string // label: k=v
	unset      []string          // unlabel: keys
}

func parseLabelArgs(unlabel bool, args []string) (labelOpts, error) {
	o := labelOpts{set: map[string]string{}}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			o.dry = true
		case a == "--sync":
			o.sync = true
		case a == "--policy" || strings.HasPrefix(a, "--policy="):
			if v, has := strings.CutPrefix(a, "--policy="); has {
				o.file = v
			} else if i+1 < len(args) {
				i++
				o.file = args[i]
			}
			if o.file == "" {
				return o, errors.New("--policy needs a file")
			}
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown %s", config.RejectedArg(a))
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		return o, errors.New("want NAME and at least one label")
	}
	o.name = rest[0]
	seen := map[string]bool{}
	for i, a := range rest[1:] {
		k, v, hasV := strings.Cut(a, "=")
		if unlabel {
			if hasV {
				return o, fmt.Errorf("unlabel wants keys (k), not k=v: key argument %d has a value", i+1)
			}
		} else if !hasV || v == "" {
			return o, fmt.Errorf("label wants k=v: label argument %d is not k=v (the value is not shown)", i+1)
		}
		if !store.ValidLabelKey(k) {
			return o, fmt.Errorf("label key in argument %d: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)", i+1)
		}
		if seen[k] {
			return o, fmt.Errorf("label %q given twice", k)
		}
		seen[k] = true
		if unlabel {
			o.unset = append(o.unset, k)
		} else {
			o.set[k] = v
		}
	}
	return o, nil
}

// afterLabelLoad is a test seam called after the edit is compiled and before the save.
var afterLabelLoad func()

// runLabel is `mashctl label` and `mashctl unlabel`. It edits the policy file, the source of truth; nothing touches
// the server or any creds unless --sync is given, and then only the directory record's labels, after the save. What
// the edit leaves to do is printed, always including the directory labels, which `mashctl policy apply` does not reconcile.
func runLabel(unlabel bool, args []string, env func(string) string, stdout, stderr io.Writer) int {
	verb, usage := "label", labelUsage
	if unlabel {
		verb = "unlabel"
	}
	o, err := parseLabelArgs(unlabel, args)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: %s: %v\n%s", verb, err, usage)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: %s %s: %s\n", verb, o.name, fmt.Sprintf(format, a...))
		return 1
	}
	if !config.ValidName(o.name) {
		fmt.Fprintf(stderr, "mashctl: %s: NAME is not a valid endpoint name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)\n", verb)
		return 2
	}
	cfg, err := loadCtlConfig(env, false)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := policyPath(o.file, env, cfg)
	if err != nil {
		return fail("no policy file: use --policy FILE or MASH_POLICY (%v)", err)
	}
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
	ep, ok := beforeFile.Endpoints[o.name]
	if !ok {
		return fail("endpoint %q is not declared in %s", o.name, path)
	}

	// The labels the endpoint would end with, checked as the directory checks them.
	next := map[string]string{}
	for k, v := range ep.Labels {
		next[k] = v
	}
	var unset []string
	for _, k := range o.unset {
		if _, has := next[k]; !has {
			fmt.Fprintf(stderr, "mashctl: unlabel %s: label %s is not set\n", o.name, k)
			continue
		}
		delete(next, k)
		unset = append(unset, k)
	}
	for k, v := range o.set {
		next[k] = v
	}
	if err := store.ValidLabels(next); err != nil {
		return fail("%v", err)
	}
	if err := doc.SetLabels(o.name, o.set, unset); err != nil {
		return fail("%v", err)
	}
	afterBytes, _ := doc.Bytes()
	// The directory record's change, as a set-meta command (set: k=v; unset: k=-).
	var kv []string
	for k, v := range o.set {
		kv = append(kv, k+"="+v)
	}
	for _, k := range o.unset {
		kv = append(kv, k+"=-")
	}
	sort.Strings(kv)
	setMeta := "mashctl set-meta " + o.name + " --label " + strings.Join(kv, " --label ")
	if string(afterBytes) == string(beforeBytes) {
		fmt.Fprintf(stdout, "%s: nothing to change\n", o.name)
		if o.sync && !o.dry {
			// The policy already says it; --sync still reconciles the record (this is the re-run the follow-up names).
			return syncLabels(o, verb, setMeta, cfg, env, stdout, stderr)
		}
		return 0
	}
	_, after, err := compileBytes(afterBytes, cfg.Owner)
	if err != nil {
		return fail("the edit does not make a valid policy: %v", err)
	}
	if afterLabelLoad != nil {
		afterLabelLoad()
	}
	done := map[bool]string{false: "labeled", true: "unlabeled"}[unlabel]
	if o.dry {
		fmt.Fprintf(stdout, "dry run: would have %s %s (nothing written)\n", done, o.name)
		if o.sync {
			fmt.Fprintf(stdout, "dry run: would then update the directory labels for %s (connected to nothing)\n", o.name)
		}
	} else {
		if err := doc.Save(); err != nil {
			if errors.Is(err, policyfile.ErrChanged) {
				return fail("%v: nothing was changed. Re-run: mashctl %s %s", err, verb, strings.Join(args, " "))
			}
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", done, o.name, labelSummary(o.set, unset))
	}
	if !o.sync {
		fmt.Fprintf(stdout, "directory labels not updated: run %s (or re-run with --sync) to refresh who --long\n", setMeta)
	}
	for _, l := range consequences(before, after) {
		fmt.Fprintln(stdout, l)
	}
	if o.sync && !o.dry {
		return syncLabels(o, verb, setMeta, cfg, env, stdout, stderr)
	}
	return 0
}

// syncLabels applies the label change to the endpoint's directory record, after the policy file is saved. The file
// is the source of truth and the record a compiled output, so a failure here leaves the file saved and says how to
// retry.
func syncLabels(o labelOpts, verb, setMeta string, cfg config.Config, env func(string) string, stdout, stderr io.Writer) int {
	retry := func(err error) int {
		fmt.Fprintf(stderr, "mashctl: %s %s: the policy file is saved but the directory labels were not updated: %v. Retry: %s\n", verb, o.name, err, setMeta)
		return 1
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return retry(err)
	}
	defer closeAdmin()
	recs, err := adm.List(ctx)
	if err != nil {
		return retry(err)
	}
	if _, ok := findRecord(recs, o.name); !ok {
		fmt.Fprintf(stdout, "%s has no directory record yet; labels will be set when it is added\n", o.name)
		return 0
	}
	labels := map[string]string{}
	for k, v := range o.set {
		labels[k] = v
	}
	for _, k := range o.unset {
		labels[k] = ""
	}
	if _, err := adm.SetMeta(ctx, o.name, admin.MetaUpdate{Labels: labels}); err != nil {
		return retry(err)
	}
	fmt.Fprintf(stdout, "directory labels updated for %s\n", o.name)
	return 0
}

// labelSummary is "k=v k2=v2" for a label, "-k -k2" for an unlabel, sorted.
func labelSummary(set map[string]string, unset []string) string {
	var out []string
	for k, v := range set {
		out = append(out, k+"="+v)
	}
	for _, k := range unset {
		out = append(out, "-"+k)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// findRecord is the directory record whose key is name. The key is the name for a legacy record and the flat
// identity for a new-form one, so a new-form record whose name token is name is not the record for name.
func findRecord(recs []store.Record, name string) (store.Record, bool) {
	for _, r := range recs {
		if r.KeyOrName() == name {
			return r, true
		}
	}
	return store.Record{}, false
}
