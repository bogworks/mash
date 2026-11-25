package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a throwaway git repository with a main branch and helpers.
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t, t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(path, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(msg string) string {
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *repo) keep(base, before, after string) (bool, string) {
	return keepLabel(realGitIO(r.dir), base, before, after)
}

const repeated = "block\nline one\nline two\nline three\nend\n"

// start makes main with a workflow, a doc, and a file with the same block twice.
func start(t *testing.T) (*repo, string) {
	r := newRepo(t)
	r.write(".github/workflows/ci.yml", "name: ci\n")
	r.write("docs/a.md", "a\n")
	r.write("repeat.txt", repeated+"\n---\n\n"+repeated)
	r.write("other.txt", "other\n")
	return r, r.commit("main")
}

func TestPureRebaseKeepsTheLabel(t *testing.T) {
	r, base := start(t)
	r.git("checkout", "-q", "-b", "pr")
	r.write(".github/workflows/ci.yml", "name: ci\njobs: {}\n")
	r.write("docs/a.md", "a\nmore\n")
	before := r.commit("pr")
	// main moves on with an unrelated file, the pr is rebased onto it
	r.git("checkout", "-q", "main")
	r.write("other.txt", "other changed\n")
	newBase := r.commit("main moves")
	r.git("checkout", "-q", "pr")
	r.git("rebase", "-q", "main")
	after := r.git("rev-parse", "HEAD")
	if before == after {
		t.Fatal("the rebase changed nothing")
	}
	_ = base
	if keep, why := r.keep(newBase, before, after); !keep {
		t.Fatalf("a pure rebase must keep the label: %s", why)
	}
}

// A rebase over an upstream change of a file the pull request also changes gives that file a different blob, so the label
// resets even though the patch is the same: nothing can vouch that the edit did not move.
func TestRebaseOverAnUpstreamChangeOfAChangedFileResets(t *testing.T) {
	r, _ := start(t)
	long := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"
	r.write("docs/a.md", long)
	r.commit("a gets ten lines")
	r.git("checkout", "-q", "-b", "pr")
	r.write("docs/a.md", long+"mine\n")
	before := r.commit("pr")
	r.git("checkout", "-q", "main")
	r.write("docs/a.md", "upstream\n"+long) // upstream edits the same file, far from the pr's line
	newBase := r.commit("main moves")
	r.git("checkout", "-q", "pr")
	r.git("rebase", "-q", "main")
	after := r.git("rev-parse", "HEAD")
	if keep, why := r.keep(newBase, before, after); keep {
		t.Fatalf("the file's blob differs after the rebase; the label must reset (%s)", why)
	}
}

// The edit moves between repeated blocks of an UNPROTECTED file that the base also changed: patch-id is equal and the base
// touched the file, which the first version of the rule wrongly excused.
func TestRelocatedEditInAFileTheBaseChangedResets(t *testing.T) {
	r, _ := start(t)
	doc := repeated + "\n---\n\n" + repeated
	r.git("checkout", "-q", "-b", "pr")
	r.write("repeat.txt", strings.Replace(doc, "line two", "line two EDIT", 1))
	before := r.commit("edit the first copy")
	r.git("checkout", "-q", "main")
	r.write("repeat.txt", doc+"\nupstream tail\n")
	newBase := r.commit("main touches the file")
	r.git("checkout", "-q", "-B", "tmp", newBase)
	second := strings.Replace(doc+"\nupstream tail\n", "line two", "line two EDIT", 2)
	r.write("repeat.txt", strings.Replace(second, "line two EDIT", "line two", 1))
	after := r.commit("edit the second copy")
	if keep, _ := r.keep(newBase, before, after); keep {
		t.Fatal("a relocated edit must reset the label even when the base changed the file")
	}
}

func TestAnyChangeToThePatchResets(t *testing.T) {
	r, base := start(t)
	r.git("checkout", "-q", "-b", "pr")
	r.write(".github/workflows/ci.yml", "name: ci\njobs: {}\n")
	before := r.commit("pr")
	cases := map[string]func(){
		"a protected line changes":       func() { r.write(".github/workflows/ci.yml", "name: ci\njobs: {x: 1}\n") },
		"a second file is touched":       func() { r.write("docs/a.md", "a changed\n") },
		"the change is removed":          func() { r.write(".github/workflows/ci.yml", "name: ci\n") },
		"a whitespace-only difference":   func() { r.write(".github/workflows/ci.yml", "name: ci\njobs:  {}\n") },
		"the same change in a new place": func() { r.write(".github/workflows/ci.yml", "name: ci\njobs: {}\n\n") },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			r.git("checkout", "-q", "-B", "tmp", before)
			mut()
			after := r.commit("push")
			if keep, _ := r.keep(base, before, after); keep {
				t.Fatalf("%s must reset the label", name)
			}
		})
	}
}

