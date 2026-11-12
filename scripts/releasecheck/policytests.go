package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/release"
)

// The policy-tests step (REQ-020): the tests of AT-001, AT-003, AT-004, AT-005, AT-010, AT-011 and AT-020 run on every
// release, and on every change of the NATS server version mash ships with, and the release is refused when one fails.
// policytests.txt is the one list; a required test that was renamed, deleted or skipped did not run, and that refuses.

const (
	policyListFile    = "scripts/releasecheck/policytests.txt"
	policyTestTimeout = "15m"
)

// policyEntry is one row of policytests.txt.
type policyEntry struct {
	AT, Pkg, Test string
	Required      bool
	Note          string
}

// parsePolicyList reads policytests.txt: blank lines and # comments are skipped.
func parsePolicyList(r io.Reader) ([]policyEntry, error) {
	var out []policyEntry
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			return nil, fmt.Errorf("%s line %d: want <AT id> <package> <test> <required|pending>", policyListFile, n)
		}
		e := policyEntry{AT: f[0], Pkg: f[1], Test: f[2], Note: strings.Join(f[4:], " ")}
		if !regexp.MustCompile(`^AT-[0-9]{3}$`).MatchString(e.AT) {
			return nil, fmt.Errorf("%s line %d: %q is not an AT id (AT-NNN)", policyListFile, n, e.AT)
		}
		switch f[3] {
		case "required":
			e.Required = true
			if e.Pkg == "-" || e.Test == "-" {
				return nil, fmt.Errorf("%s line %d: a required row names a package and a test", policyListFile, n)
			}
		case "pending":
		default:
			return nil, fmt.Errorf("%s line %d: status %q is not required or pending", policyListFile, n, f[3])
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no tests", policyListFile)
	}
	return out, nil
}

// runPattern is the -run pattern that selects exactly the named test: each slash-separated element anchored and quoted.
func runPattern(test string) string {
	parts := strings.Split(test, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

type testEvent struct {
	Action, Package, Test string
}

// outcome reads a `go test -json` stream for the named test: why it did not pass, or "" when it started and passed.
func outcome(stream []byte, test string) string {
	var started, passed, failed, skipped, pkgFailed bool
	sc := bufio.NewScanner(bytes.NewReader(stream))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var ev testEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue // build output and other text
		}
		switch {
		case ev.Test == "" && ev.Action == "fail":
			pkgFailed = true
		case ev.Test != test:
		case ev.Action == "run":
			started = true
		case ev.Action == "pass":
			passed = true
		case ev.Action == "fail":
			failed = true
		case ev.Action == "skip":
			skipped = true
		}
	}
	switch {
	case failed:
		return "failed"
	case skipped:
		return "not run: skipped"
	case !started || !passed:
		if pkgFailed {
			return "failed: the package failed before the test passed (build or timeout)"
		}
		return "not run: no started-and-passed event (renamed or deleted?)"
	case pkgFailed:
		return "failed: the package failed"
	}
	return ""
}

