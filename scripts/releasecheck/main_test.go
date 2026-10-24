package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randybias/mash/internal/release"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	t      *testing.T
	repo   string
	x      string
	origin string
	o      Options
}

// newFixture: a repo whose main (pushed to a local "release repository") holds the candidate X with a buildable
// cmd/mash that prints its version line, a signed tag v0.1.0 on X, and a fake gh serving CI runs and assets.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	repo, origin := filepath.Join(root, "repo"), filepath.Join(root, "origin.git")
	gitT(t, root, "init", "-q", "--bare", origin)
	gitT(t, root, "init", "-q", "-b", "main", repo)
	write(t, repo, "go.mod", "module github.com/randybias/mash\n\ngo 1.27\n")
	write(t, repo, "internal/endpoint/v.go", "package endpoint\n\nvar Version = \"0.0.0-dev\"\n")
	write(t, repo, "cmd/mash/main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/randybias/mash/internal/endpoint\"\n)\n\nvar commit string\n\nfunc main() { fmt.Printf(\"mash %s (commit %s)\\n\", endpoint.Version, commit) }\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "candidate")
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-q", "origin", "main")
	x := gitT(t, repo, "rev-parse", "HEAD")
	signingSetup(t, repo, root)
	gitT(t, repo, "tag", "-s", "v0.1.0", "-m", "release", x)
	gitT(t, repo, "push", "-q", "origin", "v0.1.0")
	f := &fixture{t: t, repo: repo, origin: origin, x: x}
	gh := filepath.Join(root, "gh")
	write(t, root, "gh", "#!/bin/sh\necho \"$*\" >> \""+filepath.Join(root, "gh.log")+"\"\ncase \"$1\" in\n  api) case \"$2\" in *page=2*) cat \""+filepath.Join(root, "ci-2.json")+"\" ;; *) cat \""+filepath.Join(root, "ci-1.json")+"\" ;; esac ;;\n  release) cat \""+filepath.Join(root, "assets.json")+"\" ;;\n  *) echo \"unexpected gh $*\" >&2; exit 1 ;;\nesac\n")
	if err := os.Chmod(gh, 0o755); err != nil {
		t.Fatal(err)
	}
	f.o = Options{Repo: repo, X: x, Version: "0.1.0", GH: gh, GHRepo: "randybias/mash", Remote: origin, Out: &bytes.Buffer{}}
	f.setCI(release.RequiredChecks...)
	return f
}

