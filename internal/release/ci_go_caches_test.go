package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	"ci.yml/test":              "race",
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
