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
       mashctl policy apply [--dry-run] [--policy FILE] [--sys-creds FILE]
       mashctl policy drift [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]
       mashctl policy repair [--dry-run] [--policy FILE] [--sys-creds FILE] [--servers N]
       (repair is additive EXCEPT that it clears labels the file does not name on endpoints the file declares)
`

// policyOpts are the flags and positional arguments of a policy verb.
type policyOpts struct {
	file string
	args []string
}

func parsePolicyArgs(args []string) (policyOpts, error) {
	var o policyOpts
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
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown %s", config.RejectedArg(a))
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
func runPolicy(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	usage := func() int { fmt.Fprint(stderr, policyUsage); return 2 }
	if len(args) == 0 {
		return usage()
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "check", "test":
	case "apply":
		return runPolicyApply(verb, rest, env, lookup, stdout, stderr)
	case "drift":
		return runPolicyDrift(rest, env, lookup, stdout, stderr)
	case "repair":
		return runPolicyRepair(rest, env, lookup, stdout, stderr)
	default:
		return usage()
	}
	o, err := parsePolicyArgs(rest)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: %v\n", verb, err)
		return usage()
	}
	if len(o.args) != 0 {
		return usage()
	}
	cfg, err := loadConfig(env, lookup)
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
		return policyCheck(path, data, cfg.Owner, stdout, stderr)
	}
	f, err := policy.Parse(data)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy %s: %s: %v\n", verb, path, err)
		return 1
	}
	return policyTest(f, stdout)
}

// policyCheck validates the file and compiles it. owner is the configured owner; a placeholder stands in when none is
// configured. A file that carries a zone does not parse (ADR 0091): the error says what to remove.
func policyCheck(path string, data []byte, owner string, stdout, stderr io.Writer) int {
	f, err := policy.Parse(data)
	if err != nil {
		// Parse folds the Check errors into one "policy: a; b" error: show them one per line.
		// A segment with no ": " is the rest of the previous message (a message may hold "; ").
		msg := strings.TrimPrefix(err.Error(), "policy: ")
		var lines []string
		for _, m := range strings.Split(msg, "; ") {
			if len(lines) > 0 && !strings.Contains(m, ": ") {
				lines[len(lines)-1] += "; " + m
				continue
			}
			lines = append(lines, m)
		}
		for _, m := range lines {
			fmt.Fprintf(stdout, "error: %s\n", m)
		}
		return 1
	}
	rep := f.Check()
	for _, w := range rep.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	if owner == "" {
		owner = "owner"
	}
	if _, err := policy.Compile(f, owner); err != nil {
		fmt.Fprintf(stderr, "mashctl: policy check: %v\n", err)
		return 1
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