// pendingTestRE finds a test function named for an AT that the list still calls pending.
func pendingTestRE(at string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^func (Test\w*` + strings.ReplaceAll(at, "-", "") + `(?:[^0-9]\w*)?)\(`)
}

// testsForAT lists, under dir, the test functions named for the AT id (TestAT010..., not TestAT0100).
func testsForAT(dir, at string) []string {
	re := pendingTestRE(at)
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			rel, _ := filepath.Rel(dir, p)
			found = append(found, filepath.ToSlash(rel)+":"+string(m[1]))
		}
		return nil
	})
	return found
}

// policyRunner runs `go test -json` for one entry in dir and returns the JSON stream. A seam for the tests.
type policyRunner func(ctx context.Context, dir string, e policyEntry) ([]byte, error)

// runGoTest runs the entry under the gate's pinned Go settings. -race needs cgo, so CGO_ENABLED is the one setting
// of the pin that is overridden (the same -race run as CI's test job).
func runGoTest(ctx context.Context, dir string, e policyEntry) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "test", "-race", "-count=1", "-json", "-timeout", policyTestTimeout, "-run", runPattern(e.Test), e.Pkg)
	cmd.Dir = dir
	cmd.Env = append(release.PinnedEnv(os.Environ()), "CGO_ENABLED=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return nil, op(fmt.Errorf("go test %s: %w", e.Pkg, err))
	}
	return append(stdout.Bytes(), stderr.Bytes()...), nil
}

// judgePolicy decides every row from the stream its run gave, prints one summary line per AT, and returns the problems.
func judgePolicy(out io.Writer, entries []policyEntry, streams map[int][]byte, tests func(at string) []string) error {
	var order []string
	status := map[string]string{} // AT -> pass | FAIL | pending
	detail := map[string][]string{}
	var problems []string
	for i, e := range entries {
		if _, seen := status[e.AT]; !seen {
			order = append(order, e.AT)
			status[e.AT] = "pending"
		}
		if !e.Required {
			if found := tests(e.AT); len(found) > 0 {
				status[e.AT] = "FAIL"
				msg := fmt.Sprintf("%s is pending in %s but %s exists: promote to required", e.AT, policyListFile, strings.Join(found, ", "))
				detail[e.AT] = append(detail[e.AT], "exists: promote to required ("+strings.Join(found, ", ")+")")
				problems = append(problems, msg)
			} else {
				detail[e.AT] = append(detail[e.AT], "pending: "+e.Note)
			}
			continue
		}
		if status[e.AT] == "pending" {
			status[e.AT] = "pass"
		}
		if why := outcome(streams[i], e.Test); why != "" {
			status[e.AT] = "FAIL"
			detail[e.AT] = append(detail[e.AT], e.Pkg+" "+e.Test+": "+why)
			problems = append(problems, fmt.Sprintf("%s %s %s: %s", e.AT, e.Pkg, e.Test, why))
		} else {
			detail[e.AT] = append(detail[e.AT], e.Test)
		}
	}
	for _, at := range order {
		fmt.Fprintf(out, "policy %-7s %s  %s\n", status[at], at, strings.Join(detail[at], "; "))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}

// policyTests runs the required rows of policytests.txt under dir and judges them. A failure to start go is an
// operational error (exit 4); a test that failed, did not run or a list that rotted is a refusal.
func policyTests(ctx context.Context, o Options, dir string) error {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(policyListFile)))
	if err != nil {
		return fmt.Errorf("the policy test list is missing: %w", err)
	}
	entries, err := parsePolicyList(f)
	f.Close()
	if err != nil {
		return err
	}
	run := o.policyRun
	if run == nil {
		run = runGoTest
	}
	streams := map[int][]byte{}
	for i, e := range entries {
		if !e.Required {
			continue
		}
		b, err := run(ctx, dir, e)
		if err != nil {
			return err
		}
		streams[i] = b
	}
	return judgePolicy(o.Out, entries, streams, func(at string) []string { return testsForAT(dir, at) })
}

// policyTestsStep is the gate's step: the tests run in a fresh clone of x, so they judge the candidate, not the
// working tree.
func policyTestsStep(ctx context.Context, o Options, x string) error {
	if o.policyTests != nil {
		return o.policyTests(ctx, o, x)
	}
	src, work, err := freshClone(o.Repo, x)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	return policyTests(ctx, o, src)
}

// policyTestsAlone is `release-check --policy-tests`: the step against the current checkout, the check CI runs when
// the NATS server version changes.
func policyTestsAlone(ctx context.Context, out io.Writer) error {
	o := Options{Out: out}
	r := &report{out: out}
	start := time.Now()
	r.step("the policy tests (REQ-020) pass", policyTests(ctx, o, "."))
	fmt.Fprintf(out, "policy-tests took %s\n", time.Since(start).Round(time.Second))
	return r.result("the policy tests")
}
