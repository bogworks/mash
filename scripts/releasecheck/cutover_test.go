package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// cutoverRepo is a repository with one commit; the commit holds docs/release-cutover with body when body is not nil.
func cutoverRepo(t *testing.T, body *string) (repo, x string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	gitT(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	write(t, repo, "README", "x\n")
	if body != nil {
		write(t, repo, cutoverFile, *body)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "c")
	return repo, gitT(t, repo, "rev-parse", "HEAD")
}

func TestCutoverAt(t *testing.T) {
	cases := []struct {
		name    string
		body    *string
		want    string
		ok      bool
		wantErr string
	}{
		{"absent", nil, "", false, ""},
		{"one version", strp("v0.4.0\n"), "v0.4.0", true, ""},
		{"comments and blank lines", strp("# first keyless release\n\n  v0.4.0  \n# end\n"), "v0.4.0", true, ""},
		{"two versions", strp("v0.4.0\nv0.5.0\n"), "", false, "exactly one"},
		{"no version", strp("# nothing\n"), "", false, "exactly one"},
		{"bad version", strp("0.4.0\n"), "", false, "not a version"},
		{"pre-release", strp("v0.4.0-rc1\n"), "", false, "not a version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, x := cutoverRepo(t, c.body)
			got, ok, err := cutoverAt(Options{Repo: repo}, x)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want || ok != c.ok {
				t.Fatalf("= %q, %v, %v; want %q, %v", got, ok, err, c.want, c.ok)
			}
		})
	}
	t.Run("a file only in the working tree or the index, not at X, is absent", func(t *testing.T) {
		repo, x := cutoverRepo(t, nil)
		write(t, repo, cutoverFile, "v0.1.0\n")
		if _, ok, err := cutoverAt(Options{Repo: repo}, x); err != nil || ok {
			t.Fatalf("ok = %v, err = %v: the working tree's file was read", ok, err)
		}
		gitT(t, repo, "add", "-A")
		if _, ok, err := cutoverAt(Options{Repo: repo}, x); err != nil || ok {
			t.Fatalf("ok = %v, err = %v: the index's file was read", ok, err)
		}
	})
}

func TestKeyless(t *testing.T) {
	for _, c := range []struct {
		cutover *string
		tag     string
		want    bool
	}{
		{strp("v0.4.0\n"), "v0.3.3", false},
		{strp("v0.4.0\n"), "v0.3.10", false},
		{strp("v0.4.0\n"), "v0.4.0", true},
		{strp("v0.4.0\n"), "v0.4.1", true},
		{strp("v0.4.0\n"), "v0.10.0", true},
		{nil, "v0.4.0", false},
		{nil, "v9.9.9", false},
	} {
		repo, x := cutoverRepo(t, c.cutover)
		got, err := keyless(Options{Repo: repo}, x, c.tag)
		if err != nil || got != c.want {
			t.Errorf("cutover %v tag %s: keyless = %v, %v; want %v", c.cutover != nil, c.tag, got, err, c.want)
		}
	}
	repo, x := cutoverRepo(t, strp("garbage\n"))
	if _, err := keyless(Options{Repo: repo}, x, "v0.4.0"); err == nil {
		t.Error("a malformed cutover record did not fail closed")
	}
	repo, x = cutoverRepo(t, strp("v0.4.0\n"))
	if _, err := keyless(Options{Repo: repo}, x, "main"); err == nil {
		t.Error("a tag that is not a version was judged")
	}
}

func TestAttestFlags_pinFourFacts(t *testing.T) {
	got := strings.Join(attestFlags(Options{GHRepo: "o/r"}, "v0.4.0", "abc123"), " ")
	want := "-R o/r --signer-workflow o/r/.github/workflows/release.yml --source-ref refs/tags/v0.4.0 --source-digest abc123"
	if got != want {
		t.Fatalf("attestFlags = %q, want %q", got, want)
	}
}

// makeKeyless moves the fixture to a candidate that records cutover (at or below the fixture's v0.1.0, so it is keyless),
// with v0.1.0 an annotated tag that carries no signature. setAssets then makes the keyless layout of SHA256SUMS.
func (f *fixture) makeKeyless(t *testing.T, cutover string) {
	t.Helper()
	write(t, f.repo, cutoverFile, "# the first keyless release\n"+cutover+"\n")
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "-m", "cutover")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	f.x = gitT(t, f.repo, "rev-parse", "HEAD")
	f.o.X = f.x
	write(t, f.ghDir(), "x", f.x)
	f.setCI(release.RequiredChecks...)
	f.keyless = true
	gitT(t, f.repo, "tag", "-f", "-a", "v0.1.0", "-m", "release", f.x)
	gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
}

