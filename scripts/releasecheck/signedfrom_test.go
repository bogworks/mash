package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignedFromAt(t *testing.T) {
	cases := []struct {
		name    string
		body    *string
		want    string
		ok      bool
		wantErr string
	}{
		{"absent file", nil, "", false, ""},
		{"no signed-from line", strp("v0.4.0\n"), "", false, ""},
		{"present", strp("# c\nv0.4.0\n\nsigned-from v0.6.1\n"), "v0.6.1", true, ""},
		{"equal to the cutover", strp("v0.4.0\nsigned-from v0.4.0\n"), "v0.4.0", true, ""},
		{"duplicate", strp("v0.4.0\nsigned-from v0.6.1\nsigned-from v0.6.1\n"), "", false, "more than one"},
		{"malformed", strp("v0.4.0\nsigned-from 0.6.1\n"), "", false, "not `signed-from"},
		{"no version", strp("v0.4.0\nsigned-from\n"), "", false, "not `signed-from"},
		{"trailing word", strp("v0.4.0\nsigned-from v0.6.1 now\n"), "", false, "not `signed-from"},
		{"below the cutover", strp("v0.4.0\nsigned-from v0.3.0\n"), "", false, "below the cutover"},
		{"unknown line", strp("v0.4.0\nsigned-from v0.6.1\nsomething\n"), "", false, "exactly one"},
		{"two cutovers", strp("v0.4.0\nv0.5.0\nsigned-from v0.6.1\n"), "", false, "exactly one"},
		{"no cutover", strp("signed-from v0.6.1\n"), "", false, "exactly one"},
		{"reversed order is accepted", strp("signed-from v0.6.1\nv0.4.0\n"), "v0.6.1", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, x := cutoverRepo(t, c.body)
			got, ok, err := signedFromAt(Options{Repo: repo}, x)
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
	t.Run("cutoverAt takes the cutover from a reversed record", func(t *testing.T) {
		repo, x := cutoverRepo(t, strp("signed-from v0.6.1\nv0.4.0\n"))
		if got, ok, err := cutoverAt(Options{Repo: repo}, x); err != nil || !ok || got != "v0.4.0" {
			t.Fatalf("= %q, %v, %v", got, ok, err)
		}
	})
	t.Run("cutoverAt ignores the signed-from line", func(t *testing.T) {
		repo, x := cutoverRepo(t, strp("v0.4.0\nsigned-from v0.6.1\n"))
		if got, ok, err := cutoverAt(Options{Repo: repo}, x); err != nil || !ok || got != "v0.4.0" {
			t.Fatalf("= %q, %v, %v", got, ok, err)
		}
	})
}

// signedFromFixture is a keyless fixture whose cutover file also records signed-from, with two keys in the
// allowed_signers file the gate reads: newKey is principal mash-release, oldKey is old@example.
type signedFromFixture struct {
	*fixture
	newKey, oldKey string // public key files
}

// newSignedFrom records cutover v0.1.0 and signed-from signedFrom; withRelease false leaves the mash-release line out.
func newSignedFrom(t *testing.T, signedFrom string, withRelease bool) *signedFromFixture {
	t.Helper()
	f := newFixture(t)
	rec := "v0.1.0"
	if signedFrom != "" { // "": a record with the cutover only, as every release before ADR 0199 has
		rec += "\nsigned-from " + signedFrom
	}
	f.makeKeyless(t, rec)
	setAssets(t, f, nil)
	dir := t.TempDir()
	var lines string
	s := &signedFromFixture{fixture: f}
	for _, k := range []struct{ name, principal string }{{"new", "mash-release"}, {"old", "old@example"}} {
		p := filepath.Join(dir, k.name)
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", p).CombinedOutput(); err != nil {
			t.Skipf("no ssh-keygen: %v\n%s", err, out)
		}
		pub, _ := os.ReadFile(p + ".pub")
		if k.name == "new" {
			s.newKey = p + ".pub"
			fp, err := keyFingerprint(string(pub))
			if err != nil {
				t.Fatal(err)
			}
			s.o.ReleaseKeyFingerprint = fp // a generated key cannot be the real release key
			if !withRelease {
				continue
			}
		} else {
			s.oldKey = p + ".pub"
		}
		lines += k.principal + ` namespaces="git" ` + strings.TrimSpace(string(pub)) + "\n"
	}
	write(t, dir, "signers", lines)
	f.o.Signers = filepath.Join(dir, "signers")
	return s
}

// tag makes v0.1.0 at X on origin, signed with the public key file key (git signs with the matching private key), or
// annotated and unsigned when key is empty, and returns the tag object.
func (s *signedFromFixture) tag(t *testing.T, key string) string { return s.tagAs(t, "v0.1.0", key) }

// tagAs is tag for the tag named name.
func (s *signedFromFixture) tagAs(t *testing.T, name, key string) string {
	t.Helper()
	args := []string{"-c", "tag.gpgSign=false", "tag", "-f", "-a", name, "-m", "release", s.x}
	if key != "" {
		args = []string{"-c", "user.signingkey=" + key, "tag", "-f", "-s", name, "-m", "release", s.x}
	}
	gitT(t, s.repo, args...)
	gitT(t, s.repo, "push", "-q", "-f", "origin", name)
	obj, _, err := tagRefs(s.repo, s.origin, name)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestVerifyTag_signedFrom(t *testing.T) {
	t.Run("a tag signed by the release key passes, and no temp file is left", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		before, _ := filepath.Glob(filepath.Join(os.TempDir(), "mash-release-signers-*"))
		if err := verifyTag(s.o, "v0.1.0", s.tag(t, s.newKey), s.x, tagSignedFrom); err != nil {
			t.Fatal(err)
		}
		after, _ := filepath.Glob(filepath.Join(os.TempDir(), "mash-release-signers-*"))
		if len(after) != len(before) {
			t.Errorf("temp files left: %v -> %v", before, after)
		}
	})
	t.Run("a tag signed by the old key is refused though the file lists it", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		obj := s.tag(t, s.oldKey)
		if err := verifyTag(s.o, "v0.1.0", obj, s.x, tagSignedFrom); err == nil || !strings.Contains(err.Error(), "only the mash-release key signs") {
			t.Fatalf("err = %v", err)
		}
		if err := verifyTag(s.o, "v0.1.0", obj, s.x, tagSigned); err != nil {
			t.Errorf("the old key no longer signs an old tag: %v", err)
		}
	})
	t.Run("an annotated unsigned tag is refused", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		if err := verifyTag(s.o, "v0.1.0", s.tag(t, ""), s.x, tagSignedFrom); err == nil || !strings.Contains(err.Error(), "not SSH-signed") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a signers file with no mash-release line fails closed", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", false)
		if err := verifyTag(s.o, "v0.1.0", s.tag(t, s.oldKey), s.x, tagSignedFrom); err == nil || !strings.Contains(err.Error(), "no mash-release line") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestVerifyTag_signedFromRefusesOtherSignatureKinds(t *testing.T) {
	for _, c := range []struct{ name, block string }{
		{"PGP", "-----BEGIN PGP SIGNATURE-----\n\nAAAA\n-----END PGP SIGNATURE-----\n"},
		{"X.509", "-----BEGIN SIGNED MESSAGE-----\nAAAA\n-----END SIGNED MESSAGE-----\n"},
	} {
		for _, withSSH := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s block, with a real SSH signature too: %v", c.name, withSSH), func(t *testing.T) {
				s := newSignedFrom(t, "v0.1.0", true)
				key := ""
				if withSSH {
					key = s.newKey
				}
				s.tag(t, key)
				body := gitT(t, s.repo, "cat-file", "tag", "v0.1.0") + "\n" + c.block
				f := filepath.Join(t.TempDir(), "tag")
				if err := os.WriteFile(f, []byte(body+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				obj := gitT(t, s.repo, "hash-object", "-t", "tag", "-w", f)
				gitT(t, s.repo, "update-ref", "refs/tags/v0.1.0", obj)
				gitT(t, s.repo, "push", "-q", "-f", "origin", "refs/tags/v0.1.0")
				if err := verifyTag(s.o, "v0.1.0", obj, s.x, tagSignedFrom); err == nil || !strings.Contains(err.Error(), "not SSH-signed") {
					t.Fatalf("err = %v", err)
				}
			})
		}
	}
}

