package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

type sumsKey struct{ priv, pub string } // pub is the allowed_signers key part: "ssh-ed25519 AAAA..."

// newSumsKey makes a throwaway ed25519 key in dir; the tests never touch a real key.
func newSumsKey(t *testing.T, dir, name string) sumsKey {
	t.Helper()
	k := filepath.Join(dir, name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", k, "-C", name).CombinedOutput(); err != nil {
		t.Skipf("no ssh-keygen to make a signing key: %v\n%s", err, out)
	}
	b, err := os.ReadFile(k + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	return sumsKey{priv: k, pub: f[0] + " " + f[1]}
}

func signersFor(k sumsKey) []byte { return []byte("release " + k.pub + "\n") }

func TestSums_signAndVerify(t *testing.T) {
	k := newSumsKey(t, t.TempDir(), "k")
	data := []byte("abc  mash-linux-amd64\n")
	sig, err := signSums(k.priv, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(sig), "-----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("not an armored SSH signature: %.60q", sig)
	}
	if err := verifySums(signersFor(k), data, sig); err != nil {
		t.Fatalf("a good signature did not verify: %v", err)
	}
	// The same verification the docs give a user: ssh-keygen itself, with the file as it is in the repository.
	dir := t.TempDir()
	write(t, dir, "SHA256SUMS", string(data))
	write(t, dir, "SHA256SUMS.sig", string(sig))
	write(t, dir, "signers", string(signersFor(k)))
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", filepath.Join(dir, "signers"), "-I", "release", "-n", "mash-release", "-s", filepath.Join(dir, "SHA256SUMS.sig"))
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen does not verify what signSums made: %v\n%s", err, out)
	}
}

func TestSums_verifyRefusals(t *testing.T) {
	dir := t.TempDir()
	k, other := newSumsKey(t, dir, "k"), newSumsKey(t, dir, "other")
	data := []byte("abc  mash-linux-amd64\n")
	sig, err := signSums(k.priv, data)
	if err != nil {
		t.Fatal(err)
	}
	// Signed under another namespace: the very key and the very bytes, but a signature made for another purpose.
	write(t, dir, "SHA256SUMS", string(data))
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-n", "other-release", "-f", k.priv, filepath.Join(dir, "SHA256SUMS")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	wrongNS, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS.sig"))
	cases := map[string]struct {
		signers, data, sig []byte
	}{
		"tampered SHA256SUMS":         {signersFor(k), []byte("abd  mash-linux-amd64\n"), sig},
		"wrong namespace":             {signersFor(k), data, wrongNS},
		"key not in allowed_signers":  {signersFor(other), data, sig},
		"empty allowed_signers":       {nil, data, sig},
		"garbage signature":           {signersFor(k), data, []byte("not a signature")},
		"another principal's entry":   {[]byte("someone " + k.pub + "\n"), data, sig},
		"signature of another sums":   {signersFor(k), data, mustSign(t, k, "other bytes\n")},
		"signers entry for the other": {signersFor(other), data, wrongNS},
	}
	for name, c := range cases {
		err := verifySums(c.signers, c.data, c.sig)
		if err == nil {
			t.Errorf("%s: verified", name)
		} else if got := exitCode(err); got != 3 {
			t.Errorf("%s: exit %d, want 3 (a refusal): %v", name, got, err)
		}
	}
}

