package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/internal/testenv"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
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
	t          *testing.T
	wantErr    string // when set, the refusal must say this
	repo       string
	x          string
	origin     string
	o          Options
	measuredMu sync.Mutex
	measured   []string    // the binaries the faked latency measurement ran against; the step measures in parallel
	gate       *gateRecord // what the --assets gate of this test established (signedDraft)
	specs      []string    // for each measurement, "<binary dir>:<serve args joined by space>", in the same order
}

// newFixture: a repo whose main (pushed to a local "release repository") holds the candidate X with a buildable
// cmd/mash that prints its version line, a signed tag v0.1.0 on X, and a fake gh serving CI runs and assets.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	repo, origin := filepath.Join(root, "repo"), filepath.Join(root, "origin.git")
	gitT(t, root, "init", "-q", "--bare", origin)
	gitT(t, root, "init", "-q", "-b", "main", repo)
	sumsKey := newSumsKey(t, root, "sums-key")
	write(t, repo, signersPath, "release "+sumsKey.pub+"\n")
	write(t, repo, "go.mod", "module github.com/bogworks/mash\n\ngo 1.27\n")
	write(t, repo, "internal/endpoint/v.go", "package endpoint\n\nvar Version = \"0.0.0-dev\"\n")
	write(t, repo, "cmd/mash/main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/bogworks/mash/internal/endpoint\"\n)\n\nvar commit string\n\nfunc main() { fmt.Printf(\"mash %s (commit %s)\\n\", endpoint.Version, commit) }\n")
	write(t, repo, "plugin/.claude-plugin/plugin.json", "{\"name\": \"mash\", \"version\": \"0.1.0\"}\n")
	write(t, repo, release.PluginHooks, hardenedHooks)
	write(t, repo, renderContractPath, "contract v1\n")
	write(t, repo, "scripts/"+release.ScriptName, "#!/bin/sh\necho quickstart\n")
	write(t, repo, release.PluginMCP, "{\"mcpServers\": {\"mash\": {\"command\": \"mash\", \"args\": [\"serve\"]}}}\n")
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
	ghScript := `#!/bin/sh
echo "$*" >> "ROOT/gh.log"
case "$1" in
  api) case "$2" in *page=2*) cat "ROOT/ci-2.json" ;; *) cat "ROOT/ci-1.json" ;; esac ;;
  release) case "$2" in list) cat "ROOT/releases.json" ;;
    download) n=; p=; for a in "$@"; do [ "$p" = --pattern ] && n=$a; p=$a; done
      case "$n" in *.cdx.json|*.jsonl) ;; mash-*) echo "bin-$n"; exit 0 ;; esac
      if [ -f "ROOT/files/$n" ]; then cat "ROOT/files/$n"; else echo "no assets match the file pattern" >&2; exit 1; fi ;;
    upload) mkdir -p "ROOT/files"; cp "$4" "ROOT/files/$(basename "$4")" ;;
    *) cat "ROOT/assets.json" ;; esac ;;
  attestation) b=$(basename "$3"); sbom=; bundle=
    for a in "$@"; do [ "$a" = --predicate-type ] && sbom=1; [ "$a" = --bundle ] && bundle=1; done
    if [ -n "$bundle" ]; then [ -f "ROOT/noprov/$b" ] && { echo "verification failed: no matching provenance" >&2; exit 1; }
    elif [ -n "$sbom" ]; then [ -f "ROOT/noattest-sbom/$b" ] && { echo "no attestations found" >&2; exit 1; }
    else [ -f "ROOT/noattest/$b" ] && { echo "no attestations found" >&2; exit 1; }; fi
    if [ -n "$sbom" ] && [ -z "$bundle" ]; then
      h=$( (sha256sum "$3" 2>/dev/null || shasum -a 256 "$3") | cut -d' ' -f1)
      [ -f "ROOT/badsubject/$b" ] && h=0000
      if [ -f "ROOT/predicate/$b" ]; then pred=$(cat "ROOT/predicate/$b"); else pred=$(cat "$(dirname "$3")/$b.cdx.json"); fi
      printf '[{"verificationResult":{"statement":{"predicateType":"https://cyclonedx.org/bom","subject":[{"name":"%s","digest":{"sha256":"%s"}}],"predicate":%s}}}]\n' "$b" "$h" "$pred"
    fi ;;
  *) echo "unexpected gh $*" >&2; exit 1 ;;
esac
`
	write(t, root, "gh", strings.ReplaceAll(ghScript, "ROOT", root))
	if err := os.Chmod(gh, 0o755); err != nil {
		t.Fatal(err)
	}
	f.o = Options{Repo: repo, X: x, Version: "0.1.0", GH: gh, GHRepo: "bogworks/mash", Remote: origin, Out: &bytes.Buffer{},
		Signers: filepath.Join(root, "allowed_signers")}
	t.Setenv("RELEASE_SIGNING_KEY", sumsKey.priv)
	f.setCI(release.RequiredChecks...)
	f.setReleases(map[string]bool{"v0.0.9": false}) // the tag several tests add below the candidate: published
	f.o.candBinary = candidateBinary(goodMash)
	// The fixture's binaries are shell scripts that cannot talk to NATS: a measurement that is fast and clean, by default.
	f.setLatency(func(string) latencies { return latencies{Fresh: 100 * time.Millisecond, FreshOK: true} })
	return f
}

// setLatency fakes the delivery-latency measurement: by is called with the binary's base directory name (cand-bin,
// prev-bin) and every measured binary is recorded in f.measured.
func (f *fixture) setLatency(by func(which string) latencies) {
	f.setLatencySpec(func(which string, _ serveSpec) latencies { return by(which) })
}

// setLatencySpec is setLatency for a fake that also sees the serve spec the binary is measured with; the spec is
// recorded in f.specs.
func (f *fixture) setLatencySpec(by func(which string, spec serveSpec) latencies) {
	f.o.measure = func(_ context.Context, bin string, spec serveSpec, _, _ bool) (latencies, error) {
		which := filepath.Base(filepath.Dir(bin))
		f.measuredMu.Lock()
		f.measured = append(f.measured, which)
		f.specs = append(f.specs, which+":"+strings.Join(spec.Args, " "))
		f.measuredMu.Unlock()
		return by(which, spec), nil
	}
}

// goodMash is a candidate that behaves: hook and serve are silent and exit 0.
const goodMash = "#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; esac\nexit 0\n"

// serveReply is what a serving mash does for the gate's initialize: read the request, answer it.
const serveReply = `read l; echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"mash"}}}'`

