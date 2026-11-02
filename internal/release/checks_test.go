package release

import (
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
		rs := runs("test (ubuntu-latest)", "test (macos-latest)")
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
	rest := runs("test (ubuntu-latest)", "test (macos-latest)")
	for _, rs := range [][]CheckRun{append(append([]CheckRun{}, rest...), good, bad), append(append([]CheckRun{}, rest...), bad, good)} {
		if err := CheckCI(rs, candidate); err == nil || !strings.Contains(err.Error(), "reported twice") {
			t.Fatalf("two results with one run id: %v", err)
		}
	}
}

func hashes() map[Target]string {
	m := map[Target]string{}
	for i, t := range Targets {
		m[t] = strings.Repeat(string(rune('a'+i)), 64)
	}
	return m
}

var scriptHash = strings.Repeat("9", 64)

// ciHashes are the hex digests of the SBOMs and the provenance, which the gate cannot rebuild.
func ciHashes() map[string]string {
	m := map[string]string{}
	for i, n := range CIAssetNames() {
		m[n] = strings.Repeat(string(rune('0'+i)), 64)
	}
	return m
}

// goodSums is the SHA256SUMS the draft job makes for goodAssets.
func goodSums() string { return ExpectedSums(hashes(), scriptHash, ciHashes()) }

func goodAssets() map[string]string {
	h := hashes()
	have := map[string]string{SumsName: "sha256:" + sha([]byte(goodSums())), ScriptName: "sha256:" + scriptHash}
	for _, t := range Targets {
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
	want := []string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64", ScriptName,
		"mash-darwin-amd64.cdx.json", "mash-darwin-arm64.cdx.json", "mash-linux-amd64.cdx.json", "mash-linux-arm64.cdx.json", ProvenanceName}
	if len(lines) != 10 {
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
	if err := CheckAssets(goodAssets(), hashes(), scriptHash); err != nil {
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
		"missing script":     {mutate(func(m map[string]string) { delete(m, ScriptName) }), "quickstart-synadia.sh is missing"},
		"wrong script":       {mutate(func(m map[string]string) { m[ScriptName] = "sha256:" + strings.Repeat("0", 64) }), "quickstart-synadia.sh has digest"},
		"missing sums":       {mutate(func(m map[string]string) { delete(m, SumsName) }), "SHA256SUMS is missing"},
		"substituted binary": {mutate(func(m map[string]string) { m["mash-darwin-arm64"] = "sha256:" + strings.Repeat("0", 64) }), "mash-darwin-arm64 has digest"},
		"extra asset":        {mutate(func(m map[string]string) { m["mash-windows-amd64.exe"] = "sha256:x" }), "mash-windows-amd64.exe is not one"},
		"misnamed binary": {mutate(func(m map[string]string) {
			m["mash-linux-x86_64"] = m["mash-linux-amd64"]
			delete(m, "mash-linux-amd64")
		}), "mash-linux-amd64 is missing"},
		"sums without script": {mutate(func(m map[string]string) {
			m[SumsName] = "sha256:" + sha([]byte(goodSums()[:4*(64+2)+4*len("mash-linux-amd64")]))
		}), "SHA256SUMS has digest"},
		"missing sbom":        {mutate(func(m map[string]string) { delete(m, "mash-linux-arm64.cdx.json") }), "mash-linux-arm64.cdx.json is missing"},
		"missing provenance":  {mutate(func(m map[string]string) { delete(m, ProvenanceName) }), "mash.intoto.jsonl is missing"},
		"sbom with no digest": {mutate(func(m map[string]string) { m["mash-linux-arm64.cdx.json"] = "x" }), "not a sha256"},
		"sums not covering the attached sbom": {mutate(func(m map[string]string) {
			m["mash-linux-arm64.cdx.json"] = "sha256:" + strings.Repeat("e", 64)
		}), "SHA256SUMS has digest"},
		"bad sums":            {mutate(func(m map[string]string) { m[SumsName] = "sha256:" + strings.Repeat("1", 64) }), "SHA256SUMS has digest"},
		"digest without algo": {mutate(func(m map[string]string) { m["mash-linux-amd64"] = hashes()[Target{"linux", "amd64"}] }), "mash-linux-amd64 has digest"},
	}
	for name, c := range cases {
		if err := CheckAssets(c.have, hashes(), scriptHash); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
	// An unusable build hash is a problem of its own, not a silent pass.
	h := hashes()
	h[Target{"linux", "amd64"}] = ""
	if err := CheckAssets(goodAssets(), h, scriptHash); err == nil || !strings.Contains(err.Error(), "no usable build hash") {
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
	rs := runs("test (ubuntu-latest)", "test (macos-latest)")
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