func mustSign(t *testing.T, k sumsKey, data string) []byte {
	t.Helper()
	sig, err := signSums(k.priv, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestSums_checkEntries(t *testing.T) {
	hashes := map[release.Target]string{}
	have := map[string]string{}
	var lines []string
	for i, tg := range release.Targets {
		h := strings.Repeat(string(rune('a'+i)), 64)
		hashes[tg] = h
		have[tg.AssetName()] = "sha256:" + h
		lines = append(lines, h+"  "+tg.AssetName())
	}
	script := strings.Repeat("d", 64)
	have[release.ScriptName] = "sha256:" + script
	lines = append(lines, script+"  "+release.ScriptName)
	good := strings.Join(lines, "\n") + "\n"
	have[release.SumsName] = "sha256:x"
	mod := func(f func(sums *string, have map[string]string)) (string, map[string]string) {
		s, h := good, map[string]string{}
		for k, v := range have {
			h[k] = v
		}
		f(&s, h)
		return s, h
	}
	if err := checkSumEntries([]byte(good), have, hashes, script); err != nil {
		t.Fatalf("good sums refused: %v", err)
	}
	first := release.Targets[0].AssetName()
	cases := map[string]func(*string, map[string]string){
		"malformed line":           func(s *string, h map[string]string) { *s += "not a checksum line\n" },
		"short hash":               func(s *string, h map[string]string) { *s += "abc  mash-extra\n" },
		"duplicate name":           func(s *string, h map[string]string) { *s += lines[0] + "\n" },
		"a name that is no target": func(s *string, h map[string]string) { *s += strings.Repeat("e", 64) + "  mash-plan9-386\n" },
		"a foreign file":           func(s *string, h map[string]string) { *s += strings.Repeat("e", 64) + "  install.sh\n" },
		"digest differs from build": func(s *string, h map[string]string) {
			*s = strings.Replace(*s, hashes[release.Targets[0]], strings.Repeat("0", 64), 1)
		},
		"script digest differs from the script at X": func(s *string, h map[string]string) {
			*s = strings.Replace(*s, script, strings.Repeat("0", 64), 1)
			h[release.ScriptName] = "sha256:" + strings.Repeat("0", 64)
		},
		"script missing from sums":            func(s *string, h map[string]string) { *s = strings.Join(lines[:len(lines)-1], "\n") + "\n" },
		"script listed but not attached":      func(s *string, h map[string]string) { delete(h, release.ScriptName) },
		"script attached with another digest": func(s *string, h map[string]string) { h[release.ScriptName] = "sha256:" + strings.Repeat("0", 64) },
		"target missing from sums":            func(s *string, h map[string]string) { *s = strings.Join(lines[1:], "\n") + "\n" },
		"listed but not attached":             func(s *string, h map[string]string) { delete(h, first) },
		"attached digest differs":             func(s *string, h map[string]string) { h[first] = "sha256:" + strings.Repeat("0", 64) },
		"attached but not listed":             func(s *string, h map[string]string) { h["mash-linux-riscv64"] = "sha256:" + strings.Repeat("f", 64) },
	}
	for name, f := range cases {
		s, h := mod(f)
		if err := checkSumEntries([]byte(s), h, hashes, script); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
}

// signedFixture is a draft release whose assets are right and carry no signature yet.
func signedFixture(t *testing.T, mutate func(map[string]string)) *fixture {
	t.Helper()
	f := newFixture(t)
	setAssets(t, f, mutate)
	return f
}

func (f *fixture) ghDir() string { return filepath.Dir(f.o.GH) }

func (f *fixture) ghLog() string {
	b, _ := os.ReadFile(filepath.Join(f.ghDir(), "gh.log"))
	return string(b)
}

func (f *fixture) uploaded() string {
	b, _ := os.ReadFile(filepath.Join(f.ghDir(), "files", release.SumsSigName))
	return string(b)
}

// attachSig pretends the release already carries sig (and lists it among the assets).
func (f *fixture) attachSig(t *testing.T, sig []byte, draft bool) {
	t.Helper()
	write(t, filepath.Join(f.ghDir(), "files"), release.SumsSigName, string(sig))
	setAssetsWithSig(t, f, draft)
}

func setAssetsWithSig(t *testing.T, f *fixture, draft bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.ghDir(), "assets.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	s = strings.Replace(s, `"assets":[`, `"assets":[{"name":"SHA256SUMS.sig","digest":"sha256:whatever"},`, 1)
	if !draft {
		s = strings.Replace(s, `"isDraft":true`, `"isDraft":false`, 1)
	}
	write(t, f.ghDir(), "assets.json", s)
}

func sumsOfFixture(t *testing.T, f *fixture) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.ghDir(), "files", release.SumsName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAssets_aDraftIsSignedVerifiedAndUploaded(t *testing.T) {
	f := signedFixture(t, nil)
	if err := checkAssets(context.Background(), f.o); err != nil {
		t.Fatalf("a correct draft was refused: %v", err)
	}
	sig := f.uploaded()
	if !strings.HasPrefix(sig, "-----BEGIN SSH SIGNATURE-----") {
		t.Fatalf("no signature was uploaded: %q (gh calls:\n%s)", sig, f.ghLog())
	}
	signers, err := signersAt(f.o, f.x)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySums(signers, sumsOfFixture(t, f), []byte(sig)); err != nil {
		t.Fatalf("the uploaded signature does not verify against the signers file at X: %v", err)
	}
	if !strings.Contains(f.ghLog(), "release upload v0.1.0") || !strings.Contains(f.ghLog(), "--clobber") {
		t.Errorf("gh was not asked to upload:\n%s", f.ghLog())
	}
}

func TestAssets_aValidSignatureOnTheDraftIsReusedWithoutAKey(t *testing.T) {
	f := signedFixture(t, nil)
	k := keyOfFixture(t, f)
	f.attachSig(t, mustSign(t, k, string(sumsOfFixture(t, f))), true)
	t.Setenv("RELEASE_SIGNING_KEY", filepath.Join(t.TempDir(), "no-such-key"))
	if err := checkAssets(context.Background(), f.o); err != nil {
		t.Fatalf("a resumed run with a good signature was refused: %v", err)
	}
	if strings.Contains(f.ghLog(), "release upload") {
		t.Errorf("a good signature was uploaded again:\n%s", f.ghLog())
	}
}

// keyOfFixture is the key the fixture's ci/release.allowed_signers at X lists, found through its private half.
func keyOfFixture(t *testing.T, f *fixture) sumsKey {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("RELEASE_SIGNING_KEY") + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fl := strings.Fields(string(b))
	return sumsKey{priv: os.Getenv("RELEASE_SIGNING_KEY"), pub: fl[0] + " " + fl[1]}
}

func TestAssets_signatureRefusals(t *testing.T) {
	t.Run("an attached signature that does not verify is refused, not replaced", func(t *testing.T) {
		f := signedFixture(t, nil)
		k := keyOfFixture(t, f)
		f.attachSig(t, mustSign(t, k, "some other bytes\n"), true)
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "does not verify") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if strings.Contains(f.ghLog(), "release upload") {
			t.Errorf("a bad signature was replaced:\n%s", f.ghLog())
		}
	})
	t.Run("a signature by a key X does not list is refused and not uploaded", func(t *testing.T) {
		f := signedFixture(t, nil)
		t.Setenv("RELEASE_SIGNING_KEY", newSumsKey(t, t.TempDir(), "stranger").priv)
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "lists at X") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if f.uploaded() != "" {
			t.Error("a signature by an unlisted key was uploaded")
		}
	})
	t.Run("no signing key is the gate's failure (4), and nothing is uploaded", func(t *testing.T) {
		f := signedFixture(t, nil)
		t.Setenv("RELEASE_SIGNING_KEY", filepath.Join(t.TempDir(), "no-such-key"))
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 4 || !strings.Contains(err.Error(), "signing key") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if f.uploaded() != "" {
			t.Error("something was uploaded")
		}
	})
	t.Run("a published release without a signature is refused", func(t *testing.T) {
		f := signedFixture(t, nil)
		setAssetsWithSig(t, f, false)
		// drop the sig entry again: published, and none attached
		b, _ := os.ReadFile(filepath.Join(f.ghDir(), "assets.json"))
		write(t, f.ghDir(), "assets.json", strings.Replace(string(b), `{"name":"SHA256SUMS.sig","digest":"sha256:whatever"},`, "", 1))
		err := checkAssets(context.Background(), f.o)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "not attached to the published release") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if strings.Contains(f.ghLog(), "release upload") {
			t.Error("a published release was amended")
		}
	})
	t.Run("a published release with a verifying signature passes, untouched", func(t *testing.T) {
		f := signedFixture(t, nil)
		f.attachSig(t, mustSign(t, keyOfFixture(t, f), string(sumsOfFixture(t, f))), false)
		t.Setenv("RELEASE_SIGNING_KEY", filepath.Join(t.TempDir(), "no-such-key"))
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(f.ghLog(), "release upload") {
			t.Error("a published release was amended")
		}
	})
	t.Run("the signers file missing at X is refused", func(t *testing.T) {
		f := newFixture(t)
		setAssets(t, f, nil)
		gitT(t, f.repo, "rm", "-q", signersPath)
		gitT(t, f.repo, "commit", "-q", "-m", "drop signers")
		later := gitT(t, f.repo, "rev-parse", "HEAD")
		// The candidate is the commit without the file; its tag is moved there so the other steps hold.
		gitT(t, f.repo, "push", "-q", "origin", "main")
		f.o.X = later
		retag(t, f, "-s", "v0.1.0", "-m", "release", later)
		if err := checkAssets(context.Background(), f.o); err == nil {
			t.Fatal("passed without a signers file at X")
		}
	})
	t.Run("a sums file that differs from our builds is never signed", func(t *testing.T) {
		f := signedFixture(t, nil)
		f.o.build = func(ctx context.Context, src, out, goos, goarch, version, commit string) (string, error) {
			return strings.Repeat("9", 64), nil // our rebuild gives other digests than the attached ones
		}
		if err := checkAssets(context.Background(), f.o); err == nil {
			t.Fatal("passed")
		}
		if f.uploaded() != "" {
			t.Error("a signature was made over sums that differ from the rebuild")
		}
	})
}