// candidateBinary is the candidate-binary seam: the script body, written into dir.
func candidateBinary(body string) func(context.Context, string) (string, error) {
	return func(_ context.Context, dir string) (string, error) {
		p := filepath.Join(dir, "cand")
		return p, os.WriteFile(p, []byte(body), 0o755)
	}
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
	return map[string]any{"id": id, "head_sha": sha, "name": name, "status": "completed", "conclusion": "success", "app": map[string]any{"slug": "github-actions"}}
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

// dsseLine is one line of an .intoto.jsonl file in the shape actions/attest-build-provenance writes: a Sigstore bundle whose
// dsseEnvelope.payload is the given in-toto statement.
func dsseLine(statement string) string {
	return `{"mediaType":"application/vnd.dev.sigstore.bundle+json;version=0.2","verificationMaterial":{},"dsseEnvelope":{"payload":"` + base64.StdEncoding.EncodeToString([]byte(statement)) + `","payloadType":"application/vnd.in-toto+json","signatures":[]}}` + "\n"
}

// bareDSSELine is one line holding a bare DSSE envelope (top-level payload), which is also accepted.
func bareDSSELine(statement string) string {
	return `{"payloadType":"application/vnd.in-toto+json","payload":"` + base64.StdEncoding.EncodeToString([]byte(statement)) + `","signatures":[]}` + "\n"
}

// goodCIAsset is the content the fixture gives an SBOM (a CycloneDX 1.5 document) or the provenance (a Sigstore-bundle
// line naming release.yml).
func goodCIAsset(name string) string {
	if name == release.ProvenanceName {
		return dsseLine(`{"predicate":{"buildDefinition":{"externalParameters":{"workflow":{"path":".github/workflows/release.yml"}}}}}`)
	}
	return `{"bomFormat":"CycloneDX","specVersion":"1.5","metadata":{"component":{"name":"` + name + `"}},"components":[{"type":"library","name":"x"}]}` + "\n"
}

// setAssets serves a draft release carrying exactly our builds, then lets mutate change it.
func setAssets(t *testing.T, f *fixture, mutate func(map[string]string)) {
	t.Helper()
	h := f.ourBuilds()
	have := map[string]string{}
	for _, tg := range release.Targets {
		have[tg.AssetName()] = "sha256:" + h[tg]
	}
	filesDir := filepath.Join(filepath.Dir(f.o.GH), "files")
	ci := map[string]string{}
	for _, n := range release.CIAssetNames() {
		body := goodCIAsset(n)
		write(t, filesDir, n, body)
		have[n] = "sha256:" + shaOf(body)
		ci[n] = shaOf(body)
	}
	sums := release.ExpectedSums(h, shaOf("#!/bin/sh\necho quickstart\n"), ci)
	have[release.ScriptName] = "sha256:" + shaOf("#!/bin/sh\necho quickstart\n")
	have[release.SumsName] = "sha256:" + shaOf(sums)
	if mutate != nil {
		mutate(have)
	}
	var assets []map[string]string
	for n, d := range have {
		assets = append(assets, map[string]string{"name": n, "digest": d})
	}
	b, _ := json.Marshal(map[string]any{"isDraft": true, "assets": assets})
	write(t, filepath.Dir(f.o.GH), "assets.json", string(b))
	write(t, filepath.Join(filepath.Dir(f.o.GH), "files"), release.SumsName, sums)
}

func TestCheck_passesAndPrintsTheVersionAndCommit(t *testing.T) {
	f := newFixture(t)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	out := f.o.Out.(*bytes.Buffer).String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the pass line and the version line:\n%s", out)
	}
	if got, want := lines[len(lines)-2], fmt.Sprintf("release-check: %s passes for v0.1.0", f.x); got != want {
		t.Fatalf("pass line = %q, want %q", got, want)
	}
	if got, want := lines[len(lines)-1], fmt.Sprintf("version: 0.1.0 commit: %s", f.x); got != want {
		t.Fatalf("version line = %q, want %q", got, want)
	}
	for _, w := range []string{"compat", "pin"} {
		if strings.Contains(strings.ToLower(out), w) {
			t.Fatalf("the output names %q:\n%s", w, out)
		}
	}
}

func TestCheck_refusals(t *testing.T) {
	cases := map[string]func(f *fixture){
		"a commit that is not on main of the release repository": func(f *fixture) {
			write(t, f.repo, "extra.txt", "x")
			gitT(t, f.repo, "add", "-A")
			gitT(t, f.repo, "commit", "-q", "-m", "unpushed")
			f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
			f.setCIPages(f.o.X, 100, []map[string]any{ciRun(1, "test (linux)", f.o.X), ciRun(2, "reproducible", f.o.X)})
		},
		"a required check missing": func(f *fixture) { f.setCI("test (linux)") },
		"a required check failing": func(f *fixture) {
			runs := []map[string]any{ciRun(1, "test (linux)", f.x), ciRun(2, "reproducible", f.x)}
			runs[1]["conclusion"] = "failure"
			f.setCIPages(f.x, 100, runs)
		},
		"a check still running": func(f *fixture) {
			runs := []map[string]any{ciRun(1, "test (linux)", f.x), ciRun(2, "reproducible", f.x)}
			runs[1]["status"] = "in_progress"
			runs[1]["conclusion"] = nil
			f.setCIPages(f.x, 100, runs)
		},
		"green checks for another commit": func(f *fixture) {
			other := strings.Repeat("e", 40)
			f.setCIPages(other, 100, []map[string]any{ciRun(1, "test (linux)", other), ciRun(2, "reproducible", other)})
		},
		"two builds that differ": func(f *fixture) {
			n := 0
			f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
				n++
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					return "", err
				}
				if err := os.WriteFile(out, []byte("#!/bin/sh\necho x\n"), 0o755); err != nil { // a real file, so the failure is the comparison
					return "", err
				}
				return fmt.Sprintf("%064d", n), nil
			}
			f.wantErr = "not reproducible"
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
		if f.wantErr != "" && !strings.Contains(err.Error(), f.wantErr) {
			t.Errorf("%s: refused for the wrong reason: %v (want %q)", name, err, f.wantErr)
		}
	}
}

func TestCheck_aPluginManifestAtAnotherVersionIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"stale version": "{\"version\": \"0.0.9\"}\n",
		"bad json":      "{nope",
		"no manifest":   "",
	} {
		f := newFixture(t)
		if body == "" {
			gitT(t, f.repo, "rm", "-q", "plugin/.claude-plugin/plugin.json")
		} else {
			write(t, f.repo, "plugin/.claude-plugin/plugin.json", body)
		}
		gitT(t, f.repo, "add", "-A")
		gitT(t, f.repo, "commit", "-q", "-m", "manifest")
		gitT(t, f.repo, "push", "-q", "origin", "main")
		f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
		f.setCI(release.RequiredChecks...)
		f.setCIPages(f.o.X, 100, []map[string]any{ciRun(1, "test (linux)", f.o.X), ciRun(2, "reproducible", f.o.X)})
		err := f.check()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "plugin/.claude-plugin/plugin.json") {
			t.Errorf("%s: want a refusal naming the manifest, got %v", name, err)
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
	f.setCIPages(forkX, 100, []map[string]any{ciRun(1, "test (linux)", forkX), ciRun(2, "reproducible", forkX)})
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
	runs := []map[string]any{ciRun(1, "test (linux)", f.x), ciRun(2, "reproducible", f.x)}
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
		"missing script":  func(m map[string]string) { delete(m, release.ScriptName) },
		"modified script": func(m map[string]string) { m[release.ScriptName] = "sha256:" + strings.Repeat("0", 64) },
		"wrong sums":      func(m map[string]string) { m[release.SumsName] = "sha256:" + strings.Repeat("1", 64) },
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

// The gate trusts only its allowed_signers file (docs/allowed_signers by default), never the machine's git config: a
// tag signed by a key the machine trusts, but the file does not name, is refused.
func TestAssets_theMachinesTrustedKeysDoNotCount(t *testing.T) {
	f := newFixture(t)
	setAssets(t, f, nil)
	other := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(other, "k")).CombinedOutput(); err != nil {
		t.Skipf("%v %s", err, out)
	}
	pub, _ := os.ReadFile(filepath.Join(other, "k.pub"))
	write(t, other, "signers", "x@x "+strings.TrimSpace(string(pub))+"\n")
	f.o.Signers = filepath.Join(other, "signers") // the release file names another key; the repo's config trusts the signer
	if err := checkAssets(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a key only the machine trusts passed: %v", err)
	}
	g := Options{Repo: "/some/checkout"}
	_ = g.defaults() // refuses the missing commit and version, after setting the defaults
	if g.Signers != "/some/checkout/docs/allowed_signers" {
		t.Fatalf("default signers %q", g.Signers)
	}
}

