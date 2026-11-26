package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/scripts/upgradeproof/receipt"
)

func TestPageStepsAndOrder(t *testing.T) {
	page := "# Compatibility\n\n## Upgrading from 0.5.10\n\n1. Run `mash version` and `mash doctor`.\n2. Install both programs.\n3. Update the plugin.\n4. Restart each session.\n5. Run `mash doctor` again.\n\n## Other\n\n1. not this\n"
	steps := pageSteps(page, "0.5.10")
	if len(steps) != 5 {
		t.Fatalf("steps = %q", steps)
	}
	order, unknown := orderFrom(steps)
	if len(unknown) != 0 || !reflect.DeepEqual(order, fallbackOrder) {
		t.Fatalf("order %v unknown %v", order, unknown)
	}
	if got := pageSteps("# Upgrading from 0.5.10\n\nSome prose, no list.\n", "0.5.10"); len(got) != 0 {
		t.Fatalf("prose is not steps: %q", got)
	}
}

func TestParseHook(t *testing.T) {
	out := `{"hookSpecificOutput":{"additionalContext":"--- #7 from acme.demo.bob [verified] at 2026-11-26T10:00:00Z\ninstance:x\n> hello [tok:d01]\n--- #9 from acme.demo.bob [verified] at t\n> two [tok:p01]","hookEventName":"PostToolUse"}}`
	got := parseHook(out)
	want := []shown{{7, "acme.demo.bob", "d01"}, {9, "acme.demo.bob", "p01"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if len(parseHook("")) != 0 {
		t.Fatal("empty output shows nothing")
	}
}

func TestRowsFrom(t *testing.T) {
	rows, err := rowsFrom(`{"address":"a","total":3,"messages":[{"number":1,"from":"acme.demo.bob","trust":"verified"},{"number":2,"trust":"refused","reason":"time not in UTC"},{"number":3,"from":"x","trust":"forged"}]}`)
	if err != nil || len(rows) != 3 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0].Refused || !rows[1].Refused || rows[1].Reason != "time not in UTC" || rows[2].Refused {
		t.Fatalf("%+v", rows)
	}
	if _, err := rowsFrom("no json here"); err == nil {
		t.Fatal("text is not a listing")
	}
}

func TestSumsLine(t *testing.T) {
	h := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if d, ok := sumsLine(h+"  mash-linux-amd64\n"+h+" *x\n", "x"); !ok || d != h {
		t.Fatal("binary-mode line")
	}
	if _, ok := sumsLine(h+"  mash-linux-amd64\n", "mashctl-linux-amd64"); ok {
		t.Fatal("other name")
	}
}

func TestRealUpgradePageSection(t *testing.T) {
	b, err := os.ReadFile("testdata/compat-060.md")
	if err != nil {
		t.Fatal(err)
	}
	steps := pageSteps(string(b), "0.5.10")
	if len(steps) != 5 {
		t.Fatalf("want the 5 steps of the 0.5.10 section only, got %d: %q", len(steps), steps)
	}
	order, unknown := orderFrom(steps)
	if p := orderProblem(order, unknown); p != "" {
		t.Fatalf("the real page fails: %s (order %v)", p, order)
	}
	if got := pageSteps(string(b), "0.5.8"); len(got) != 0 {
		t.Fatalf("the 0.5.8 section is prose, got steps %q", got)
	}
	if got := pageSteps(string(b), "0.5.1"); len(got) != 0 {
		t.Fatalf("0.5.1 is not 0.5.10: %q", got)
	}
}

func TestOrderProblem(t *testing.T) {
	short := "## Upgrading from 0.5.10\n\n1. Install both programs.\n2. Restart each session.\n"
	order, unknown := orderFrom(pageSteps(short, "0.5.10"))
	p := orderProblem(order, unknown)
	for _, want := range []string{actCheck, actPlugins, actAfter} {
		if !strings.Contains(p, want) {
			t.Errorf("problem %q does not name the missing %q", p, want)
		}
	}
	if strings.Contains(p, "missing: "+actInstall) || strings.Contains(p, actRestart+";") {
		t.Errorf("problem %q names actions that are present", p)
	}
	if p := orderProblem(fallbackOrder, nil); p != "" {
		t.Errorf("the full order is refused: %s", p)
	}
	if p := orderProblem([]string{actCheck, actPlugins, actInstall, actRestart, actAfter}, nil); !strings.Contains(p, "out of order") {
		t.Errorf("out of order accepted: %q", p)
	}
	if p := orderProblem(fallbackOrder, []string{"reboot the machine"}); p == "" {
		t.Error("an unknown step was accepted")
	}
}

func goodReceipt() receipt.Receipt {
	commit := strings.Repeat("ab", 20)
	r := receipt.Receipt{Kind: receipt.KindAT011, Commit: commit, Result: receipt.ResultPassed, Environment: "venue",
		InstallSHA256: sumBytes([]byte("install.sh")), SumsSHA256: sumBytes([]byte("SHA256SUMS")),
		PrevVersion: "0.5.10", UpgradePage: "0123456789abcdef0123456789abcdef01234567"}
	for _, p := range []string{"mash", "mashctl"} {
		r.Executables = append(r.Executables, receipt.Exec{Name: p, GOOS: "linux", GOARCH: "amd64", SHA256: sumBytes([]byte("new " + p)),
			Build: receipt.BuildConfig{Toolchain: release.Toolchain, Tags: []string{}, Flags: release.BuildFlags("0.6.0", commit)}})
	}
	for _, b := range release.Binaries() {
		r.Prior = append(r.Prior, receipt.Prior{Name: b.Program, GOOS: b.GOOS, GOARCH: b.GOARCH, SHA256: sumBytes([]byte("old " + b.AssetName()))})
	}
	return r
}

func TestFinalizeReceipt(t *testing.T) {
	rec := goodReceipt()
	if ps := rec.Problems(); len(ps) > 0 {
		t.Fatalf("the fixture is not well formed: %v", ps)
	}
	got := finalizeReceipt(rec, "the container was removed", true)
	if got.Result != receipt.ResultPassed || !strings.HasSuffix(got.Environment, "; the container was removed") {
		t.Fatalf("a destroyed environment keeps a passed receipt passed: %+v", got)
	}
	got = finalizeReceipt(rec, "the container STILL EXISTS", false)
	if got.Result != receipt.ResultFailed || got.ExitStatus != 1 || !strings.Contains(got.Environment, "THE ENVIRONMENT STILL EXISTS") {
		t.Fatalf("a surviving environment must fail the receipt: %+v", got)
	}
	rec.Commit = "short"
	if got := finalizeReceipt(rec, "gone", true); got.Result != receipt.ResultFailed {
		t.Fatalf("a malformed receipt must fail: %+v", got)
	}
}

func TestSumsDigestOK(t *testing.T) {
	if !sumsDigestOK(strings.Repeat("ab", 32)) || sumsDigestOK("") || sumsDigestOK(strings.Repeat("zz", 32)) || sumsDigestOK("ab") {
		t.Fatal("digest check")
	}
}

func TestDropFromPath(t *testing.T) {
	withGH, other, withGH2 := t.TempDir(), t.TempDir(), t.TempDir()
	for _, f := range []struct {
		dir  string
		mode os.FileMode
	}{{withGH, 0o755}, {withGH2, 0o755}, {other, 0o644}} {
		if err := os.WriteFile(f.dir+"/gh", []byte("#!/bin/sh\n"), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	sep := string(os.PathListSeparator)
	in := strings.Join([]string{"/a", withGH, other, "/b", withGH2}, sep)
	got, dropped := dropFromPath(in, "gh")
	if want := strings.Join([]string{"/a", other, "/b"}, sep); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(dropped, []string{withGH, withGH2}) {
		t.Fatalf("dropped = %q", dropped)
	}
	if onPath(got, "gh") {
		t.Fatal("gh still on the path")
	}
}