// The relocation attack: the same edit, with the same context, applied to the other copy of a repeated block keeps the
// patch-id, so it is the blob comparison that must refuse.
func TestTheSameEditInAnotherPlaceResets(t *testing.T) {
	for _, path := range []string{"repeat.txt", ".github/workflows/repeat.yml"} {
		t.Run(path, func(t *testing.T) {
			r, _ := start(t)
			r.write(".github/workflows/repeat.yml", repeated+"\n---\n\n"+repeated)
			base := r.commit("add repeat.yml")
			r.git("checkout", "-q", "-b", "pr")
			doc := repeated + "\n---\n\n" + repeated
			first := strings.Replace(doc, "line two", "line two EDIT", 1)
			r.write(path, first)
			before := r.commit("edit the first copy")
			second := strings.Replace(doc, "line two", "line two EDIT", 2)
			second = strings.Replace(second, "line two EDIT", "line two", 1) // only the second copy is edited
			r.git("checkout", "-q", "-B", "tmp", base)
			r.write(path, second)
			after := r.commit("edit the second copy")
			idB, _, _ := patchIDOf(t, r, base, before)
			idA, _, _ := patchIDOf(t, r, base, after)
			if idA != idB {
				t.Skip("this git's patch-id told the two places apart; the attack does not apply")
			}
			if keep, _ := r.keep(base, before, after); keep {
				t.Fatalf("the same edit in another place must reset the label (%s)", path)
			}
		})
	}
}