// Publishing refuses a release that has no SHA256SUMS.sig that verifies, whatever the gate before it said.
func TestPublish_realWorldRefusesWithoutAVerifyingSignature(t *testing.T) {
	f := signedFixture(t, nil)
	w := realWorld{ctx: context.Background(), o: f.o}
	err := w.Publish("v0.1.0")
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "SHA256SUMS.sig is no longer attached") {
		t.Fatalf("err = %v (exit %d)", err, exitCode(err))
	}
	if strings.Contains(f.ghLog(), "release edit") {
		t.Fatal("the release was published without a signature")
	}
	f.attachSig(t, mustSign(t, keyOfFixture(t, f), "tampered\n"), true)
	if err := w.Publish("v0.1.0"); err == nil || !strings.Contains(err.Error(), "does not verify") || strings.Contains(f.ghLog(), "release edit") {
		t.Fatalf("a signature that does not verify did not stop publishing: %v", err)
	}
	f.attachSig(t, mustSign(t, keyOfFixture(t, f), string(sumsOfFixture(t, f))), true)
	f.reportUploads(t)
	if err := w.Publish("v0.1.0"); err != nil {
		t.Fatalf("a verifying signature was refused: %v", err)
	}
	if !strings.Contains(f.ghLog(), "release edit v0.1.0") {
		t.Errorf("the release was not published:\n%s", f.ghLog())
	}
}