func signingSetup(t *testing.T, repo, dir string) {
	t.Helper()
	key := filepath.Join(dir, "release-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key, "-C", "release").CombinedOutput(); err != nil {
		t.Skipf("no ssh-keygen to make a signing key: %v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	write(t, dir, "allowed_signers", "t@t "+strings.TrimSpace(string(pub))+"\n")
	for k, v := range map[string]string{"gpg.format": "ssh", "user.signingkey": key + ".pub", "gpg.ssh.allowedSignersFile": filepath.Join(dir, "allowed_signers")} {
		gitT(t, repo, "config", k, v)
	}
}

func ciRun(id int, name, sha string) map[string]any {
	return map[string]any{"id": id, "head_sha": sha, "name": name, "status": "completed", "conclusion": "success"}
}

func (f *fixture) setCIPages(sha string, perPage int, runs []map[string]any) {
	dir := filepath.Dir(f.o.GH)
	for page := 1; ; page++ {
		lo := (page - 1) * perPage
		var chunk []map[string]any
		if lo < len(runs) {
			chunk = runs[lo:min(lo+perPage, len(runs))]
		}
		b, _ := json.Marshal(map[string]any{"total_count": len(runs), "check_runs": chunk})
		write(f.t, dir, fmt.Sprintf("ci-%d.json", page), string(b))
		if lo+perPage >= len(runs) || page == 2 {
			break
		}
	}
}

func (f *fixture) setCI(names ...string) {
	var runs []map[string]any
	for i, n := range names {
		runs = append(runs, ciRun(i+1, n, f.x))
	}
	f.setCIPages(f.x, 100, runs)
}

func (f *fixture) check() error { return check(context.Background(), f.o) }

// ourBuilds is the hash of X built for each target, the way the gate builds it.
func (f *fixture) ourBuilds() map[release.Target]string {
	f.t.Helper()
	h := map[release.Target]string{}
	for _, tg := range release.Targets {
		src := filepath.Join(f.t.TempDir(), "src")
		gitT(f.t, filepath.Dir(f.repo), "clone", "-q", f.repo, src)
		sum, err := release.Build(context.Background(), src, filepath.Join(f.t.TempDir(), "mash"), tg.GOOS, tg.GOARCH, f.o.Version, f.x)
		if err != nil {
			f.t.Fatal(err)
		}
		h[tg] = sum
	}
	return h
}

// setAssets serves a draft release carrying exactly our builds, then lets mutate change it.
func setAssets(t *testing.T, f *fixture, mutate func(map[string]string)) {
	t.Helper()
	h := f.ourBuilds()
	have := map[string]string{}
	for _, tg := range release.Targets {
		have[tg.AssetName()] = "sha256:" + h[tg]
	}
	sums := release.ExpectedSums(h)
	have[release.SumsName] = "sha256:" + shaOf(sums)
	if mutate != nil {
		mutate(have)
	}
	var assets []map[string]string
	for n, d := range have {
		assets = append(assets, map[string]string{"name": n, "digest": d})
	}
	b, _ := json.Marshal(map[string]any{"assets": assets})
	write(t, filepath.Dir(f.o.GH), "assets.json", string(b))
}

func TestCheck_passesAndPrintsTheCompatPin(t *testing.T) {
	f := newFixture(t)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	out := f.o.Out.(*bytes.Buffer).String()
	if !strings.Contains(out, fmt.Sprintf(`compat.json: {"version": "0.1.0", "commit": %q}`, f.x)) {
		t.Fatalf("the pin to copy into compat.json is missing:\n%s", out)
	}
}

func TestCheck_refusals(t *testing.T) {
	cases := map[string]func(f *fixture){
		"a commit that is not on main of the release repository": func(f *fixture) {
			write(t, f.repo, "extra.txt", "x")
			gitT(t, f.repo, "add", "-A")
			gitT(t, f.repo, "commit", "-q", "-m", "unpushed")
			f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
			f.setCIPages(f.o.X, 100, []map[string]any{ciRun(1, "test (ubuntu-latest)", f.o.X), ciRun(2, "test (macos-latest)", f.o.X), ciRun(3, "reproducible", f.o.X)})
		},
		"a required check missing": func(f *fixture) { f.setCI("test (ubuntu-latest)", "test (macos-latest)") },
		"a required check failing": func(f *fixture) {
			runs := []map[string]any{ciRun(1, "test (ubuntu-latest)", f.x), ciRun(2, "test (macos-latest)", f.x), ciRun(3, "reproducible", f.x)}
			runs[2]["conclusion"] = "failure"
			f.setCIPages(f.x, 100, runs)
		},
		"a check still running": func(f *fixture) {
			runs := []map[string]any{ciRun(1, "test (ubuntu-latest)", f.x), ciRun(2, "test (macos-latest)", f.x), ciRun(3, "reproducible", f.x)}
			runs[2]["status"] = "in_progress"
			runs[2]["conclusion"] = nil
			f.setCIPages(f.x, 100, runs)
		},
		"green checks for another commit": func(f *fixture) {
			other := strings.Repeat("e", 40)
			f.setCIPages(other, 100, []map[string]any{ciRun(1, "test (ubuntu-latest)", other), ciRun(2, "test (macos-latest)", other), ciRun(3, "reproducible", other)})
		},
		"two builds that differ": func(f *fixture) {
			n := 0
			f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
				n++
				return fmt.Sprintf("%064d", n), nil
			}
		},
		"a binary that prints another version": func(f *fixture) {
			f.o.Version = "0.1.1" // the tag the binary is built for is 0.1.1 ... but its line is compared to the same version
			f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
				return release.Build(ctx, src, out, goos, goarch, "9.9.9", commit) // stamps a version other than asked
			}
		},
	}
	for name, mutate := range cases {
		f := newFixture(t)
		mutate(f)
		err := f.check()
		if err == nil {
			t.Errorf("%s: passed", name)
			continue
		}
		if got := exitCode(err); got != 3 {
			t.Errorf("%s: exit %d, want 3 (refused): %v", name, got, err)
		}
	}
}

