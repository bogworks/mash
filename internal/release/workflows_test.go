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
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]any    `yaml:"with"`
}

type job struct {
	Needs          any               `yaml:"needs"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	RunsOn         string            `yaml:"runs-on"`
	Permissions    map[string]string `yaml:"permissions"`
	Env            map[string]string `yaml:"env"`
	Strategy       struct {
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