// A valid PGP signature, by a key this machine's GPG trusts, is not a release signature: docs/allowed_signers governs
// SSH keys only, so the gate refuses any tag that is not SSH-signed (codex H1 on #24).
func TestAssets_aPGPSignedTagIsRefused(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("no gpg")
	}
	f := newFixture(t)
	setAssets(t, f, nil)
	home := t.TempDir()
	t.Setenv("GNUPGHOME", home)
	if out, err := exec.Command("gpg", "--batch", "--passphrase", "", "--quick-gen-key", "pgp@x", "ed25519", "sign", "never").CombinedOutput(); err != nil {
		t.Skipf("gpg key: %v %s", err, out)
	}
	gitT(t, f.repo, "config", "gpg.format", "openpgp")
	gitT(t, f.repo, "config", "user.signingkey", "pgp@x")
	retag(t, f, "-s", "v0.1.0", "-m", "release", f.x)
	if out, err := testenv.RunGit("-C", f.repo, "verify-tag", "v0.1.0"); err != nil {
		t.Skipf("the PGP tag does not verify on this machine, so it proves nothing: %v %s", err, out)
	}
	if err := checkAssets(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "not SSH-signed") {
		t.Fatalf("a PGP-signed tag passed the release gate: %v", err)
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

func TestCheck_exitCodesOfEmptyAndUnreadableResponses(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Dir(f.o.GH), "ci-1.json", `{"total_count": 0, "check_runs": []}`)
	if got := exitCode(f.check()); got != 3 { // CI demonstrably has not run: refused, not "could not decide"
		t.Errorf("zero check runs: exit %d, want 3", got)
	}
	g := newFixture(t)
	setAssets(t, g, nil)
	write(t, filepath.Dir(g.o.GH), "assets.json", "not json")
	if got := exitCode(checkAssets(context.Background(), g.o)); got != 4 {
		t.Errorf("an unreadable assets listing: exit %d, want 4", got)
	}
}

// The binary's line must be exactly "mash <version> (commit X)" and one newline: surrounding whitespace is refused.
func TestCheck_versionLineMustBeExact(t *testing.T) {
	f := newFixture(t)
	f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return "", err
		}
		line := "mash " + version + " (commit " + commit + ")"
		if err := os.WriteFile(out, []byte("#!/bin/sh\nprintf '\\n  "+line+"\\n\\n'\n"), 0o755); err != nil {
			return "", err
		}
		return strings.Repeat("a", 64), nil
	}
	err := f.check()
	if err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("a version line with surrounding whitespace passed: %v", err)
	}
}

// The binary under test is written to a name nobody can predict: a planted symlink at the old, guessable path is
// never followed.
func TestReproducible_doesNotWriteThroughAPredictableTempPath(t *testing.T) {
	f := newFixture(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(os.TempDir(), fmt.Sprintf("release-check-%d-mash", os.Getpid()))
	if err := os.Symlink(victim, planted); err != nil {
		t.Skipf("cannot plant a symlink: %v", err)
	}
	defer os.Remove(planted)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("the gate wrote through a planted symlink: %q", b)
	}
}

// stubBuild installs a build that leaves whatever binary body it is given at out (nil: no file at all).
func stubBuild(f *fixture, body []byte) {
	f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
		if body != nil {
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(out, body, 0o755); err != nil {
				return "", err
			}
		}
		return strings.Repeat("a", 64), nil
	}
}

// A failure of the gate's own plumbing (the built file cannot be read, the binary cannot be started) says nothing
// about the candidate: exit 4, could not decide.
func TestReproducible_localFailuresAreFourNotARefusal(t *testing.T) {
	f := newFixture(t)
	stubBuild(f, nil) // the build "succeeds" and leaves no file to stage
	if got := exitCode(f.check()); got != 4 {
		t.Errorf("a built file that cannot be read: exit %d, want 4", got)
	}
	g := newFixture(t)
	stubBuild(g, []byte("not an executable")) // exec fails to start it (no shebang, not an object file)
	if got := exitCode(g.check()); got != 4 {
		t.Errorf("a binary that cannot be started: exit %d, want 4", got)
	}
	h := newFixture(t)
	stubBuild(h, []byte("#!/bin/sh\nexit 1\n")) // it ran and failed: the candidate's fault
	if got := exitCode(h.check()); got != 3 {
		t.Errorf("a binary that exits 1: exit %d, want 3", got)
	}
}

// The candidate is unvetted code: a `version` that never returns is a refusal, not a stalled gate.
func TestReproducible_aHangingVersionIsRefusedAtADeadline(t *testing.T) {
	old := versionTimeout
	versionTimeout = 300 * time.Millisecond
	defer func() { versionTimeout = old }()
	f := newFixture(t)
	stubBuild(f, []byte("#!/bin/sh\nsleep 30\n")) // the sleep outlives the shell and keeps the pipe open
	start := time.Now()
	err := f.check()
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the gate waited %v for a hanging binary", time.Since(start))
	}
	if exitCode(err) != 3 || err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("a hanging binary: %v (exit %d), want a refusal that says so", err, exitCode(err))
	}
}

// A `version` that exits 0 but leaves a child holding its output open is the candidate's misbehaviour too.
func TestReproducible_aLingeringChildIsARefusalNotFour(t *testing.T) {
	f := newFixture(t)
	stubBuild(f, []byte("#!/bin/sh\n(sleep 30 &)\nexit 0\n"))
	err := f.check()
	if exitCode(err) != 3 {
		t.Fatalf("a binary that leaves a child on its output: %v (exit %d), want a refusal", err, exitCode(err))
	}
}

// The wrapper reads the toolchain from internal/release, the one place it is declared.
func TestWrapper_toolchainPinComesFromTheDeclaredConstant(t *testing.T) {
	b, err := os.ReadFile("../release-check")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "go1.") {
		t.Fatal("scripts/release-check hardcodes a Go version")
	}
	var line string
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, "toolchain=") {
			line = ln
		}
	}
	if line == "" || !strings.Contains(string(b), "GOTOOLCHAIN=\"$toolchain\"") {
		t.Fatal("the wrapper does not derive and use $toolchain")
	}
	cmd := exec.Command("sh", "-c", line+"; printf %s \"$toolchain\"")
	cmd.Dir = "../.." // the wrapper cds to the repository root before reading it
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if string(out) != release.Toolchain {
		t.Fatalf("the wrapper pins %q, internal/release.Toolchain is %q", out, release.Toolchain)
	}
}

// hardenedHooks is the shape of the shipped hooks.json: it never exits non-zero and keeps stdout for the context.
const hardenedHooks = `{"hooks": {
  "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "mash hook 2>/dev/null; exit 0"}]}],
  "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "mash hook 2>/dev/null; exit 0"}]}]}}
`