func TestReleaseSignersFile(t *testing.T) {
	keygen := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "k")
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", p).CombinedOutput(); err != nil {
			t.Skipf("no ssh-keygen: %v\n%s", err, out)
		}
		pub, _ := os.ReadFile(p + ".pub")
		return `mash-release namespaces="git" ` + strings.TrimSpace(string(pub))
	}
	file := func(t *testing.T, body string) string {
		t.Helper()
		d := t.TempDir()
		write(t, d, "signers", body)
		return filepath.Join(d, "signers")
	}
	t.Run("two mash-release lines fail", func(t *testing.T) {
		a, b := keygen(t), keygen(t)
		fp, _ := keyFingerprint(a)
		if _, err := releaseSignersFile(file(t, a+"\n"+b+"\n"), fp); err == nil || !strings.Contains(err.Error(), "has 2 mash-release lines") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a mash-release line with a different key fails", func(t *testing.T) {
		if _, err := releaseSignersFile(file(t, keygen(t)+"\n"), releaseKeyFingerprint); err == nil || !strings.Contains(err.Error(), "not the release key") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a key under another principal does not count", func(t *testing.T) {
		other := "other@example" + strings.TrimPrefix(keygen(t), "mash-release")
		if _, err := releaseSignersFile(file(t, other+"\n"), releaseKeyFingerprint); err == nil || !strings.Contains(err.Error(), "no mash-release line") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the real key line of docs/allowed_signers passes", func(t *testing.T) {
		f, err := releaseSignersFile(filepath.Join("..", "..", "docs", "allowed_signers"), releaseKeyFingerprint)
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(f)
	})
}

