package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bogworks/mash/internal/testenv"
)

type step struct {
	ID               string            `yaml:"id"`
	Name             string            `yaml:"name"`
	If               string            `yaml:"if"`
	ContinueOnError  any               `yaml:"continue-on-error"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	WorkingDirectory string            `yaml:"working-directory"`
	Env              map[string]string `yaml:"env"`
	With             map[string]any    `yaml:"with"`
}

// runsOn is a job's runs-on: a scalar as written, or a sequence of labels rendered as "[a, b]".
type runsOn string

// runsOnKey is a runs-on list in the fmt.Sprint form linuxLabels and macosLabels use: "[a b]".
func runsOnKey(r runsOn) string { return strings.ReplaceAll(string(r), ",", "") }

func (r *runsOn) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*r = runsOn("[" + strings.Join(l, ", ") + "]")
		return nil
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	*r = runsOn(s)
	return nil
}

type job struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Needs           any               `yaml:"needs"`
	Uses            string            `yaml:"uses"` // a reusable-workflow caller job
	With            map[string]any    `yaml:"with"`
	Outputs         map[string]string `yaml:"outputs"`
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
	RunsOn          runsOn            `yaml:"runs-on"`
	Permissions     map[string]string `yaml:"permissions"`
	Env             map[string]string `yaml:"env"`
	Strategy        struct {
		Matrix   map[string]any `yaml:"matrix"`
		FailFast *bool          `yaml:"fail-fast"`
	} `yaml:"strategy"`
	Steps []step `yaml:"steps"`
}

type workflow struct {
	On          any               `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Env         map[string]string `yaml:"env"`
	Jobs        map[string]job    `yaml:"jobs"`
}

func load(t *testing.T, name string) workflow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../../.github/workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var w workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return w
}

func all(t *testing.T) map[string]workflow {
	t.Helper()
	files, _ := filepath.Glob("../../.github/workflows/*.yml")
	if len(files) == 0 {
		t.Fatal("no workflows")
	}
	out := map[string]workflow{}
	for _, f := range files {
		out[filepath.Base(f)] = load(t, filepath.Base(f))
	}
	return out
}

// GitHub rejects the runner context in workflow-level and job-level env.
func TestWorkflows_noRunnerContextOutsideSteps(t *testing.T) {
	for name, w := range all(t) {
		for k, v := range w.Env {
			if strings.Contains(v, "runner.") {
				t.Errorf("%s: workflow env %s uses the runner context", name, k)
			}
		}
		for jn, j := range w.Jobs {
			for k, v := range j.Env {
				if strings.Contains(v, "runner.") {
					t.Errorf("%s: job %s env %s uses the runner context", name, jn, k)
				}
			}
		}
	}
}

func TestWorkflows_everyJobHasATimeout(t *testing.T) {
	for name, w := range all(t) {
		for jn, j := range w.Jobs {
			if j.Uses != "" {
				continue // a reusable-workflow caller job cannot set timeout-minutes; the called workflow does
			}
			if j.TimeoutMinutes <= 0 || j.TimeoutMinutes > 60 {
				t.Errorf("%s: job %s has timeout-minutes %d", name, jn, j.TimeoutMinutes)
			}
		}
	}
}

var shaRef = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func TestWorkflows_everyActionIsPinnedToACommit(t *testing.T) {
	for name, w := range all(t) {
		for jn, j := range w.Jobs {
			if j.Uses != "" {
				// No workflow calls a reusable workflow of another repository (workflows_selfhosted_test.go); a local one
				// is not an action to pin.
				continue
			}
			for _, s := range j.Steps {
				if s.Uses == "" || strings.HasPrefix(s.Uses, "./") {
					continue
				}
				if _, ref, ok := strings.Cut(s.Uses, "@"); !ok || !shaRef.MatchString(ref) {
					t.Errorf("%s: job %s uses %q, not pinned to a full commit", name, jn, s.Uses)
				}
			}
		}
	}
}

// permissionedJobs are the release jobs that set permissions of their own; every other job takes the workflow's
// contents: read.
var permissionedJobs = map[string]bool{"guard": true, "draft": true, "attest": true, "attest-sbom": true, "sbom": true, "dry-run-verify": true}

func TestRelease_isTriggeredOnlyByVersionTagsAndPinsTheBuild(t *testing.T) {
	w := load(t, "release.yml")
	on, _ := w.On.(map[string]any)
	if len(on) != 2 || on["push"] == nil {
		t.Fatalf("release.yml must trigger only on push of tags and workflow_dispatch (the dry run), got %v", w.On)
	}
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Fatalf("release.yml has no workflow_dispatch trigger: %v", w.On)
	}
	push := on["push"].(map[string]any)
	if len(push) != 1 || fmt.Sprint(push["tags"]) != "[v*]" {
		t.Fatalf("release.yml push trigger: %v", push)
	}
	if w.Env["GOTOOLCHAIN"] != Toolchain || w.Env["CGO_ENABLED"] != "0" {
		t.Errorf("env pins: %v (want GOTOOLCHAIN %s, CGO_ENABLED 0)", w.Env, Toolchain)
	}
	if w.Permissions["contents"] != "read" || len(w.Permissions) != 1 {
		t.Errorf("workflow permissions: %v (want contents: read only)", w.Permissions)
	}
	for jn, j := range w.Jobs {
		if !permissionedJobs[jn] && len(j.Permissions) != 0 {
			t.Errorf("job %s widens permissions: %v", jn, j.Permissions)
		}
	}
	if w.Jobs["draft"].Permissions["contents"] != "write" {
		t.Error("the draft job needs contents: write")
	}
}

func TestRelease_buildsExactlyTheFourTargetsTwiceInFreshClones(t *testing.T) {
	w := load(t, "release.yml")
	b := w.Jobs["build"]
	inc, _ := b.Strategy.Matrix["include"].([]any)
	var got []string
	for _, e := range inc {
		m := e.(map[string]any)
		got = append(got, fmt.Sprintf("%v/%v", m["goos"], m["goarch"]))
	}
	sort.Strings(got)
	var want []string
	for _, tg := range Targets {
		want = append(want, tg.GOOS+"/"+tg.GOARCH)
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("matrix targets %v, want %v", got, want)
	}
	var run string
	for _, st := range b.Steps {
		if strings.Contains(st.Run, "build-twice") {
			run = st.Run
		}
	}
	for _, must := range []string{`scripts/build-twice "$GOOS_T" "$GOARCH_T" "$version" "$commit"`, "mash-${GOOS_T}-${GOARCH_T}"} {
		if !strings.Contains(run, must) {
			t.Errorf("the build step lacks %q:\n%s", must, run)
		}
	}
	if strings.Contains(run, "-tags") {
		t.Error("the release build passes build tags")
	}
}

func TestRelease_jobOrderAndDraftOnly(t *testing.T) {
	w := load(t, "release.yml")
	need := func(j string) string { return strings.Join(jobNeeds(w.Jobs[j].Needs), ",") }
	sorted := func(j string) string {
		n := jobNeeds(w.Jobs[j].Needs)
		sort.Strings(n)
		return strings.Join(n, ",")
	}
	if need("build") != "guard" || sorted("draft") != "attest,attest-sbom,build,sbom" {
		t.Fatalf("job order: build needs %s, draft needs %s", need("build"), need("draft"))
	}
	if need("sbom") != "build" || need("attest-sbom") != "sbom" || need("attest") != "build" {
		t.Fatalf("job order: sbom needs %s, attest-sbom %s, attest %s", need("sbom"), need("attest-sbom"), need("attest"))
	}
	var run string
	for _, s := range w.Jobs["draft"].Steps {
		run += s.Run
	}
	if !strings.Contains(run, "gh release create") || !strings.Contains(run, "--draft") || !strings.Contains(run, "--verify-tag") {
		t.Errorf("the draft job must create a draft release and verify the tag:\n%s", run)
	}
	if strings.Contains(run, "gh release edit") || strings.Contains(run, "--latest") || strings.Contains(run, "gh release upload") {
		t.Errorf("the draft job publishes or edits beyond creating a draft:\n%s", run)
	}
}