// signedDraft is a draft whose assets are right and whose SHA256SUMS.sig is made and uploaded by the gate, ready to publish.
func signedDraft(t *testing.T) *fixture {
	t.Helper()
	f := signedFixture(t, nil)
	f.gate = &gateRecord{}
	if err := checkAssetsRecord(context.Background(), f.o, f.gate); err != nil {
		t.Fatalf("a correct draft was refused: %v", err)
	}
	f.reportUploads(t)
	return f
}

func (f *fixture) publish() error {
	return realWorld{ctx: context.Background(), o: f.o, gate: f.gate}.Publish("v0.1.0")
}

// reportUploads makes the fake gh report SHA256SUMS and SHA256SUMS.sig with the digests of the bytes it serves, as
// GitHub does (the fake's upload only stores the file).
func (f *fixture) reportUploads(t *testing.T) {
	t.Helper()
	f.editAssets(t, func(s string) string {
		var v struct {
			IsDraft bool                `json:"isDraft"`
			Assets  []map[string]string `json:"assets"`
		}
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		keep := v.Assets[:0]
		for _, a := range v.Assets {
			if a["name"] != release.SumsName && a["name"] != release.SumsSigName {
				keep = append(keep, a)
			}
		}
		v.Assets = keep
		for _, n := range []string{release.SumsName, release.SumsSigName} {
			b, err := os.ReadFile(filepath.Join(f.ghDir(), "files", n))
			if err != nil {
				t.Fatal(err)
			}
			v.Assets = append(v.Assets, map[string]string{"name": n, "digest": "sha256:" + shaOf(string(b))})
		}
		out, _ := json.Marshal(v)
		return string(out)
	})
}