// oldMash is a v0.1.x binary: it has serve, and any other command prints usage and exits 2.
const oldMash = "#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; *) echo 'mash: unknown command' >&2; exit 2 ;; esac\n"

// loggingHooks is the shipped hook command shape: mash hook's stderr goes to the hook log, and it always exits 0.
const loggingHooks = `{"hooks": {"PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "mash hook 2>/dev/null; exit 0"}]}], "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "d=\"${MASH_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/mash}\"; mkdir -p \"$d\" 2>/dev/null; l=\"$d/hook.log\"; (: >>\"$l\") 2>/dev/null || l=/dev/null; mash hook 2>>\"$l\"; exit 0"}]}]}}
`

// withPrevious tags v0.0.9 (below the candidate's 0.1.0) and makes the previous release's binary the script body.
func withPrevious(t *testing.T, f *fixture, body string) {
	t.Helper()
	gitT(t, f.repo, "tag", "v0.0.9", f.x)
	gitT(t, f.repo, "push", "-q", "origin", "v0.0.9")
	f.o.prevBinary = func(ctx context.Context, tag, dir string) (string, error) {
		if tag != "v0.0.9" {
			t.Errorf("downloaded %s, want v0.0.9", tag)
		}
		p := filepath.Join(dir, "prev")
		return p, os.WriteFile(p, []byte(body), 0o755)
	}
}

// commitPlugin commits a plugin file and makes the new commit the candidate (pushed, CI green for it).
func commitPlugin(t *testing.T, f *fixture, name, body string) {
	t.Helper()
	write(t, f.repo, name, body)
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "--allow-empty", "-m", "plugin")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
	f.setCIPages(f.o.X, 100, []map[string]any{ciRun(1, "test (linux)", f.o.X), ciRun(2, "reproducible", f.o.X)})
}

func TestCheck_pluginRunsAgainstThePreviousReleasesBinary(t *testing.T) {
	f := newFixture(t)
	withPrevious(t, f, oldMash)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "previous release v0.0.9") {
		t.Fatalf("the step does not say which release it ran against:\n%s", out)
	}
}

// The release procedure fetches main only: the previous release is the remote's, not whatever tags the checkout has.
func TestCheck_previousReleaseComesFromTheRemoteNotLocalTags(t *testing.T) {
	f := newFixture(t)
	withPrevious(t, f, oldMash)
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, filepath.Dir(clone), "clone", "-q", "--no-tags", f.origin, clone)
	if tags := gitT(t, clone, "tag", "-l"); tags != "" {
		t.Fatalf("the clone has tags: %s", tags)
	}
	f.o.Repo = clone
	if err := pluginCompat(context.Background(), f.o, f.x, &binaries{}); err != nil {
		t.Fatal(err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "previous release v0.0.9") || strings.Contains(out, "no previous release") {
		t.Fatalf("the compatibility check did not run:\n%s", out)
	}
}

func TestCheck_aFailedTagListingIsFour(t *testing.T) {
	f := newFixture(t)
	f.o.Remote = filepath.Join(t.TempDir(), "no-such-remote.git")
	var oe opError
	if err := pluginCompat(context.Background(), f.o, f.x, &binaries{}); !errors.As(err, &oe) {
		t.Fatalf("want an op error (exit 4), got %v", err)
	}
}

func TestCheck_noPreviousReleasePassesWithANote(t *testing.T) {
	f := newFixture(t) // only v0.1.0, the candidate's own tag
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "no previous release") {
		t.Fatalf("no note:\n%s", out)
	}
}

func TestCheck_pluginCompatRefusals(t *testing.T) {
	cases := []struct {
		name, hooks, mash, want string
	}{
		{"a bare hook command against a binary without hook", `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"mash hook"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"mash hook 2>/dev/null; exit 0"}]}]}}`, oldMash, "PostToolUse"},
		{"a hook that prints to stdout", hardenedHooks, "#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; *) echo CTX ;; esac\n", "stdout"},
		{"a binary that does not know serve", hardenedHooks, "#!/bin/sh\necho 'mash: unknown command \"serve\"' >&2; exit 2\n", "serve"},
		{"a hook that exits non-zero", `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"mash hook 2>/dev/null; exit 0"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"mash hook 2>/dev/null"}]}]}}`, oldMash, "UserPromptSubmit"},
	}
	for _, c := range cases {
		f := newFixture(t)
		commitPlugin(t, f, release.PluginHooks, c.hooks)
		withPrevious(t, f, c.mash)
		err := f.check()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "previous release") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal naming the previous release and %q, got %v", c.name, c.want, err)
		}
	}
	f := newFixture(t)
	commitPlugin(t, f, release.PluginMCP, "{\"mcpServers\": {\"mash\": {\"command\": \"node\", \"args\": [\"serve\"]}}}\n")
	withPrevious(t, f, oldMash)
	if err := f.check(); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), `"node"`) {
		t.Errorf("an MCP server that is not mash: %v", err)
	}
	f = newFixture(t)
	commitPlugin(t, f, release.PluginHooks, strings.Replace(hardenedHooks, `"matcher": "*"`, `"matcher": "Nope"`, 1))
	withPrevious(t, f, oldMash)
	if err := f.check(); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "PostToolUse") || !strings.Contains(err.Error(), `"Nope"`) {
		t.Errorf("a PostToolUse matcher that never fires: %v", err)
	}
	f = newFixture(t)
	gitT(t, f.repo, "rm", "-q", release.PluginHooks)
	commitPlugin(t, f, "x.txt", "x")
	withPrevious(t, f, oldMash)
	if err := f.check(); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), release.PluginHooks) {
		t.Errorf("a missing hooks.json: %v", err)
	}
}

// The hook command always exits 0, so a candidate whose `mash hook` or `serve` is broken is only caught by what it
// leaves in the hook log and on its own streams.
func TestCheck_pluginRunsAgainstTheCandidateToo(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"usage on stderr and exit 2", "#!/bin/sh\necho 'usage: mash <command>' >&2; exit 2\n", "candidate"},
		{"stdout from the hook", "#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; *) echo CTX ;; esac\n", "candidate"},
		{"an unknown serve", "#!/bin/sh\ncase \"$1\" in serve) echo 'mash: unknown command \"serve\"' >&2; exit 2 ;; *) exit 0 ;; esac\n", "serve"},
	} {
		f := newFixture(t)
		withPrevious(t, f, oldMash)
		f.o.candBinary = candidateBinary(c.body)
		err := f.check()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal naming the candidate and %q, got %v (exit %d)", c.name, c.want, err, exitCode(err))
		}
	}
	// With no previous release the candidate is still checked.
	f := newFixture(t)
	f.o.candBinary = candidateBinary("#!/bin/sh\necho x >&2; exit 2\n")
	if err := f.check(); err == nil || exitCode(err) != 3 {
		t.Errorf("no previous release, broken candidate: %v", err)
	}
}

// Without the seam the binary comes from gh release download; a gh that cannot is the gate failing, not a refusal.
func TestCheck_aFailedDownloadOfThePreviousReleaseIsFour(t *testing.T) {
	f := newFixture(t)
	gitT(t, f.repo, "tag", "v0.0.9", f.x)
	gitT(t, f.repo, "push", "-q", "origin", "v0.0.9")
	err := f.check() // the fixture's gh has no assets.json to serve
	if err == nil || exitCode(err) != 4 {
		t.Fatalf("exit %d, want 4: %v", exitCode(err), err)
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(f.o.GH), "gh.log"))
	if want := "release download v0.0.9 "; !strings.Contains(string(log), want) || !strings.Contains(string(log), "-p mash-") {
		t.Fatalf("gh was not asked for the previous release's host binary:\n%s", log)
	}
}

