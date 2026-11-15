package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runGoCaches runs scripts/ci-go-caches kind with the given environment and returns its stdout and the lines it wrote
// to $GITHUB_ENV.
func runGoCaches(t *testing.T, kind string, env ...string) (string, string) {
	t.Helper()
	ghEnv := filepath.Join(t.TempDir(), "github_env")
	if err := os.WriteFile(ghEnv, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "../../scripts/ci-go-caches", kind)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GITHUB_ENV=" + ghEnv}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-go-caches %s: %v\n%s", kind, err, out)
	}
	got, err := os.ReadFile(ghEnv)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), string(got)
}

// makeSeed builds a seed dir for kind with a go-build entry and a go-mod entry, read-only like the real one.
func makeSeed(t *testing.T, root, kind string) string {
	t.Helper()
	seed := filepath.Join(root, kind)
	for _, d := range []string{"go-build/ab", "go-mod/cache"} {
		if err := os.MkdirAll(filepath.Join(seed, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "go-build/ab/entry"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "go-mod/cache/sentinel"), []byte("mod-entry"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"go-build/ab", "go-build", "go-mod/cache", "go-mod", "."} {
		if err := os.Chmod(filepath.Join(seed, d), 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return seed
}

// A runner with MASH_CI_WARM=1 and a read-only seed gets its own writable copy, and GOCACHE, GOMODCACHE and the lint cache point at it.
func TestCIGoCaches_copiesAReadOnlySeed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only seed")
	}
	root, tmp := t.TempDir(), t.TempDir()
	makeSeed(t, root, "race")
	out, env := runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp, "MASH_GO_SEED_SKIP_OWNER_CHECK=1")
	dest := filepath.Join(tmp, "go-seed-race")
	for _, want := range []string{"GOCACHE=" + dest + "/go-build", "GOMODCACHE=" + dest + "/go-mod", "GOLANGCI_LINT_CACHE=" + dest + "/golangci", "-modcacherw"} {
		if !strings.Contains(env, want) {
			t.Errorf("GITHUB_ENV lacks %q:\n%s", want, env)
		}
	}
	if !strings.Contains(out, "per-job copy of the seed") {
		t.Errorf("stdout does not say the seed was copied:\n%s", out)
	}
	// The seed's entries arrived with their contents.
	for rel, want := range map[string]string{"go-build/ab/entry": "x", "go-mod/cache/sentinel": "mod-entry"} {
		got, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil || string(got) != want {
			t.Errorf("copied %s = %q, %v; want %q", rel, got, err, want)
		}
	}
	// The copy is writable and the seed is untouched.
	if err := os.WriteFile(filepath.Join(dest, "go-build/ab/new"), []byte("y"), 0o644); err != nil {
		t.Errorf("the copy is not writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "race/go-build/ab/new")); err == nil {
		t.Error("a write to the copy reached the seed")
	}
}

// With no seed, a seed this user can write or own, or a runner that is not warm, the shared caches stay: no env is written,
// and the output says which path was taken.
func TestCIGoCaches_fallsBackToTheSharedCaches(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a read-only seed")
	}
	tmp := t.TempDir()
	out, env := runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+t.TempDir(), "RUNNER_TEMP="+tmp)
	if env != "" || !strings.Contains(out, "SHARED (no seed at") {
		t.Errorf("no seed: env %q, out %q", env, out)
	}

	root := t.TempDir()
	seed := makeSeed(t, root, "race")
	if err := os.Chmod(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	skip := "MASH_GO_SEED_SKIP_OWNER_CHECK=1"
	out, env = runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp, skip)
	if env != "" || !strings.Contains(out, "SHARED (the seed at") || !strings.Contains(out, "not trusted") {
		t.Errorf("writable seed: env %q, out %q", env, out)
	}

	// A writable directory nested inside an otherwise read-only seed is refused too.
	root = t.TempDir()
	seed = makeSeed(t, root, "race")
	if err := os.Chmod(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(seed, "golangci"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(seed, 0o555); err != nil {
		t.Fatal(err)
	}
	out, env = runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp, skip)
	if env != "" || !strings.Contains(out, "SHARED (the seed at") {
		t.Errorf("nested writable dir: env %q, out %q", env, out)
	}

	// A symlink anywhere in the seed (here to a writable dir) is refused: cp -a would keep it.
	root = t.TempDir()
	seed = makeSeed(t, root, "race")
	if err := os.Chmod(filepath.Join(seed, "go-mod/cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(seed, "go-mod/cache/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(seed, "go-mod/cache"), 0o555); err != nil {
		t.Fatal(err)
	}
	out, env = runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp, skip)
	if env != "" || !strings.Contains(out, "symlink") {
		t.Errorf("symlink in the seed: env %q, out %q", env, out)
	}

	// Without the test hook the owner check refuses this seed: its files are the test user's own.
	root = t.TempDir()
	makeSeed(t, root, "race")
	out, env = runGoCaches(t, "race", "MASH_CI_WARM=1", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp)
	if env != "" || !strings.Contains(out, "owned by") {
		t.Errorf("owned seed: env %q, out %q", env, out)
	}

	out, env = runGoCaches(t, "race", "MASH_GO_SEED="+root, "RUNNER_TEMP="+tmp)
	if env != "" || out != "" {
		t.Errorf("not a warm runner: env %q, out %q", env, out)
	}
}

// goCacheCall is the one line a warming step runs, with the seed kind filled in.
const goCacheCall = "if [ -f scripts/ci-go-caches ]; then scripts/ci-go-caches %s; fi"

// goCacheJobs lists every job that warms its Go caches, with its seed kind. Every other job in every workflow must
// not run the script: the release jobs and reproducible build cold (a release is built from their output), adr-lint
// runs under pull_request_target, publish-check and codeql do not need it, and test-macos has no seed planned (operations,
// 2026-11-11). This pins workflow steps only: the script's own unit tests above run under `go test ./...` on every
// platform CI tests, macOS included, and the script avoids GNU-only find options for that reason.
var goCacheJobs = map[string]string{
	"ci.yml/conformance":       "check",
	"lint.yml/lint":            "check",
	"security.yml/govulncheck": "check",
}

// Each job in goCacheJobs runs the script, with its kind and exactly as pinned, before its setup-go step, and every
// other job runs it nowhere.
func TestWorkflows_goCacheStepsCallCIGoCaches(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for file, w := range all(t) {
		for name, j := range w.Jobs {
			key := file + "/" + name
			kind, want := goCacheJobs[key]
			called, setupGo := -1, -1
			for i, st := range j.Steps {
				if strings.Contains(st.Run, "ci-go-caches") {
					if called >= 0 {
						t.Errorf("%s: more than one step runs ci-go-caches", key)
					}
					called = i
					if want && st.Run != fmt.Sprintf(goCacheCall, kind) {
						t.Errorf("%s: step runs %q, want %q", key, st.Run, fmt.Sprintf(goCacheCall, kind))
					}
				}
				if strings.HasPrefix(st.Uses, "actions/setup-go@") && setupGo < 0 {
					setupGo = i
				}
			}
			switch {
			case want:
				seen[key] = true
				if called < 0 {
					t.Errorf("%s: no step runs ci-go-caches %s", key, kind)
				} else if setupGo < 0 || called > setupGo {
					t.Errorf("%s: ci-go-caches must run before setup-go (step %d, setup-go %d)", key, called, setupGo)
				}
			case called >= 0:
				t.Errorf("%s: this job must not run ci-go-caches", key)
			}
		}
	}
	for key := range goCacheJobs {
		if !seen[key] {
			t.Errorf("%s: job not found", key)
		}
	}
}

// The two Linux test jobs (test, test-eph) run their steps through the local action .github/actions/go-test, so the
// race warm-up step lives in that action: the action runs ci-go-caches race, exactly as pinned, before its setup-go step,
// and each job checks out first (a local action is read from the checkout) and calls the action.
func TestWorkflows_goCacheStepInTheSharedTestAction(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../.github/actions/go-test/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Runs struct {
			Steps []step `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	called, setupGo := -1, -1
	for i, st := range a.Runs.Steps {
		if strings.Contains(st.Run, "ci-go-caches") {
			called = i
			if st.Run != fmt.Sprintf(goCacheCall, "race") {
				t.Errorf("go-test action: step runs %q, want %q", st.Run, fmt.Sprintf(goCacheCall, "race"))
			}
		}
		if strings.HasPrefix(st.Uses, "actions/setup-go@") && setupGo < 0 {
			setupGo = i
		}
	}
	if called < 0 || setupGo < 0 || called > setupGo {
		t.Errorf("go-test action: ci-go-caches race must run before setup-go (step %d, setup-go %d)", called, setupGo)
	}
	for _, jn := range []string{"test", "test-eph"} {
		steps := load(t, "ci.yml").Jobs[jn].Steps
		if len(steps) != 2 || !strings.HasPrefix(steps[0].Uses, "actions/checkout@") || steps[1].Uses != "./.github/actions/go-test" {
			t.Errorf("ci.yml/%s: steps must be actions/checkout then ./.github/actions/go-test, got %+v", jn, steps)
			continue
		}
		if steps[1].With["forbidden-words"] != "${{ vars.MASH_FORBIDDEN_WORDS }}" {
			t.Errorf("ci.yml/%s: the action must be given the forbidden words from vars: %v", jn, steps[1].With)
		}
	}
}

// ADR 0127: only test (linux, eph) sends its JUnit file to Mergify CI Insights. The go-test action does it behind the input
// insights: "true", which only test-eph passes, and test-eph alone holds id-token: write (OIDC, no stored key). The required
// Linux test keeps the plain go test step, and the insights path still fails the job when the tests fail.
func TestCI_insightsRunsOnlyInTheEphJobAndStillFailsOnFailingTests(t *testing.T) {
	t.Parallel()
	w := load(t, "ci.yml")
	for jn, j := range w.Jobs {
		if jn == "test-eph" {
			continue
		}
		if len(j.Permissions) != 0 {
			t.Errorf("ci.yml/%s sets permissions %v: only test-eph may (id-token: write)", jn, j.Permissions)
		}
		for _, st := range j.Steps {
			if _, ok := st.With["insights"]; ok {
				t.Errorf("ci.yml/%s passes insights: only test-eph may", jn)
			}
		}
	}
	if p := w.Permissions; len(p) != 1 || p["contents"] != "read" {
		t.Errorf("ci.yml workflow permissions = %v, want contents: read only", p)
	}
	e := w.Jobs["test-eph"]
	if len(e.Permissions) != 2 || e.Permissions["contents"] != "read" || e.Permissions["id-token"] != "write" {
		t.Errorf("test-eph permissions = %v, want exactly contents: read and id-token: write", e.Permissions)
	}
	if len(e.Steps) != 2 || e.Steps[1].With["insights"] != "true" {
		t.Errorf("test-eph must call the go-test action with insights: \"true\": %+v", e.Steps)
	}

	b, err := os.ReadFile("../../.github/actions/go-test/action.yml")
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Runs struct {
			Steps []step `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	if a.Inputs["insights"].Default != "false" {
		t.Errorf("go-test action: input insights must default to \"false\", got %q", a.Inputs["insights"].Default)
	}
	var plain, tests, upload, verdict *step
	for i := range a.Runs.Steps {
		st := &a.Runs.Steps[i]
		switch {
		case st.Run == "go test -race -timeout 20m ./...":
			plain = st
		case st.ID == "gotest":
			tests = st
		case strings.Contains(st.Run, "mergify ci junit-process"):
			upload = st
		case strings.HasPrefix(st.Name, "Fail when the tests failed"):
			verdict = st
		}
	}
	if plain == nil || tests == nil || upload == nil || verdict == nil {
		t.Fatalf("go-test action: missing a step (plain %v, gotest %v, upload %v, verdict %v)", plain != nil, tests != nil, upload != nil, verdict != nil)
	}
	if plain.If != "inputs.insights != 'true'" {
		t.Errorf("the plain go test step must run exactly when insights is not true, if = %q", plain.If)
	}
	for _, st := range []*step{tests, upload, verdict} {
		if !strings.Contains(st.If, "inputs.insights == 'true'") {
			t.Errorf("go-test action: step %q is not behind inputs.insights == 'true': %q", st.Name, st.If)
		}
	}
	if tests.ContinueOnError != true || !strings.Contains(tests.Run, "/opt/ci-tools/bin/gotestsum") || !strings.Contains(tests.Run, "-race -timeout 20m ./...") {
		t.Errorf("go-test action: the insights test step must be the pinned gotestsum with the same go test flags and continue-on-error: %+v", tests)
	}
	if !strings.Contains(upload.If, "(success() || failure())") || !strings.Contains(upload.If, "github.event.pull_request.head.repo.fork != true") || !strings.Contains(upload.Run, "/opt/ci-tools/bin/mergify") {
		t.Errorf("go-test action: the upload runs only on success() or failure(), never for a fork, with the pinned CLI: %+v", upload)
	}
	if strings.Contains(upload.Run, "--token") || strings.Contains(upload.Run, "MERGIFY_TOKEN") {
		t.Errorf("go-test action: the upload must use OIDC, not a token: %q", upload.Run)
	}
	if !strings.Contains(verdict.If, "steps.gotest.outcome != 'success'") || !strings.Contains(verdict.Run, "exit 1") {
		t.Errorf("go-test action: the last step must fail the job when the tests did not succeed: %+v", verdict)
	}
}