// dropAsset removes name from the release's listing; setDigest changes the digest it reports.
func (f *fixture) dropAsset(t *testing.T, name string) {
	t.Helper()
	f.editAssets(t, func(s string) string {
		var v struct {
			IsDraft bool                `json:"isDraft"`
			Assets  []map[string]string `json:"assets"`
		}
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		keep := v.Assets[:0]
		for _, a := range v.Assets {
			if a["name"] != name {
				keep = append(keep, a)
			}
		}
		v.Assets = keep
		out, _ := json.Marshal(v)
		return string(out)
	})
}

func (f *fixture) editAssets(t *testing.T, edit func(s string) string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.ghDir(), "assets.json"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, f.ghDir(), "assets.json", edit(string(b)))
}

func TestPublish_signedDraftIsPublished(t *testing.T) {
	f := signedDraft(t)
	if err := f.publish(); err != nil {
		t.Fatalf("a signed, unchanged draft was refused: %v", err)
	}
	if !strings.Contains(f.ghLog(), "release edit v0.1.0") || !strings.Contains(f.ghLog(), "--draft=false") {
		t.Errorf("gh was not asked to publish:\n%s", f.ghLog())
	}
}

func TestPublish_assetChangedAfterTheGateIsRefused(t *testing.T) {
	f := signedDraft(t)
	name := release.Targets[0].AssetName()
	f.editAssets(t, func(s string) string {
		var v struct {
			IsDraft bool                `json:"isDraft"`
			Assets  []map[string]string `json:"assets"`
		}
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatal(err)
		}
		for _, a := range v.Assets {
			if a["name"] == name {
				a["digest"] = "sha256:" + strings.Repeat("0", 64) // swapped after the gate
			}
		}
		out, _ := json.Marshal(v)
		return string(out)
	})
	err := f.publish()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "not our fresh build of X") {
		t.Fatalf("err = %v (exit %d)", err, exitCode(err))
	}
	if strings.Contains(f.ghLog(), "release edit") {
		t.Errorf("a tampered draft was published:\n%s", f.ghLog())
	}
}

func TestPublish_signedAssetRemovedOrExtraAssetAddedIsRefused(t *testing.T) {
	cases := map[string]struct {
		edit func(s string) string
		want string
	}{
		"an unlisted mash-* asset": {func(s string) string {
			return strings.Replace(s, `"assets":[`, `"assets":[{"name":"mash-linux-riscv64","digest":"sha256:`+strings.Repeat("f", 64)+`"},`, 1)
		}, "not listed in the signed SHA256SUMS"},
		"an unlisted installer": {func(s string) string {
			return strings.Replace(s, `"assets":[`, `"assets":[{"name":"install.sh","digest":"sha256:`+strings.Repeat("f", 64)+`"},`, 1)
		}, "not listed in the signed SHA256SUMS"},
		"the script swapped after the gate": {func(s string) string {
			return strings.Replace(s, "sha256:"+shaOf("#!/bin/sh\necho quickstart\n"), "sha256:"+strings.Repeat("0", 64), 1)
		}, "not the script at X"},
		"a signed asset gone": {func(s string) string {
			return strings.Replace(s, `"name":"`+release.Targets[0].AssetName()+`"`, `"name":"renamed"`, 1)
		}, "no longer attached"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := signedDraft(t)
			f.editAssets(t, c.edit)
			err := f.publish()
			if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v (exit %d)", err, exitCode(err))
			}
			if strings.Contains(f.ghLog(), "release edit") {
				t.Errorf("published:\n%s", f.ghLog())
			}
		})
	}
}