func TestCheck_aForkConfiguredAsOriginDoesNotMakeACandidateOnMain(t *testing.T) {
	f := newFixture(t)
	// The candidate is on the *fork's* main (the local origin), not on the release repository's.
	fork := filepath.Join(t.TempDir(), "fork.git")
	gitT(t, filepath.Dir(f.repo), "init", "-q", "--bare", fork)
	write(t, f.repo, "fork.txt", "x")
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "-m", "only on the fork")
	forkX := gitT(t, f.repo, "rev-parse", "HEAD")
	gitT(t, f.repo, "push", "-q", fork, "HEAD:refs/heads/main")
	gitT(t, f.repo, "remote", "set-url", "origin", fork)
	f.o.X = forkX
	f.setCIPages(forkX, 100, []map[string]any{ciRun(1, "test (ubuntu-latest)", forkX), ciRun(2, "test (macos-latest)", forkX), ciRun(3, "reproducible", forkX)})
	err := f.check() // Remote is still the release repository: main fetched from it does not contain forkX
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("a candidate only on a fork's main passed: %v", err)
	}
}

func TestCheck_ghAndRemoteFailuresAreFourNotARefusal(t *testing.T) {
	f := newFixture(t)
	f.o.GH = filepath.Join(t.TempDir(), "no-such-gh")
	if got := exitCode(f.check()); got != 4 {
		t.Errorf("gh unavailable: exit %d, want 4", got)
	}
	g := newFixture(t)
	g.o.Remote = filepath.Join(t.TempDir(), "no-such-remote.git")
	if got := exitCode(g.check()); got != 4 {
		t.Errorf("remote unreachable: exit %d, want 4", got)
	}
}

func TestCheck_aTruncatedOrMalformedCIResponseCannotPass(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Dir(f.o.GH)
	write(t, dir, "ci-1.json", `{"check_runs": []}`) // no total_count
	if err := f.check(); err == nil {
		t.Fatal("a response without total_count passed")
	}
	runs := []map[string]any{ciRun(1, "test (ubuntu-latest)", f.x), ciRun(2, "test (macos-latest)", f.x), ciRun(3, "reproducible", f.x)}
	b, _ := json.Marshal(map[string]any{"total_count": 3, "check_runs": runs[:1]})
	write(t, dir, "ci-1.json", string(b)) // page 1 holds 1 of 3, page 2 is empty: truncated
	write(t, dir, "ci-2.json", `{"total_count": 3, "check_runs": []}`)
	if err := f.check(); err == nil {
		t.Fatal("a truncated listing passed")
	}
	write(t, dir, "ci-1.json", `not json`)
	if err := f.check(); err == nil {
		t.Fatal("malformed JSON passed")
	}
}

func TestRun_usageAndExitCodes(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{}, {"onlyone"}, {"--assets", "0.1.0"}, {"a", "0.1"}, {"a", "v0.1.0"}, {"a", "0.1.0-rc1"}, {"--assets", "01", "x"}} {
		if got := mainRun(args, &out); got != 2 {
			t.Errorf("%v: exit %d, want 2", args, got)
		}
	}
}

func TestWrapper_preservesTheExitCodes(t *testing.T) {
	run := func(args ...string) int {
		err := exec.Command("../release-check", args...).Run()
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatal(err)
		return -1
	}
	if got := run(); got != 2 {
		t.Errorf("no arguments: exit %d, want 2", got)
	}
	if got := run("no-such-ref-xyz", "0.1.0"); got != 3 {
		t.Errorf("an unknown candidate: exit %d, want 3", got)
	}
}

func TestAssets_passWithOurBuildsAndASignedTagAtX(t *testing.T) {
	f := newFixture(t)
	setAssets(t, f, nil)
	if err := checkAssets(context.Background(), f.o); err != nil {
		t.Fatalf("a correct release was refused: %v", err)
	}
}

func TestAssets_everyWrongAssetSetIsRefused(t *testing.T) {
	cases := map[string]func(m map[string]string){
		"missing binary":     func(m map[string]string) { delete(m, "mash-linux-arm64") },
		"missing sums":       func(m map[string]string) { delete(m, release.SumsName) },
		"substituted binary": func(m map[string]string) { m["mash-darwin-arm64"] = "sha256:" + strings.Repeat("0", 64) },
		"extra asset":        func(m map[string]string) { m["notes.txt"] = "sha256:x" },
		"misnamed binary": func(m map[string]string) {
			m["mash-linux-x86_64"] = m["mash-linux-amd64"]
			delete(m, "mash-linux-amd64")
		},
		"wrong sums": func(m map[string]string) { m[release.SumsName] = "sha256:" + strings.Repeat("1", 64) },
	}
	for name, mutate := range cases {
		f := newFixture(t)
		setAssets(t, f, mutate)
		err := checkAssets(context.Background(), f.o)
		if err == nil {
			t.Errorf("%s: passed", name)
		} else if got := exitCode(err); got != 3 {
			t.Errorf("%s: exit %d, want 3: %v", name, got, err)
		}
	}
}