// The guard's tag-name check is run, not just read: good names pass, malformed ones fail.
func TestRelease_tagNameGuardAcceptsOnlyVersionTags(t *testing.T) {
	w := load(t, "release.yml")
	var line string
	for _, s := range w.Jobs["guard"].Steps {
		for _, ln := range strings.Split(s.Run, "\n") {
			if strings.Contains(ln, "=~") && strings.Contains(ln, "GITHUB_REF_NAME") {
				line = ln
			}
		}
	}
	if line == "" {
		t.Fatal("the guard does not check the tag name")
	}
	for name, ok := range map[string]bool{"v0.1.0": true, "v10.20.30": true, "v0.1": false, "0.1.0": false, "v0.1.0-rc1": false, "v0.1.0\nx": false, "vX.Y.Z": false, "v01.2.3x": false} {
		cmd := exec.Command("bash", "-c", line) // Actions runs `run` steps with bash
		cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+name)
		err := cmd.Run()
		if (err == nil) != ok {
			t.Errorf("tag %q: accepted=%v, want %v", name, err == nil, ok)
		}
	}
	var run string
	for _, s := range w.Jobs["guard"].Steps {
		run += s.Run
	}
	for _, must := range []string{"cat-file -t", "BEGIN SSH SIGNATURE", "BEGIN PGP SIGNATURE", "merge-base --is-ancestor"} {
		if !strings.Contains(run, must) {
			t.Errorf("the guard lacks %q", must)
		}
	}
}

// release-check requires check runs named by RequiredChecks: ci.yml must produce exactly those names.
func TestCI_producesTheCheckNamesTheGateRequires(t *testing.T) {
	w := load(t, "ci.yml")
	var names []string
	for jn, j := range w.Jobs {
		if len(j.Strategy.Matrix) != 0 {
			t.Errorf("job %s is matrixed: a check name must be exactly the job's name", jn)
		}
		if j.Name != "" {
			names = append(names, j.Name)
		} else {
			names = append(names, jn)
		}
	}
	sort.Strings(names)
	want := append(append([]string{}, RequiredChecks...), ciOnlyJobs...)
	sort.Strings(want)
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("ci.yml produces %v but release-check requires %v", names, want)
	}
	if w.Env["GOTOOLCHAIN"] != Toolchain {
		t.Errorf("ci.yml GOTOOLCHAIN = %q, want %s", w.Env["GOTOOLCHAIN"], Toolchain)
	}
}

// Nothing in the release workflow can route around a failed guard or a failed build: no conditions, no
// continue-on-error, strict shell options in every script that compares or publishes.
func TestRelease_noJobOrStepCanBypassAFailure(t *testing.T) {
	w := load(t, "release.yml")
	for jn, j := range w.Jobs {
		if j.ContinueOnError != nil {
			t.Errorf("job %s has continue-on-error: %v", jn, j.ContinueOnError)
		}
		if want, ok := jobConditions[jn]; j.If != want {
			t.Errorf("job %s has if %q, want %q (listed in jobConditions: %v)", jn, j.If, want, ok)
		}
		for _, st := range j.Steps {
			if st.If != "" || st.ContinueOnError != nil {
				t.Errorf("job %s step %q has if/continue-on-error: %q %v", jn, st.Name, st.If, st.ContinueOnError)
			}
			if strings.Contains(st.Run, "|| true") || strings.Contains(st.Run, "|| :") {
				t.Errorf("job %s step %q swallows a failure", jn, st.Name)
			}
			if (jn == "build" || jn == "draft") && st.Run != "" && !strings.Contains(st.Run, "set -euo pipefail") && !strings.Contains(st.Run, "set -eu") {
				// a one-line step is fine, a script that compares or publishes must be strict
				if strings.Count(st.Run, "\n") > 1 {
					t.Errorf("job %s step %q is a script without set -e", jn, st.Name)
				}
			}
		}
	}
}

// jobConditions are the only job-level conditions release.yml has: the event split between a tag push and the dry run
// (workflow_dispatch). Every other job has none. None of them is a status function that could run a job after a failure,
// except that every job downstream of build carries one that also demands its direct needs' success (see
// TestRelease_everyJobBelowBuildHasAStatusFunction). On a push the guard succeeds, so these behave as the implicit success().
// allFourSucceeded is what draft and dry-run-verify demand of the four jobs they wait for.
const allFourSucceeded = "needs.build.result == 'success' && needs.attest.result == 'success' && needs.sbom.result == 'success' && needs['attest-sbom'].result == 'success'"

var jobConditions = map[string]string{
	"guard":          "${{ github.event_name == 'push' }}",
	"draft":          "${{ !cancelled() && github.event_name == 'push' && " + allFourSucceeded + " }}",
	"build":          "${{ !cancelled() && (needs.guard.result == 'success' || (github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main' && needs.guard.result == 'skipped')) }}",
	"attest":         "${{ !cancelled() && needs.build.result == 'success' }}",
	"sbom":           "${{ !cancelled() && needs.build.result == 'success' }}",
	"attest-sbom":    "${{ !cancelled() && needs.sbom.result == 'success' }}",
	"dry-run-verify": "${{ !cancelled() && github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main' && " + allFourSucceeded + " }}",
}

// ci.yml's check names are the job names, except the test jobs' explicit test (linux) and test (macos): a name that differs from
// the job id, a condition or a matrix on a required job would silently make the gate refuse every candidate.
func TestCI_requiredJobsKeepTheirCheckNames(t *testing.T) {
	for jn, j := range load(t, "ci.yml").Jobs {
		if want, ok := map[string]string{"test": "test (linux)", "test-macos": "test (macos)"}[jn]; ok {
			// The check name differs from the job id on purpose, and no matrix may rename it.
			if j.Name != want {
				t.Errorf("the %s job is named %q, want exactly %s", jn, j.Name, want)
			}
		} else if j.Name != "" && j.Name != jn {
			t.Errorf("job %s is named %q: its check run would not be called %q", jn, j.Name, jn)
		}
		if jn == "reproducible" {
			if j.Name != "reproducible" {
				t.Errorf("the reproducible job must set name: reproducible explicitly (the gate requires that exact check name)")
			}
			if j.If != "" || len(j.Strategy.Matrix) != 0 || j.ContinueOnError != nil {
				t.Errorf("the reproducible job must run unconditionally, unmatrixed and strictly: %+v", j)
			}
		}
	}
}

// ci.yml's reproducible job builds all four release targets through the same script the release workflow uses, and
// says what the binary reports when it is wrong.
func TestCI_reproducibleJobUsesTheSharedScriptForEveryTarget(t *testing.T) {
	j := load(t, "ci.yml").Jobs["reproducible"]
	var run string
	for _, st := range j.Steps {
		run += st.Run + "\n"
	}
	for _, must := range []string{"scripts/build-twice", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "$GITHUB_SHA", "version"} {
		if !strings.Contains(run, must) {
			t.Errorf("the reproducible job lacks %q:\n%s", must, run)
		}
	}
	if !strings.Contains(run, "expected") {
		t.Error("a version mismatch must report what was printed and what was expected")
	}
}

