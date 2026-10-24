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
	for _, s := range b.Steps {
		if strings.Contains(s.Run, "buildrelease") {
			run = s.Run
		}
	}
	for _, must := range []string{"for n in a b", "git clone", "checkout -q \"$GITHUB_SHA\"", "-commit \"$GITHUB_SHA\"", "not reproducible", "mash-${GOOS_T}-${GOARCH_T}"} {
		if !strings.Contains(run, must) {
			t.Errorf("the build step lacks %q", must)
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

// ci.yml's check names are the job ids (plus matrix values): an explicit job name would change them silently.
func TestCI_jobsHaveNoExplicitNames(t *testing.T) {
	for jn, j := range load(t, "ci.yml").Jobs {
		if j.Name != "" {
			t.Errorf("job %s has name %q: GitHub would name its check run that, not %q", jn, j.Name, jn)
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
