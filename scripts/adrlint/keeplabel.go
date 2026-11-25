package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// keep-label decides whether a push may leave the decision-confirmed label on a pull request (ADR 0178). The
// label is the reviewer's confirmation of one head's change; it survives a push only if the pull request's OWN change is
// unchanged by it, which is what a rebase does and nothing else does. Every doubt answers "reset": a missing object, a git
// failure, an empty change, a path set or a patch that differs.
//
// The change of a head X against base B is the diff from merge-base(B, X) to X. The label stays when ALL of these hold:
//  1. the git patch-id (--verbatim) of that change is the same for the old and the new head, and not empty;
//  2. the set of changed paths is the same;
//  3. for every changed path, protected or not, its blob is identical in the old and the new head.
//
// The decision is local to one push, so it also needs the chain to be unbroken: the label-reset check run on the OLD head
// must have concluded success (chainOK). Without that, a push whose label-reset failed or was missed would leave a label
// that a later pure rebase then keeps, and the label from an earlier head would cover a change nobody reviewed.
//
// 1 alone is not enough: patch-id ignores line numbers, so the same edit applied to a different repeated block of a file
// keeps the id. 3 compares what the files became. There is no exception for a file the base
// changed meanwhile: the blob would differ and so could a relocated edit; such a rebase resets.

type gitIO func(stdin []byte, args ...string) ([]byte, error)

