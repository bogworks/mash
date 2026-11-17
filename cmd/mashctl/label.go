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
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const labelUsage = `usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
`

type labelOpts struct {
	addr  sap.Address
	dry   bool
	set   map[string]string // label: k=v
	unset []string          // unlabel: keys
}

func parseLabelArgs(unlabel bool, args []string) (labelOpts, error) {
	o := labelOpts{set: map[string]string{}}
	var rest []string
	for _, a := range args {
		switch {
		case a == "--dry-run":
			o.dry = true
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown %s", config.RejectedArg(a))
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		return o, errors.New("want an address (owner.group.instance) and at least one label")
	}
	addr, err := parseAddr(rest[0])
	if err != nil {
		return o, err
	}
	o.addr = addr
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

// runLabel is `mashctl label` and `mashctl unlabel`. They set and clear labels on the endpoint's directory record,
// which is where its labels live: nothing is read from or written to a file, and no creds are touched. The endpoint
// must have a directory record.
func runLabel(unlabel bool, args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
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
		fmt.Fprintf(stderr, "mashctl: %s %s: %s\n", verb, o.addr, fmt.Sprintf(format, a...))
		return 1
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	recs, err := adm.List(ctx)
	if err != nil {
		return fail("%v", err)
	}
	rec, ok := findRecord(recs, o.addr)
	if !ok {
		return fail("no directory record for %s", o.addr)
	}

	// The labels the endpoint would end with, checked as the directory checks them.
	next := map[string]string{}
	for k, v := range rec.Labels {
		next[k] = v
	}
	var unset []string
	for _, k := range o.unset {
		if _, has := next[k]; !has {
			fmt.Fprintf(stderr, "mashctl: unlabel %s: label %s is not set\n", o.addr, k)
			continue
		}
		delete(next, k)
		unset = append(unset, k)
	}
	changes := map[string]string{} // what SetMeta is given: k=v sets, k="" clears
	for k, v := range o.set {
		if old, has := next[k]; !has || old != v {
			changes[k] = v
		}
		next[k] = v
	}
	for _, k := range unset {
		changes[k] = ""
	}
	if err := store.ValidLabels(next); err != nil {
		return fail("%v", err)
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "%s: nothing to change\n", o.addr)
		return 0
	}
	done := map[bool]string{false: "labeled", true: "unlabeled"}[unlabel]
	if o.dry {
		fmt.Fprintf(stdout, "dry run: would have %s %s (nothing written)\n", done, o.addr)
		return 0
	}
	if _, err := adm.SetMeta(ctx, o.addr, admin.MetaUpdate{Labels: changes}); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "%s %s: %s\n", done, o.addr, labelSummary(o.set, unset))
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

// findRecord is the directory record of the endpoint at addr.
func findRecord(recs []store.Record, addr sap.Address) (store.Record, bool) {
	for _, r := range recs {
		if ra, err := r.Address(); err == nil && ra == addr {
			return r, true
		}
	}
	return store.Record{}, false
}
