package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/policy"
)

const policyUsage = `usage: mashctl policy check [--policy FILE]
       mashctl policy test [--policy FILE]
       mashctl policy explain FROM TO [--policy FILE]
       mashctl policy show [--json] [--budget] [--policy FILE]
       mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE]
       mashctl policy diff [--policy FILE] [--sys-creds FILE]
       mashctl policy drift [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]
       mashctl policy repair [--policy FILE] [--sys-creds FILE] [--servers N] [--keep-zone ZONE]... [--remove-zone ZONE]... [--replace-zone-key ZONE]...
`

// policyOpts are the flags and positional arguments of a policy verb.
type policyOpts struct {
	file         string
	json, budget bool
	args         []string
}

func parsePolicyArgs(args []string, allowed ...string) (policyOpts, error) {
	var o policyOpts
	ok := func(f string) bool {
		for _, a := range allowed {
			if a == f {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--policy" || strings.HasPrefix(a, "--policy="):
			if v, has := strings.CutPrefix(a, "--policy="); has {
				o.file = v
			} else if i+1 < len(args) {
				i++
				o.file = args[i]
			} else {
				return o, errors.New("--policy needs a file")
			}
			if o.file == "" {
				return o, errors.New("--policy needs a file")
			}
		case a == "--json" && ok("--json"):
			o.json = true
		case a == "--budget" && ok("--budget"):
			o.budget = true
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			o.args = append(o.args, a)
		}
	}
	return o, nil
}

// policyPath is --policy, else MASH_POLICY, else <config dir>/policy.yaml.
func policyPath(flagVal string, env func(string) string, cfg config.Config) (string, error) {
	switch {
	case flagVal != "":
		return flagVal, nil
	case env("MASH_POLICY") != "":
		return env("MASH_POLICY"), nil
	case cfg.Dir != "":
		return filepath.Join(cfg.Dir, "policy.yaml"), nil
	}
	return "", config.ErrNoConfigDir
}

// runPolicy handles `mashctl policy ...`. It needs no server and no admin credentials.
func runPolicy(args []string, env func(string) string, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprint(stderr, policyUsage); return 2 }
	if len(args) == 0 {
		return usage()
	}
	verb, rest := args[0], args[1:]
	var allowed []string
	switch verb {
	case "check", "test", "explain":
	case "show":
		allowed = []string{"--json", "--budget"}
	case "apply", "diff":
		return runPolicyApply(verb, rest, env, stdout, stderr)
	case "drift":
		return runPolicyDrift(rest, env, stdout, stderr)
	case "repair":
		return runPolicyRepair(rest, env, stdout, stderr)
	default:
		return usage()
	}
	o, err := parsePolicyArgs(rest, allowed...)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: %v\n", verb, err)
		return usage()
	}
	if want := map[string]int{"explain": 2}[verb]; len(o.args) != want {
		return usage()
	}
	cfg, err := config.Load(env, env("HOME"))
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := policyPath(o.file, env, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: no policy file: use --policy FILE or MASH_POLICY (%v)\n", verb, err)
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: %v\n", verb, err)
		return 1
	}
	if verb == "check" {
		return policyCheck(path, data, stdout, stderr)
	}
	f, err := policy.Parse(data)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: %s: %v\n", verb, path, err)
		return 1
	}
	switch verb {
	case "test":
		return policyTest(f, stdout)
	case "explain":
		x, err := policy.Explain(f, o.args[0], o.args[1])
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: policy explain: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, x.Text)
		fmt.Fprintln(stdout, coverageNote)
		return 0
	}
	return policyShow(f, cfg.Owner, o, stdout, stderr)
}

// coverageNote says what rules police today (ADR 0054, ADR 0055).
const coverageNote = "note: rules cover mail only; prompt and status probes are not covered by rules yet (open)"

func policyCheck(path string, data []byte, stdout, stderr io.Writer) int {
	f, err := policy.Parse(data)
	if err != nil {
		// Parse folds the Check errors into one "policy: a; b" error: show them one per line.
		msg := strings.TrimPrefix(err.Error(), "policy: ")
		for _, m := range strings.Split(msg, "; ") {
			fmt.Fprintf(stdout, "error: %s\n", m)
		}
		return 1
	}
	rep := f.Check()
	for _, w := range rep.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	fmt.Fprintf(stdout, "check: OK - %d endpoints, %d rules\n", len(f.Endpoints), len(f.Rules))
	return 0
}

func policyTest(f *policy.File, stdout io.Writer) int {
	res := policy.RunTests(f)
	if len(res) == 0 {
		fmt.Fprintln(stdout, "test: no tests in the file")
		return 0
	}
	code := 0
	for _, r := range res {
		if r.Pass {
			fmt.Fprintf(stdout, "PASS %s->%s expect %s\n", r.From, r.To, r.Expect)
			continue
		}
		code = 1
		fmt.Fprintf(stdout, "FAIL %s->%s expect %s got %s: %s\n", r.From, r.To, r.Expect, r.Got, r.Text)
	}
	return code
}

func policyShow(f *policy.File, owner string, o policyOpts, stdout, stderr io.Writer) int {
	if owner == "" {
		fmt.Fprintln(stderr, `mashctl: policy show: no owner (config.json "owner" or MASH_OWNER)`)
		return 1
	}
	c, err := policy.Compile(f, owner)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy show: %v\n", err)
		return 1
	}
	if o.json {
		b, err := c.JSON()
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: policy show: %v\n", err)
			return 1
		}
		stdout.Write(b)
	} else {
		fmt.Fprint(stdout, c.Matrix())
		fmt.Fprint(stdout, zonesSummary(c))
		fmt.Fprintln(stdout, coverageNote)
	}
	if !o.budget {
		return 0
	}
	rep, err := policy.Budget(c)
	// In --json mode stdout is a JSON document, so the budget report goes to stderr.
	w := stdout
	if o.json {
		w = stderr
	}
	fmt.Fprintf(w, "budget: account JWT %d of %d bytes (%d zones); worst user JWT %d of %d bytes (%s)\n",
		rep.AccountJWTBytes, rep.AccountLimit, rep.Zones, rep.WorstUserJWTBytes, rep.UserLimit, rep.WorstUser)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy show: %v\n", err)
		return 1
	}
	if !rep.OK {
		fmt.Fprintln(stderr, "mashctl: policy show: over budget")
		return 1
	}
	return 0
}

func zonesSummary(c *policy.Compiled) string {
	var b strings.Builder
	b.WriteString("\nzones:\n")
	for _, z := range c.Zones {
		fmt.Fprintf(&b, "  %s: %s\n", z.Name, strings.Join(z.Endpoints, ", "))
	}
	b.WriteString("\nwildcard zones (every endpoint of the zone may mail the whole recipient zone):\n")
	any := false
	for _, z := range c.Zones {
		if len(z.Wildcards) > 0 {
			any = true
			fmt.Fprintf(&b, "  %s -> %s\n", z.Name, strings.Join(z.Wildcards, ", "))
		}
	}
	if !any {
		b.WriteString("  (none)\n")
	}
	b.WriteString("\ntags per endpoint (the exceptions its user JWT carries):\n")
	for _, e := range c.Endpoints {
		fmt.Fprintf(&b, "  %s: %d\n", e.Name, len(e.Tags))
	}
	return b.String()
}