// A new push cancels the superseded PR run, but never a run on main: the release gate reads main's check runs.
func TestCI_concurrencyNeverCancelsMain(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Concurrency struct {
			Group  string `yaml:"group"`
			Cancel any    `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(w.Concurrency.Cancel) != "${{ github.event_name == 'pull_request' }}" {
		t.Fatalf("concurrency: %+v", w.Concurrency)
	}
	// A pending run is replaced by a newer one in its group even without cancel-in-progress, so runs on main must not
	// share a group: only a pull request's runs do.
	if !strings.Contains(w.Concurrency.Group, "github.event_name == 'pull_request' && github.ref || github.run_id") {
		t.Fatalf("concurrency group %q lets a run on main replace another pending one", w.Concurrency.Group)
	}
}

// bash 3.2 (macOS) does not stop `set -e` on a failing `[[ ]]` that stands alone, so a check written that way
// passes there while the same script refuses on Linux. Every `[[` must be the condition of an if/while or part of
// a && / || list; the guard test below runs the real script on whichever OS the tests run on.
func TestWorkflows_noBareDoubleBracketUnderSetE(t *testing.T) {
	for name, w := range all(t) {
		for jn, j := range w.Jobs {
			for _, st := range j.Steps {
				for _, ln := range strings.Split(st.Run, "\n") {
					if strings.HasPrefix(strings.TrimSpace(ln), "[[") {
						t.Errorf("%s job %s: a bare [[ ]] is ignored by set -e in bash 3.2: %q", name, jn, ln)
					}
				}
			}
		}
	}
	b, err := os.ReadFile("../../scripts/build-twice")
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "[[") {
			t.Errorf("scripts/build-twice: a bare [[ ]] is ignored by set -e in bash 3.2: %q", ln)
		}
	}
}

// One OS failing must not hide the other's result: the two legs are separate jobs that do not need each other.
func TestCI_osLegsAreIndependentJobs(t *testing.T) {
	w := load(t, "ci.yml")
	for _, jn := range []string{"test", "test-macos"} {
		j := w.Jobs[jn]
		if len(j.Strategy.Matrix) != 0 {
			t.Errorf("%s must not be matrixed", jn)
		}
		if n := fmt.Sprint(j.Needs); n != "changes" && n != "[changes]" {
			t.Errorf("%s needs %v, want only changes (the legs must not cancel or skip each other)", jn, j.Needs)
		}
	}
}

// guardRepo makes a repository whose origin/main holds X, signing with a throwaway SSH key.
func guardRepo(t *testing.T) (dir, x string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "repo")
	run := func(args ...string) string {
		t.Helper()
		out, err := testenv.RunGit(append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "--bare", "origin.git")
	run("init", "-q", "-b", "main", "repo")
	g := func(args ...string) string { return run(append([]string{"-C", dir}, args...)...) }
	g("commit", "-q", "--allow-empty", "-m", "x")
	g("remote", "add", "origin", filepath.Join(root, "origin.git"))
	g("push", "-q", "origin", "main")
	g("fetch", "-q", "origin")
	key := filepath.Join(root, "k")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skipf("no ssh-keygen: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	if err := os.WriteFile(filepath.Join(root, "signers"), []byte("t@t "+string(pub)), 0o644); err != nil {
		t.Fatal(err)
	}
	g("config", "gpg.format", "ssh")
	g("config", "user.signingkey", key+".pub")
	g("config", "gpg.ssh.allowedSignersFile", filepath.Join(root, "signers"))
	return dir, g("rev-parse", "HEAD")
}

// The whole guard script is run against real tags: only an annotated, signed v<N.N.N> at a commit on main passes.
func TestRelease_guardScriptRefusesEveryBadTag(t *testing.T) {
	w := load(t, "release.yml")
	var script string
	for _, st := range w.Jobs["guard"].Steps {
		if strings.Contains(st.Run, "draft-guard") {
			continue // needs gh and the network; scripts/draft-guard has its own tests
		}
		script += st.Run + "\n"
	}
	dir, x := guardRepo(t)
	git := func(args ...string) {
		t.Helper()
		if out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGuard := func(tag, sha string) error {
		cmd := exec.Command("bash", "-e", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tag, "GITHUB_SHA="+sha)
		return cmd.Run()
	}
	// tag makes a tag and pushes it, as the maintainer does; the guard judges origin's tag, not the checkout's.
	tag := func(args ...string) {
		t.Helper()
		git(append([]string{"tag"}, args...)...)
		for _, a := range args {
			if strings.HasPrefix(a, "v") {
				git("push", "-q", "origin", "refs/tags/"+a)
				return
			}
		}
	}
	tag("-s", "v0.1.0", "-m", "release", x)
	if err := runGuard("v0.1.0", x); err != nil {
		t.Fatalf("an annotated, signed tag on main was refused: %v", err)
	}
	// actions/checkout leaves the pushed tag as a lightweight ref to the commit (v0.1.1's run failed on that): the
	// guard still sees origin's annotated, signed tag.
	git("tag", "-f", "v0.1.0", x)
	if err := runGuard("v0.1.0", x); err != nil {
		t.Fatalf("the checkout's lightweight copy of a good tag was refused: %v", err)
	}
	// GitHub's docs do not say whether GITHUB_SHA on an annotated-tag push is the commit or the tag object, so the
	// workflow must not depend on it: the candidate is derived from the tag.
	tagObj := func(tag string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", tag).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if tagObj("v0.1.0") == x {
		t.Fatal("the annotated tag object has the commit's SHA")
	}
	if err := runGuard("v0.1.0", tagObj("v0.1.0")); err != nil {
		t.Fatalf("with GITHUB_SHA set to the tag object the guard refused a good tag: %v", err)
	}
	tag("v0.2.0", x)
	if err := runGuard("v0.2.0", x); err == nil {
		t.Error("a lightweight tag passed the guard")
	}
	git("-c", "tag.gpgSign=false", "tag", "-a", "v0.3.0", "-m", "unsigned", x)
	git("push", "-q", "origin", "refs/tags/v0.3.0")
	if err := runGuard("v0.3.0", x); err == nil {
		t.Error("an unsigned annotated tag passed the guard")
	}
	tag("-s", "v0.4", "-m", "bad name", x)
	if err := runGuard("v0.4", x); err == nil {
		t.Error("a malformed tag name passed the guard")
	}
	// A signed tag at a commit that is not on main.
	git("commit", "-q", "--allow-empty", "-m", "off main")
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	off := strings.TrimSpace(string(out))
	tag("-s", "v0.5.0", "-m", "off main", off)
	if err := runGuard("v0.5.0", off); err == nil {
		t.Error("a tag on a commit that is not on main passed the guard")
	}
}

// No release job reads GITHUB_SHA: whatever it holds on an annotated-tag push, the commit comes from the tag.
func TestRelease_candidateIsDerivedFromTheTagNotGITHUB_SHA(t *testing.T) {
	w := load(t, "release.yml")
	for name, j := range w.Jobs {
		for _, st := range j.Steps {
			if strings.Contains(st.Run, "GITHUB_SHA") || strings.Contains(st.Env["X"], "github.sha") {
				t.Errorf("job %s step %q reads GITHUB_SHA", name, st.Name)
			}
		}
	}
}

// The changelog step, run in a real repository with GITHUB_SHA set to the tag object, lists the right range.
func TestRelease_changelogUsesTheTaggedCommit(t *testing.T) {
	w := load(t, "release.yml")
	var script string
	for _, st := range w.Jobs["draft"].Steps {
		if strings.Contains(st.Run, "git log") {
			script = st.Run
		}
	}
	if script == "" {
		t.Fatal("no changelog step")
	}
	dir, x := guardRepo(t)
	g := func(args ...string) string {
		t.Helper()
		out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g("tag", "-s", "v0.1.0", "-m", "one", x)
	g("commit", "-q", "--allow-empty", "-m", "second change")
	g("tag", "-s", "v0.2.0", "-m", "two", "HEAD")
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range append([]string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64"}, releaseCIAssets...) {
		if err := os.WriteFile(filepath.Join(dir, "dist", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The runner is Linux and has sha256sum; the test also runs where only shasum exists (macOS).
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "sha256sum"), []byte("#!/bin/sh\nexec shasum -a 256 \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	if _, err := exec.LookPath("sha256sum"); err != nil {
		path = shim + ":" + path
	}
	// The base comes from scripts/notes-base and the published-release list: stand-in gh reports v0.1.0 published.
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	nb, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "notes-base"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "notes-base"), nb, 0o755); err != nil {
		t.Fatal(err)
	}
	ghBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(ghBin, "gh"), []byte(`#!/bin/sh
echo '[{"tagName":"v0.1.0","isDraft":false,"isPrerelease":false}]'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	runScript := func() ([]byte, error) {
		cmd := exec.Command("bash", "-e", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+ghBin+":"+path, "RUNNER_TEMP="+t.TempDir(), "GITHUB_REF_NAME=v0.2.0", "GITHUB_SHA="+g("rev-parse", "v0.2.0"))
		return cmd.CombinedOutput()
	}
	// The tagged checkout must carry the script: without it the step fails.
	if out, err := runScript(); err == nil || !strings.Contains(string(out), "quickstart-synadia.sh is missing") {
		t.Fatalf("a checkout without the script passed: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "quickstart-synadia.sh"), []byte("#!/bin/sh\necho q\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runScript(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, "dist", "SHA256SUMS"))
	sl := strings.Split(strings.TrimSpace(string(sums)), "\n")
	wantSums := append(append([]string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64", "quickstart-synadia.sh"}, releaseCIAssets[:4]...), releaseCIAssets[4])
	if len(sl) != len(wantSums) {
		t.Fatalf("SHA256SUMS:\n%s", sums)
	}
	for i, n := range wantSums {
		if !strings.HasSuffix(sl[i], "  "+n) {
			t.Fatalf("SHA256SUMS line %d is not %s:\n%s", i+1, n, sums)
		}
	}
	// Whatever a binary is missing, the sums are not made.
	if err := os.Remove(filepath.Join(dir, "dist", "mash-linux-arm64.cdx.json")); err != nil {
		t.Fatal(err)
	}
	if out, err := runScript(); err == nil {
		t.Fatalf("the sums were made without an SBOM:\n%s", out)
	}
	if st, err := os.Stat(filepath.Join(dir, "dist", "quickstart-synadia.sh")); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the script was not copied with its mode: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "dist", "CHANGELOG.md"))
	if !strings.Contains(string(b), "second change") || strings.Contains(string(b), " x (") {
		t.Fatalf("changelog:\n%s", b)
	}
}

// releaseCIAssets are the SBOMs and the build provenance the draft job attaches besides the binaries and the script.
var releaseCIAssets = []string{"mash-darwin-amd64.cdx.json", "mash-darwin-arm64.cdx.json", "mash-linux-amd64.cdx.json", "mash-linux-arm64.cdx.json", "mash.intoto.jsonl"}

// The attest job, and only it, holds the signing permissions (and no contents); it attests the four binaries.
// jobNeeds normalises a job's `needs`, which YAML allows as a string or a list.
func jobNeeds(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, e := range n {
			out = append(out, fmt.Sprint(e))
		}
		return out
	}
	return nil
}

func TestRelease_attestJobAttestsBuildProvenance(t *testing.T) {
	w := load(t, "release.yml")
	have := map[string]bool{}
	for _, n := range jobNeeds(w.Jobs["draft"].Needs) {
		have[n] = true
	}
	if !have["build"] || !have["attest"] {
		t.Errorf("draft must need both build and attest, got %v", w.Jobs["draft"].Needs)
	}
	j := w.Jobs["attest"]
	for _, p := range []string{"id-token", "attestations"} {
		if j.Permissions[p] != "write" {
			t.Errorf("attest job must grant %s: write, got %q", p, j.Permissions[p])
		}
	}
	attest := regexp.MustCompile(`^actions/attest-build-provenance@[0-9a-f]{40}$`)
	found := false
	for _, st := range j.Steps {
		if attest.MatchString(st.Uses) {
			found = true
			if st.With["subject-path"] != "dist/mash-*" {
				t.Errorf("attest subject-path = %v, want dist/mash-*", st.With["subject-path"])
			}
		}
	}
	if !found {
		t.Error("attest job has no attest-build-provenance step pinned to a full commit")
	}
	// The attest job's permissions are exactly the three the in-job attestation needs (and only it holds contents: read
	// explicitly with them).
	if len(j.Permissions) != 3 || j.Permissions["contents"] != "read" {
		t.Errorf("attest job permissions = %v, want id-token: write, attestations: write, contents: read", j.Permissions)
	}
	// Least privilege: no other job and not the workflow gets the signing permissions. The two attest jobs sign.
	for _, p := range []string{"id-token", "attestations"} {
		if w.Permissions[p] != "" {
			t.Errorf("workflow-level %s permission must not be set", p)
		}
		for id, o := range w.Jobs {
			ok := id == "attest" || id == "attest-sbom"
			if !ok && o.Permissions[p] != "" {
				t.Errorf("job %s must not hold %s", id, p)
			}
		}
	}
}

func TestRelease_draftScriptCreatesOnlyADraft(t *testing.T) {
	w := load(t, "release.yml")
	var script string
	for _, st := range w.Jobs["draft"].Steps {
		if strings.Contains(st.Run, "gh release create") {
			script = st.Run
		}
	}
	if script == "" {
		t.Fatal("no draft step")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"mash-linux-amd64", "quickstart-synadia.sh", "SHA256SUMS", "CHANGELOG.md"} {
		if err := os.WriteFile(filepath.Join(dir, "dist", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "bin")
	log := filepath.Join(dir, "gh.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n[ \"$1 $2\" = \"release list\" ] && echo '[]'\n[ \"$1 $2\" = \"release view\" ] && echo true\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	guard, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "draft-guard"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "draft-guard"), guard, 0o755); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, `scripts/draft-guard --after-create "$GITHUB_REF_NAME"`) {
		t.Fatalf("the draft step does not re-check order after creating:\n%s", script)
	}
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "GITHUB_REF_NAME=v0.1.0", "GH_TOKEN=x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if !strings.Contains(lines[0], " dist/quickstart-synadia.sh ") {
		t.Fatalf("the script is not attached: %s", lines[0])
	}
	if len(lines) != 2 || lines[1] != "release list --limit 200 --json tagName,isDraft" || !strings.HasPrefix(lines[0], "release create v0.1.0 ") || !strings.Contains(lines[0], " --draft ") || !strings.Contains(lines[0], "--verify-tag") ||
		strings.Contains(lines[0], "--draft=false") || strings.Contains(lines[0], "--latest") {
		t.Fatalf("gh was called as %q", lines)
	}
}

// ---- scripts/build-twice, run for real ----

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func buildTwice(t *testing.T, ws, tool string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "build-twice"), args...)
	cmd.Env = append(os.Environ(), "WORKSPACE="+ws)
	if tool != "" {
		cmd.Env = append(cmd.Env, "BUILDRELEASE="+tool)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestBuildTwice_buildsReproduciblyAndTheBinarySaysWhatItIs(t *testing.T) {
	root := repoRoot(t)
	x := headOf(t, root)
	out := filepath.Join(t.TempDir(), "out", "mash")
	got, err := buildTwice(t, root, "", "-", "-", "0.1.0", x, out)
	if err != nil {
		t.Fatalf("%v\n%s", err, got)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}  `).MatchString(got) {
		t.Fatalf("output %q", got)
	}
	line, err := exec.Command(out, "version").Output()
	if err != nil || strings.TrimSpace(string(line)) != "mash 0.1.0 (commit "+x+")" {
		t.Fatalf("%q %v", line, err)
	}
}

// stub is a stand-in buildrelease: it writes the -out file with the given content and prints what it is told to.
func stub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "buildrelease")
	if err := os.WriteFile(p, []byte("#!/bin/bash\nset -eu\nwhile [ $# -gt 0 ]; do case $1 in -out) out=$2; shift;; esac; shift; done\nmkdir -p \"$(dirname \"$out\")\"\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildTwice_refusesEmptyMalformedOrDifferingHashes(t *testing.T) {
	root := repoRoot(t)
	x := headOf(t, root)
	counter := filepath.Join(t.TempDir(), "n")
	cases := map[string]struct{ body, want string }{
		"empty output":     {`echo x > "$out"; echo ""`, "no valid sha256"},
		"a banner first":   {`echo x > "$out"; echo "building..."`, "no valid sha256"},
		"short hash":       {`echo x > "$out"; echo "abc123  $out"`, "no valid sha256"},
		"uppercase hash":   {`echo x > "$out"; echo "` + strings.Repeat("A", 64) + `  $out"`, "no valid sha256"},
		"differing hashes": {`n=$(cat ` + counter + ` 2>/dev/null || echo 0); n=$((n+1)); echo $n > ` + counter + `; echo x > "$out"; printf '%064d  %s\n' $n "$out"`, "not reproducible"},
	}
	for name, c := range cases {
		os.Remove(counter)
		out := filepath.Join(t.TempDir(), "mash")
		got, err := buildTwice(t, root, stub(t, c.body), "-", "-", "0.1.0", x, out)
		if err == nil || !strings.Contains(got, c.want) {
			t.Errorf("%s: err=%v output=%q (want a failure saying %q)", name, err, got, c.want)
		}
		if _, statErr := os.Stat(out); statErr == nil {
			t.Errorf("%s: an output file was published although the check failed", name)
		}
	}
}

// A pull request's candidate is a synthetic merge commit that no branch or tag points at: the clones must still get it.
func TestBuildTwice_buildsACommitNoBranchOrTagReaches(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	run := func(args ...string) string {
		t.Helper()
		out, err := testenv.RunGit(append([]string{"-C", ws, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := testenv.RunGit("init", "-q", "-b", "main", ws); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	run("commit", "-q", "--allow-empty", "-m", "base")
	tree := run("rev-parse", "HEAD^{tree}")
	base := run("rev-parse", "HEAD")
	merge := run("commit-tree", tree, "-p", base, "-m", "refs/pull/1/merge") // reachable from nothing
	if refs := run("for-each-ref", "--contains", merge); refs != "" {
		t.Fatalf("the test commit is reachable from %q", refs)
	}
	ok := stub(t, `echo x > "$out"; echo "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  $out"`)
	out := filepath.Join(t.TempDir(), "mash")
	if got, err := buildTwice(t, ws, ok, "-", "-", "0.0.0-ci", merge, out); err != nil {
		t.Fatalf("a commit that only HEAD-less refs reach could not be cloned: %v\n%s", err, got)
	}
	// And the script leaves no helper ref behind in the workspace.
	if refs := run("for-each-ref", "refs/ci"); refs != "" {
		t.Fatalf("refs left in the workspace: %s", refs)
	}
}

// The `test` jobs check out shallowly (actions/checkout's default): the tests and the script must work from there.
func TestBuildTwice_worksFromAShallowWorkspace(t *testing.T) {
	root := repoRoot(t)
	// Two commits of history, then a depth-1 clone of it: a shallow workspace like CI's.
	full := filepath.Join(t.TempDir(), "full")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := testenv.RunGit("init", "-q", "-b", "main", full); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	run(full, "commit", "-q", "--allow-empty", "-m", "one")
	run(full, "commit", "-q", "--allow-empty", "-m", "two")
	ws := filepath.Join(t.TempDir(), "ws")
	if out, err := testenv.RunGit("clone", "-q", "--depth", "1", "file://"+full, ws); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if run(ws, "rev-parse", "--is-shallow-repository") != "true" {
		t.Fatal("the workspace is not shallow")
	}
	x := run(ws, "rev-parse", "HEAD")
	ok := stub(t, `echo x > "$out"; echo "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  $out"`)
	out := filepath.Join(t.TempDir(), "mash")
	if got, err := buildTwice(t, ws, ok, "-", "-", "0.1.0", x, out); err != nil {
		t.Fatalf("a shallow workspace could not be cloned from: %v\n%s", err, got)
	}
	_ = root
}

// A failed ref cleanup must not stop the temporary directories being removed.
func TestBuildTwice_cleansUpEvenWhenTheRefIsAlreadyGone(t *testing.T) {
	root := repoRoot(t)
	x := headOf(t, root)
	tmp := t.TempDir()
	// A tool that deletes the helper ref itself, so the script's own deletion fails.
	tool := stub(t, `case "$out" in *out-b*) for r in $(git -C `+root+` for-each-ref --format='%(refname)' refs/ci); do git -C `+root+` update-ref -d $r; done;; esac; echo x > "$out"; echo "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  $out"`)
	cmd := exec.Command(filepath.Join(root, "scripts", "build-twice"), "-", "-", "0.1.0", x, filepath.Join(tmp, "mash"))
	cmd.Env = append(os.Environ(), "WORKSPACE="+root, "BUILDRELEASE="+tool, "TMPDIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	left, _ := filepath.Glob(filepath.Join(tmp, "tmp.*"))
	if len(left) != 0 {
		t.Fatalf("temporary directories left behind: %v", left)
	}
}

// ciOnlyJobs are ci.yml's jobs that the release gate does not require: changes (does a pull request touch code?) and
// pr-check (the one check a pull request's merge waits for), and conformance (the fast static slice a pull request's
// review starts on; it gates neither a merge nor a release).
// test (macos) is advisory (ADR 0079, TODO(macos-gate) in ci.yml): visible, but it gates neither a merge nor a release.
var ciOnlyJobs = []string{"adr-numbers", "changes", "conformance", "pr-check", "test (macos)"}

// pr-check is the single check scripts/review-pr waits for before a review: it must run on every pull request (a
// docs-only one too, or its review waits forever), after every required job, and pass only if they did.
func TestCI_prCheckGatesOnEveryRequiredJob(t *testing.T) {
	w := load(t, "ci.yml")
	j, ok := w.Jobs["pr-check"]
	if !ok {
		t.Fatal("ci.yml has no pr-check job")
	}
	if j.Name != "" || j.If != "always()" || j.ContinueOnError != nil || len(j.Strategy.Matrix) != 0 {
		t.Fatalf("pr-check must be named pr-check, run always(), strictly and unmatrixed: %+v", j)
	}
	needs := fmt.Sprint(j.Needs)
	for _, n := range []string{"changes", "test", "reproducible"} {
		if !strings.Contains(needs, n) {
			t.Errorf("pr-check does not need %s: %v", n, j.Needs)
		}
	}
	on, _ := w.On.(map[string]any)
	pr, _ := on["pull_request"].(map[string]any)
	if _, ignores := pr["paths-ignore"]; ignores {
		t.Error("pull_request must not use paths-ignore: a docs-only pull request would get no pr-check")
	}
	push, _ := on["push"].(map[string]any)
	if _, ignores := push["paths-ignore"]; ignores {
		t.Error("push must not use paths-ignore: a docs-only head of main would have no test runs, and the release gate refuses it")
	}
	if strings.Contains(needs, "test-macos") {
		t.Errorf("pr-check needs test-macos, which is advisory (TODO(macos-gate) in ci.yml): %v", j.Needs)
	}
	if mj := w.Jobs["test-macos"]; mj.ContinueOnError != nil || mj.If != w.Jobs["test"].If || !strings.Contains(fmt.Sprint(mj.RunsOn), "rh-macos") {
		t.Errorf("test-macos must run under the same condition as test, on rh-macos, and never with continue-on-error (it would hide a failure): %+v", mj)
	}
	if tj := w.Jobs["test"]; !strings.Contains(tj.If, "github.event_name == 'push'") {
		t.Errorf("the test job must always run on a push (the release gate reads main's check runs): if %q", tj.If)
	}
}

// The pr-check verdict, run as written in ci.yml: success only when reproducible passed and test passed, or test was
// skipped because the pull request touches no code.
func TestCI_prCheckVerdict(t *testing.T) {
	var script string
	for _, st := range load(t, "ci.yml").Jobs["pr-check"].Steps {
		if st.Run != "" {
			script = st.Run
		}
	}
	if script == "" {
		t.Fatal("pr-check has no run step")
	}
	for _, c := range []struct {
		code, test, repro, adr string
		ok                     bool
	}{
		{"true", "success", "success", "success", true},
		{"false", "skipped", "success", "success", true},
		{"false", "success", "success", "success", true},
		{"true", "skipped", "success", "success", false}, // code changed but tests did not run
		{"true", "failure", "success", "success", false},
		{"true", "success", "failure", "success", false},
		{"false", "skipped", "failure", "success", false},
		{"true", "cancelled", "success", "success", false},
		{"", "skipped", "success", "success", false},     // the changes job failed: no answer
		{"true", "success", "success", "failure", false}, // the ADR numbers check failed
		{"false", "skipped", "success", "cancelled", false},
		{"true", "success", "success", "", false},
	} {
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "CODE="+c.code, "TEST="+c.test, "REPRO="+c.repro, "ADR="+c.adr)
		out, err := cmd.CombinedOutput()
		if (err == nil) != c.ok {
			t.Errorf("code=%q test=%s reproducible=%s: ok=%v, want %v (%s)", c.code, c.test, c.repro, err == nil, c.ok, out)
		}
	}
}

// The changes job, run as written in ci.yml against a stand-in git: a pull request touches code unless every changed
// file is under docs/ or is Markdown; a push always counts as code.
func TestCI_changesClassifiesThePullRequest(t *testing.T) {
	var script string
	for _, st := range load(t, "ci.yml").Jobs["changes"].Steps {
		if st.Run != "" {
			script = st.Run
		}
	}
	if script == "" {
		t.Fatal("changes has no run step")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s' \"$FILES\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		event, files, want string
	}{
		{"pull_request", "docs/a.md\nREADME.md", "false"},
		{"pull_request", "docs/a.md\ncmd/mash/cli.go", "true"},
		{"pull_request", "internal/x.go", "true"},
		{"pull_request", ".github/workflows/ci.yml", "true"},
		{"pull_request", "docs/plans/p.txt", "false"},
		{"pull_request", "", "true"}, // nothing to compare: run the tests
		{"push", "README.md", "true"},
	} {
		out := filepath.Join(t.TempDir(), "out")
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "EVENT="+c.event, "BASE=base", "FILES="+c.files, "GITHUB_OUTPUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %q: %v %s", c.event, c.files, err, b)
		}
		got, _ := os.ReadFile(out)
		if strings.TrimSpace(string(got)) != "code="+c.want {
			t.Errorf("%s %q: %q, want code=%s", c.event, c.files, got, c.want)
		}
	}
}

// adr-lint runs from the base branch (pull_request_target) and executes nothing from the pull request, so it runs on the
// repository's self-hosted runners, labelled self-hosted and rh-linux. Fork pull requests are not skipped: they run there
// once a person approves them. Its runs-on is exactly that list and the trigger is pull_request_target.
func TestADRLint_selfHostedRunner(t *testing.T) {
	wf := load(t, "adr-lint.yml")
	if r := string(wf.Jobs["adr-lint"].RunsOn); r != "[self-hosted, rh-linux]" {
		t.Errorf("adr-lint runs-on = %q, want [self-hosted, rh-linux]", r)
	}
	on, _ := wf.On.(map[string]any)
	prt, ok := on["pull_request_target"].(map[string]any)
	if !ok {
		t.Fatalf("adr-lint must trigger on pull_request_target, got %v", wf.On)
	}
	types := fmt.Sprint(prt["types"])
	for _, want := range []string{"labeled", "unlabeled"} {
		if !strings.Contains(types, want) {
			t.Errorf("adr-lint pull_request_target types lack %s: %s", want, types)
		}
	}
	for _, st := range wf.Jobs["adr-lint"].Steps {
		if strings.Contains(st.Run, "git -C \"$GITHUB_WORKSPACE/pr\" diff --no-renames") &&
			!strings.Contains(st.Run, "# --no-renames:") {
			t.Errorf("the --no-renames comment must sit in the step that runs git diff:\n%s", st.Run)
		}
		if strings.Contains(st.Run, "fetch --no-tags") && strings.Contains(st.Run, "--no-renames:") {
			t.Errorf("the --no-renames comment is on the fetch step, not the diff step:\n%s", st.Run)
		}
	}
}

// A synchronize, reopened or opened run must not honour decision-confirmed (label-reset removes it, in a run that
// triggers nothing new), so the label counts only from a labeled run on the current head. The workflow runs on labeled and
// unlabeled, passes the event action to the checker, and reads the labels live, failing on an API error.
func TestADRLint_confirmationNeedsALabeledRun(t *testing.T) {
	wf := load(t, "adr-lint.yml")
	on, _ := wf.On.(map[string]any)
	prt, _ := on["pull_request_target"].(map[string]any)
	types := fmt.Sprint(prt["types"])
	for _, want := range []string{"labeled", "unlabeled", "synchronize", "reopened", "opened"} {
		if !strings.Contains(types, want) {
			t.Errorf("adr-lint must trigger on pull_request_target %s, got %s", want, types)
		}
	}
	var st *step
	for i, s := range wf.Jobs["adr-lint"].Steps {
		if strings.Contains(s.Run, "adrlint\" pr ") {
			st = &wf.Jobs["adr-lint"].Steps[i]
		}
	}
	if st == nil {
		t.Fatal("no step runs adrlint pr")
	}
	if st.Env["PR_ACTION"] != "${{ github.event.action }}" {
		t.Errorf("the event action must reach the checker as PR_ACTION, got %q", st.Env["PR_ACTION"])
	}
	if !strings.Contains(st.Run, "gh pr view") || !strings.Contains(st.Run, "--json labels") || !strings.Contains(st.Run, "PR_LABELS=") {
		t.Errorf("the labels must be read live with gh pr view --json labels:\n%s", st.Run)
	}
	if strings.Contains(fmt.Sprint(st.Env), "pull_request.labels") {
		t.Errorf("the labels must not come from the event payload snapshot: %v", st.Env)
	}
	if !strings.Contains(st.Run, "set -euo pipefail") {
		t.Error("an API error must fail the step")
	}
}

// label-reset runs from the base branch and executes nothing from the pull request; like every job it runs on the
// self-hosted labels (ADR 0079) and names no GitHub-hosted runner: its runs-on is exactly [self-hosted, rh-linux].
func TestLabelReset_noHostedRunner(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/label-reset.yml")
	if err != nil {
		t.Fatal(err)
	}
	for n, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if hostedRunnerRE.MatchString(line) {
			t.Errorf("label-reset.yml:%d names a GitHub-hosted runner: %s", n+1, line)
		}
	}
	if r := strings.TrimSpace(string(load(t, "label-reset.yml").Jobs["label-reset"].RunsOn)); r != "[self-hosted, rh-linux]" {
		t.Errorf("label-reset runs-on = %q, want [self-hosted, rh-linux]", r)
	}
}

var hostedRunnerRE = regexp.MustCompile(`(ubuntu|macos|windows)-`)

// A draft left behind stops the next draft before anything is built: the guard job runs scripts/draft-guard, and it is
// the only jobs that get contents: write are guard and draft; attest holds the signing permissions with contents: read (the workflow default).
func TestRelease_guardRefusesOlderDraftsAndHoldsWriteOnlyThere(t *testing.T) {
	w := load(t, "release.yml")
	var run string
	for _, st := range w.Jobs["guard"].Steps {
		run += st.Run + "\n"
	}
	if !strings.Contains(run, "scripts/draft-guard") {
		t.Errorf("the guard job does not call scripts/draft-guard:\n%s", run)
	}
	if w.Permissions["contents"] != "read" || len(w.Permissions) != 1 {
		t.Errorf("workflow permissions must stay contents: read, got %v", w.Permissions)
	}
	for id, j := range w.Jobs {
		switch id {
		case "guard", "draft":
			if j.Permissions["contents"] != "write" || len(j.Permissions) != 1 {
				t.Errorf("job %s permissions = %v, want contents: write only", id, j.Permissions)
			}
		case "attest":
			if j.Permissions["id-token"] != "write" || j.Permissions["attestations"] != "write" || j.Permissions["contents"] != "read" || len(j.Permissions) != 3 {
				t.Errorf("job attest permissions = %v, want exactly id-token: write, attestations: write, contents: read", j.Permissions)
			}
		case "attest-sbom":
			if j.Permissions["id-token"] != "write" || j.Permissions["attestations"] != "write" || len(j.Permissions) != 2 {
				t.Errorf("job %s permissions = %v, want id-token: write and attestations: write only", id, j.Permissions)
			}
		case "sbom", "dry-run-verify":
			if j.Permissions["contents"] != "read" || len(j.Permissions) != 1 {
				t.Errorf("job %s permissions = %v, want contents: read only", id, j.Permissions)
			}
		default:
			if len(j.Permissions) != 0 {
				t.Errorf("job %s must not widen permissions: %v", id, j.Permissions)
			}
		}
	}
}

// conformance is the fast static slice a pull request's review starts on. It is advisory: it gates neither a merge
// (not in pr-check's needs, not a Mergify condition) nor a release, and it never runs on a merge queue's branches.
func TestCI_conformanceIsAdvisory(t *testing.T) {
	w := load(t, "ci.yml")
	j, ok := w.Jobs["conformance"]
	if !ok {
		t.Fatal("ci.yml has no conformance job")
	}
	for _, want := range []string{"github.event_name == 'pull_request'", "!startsWith(github.head_ref, 'mergify/')"} {
		if !strings.Contains(j.If, want) {
			t.Errorf("conformance if %q lacks %q", j.If, want)
		}
	}
	if got := runsOnKey(j.RunsOn); got != linuxLabels {
		t.Errorf("conformance runs-on %s; want %s", got, linuxLabels)
	}
	if needs := fmt.Sprint(w.Jobs["pr-check"].Needs); strings.Contains(needs, "conformance") {
		t.Errorf("pr-check needs conformance, which would make it gate a merge: %v", needs)
	}
	for _, c := range RequiredChecks {
		if c == "conformance" {
			t.Error("conformance is in RequiredChecks, which would make it gate a release")
		}
	}
	m, err := os.ReadFile("../../.mergify.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m), "conformance") {
		t.Error(".mergify.yml names conformance; it must not be a queue or merge condition")
	}
	var runs strings.Builder
	for _, s := range j.Steps {
		runs.WriteString(s.Run + "\n")
	}
	for _, want := range []string{"go build ./...", "go vet ./...", "gofmt -l", "go mod tidy -diff", "staticcheck"} {
		if !strings.Contains(runs.String(), want) {
			t.Errorf("conformance has no step running %q", want)
		}
	}
}

func TestRelease_notesComeFromThePublishedReleaseNotGitDescribe(t *testing.T) {
	w := load(t, "release.yml")
	var run string
	for _, st := range w.Jobs["draft"].Steps {
		run += st.Run + "\n"
	}
	if strings.Contains(run, "git describe") {
		t.Errorf("the draft job still uses git describe:\n%s", run)
	}
	if !strings.Contains(run, "scripts/notes-base") {
		t.Errorf("the draft job does not call scripts/notes-base:\n%s", run)
	}
}

// adr-lint judges a pull request with a checker built from the base branch; the pull request's own tree is data only.
func TestADRLint_runsTheBaseBranchsCheckerOnThePullRequestsTree(t *testing.T) {
	j := load(t, "adr-lint.yml").Jobs["adr-lint"]
	var trusted, pr, goStep *step
	var runs string
	for i := range j.Steps {
		st := &j.Steps[i]
		runs += st.Run + "\n"
		switch {
		case strings.HasPrefix(st.Uses, "actions/checkout@") && st.With["path"] == "trusted":
			trusted = st
		case strings.HasPrefix(st.Uses, "actions/checkout@") && st.With["path"] == "pr":
			pr = st
		case strings.HasPrefix(st.Uses, "actions/setup-go@"):
			goStep = st
		}
		if st.Uses != "" && strings.HasPrefix(st.Uses, "actions/checkout@") && st.With["persist-credentials"] != false {
			t.Errorf("checkout %v keeps credentials", st.With["path"])
		}
	}
	if trusted == nil || pr == nil || goStep == nil {
		t.Fatalf("need a trusted/ checkout, a pr/ checkout and setup-go: %v %v %v", trusted, pr, goStep)
	}
	if trusted.With["ref"] != "${{ github.event.pull_request.base.sha }}" {
		t.Errorf("trusted/ ref = %v, want the base sha", trusted.With["ref"])
	}
	if pr.With["ref"] != "${{ github.event.pull_request.head.sha }}" || pr.With["repository"] != "${{ github.event.pull_request.head.repo.full_name }}" {
		t.Errorf("pr/ must be the head sha of the head repo, got %v of %v", pr.With["ref"], pr.With["repository"])
	}
	// pull_request_target: GitHub runs the base branch's copy of this workflow; the PR is data only.
	wf := load(t, "adr-lint.yml")
	on, _ := wf.On.(map[string]any)
	if _, ok := on["pull_request_target"]; !ok || len(on) != 1 {
		t.Errorf("adr-lint must trigger on pull_request_target only, got %v", wf.On)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" || len(j.Permissions) != 0 {
		t.Errorf("permissions must be exactly contents: read, got %v / job %v", wf.Permissions, j.Permissions)
	}
	for _, st := range j.Steps {
		if st.WorkingDirectory == "pr" || strings.HasPrefix(st.WorkingDirectory, "pr/") {
			t.Errorf("step %q runs in the pull request tree", st.Name)
		}
	}
	if goStep.With["go-version-file"] != "trusted/go.mod" {
		t.Errorf("go-version-file = %v, want trusted/go.mod", goStep.With["go-version-file"])
	}
	for _, bad := range []string{"go run", "go build ./pr", "cd pr", "./pr/", "pr/scripts", "source pr/", "bash pr/"} {
		if strings.Contains(runs, bad) {
			t.Errorf("a step runs code from the pull request tree (%q):\n%s", bad, runs)
		}
	}
	if !strings.Contains(runs, `"$RUNNER_TEMP/adrlint" docs --root "$GITHUB_WORKSPACE/pr"`) ||
		!strings.Contains(runs, `"$RUNNER_TEMP/adrlint" pr --root "$GITHUB_WORKSPACE/pr"`) {
		t.Errorf("both modes must run the base-built checker with --root pr:\n%s", runs)
	}
	for _, st := range j.Steps {
		if strings.Contains(st.Run, "github.event") {
			t.Errorf("step %q puts event data in the script text", st.Name)
		}
	}
}

// cyclonedxGomod is the SBOM tool and the one version the release builds with.
const cyclonedxGomod = "github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.9.0"

func matrixTargets(t *testing.T, j job) []string {
	t.Helper()
	inc, _ := j.Strategy.Matrix["include"].([]any)
	var got []string
	for _, e := range inc {
		m := e.(map[string]any)
		got = append(got, fmt.Sprintf("%v/%v", m["goos"], m["goarch"]))
	}
	sort.Strings(got)
	return got
}

func allTargets() []string {
	var want []string
	for _, tg := range Targets {
		want = append(want, tg.GOOS+"/"+tg.GOARCH)
	}
	sort.Strings(want)
	return want
}

// The sbom job makes one CycloneDX 1.5 SBOM per binary from the built binary, with the pinned tool, and may only read.
func TestRelease_sbomJobMakesAPinnedCycloneDXSBOMPerBinary(t *testing.T) {
	j := load(t, "release.yml").Jobs["sbom"]
	if strings.Join(jobNeeds(j.Needs), ",") != "build" {
		t.Errorf("sbom needs %v, want build", j.Needs)
	}
	if got := matrixTargets(t, j); strings.Join(got, ",") != strings.Join(allTargets(), ",") {
		t.Errorf("sbom matrix %v, want the four targets", got)
	}
	var run, uploads string
	for _, st := range j.Steps {
		run += st.Run + "\n"
		if strings.HasPrefix(st.Uses, "actions/upload-artifact@") {
			uploads = fmt.Sprint(st.With["name"], " ", st.With["path"])
		}
	}
	if strings.Count(run, "cyclonedx-gomod/cmd/cyclonedx-gomod@") != 1 || !strings.Contains(run, "go install "+cyclonedxGomod) {
		t.Errorf("the SBOM tool is not installed at exactly %s:\n%s", cyclonedxGomod, run)
	}
	for _, must := range []string{" bin ", "-json", "-output-version 1.5", `"dist/$name.cdx.json" "dist/$name"`, "set -euo pipefail"} {
		if !strings.Contains(run, must) {
			t.Errorf("the SBOM step lacks %q:\n%s", must, run)
		}
	}
	if uploads != "sbom-${{ matrix.goos }}-${{ matrix.goarch }} dist/mash-${{ matrix.goos }}-${{ matrix.goarch }}.cdx.json" {
		t.Errorf("sbom upload: %s", uploads)
	}
}

// attest-sbom attests each SBOM with its binary as the subject, through actions/attest-sbom pinned to a commit.
func TestRelease_attestSBOMJobAttestsEachBinarysSBOM(t *testing.T) {
	j := load(t, "release.yml").Jobs["attest-sbom"]
	if strings.Join(jobNeeds(j.Needs), ",") != "sbom" {
		t.Errorf("attest-sbom needs %v, want sbom", j.Needs)
	}
	if got := matrixTargets(t, j); strings.Join(got, ",") != strings.Join(allTargets(), ",") {
		t.Errorf("attest-sbom matrix %v, want the four targets", got)
	}
	pin := regexp.MustCompile(`^actions/attest-sbom@[0-9a-f]{40}$`)
	found := false
	for _, st := range j.Steps {
		if pin.MatchString(st.Uses) {
			found = true
			if st.With["subject-path"] != "dist/mash-${{ matrix.goos }}-${{ matrix.goarch }}" ||
				st.With["sbom-path"] != "dist/mash-${{ matrix.goos }}-${{ matrix.goarch }}.cdx.json" {
				t.Errorf("attest-sbom with: %v", st.With)
			}
		}
	}
	if !found {
		t.Error("attest-sbom has no actions/attest-sbom step pinned to a full commit")
	}
}

// The provenance is made in-job on the self-hosted runner: the attest job downloads the build artifacts only (mash-*, never
// an SBOM or provenance artifact), attests the four binaries, and keeps the Sigstore bundle as mash.intoto.jsonl in a
// `provenance` artifact the draft and dry-run jobs download. The keep step, run for real, refuses anything but four binaries.
func TestRelease_attestJobKeepsTheBundleAsTheProvenanceAsset(t *testing.T) {
	w := load(t, "release.yml")
	j := w.Jobs["attest"]
	if strings.Join(jobNeeds(j.Needs), ",") != "build" {
		t.Errorf("attest needs %v, want build", j.Needs)
	}
	for _, st := range j.Steps {
		if strings.HasPrefix(st.Uses, "actions/download-artifact@") && st.With["pattern"] != "mash-*" {
			t.Errorf("attest downloads %v, not just the binaries (pattern mash-*)", st.With)
		}
	}
	var script, bundleEnv string
	var attestID string
	uploaded := false
	for _, st := range j.Steps {
		if strings.HasPrefix(st.Uses, "actions/attest-build-provenance@") {
			attestID = st.ID
		}
		if strings.Contains(st.Run, "mash.intoto.jsonl") {
			script, bundleEnv = st.Run, st.Env["BUNDLE"]
		}
		if strings.HasPrefix(st.Uses, "actions/upload-artifact@") && st.With["name"] == "provenance" && st.With["path"] == "provenance/mash.intoto.jsonl" {
			uploaded = true
		}
	}
	if attestID == "" || bundleEnv != "${{ steps."+attestID+".outputs.bundle-path }}" {
		t.Errorf("the keep step reads the bundle from %q, want the attest step's bundle-path output (step id %q)", bundleEnv, attestID)
	}
	if !uploaded {
		t.Error("attest does not upload provenance/mash.intoto.jsonl as the provenance artifact")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "bundle.jsonl")
	run := func() error {
		cmd := exec.Command("bash", "-e", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "BUNDLE="+bundle)
		_, err := cmd.CombinedOutput()
		return err
	}
	if err := os.WriteFile(bundle, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64"} {
		if err := os.WriteFile(filepath.Join(dir, "dist", n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if run() == nil {
		t.Error("three binaries were attested")
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "mash-linux-arm64"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatalf("four binaries and a bundle: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "provenance", "mash.intoto.jsonl")); err != nil || string(b) != "{}\n" {
		t.Errorf("provenance/mash.intoto.jsonl = %q, %v", b, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "mash-linux-arm64.cdx.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Error("an extra file in dist was attested")
	}
	os.Remove(filepath.Join(dir, "dist", "mash-linux-arm64.cdx.json"))
	if err := os.WriteFile(bundle, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Error("an empty bundle was kept")
	}
}

// No workflow calls the SLSA generator or any other reusable workflow of another repository: the release provenance is
// the attest job's in-job attestation (ADR 0079).
func TestRelease_noSLSAGeneratorAndNoGeneratorJobs(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"slsa-github-generator", "slsa-verifier", "SLSA_GENERATOR", "generator-pin", "base64-subjects"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("release.yml still mentions %q", banned)
		}
	}
	w := load(t, "release.yml")
	for _, jn := range []string{"provenance", "hashes", "generator-pin"} {
		if _, ok := w.Jobs[jn]; ok {
			t.Errorf("release.yml still has the %s job", jn)
		}
	}
}

// The draft attaches and lists every asset the contract names, and only the two attest jobs hold id-token.
func TestRelease_draftCarriesAndSumsTheSBOMsAndProvenance(t *testing.T) {
	w := load(t, "release.yml")
	var sums, create string
	for _, st := range w.Jobs["draft"].Steps {
		if strings.Contains(st.Run, "sha256sum") {
			sums = st.Run
		}
		if strings.Contains(st.Run, "gh release create") {
			create = st.Run
		}
	}
	names := append([]string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64", "quickstart-synadia.sh"}, releaseCIAssets...)
	for _, n := range names {
		if !strings.Contains(sums, n) {
			t.Errorf("SHA256SUMS does not list %s:\n%s", n, sums)
		}
		if !strings.Contains(create, "dist/"+n) {
			t.Errorf("the draft does not attach %s:\n%s", n, create)
		}
	}
	for id, j := range w.Jobs {
		if j.Permissions["id-token"] != "" && id != "attest" && id != "attest-sbom" {
			t.Errorf("job %s holds id-token", id)
		}
	}
	if w.Jobs["draft"].Permissions["contents"] != "write" || len(w.Jobs["draft"].Permissions) != 1 {
		t.Errorf("draft permissions: %v", w.Jobs["draft"].Permissions)
	}
}

// The dry run: a workflow_dispatch on main runs build to the attestations and dry-run-verify, and never the guard, the draft or
// anything that publishes. A tag push never runs dry-run-verify.
func TestRelease_dryRunVerifiesAndNeverReleases(t *testing.T) {
	w := load(t, "release.yml")
	push := "${{ github.event_name == 'push' }}"
	if w.Jobs["guard"].If != push {
		t.Errorf("guard if %q, want %q: it must never run on a dispatch", w.Jobs["guard"].If, push)
	}
	if d := w.Jobs["draft"].If; !strings.Contains(d, "github.event_name == 'push'") || strings.Contains(d, "workflow_dispatch") || strings.Contains(d, "||") {
		t.Errorf("draft if %q: it must require a push and never run on a dispatch", d)
	}
	// A tag push needs the guard's success; only a dispatch on main may build past a skipped guard.
	b := w.Jobs["build"].If
	for _, must := range []string{"!cancelled()", "needs.guard.result == 'success'", "github.event_name == 'workflow_dispatch'", "github.ref == 'refs/heads/main'", "needs.guard.result == 'skipped'"} {
		if !strings.Contains(b, must) {
			t.Errorf("build if %q lacks %q", b, must)
		}
	}
	if strings.Contains(b, "needs.guard.result != ") || strings.Contains(b, "always()") {
		t.Errorf("build if %q could run after a failed guard", b)
	}
	j, ok := w.Jobs["dry-run-verify"]
	if !ok {
		t.Fatal("release.yml has no dry-run-verify job")
	}
	if !strings.Contains(j.If, "github.event_name == 'workflow_dispatch'") || !strings.Contains(j.If, "github.ref == 'refs/heads/main'") || strings.Contains(j.If, "push") {
		t.Errorf("dry-run-verify if %q", j.If)
	}
	if len(j.Permissions) != 1 || j.Permissions["contents"] != "read" || j.Permissions["id-token"] != "" {
		t.Errorf("dry-run-verify permissions %v, want contents: read only", j.Permissions)
	}
	n := jobNeeds(j.Needs)
	sort.Strings(n)
	if strings.Join(n, ",") != "attest,attest-sbom,build,sbom" {
		t.Errorf("dry-run-verify needs %v", n)
	}
	for id, o := range w.Jobs {
		for _, d := range jobNeeds(o.Needs) {
			if d == "dry-run-verify" {
				t.Errorf("job %s needs dry-run-verify: the dry run must not gate anything", id)
			}
		}
	}
	var run string
	for _, st := range j.Steps {
		run += st.Run + "\n"
		if strings.Contains(st.Run, "gh release") || strings.Contains(st.Run, "git tag") || strings.Contains(st.Run, "git push") {
			t.Errorf("dry-run-verify step %q releases, tags or pushes", st.Name)
		}
	}
	for _, must := range []string{
		`gh attestation verify "$n" -R "${{ github.repository }}" --bundle mash.intoto.jsonl --signer-workflow "${{ github.repository }}/.github/workflows/release.yml" --source-ref refs/heads/main --deny-self-hosted-runners`,
		`gh attestation verify "$n" -R "${{ github.repository }}" --signer-workflow "${{ github.repository }}/.github/workflows/release.yml" --source-ref refs/heads/main --deny-self-hosted-runners`,
		"(.dsseEnvelope.payload // .payload)",
		".predicate.invocation.configSource.entryPoint",
		".predicate.buildDefinition.externalParameters.workflow.path",
		`".github/workflows/release.yml"`,
		"--predicate-type https://cyclonedx.org/bom --format json",
		"jq -S",
		"mash-linux-amd64 mash-linux-arm64 mash-darwin-amd64 mash-darwin-arm64",
	} {
		if !strings.Contains(run, must) {
			t.Errorf("dry-run-verify lacks %q:\n%s", must, run)
		}
	}
	if strings.Count(run, "--signer-workflow") != 3 || strings.Count(run, "--source-ref refs/heads/main") != 3 || strings.Count(run, "--deny-self-hosted-runners") != 3 {
		t.Errorf("all three gh attestation verify calls (the bundle, build provenance and SBOM) need the signer, ref and runner flags:\n%s", run)
	}
	if strings.Contains(run, "--source-tag") {
		t.Error("the dry run's provenance comes from a branch: --source-tag cannot apply")
	}
	// The tag is read from the ref in guard and draft only; everything else uses RELEASE_TAG / RELEASE_REF, which are the
	// tag and its ref on a push and a non-release version and HEAD on a dispatch.
	if w.Env["RELEASE_TAG"] != "${{ github.event_name == 'push' && github.ref_name || 'v0.0.0-dryrun' }}" || w.Env["RELEASE_REF"] != "${{ github.event_name == 'push' && github.ref_name || 'HEAD' }}" {
		t.Errorf("release env: %v", w.Env)
	}
	for id, o := range w.Jobs {
		if id == "guard" || id == "draft" {
			continue
		}
		for _, st := range o.Steps {
			if strings.Contains(st.Run, "GITHUB_REF_NAME") {
				t.Errorf("job %s step %q reads GITHUB_REF_NAME; a dry run has no tag", id, st.Name)
			}
		}
	}
}

// A skipped job anywhere in a job's needs chain fails its implicit success(), so a dry run (guard skipped) silently skipped
// everything below build. Every job that needs build, directly or through any other job, must therefore carry an explicit
// status function, and must check each direct need's result.
func TestRelease_everyJobBelowBuildHasAStatusFunction(t *testing.T) {
	w := load(t, "release.yml")
	below := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for jn, j := range w.Jobs {
			if below[jn] {
				continue
			}
			for _, n := range jobNeeds(j.Needs) {
				if n == "build" || below[n] {
					below[jn], changed = true, true
				}
			}
		}
	}
	for _, jn := range []string{"attest", "sbom", "attest-sbom", "dry-run-verify", "draft"} {
		if !below[jn] {
			t.Errorf("job %s is not below build: the chain walk is broken", jn)
		}
	}
	for jn := range below {
		j := w.Jobs[jn]
		if !strings.Contains(j.If, "!cancelled()") && !strings.Contains(j.If, "always()") {
			t.Errorf("job %s needs build (directly or through a chain) but its if %q has no !cancelled()/always(): a skipped guard would skip it", jn, j.If)
		}
		for _, n := range jobNeeds(j.Needs) {
			ref := "needs." + n + ".result == 'success'"
			if strings.Contains(n, "-") {
				ref = "needs['" + n + "'].result == 'success'"
			}
			if !strings.Contains(j.If, ref) {
				t.Errorf("job %s needs %s but its if does not require %s", jn, n, ref)
			}
		}
	}
}

// git diff drops the source of a rename unless told not to: `git mv .mergify.yml elsewhere` would list only "elsewhere" and
// escape adr-lint's protected-path check (and a code file renamed into docs/ would skip CI). Every changed-path list a
// workflow computes passes --no-renames, so the old path is listed too.
func TestWorkflows_changedFileListsKeepRenameSources(t *testing.T) {
	files, _ := filepath.Glob("../../.github/workflows/*.yml")
	seen := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") || !strings.Contains(line, "git") || !strings.Contains(line, " diff ") {
				continue
			}
			if !strings.Contains(line, "--name-only") && !strings.Contains(line, "--name-status") {
				continue
			}
			seen++
			if !strings.Contains(line, "--no-renames") {
				t.Errorf("%s:%d lists changed files without --no-renames (a rename's old path would be missing): %s", filepath.Base(f), i+1, strings.TrimSpace(line))
			}
		}
	}
	if seen < 2 {
		t.Errorf("found %d changed-file lists in the workflows, want adr-lint.yml's and ci.yml's", seen)
	}
}
