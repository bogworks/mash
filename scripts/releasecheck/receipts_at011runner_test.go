package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// The AT-011 runner (scripts/upgradeproof) cannot import this package, so its receipt type repeats the gate's.
// testdata/at011-runner-golden.json is a receipt that runner's receipt package wrote (its TestGolden keeps the file
// current); this test holds it to the gate's own check against a matching Expected.

func runnerGoldenExpected(t *testing.T) (Expected, string) {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join("testdata", "at011-runner-golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "at-011-linux-amd64.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := loadReceipts(dir) // refuses unknown fields: the runner may not write one the gate does not know
	if err != nil || len(rs) != 1 {
		t.Fatalf("gate cannot read the runner's receipt: %v (%d receipts)", err, len(rs))
	}
	g := rs[0]
	p := Platform{"linux", "amd64"}
	exp := Expected{Commit: g.Commit, Platforms: []Platform{p}, InstallSHA: g.InstallSHA256, SumsSHA: g.SumsSHA256,
		PrevVersion: "0.5.10", Prior: g.Prior, UpgradePage: g.UpgradePage}
	for _, n := range executableNames {
		e, ok := findExec(g.Executables, n, p)
		if !ok {
			t.Fatalf("golden receipt lacks %s", n)
		}
		// The build configuration is the gate's own, not copied from the receipt.
		e.Build = BuildConfig{Toolchain: release.Toolchain, Tags: []string{}, Flags: release.BuildFlags("0.6.0", g.Commit)}
		exp.Executables = append(exp.Executables, e)
	}
	return exp, dir
}

func TestRunnerReceiptAccepted(t *testing.T) {
	exp, dir := runnerGoldenExpected(t)
	rs, err := loadReceipts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ps := checkReceipts(exp, rs, lineAT011); len(ps) > 0 {
		t.Fatalf("gate refuses the runner's receipt: %v", ps)
	}
}

func TestRunnerReceiptRefusedWhenFailedOrDrifting(t *testing.T) {
	exp, dir := runnerGoldenExpected(t)
	rs, _ := loadReceipts(dir)
	for name, mut := range map[string]func(*Receipt){
		"failed result":   func(r *Receipt) { r.Result = resultFailed },
		"other commit":    func(r *Receipt) { r.Commit = strings.Repeat("cd", 20) },
		"other sums":      func(r *Receipt) { r.SumsSHA256 = rsha("other") },
		"other page":      func(r *Receipt) { r.UpgradePage = "blob" },
		"other binary":    func(r *Receipt) { r.Executables[0].SHA256 = rsha("other") },
		"missing prior":   func(r *Receipt) { r.Prior = r.Prior[1:] },
		"other toolchain": func(r *Receipt) { r.Executables[1].Build.Toolchain = "go1.0" },
	} {
		c := rs[0]
		c.Executables = append([]Exec(nil), c.Executables...)
		mut(&c)
		if ps := checkReceipts(exp, []Receipt{c}, lineAT011); len(ps) == 0 {
			t.Errorf("%s: gate accepted it", name)
		}
	}
}

func TestRunnerReceiptFieldsMatchGate(t *testing.T) {
	_, dir := runnerGoldenExpected(t)
	b, _ := os.ReadFile(filepath.Join(dir, "at-011-linux-amd64.json"))
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["kind"]) != `"`+kindAT011+`"` || string(m["result"]) != `"`+resultPassed+`"` {
		t.Fatalf("kind/result not the gate's strings: %s %s", m["kind"], m["result"])
	}
}