// H2: the final listing must still carry SHA256SUMS and SHA256SUMS.sig, with the digests of the bytes just verified.
func TestPublish_sumsOrSignatureMissingOrChangedInTheFinalListingIsRefused(t *testing.T) {
	for _, name := range []string{release.SumsSigName, release.SumsName} {
		t.Run("removed "+name, func(t *testing.T) {
			f := signedDraft(t)
			f.dropAsset(t, name)
			err := f.publish()
			if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "no longer attached") {
				t.Fatalf("err = %v (exit %d)", err, exitCode(err))
			}
			if strings.Contains(f.ghLog(), "release edit") {
				t.Errorf("published:\n%s", f.ghLog())
			}
		})
	}
	t.Run("a reported digest that is not the downloaded bytes", func(t *testing.T) {
		f := signedDraft(t)
		f.editAssets(t, func(s string) string {
			return strings.Replace(s, "sha256:"+shaOf(string(sumsOfFixture(t, f))), "sha256:"+strings.Repeat("0", 64), 1)
		})
		err := f.publish()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "changed after it was verified") {
			t.Fatalf("err = %v (exit %d)", err, exitCode(err))
		}
		if strings.Contains(f.ghLog(), "release edit") {
			t.Errorf("published:\n%s", f.ghLog())
		}
	})
}

// H1: the whole asset set (binaries, SHA256SUMS and a valid signature) is swapped for another validly signed release's
// after the --assets gate. Publishing is bound to the gate's fresh builds of X, so it is refused.
func TestPublish_wholeAssetSetReplacedAfterTheGateIsRefused(t *testing.T) {
	f := signedDraft(t)
	other := map[release.Target]string{}
	var assets []map[string]string
	for i, tg := range release.Targets {
		other[tg] = strings.Repeat(string(rune('a'+i)), 64) // binaries that were never built from X
		assets = append(assets, map[string]string{"name": tg.AssetName(), "digest": "sha256:" + other[tg]})
	}
	sums := release.ExpectedSums(other, shaOf("#!/bin/sh\necho quickstart\n"))
	sig := mustSign(t, keyOfFixture(t, f), sums) // validly signed by the release key
	files := filepath.Join(f.ghDir(), "files")
	write(t, files, release.SumsName, sums)
	write(t, files, release.SumsSigName, string(sig))
	assets = append(assets,
		map[string]string{"name": release.SumsName, "digest": "sha256:" + shaOf(sums)},
		map[string]string{"name": release.SumsSigName, "digest": "sha256:" + shaOf(string(sig))})
	b, _ := json.Marshal(map[string]any{"isDraft": true, "assets": assets})
	write(t, f.ghDir(), "assets.json", string(b))

	err := f.publish()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "not the one the --assets gate checked") {
		t.Fatalf("err = %v (exit %d)", err, exitCode(err))
	}
	if strings.Contains(f.ghLog(), "release edit") {
		t.Errorf("a replaced asset set was published:\n%s", f.ghLog())
	}
}

// With no gate result from this process, Publish runs the gate itself rather than trust anything else.
func TestPublish_withoutAGateResultTheGateRunsFirst(t *testing.T) {
	f := signedDraft(t)
	f.gate = nil
	if err := f.publish(); err != nil {
		t.Fatalf("a correct signed draft was refused: %v", err)
	}
	if !strings.Contains(f.ghLog(), "release edit v0.1.0") {
		t.Errorf("not published:\n%s", f.ghLog())
	}
}
