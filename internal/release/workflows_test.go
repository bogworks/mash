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
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	With            map[string]any    `yaml:"with"`
}

type job struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Needs           any               `yaml:"needs"`
	Outputs         map[string]string `yaml:"outputs"`
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
	RunsOn          string            `yaml:"runs-on"`
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
			if j.TimeoutMinutes <= 0 || j.TimeoutMinutes > 60 {
				t.Errorf("%s: job %s has timeout-minutes %d", name, jn, j.TimeoutMinutes)
			}
		}
	}
}

var shaRef = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func TestWorkflows_thirdPartyActionsArePinnedToACommit(t *testing.T) {
	for name, w := range all(t) {
		for jn, j := range w.Jobs {
			for _, s := range j.Steps {
				if s.Uses == "" || strings.HasPrefix(s.Uses, "actions/") || strings.HasPrefix(s.Uses, "./") {
					continue
				}
				if _, ref, ok := strings.Cut(s.Uses, "@"); !ok || !shaRef.MatchString(ref) {
					t.Errorf("%s: job %s uses %q, not pinned to a full commit", name, jn, s.Uses)
				}
			}
		}
	}
}

func TestRelease_isTriggeredOnlyByVersionTagsAndPinsTheBuild(t *testing.T) {
	w := load(t, "release.yml")
	on, _ := w.On.(map[string]any)
	if len(on) != 1 || on["push"] == nil {
		t.Fatalf("release.yml must trigger only on push of tags, got %v", w.On)
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
		if jn != "draft" && jn != "guard" && len(j.Permissions) != 0 {
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
	need := func(j string) string { return fmt.Sprint(w.Jobs[j].Needs) }
	if need("build") != "guard" || need("draft") != "build" {
		t.Fatalf("job order: build needs %s, draft needs %s", need("build"), need("draft"))
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
		if m, ok := j.Strategy.Matrix["os"].([]any); ok {
			for _, o := range m {
				names = append(names, fmt.Sprintf("%s (%v)", jn, o)) // GitHub names matrix jobs "<job> (<value>)"
			}
			continue
		}
		names = append(names, jn)
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
		if j.If != "" || j.ContinueOnError != nil {
			t.Errorf("job %s has if/continue-on-error: %q %v", jn, j.If, j.ContinueOnError)
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

// ci.yml's check names are the job names (plus matrix values): a name that differs from the job id, a condition or a
// matrix on a required job would silently make the gate refuse every candidate.
func TestCI_requiredJobsKeepTheirCheckNames(t *testing.T) {
	for jn, j := range load(t, "ci.yml").Jobs {
		if j.Name != "" && j.Name != jn {
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

// One OS failing must not hide the other's result: the matrix runs to the end.
func TestCI_matrixDoesNotFailFast(t *testing.T) {
	j := load(t, "ci.yml").Jobs["test"]
	if j.Strategy.FailFast == nil || *j.Strategy.FailFast {
		t.Fatal("ci.yml test matrix must set fail-fast: false")
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
	if err := os.WriteFile(filepath.Join(dir, "dist", "mash-linux-amd64"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
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
	if len(sl) != 2 || !strings.HasSuffix(sl[0], "  mash-linux-amd64") || !strings.HasSuffix(sl[1], "  quickstart-synadia.sh") {
		t.Fatalf("SHA256SUMS:\n%s", sums)
	}
	if st, err := os.Stat(filepath.Join(dir, "dist", "quickstart-synadia.sh")); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the script was not copied with its mode: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "dist", "CHANGELOG.md"))
	if !strings.Contains(string(b), "second change") || strings.Contains(string(b), " x (") {
		t.Fatalf("changelog:\n%s", b)
	}
}

// The draft job's script is run with a stand-in gh: it creates a draft, verifies the tag, and publishes nothing else.
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
// pr-check (the one check a pull request's review waits for).
var ciOnlyJobs = []string{"changes", "pr-check"}

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
		code, test, repro string
		ok                bool
	}{
		{"true", "success", "success", true},
		{"false", "skipped", "success", true},
		{"false", "success", "success", true},
		{"true", "skipped", "success", false}, // code changed but tests did not run
		{"true", "failure", "success", false},
		{"true", "success", "failure", false},
		{"false", "skipped", "failure", false},
		{"true", "cancelled", "success", false},
		{"", "skipped", "success", false}, // the changes job failed: no answer
	} {
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "CODE="+c.code, "TEST="+c.test, "REPRO="+c.repro)
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

// No job runs on a self-hosted runner: bogworks/mash is a public repo, and a self-hosted runner would run fork code on a
// maintainer's machine. Every runs-on is GitHub-hosted by default (ubuntu-latest, macos-latest), or an expression whose
// only non-hosted target is the repository variable RUNNER_LINUX, which a maintainer sets and a fork pull request never
// reaches. No runs-on may name bf1 or a self-hosted label.
func TestWorkflows_noSelfHostedRunners(t *testing.T) {
	files, err := filepath.Glob("../../.github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			text := strings.TrimSpace(line)
			if strings.HasPrefix(text, "#") {
				continue
			}
			if strings.Contains(text, "bf1") || strings.Contains(strings.ToLower(text), "self-hosted") {
				t.Errorf("%s:%d names a self-hosted runner: %s", name, n+1, text)
			}
		}
		for id, j := range load(t, name).Jobs {
			r := strings.TrimSpace(j.RunsOn)
			switch {
			case r == "ubuntu-latest" || r == "macos-latest":
			case strings.HasPrefix(r, "${{"):
				if !strings.Contains(r, "'ubuntu-latest'") {
					t.Errorf("%s job %s: runs-on %q has no ubuntu-latest default", name, id, r)
				}
			default:
				t.Errorf("%s job %s: runs-on %q is neither a GitHub-hosted label nor an expression", name, id, r)
			}
		}
	}
}

// Every Linux job takes its runner from the repository variable RUNNER_LINUX with ubuntu-latest as the default, so a
// maintainer can move mash's Linux CI by setting one variable. A fork pull request always gets ubuntu-latest, never
// RUNNER_LINUX. The macOS branch of the test matrix is macos-latest for every run.
func TestCI_linuxRunnersReadRUNNER_LINUX(t *testing.T) {
	const want = "vars.RUNNER_LINUX || 'ubuntu-latest'"
	for id, j := range load(t, "ci.yml").Jobs {
		r := strings.TrimSpace(j.RunsOn)
		if !strings.HasPrefix(r, "${{") || !strings.Contains(r, want) {
			t.Errorf("ci.yml job %s: runs-on %q does not read %s", id, r, want)
		}
		if !strings.Contains(r, "&& 'ubuntu-latest' || ") {
			t.Errorf("ci.yml job %s: runs-on %q has no ubuntu-latest fork fallback", id, r)
		}
		if strings.Contains(r, "&& vars.RUNNER_LINUX") {
			t.Errorf("ci.yml job %s: runs-on %q routes a fork pull request to RUNNER_LINUX", id, r)
		}
		if id == "test" && !strings.HasSuffix(r, "|| 'macos-latest' }}") {
			t.Errorf("ci.yml test job: runs-on %q does not end in macos-latest", r)
		}
	}
}

// release.yml builds what gets signed; a tag push is never a fork pull request, so each job reads RUNNER_LINUX with the
// ubuntu-latest default and no fork routing.
func TestRelease_runsOnRunnerLinuxDefaultHosted(t *testing.T) {
	const want = "${{ vars.RUNNER_LINUX || 'ubuntu-latest' }}"
	for id, j := range load(t, "release.yml").Jobs {
		if r := strings.TrimSpace(j.RunsOn); r != want {
			t.Errorf("release.yml job %s runs on %q; want %s", id, r, want)
		}
	}
}

// A draft left behind stops the next draft before anything is built: the guard job runs scripts/draft-guard, and it is
// the only job that gets contents: write beyond the draft job (workflow default stays read).
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
		default:
			if len(j.Permissions) != 0 {
				t.Errorf("job %s must not widen permissions: %v", id, j.Permissions)
			}
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
