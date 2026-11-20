package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// epSpec is one command of `mashctl endpoint` that reads its arguments through parseEpInput.
type epSpec struct {
	name    string
	usage   string   // the text of --help; its first line is the usage line and it ends with an Example
	flags   []string // options without a value, besides --json
	values  []string // options that take a value
	min     int      // fewest positional arguments
	max     int      // most positional arguments, -1 for any number
	need    string   // what a missing positional argument is, for the error
	offline bool     // reads no server
	// check reads the positional arguments and values into in, before anything connects. Its errors are usage errors.
	check func(in *epInput) error
}

// epInput is what the command line of an endpoint command holds.
type epInput struct {
	pos    []string
	flags  map[string]bool
	values map[string]string
	json   bool

	// set by a command's check
	addr     sap.Address
	prefix   []string
	limit    int
	after    uint64
	afterSet bool
	edit     metaEdit
}

// epRuntime is what a command's body works with.
type epRuntime struct {
	in     *epInput
	stdout io.Writer
	stderr io.Writer
	adm    *admin.Admin
	ctx    context.Context
}

// parseEpInput reads args by the spec. -h and --help are handled before this. A positional argument is never echoed in
// an error: a mistyped one may be a secret.
func parseEpInput(s epSpec, args []string) (epInput, error) {
	in := epInput{flags: map[string]bool{}, values: map[string]string{}}
	isFlag := map[string]bool{}
	for _, f := range s.flags {
		isFlag[f] = true
	}
	isValue := map[string]bool{}
	for _, f := range s.values {
		isValue[f] = true
	}
	hint := "mashctl endpoint " + s.name + " --help"
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch {
		case a == "--json":
			in.json = true
		case isFlag[name] && !hasInline:
			in.flags[name] = true
		case isValue[name]:
			v, err := flagValue(args, &i, name, inline, hasInline)
			if err != nil {
				return in, cliframe.Usagef("%v", err).WithHint(hint)
			}
			in.values[name] = v
		case a != "-" && strings.HasPrefix(a, "-"):
			return in, cliframe.Usagef("unknown %s", config.RejectedArg(a)).WithHint(hint)
		default:
			in.pos = append(in.pos, a)
		}
	}
	if len(in.pos) < s.min {
		return in, cliframe.Usagef("missing %s", s.need).WithHint(hint)
	}
	if s.max >= 0 && len(in.pos) > s.max {
		return in, cliframe.Usagef("too many arguments").WithHint(hint)
	}
	if s.check != nil {
		if err := s.check(&in); err != nil {
			if ce, ok := err.(*cliframe.Error); ok && ce.Hint == "" {
				err = ce.WithHint(hint)
			}
			return in, err
		}
	}
	return in, nil
}

// jsonRequested reports whether args hold a bare --json, so a usage error can be answered in JSON. The value after a
// flag that takes one is skipped.
func jsonRequested(args []string, valueFlags ...string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			return true
		}
		for _, f := range valueFlags {
			if args[i] == f {
				i++
				break
			}
		}
	}
	return false
}

// helpAsked reports whether args hold -h or --help as an argument of their own.
func helpAsked(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// runEp is the frame of an endpoint command: help, the arguments, the connection, the body, and the report of its error.
func runEp(s epSpec, args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer, body func(rt *epRuntime) error) int {
	if helpAsked(args) {
		fmt.Fprint(stdout, s.usage)
		return 0
	}
	in, err := parseEpInput(s, args)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args, s.values...))
	}
	rt := &epRuntime{in: &in, stdout: stdout, stderr: stderr}
	if s.offline {
		return cliframe.Report(stdout, stderr, body(rt), in.json)
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return cliframe.Report(stdout, stderr, failErr(err), in.json)
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return cliframe.Report(stdout, stderr, failErr(err), in.json)
	}
	defer closeAdmin()
	rt.adm, rt.ctx = adm, ctx
	return cliframe.Report(stdout, stderr, body(rt), in.json)
}

// failErr is a failure (exit 1) with the text of err, without the program prefix the older commands put on it.
func failErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*cliframe.Error); ok {
		return err
	}
	msg := err.Error()
	for _, p := range []string{"mash: ", "mashctl: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return cliframe.Failf("%s", msg)
}

// parseEpAddress reads an endpoint address: owner.group.name, dotted, exactly three parts.
func parseEpAddress(arg string) (sap.Address, error) {
	a, err := sap.ParseAddress(arg)
	if err != nil {
		return sap.Address{}, cliframe.Usagef("not an endpoint address (the value is not shown): write owner.group.name, for example acme.demo.bob")
	}
	return a, nil
}

// checkAddress is the check of a command whose first positional argument is an address.
func checkAddress(in *epInput) error {
	a, err := parseEpAddress(in.pos[0])
	if err != nil {
		return err
	}
	in.addr = a
	return nil
}

// notFound is the failure of an address with no directory record.
func notFound(a sap.Address) error {
	return cliframe.Failf("no endpoint %s", a).WithHint("mashctl endpoint list")
}

// loadRecord is the directory record of a, with the warnings of the records that could not be read.
func (rt *epRuntime) loadRecord(a sap.Address) (store.Record, error) {
	recs, bad, err := rt.adm.List(rt.ctx)
	if err != nil {
		return store.Record{}, failErr(err)
	}
	for _, b := range bad {
		b.Warn(rt.stderr, "mashctl")
	}
	r, ok := findRecord(recs, a)
	if !ok {
		return store.Record{}, notFound(a)
	}
	return r, nil
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

// stateWord is the state of an endpoint as every command words it.
func stateWord(st admin.EndpointState) string {
	switch {
	case st.Suspended:
		return "suspended"
	case st.Online:
		return "online"
	}
	return "offline"
}

// utcMinute is a stored time as 2026-11-19 09:40, or the stored text when it is not a time.
func utcMinute(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return config.Clean(s)
}

// utcRFC3339 is a stored time as RFC 3339 in UTC, or the stored text when it is not a time.
func utcRFC3339(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}

// messages is "1 message" or "3 messages".
func messages(n uint64) string {
	if n == 1 {
		return "1 message"
	}
	return fmt.Sprintf("%d messages", n)
}

// labelPairs is the labels as k=v joined by commas, in key order; "-" when there are none.
func labelPairs(l map[string]string) string {
	if len(l) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = config.Clean(k) + "=" + config.Clean(l[k])
	}
	return strings.Join(keys, ",")
}