// A previous release whose hook failed is only visible in the hook log (the wrapper exits 0): that must be a refusal,
// except `unknown command "hook"` from a release that predates `mash hook`.
func TestCheck_previousReleaseHookLog(t *testing.T) {
	unknown := func(c string) string {
		return "#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; *) echo 'mash: unknown command \"" + c + "\"' >&2; exit 2 ;; esac\n"
	}
	for _, c := range []struct {
		name, tag, body string
		ok              bool
	}{
		{"another unknown command", "v0.2.3", unknown("foo"), false},
		{"hook unknown to a release since hook", "v0.2.3", unknown("hook"), false},
		{"hook unknown to a release before hook", "v0.1.2", unknown("hook"), true},
		{"another unknown command from an old release", "v0.1.2", unknown("foo"), false},
	} {
		f := newFixture(t)
		f.o.Version = "0.3.0"
		gitT(t, f.repo, "tag", c.tag, f.x)
		gitT(t, f.repo, "push", "-q", "origin", c.tag)
		f.o.prevBinary = func(_ context.Context, _, dir string) (string, error) {
			p := filepath.Join(dir, "prev")
			return p, os.WriteFile(p, []byte(c.body), 0o755)
		}
		commitPlugin(t, f, release.PluginHooks, loggingHooks)
		err := pluginCompat(context.Background(), f.o, f.o.X, &binaries{})
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "hook log") || !strings.Contains(err.Error(), "previous release")) {
			t.Errorf("%s: want a hook-log refusal naming the previous release, got %v", c.name, err)
		}
	}
}

// serve must speak MCP for both binaries, not merely exist.
func TestCheck_serveMustAnswerInitialize(t *testing.T) {
	silent := "#!/bin/sh\nexit 0\n"
	errReply := "#!/bin/sh\ncase \"$1\" in serve) read l; echo '{\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-32600,\"message\":\"no\"}}' ;; esac\n"
	for _, c := range []struct {
		name, body string
		cand       bool
	}{
		{"candidate exits silently", silent, true},
		{"candidate answers an error", errReply, true},
		{"previous exits silently", silent, false},
		{"previous answers an error", errReply, false},
	} {
		f := newFixture(t)
		withPrevious(t, f, oldMash)
		who := "previous release"
		if c.cand {
			f.o.candBinary = candidateBinary(c.body)
			who = "candidate"
		} else {
			withPreviousBody(f, c.body)
		}
		err := f.check()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "MCP") || !strings.Contains(err.Error(), who) {
			t.Errorf("%s: want a refusal about MCP naming the %s, got %v", c.name, who, err)
		}
	}
}

// withPreviousBody swaps the previous release's binary after withPrevious.
func withPreviousBody(f *fixture, body string) {
	f.o.prevBinary = func(_ context.Context, _, dir string) (string, error) {
		p := filepath.Join(dir, "prev")
		return p, os.WriteFile(p, []byte(body), 0o755)
	}
}

func TestCheck_latencyStepReusesTheBinariesTheCompatStepBuilt(t *testing.T) {
	f := newFixture(t)
	withPrevious(t, f, oldMash)
	builds, downloads := 0, 0
	cand, dl := f.o.candBinary, f.o.prevBinary
	f.o.candBinary = func(ctx context.Context, dir string) (string, error) { builds++; return cand(ctx, dir) }
	f.o.prevBinary = func(ctx context.Context, tag, dir string) (string, error) { downloads++; return dl(ctx, tag, dir) }
	f.setLatency(func(which string) latencies {
		if which == "prev-bin" {
			return latencies{Fresh: 300 * time.Millisecond, FreshOK: true}
		}
		return latencies{Fresh: 100 * time.Millisecond, FreshOK: true}
	})
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if builds != 1 || downloads != 1 {
		t.Fatalf("built the candidate %d times and downloaded the previous release %d times, want once each", builds, downloads)
	}
	sort.Strings(f.measured)
	if got := strings.Join(f.measured, ","); got != "cand-bin,cand-bin,prev-bin" {
		t.Fatalf("measured %q", got)
	}
	out := f.o.Out.(*bytes.Buffer).String()
	for _, want := range []string{"ok    fresh-session mail arrives no slower than the previous release", "note  fresh-session first mail: candidate 0.10s, v0.0.9 0.30s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func sortedSpecs(f *fixture) string {
	s := append([]string(nil), f.specs...)
	sort.Strings(s)
	return strings.Join(s, "|")
}

func TestCheck_latencyStepMeasuresTheCandidateUnderBothPluginsSpecs(t *testing.T) {
	f := newFixture(t)
	withPreviousPlugin(t, f, map[string]*string{release.PluginMCP: strp(mcpWith(`["serve", "--old"]`, `{}`))})
	f.o.candBinary = candidateBinary(goodMash)
	f.setLatency(func(string) latencies { return latencies{Fresh: 100 * time.Millisecond, FreshOK: true} })
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if got, want := sortedSpecs(f), "cand-bin:serve|cand-bin:serve --old|prev-bin:serve --old"; got != want {
		t.Fatalf("measured %q, want %q", got, want)
	}
	out := f.o.Out.(*bytes.Buffer).String()
	if want := "the candidate under the previous release's plugin (v0.0.9) 0.10s, v0.0.9 0.10s"; !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestCheck_aCandidateSlowUnderThePreviousReleasesPluginIsRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		l    latencies
		want string
	}{
		{"slow first mail", latencies{Fresh: 45 * time.Second, FreshOK: true}, "took the candidate under the previous release's plugin (v0.0.9) 45.00s"},
		{"a finding", latencies{Fresh: 100 * time.Millisecond, FreshOK: true, Findings: []string{"mail never arrived"}}, "the candidate under the previous release's plugin (v0.0.9): mail never arrived"},
	} {
		f := newFixture(t)
		withPreviousPlugin(t, f, map[string]*string{release.PluginMCP: strp(mcpWith(`["serve", "--old"]`, `{}`))})
		f.setLatencySpec(func(which string, spec serveSpec) latencies {
			if which == "cand-bin" && strings.Join(spec.Args, " ") == "serve --old" {
				return c.l
			}
			return latencies{Fresh: 100 * time.Millisecond, FreshOK: true}
		})
		err := f.check()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal (exit 3) containing %q, got %v", c.name, c.want, err)
		}
	}
}

func TestCheck_latencyUnderAnUnreadablePreviousPluginIsSkippedWithANote(t *testing.T) {
	f := newFixture(t)
	withPreviousPlugin(t, f, map[string]*string{release.PluginMCP: strp("{not json")})
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if got := sortedSpecs(f); got != "cand-bin:serve|prev-bin:serve" {
		t.Fatalf("measured %q", got)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "could not be read: the candidate is not measured under its serve args and env, and the baseline uses the candidate's plugin") {
		t.Fatalf("no skip note:\n%s", out)
	}
}

