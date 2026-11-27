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
	if len(unknown) != 0 || !reflect.DeepEqual(order, upgradeActions) {
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
	if p := orderProblem(upgradeActions, nil); p != "" {
		t.Errorf("the full order is refused: %s", p)
	}
	if p := orderProblem([]string{actCheck, actPlugins, actInstall, actRestart, actAfter}, nil); !strings.Contains(p, "out of order") {
		t.Errorf("out of order accepted: %q", p)
	}
	if p := orderProblem(upgradeActions, []string{"reboot the machine"}); p == "" {
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

func TestWhoOnline(t *testing.T) {
	cases := []struct {
		name, out string
		want      []string
		bad       []string
		notJSON   bool
	}{
		{"both online", `{"endpoints":[{"address":"o.g.a","online":true},{"address":"o.g.b","online":true}]}`, []string{"o.g.a", "o.g.b"}, nil, false},
		{"one offline", `{"endpoints":[{"address":"o.g.a","online":true},{"address":"o.g.b","online":false}]}`, []string{"o.g.a", "o.g.b"}, []string{"o.g.b is offline"}, false},
		{"one missing", `{"endpoints":[{"address":"o.g.a","online":true}]}`, []string{"o.g.a", "o.g.b"}, []string{"o.g.b is not listed"}, false},
		{"bad json", `not json`, []string{"o.g.a"}, nil, true},
		{"extra fields ignored", `{"self":"o.g.a","count":2,"endpoints":[{"address":"o.g.a","online":true,"seen":"now"},{"address":"o.g.b","online":true}]}`, []string{"o.g.a", "o.g.b"}, nil, false},
	}
	for _, c := range cases {
		err := whoOnline(c.out, c.want...)
		switch {
		case c.notJSON:
			if err == nil || !strings.Contains(err.Error(), "not JSON") {
				t.Errorf("%s: err = %v", c.name, err)
			}
		case len(c.bad) == 0:
			if err != nil {
				t.Errorf("%s: err = %v", c.name, err)
			}
		default:
			if err == nil {
				t.Errorf("%s: want error", c.name)
				continue
			}
			for _, b := range c.bad {
				if !strings.Contains(err.Error(), b) {
					t.Errorf("%s: err %q lacks %q", c.name, err, b)
				}
			}
		}
	}
}

func TestVersionsProblem(t *testing.T) {
	for _, c := range []struct{ version, prev string }{
		{"0.6.1", ""}, {"", "0.6.0"}, {"0.6.1", "v0.6.0"}, {"v0.6.1", "0.6.0"}, {"0.6.1", "0.6"},
		{"0.6.1", "abc"}, {"0.6.1", "0.6.0-rc1"}, {"0.6.1", "0.6.0\n"}, {"0.6.0", "0.6.0"},
	} {
		if p := versionsProblem(c.version, c.prev); p == "" {
			t.Errorf("version %q prev %q passed validation", c.version, c.prev)
		}
	}
	for _, c := range []struct{ version, prev string }{{"0.6.1", "0.6.0"}, {"0.6.0", "0.5.10"}, {"1.10.0", "1.9.12"}} {
		if p := versionsProblem(c.version, c.prev); p != "" {
			t.Errorf("version %q prev %q refused: %s", c.version, c.prev, p)
		}
	}
}

func TestRunFlags(t *testing.T) {
	sha := strings.Repeat("a", 40)
	base := []string{"run", "-commit", sha, "-out", "/nonexistent/out", "-pack", "p", "-release-repo", "o/n", "-work", "/nonexistent/work"}
	for name, extra := range map[string][]string{
		"no prev":     {"-version", "0.6.1"},
		"v prefix":    {"-version", "0.6.1", "-prev", "v0.6.0"},
		"short":       {"-version", "0.6.1", "-prev", "0.6"},
		"word":        {"-version", "0.6.1", "-prev", "abc"},
		"same":        {"-version", "0.6.0", "-prev", "0.6.0"},
		"no version":  {"-prev", "0.6.0"},
		"bad version": {"-version", "v0.6.1", "-prev", "0.6.0"},
	} {
		if code := realMain(append(append([]string{}, base...), extra...)); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestNotesStepsReal(t *testing.T) {
	b, err := os.ReadFile("../../../docs/release-notes/0.6.0.md")
	if err != nil {
		t.Fatal(err)
	}
	steps := notesSteps(string(b))
	order, unknown := orderFrom(steps)
	if p := orderProblem(order, unknown); p != "" || !reflect.DeepEqual(order, upgradeActions) {
		t.Fatalf("0.6.0 notes: order %v, problem %q, steps %q", order, p, steps)
	}
}

func TestNotesStepsSynthetic(t *testing.T) {
	good := "### Summary\n\n1. not this\n\n### Upgrading\n\nSome words.\n\n1. Check first: `mash doctor`.\n2. Install both programs.\n3. Update the plugins.\n4. Restart each session.\n5. Check again.\n\n#### Detail\n\n6. still inside\n\n### Next\n\n1. not this\n"
	if steps := notesSteps(good); len(steps) != 6 {
		t.Fatalf("steps %q", steps)
	}
	if got := notesSteps("### upgrading from x\n1. Install both programs.\n2. Restart each session.\n"); len(got) != 2 {
		t.Fatalf("case-insensitive heading: %q", got)
	}
	order, unknown := orderFrom(notesSteps("### Upgrading\n\n1. Install both programs.\n2. Restart each session.\n"))
	if p := orderProblem(order, unknown); !strings.Contains(p, "missing") {
		t.Errorf("missing steps accepted: %q", p)
	}
	order, unknown = orderFrom(notesSteps("### Upgrading\n\n1. Check first.\n2. Update the plugins.\n3. Install both programs.\n4. Restart each session.\n5. Check again.\n"))
	if p := orderProblem(order, unknown); !strings.Contains(p, "out of order") {
		t.Errorf("out of order accepted: %q", p)
	}
	if got := notesSteps("### Summary\n\n1. Install both programs.\n"); len(got) != 0 {
		t.Errorf("steps without an Upgrading section: %q", got)
	}
}

func TestPageStepsPicksPrevSection(t *testing.T) {
	page := "# Compatibility\n\n## Upgrading from 0.5.10 to 0.6.0\n\n1. Old check.\n2. Old install.\n\n## Upgrading from 0.6.0 to 0.6.1\n\n1. Check first.\n2. Install both programs.\n3. Update the plugins.\n4. Restart each session.\n5. Check again.\n\n## Other\n\n1. not this\n"
	if got := pageSteps(page, "0.6.0"); len(got) != 5 || got[0] != "Check first." {
		t.Fatalf("0.6.0 section: %q", got)
	}
	if got := pageSteps(page, "0.5.10"); len(got) != 2 || got[0] != "Old check." {
		t.Fatalf("0.5.10 section: %q", got)
	}
}

func TestPageSectionPicksPrevOnly(t *testing.T) {
	page := "# Compatibility\n\n## Upgrading from 0.6.0 to 0.6.1\n\n1. Check first.\n2. Install both programs.\n\n" +
		"## Upgrading from 0.5.10 to 0.6.0\n\nAn open session keeps its old version until it restarts.\n\n" +
		"This upgrade is one-way: there is no way back.\n\n## Other\n\nText.\n"
	sect := pageSection(page, "0.6.0")
	if runningRE.MatchString(sect) || oneWayRE.MatchString(sect) {
		t.Fatalf("the 0.6.0 section borrowed text from the 0.5.10 section: %q", sect)
	}
	// The old whole-page match would have passed wrongly.
	if !runningRE.MatchString(page) || !oneWayRE.MatchString(page) {
		t.Fatal("the synthetic page must satisfy both patterns as a whole")
	}
	sect = pageSection(page, "0.5.10")
	if !runningRE.MatchString(sect) || !oneWayRE.MatchString(sect) {
		t.Fatalf("the 0.5.10 section lacks its text: %q", sect)
	}
	if got := pageSection(page, "0.4.0"); got != "" {
		t.Fatalf("an absent section is empty: %q", got)
	}
}

func TestPageSectionReal(t *testing.T) {
	b, err := os.ReadFile("../../../docs/compatibility.md")
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if s := pageSection(page, "0.6.0"); !runningRE.MatchString(s) {
		t.Errorf("the 0.6.0 section lacks the open-session line: %q", s)
	}
	s := pageSection(page, "0.5.10")
	if !runningRE.MatchString(s) || !oneWayRE.MatchString(s) {
		t.Errorf("the 0.5.10 section lacks the open-session or one-way line: %q", s)
	}
}
