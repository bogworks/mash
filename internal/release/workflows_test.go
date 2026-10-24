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
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
	RunsOn          string            `yaml:"runs-on"`
	Permissions     map[string]string `yaml:"permissions"`
	Env             map[string]string `yaml:"env"`
	Strategy        struct {
		Matrix map[string]any `yaml:"matrix"`
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
		if jn != "draft" && len(j.Permissions) != 0 {
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
	for _, must := range []string{`scripts/build-twice "$GOOS_T" "$GOARCH_T" "$version" "$GITHUB_SHA"`, "mash-${GOOS_T}-${GOARCH_T}"} {
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
	want := append([]string{}, RequiredChecks...)
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
	if w.Concurrency.Group == "" || fmt.Sprint(w.Concurrency.Cancel) != "${{ github.event_name == 'pull_request' }}" {
		t.Fatalf("concurrency: %+v", w.Concurrency)
	}
}

// guardRepo makes a repository whose origin/main holds X, signing with a throwaway SSH key.
func guardRepo(t *testing.T) (dir, x string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "repo")
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
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
		script += st.Run + "\n"
	}
	dir, x := guardRepo(t)
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGuard := func(tag, sha string) error {
		cmd := exec.Command("bash", "-e", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GITHUB_REF_NAME="+tag, "GITHUB_SHA="+sha)
		return cmd.Run()
	}
	git("tag", "-s", "v0.1.0", "-m", "release", x)
	if err := runGuard("v0.1.0", x); err != nil {
		t.Fatalf("an annotated, signed tag on main was refused: %v", err)
	}
	git("tag", "v0.2.0", x)
	if err := runGuard("v0.2.0", x); err == nil {
		t.Error("a lightweight tag passed the guard")
	}
	git("-c", "tag.gpgSign=false", "tag", "-a", "v0.3.0", "-m", "unsigned", x)
	if err := runGuard("v0.3.0", x); err == nil {
		t.Error("an unsigned annotated tag passed the guard")
	}
	git("tag", "-s", "v0.4", "-m", "bad name", x)
	if err := runGuard("v0.4", x); err == nil {
		t.Error("a malformed tag name passed the guard")
	}
	// A signed tag at a commit that is not on main.
	git("commit", "-q", "--allow-empty", "-m", "off main")
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	off := strings.TrimSpace(string(out))
	git("tag", "-s", "v0.5.0", "-m", "off main", off)
	if err := runGuard("v0.5.0", off); err == nil {
		t.Error("a tag on a commit that is not on main passed the guard")
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
	for _, n := range []string{"mash-linux-amd64", "SHA256SUMS", "CHANGELOG.md"} {
		if err := os.WriteFile(filepath.Join(dir, "dist", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "bin")
	log := filepath.Join(dir, "gh.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "GITHUB_REF_NAME=v0.1.0", "GH_TOKEN=x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "release create v0.1.0 ") || !strings.Contains(lines[0], " --draft ") || !strings.Contains(lines[0], "--verify-tag") ||
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
		out, err := exec.Command("git", append([]string{"-C", ws, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", ws).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	run("commit", "-q", "--allow-empty", "-m", "base")
	tree := run("rev-parse", "HEAD^{tree}")
	base := run("rev-parse", "HEAD")
	merge := run("commit-tree", tree, "-p", base, "-m", "refs/pull/1/merge") // reachable from nothing
	if refs := run("for-each-ref", "--contains", merge); refs != "" {
		t.Fatalf("the test commit is reachable from %q", refs)
	}
	ok := stub(t, `echo x > "$out"; sha=$(sha256sum "$out" | cut -d' ' -f1); echo "$sha  $out"`)
	out := filepath.Join(t.TempDir(), "mash")
	if got, err := buildTwice(t, ws, ok, "-", "-", "0.0.0-ci", merge, out); err != nil {
		t.Fatalf("a commit that only HEAD-less refs reach could not be cloned: %v\n%s", err, got)
	}
	// And the script leaves no helper ref behind in the workspace.
	if refs := run("for-each-ref", "refs/ci"); refs != "" {
		t.Fatalf("refs left in the workspace: %s", refs)
	}
}