func TestCheck_aCandidateSlowerThanThePreviousReleaseIsRefused(t *testing.T) {
	f := newFixture(t)
	withPrevious(t, f, oldMash)
	f.setLatency(func(which string) latencies {
		if which == "prev-bin" {
			return latencies{Fresh: 100 * time.Millisecond, FreshOK: true}
		}
		return latencies{Fresh: 45 * time.Second, FreshOK: true}
	})
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "took the candidate 45.00s") {
		t.Fatalf("want a refusal (exit 3) naming the latency, got %v", err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "FAIL  fresh-session mail arrives no slower than the previous release") {
		t.Fatalf("no failing step:\n%s", out)
	}
}

func TestCheck_aCandidatesUrgentOrFyiFindingIsRefusedWithoutAPreviousRelease(t *testing.T) {
	f := newFixture(t)
	f.setLatency(func(string) latencies {
		return latencies{Fresh: 100 * time.Millisecond, FreshOK: true, Findings: []string{"fyi mail to a hooked session woke it"}}
	})
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "fyi mail to a hooked session woke it") {
		t.Fatalf("want a refusal (exit 3), got %v", err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "no previous release to compare with") {
		t.Fatalf("no note:\n%s", out)
	}
}

func TestCheck_aFailedMeasurementIsFourNotARefusal(t *testing.T) {
	f := newFixture(t)
	f.o.measure = func(context.Context, string, serveSpec, bool, bool) (latencies, error) {
		return latencies{}, op(errors.New("no nats"))
	}
	if err := f.check(); err == nil || exitCode(err) != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
}

func strp(s string) *string { return &s }

func TestContractVerdict(t *testing.T) {
	call := []string{"abc1234 render-contract: kind line"}
	cases := []struct {
		name        string
		x, prev     *string
		hasPrev     bool
		callouts    []string
		wantNote    string
		wantRefusal bool
	}{
		{"absent at the candidate", nil, strp("a"), true, nil, "", true},
		{"no previous release", strp("a"), nil, false, nil, "first", false},
		{"absent at the previous release", strp("a"), nil, true, nil, "first", false},
		{"unchanged", strp("a"), strp("a"), true, nil, "", false},
		{"changed and called out", strp("b"), strp("a"), true, call, "abc1234", false},
		{"changed without a callout", strp("b"), strp("a"), true, nil, "", true},
	}
	for _, c := range cases {
		note, err := contractVerdict(c.x, c.prev, c.hasPrev, "v0.0.9", c.callouts)
		if (err != nil) != c.wantRefusal {
			t.Errorf("%s: err = %v, want refusal %v", c.name, err, c.wantRefusal)
		}
		if c.wantNote == "" && note != "" || !strings.Contains(note, c.wantNote) {
			t.Errorf("%s: note %q, want it to contain %q", c.name, note, c.wantNote)
		}
		if c.name == "changed without a callout" && !strings.Contains(err.Error(), "render-contract:") {
			t.Errorf("%s: the refusal does not say what to do: %v", c.name, err)
		}
	}
}

func TestRenderContractCallouts(t *testing.T) {
	got := renderContractCallouts([]string{"a1 render-contract: x", "b2 fix render-contract: y", "c3 render-contractx", "d4 render-contract: z"})
	if len(got) != 2 || got[0] != "a1 render-contract: x" || got[1] != "d4 render-contract: z" {
		t.Fatalf("got %v", got)
	}
}

// commitContract commits a new contract body with the given subject and makes the commit the candidate.
func commitContract(t *testing.T, f *fixture, body, subject string) {
	t.Helper()
	write(t, f.repo, renderContractPath, body)
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "--allow-empty", "-m", subject)
	gitT(t, f.repo, "push", "-q", "origin", "main")
	f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
	f.setCIPages(f.o.X, 100, []map[string]any{ciRun(1, "test (linux)", f.o.X), ciRun(2, "reproducible", f.o.X)})
}

func TestCheck_renderContractStep(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, f *fixture)
		wantErr  string // empty: passes
		wantNote string
	}{
		{"unchanged", func(t *testing.T, f *fixture) {
			withPrevious(t, f, oldMash)
			commitContract(t, f, "contract v1\n", "unrelated")
		}, "", ""},
		{"changed with a callout", func(t *testing.T, f *fixture) {
			withPrevious(t, f, oldMash)
			commitContract(t, f, "contract v2\n", "render-contract: add a kind line")
		}, "", "render-contract: add a kind line"},
		{"changed with a callout on an earlier commit", func(t *testing.T, f *fixture) {
			withPrevious(t, f, oldMash)
			commitContract(t, f, "contract v2\n", "render-contract: add a kind line")
			commitContract(t, f, "contract v2\n", "later work")
		}, "", "render-contract: add a kind line"},
		{"changed without a callout", func(t *testing.T, f *fixture) {
			withPrevious(t, f, oldMash)
			commitContract(t, f, "contract v2\n", "tidy the renderer")
		}, "render-contract:", ""},
		{"no previous release", func(t *testing.T, f *fixture) {}, "", "no previous release"},
		{"absent at the previous release", func(t *testing.T, f *fixture) {
			gitT(t, f.repo, "rm", "-q", renderContractPath)
			gitT(t, f.repo, "commit", "-q", "-m", "drop the contract")
			gitT(t, f.repo, "tag", "v0.0.9")
			gitT(t, f.repo, "push", "-q", "origin", "main", "v0.0.9")
			f.o.prevBinary = func(ctx context.Context, tag, dir string) (string, error) {
				p := filepath.Join(dir, "prev")
				return p, os.WriteFile(p, []byte(oldMash), 0o755)
			}
			commitContract(t, f, "contract v1\n", "add the contract")
		}, "", "first"},
		{"absent at the candidate", func(t *testing.T, f *fixture) {
			gitT(t, f.repo, "rm", "-q", renderContractPath)
			gitT(t, f.repo, "commit", "-q", "-m", "drop the contract")
			gitT(t, f.repo, "push", "-q", "origin", "main")
			f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
		}, "missing at the candidate", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(t, f)
			err := renderContract(f.o, gitT(t, f.repo, "rev-parse", f.o.X))
			if c.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if c.wantErr != "" {
				var oe opError
				if err == nil || errors.As(err, &oe) || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want a refusal containing %q, got %v", c.wantErr, err)
				}
			}
			if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, c.wantNote) {
				t.Fatalf("want a note containing %q:\n%s", c.wantNote, out)
			}
		})
	}
}

func TestCheck_renderContractTagListingFailureIsFour(t *testing.T) {
	f := newFixture(t)
	f.o.Remote = filepath.Join(t.TempDir(), "no-such-remote.git")
	var oe opError
	if err := renderContract(f.o, f.x); !errors.As(err, &oe) {
		t.Fatalf("want an op error, got %v", err)
	}
}

// withPreviousPlugin tags v0.0.9 on a commit whose plugin files are as given (a nil body removes the file) and then
// makes a new candidate with the plugin files the repository had when called, so the previous release's plugin differs from the
// candidate's. The previous release's binary is oldMash.
func withPreviousPlugin(t *testing.T, f *fixture, files map[string]*string) {
	t.Helper()
	cand := gitT(t, f.repo, "rev-parse", "HEAD")
	for name, body := range files {
		if body == nil {
			gitT(t, f.repo, "rm", "-q", name)
		} else {
			write(t, f.repo, name, *body)
		}
	}
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "--allow-empty", "-m", "previous release's plugin")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	gitT(t, f.repo, "tag", "v0.0.9")
	gitT(t, f.repo, "push", "-q", "origin", "v0.0.9")
	gitT(t, f.repo, "checkout", "-q", cand, "--", ".")
	commitPlugin(t, f, "x.txt", "x")
	withPreviousBody(f, oldMash)
}

