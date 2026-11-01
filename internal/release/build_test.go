package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestBuildFlags_neverTagsAndCarriesVersionAndCommit(t *testing.T) {
	f := strings.Join(BuildFlags("0.1.0", "abc123"), " ")
	for _, want := range []string{"-trimpath", "-buildvcs=false", "-mod=readonly", "-X github.com/bogworks/mash/internal/endpoint.Version=0.1.0", "-X main.commit=abc123"} {
		if !strings.Contains(f, want) {
			t.Errorf("flags %q lack %q", f, want)
		}
	}
	if strings.Contains(f, "-tags") {
		t.Errorf("release flags carry build tags: %q", f)
	}
}

func cloneHead(t *testing.T) string {
	t.Helper()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "src")
	if out, err := testenv.RunGit("clone", "-q", strings.TrimSpace(string(root)), dst); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	return dst
}

func TestBuild_sameSourceSameHashInDifferentDirectories(t *testing.T) {
	a, b := cloneHead(t), cloneHead(t)
	ctx := context.Background()
	outA, outB := filepath.Join(t.TempDir(), "mash"), filepath.Join(t.TempDir(), "mash")
	ha, err := Build(ctx, a, outA, "", "", "0.1.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	hb, err := Build(ctx, b, outB, "", "", "0.1.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb || ha == "" {
		t.Fatalf("builds differ across clone directories: %s vs %s", ha, hb)
	}
	if fi, err := os.Stat(outA); err != nil || fi.Size() == 0 {
		t.Fatalf("no binary written: %v", err)
	}
}

func TestBuild_versionChangesWhatTheBinaryPrints(t *testing.T) {
	src := cloneHead(t)
	x := gitIn(t, src, "rev-parse", "HEAD")
	for _, v := range []string{"0.1.0", "0.1.1"} {
		out := filepath.Join(t.TempDir(), "mash")
		if _, err := Build(context.Background(), src, out, "", "", v, x); err != nil {
			t.Fatal(err)
		}
		got, err := exec.Command(out, "version").Output()
		if err != nil || strings.TrimSpace(string(got)) != "mash "+v+" (commit "+x+")" {
			t.Fatalf("version %s: %q %v", v, got, err)
		}
	}
}

func TestBuild_versionChangesTheHash(t *testing.T) {
	src := cloneHead(t)
	ctx := context.Background()
	h1, err := Build(ctx, src, filepath.Join(t.TempDir(), "r"), "", "", "0.1.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := Build(ctx, src, filepath.Join(t.TempDir(), "r"), "", "", "0.1.1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("the version string is not part of the binary")
	}
}

func TestBuildEnv_pinsTheToolchainAndDropsAmbientConfig(t *testing.T) {
	hostile := []string{
		"PATH=/usr/bin", "HOME=/h", "GOTOOLCHAIN=local", "GOENV=/evil", "GOWORK=/evil/go.work", "GOROOT=/evil",
		"GOEXPERIMENT=boringcrypto", "GOAMD64=v3", "GOARM64=v9.0", "GOFLAGS=-tags=failpoints", "CGO_ENABLED=1",
		"CGO_CFLAGS=-O3", "GOOS=windows", "GOARCH=riscv64", "GOCACHE=/c", "GOMODCACHE=/m", "GOPROXY=off",
	}
	env := strings.Join(buildEnv(hostile, "linux", "arm64"), "\n")
	for _, want := range []string{"GOTOOLCHAIN=" + Toolchain, "GOENV=off", "GOWORK=off", "CGO_ENABLED=0", "GOFLAGS=", "GOOS=linux", "GOARCH=arm64", "PATH=/usr/bin", "GOCACHE=/c", "GOMODCACHE=/m", "GOPROXY=off"} {
		if !strings.Contains(env+"\n", want+"\n") {
			t.Errorf("env lacks %q:\n%s", want, env)
		}
	}
	for _, bad := range []string{"GOTOOLCHAIN=local", "/evil", "boringcrypto", "GOAMD64", "GOARM64", "-tags=failpoints", "CGO_ENABLED=1", "CGO_CFLAGS", "windows", "riscv64"} {
		if strings.Contains(env, bad) {
			t.Errorf("env still carries %q:\n%s", bad, env)
		}
	}
}

func TestBuild_ignoresAHostileAmbientEnvironment(t *testing.T) {
	src := cloneHead(t)
	ctx := context.Background()
	clean, err := Build(ctx, src, filepath.Join(t.TempDir(), "r"), "linux", "amd64", "0.1.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	// Settings that would change the compiled bytes if they reached the build, including ones that arrive in a
	// go env file, a workspace and a separate build cache.
	envFile := filepath.Join(t.TempDir(), "go.env")
	if err := os.WriteFile(envFile, []byte("GOFLAGS=-ldflags=-s -w\nGOAMD64=v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "go.work")
	if err := os.WriteFile(work, []byte("go 1.27.3\n\nuse "+src+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", envFile)
	t.Setenv("GOWORK", work)
	t.Setenv("GOAMD64", "v3")
	t.Setenv("GOFLAGS", "-ldflags=-s -w")
	t.Setenv("GOEXPERIMENT", "nocoverageredesign")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOCACHE", t.TempDir())
	hostile, err := Build(ctx, src, filepath.Join(t.TempDir(), "r"), "linux", "amd64", "0.1.0", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if clean != hostile {
		t.Fatal("the ambient Go environment changed the release binary")
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCheckSource(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	outside := filepath.Join(t.TempDir(), "mash")
	if err := CheckSource(dir, x, outside); err != nil {
		t.Fatalf("clean source at X refused: %v", err)
	}
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	if err := CheckSource(dir, x, outside); err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("source at another commit accepted: %v", err)
	}
	y := gitIn(t, dir, "rev-parse", "HEAD")
	if err := CheckSource(dir, y[:12], outside); err == nil {
		t.Fatal("an abbreviated commit was accepted as the full SHA")
	}
	if err := os.WriteFile(filepath.Join(dir, "dirt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(dir, y, outside); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("a dirty tree accepted: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "dirt"))
	if err := CheckSource(dir, y, filepath.Join(dir, "out", "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("output inside the clone accepted: %v", err)
	}
}

func TestCheckSource_containmentIsExactNotAPrefixMatch(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	// A first path component that merely starts with ".." is inside the tree.
	if err := CheckSource(dir, x, filepath.Join(dir, "..data", "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("an output under a directory named ..data inside the tree was accepted: %v", err)
	}
	// A sibling that shares the tree's name as a prefix is outside it.
	if err := CheckSource(dir, x, dir+"-out/range"); err != nil {
		t.Fatalf("a sibling directory was taken for the inside: %v", err)
	}
	// A symlink to the tree does not hide an output inside it.
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(dir, x, filepath.Join(link, "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("an output reached through a symlink to the tree was accepted: %v", err)
	}
}

func TestCheckSource_danglingAndLoopingSymlinksAreResolvedNotGuessed(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "not-created-yet")
	for name, target := range map[string]string{"out": outside, "inward": "missing", "loop": "loop"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "links")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	// A tracked symlink that points outside, dangling: the output is outside the tree.
	if err := CheckSource(dir, x, filepath.Join(dir, "out", "mash")); err != nil {
		t.Fatalf("an output behind a dangling symlink that leaves the tree was called inside: %v", err)
	}
	// One that points back into the tree, dangling: inside.
	if err := CheckSource(dir, x, filepath.Join(dir, "inward", "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("an output behind a dangling symlink into the tree was accepted: %v", err)
	}
	// A loop cannot be resolved: that is an error, not a guess.
	if err := CheckSource(dir, x, filepath.Join(dir, "loop", "mash")); err == nil || strings.Contains(err.Error(), "inside") {
		t.Fatalf("a symlink loop: %v (want a resolution error)", err)
	}
}

func TestCheckSource_dotDotAfterASymlinkGoesWhereTheFilesystemGoes(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "checkout")
	if err := os.MkdirAll(filepath.Join(tree, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, tree, "init", "-q")
	if err := os.WriteFile(filepath.Join(tree, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, tree, "add", "-A")
	gitIn(t, tree, "commit", "-q", "-m", "c")
	x := gitIn(t, tree, "rev-parse", "HEAD")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// outside/link -> checkout/subdir ; outside/out -> outside/link/.. , which the filesystem reads as checkout.
	if err := os.Symlink(filepath.Join(tree, "subdir"), filepath.Join(outside, "link")); err != nil {
		t.Fatal(err)
	}
	// outside+"/link/.." and not filepath.Join: Join would clean the ".." away.
	if err := os.Symlink(outside+"/link/..", filepath.Join(outside, "out")); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(tree, x, filepath.Join(outside, "out", "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("an output that the filesystem places in the checkout was accepted: %v", err)
	}
	// A relative target with the same shape.
	if err := os.Symlink("link/..", filepath.Join(outside, "rel")); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(tree, x, filepath.Join(outside, "rel", "mash")); err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("a relative target: %v", err)
	}
}

func TestBuild_writesTheOutputWhereTheFilesystemPutsIt(t *testing.T) {
	src := cloneHead(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "deep"), filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	out := src + "/link/../bin/mash" // the filesystem reads link/.. as <outside>
	if _, err := Build(context.Background(), src, out, "", "", "0.1.0", "abc123"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "bin", "mash")); err != nil {
		t.Fatalf("the binary is not where the filesystem puts the path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "bin", "mash")); err == nil {
		t.Fatal("Build cleaned the path lexically and wrote into the checkout")
	}
}

func TestCheckSource_dotDotThroughAMissingDirectoryIsAnErrorNotAGuess(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	if err := CheckSource(dir, x, filepath.Join(t.TempDir(), "missing")+"/../range"); err == nil || !strings.Contains(err.Error(), "..") {
		t.Fatalf("a path that goes through a missing directory and back was accepted: %v", err)
	}
}

func TestToolchain_matchesGoMod(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var goLine string
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "go ") {
			goLine = strings.TrimSpace(strings.TrimPrefix(ln, "go "))
		}
	}
	if "go"+goLine != Toolchain {
		t.Fatalf("go.mod says go %s but the release build uses %s", goLine, Toolchain)
	}
}

// A release binary names its version and commit exactly: what `mash version` prints is what the gate will compare.
func TestBuild_binaryPrintsTheVersionAndCommit(t *testing.T) {
	src := cloneHead(t)
	x := gitIn(t, src, "rev-parse", "HEAD")
	out := filepath.Join(t.TempDir(), "mash")
	if err := CheckSource(src, x, out); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), src, out, "", "", "0.1.0", x); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(out, "version").Output()
	if err != nil || strings.TrimSpace(string(got)) != "mash 0.1.0 (commit "+x+")" {
		t.Fatalf("%q %v", got, err)
	}
}

// Source that is not exactly the commit is refused whatever git is configured to hide.
func TestCheckSource_untrackedAndIgnoredFilesAreRefusedWhateverGitIsConfiguredToHide(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	out := filepath.Join(t.TempDir(), "mash")
	home := t.TempDir()
	// A global configuration that hides untracked files and ignores *.go everywhere.
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[status]\n\tshowUntrackedFiles = no\n[core]\n\texcludesFile = "+filepath.Join(home, "ignore")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "ignore"), []byte("*.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if err := os.WriteFile(filepath.Join(dir, "evil.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(dir, x, out); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("a stray ignored .go file in the tree was accepted: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, "evil.go"))
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckSource(dir, x, out); err == nil || !strings.Contains(err.Error(), "clean") {
		t.Fatalf("a stray untracked file hidden by showUntrackedFiles=no was accepted: %v", err)
	}
}

func TestCheckSource_gitRedirectionInTheEnvironmentIsIgnored(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "one")
	x := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, other, "init", "-q")
	gitIn(t, other, "commit", "-q", "--allow-empty", "-m", "elsewhere")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(other, ".git"))
	if err := CheckSource(dir, x, filepath.Join(t.TempDir(), "out")); err != nil {
		t.Fatalf("an inherited GIT_DIR changed which repository was judged: %v", err)
	}
}

func TestCheckSource_gitFailuresKeepStderrAndAreToolErrors(t *testing.T) {
	err := CheckSource(t.TempDir(), strings.Repeat("a", 40), filepath.Join(t.TempDir(), "out"))
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("a directory that is not a checkout is %v, want a *ToolError", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") || !strings.Contains(err.Error(), "rev-parse") {
		t.Fatalf("the error lost git's reason or the command: %v", err)
	}
}