func TestVerifyTag_keyless(t *testing.T) {
	tagObj := func(f *fixture) string {
		obj, _, err := tagRefs(f.repo, f.origin, "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		return obj
	}
	t.Run("an unsigned annotated tag at X passes", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		if err := verifyTag(f.o, "v0.1.0", tagObj(f), f.x, true); err != nil {
			t.Fatalf("refused: %v", err)
		}
	})
	t.Run("the same tag is refused before the cutover", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		if err := verifyTag(f.o, "v0.1.0", tagObj(f), f.x, false); err == nil || !strings.Contains(err.Error(), "no valid signature") {
			t.Fatalf("an unsigned tag passed before the cutover: %v", err)
		}
	})
	t.Run("a lightweight tag is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		gitT(t, f.repo, "tag", "-f", "v0.1.0", f.x)
		gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
		if err := verifyTag(f.o, "v0.1.0", tagObj(f), f.x, true); err == nil || !strings.Contains(err.Error(), "not an annotated tag") {
			t.Fatalf("a lightweight tag passed: %v", err)
		}
	})
	t.Run("an annotated tag at another commit is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		write(t, f.repo, "other.txt", "x")
		gitT(t, f.repo, "add", "-A")
		gitT(t, f.repo, "commit", "-q", "-m", "other")
		other := gitT(t, f.repo, "rev-parse", "HEAD")
		gitT(t, f.repo, "tag", "-f", "-a", "v0.1.0", "-m", "release", other)
		gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
		if err := verifyTag(f.o, "v0.1.0", tagObj(f), f.x, true); err == nil || !strings.Contains(err.Error(), "not the candidate") {
			t.Fatalf("a tag at another commit passed: %v", err)
		}
	})
	t.Run("a tag object swapped after the listing is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		listed := tagObj(f)
		gitT(t, f.repo, "tag", "-f", "-a", "v0.1.0", "-m", "again", f.x)
		gitT(t, f.repo, "push", "-q", "-f", "origin", "v0.1.0")
		if err := verifyTag(f.o, "v0.1.0", listed, f.x, true); err == nil || !strings.Contains(err.Error(), "changed between") {
			t.Fatalf("a swapped tag passed: %v", err)
		}
	})
	t.Run("the whole gate: keyless names its step", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		setAssets(t, f, nil)
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatal(err)
		}
		out := f.o.Out.(*bytes.Buffer).String()
		if !strings.Contains(out, "tag v0.1.0 is annotated and at X (keyless: no signature, trust is the tag rulesets, cutover v0.1.0)") || strings.Contains(out, "signed by a trusted key") {
			t.Errorf("report:\n%s", out)
		}
	})
	t.Run("pre-cutover: an unsigned annotated tag is still refused by the gate", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.2.0") // v0.1.0 is below the cutover
		f.keyless = false
		setAssets(t, f, nil)
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "no valid signature") {
			t.Fatalf("err = %v\n%s", err, f.o.Out.(*bytes.Buffer).String())
		}
	})
	t.Run("a malformed cutover record fails the gate closed", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0\nv0.2.0")
		if err := checkAssets(context.Background(), f.o); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
}

func keylessFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.makeKeyless(t, "v0.1.0")
	setAssets(t, f, nil)
	// Nothing in a keyless release needs a signing key.
	t.Setenv("RELEASE_SIGNING_KEY", filepath.Join(t.TempDir(), "no-such-key"))
	return f
}

func (f *fixture) sumsAttestationCalls() int {
	n := 0
	for _, ln := range strings.Split(f.ghLog(), "\n") {
		if strings.HasPrefix(ln, "attestation verify") && strings.Contains(ln, "/SHA256SUMS ") {
			n++
		}
	}
	return n
}

