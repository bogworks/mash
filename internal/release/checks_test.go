package release

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

var candidate = strings.Repeat("a", 40)

func withApp(r CheckRun) CheckRun { r.App.Slug = CIApp; return r }

func runs(names ...string) []CheckRun {
	var out []CheckRun
	for i, n := range names {
		r := CheckRun{ID: int64(i + 1), HeadSHA: candidate, Name: n, Status: "completed", Conclusion: "success"}
		r.App.Slug = CIApp
		out = append(out, r)
	}
	return out
}

func TestCheckCI_needsEveryRequiredName(t *testing.T) {
	if err := CheckCI(runs(RequiredChecks...), candidate); err != nil {
		t.Fatal(err)
	}
	for i := range RequiredChecks {
		rest := append(append([]string{}, RequiredChecks[:i]...), RequiredChecks[i+1:]...)
		if err := CheckCI(runs(rest...), candidate); err == nil || !strings.Contains(err.Error(), RequiredChecks[i]) {
			t.Errorf("without %q: %v", RequiredChecks[i], err)
		}
	}
	if err := CheckCI(nil, candidate); err == nil {
		t.Fatal("no check runs passed")
	}
}

func TestCheckCI_failureNamesTheRun(t *testing.T) {
	for _, bad := range []CheckRun{
		{Name: "reproducible", Status: "completed", Conclusion: "failure"},
		{Name: "reproducible", Status: "in_progress", Conclusion: ""},
		{Name: "reproducible", Status: "completed", Conclusion: "skipped"},
		{Name: "reproducible", Status: "completed", Conclusion: "cancelled"},
	} {
		rs := runs("test (linux)")
		bad.ID, bad.HeadSHA = 99, candidate
		bad.App.Slug = CIApp
		if err := CheckCI(append(rs, bad), candidate); err == nil || !strings.Contains(err.Error(), "reproducible") {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestCheckCI_aRerunSupersedesAnEarlierFailureButNotALaterOne(t *testing.T) {
	rs := append(runs(RequiredChecks...), withApp(CheckRun{ID: 0, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "failure"}))
	if err := CheckCI(rs, candidate); err != nil {
		t.Fatalf("the latest run of a check decides: %v", err)
	}
	rs = append(runs(RequiredChecks...), withApp(CheckRun{ID: 50, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "failure"}))
	if err := CheckCI(rs, candidate); err == nil {
		t.Fatal("a newer failure was ignored")
	}
}

func TestCheckCI_runsMustBeForTheCandidate(t *testing.T) {
	other := strings.Repeat("e", 40)
	rs := runs(RequiredChecks...)
	for i := range rs {
		rs[i].HeadSHA = other
	}
	if err := CheckCI(rs, candidate); err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("checks for another commit passed: %v", err)
	}
	mixed := append(runs(RequiredChecks...), withApp(CheckRun{ID: 77, HeadSHA: other, Name: "reproducible", Status: "completed", Conclusion: "success"}))
	if err := CheckCI(mixed, candidate); err == nil {
		t.Fatal("a run for another commit was ignored")
	}
}

func TestCheckCI_conflictingDuplicatesOfOneIDAreRefusedWhateverTheOrder(t *testing.T) {
	good := withApp(CheckRun{ID: 5, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "success"})
	bad := good
	bad.Conclusion = "failure"
	rest := runs("test (linux)")
	for _, rs := range [][]CheckRun{append(append([]CheckRun{}, rest...), good, bad), append(append([]CheckRun{}, rest...), bad, good)} {
		if err := CheckCI(rs, candidate); err == nil || !strings.Contains(err.Error(), "reported twice") {
			t.Fatalf("two results with one run id: %v", err)
		}
	}
}

func hashes() map[Binary]string {
	m := map[Binary]string{}
	for i, t := range Binaries() {
		m[t] = strings.Repeat(string("abcdef97"[i]), 64)
	}
	return m
}

// ciHashes are the hex digests of the SBOMs and the provenance, which the gate cannot rebuild.
func ciHashes() map[string]string {
	m := map[string]string{}
	for i, n := range CIAssetNames() {
		m[n] = strings.Repeat(string(rune('0'+i)), 64)
	}
	return m
}

// goodSums is the SHA256SUMS the draft job makes for goodAssets.
func goodSums() string { return ExpectedSums(hashes(), ciHashes(), false) }

func goodAssets() map[string]string {
	h := hashes()
	have := map[string]string{SumsName: "sha256:" + sha([]byte(goodSums()))}
	for _, t := range Binaries() {
		have[t.AssetName()] = "sha256:" + h[t]
	}
	for n, d := range ciHashes() {
		have[n] = "sha256:" + d
	}
	return have
}

func TestExpectedSums_isSha256sumOutput(t *testing.T) {
	got := goodSums()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	want := []string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64",
		"mashctl-darwin-amd64", "mashctl-darwin-arm64", "mashctl-linux-amd64", "mashctl-linux-arm64",
		"install.sh", "mash-local-nats-setup.sh",
		"mash-darwin-amd64.cdx.json", "mash-darwin-arm64.cdx.json", "mash-linux-amd64.cdx.json", "mash-linux-arm64.cdx.json",
		"mashctl-darwin-amd64.cdx.json", "mashctl-darwin-arm64.cdx.json", "mashctl-linux-amd64.cdx.json", "mashctl-linux-arm64.cdx.json", ProvenanceName}
	if len(lines) != 19 {
		t.Fatalf("%q", got)
	}
	for i, ln := range lines {
		f := strings.Split(ln, "  ") // sha256sum separates hash and name with two spaces
		if len(f) != 2 || len(f[0]) != 64 || f[1] != want[i] {
			t.Errorf("line %d: %q", i, ln)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("no trailing newline")
	}
}

func TestCheckAssets(t *testing.T) {
	if err := CheckAssets(goodAssets(), hashes(), false); err != nil {
		t.Fatalf("a correct release was refused: %v", err)
	}
	mutate := func(f func(m map[string]string)) map[string]string {
		m := goodAssets()
		f(m)
		return m
	}
	cases := map[string]struct {
		have map[string]string
		want string
	}{
		"missing binary":     {mutate(func(m map[string]string) { delete(m, "mash-linux-arm64") }), "mash-linux-arm64 is missing"},
		"missing sums":       {mutate(func(m map[string]string) { delete(m, SumsName) }), "SHA256SUMS is missing"},
		"substituted binary": {mutate(func(m map[string]string) { m["mash-darwin-arm64"] = "sha256:" + strings.Repeat("0", 64) }), "mash-darwin-arm64 has digest"},
		"extra asset":        {mutate(func(m map[string]string) { m["mash-windows-amd64.exe"] = "sha256:x" }), "mash-windows-amd64.exe is not one"},
		"misnamed binary": {mutate(func(m map[string]string) {
			m["mash-linux-x86_64"] = m["mash-linux-amd64"]
			delete(m, "mash-linux-amd64")
		}), "mash-linux-amd64 is missing"},
		"truncated sums": {mutate(func(m map[string]string) {
			m[SumsName] = "sha256:" + sha([]byte(goodSums()[:8*(64+2)+4*len("mash-linux-amd64")+4*len("mashctl-linux-amd64")]))
		}), "SHA256SUMS has digest"},
		"missing mashctl binary": {mutate(func(m map[string]string) { delete(m, "mashctl-linux-arm64") }), "mashctl-linux-arm64 is missing"},
		"substituted mashctl":    {mutate(func(m map[string]string) { m["mashctl-darwin-arm64"] = "sha256:" + strings.Repeat("0", 64) }), "mashctl-darwin-arm64 has digest"},
		"missing mashctl sbom":   {mutate(func(m map[string]string) { delete(m, "mashctl-darwin-amd64.cdx.json") }), "mashctl-darwin-amd64.cdx.json is missing"},
		"mashctl missing from the sums": {mutate(func(m map[string]string) {
			h := hashes()
			delete(h, Binary{"mashctl", Target{"linux", "amd64"}})
			m[SumsName] = "sha256:" + sha([]byte(ExpectedSums(h, ciHashes(), false)))
		}), "SHA256SUMS has digest"},
		"missing install.sh":    {mutate(func(m map[string]string) { delete(m, "install.sh") }), "install.sh is missing"},
		"missing nats setup":    {mutate(func(m map[string]string) { delete(m, "mash-local-nats-setup.sh") }), "mash-local-nats-setup.sh is missing"},
		"script with no digest": {mutate(func(m map[string]string) { m["install.sh"] = "x" }), "not a sha256"},
		"missing sbom":          {mutate(func(m map[string]string) { delete(m, "mash-linux-arm64.cdx.json") }), "mash-linux-arm64.cdx.json is missing"},
		"missing provenance":    {mutate(func(m map[string]string) { delete(m, ProvenanceName) }), "mash.intoto.jsonl is missing"},
		"sbom with no digest":   {mutate(func(m map[string]string) { m["mash-linux-arm64.cdx.json"] = "x" }), "not a sha256"},
		"sums not covering the attached sbom": {mutate(func(m map[string]string) {
			m["mash-linux-arm64.cdx.json"] = "sha256:" + strings.Repeat("e", 64)
		}), "SHA256SUMS has digest"},
		"bad sums":            {mutate(func(m map[string]string) { m[SumsName] = "sha256:" + strings.Repeat("1", 64) }), "SHA256SUMS has digest"},
		"digest without algo": {mutate(func(m map[string]string) { m["mash-linux-amd64"] = hashes()[Binary{"mash", Target{"linux", "amd64"}}] }), "mash-linux-amd64 has digest"},
	}
	for name, c := range cases {
		if err := CheckAssets(c.have, hashes(), false); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
	// An unusable build hash is a problem of its own, not a silent pass.
	h := hashes()
	h[Binary{"mashctl", Target{"linux", "amd64"}}] = ""
	if err := CheckAssets(goodAssets(), h, false); err == nil || !strings.Contains(err.Error(), "no usable build hash") {
		t.Fatalf("%v", err)
	}
}

// Another integration's own check (Mergify writes one on every main commit) is not CI's: it is ignored, whether
// finished or still running. Only a foreign run carrying a required name is refused, as a spoof.
func TestCheckCI_anotherAppsOwnCheckIsIgnored(t *testing.T) {
	rs := runs(RequiredChecks...)
	mergify := CheckRun{ID: 91, HeadSHA: candidate, Name: "Mergify Merge Protections", Status: "completed", Conclusion: "neutral"}
	mergify.App.Slug = "mergify"
	rs = append(rs, mergify)
	if err := CheckCI(rs, candidate); err != nil {
		t.Fatalf("another app's check was not ignored: %v", err)
	}
	other := CheckRun{ID: 92, HeadSHA: candidate, Name: "some-other-check", Status: "in_progress"}
	other.App.Slug = "some-other-app"
	if err := CheckCI(append(rs, other), candidate); err != nil {
		t.Fatalf("an in-progress foreign check was not ignored: %v", err)
	}
}

// A check run written by another integration under a required name is refused, not counted.
func TestCheckCI_aSpoofedNameFromAnotherAppIsRefused(t *testing.T) {
	rs := runs("test (linux)")
	spoof := CheckRun{ID: 90, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "success"}
	spoof.App.Slug = "some-other-app"
	if err := CheckCI(append(rs, spoof), candidate); err == nil || !strings.Contains(err.Error(), "some-other-app") {
		t.Fatalf("a spoofed check passed: %v", err)
	}
	spoof.App.Slug = "" // no app at all
	if err := CheckCI(append(rs, spoof), candidate); err == nil {
		t.Fatal("a check with no app passed")
	}
}

// test (macos) is advisory (ADR 0079): the gate neither requires it nor reads its conclusion.
func TestCheckCI_theAdvisoryMacOSCheckNeitherPassesNorBlocks(t *testing.T) {
	rs := runs(RequiredChecks...)
	if err := CheckCI(rs, candidate); err != nil {
		t.Fatalf("a candidate without test (macos) must pass: %v", err)
	}
	failed := withApp(CheckRun{ID: 98, HeadSHA: candidate, Name: "test (macos)", Status: "completed", Conclusion: "failure"})
	if err := CheckCI(append(rs, failed), candidate); err != nil {
		t.Fatalf("a failed advisory test (macos) must not refuse a candidate: %v", err)
	}
}

// keylessAssets is a release at or above the cutover: SHA256SUMS does not list the provenance bundle, which is attached.
func keylessAssets() map[string]string {
	m := goodAssets()
	m[SumsName] = "sha256:" + sha([]byte(ExpectedSums(hashes(), ciHashes(), true)))
	return m
}

func TestExpectedSums_keylessOmitsTheProvenance(t *testing.T) {
	got := ExpectedSums(hashes(), ciHashes(), true)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 18 || strings.Contains(got, ProvenanceName) {
		t.Fatalf("keyless sums:\n%s", got)
	}
	if want := strings.TrimSuffix(goodSums(), strings.Split(goodSums(), "\n")[18]+"\n"); got != want {
		t.Errorf("keyless sums are not the others' first eighteen lines:\n%s", got)
	}
}

func TestCheckAssets_keyless(t *testing.T) {
	if err := CheckAssets(keylessAssets(), hashes(), true); err != nil {
		t.Fatalf("a correct keyless release was refused: %v", err)
	}
	// The sums that list the provenance are the pre-cutover ones: refused when keyless.
	if err := CheckAssets(goodAssets(), hashes(), true); err == nil || !strings.Contains(err.Error(), "SHA256SUMS has digest") {
		t.Errorf("pre-cutover sums accepted when keyless: %v", err)
	}
	// ... and the keyless sums are refused before the cutover.
	if err := CheckAssets(keylessAssets(), hashes(), false); err == nil || !strings.Contains(err.Error(), "SHA256SUMS has digest") {
		t.Errorf("keyless sums accepted before the cutover: %v", err)
	}
	// The provenance stays required.
	m := keylessAssets()
	delete(m, ProvenanceName)
	if err := CheckAssets(m, hashes(), true); err == nil || !strings.Contains(err.Error(), "mash.intoto.jsonl is missing") {
		t.Errorf("missing provenance: %v", err)
	}
	// A SHA256SUMS.sig is refused when keyless, allowed otherwise.
	m = keylessAssets()
	m[SumsSigName] = "sha256:" + strings.Repeat("a", 64)
	if err := CheckAssets(m, hashes(), true); err == nil || !strings.Contains(err.Error(), "keyless release carries no SHA256SUMS.sig") {
		t.Errorf("sig on a keyless release: %v", err)
	}
	g := goodAssets()
	g[SumsSigName] = "sha256:" + strings.Repeat("a", 64)
	if err := CheckAssets(g, hashes(), false); err != nil {
		t.Errorf("sig before the cutover refused: %v", err)
	}
}

// A release carries mash and mashctl for every target (ADR 0148): eight binaries and eight SBOMs.
func TestBinaries_areMashAndMashctlForEveryTarget(t *testing.T) {
	var got []string
	for _, b := range Binaries() {
		got = append(got, b.AssetName())
	}
	want := []string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64",
		"mashctl-darwin-amd64", "mashctl-darwin-arm64", "mashctl-linux-amd64", "mashctl-linux-arm64"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("binaries %v, want %v", got, want)
	}
	if n := Targets[0].CtlAssetName(); n != "mashctl-darwin-amd64" {
		t.Errorf("CtlAssetName %q", n)
	}
	if n := len(CIAssetNames()); n != 11 { // two scripts, eight SBOMs and the provenance
		t.Errorf("%d CI assets", n)
	}
}

func TestBuildCmd_refusesAProgramThatIsNotReleased(t *testing.T) {
	_, err := BuildCmd(context.Background(), t.TempDir(), filepath.Join(t.TempDir(), "x"), "", "", "0.1.0", "abc123", "mashd")
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("%v", err)
	}
}

// ADR 0150: SHA256SUMS lists the two scripts after the binaries and before the SBOMs, and the gate holds them to the repository.
func TestScripts_areListedAfterTheBinariesAndHeldToTheRepository(t *testing.T) {
	if got := strings.Join(ScriptNames(), " "); got != "install.sh mash-local-nats-setup.sh" {
		t.Fatalf("scripts %q", got)
	}
	lines := strings.Split(goodSums(), "\n")
	if !strings.HasSuffix(lines[8], "  install.sh") || !strings.HasSuffix(lines[9], "  mash-local-nats-setup.sh") || !strings.HasSuffix(lines[10], "  mash-darwin-amd64.cdx.json") {
		t.Errorf("the scripts are not lines 9 and 10:\n%s", goodSums())
	}
	content := map[string][]byte{"install.sh": []byte("a\n"), "mash-local-nats-setup.sh": []byte("b\n")}
	have := map[string]string{"install.sh": "sha256:" + sha(content["install.sh"]), "mash-local-nats-setup.sh": "sha256:" + sha(content["mash-local-nats-setup.sh"])}
	if err := CheckScripts(have, content); err != nil {
		t.Fatalf("identical scripts refused: %v", err)
	}
	changed := map[string]string{"install.sh": "sha256:" + sha([]byte("evil")), "mash-local-nats-setup.sh": have["mash-local-nats-setup.sh"]}
	if err := CheckScripts(changed, content); err == nil || !strings.Contains(err.Error(), "script install.sh has digest") {
		t.Errorf("a changed install.sh: %v", err)
	}
	if err := CheckScripts(map[string]string{"install.sh": have["install.sh"]}, content); err == nil || !strings.Contains(err.Error(), "script mash-local-nats-setup.sh is missing") {
		t.Errorf("a missing script: %v", err)
	}
	if err := CheckScripts(have, map[string][]byte{"install.sh": content["install.sh"]}); err == nil || !strings.Contains(err.Error(), "no copy of scripts/mash-local-nats-setup.sh") {
		t.Errorf("no repository copy: %v", err)
	}
}