func mcpWith(args, env string) string {
	return `{"mcpServers": {"mash": {"command": "mash", "args": ` + args + `, "env": ` + env + `}}}` + "\n"
}

// The common real case: an older installed plugin runs against a newer binary.
func TestCheck_thePreviousReleasesPluginRunsAgainstTheCandidate(t *testing.T) {
	// The previous release's .mcp.json passes an arg the candidate rejects.
	f := newFixture(t)
	f.o.candBinary = candidateBinary("#!/bin/sh\ncase \"$1\" in serve) case \"$2\" in --old) echo 'mash: unknown flag --old' >&2; exit 2 ;; esac; " + serveReply + " ;; esac\nexit 0\n")
	withPreviousPlugin(t, f, map[string]*string{release.PluginMCP: strp(mcpWith(`["serve", "--old"]`, `{}`))})
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "the previous release's plugin (v0.0.9)") || !strings.Contains(err.Error(), "MCP") {
		t.Fatalf("want a refusal naming the previous release's plugin, got %v (exit %d)", err, exitCode(err))
	}
	// A previous release's hook command the candidate breaks on.
	f = newFixture(t)
	f.o.candBinary = candidateBinary("#!/bin/sh\ncase \"$1\" in serve) " + serveReply + " ;; hook) if [ -n \"$MASH_X\" ]; then exit 0; fi; echo CTX ;; esac\nexit 0\n")
	// the candidate's own plugin passes: its hook command sets MASH_X; the previous release's does not
	candHooks := strings.ReplaceAll(hardenedHooks, "mash hook", "MASH_X=1 mash hook")
	commitPlugin(t, f, release.PluginHooks, candHooks)
	withPreviousPlugin(t, f, map[string]*string{release.PluginHooks: strp(hardenedHooks)})
	err = f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "the previous release's plugin (v0.0.9)") || !strings.Contains(err.Error(), "stdout") {
		t.Fatalf("want a refusal of the old hook command naming the previous release's plugin, got %v (exit %d)", err, exitCode(err))
	}
}

// The plugin launches serve with the .mcp.json's env block, so the handshake must too, for both plugins.
func TestCheck_theEnvBlockReachesServe(t *testing.T) {
	envMash := "#!/bin/sh\ncase \"$1\" in serve) [ \"$MASH_SPOOL_FROM_START\" = 1 ] || exit 1; " + serveReply + " ;; esac\nexit 0\n"
	withEnv := mcpWith(`["serve"]`, `{"MASH_SPOOL_FROM_START": "1"}`)
	without := mcpWith(`["serve"]`, `{}`)
	for _, c := range []struct {
		name       string
		cand, prev string
		want       string // empty: passes
	}{
		{"both plugins set it", withEnv, withEnv, ""},
		{"the candidate's plugin does not", without, withEnv, "the candidate"},
		{"the previous release's plugin does not", withEnv, without, "the previous release's plugin (v0.0.9)"},
	} {
		f := newFixture(t)
		f.o.candBinary = candidateBinary(envMash)
		commitPlugin(t, f, release.PluginMCP, c.cand)
		withPreviousPlugin(t, f, map[string]*string{release.PluginMCP: strp(c.prev)})
		err := f.check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "MCP") || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want an MCP refusal naming %q, got %v", c.name, c.want, err)
		}
	}
}

// A release before hooks has no hooks.json: only its .mcp.json is run. A previous plugin that cannot be read is a
// note: a shipped plugin cannot be fixed.
func TestCheck_aPreviousPluginThatCannotBeRunIsANote(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]*string
		want  string
	}{
		{"no hooks.json", map[string]*string{release.PluginHooks: nil}, release.PluginHooks + " is absent"},
		{"an unparseable .mcp.json", map[string]*string{release.PluginMCP: strp("{not json")}, release.PluginMCP + " is unreadable"},
		{"no .mcp.json", map[string]*string{release.PluginMCP: nil}, release.PluginMCP + " is absent"},
	} {
		f := newFixture(t)
		withPreviousPlugin(t, f, c.files)
		if err := f.check(); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		out := f.o.Out.(*bytes.Buffer).String()
		if !strings.Contains(out, "the previous release's plugin (v0.0.9)") || !strings.Contains(out, c.want) {
			t.Errorf("%s: no note saying %q:\n%s", c.name, c.want, out)
		}
	}
	// With hooks absent, the .mcp.json is still run against the candidate (and refused if the candidate cannot serve it).
	f := newFixture(t)
	f.o.candBinary = candidateBinary("#!/bin/sh\ncase \"$1\" in serve) case \"$2\" in --old) exit 2 ;; esac; " + serveReply + " ;; esac\nexit 0\n")
	withPreviousPlugin(t, f, map[string]*string{release.PluginHooks: nil, release.PluginMCP: strp(mcpWith(`["serve", "--old"]`, `{}`))})
	if err := f.check(); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "the previous release's plugin (v0.0.9)") {
		t.Errorf("a previous plugin without hooks.json whose serve args the candidate rejects: %v", err)
	}
}

// The previous tag is fetched once for the plugin and render-contract steps.
func TestCheck_thePreviousTagIsFetchedOnce(t *testing.T) {
	f := newFixture(t)
	withPreviousPlugin(t, f, map[string]*string{})
	f.o.prevFetched = new(string)
	if _, err := fetchPrevTag(f.o, "v0.0.9"); err != nil {
		t.Fatal(err)
	}
	f.o.Remote = filepath.Join(t.TempDir(), "gone.git") // a second fetch would now fail
	if _, err := fetchPrevTag(f.o, "v0.0.9"); err != nil {
		t.Fatalf("fetched again: %v", err)
	}
}

// setReleases fakes `gh release list`: the releases that exist, tag to isDraft. A tag not listed has no release.
func (f *fixture) setReleases(releases map[string]bool) {
	var list []map[string]any
	for tag, draft := range releases {
		list = append(list, map[string]any{"tagName": tag, "isDraft": draft})
	}
	b, _ := json.Marshal(list)
	write(f.t, filepath.Dir(f.o.GH), "releases.json", string(b))
}

// withEarlierTags pushes lightweight tags to the release repository, as earlier releases that were tagged.
func withEarlierTags(t *testing.T, f *fixture, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		gitT(t, f.repo, "tag", tag, f.x)
		gitT(t, f.repo, "push", "-q", "origin", tag)
	}
}

// commitSkipped commits docs/skipped-releases.txt and makes the new commit the candidate.
func commitSkipped(t *testing.T, f *fixture, body string) {
	t.Helper()
	commitPlugin(t, f, skippedReleases, body)
}

