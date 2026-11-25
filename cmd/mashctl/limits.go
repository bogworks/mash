package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const limitsHelp = `usage: mashctl limits <command>
commands:
  show                   print the limits in force
  apply [prefix]         raise existing inboxes to the configured limits
Run mashctl limits <command> --help for a command's options.
`

var limitsShowSpec = epSpec{name: "show", verb: "limits show", min: 0, max: 0, offline: true}

var limitsApplySpec = epSpec{
	name: "apply", verb: "limits apply", flags: []string{"--dry-run"}, min: 0, max: 1, need: "prefix",
	check: func(in *epInput) error {
		if len(in.pos) == 0 {
			return nil
		}
		p, err := parsePrefix(in.pos[0])
		if err != nil {
			return err
		}
		in.prefix = p
		return nil
	},
}

// parsePrefix reads an owner, an owner.group or one dotted address; acme.* means acme. The text is never shown in an
// error: a mistyped one may be a secret.
func parsePrefix(arg string) ([]string, error) {
	bad := cliframe.Usagef("not an owner, owner.group or owner.group.name prefix (the value is not shown): write acme, acme.demo or acme.demo.bob")
	parts := strings.Split(strings.TrimSuffix(arg, ".*"), ".")
	if len(parts) > 3 {
		return nil, bad
	}
	for i, p := range parts {
		o, g, n := "o", "g", "n"
		switch i {
		case 0:
			o = p
		case 1:
			o, g = parts[0], p
		case 2:
			o, g, n = parts[0], parts[1], p
		}
		if _, err := sap.NewAddress(o, g, n); err != nil {
			return nil, bad
		}
	}
	return parts, nil
}

// underPrefix reports whether a is the endpoint p names, or under the owner or owner.group it names.
func underPrefix(a sap.Address, p []string) bool {
	switch len(p) {
	case 0:
		return true
	case 1:
		return a.Owner == p[0]
	case 2:
		return a.Owner == p[0] && a.Group == p[1]
	}
	return a.Owner == p[0] && a.Group == p[1] && a.Instance == p[2]
}

// runLimits is `mashctl limits <show|apply>`.
func runLimits(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, limitsHelp)
		return 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, limitsHelp)
		return 0
	}
	rest := args[1:]
	switch args[0] {
	case "show":
		return runLimitsShow(rest, g, env, lookup, stdout, stderr)
	case "apply":
		return runLimitsApply(rest, g, env, lookup, stdout, stderr)
	}
	if strings.HasPrefix(args[0], "-") {
		return cliframe.Report(stdout, stderr, unknownFlag(args[0]), jsonRequested(args))
	}
	return cliframe.Report(stdout, stderr, cliframe.Usagef("mashctl limits takes show or apply").WithHint("mashctl limits --help"), jsonRequested(rest))
}

// sizeText is a byte count the way a person says it: 16 MiB.
func sizeText(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n >= u.mult && n%u.mult == 0 {
			return strconv.FormatInt(n/u.mult, 10) + " " + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// ageText is a duration the way a person says it: 30 days.
func ageText(d time.Duration) string {
	hours := int(d / time.Hour)
	switch {
	case d <= 0:
		return "unlimited"
	case d%time.Hour == 0 && hours%24 == 0:
		return count(hours/24, "day")
	case d%time.Hour == 0:
		return count(hours, "hour")
	}
	return d.String()
}

// ageJSON is a duration for JSON: 720h when it is whole hours, else the Go form.
func ageJSON(d time.Duration) string {
	if d > 0 && d%time.Hour == 0 {
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	}
	return d.String()
}

// runLimitsShow is `mashctl limits show`: the limits config.json gives (else the defaults). It reads no server.
func runLimitsShow(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	in, err := parseEpInput(limitsShowSpec, args)
	jsonOut := in.json || g.JSON
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args))
	}
	cfg, err := loadCtlConfig(env, lookup, false)
	if err != nil {
		return cliframe.Report(stdout, stderr, failErr(err), jsonOut)
	}
	l := cfg.Limits
	if l == (store.Limits{}) {
		l = store.DefaultLimits()
	}
	if jsonOut {
		writeJSON(stdout, struct {
			Bytes int64  `json:"inbox_bytes"`
			Age   string `json:"inbox_age"`
		}{l.InboxMaxBytes, ageJSON(l.InboxMaxAge)})
		return 0
	}
	fmt.Fprintf(stdout, "inbox size:  %s\ninbox age:   %s\n", sizeText(l.InboxMaxBytes), ageText(l.InboxMaxAge))
	return 0
}

// runLimitsApply is `mashctl limits apply [prefix]`: it raises existing inboxes to the configured limits and never
// lowers one.
func runLimitsApply(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(limitsApplySpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		recs, bad, err := rt.adm.List(rt.ctx)
		if err != nil {
			return failErr(err)
		}
		for _, b := range bad {
			b.Warn(stderr, "mashctl")
		}
		var addrs []sap.Address
		for _, r := range recs {
			if a := recordAddress(r); underPrefix(a, rt.in.prefix) {
				addrs = append(addrs, a)
			}
		}
		if len(addrs) == 0 && len(rt.in.prefix) == 3 {
			a, err := sap.NewAddress(rt.in.prefix[0], rt.in.prefix[1], rt.in.prefix[2])
			if err != nil {
				return failErr(err)
			}
			return notFound(a)
		}
		dry := rt.in.flags["--dry-run"]
		var lines []string
		var lerr error
		if len(addrs) > 0 {
			lines, lerr = rt.adm.ApplyLimitsDry(rt.ctx, dry, addrs...)
		}
		var changed []string
		for _, l := range lines {
			if !strings.HasSuffix(l, ": unchanged") && !strings.Contains(l, "warning:") {
				changed = append(changed, l)
			}
		}
		n := len(changed)
		switch {
		case rt.in.json && lerr != nil:
		case rt.in.json:
			out := struct {
				Raised     int  `json:"raised"`
				WouldRaise *int `json:"would_raise,omitempty"`
			}{Raised: n}
			if dry {
				out.Raised, out.WouldRaise = 0, &n
			}
			writeJSON(rt.stdout, out)
		case n == 0:
			if lerr == nil {
				fmt.Fprintln(rt.stdout, "Nothing to raise")
			}
		case dry:
			fmt.Fprintf(rt.stdout, "Would raise %s\nNothing was changed.\n", count(n, "inbox"))
		default:
			fmt.Fprintf(rt.stdout, "Raised %s\n", count(n, "inbox"))
		}
		return failErr(lerr)
	})
}