func tagAt(t *testing.T, f *fixture, annotated bool) {
	t.Helper()
	write(t, f.repo, "later.txt", "later")
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "-m", "later")
	later := gitT(t, f.repo, "rev-parse", "HEAD")
	args := []string{"tag", "-f", "v0.1.0", later}
	if annotated {
		args = []string{"tag", "-f", "-s", "v0.1.0", "-m", "x", later}
	}
	gitT(t, f.repo, args...)
	gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
}

func retag(t *testing.T, f *fixture, args ...string) {
	t.Helper()
	gitT(t, f.repo, append([]string{"tag", "-f"}, args...)...)
	gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
}

func TestAssets_theTagMustPointAtTheCandidate(t *testing.T) {
	for _, annotated := range []bool{true, false} {
		f := newFixture(t)
		setAssets(t, f, nil)
		tagAt(t, f, annotated)
		if err := checkAssets(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "v0.1.0") {
			t.Errorf("annotated=%v: a tag at another commit was accepted: %v", annotated, err)
		}
	}
	f := newFixture(t)
	setAssets(t, f, nil)
	gitT(t, f.repo, "push", "-q", "origin", ":refs/tags/v0.1.0")
	if err := checkAssets(context.Background(), f.o); err == nil {
		t.Error("a release with no tag was accepted")
	}
}

func TestAssets_theTagMustBeAnnotatedAndSignedByATrustedKey(t *testing.T) {
	cases := map[string]func(f *fixture){
		"lightweight": func(f *fixture) { retag(t, f, "v0.1.0", f.x) },
		"annotated but unsigned": func(f *fixture) {
			gitT(t, f.repo, "config", "tag.gpgSign", "false")
			retag(t, f, "-a", "v0.1.0", "-m", "release", f.x)
		},
		"unsigned with signature marker text in the message": func(f *fixture) {
			retag(t, f, "-a", "v0.1.0", "-m", "-----BEGIN SSH SIGNATURE-----\nfake\n-----END SSH SIGNATURE-----", f.x)
		},
		"signed by a key nobody trusts": func(f *fixture) {
			other := t.TempDir()
			if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(other, "k")).CombinedOutput(); err != nil {
				t.Skipf("%v %s", err, out)
			}
			gitT(t, f.repo, "config", "user.signingkey", filepath.Join(other, "k.pub"))
			retag(t, f, "-s", "v0.1.0", "-m", "release", f.x)
		},
	}
	for name, mutate := range cases {
		g := newFixture(t)
		setAssets(t, g, nil)
		mutate(g)
		if err := checkAssets(context.Background(), g.o); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAssets_aTagMovedWhileTheGateRunsIsCaught(t *testing.T) {
	f := newFixture(t)
	setAssets(t, f, nil)
	f.o.afterTagList = func() { tagAt(t, f, true) } // swapped between listing and fetching
	if err := checkAssets(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "v0.1.0") {
		t.Fatalf("a tag swapped between listing and fetch passed: %v", err)
	}
	g := newFixture(t)
	setAssets(t, g, nil)
	g.o.afterAssets = func() { // replaced after verification by an unsigned tag at the very same commit
		gitT(t, g.repo, "config", "tag.gpgSign", "false")
		retag(t, g, "-a", "v0.1.0", "-m", "unsigned", g.x)
	}
	if err := checkAssets(context.Background(), g.o); err == nil || !strings.Contains(err.Error(), "v0.1.0") {
		t.Fatalf("a tag object replaced after verification passed: %v", err)
	}
}

func TestAssets_aMissingReleaseIsFourNotARefusal(t *testing.T) {
	f := newFixture(t)
	f.o.GH = filepath.Join(t.TempDir(), "no-such-gh")
	if got := exitCode(checkAssets(context.Background(), f.o)); got != 4 {
		t.Errorf("exit %d, want 4", got)
	}
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