func TestCheck_earlierReleasesMustBePublishedOrRecordedAsSkipped(t *testing.T) {
	cases := map[string]struct {
		tags     []string
		releases map[string]bool
		skipped  string
		bad      []string // the tags the refusal names; none: passes
	}{
		"all earlier published":                                 {tags: []string{"v0.0.7", "v0.0.8"}, releases: map[string]bool{"v0.0.7": false, "v0.0.8": false}},
		"an earlier draft":                                      {tags: []string{"v0.0.8"}, releases: map[string]bool{"v0.0.8": true}, bad: []string{"v0.0.8"}},
		"an earlier release missing":                            {tags: []string{"v0.0.7", "v0.0.8"}, releases: map[string]bool{"v0.0.7": false}, bad: []string{"v0.0.8"}},
		"every offender is named":                               {tags: []string{"v0.0.7", "v0.0.8"}, releases: map[string]bool{"v0.0.7": true}, bad: []string{"v0.0.7", "v0.0.8"}},
		"a draft listed as skipped":                             {tags: []string{"v0.0.8"}, releases: map[string]bool{"v0.0.8": true}, skipped: "# why\nv0.0.8 tagged, never published\n"},
		"a missing one listed as skipped, another not":          {tags: []string{"v0.0.7", "v0.0.8"}, releases: map[string]bool{}, skipped: "v0.0.8 never published\n", bad: []string{"v0.0.7"}},
		"a skipped line without a reason does not count":        {tags: []string{"v0.0.8"}, releases: map[string]bool{}, skipped: "v0.0.8\n", bad: []string{"v0.0.8"}},
		"a skipped line with a reason counts":                   {tags: []string{"v0.0.8"}, releases: map[string]bool{}, skipped: "v0.0.8 never published\n"},
		"a prerelease tag is ignored":                           {tags: []string{"v0.0.8-rc1", "not-semver"}, releases: map[string]bool{}},
		"a later tag is ignored":                                {tags: []string{"v0.1.1", "v1.0.0"}, releases: map[string]bool{}},
		"numeric, not lexical, order (v0.0.10 is below v0.1.0)": {tags: []string{"v0.0.10"}, releases: map[string]bool{}, bad: []string{"v0.0.10"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			withEarlierTags(t, f, c.tags...)
			f.setReleases(c.releases)
			if c.skipped != "" {
				commitSkipped(t, f, c.skipped)
			}
			err := earlierReleases(f.o, f.o.X)
			if len(c.bad) == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("want a refusal")
			}
			var oe opError
			if errors.As(err, &oe) {
				t.Fatalf("a refusal, not an op error: %v", err)
			}
			for _, tag := range c.bad {
				if !strings.Contains(err.Error(), tag) {
					t.Errorf("the refusal does not name %s: %v", tag, err)
				}
			}
			if c.skipped == "v0.0.8\n" && !strings.Contains(err.Error(), "missing its reason") {
				t.Errorf("the refusal does not say the line is missing its reason: %v", err)
			}
			if !strings.Contains(err.Error(), "gh release edit") || !strings.Contains(err.Error(), skippedReleases) {
				t.Errorf("the refusal does not give the fix: %v", err)
			}
		})
	}
}

func TestCheck_earlierReleasesReadAtTheCandidateNotTheCheckout(t *testing.T) {
	f := newFixture(t)
	withEarlierTags(t, f, "v0.0.8")
	f.setReleases(map[string]bool{})
	write(t, f.repo, skippedReleases, "v0.0.8 uncommitted\n") // in the work tree only: X does not have it
	if err := earlierReleases(f.o, f.o.X); err == nil {
		t.Fatal("a skipped record that X does not hold was honoured")
	}
}

func TestCheck_aReleaseListingFailureCannotDecide(t *testing.T) {
	f := newFixture(t)
	withEarlierTags(t, f, "v0.0.8")
	bad := filepath.Join(t.TempDir(), "gh")
	write(t, filepath.Dir(bad), "gh", "#!/bin/sh\ncase \"$1\" in release) echo boom >&2; exit 1 ;; esac\nexit 1\n")
	if err := os.Chmod(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	f.o.GH = bad
	var oe opError
	if err := earlierReleases(f.o, f.o.X); !errors.As(err, &oe) {
		t.Fatalf("want an op error (exit 4), got %v", err)
	}
}

func TestCheck_earlierReleasesIsAReportedStep(t *testing.T) {
	f := newFixture(t)
	withEarlierTags(t, f, "v0.0.8")
	f.setReleases(map[string]bool{"v0.0.8": true})
	err := f.check()
	if got := exitCode(err); got != 3 {
		t.Fatalf("exit %d, want 3: %v", got, err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "FAIL  earlier releases are published or recorded as skipped") {
		t.Fatalf("the step is not reported:\n%s", out)
	}
}

func TestParseGitHubRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/bogworks/mash.git":   "bogworks/mash",
		"https://github.com/bogworks/mash":       "bogworks/mash",
		"git@github.com:bogworks/mash.git":       "bogworks/mash",
		"git@github.com:bogworks/mash":           "bogworks/mash",
		"ssh://git@github.com/bogworks/mash.git": "bogworks/mash",
		"ssh://git@github.com/bogworks/mash":     "bogworks/mash",
	} {
		if got, ok := parseGitHubRepo(in); !ok || got != want {
			t.Errorf("%q: got %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "/tmp/origin.git", "https://gitlab.com/a/b.git", "https://github.com/a", "https://github.com/a/b/c", "git@github.com:a/"} {
		if got, ok := parseGitHubRepo(in); ok {
			t.Errorf("%q: unexpectedly parsed %q", in, got)
		}
	}
}

func TestGHRepoDefaults(t *testing.T) {
	mk := func(origin string) string {
		d := t.TempDir()
		gitT(t, d, "init", "-q")
		if origin != "" {
			gitT(t, d, "remote", "add", "origin", origin)
		}
		return d
	}
	resolve := func(o Options) Options {
		o.X, o.Version = "x", "0.1.0"
		if err := o.defaults(); err != nil {
			t.Fatal(err)
		}
		return o
	}
	t.Setenv("RELEASE_CHECK_REPO", "")
	if o := resolve(Options{Repo: mk("")}); o.GHRepo != "bogworks/mash" {
		t.Errorf("fallback: %q", o.GHRepo)
	}
	if o := resolve(Options{Repo: mk("/some/local/path")}); o.GHRepo != "bogworks/mash" {
		t.Errorf("non-github origin: %q", o.GHRepo)
	}
	repo := mk("git@github.com:bogworks/mash.git")
	o := resolve(Options{Repo: repo})
	if o.GHRepo != "bogworks/mash" || o.Remote != "https://github.com/bogworks/mash.git" {
		t.Errorf("origin: %q %q", o.GHRepo, o.Remote)
	}
	t.Setenv("RELEASE_CHECK_REPO", "env/repo")
	if o := resolve(Options{Repo: repo}); o.GHRepo != "env/repo" {
		t.Errorf("env: %q", o.GHRepo)
	}
	if o := resolve(Options{Repo: repo, GHRepo: "opt/repo"}); o.GHRepo != "opt/repo" {
		t.Errorf("option: %q", o.GHRepo)
	}
}

func TestAssets_aScriptMissingAtXIsAClearError(t *testing.T) {
	f := newFixture(t)
	setAssets(t, f, nil)
	gitT(t, f.repo, "rm", "-q", "scripts/"+release.ScriptName)
	gitT(t, f.repo, "commit", "-q", "-m", "drop the script")
	f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	gitT(t, f.repo, "tag", "-s", "-f", "v0.1.0", "-m", "release", f.o.X)
	gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
	err := checkAssets(context.Background(), f.o)
	if err == nil || !strings.Contains(err.Error(), "script is missing at") {
		t.Fatalf("%v", err)
	}
}