func TestAssets_keylessSums(t *testing.T) {
	t.Run("no signature, an attested SHA256SUMS: passes with no key, signers or upload", func(t *testing.T) {
		f := keylessFixture(t)
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatalf("a correct keyless release was refused: %v", err)
		}
		if f.sumsAttestationCalls() != 1 {
			t.Errorf("SHA256SUMS attestation not verified once:\n%s", f.ghLog())
		}
		log := f.ghLog()
		if strings.Contains(log, "release upload") || strings.Contains(log, release.SumsSigName) {
			t.Errorf("a signature was handled:\n%s", log)
		}
		for _, ln := range strings.Split(log, "\n") {
			if strings.HasPrefix(ln, "attestation verify") && !strings.Contains(ln, "--source-digest "+f.x) {
				t.Errorf("gh call without the candidate's source digest: %s", ln)
			}
		}
	})
	t.Run("the SHA256SUMS attestation failing is refused naming SHA256SUMS", func(t *testing.T) {
		f := keylessFixture(t)
		f.markerFor(t, "noattest", "SHA256SUMS")
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "attestation for SHA256SUMS") {
			t.Fatalf("err = %v\n%s", err, f.o.Out.(*bytes.Buffer).String())
		}
	})
	t.Run("a sums line that differs from a fresh build is refused", func(t *testing.T) {
		f := keylessFixture(t)
		sums := string(sumsOfFixture(t, f))
		first := strings.Split(sums, "\n")[0]
		f.setSums(t, strings.Replace(sums, first, strings.Repeat("0", 64)+first[64:], 1))
		if err := checkAssets(context.Background(), f.o); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a binary whose digest differs from our build is refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		setAssets(t, f, func(m map[string]string) { m["mash-linux-amd64"] = "sha256:" + strings.Repeat("7", 64) })
		if err := checkAssets(context.Background(), f.o); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an attached SHA256SUMS.sig is refused", func(t *testing.T) {
		f := keylessFixture(t)
		f.attachSig(t, []byte("sig"), true)
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "keyless release carries no SHA256SUMS.sig") {
			t.Fatalf("err = %v\n%s", err, f.o.Out.(*bytes.Buffer).String())
		}
	})
	t.Run("sums that list the provenance, the old layout, are refused", func(t *testing.T) {
		f := newFixture(t)
		f.makeKeyless(t, "v0.1.0")
		f.keyless = false // setAssets makes the pre-cutover sums
		setAssets(t, f, nil)
		if err := checkAssets(context.Background(), f.o); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPublish_keylessRealWorld(t *testing.T) {
	gated := func(t *testing.T) *fixture {
		f := keylessFixture(t)
		f.gate = &gateRecord{}
		if err := checkAssetsRecord(context.Background(), f.o, f.gate); err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Run("publishes with no key and no signature", func(t *testing.T) {
		f := gated(t)
		if err := f.publish(); err != nil {
			t.Fatalf("refused: %v", err)
		}
		if !strings.Contains(f.ghLog(), "release edit v0.1.0") {
			t.Errorf("not published:\n%s", f.ghLog())
		}
		if n := f.sumsAttestationCalls(); n != 2 { // the gate's and publish's
			t.Errorf("SHA256SUMS attestation verified %d times:\n%s", n, f.ghLog())
		}
	})
	t.Run("the SHA256SUMS attestation failing after the gate stops the publish", func(t *testing.T) {
		f := gated(t)
		f.markerFor(t, "noattest", "SHA256SUMS")
		err := f.publish()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "SHA256SUMS") || strings.Contains(f.ghLog(), "release edit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a SHA256SUMS.sig attached after the gate stops the publish", func(t *testing.T) {
		f := gated(t)
		f.attachSig(t, []byte("sig"), true)
		err := f.publish()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "no SHA256SUMS.sig") || strings.Contains(f.ghLog(), "release edit") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("SHA256SUMS changed after the gate stops the publish", func(t *testing.T) {
		f := gated(t)
		f.setSums(t, string(sumsOfFixture(t, f))+"\n\n")
		if err := f.publish(); err == nil || !strings.Contains(err.Error(), "not the one the --assets gate checked") {
			t.Fatalf("err = %v", err)
		}
	})
}

// The fake gh holds every verification to the four facts: it refuses a call with no --source-digest or a wrong one.
func TestFakeGH_requiresTheSourceDigest(t *testing.T) {
	f := newFixture(t)
	call := func(extra ...string) error {
		args := append([]string{"attestation", "verify", "x", "-R", "a/b", "--source-ref", "refs/tags/v0.1.0"}, extra...)
		return exec.Command(f.o.GH, args...).Run()
	}
	if call() == nil {
		t.Error("a call without --source-digest was accepted")
	}
	if call("--source-digest", "deadbeef") == nil {
		t.Error("a wrong --source-digest was accepted")
	}
	if err := call("--source-digest", f.x); err != nil {
		t.Errorf("the right --source-digest was refused: %v", err)
	}
}

func TestPublish_keylessStateMachine(t *testing.T) {
	made := func(f *fakeWorld) bool {
		for _, c := range f.calls {
			if c == "createtag" {
				return true
			}
		}
		return false
	}
	t.Run("keyless and no tag: refused, no tag made, no pre-tag gate", func(t *testing.T) {
		f := newFake()
		f.cutover, f.keyless = "v0.4.0", true
		err := run(f)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "refused: v1.2.3 is at or above the cutover v0.4.0: its tag is pushed by the minting service, not made here; nothing was changed") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if len(f.effects()) != 0 {
			t.Errorf("effects: %v", f.effects())
		}
	})
	t.Run("keyless and the tag exists at X: gate, assets and publish, never a tag", func(t *testing.T) {
		f := newFake()
		f.cutover, f.keyless = "v0.4.0", true
		f.tagAt, f.release = "abc", relDraft
		if err := run(f); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(f.effects(), ","); got != "pretag,assets,publish,assets" {
			t.Errorf("effects = %s", got)
		}
		if made(f) {
			t.Fatalf("a tag was made: %v", f.calls)
		}
	})
	t.Run("keyless and the draft is awaited: same", func(t *testing.T) {
		f := newFake()
		f.cutover, f.keyless = "v0.4.0", true
		f.tagAt = "abc"
		f.draftAfter = 2
		if err := run(f); err != nil {
			t.Fatal(err)
		}
		if made(f) {
			t.Fatalf("a tag was made: %v", f.calls)
		}
	})
	t.Run("pre-cutover and no tag: tagged as before", func(t *testing.T) {
		f := newFake()
		f.draftAfter = 2
		if err := run(f); err != nil {
			t.Fatal(err)
		}
		if got := f.effects(); got[0] != "pretag" || got[1] != "createtag" {
			t.Errorf("effects = %v", got)
		}
	})
}