func realGitIO(dir string) gitIO {
	return func(stdin []byte, args ...string) ([]byte, error) {
		if dir != "" {
			args = append([]string{"-C", dir}, args...)
		}
		cmd := exec.Command("git", args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		// Read at most maxOut+1 bytes: git's output is attacker-sized (a diff of the pull request), and an unbounded read
		// is memory the checker should not spend. Over the cap the process is killed and the answer is an error (a reset).
		out, rerr := io.ReadAll(io.LimitReader(pipe, maxOut+1))
		if len(out) > maxOut {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("git %s: output over %d bytes", args[len(args)-1], maxOut)
		}
		werr := cmd.Wait()
		if rerr != nil {
			return out, rerr
		}
		if werr != nil && stderr.Len() > 0 {
			return out, fmt.Errorf("%w: %s", werr, strings.TrimSpace(stderr.String()))
		}
		return out, werr
	}
}

// maxOut bounds what any one git command may print to the checker.
const maxOut = 16 << 20

// maxDiff caps what keep-label reads of a change; a larger one resets. Binary files are not diffed (the blob comparison of
// every changed path covers their content).
const maxDiff = maxOut

var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// keepLabel returns true when the label may stay; otherwise false and the reason.
func keepLabel(git gitIO, base, before, after string) (bool, string) {
	for name, v := range map[string]string{"base": base, "before": before, "after": after} {
		if !sha40.MatchString(v) {
			return false, name + " is not a full commit sha"
		}
	}
	if before == after {
		return false, "before and after are the same commit: nothing to compare (a label event, not a push)"
	}
	one := func(args ...string) (string, error) {
		out, err := git(nil, args...)
		return strings.TrimSpace(string(out)), err
	}
	// Exactly one merge base: a criss-cross history has several, and "the change" would depend on which one git printed.
	single := func(head string) (string, error) {
		out, err := one("merge-base", "--all", base, head)
		if err != nil {
			return "", err
		}
		if n := len(strings.Fields(out)); n != 1 {
			return "", fmt.Errorf("%d merge bases, want exactly 1", n)
		}
		return out, nil
	}
	mbB, err := single(before)
	if err != nil {
		return false, "old head: " + err.Error()
	}
	mbA, err := single(after)
	if err != nil {
		return false, "new head: " + err.Error()
	}
	patchID := func(mb, head string) (string, []string, error) {
		diff, err := git(nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", mb, head)
		if err != nil {
			return "", nil, err
		}
		if len(bytes.TrimSpace(diff)) == 0 {
			return "", nil, errors.New("the change is empty")
		}
		if len(diff) > maxDiff {
			return "", nil, errors.New("the change is too large to compare")
		}
		id, err := git(diff, "patch-id", "--verbatim")
		if err != nil {
			return "", nil, err
		}
		fields := strings.Fields(string(id))
		if len(fields) == 0 {
			return "", nil, errors.New("no patch-id")
		}
		names, err := git(nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", mb, head)
		if err != nil {
			return "", nil, err
		}
		var paths []string
		for _, p := range strings.Split(string(names), "\x00") {
			if p != "" {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		return fields[0], paths, nil
	}
	idB, pathsB, err := patchID(mbB, before)
	if err != nil {
		return false, "old head: " + err.Error()
	}
	idA, pathsA, err := patchID(mbA, after)
	if err != nil {
		return false, "new head: " + err.Error()
	}
	if idA != idB {
		return false, "the pull request's own change differs (patch-id)"
	}
	if strings.Join(pathsA, "\x00") != strings.Join(pathsB, "\x00") {
		return false, "the set of changed paths differs"
	}
	blob := func(rev, path string) (string, error) {
		out, err := git(nil, "ls-tree", "-z", rev, "--", path)
		return strings.TrimSpace(string(out)), err
	}
	for _, p := range pathsA {
		bb, err := blob(before, p)
		if err != nil {
			return false, "cannot read " + p + " at the old head: " + err.Error()
		}
		ba, err := blob(after, p)
		if err != nil {
			return false, "cannot read " + p + " at the new head: " + err.Error()
		}
		if bb == ba {
			continue
		}
		if protectedPath(p) {
			return false, p + " is a protected path and its content differs"
		}
		return false, p + " differs: a rebase over an upstream change of a file the pull request changes needs a new look"
	}
	return true, ""
}

// checkRuns is the part of `gh api repos/R/commits/SHA/check-runs` that chainOK reads.
type checkRuns struct {
	CheckRuns []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		StartedAt  string `json:"started_at"`
		App        struct {
			Slug string `json:"slug"`
		} `json:"app"`
	} `json:"check_runs"`
}

// chainOK says whether label-reset judged the old head: its newest check run named label-reset, from the GitHub Actions app,
// is completed with conclusion success. Skipped runs are ignored; missing, failed, cancelled, in progress, or from another
// app, or no run but skipped ones: a broken chain.
func chainOK(data []byte) (bool, string) {
	var cr checkRuns
	if err := json.Unmarshal(data, &cr); err != nil {
		return false, "the check runs of the old head are unreadable"
	}
	newest := -1
	for i, r := range cr.CheckRuns {
		if r.Name != "label-reset" || r.App.Slug != "github-actions" {
			continue
		}
		// A skipped run made no judgement: a skipped label-reset run (its job `if` was false) can be newer than the push's real
		// run. It proves nothing either way and is ignored; a head with only skipped runs has no judgement at all, which is a
		// broken chain below (ADR 0178).
		if r.Status == "completed" && r.Conclusion == "skipped" {
			continue
		}
		if newest < 0 || r.StartedAt > cr.CheckRuns[newest].StartedAt {
			newest = i
		}
	}
	if newest < 0 {
		return false, "label-reset has no check run on the old head: the chain is broken"
	}
	r := cr.CheckRuns[newest]
	if r.Status != "completed" || r.Conclusion != "success" {
		return false, "label-reset on the old head is " + r.Status + "/" + r.Conclusion + ": the chain is broken"
	}
	return true, ""
}

// clean makes a reason safe to print in a workflow log: no control characters, no "::" (workflow commands), bounded.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			r = '?'
		}
		b.WriteRune(r)
	}
	out := strings.ReplaceAll(b.String(), "::", ":?")
	if len(out) > 300 {
		out = out[:300] + "..."
	}
	return out
}

// runKeepLabel is `adrlint keep-label --base B --before X --after Y --before-checks FILE [--repo dir]`: prints "keep" and
// exits 0, or prints "reset: <reason>" and exits 1. FILE holds the check runs of the old head (chainOK). A usage problem
// exits 2, which callers treat as reset too.
func runKeepLabel(args []string, out, errw io.Writer, git func(dir string) gitIO) int {
	fs := flag.NewFlagSet("keep-label", flag.ContinueOnError)
	fs.SetOutput(errw)
	base := fs.String("base", "", "the pull request's base commit (full sha)")
	before := fs.String("before", "", "the head before the push (full sha)")
	after := fs.String("after", "", "the head after the push (full sha)")
	repo := fs.String("repo", "", "the git directory that holds all three commits")
	checks := fs.String("before-checks", "", "a file with `gh api repos/R/commits/<before>/check-runs` output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	reset := func(why string) int {
		fmt.Fprintln(out, "reset: "+clean(why))
		return 1
	}
	if *checks == "" {
		return reset("no check runs of the old head were given: the chain cannot be verified")
	}
	data, err := os.ReadFile(*checks)
	if err != nil {
		return reset("the check runs of the old head cannot be read")
	}
	if ok, why := chainOK(data); !ok {
		return reset(why)
	}
	keep, why := keepLabel(git(*repo), *base, *before, *after)
	if !keep {
		return reset(why)
	}
	fmt.Fprintln(out, "keep")
	return 0
}