func patchIDOf(t *testing.T, r *repo, base, head string) (string, string, error) {
	t.Helper()
	diff := r.git("diff", "--no-renames", "--binary", base, head)
	cmd := exec.Command("git", "-C", r.dir, "patch-id", "--verbatim")
	cmd.Stdin = strings.NewReader(diff + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(out.String())
	return f[0], "", nil
}

func TestErrorsResetTheLabel(t *testing.T) {
	r, base := start(t)
	r.git("checkout", "-q", "-b", "pr")
	r.write("docs/a.md", "a\nmine\n")
	before := r.commit("pr")
	for name, args := range map[string][3]string{
		"an unknown commit":       {base, before, strings.Repeat("a", 40)},
		"not a full sha":          {base, before, "abc"},
		"before equals after":     {base, before, before},
		"an unknown base":         {strings.Repeat("b", 40), before, before + ""},
		"the old head is missing": {base, strings.Repeat("c", 40), before},
	} {
		if keep, _ := r.keep(args[0], args[1], args[2]); keep {
			t.Errorf("%s must reset the label", name)
		}
	}
	// an empty change (the pr's commit is reverted)
	r.git("revert", "--no-edit", "HEAD")
	after := r.git("rev-parse", "HEAD")
	if keep, why := r.keep(base, before, after); keep {
		t.Fatalf("an empty change must reset the label (%s)", why)
	}
}

const okRuns = `{"check_runs":[{"name":"label-reset","status":"completed","conclusion":"success","started_at":"2026-11-16T06:00:00Z","app":{"slug":"github-actions"},"pull_requests":[{"number":7}]}]}`

func TestChainBrokenResets(t *testing.T) {
	run := func(app, status, conclusion, started string) string {
		return `{"name":"label-reset","status":"` + status + `","conclusion":"` + conclusion + `","started_at":"` + started + `","app":{"slug":"` + app + `"},"pull_requests":[{"number":7}]}`
	}
	runPRs := func(prs string) string {
		return `{"name":"label-reset","status":"completed","conclusion":"success","started_at":"2026-11-16T06:00:00Z","app":{"slug":"github-actions"},"pull_requests":` + prs + `}`
	}
	cases := map[string]struct {
		json string
		ok   bool
	}{
		"success":   {`{"check_runs":[` + run("github-actions", "completed", "success", "2026-11-16T06:00:00Z") + `]}`, true},
		"no runs":   {`{"check_runs":[]}`, false},
		"failure":   {`{"check_runs":[` + run("github-actions", "completed", "failure", "2026-11-16T06:00:00Z") + `]}`, false},
		"cancelled": {`{"check_runs":[` + run("github-actions", "completed", "cancelled", "2026-11-16T06:00:00Z") + `]}`, false},
		"skipped":   {`{"check_runs":[` + run("github-actions", "completed", "skipped", "2026-11-16T06:00:00Z") + `]}`, false},
		"success then a newer skipped (a body edit)": {`{"check_runs":[` + run("github-actions", "completed", "success", "2026-11-16T06:00:00Z") + `,` + run("github-actions", "completed", "skipped", "2026-11-16T06:05:00Z") + `]}`, true},
		"failure then a newer skipped":               {`{"check_runs":[` + run("github-actions", "completed", "failure", "2026-11-16T06:00:00Z") + `,` + run("github-actions", "completed", "skipped", "2026-11-16T06:05:00Z") + `]}`, false},
		"in progress":                                {`{"check_runs":[` + run("github-actions", "in_progress", "", "2026-11-16T06:00:00Z") + `]}`, false},
		"another app":                                {`{"check_runs":[` + run("some-other-app", "completed", "success", "2026-11-16T06:00:00Z") + `]}`, false},
		"newest failed, older success":               {`{"check_runs":[` + run("github-actions", "completed", "success", "2026-11-16T06:00:00Z") + `,` + run("github-actions", "completed", "failure", "2026-11-16T06:05:00Z") + `]}`, false},
		"newest success, older failure":              {`{"check_runs":[` + run("github-actions", "completed", "failure", "2026-11-16T06:00:00Z") + `,` + run("github-actions", "completed", "success", "2026-11-16T06:05:00Z") + `]}`, true},
		"not json":                                   {`oops`, false},
		"another check only":                         {`{"check_runs":[{"name":"adr-lint","status":"completed","conclusion":"success","started_at":"x","app":{"slug":"github-actions"},"pull_requests":[{"number":7}]}]}`, false},
		"a run listing only another PR":              {`{"check_runs":[` + runPRs(`[{"number":8}]`) + `]}`, false},
		"a run listing both PRs":                     {`{"check_runs":[` + runPRs(`[{"number":8},{"number":7}]`) + `]}`, true},
		"a run with an empty pull_requests list":     {`{"check_runs":[` + runPRs(`[]`) + `]}`, false},
		"a run without pull_requests":                {`{"check_runs":[{"name":"label-reset","status":"completed","conclusion":"success","started_at":"2026-11-16T06:00:00Z","app":{"slug":"github-actions"}}]}`, false},
		"another PR's newer run is ignored":          {`{"check_runs":[` + run("github-actions", "completed", "success", "2026-11-16T06:00:00Z") + `,` + runPRs(`[{"number":8}]`) + `]}`, true},
	}
	for name, c := range cases {
		if ok, _ := chainOK([]byte(c.json), 7); ok != c.ok {
			t.Errorf("%s: chainOK = %v, want %v", name, ok, c.ok)
		}
	}
}

// The broken-chain scenario of the review: H1 reviewed and labelled; H1 to H2 changes the change but label-reset failed
// on H2; H2 to H3 is a pure rebase. The decision on H3 alone says keep; the chain check is what resets.
func TestBrokenChainThenPureRebaseResets(t *testing.T) {
	r, _ := start(t)
	r.git("checkout", "-q", "-b", "pr")
	r.write(".github/workflows/ci.yml", "name: ci\njobs: {}\n")
	r.commit("h1")
	r.write(".github/workflows/ci.yml", "name: ci\njobs: {run: evil}\n")
	h2 := r.commit("h2 changes the change")
	r.git("checkout", "-q", "main")
	r.write("other.txt", "other changed\n")
	newBase := r.commit("main moves")
	r.git("checkout", "-q", "pr")
	r.git("rebase", "-q", "main")
	h3 := r.git("rev-parse", "HEAD")
	if keep, why := r.keep(newBase, h2, h3); !keep {
		t.Fatalf("the pure rebase itself keeps (%s): the chain check is what must reset", why)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	failed := write("failed.json", `{"check_runs":[{"name":"label-reset","status":"completed","conclusion":"failure","started_at":"2026-11-16T06:00:00Z","app":{"slug":"github-actions"},"pull_requests":[{"number":7}]}]}`)
	good := write("good.json", okRuns)
	var out, errw bytes.Buffer
	if code := runKeepLabel([]string{"--base", newBase, "--before", h2, "--after", h3, "--repo", r.dir, "--pr", "7", "--before-checks", failed}, &out, &errw, realGitIO); code != 1 || !strings.Contains(out.String(), "chain is broken") {
		t.Fatalf("a failed label-reset on the old head must reset: code %d %q", code, out.String())
	}
	out.Reset()
	if code := runKeepLabel([]string{"--base", newBase, "--before", h2, "--after", h3, "--repo", r.dir, "--pr", "7", "--before-checks", good}, &out, &errw, realGitIO); code != 0 || out.String() != "keep\n" {
		t.Fatalf("an unbroken chain and a pure rebase keep: code %d %q", code, out.String())
	}
	out.Reset()
	if code := runKeepLabel([]string{"--base", newBase, "--before", h2, "--after", h3, "--repo", r.dir, "--pr", "7"}, &out, &errw, realGitIO); code != 1 {
		t.Fatalf("no check runs given must reset: code %d", code)
	}
}

func TestTwoMergeBasesReset(t *testing.T) {
	r, _ := start(t)
	r.git("checkout", "-q", "-b", "a")
	r.write("a.txt", "a\n")
	a1 := r.commit("a1")
	r.git("checkout", "-q", "main")
	r.git("checkout", "-q", "-b", "b")
	r.write("b.txt", "b\n")
	b1 := r.commit("b1")
	// a criss-cross: a merges b1, b merges a1; main is the original base of both
	r.git("checkout", "-q", "a")
	r.git("merge", "-q", "--no-edit", b1)
	am := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "b")
	r.git("merge", "-q", "--no-edit", a1)
	bm := r.git("rev-parse", "HEAD")
	if n := len(strings.Fields(r.git("merge-base", "--all", am, bm))); n < 2 {
		t.Skipf("this history has %d merge base(s); the criss-cross was not built", n)
	}
	if keep, why := r.keep(am, bm, bm[:0]+am); keep {
		t.Fatalf("two merge bases must reset (%s)", why)
	}
	if keep, why := r.keep(am, am, bm); keep || !strings.Contains(why, "merge bases") {
		t.Fatalf("two merge bases must reset with that reason: %v %q", keep, why)
	}
}

func TestVerdictIsSafeToPrint(t *testing.T) {
	got := clean("path ::error::boom\x1b[31m\nnext")
	if strings.Contains(got, "::") || strings.ContainsAny(got, "\x1b\n") {
		t.Fatalf("not cleaned: %q", got)
	}
	if long := clean(strings.Repeat("x", 1000)); len(long) > 310 {
		t.Fatalf("not bounded: %d", len(long))
	}
}

func TestKeepLabelCommand(t *testing.T) {
	r, base := start(t)
	r.git("checkout", "-q", "-b", "pr")
	r.write("docs/a.md", "a\nmine\n")
	before := r.commit("pr")
	r.write("docs/b.md", "b\n")
	after := r.commit("more")
	good := filepath.Join(t.TempDir(), "ok.json")
	if err := os.WriteFile(good, []byte(okRuns), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	code := runKeepLabel([]string{"--base", base, "--before", before, "--after", after, "--repo", r.dir, "--pr", "7", "--before-checks", good}, &out, &errw, realGitIO)
	if code != 1 || !strings.HasPrefix(out.String(), "reset: ") {
		t.Fatalf("code %d out %q", code, out.String())
	}
	for _, pr := range []string{"", "0", "-3", "x"} {
		args := []string{"--base", base, "--before", before, "--after", after, "--repo", r.dir, "--before-checks", good}
		if pr != "" {
			args = append(args, "--pr", pr)
		}
		out.Reset()
		if code := runKeepLabel(args, &out, &errw, realGitIO); code != 2 {
			t.Fatalf("--pr %q must be a usage error (exit 2), got %d", pr, code)
		}
	}
	if code := runKeepLabel([]string{"--bogus"}, &out, &errw, realGitIO); code != 2 {
		t.Fatalf("a usage error must exit 2, got %d", code)
	}
}

// The shape of the real thing: `gh api repos/R/commits/<pr head>/check-runs` filtered to label-reset, captured on
// 2026-11-16 (the pull_request_target run IS attached to the PR head); the sha is a placeholder.
func TestChainOKOnARealPayload(t *testing.T) {
	b, err := os.ReadFile("testdata/label-reset-check-runs.json")
	if err != nil {
		t.Fatal(err)
	}
	if ok, why := chainOK(b, 7); !ok {
		t.Fatalf("a real successful label-reset run must keep the chain: %s", why)
	}
	failed := strings.Replace(string(b), `"conclusion":"success"`, `"conclusion":"failure"`, 1)
	if ok, _ := chainOK([]byte(failed), 7); ok {
		t.Fatal("the same payload with a failed run must break the chain")
	}
}

func TestGitIOSuccessAndFailure(t *testing.T) {
	r := newRepo(t)
	r.write("big.txt", strings.Repeat("x", 100))
	r.commit("c")
	g := realGitIO(r.dir)
	if _, err := g(nil, "rev-parse", "HEAD"); err != nil {
		t.Fatalf("a small output must work: %v", err)
	}
	if _, err := g(nil, "no-such-subcommand"); err == nil {
		t.Fatal("a git failure must be an error")
	}
}