func TestAssets_signedFrom(t *testing.T) {
	gate := func(s *signedFromFixture) (string, error) {
		err := checkAssets(context.Background(), s.o)
		return s.o.Out.(*bytes.Buffer).String(), err
	}
	t.Run("signed by the release key: passes and names its step", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		s.tag(t, s.newKey)
		out, err := gate(s)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, "tag v0.1.0 is annotated, SSH-signed by the release key mash-release and at X (signed-from v0.1.0)") {
			t.Errorf("step missing:\n%s", out)
		}
	})
	t.Run("signed by the old key: refused", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		s.tag(t, s.oldKey)
		if _, err := gate(s); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unsigned at signed-from: refused", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		s.tag(t, "")
		if _, err := gate(s); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a record with no signed-from line: an unsigned tag still passes, as before ADR 0199", func(t *testing.T) {
		s := newSignedFrom(t, "", true)
		s.tag(t, "")
		out, err := gate(s)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, "ok    tag v0.1.0 is annotated and at X (keyless") || strings.Contains(out, "SSH-signed") {
			t.Errorf("an unsigned tag is judged keyless when no signed-from is recorded:\n%s", out)
		}
	})
	t.Run("unsigned between the cutover and signed-from: still passes", func(t *testing.T) {
		s := newSignedFrom(t, "v0.2.0", true)
		s.tag(t, "")
		if out, err := gate(s); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	})
	// The comparison is numeric, field by field: v0.6.10 and v0.10.0 are above signed-from v0.6.1, v0.6.0 is below.
	for _, c := range []struct {
		tag      string
		required bool
	}{{"v0.6.10", true}, {"v0.10.0", true}, {"v0.6.1", true}, {"v0.6.0", false}} {
		t.Run("version compare: unsigned "+c.tag+" against signed-from v0.6.1", func(t *testing.T) {
			s := newSignedFrom(t, "v0.6.1", true)
			s.tagAs(t, c.tag, "")
			s.o.Version = strings.TrimPrefix(c.tag, "v")
			out, _ := gate(s)
			signedStep := "FAIL  tag " + c.tag + " is annotated, SSH-signed by the release key"
			if got := strings.Contains(out, signedStep); got != c.required {
				t.Fatalf("unsigned %s refused as unsigned = %v, want %v:\n%s", c.tag, got, c.required, out)
			}
			if !c.required && !strings.Contains(out, "ok    tag "+c.tag+" is annotated and at X (keyless") {
				t.Errorf("below signed-from the tag is judged keyless:\n%s", out)
			}
		})
	}
	t.Run("a malformed record fails the gate", func(t *testing.T) {
		s := newSignedFrom(t, "v0.0.1", true) // below the cutover v0.1.0
		s.tag(t, s.newKey)
		if _, err := gate(s); err == nil || exitCode(err) != 3 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("publish never makes the tag: a signed-from tag is keyless", func(t *testing.T) {
		s := newSignedFrom(t, "v0.1.0", true)
		if cv, kl, err := (realWorld{o: s.o}).Keyless("v0.1.0", s.x); err != nil || !kl || cv != "v0.1.0" {
			t.Fatalf("Keyless = %q, %v, %v", cv, kl, err)
		}
	})
}

// The repository's own records say what ADR 0199 says: v0.6.2 is the first tag the gate requires signed, the release key is listed under
// its principal, and the frozen sums-signer file does not list it.
func TestRepositoryRecordsSignedFromAndTheReleaseKey(t *testing.T) {
	cut, err := os.ReadFile(filepath.Join("..", "..", "docs", "release-cutover"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, ln := range strings.Split(string(cut), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" && !strings.HasPrefix(ln, "#") {
			lines = append(lines, ln)
		}
	}
	if len(lines) != 2 || lines[0] != "v0.4.0" || lines[1] != "signed-from v0.6.2" {
		t.Errorf("docs/release-cutover records %q, want the cutover v0.4.0 and signed-from v0.6.2", lines)
	}
	signers, err := os.ReadFile(filepath.Join("..", "..", "docs", "allowed_signers"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(signers), "mash-release namespaces=\"git\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN+y4LF/JU6z+cNrfeZnwAyjT/j/K3IyrhS5iulw0bTu") {
		t.Error("docs/allowed_signers does not list the mash-release key")
	}
	frozen, err := os.ReadFile(filepath.Join("..", "..", "ci", "release.allowed_signers"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(frozen), "mash-release ssh-ed25519") || strings.Contains(string(frozen), "AAAAC3NzaC1lZDI1NTE5AAAAIN+y4LF") {
		t.Error("ci/release.allowed_signers lists the new key: it stays frozen (SHA256SUMS is attested, not signed)")
	}
}
