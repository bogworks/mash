package release

import (
	"strings"
	"testing"
)

var candidate = strings.Repeat("a", 40)

func runs(names ...string) []CheckRun {
	var out []CheckRun
	for i, n := range names {
		out = append(out, CheckRun{ID: int64(i + 1), HeadSHA: candidate, Name: n, Status: "completed", Conclusion: "success"})
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
		if err := CheckCI(append(rs, bad), candidate); err == nil || !strings.Contains(err.Error(), "reproducible") {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestCheckCI_aRerunSupersedesAnEarlierFailureButNotALaterOne(t *testing.T) {
	rs := append(runs(RequiredChecks...), CheckRun{ID: 0, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "failure"})
	if err := CheckCI(rs, candidate); err != nil {
		t.Fatalf("the latest run of a check decides: %v", err)
	}
	rs = append(runs(RequiredChecks...), CheckRun{ID: 50, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "failure"})
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
	mixed := append(runs(RequiredChecks...), CheckRun{ID: 77, HeadSHA: other, Name: "reproducible", Status: "completed", Conclusion: "success"})
	if err := CheckCI(mixed, candidate); err == nil {
		t.Fatal("a run for another commit was ignored")
	}
}

func TestCheckCI_conflictingDuplicatesOfOneIDAreRefusedWhateverTheOrder(t *testing.T) {
	good := CheckRun{ID: 5, HeadSHA: candidate, Name: "reproducible", Status: "completed", Conclusion: "success"}
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

func goodAssets() map[string]string {
	h := hashes()
	have := map[string]string{SumsName: "sha256:" + sha([]byte(ExpectedSums(h)))}
	for _, t := range Targets {
		have[t.AssetName()] = "sha256:" + h[t]
	}
	return have
}

func TestExpectedSums_isSha256sumOutput(t *testing.T) {
	got := ExpectedSums(hashes())
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	want := []string{"mash-darwin-amd64", "mash-darwin-arm64", "mash-linux-amd64", "mash-linux-arm64"}
	if len(lines) != 4 {
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
	if err := CheckAssets(goodAssets(), hashes()); err != nil {
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
		"bad sums":            {mutate(func(m map[string]string) { m[SumsName] = "sha256:" + strings.Repeat("1", 64) }), "SHA256SUMS has digest"},
		"digest without algo": {mutate(func(m map[string]string) { m["mash-linux-amd64"] = hashes()[Target{"linux", "amd64"}] }), "mash-linux-amd64 has digest"},
	}
	for name, c := range cases {
		if err := CheckAssets(c.have, hashes()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
	// An unusable build hash is a problem of its own, not a silent pass.
	h := hashes()
	h[Target{"linux", "amd64"}] = ""
	if err := CheckAssets(goodAssets(), h); err == nil || !strings.Contains(err.Error(), "no usable build hash") {
		t.Fatalf("%v", err)
	}
}
