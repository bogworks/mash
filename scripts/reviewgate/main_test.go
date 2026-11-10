package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testPolicy mirrors docs/review/policy.json in shape, with one extra tier to test ties and exclusion.
const testPolicy = `{
 "min_complete": 1, "default_tier": "standard",
 "tiers": [
  {"name":"sec","block":["blocker","high","medium"],"round_cap":6,"paths":["sec/**","!sec/docs/**"]},
  {"name":"docs","block":["blocker"],"round_cap":2,"paths":["**/*.md","**/*_test.go"]},
  {"name":"standard","block":["blocker","high"],"round_cap":4,"paths":["src/**"]},
  {"name":"twin","block":["blocker","high"],"round_cap":9,"paths":["twin/**"]}
 ],
 "models": []}`

type cm struct {
	path, sev, cat, content, fix string
	start, end                   int
}

func ocrJSON(status, provider, model string, cs ...cm) string {
	var parts []string
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf(`{"path":%q,"content":%q,"suggestion_code":%q,"existing_code":"","start_line":%d,"end_line":%d,"category":%q,"severity":%q}`,
			c.path, c.content, c.fix, c.start, c.end, c.cat, c.sev))
	}
	return fmt.Sprintf(`{"status":%q,"llm":{"provider":%q,"model":%q},"message":"","summary":{},"comments":[%s]}`,
		status, provider, model, strings.Join(parts, ","))
}

type env struct {
	t      *testing.T
	dir    string
	policy string
}

func newEnv(t *testing.T) *env {
	dir := t.TempDir()
	return &env{t: t, dir: dir, policy: writeFile(t, filepath.Join(dir, "policy.json"), testPolicy)}
}

// verdict runs the verdict subcommand over the changed files and OCR bodies, returning exit, stdout and stderr.
func (e *env) verdict(round int, changed []string, ocrs ...string) (int, string, string) {
	e.t.Helper()
	args := []string{"verdict", "-policy", e.policy, "-changed", writeFile(e.t, filepath.Join(e.dir, "changed.txt"), strings.Join(changed, "\n")), "-round", fmt.Sprint(round)}
	for i, o := range ocrs {
		args = append(args, "-ocr", writeFile(e.t, filepath.Join(e.dir, fmt.Sprintf("ocr%d.json", i)), o))
	}
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVerdictMergeTwoModels(t *testing.T) {
	e := newEnv(t)
	a := ocrJSON("complete", "p1", "m1",
		cm{"src/a.go", "medium", "bug", "m1 text", "", 10, 12},
		cm{"src/a.go", "low", "style", "nit", "", 50, 50})
	b := ocrJSON("complete", "p2", "m2",
		cm{"src/a.go", "high", "bug", "m2 text", "x := 1\ny := 2", 14, 15},
		cm{"src/b.go", "info", "style", "fyi", "", 3, 3})
	code, out, _ := e.verdict(1, []string{"src/a.go", "src/b.go"}, a, b)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	for _, want := range []string{
		"tier: standard (blocks blocker, high, round cap 4), because src/a.go matches",
		"models: p1/m1 ok", "models: p2/m2 ok",
		"H1 high src/a.go:10-15 [bug] (2 of 2 models: p1/m1, p2/m2)",
		"    m2 text", "    fix:", "      x := 1",
		"also (not blocking): L1 src/a.go:50, L2 src/b.go:3",
		"BLOCKED: 1 blocking finding(s); fix them and rerun (round 1 of 4)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "m1 text") {
		t.Errorf("the merged finding must carry the highest-severity text only:\n%s", out)
	}
}

func TestMergeRules(t *testing.T) {
	run1 := ocrRun{label: "a", ok: true, comments: []comment{
		{Path: "f.go", StartLine: 10, EndLine: 10, Category: "bug", Severity: "low", Content: "low"},
		{Path: "f.go", StartLine: 100, EndLine: 100, Category: "bug", Severity: "low"},
	}}
	run2 := ocrRun{label: "b", ok: true, comments: []comment{
		{Path: "f.go", StartLine: 13, EndLine: 13, Category: "bug", Severity: "medium", Content: "med"},   // within 3: same
		{Path: "f.go", StartLine: 14, EndLine: 14, Category: "bug", Severity: "high"},                     // 4 past the merged end... start 14 > 13+3? no, 14<=16: same
		{Path: "f.go", StartLine: 100, EndLine: 100, Category: "perf", Severity: "low"},                   // other category: distinct
		{Path: "g.go", StartLine: 100, EndLine: 100, Category: "bug", Severity: "low"},                    // other path: distinct
		{Path: "f.go", StartLine: 200, EndLine: 0, Category: "bug", Severity: "critical", Content: "end"}, // missing end_line
	}}
	got := merge([]ocrRun{run1, run2, {label: "dead", ok: false}})
	var ids []string
	for _, f := range got {
		ids = append(ids, f.id+"="+f.where()+"/"+fmt.Sprint(len(f.models)))
	}
	want := "B1=f.go:200/1 H1=f.go:10-14/2 L1=f.go:100/1 L2=f.go:100/1 L3=g.go:100/1"
	if strings.Join(ids, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(ids, " "), want)
	}
	// Four lines apart is not the same finding.
	far := merge([]ocrRun{{label: "a", ok: true, comments: []comment{
		{Path: "f.go", StartLine: 1, EndLine: 1, Category: "bug"}, {Path: "f.go", StartLine: 5, EndLine: 5, Category: "bug"}}}})
	if len(far) != 2 {
		t.Errorf("lines 1 and 5 merged: %d findings", len(far))
	}
	// One model repeating itself counts once in "k of n".
	same := merge([]ocrRun{{label: "a", ok: true, comments: []comment{
		{Path: "f.go", StartLine: 1, EndLine: 1, Category: "bug"}, {Path: "f.go", StartLine: 2, EndLine: 2, Category: "bug"}}}})
	if len(same) != 1 || len(same[0].models) != 1 {
		t.Errorf("same-model duplicates: %+v", same)
	}
}

func TestSeverityMapping(t *testing.T) {
	for in, want := range map[string]int{
		"blocker": sevBlocker, "critical": sevBlocker, "CRITICAL": sevBlocker, "high": sevHigh, "High": sevHigh,
		"medium": sevMedium, "low": sevLow, "info": sevLow, "": sevLow, "weird": sevLow,
	} {
		if got := severityRank(in); got != want {
			t.Errorf("severityRank(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTierChoice(t *testing.T) {
	e := newEnv(t)
	p, err := loadPolicy(e.policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		changed []string
		want    string
		file    string
	}{
		{"default when nothing matches", []string{"other/x.txt"}, "standard", "other/x.txt"},
		{"no files is the default", nil, "standard", ""},
		{"lone doc", []string{"x.md"}, "docs", "x.md"},
		{"strictest of several files", []string{"x.md", "src/a.go", "sec/k.go"}, "sec", "sec/k.go"},
		{"one file in two tiers, strictest wins", []string{"sec/k_test.go"}, "sec", "sec/k_test.go"},
		{"exclusion drops the file from its tier", []string{"sec/docs/a.txt"}, "standard", "sec/docs/a.txt"},
		{"exclusion leaves the other tier", []string{"sec/docs/a.md"}, "docs", "sec/docs/a.md"},
		{"tie goes to the first in policy order", []string{"twin/a", "src/b"}, "standard", "src/b"},
		{"tie, other order of files", []string{"src/b", "twin/a"}, "standard", "src/b"},
	} {
		got, _ := chooseTier(p, tc.changed)
		if got.t.Name != tc.want || got.file != tc.file {
			t.Errorf("%s: got %s (%s), want %s (%s)", tc.name, got.t.Name, got.file, tc.want, tc.file)
		}
	}
	// The header names a file for each tier that mattered.
	code, out, _ := e.verdict(1, []string{"x.md", "sec/k.go"}, ocrJSON("complete", "p", "m"))
	if code != 0 || !strings.Contains(out, "tier: sec (blocks blocker, high, medium, round cap 6), because sec/k.go matches") ||
		!strings.Contains(out, "also in play, less strict: docs (x.md)") {
		t.Errorf("exit %d\n%s", code, out)
	}
	_, out, _ = e.verdict(1, []string{"other/x"}, ocrJSON("complete", "p", "m"))
	if !strings.Contains(out, "because other/x matches no tier, so the default") {
		t.Error(out)
	}
}

func TestTierBlocksSeverities(t *testing.T) {
	e := newEnv(t)
	med := ocrJSON("complete", "p", "m", cm{"sec/a.go", "medium", "bug", "m", "", 1, 1})
	if code, out, _ := e.verdict(1, []string{"sec/a.go"}, med); code != 1 {
		t.Errorf("medium in sec: exit %d\n%s", code, out)
	}
	if code, out, _ := e.verdict(1, []string{"src/a.go"}, ocrJSON("complete", "p", "m", cm{"src/a.go", "medium", "bug", "m", "", 1, 1})); code != 0 || !strings.Contains(out, "also (not blocking): M1 src/a.go:1") || !strings.Contains(out, "PASS") {
		t.Errorf("medium in standard: exit %d\n%s", code, out)
	}
	if code, _, _ := e.verdict(1, []string{"x.md"}, ocrJSON("complete", "p", "m", cm{"x.md", "high", "bug", "m", "", 1, 1})); code != 0 {
		t.Errorf("high in docs must not block: exit %d", code)
	}
	if code, _, _ := e.verdict(1, []string{"x.md"}, ocrJSON("complete", "p", "m", cm{"x.md", "critical", "bug", "m", "", 1, 1})); code != 1 {
		t.Errorf("critical in docs must block: exit %d", code)
	}
}

func TestFailedRunsAndMinComplete(t *testing.T) {
	e := newEnv(t)
	good := ocrJSON("complete", "p1", "m1", cm{"src/a.go", "high", "bug", "m", "", 1, 1})
	bad := ocrJSON("error", "p2", "m2")
	code, out, errs := e.verdict(1, []string{"src/a.go"}, good, bad)
	if code != 1 || !strings.Contains(out, `models: p2/m2 FAILED: status "error", not complete`) || !strings.Contains(out, "(1 of 1 models: p1/m1)") {
		t.Errorf("one failed of two (min_complete 1): exit %d\n%s%s", code, out, errs)
	}
	code, out, errs = e.verdict(1, []string{"src/a.go"}, bad, "{not json")
	if code != 2 || !strings.Contains(out, "FAILED: unreadable JSON") || !strings.Contains(errs, "failing closed") || !strings.Contains(errs, "p2/m2") {
		t.Errorf("all failed: exit %d\n%s%s", code, out, errs)
	}
	if code, _, _ = e.verdict(1, []string{"src/a.go"}); code != 2 {
		t.Errorf("no runs at all must fail closed: exit %d", code)
	}
	// min_complete 2 with one good run fails closed.
	e.policy = writeFile(t, e.policy, strings.Replace(testPolicy, `"min_complete": 1`, `"min_complete": 2`, 1))
	if code, _, errs = e.verdict(1, []string{"src/a.go"}, good, bad); code != 2 || !strings.Contains(errs, "1 complete run(s), need 2") {
		t.Errorf("min_complete 2: exit %d %s", code, errs)
	}
	// A missing file is a failed run, not a crash.
	var o, er bytes.Buffer
	if code := run([]string{"verdict", "-policy", e.policy, "-changed", writeFile(t, filepath.Join(e.dir, "c"), "a"), "-round", "1", "-ocr", filepath.Join(e.dir, "nope.json")}, &o, &er); code != 2 || !strings.Contains(o.String(), "FAILED: unreadable") {
		t.Errorf("missing ocr file: exit %d\n%s", code, o.String())
	}
}

func TestRoundCap(t *testing.T) {
	e := newEnv(t)
	hi := ocrJSON("complete", "p", "m", cm{"x.md", "blocker", "bug", "m", "", 1, 1})
	// docs tier has cap 2; a high does not block there, so use standard (cap 4) for the cap case.
	high := ocrJSON("complete", "p", "m", cm{"src/a.go", "high", "bug", "m", "", 1, 1})
	if code, out, _ := e.verdict(3, []string{"src/a.go"}, high); code != 1 || strings.Contains(out, "ROUND CAP") {
		t.Errorf("round 3 of 4: exit %d\n%s", code, out)
	}
	code, out, _ := e.verdict(4, []string{"src/a.go"}, high)
	if code != 3 || !strings.Contains(out, "ROUND CAP: round 4 reached the cap 4 with 1 blocking finding(s) left: fix only correctness on the supported path") {
		t.Errorf("round 4 of 4: exit %d\n%s", code, out)
	}
	if code, _, _ = e.verdict(9, []string{"src/a.go"}, high); code != 3 {
		t.Errorf("past the cap: exit %d", code)
	}
	// A blocker is never deferred: exit 1 even at the cap.
	code, out, _ = e.verdict(2, []string{"x.md"}, hi)
	if code != 1 || strings.Contains(out, "ROUND CAP") || !strings.Contains(out, "never deferred") || !strings.Contains(out, "(round 2 of 2)") {
		t.Errorf("blocker at cap: exit %d\n%s", code, out)
	}
	// A blocker alongside non-blocker findings at the cap is still a 1.
	both := ocrJSON("complete", "p", "m", cm{"src/a.go", "blocker", "bug", "m", "", 1, 1}, cm{"src/a.go", "high", "perf", "m", "", 90, 90})
	if code, _, _ = e.verdict(4, []string{"src/a.go"}, both); code != 1 {
		t.Errorf("blocker plus high at cap: exit %d", code)
	}
	// A pass at the cap is still a pass.
	if code, _, _ = e.verdict(4, []string{"src/a.go"}, ocrJSON("complete", "p", "m")); code != 0 {
		t.Errorf("clean at cap: exit %d", code)
	}
}

func TestVerdictUsage(t *testing.T) {
	e := newEnv(t)
	changed := writeFile(t, filepath.Join(e.dir, "c"), "a")
	for name, args := range map[string][]string{
		"no args":        {},
		"unknown":        {"bogus"},
		"no round":       {"verdict", "-policy", e.policy, "-changed", changed},
		"round zero":     {"verdict", "-policy", e.policy, "-changed", changed, "-round", "0"},
		"no policy":      {"verdict", "-changed", changed, "-round", "1"},
		"missing policy": {"verdict", "-policy", filepath.Join(e.dir, "nope"), "-changed", changed, "-round", "1"},
		"missing change": {"verdict", "-policy", e.policy, "-changed", filepath.Join(e.dir, "nope"), "-round", "1"},
		"bad flag":       {"verdict", "-nope"},
	} {
		var o, er bytes.Buffer
		if code := run(args, &o, &er); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	for name, body := range map[string]string{
		"bad json":              `{`,
		"bad default":           `{"default_tier":"x","tiers":[{"name":"a","round_cap":1}]}`,
		"no cap":                `{"default_tier":"a","tiers":[{"name":"a"}]}`,
		"bad severity":          `{"default_tier":"a","tiers":[{"name":"a","round_cap":1,"block":["nope"]}]}`,
		"duplicate tier":        `{"default_tier":"a","tiers":[{"name":"a","round_cap":1},{"name":"a","round_cap":1}]}`,
		"unnamed tier":          `{"default_tier":"a","tiers":[{"round_cap":1}]}`,
		"question empty text":   `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}],"questions":[{"id":"q","severity":"high","text":" "}]}`,
		"question empty id":     `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}],"questions":[{"severity":"high","text":"t"}]}`,
		"question duplicate id": `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}],"questions":[{"id":"q","severity":"high","text":"t"},{"id":"q","severity":"low","text":"u"}]}`,
		"question bad severity": `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}],"questions":[{"id":"q","severity":"nope","text":"t"}]}`,
		"trailing junk":         `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}]}x`,
	} {
		if _, err := loadPolicy(writeFile(t, filepath.Join(e.dir, "bad.json"), body)); err == nil {
			t.Errorf("%s: policy accepted", name)
		}
	}
}

func TestTitleEcho(t *testing.T) {
	e := newEnv(t)
	args := []string{"verdict", "-policy", e.policy, "-changed", writeFile(t, filepath.Join(e.dir, "c"), "x.md"), "-round", "1", "-title", "my change", "-ocr", writeFile(t, filepath.Join(e.dir, "o"), ocrJSON("complete", "p", "m"))}
	var o, er bytes.Buffer
	if code := run(args, &o, &er); code != 0 || !strings.HasPrefix(o.String(), "review: my change\n") {
		t.Errorf("exit %d\n%s", code, o.String())
	}
}

func TestFixIsTrimmedToSixLines(t *testing.T) {
	e := newEnv(t)
	fix := "1\n2\n3\n4\n5\n6\n7\n8"
	_, out, _ := e.verdict(1, []string{"src/a.go"}, ocrJSON("complete", "p", "m", cm{"src/a.go", "high", "bug", "m", fix, 1, 1}))
	if !strings.Contains(out, "      6\n") || strings.Contains(out, "      7\n") {
		t.Error(out)
	}
}

func TestGlobs(t *testing.T) {
	for _, tc := range []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"a/**"}, "a/b/c.go", true},
		{[]string{"a/**"}, "a", false},
		{[]string{"a/**"}, "ab/c", false},
		{[]string{"a/"}, "a/b/c", true},
		{[]string{"*.md"}, "x.md", true},
		{[]string{"*.md"}, "d/x.md", false},
		{[]string{"**/*.md"}, "x.md", true},
		{[]string{"**/*.md"}, "d/e/x.md", true},
		{[]string{"**/*_test.go"}, "internal/credentials/a_test.go", true},
		{[]string{"**/testdata/**"}, "x/testdata/f", true},
		{[]string{"x?.go"}, "xa.go", true},
		{[]string{"x?.go"}, "x/.go", false},
		{[]string{"a.b"}, "aXb", false},
		{[]string{"scripts/release-check"}, "scripts/release-check", true},
		{[]string{"scripts/release-check"}, "scripts/release-check2", false},
		{[]string{"src/**", "!src/gen/**"}, "src/gen/x.go", false},
		{[]string{"src/**", "!src/gen/**"}, "src/x.go", true},
		{[]string{"src/**", "!src/gen/**", "src/gen/keep.go"}, "src/gen/keep.go", true},
		{[]string{"!src/**"}, "src/x.go", false},
		{[]string{"", " "}, "x", false},
	} {
		rules, err := parseGlobs(tc.globs)
		if err != nil {
			t.Fatal(err)
		}
		if got := matches(rules, tc.path); got != tc.want {
			t.Errorf("%v on %q = %v, want %v", tc.globs, tc.path, got, tc.want)
		}
	}
}

func TestRecord(t *testing.T) {
	dir := t.TempDir()
	base := []string{"record", "-store", dir, "-slug", "acme/widget", "-patchid", "abc123", "-verdict", "blocked", "-tier", "standard", "-round", "2", "-models", "p/m1,p/m2", "-time", "2026-11-01T10:00:00Z"}
	var o, er bytes.Buffer
	if code := run(base, &o, &er); code != 0 {
		t.Fatalf("exit %d: %s", code, er.String())
	}
	path := filepath.Join(dir, "acme_widget", "abc123")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "verdict: blocked\ntier: standard\nround: 2\nmodels: p/m1,p/m2\ntime: 2026-11-01T10:00:00Z\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if strings.TrimSpace(o.String()) != path {
		t.Errorf("printed %q", o.String())
	}
	ents, err := os.ReadDir(filepath.Join(dir, "acme_widget"))
	if err != nil || len(ents) != 1 {
		t.Errorf("a temp file was left behind: %v %v", ents, err)
	}
	// Overwrite replaces; the default time is now, in RFC3339 UTC.
	again := slices.Clone(base)
	again[8] = "pass"
	again = again[:len(again)-2]
	if code := run(again, &o, &er); code != 0 {
		t.Fatalf("exit %d: %s", code, er.String())
	}
	got, err = os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(got), "verdict: pass\n") || !strings.HasSuffix(string(got), "Z\n") {
		t.Errorf("%s %v", got, err)
	}
}

func TestRecordCoverage(t *testing.T) {
	dir := t.TempDir()
	base := []string{"record", "-store", dir, "-slug", "a/b", "-patchid", "id1", "-verdict", "pass", "-tier", "t", "-round", "1", "-models", "m", "-time", "2026-11-01T10:00:00Z"}
	var o, er bytes.Buffer
	if code := run(append(slices.Clone(base), "-covered", "2", "-total", "3"), &o, &er); code != 0 {
		t.Fatalf("exit %d: %s", code, er.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "a_b", "id1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "time: 2026-11-01T10:00:00Z\ncovered: 2\ntotal: 3\n") {
		t.Errorf("record: %s", got)
	}
	cf := func(content string) string { return writeFile(t, filepath.Join(t.TempDir(), "c.txt"), content) }
	a := slices.Clone(base)
	a[6] = "id3"
	if code := run(append(a, "-covered", "2", "-total", "3", "-covered-files", cf("cmd/x.go\ndocs/y.md\n")), &o, &er); code != 0 {
		t.Fatalf("names: exit %d: %s", code, er.String())
	}
	got, err = os.ReadFile(filepath.Join(dir, "a_b", "id3"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "covered: 2\ntotal: 3\ncovered-file: cmd/x.go\ncovered-file: docs/y.md\n") {
		t.Errorf("record: %s", got)
	}
	for name, extra := range map[string][]string{
		"names fewer than covered": {"-covered", "2", "-total", "3", "-covered-files", cf("cmd/x.go\n")},
		"names without covered":    {"-covered-files", cf("cmd/x.go\n")},
		"dot-dot name":             {"-covered", "1", "-total", "3", "-covered-files", cf("../x.go\n")},
		"inner dot-dot name":       {"-covered", "1", "-total", "3", "-covered-files", cf("a/../x.go\n")},
		"absolute name":            {"-covered", "1", "-total", "3", "-covered-files", cf("/etc/passwd\n")},
		"repeated name":            {"-covered", "2", "-total", "3", "-covered-files", cf("a.go\na.go\n")},
		"names file missing":       {"-covered", "1", "-total", "3", "-covered-files", "/nonexistent/c.txt"},
	} {
		a := slices.Clone(base)
		a[6] = "id4"
		if code := run(append(a, extra...), &o, &er); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	for name, extra := range map[string][]string{
		"non-numeric covered": {"-covered", "x", "-total", "3"},
		"non-numeric total":   {"-covered", "1", "-total", "y"},
		"covered alone":       {"-covered", "1"},
		"total alone":         {"-total", "1"},
		"covered over total":  {"-covered", "4", "-total", "3"},
	} {
		a := slices.Clone(base)
		a[6] = "id2"
		if code := run(append(a, extra...), &o, &er); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a_b", "id2")); err == nil {
		t.Error("a refused record was written")
	}
}

func TestRecordEvidence(t *testing.T) {
	dir, src := t.TempDir(), t.TempDir()
	withManifest := writeFile(t, filepath.Join(src, "ocr-0.json"), `{"status":"complete","manifest":{"input":{"exact_range":"abc..def","source_artifact_sha256":"cafe"}}}`)
	noManifest := writeFile(t, filepath.Join(src, "ocr-1.json"), `{"status":"complete","comments":[]}`)
	noSource := writeFile(t, filepath.Join(src, "ocr-2.json"), `{"manifest":{"input":{"exact_range":"r2"}}}`)
	args := []string{"record", "-store", dir, "-slug", "a/b", "-patchid", "id1", "-verdict", "pass", "-tier", "t", "-round", "1", "-models", "m", "-time", "2026-11-01T10:00:00Z",
		"-evidence", withManifest, "-evidence", noManifest, "-evidence", noSource}
	var o, er bytes.Buffer
	if code := run(args, &o, &er); code != 0 {
		t.Fatalf("exit %d: %s", code, er.String())
	}
	rec := filepath.Join(dir, "a_b", "id1")
	got, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	want.WriteString("verdict: pass\ntier: t\nround: 1\nmodels: m\ntime: 2026-11-01T10:00:00Z\nevidence: 3\n")
	for i, c := range []struct{ f, rng, sha string }{{withManifest, "abc..def", "cafe"}, {noManifest, "", ""}, {noSource, "r2", ""}} {
		data, err := os.ReadFile(c.f)
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(fmt.Sprintf("%s.evidence-%d.json", rec, i+1))
		if err != nil || !bytes.Equal(copied, data) {
			t.Errorf("evidence %d copy: %v %q", i+1, err, copied)
		}
		fmt.Fprintf(&want, "run-%d: %x %s %s\n", i+1, sha256.Sum256(data), c.rng, c.sha)
	}
	if string(got) != want.String() {
		t.Errorf("record\n%s\nwant\n%s", got, want.String())
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "a_b")); len(ents) != 4 {
		t.Errorf("want the record and 3 evidence files, got %v", ents)
	}
}

// The record is the commit point: it is renamed into place only after its evidence is, and an unreadable evidence
// file refuses the record without writing anything.
func TestRecordEvidenceOrderAndRefusal(t *testing.T) {
	dir := t.TempDir()
	base := []string{"record", "-store", dir, "-slug", "a/b", "-patchid", "id1", "-verdict", "pass", "-tier", "t", "-round", "1", "-models", "m"}
	var o, er bytes.Buffer
	if code := run(append(slices.Clone(base), "-evidence", filepath.Join(dir, "missing.json")), &o, &er); code != 2 {
		t.Errorf("a missing evidence file: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_b")); err == nil {
		t.Error("a refused record wrote something")
	}
	// Order: make the record path a directory so the record write fails; the evidence file is then already there.
	ev := writeFile(t, filepath.Join(t.TempDir(), "e.json"), `{}`)
	if err := os.MkdirAll(filepath.Join(dir, "a_b", "id1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := run(append(slices.Clone(base), "-evidence", ev), &o, &er); code != 2 {
		t.Errorf("an unwritable record: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "a_b", "id1.evidence-1.json")); err != nil {
		t.Errorf("evidence should be written before the record: %v", err)
	}
}

func TestRecordRefusals(t *testing.T) {
	dir := t.TempDir()
	mk := func(slug, id, verdict, when string) []string {
		a := []string{"record", "-store", dir, "-slug", slug, "-patchid", id, "-verdict", verdict, "-tier", "t", "-round", "1", "-models", "m"}
		if when != "" {
			a = append(a, "-time", when)
		}
		return a
	}
	for name, args := range map[string][]string{
		"empty patchid":   mk("a/b", "", "pass", ""),
		"slash patchid":   mk("a/b", "x/y", "pass", ""),
		"dotdot patchid":  mk("a/b", "..", "pass", ""),
		"dotdot inside":   mk("a/b", "a..b", "pass", ""),
		"bad verdict":     mk("a/b", "id", "maybe", ""),
		"slug no slash":   mk("ab", "id", "pass", ""),
		"slug dotdot":     mk("../b", "id", "pass", ""),
		"slug three part": mk("a/b/c", "id", "pass", ""),
		"bad time":        mk("a/b", "id", "pass", "yesterday"),
		"no store":        {"record", "-slug", "a/b", "-patchid", "id", "-verdict", "pass", "-tier", "t", "-round", "1"},
	} {
		var o, er bytes.Buffer
		if code := run(args, &o, &er); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("a refused record wrote something: %v %v", ents, err)
	}
}

func TestRounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "rounds")
	next := func(id string, bump bool) string {
		t.Helper()
		args := []string{"rounds", "-file", file, "-patchid", id}
		if bump {
			args = append(args, "-bump")
		}
		var o, er bytes.Buffer
		if code := run(args, &o, &er); code != 0 {
			t.Fatalf("exit %d: %s", code, er.String())
		}
		return strings.TrimSpace(o.String())
	}
	if got := next("p1", false); got != "1" {
		t.Errorf("first review: %s", got)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("no -bump must not write")
	}
	if got := next("p1", true); got != "1" {
		t.Errorf("bump p1: %s", got)
	}
	if got := next("p1", true); got != "1" {
		t.Errorf("re-review of the same patch keeps its number: %s", got)
	}
	if got := next("p2", false); got != "2" {
		t.Errorf("new patch: %s", got)
	}
	if got := next("p2", true); got != "2" {
		t.Errorf("bump p2: %s", got)
	}
	if got := next("p3", true); got != "3" {
		t.Errorf("bump p3: %s", got)
	}
	if got := next("p1", false); got != "1" {
		t.Errorf("old patch after later ones: %s", got)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("file:\n%s", b)
	}
	for i, id := range []string{"p1", "p2", "p3"} {
		f := strings.Fields(lines[i])
		if len(f) != 2 || f[0] != id || !strings.HasSuffix(f[1], "Z") {
			t.Errorf("line %d: %q", i, lines[i])
		}
	}
	var o, er bytes.Buffer
	for _, args := range [][]string{
		{"rounds", "-patchid", "x"}, {"rounds", "-file", file, "-patchid", ""}, {"rounds", "-file", file, "-patchid", "a/b"},
	} {
		if code := run(args, &o, &er); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// TestRepoPolicy loads the policy the repository ships and checks ADR 0080's table and its strictest-wins examples.
func TestRepoPolicy(t *testing.T) {
	p, err := loadPolicy(filepath.Join("..", "..", "docs", "review", "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.MinComplete != 1 || p.DefaultTier != "standard" ||
		len(p.Models) != 1 || p.Models[0] != (modelRef{"openrouter", "z-ai/glm-5.3-flash"}) ||
		len(p.FallbackModels) != 1 || p.FallbackModels[0] != (modelRef{"openrouter", "z-ai/glm-5.3"}) {
		t.Errorf("min_complete %d, default %q, models %v, fallback %v", p.MinComplete, p.DefaultTier, p.Models, p.FallbackModels)
	}
	var names []string
	for _, tr := range p.Tiers {
		names = append(names, tr.Name)
	}
	if strings.Join(names, ",") != "security-critical,tests-and-docs,standard" {
		t.Fatalf("tiers %v", names)
	}
	for name, want := range map[string]struct {
		blocks string
		cap    int
	}{
		"security-critical": {"blocker, high, medium", 6},
		"tests-and-docs":    {"blocker", 2},
		"standard":          {"blocker, high", 4},
	} {
		for _, tr := range p.Tiers {
			if tr.Name == name && (tr.blockList() != want.blocks || tr.RoundCap != want.cap) {
				t.Errorf("%s: blocks %q cap %d", name, tr.blockList(), tr.RoundCap)
			}
		}
	}
	for _, tc := range []struct {
		changed []string
		want    string
	}{
		{[]string{"internal/sap/verify_test.go"}, "security-critical"},
		{[]string{"docs/x.md"}, "tests-and-docs"},
		{[]string{"docs/x.md", "internal/endpoint/a.go"}, "standard"},
		{[]string{"docs/x.md", ".github/workflows/ci.yml"}, "security-critical"},
		{[]string{".github/dependabot.yml"}, "security-critical"},
		{[]string{"internal/admin/a.go"}, "security-critical"},
		{[]string{"internal/policy/a.go"}, "security-critical"},
		{[]string{"internal/policyfile/a.go"}, "standard"},
		{[]string{"internal/store/admit.go"}, "security-critical"},
		{[]string{"internal/store/admit_test.go"}, "security-critical"},
		{[]string{"internal/store/store.go"}, "security-critical"},
		{[]string{"internal/store/other.go"}, "security-critical"},
		{[]string{"internal/release/x.go"}, "security-critical"},
		{[]string{"scripts/buildrelease/main.go"}, "security-critical"},
		{[]string{"scripts/release-check"}, "security-critical"},
		{[]string{"scripts/build-twice"}, "security-critical"},
		{[]string{"docs/allowed_signers"}, "security-critical"},
		{[]string{"ci/release.allowed_signers"}, "security-critical"},
		{[]string{".github/workflows/release.yml"}, "security-critical"},
		{[]string{"internal/natsauth/x.go"}, "security-critical"},
		{[]string{"internal/mint/x.go"}, "security-critical"},
		{[]string{"cmd/mashctl/main.go"}, "security-critical"},
		{[]string{"cmd/mash/main.go"}, "standard"},
		{[]string{"scripts/reviewgate/main.go"}, "standard"},
		{[]string{"scripts/reviewgate/main_test.go"}, "standard"}, // scripts/** also matches, and standard is stricter than docs
		{[]string{"plugin/hooks/x.sh"}, "standard"},
		{[]string{"cmd/mash/main.go", "go.mod"}, "standard"},
		{[]string{"README.md"}, "tests-and-docs"},
		{[]string{"unlisted/file.txt"}, "standard"},
		{[]string{".mergify.yml"}, "security-critical"},
		{[]string{"internal/sap/testdata/x.json"}, "security-critical"},
	} {
		if got, _ := chooseTier(p, tc.changed); got.t.Name != tc.want {
			t.Errorf("%v: tier %s, want %s", tc.changed, got.t.Name, tc.want)
		}
	}
	// The sap test file is in all three tiers (internal/** too); the header names the other two.
	got, others := chooseTier(p, []string{"internal/sap/a_test.go"})
	if got.t.Name != "security-critical" || len(others) != 2 || others[0].t.Name != "tests-and-docs" || others[1].t.Name != "standard" {
		t.Errorf("%s %v", got.t.Name, others)
	}
}

// ---- verify ----

type vfx struct {
	t       *testing.T
	store   string
	policy  string
	changed string
	record  string
}

// newVerifyFixture writes a record with `reviewgate record` over the given OCR bodies, under the repo's own policy.
func newVerifyFixture(t *testing.T, verdictV, tierV string, covered, total int, changed []string, ocrs ...string) *vfx {
	t.Helper()
	return newVerifyFixtureNames(t, verdictV, tierV, covered, total, changed, nil, ocrs...)
}

// newVerifyFixtureNames is newVerifyFixture with the covered file names recorded (-covered-files) when names is not nil.
func newVerifyFixtureNames(t *testing.T, verdictV, tierV string, covered, total int, changed, names []string, ocrs ...string) *vfx {
	t.Helper()
	dir := t.TempDir()
	f := &vfx{t: t, store: filepath.Join(dir, "store"), policy: filepath.Join("..", "..", "docs", "review", "policy.json")}
	f.changed = writeFile(t, filepath.Join(dir, "changed.txt"), strings.Join(changed, "\n"))
	args := []string{"record", "-store", f.store, "-slug", "o/r", "-patchid", "pid1", "-verdict", verdictV, "-tier", tierV, "-round", "2", "-models", "m1,m2",
		"-time", "2026-11-01T10:00:00Z", "-covered", fmt.Sprint(covered), "-total", fmt.Sprint(total)}
	if names != nil {
		args = append(args, "-covered-files", writeFile(t, filepath.Join(dir, "covered.txt"), strings.Join(names, "\n")))
	}
	for i, o := range ocrs {
		args = append(args, "-evidence", writeFile(t, filepath.Join(dir, fmt.Sprintf("ev%d.json", i)), o))
	}
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("record exit %d: %s", code, errb.String())
	}
	f.record = filepath.Join(f.store, "o_r", "pid1")
	return f
}

func (f *vfx) verify(patchid string) (int, string) {
	f.t.Helper()
	var out, errb bytes.Buffer
	code := run([]string{"verify", "-store", f.store, "-slug", "o/r", "-patchid", patchid, "-policy", f.policy, "-changed", f.changed}, &out, &errb)
	if code == 1 && errb.Len() != 0 {
		f.t.Errorf("stderr on exit 1: %s", errb.String())
	}
	return code, strings.TrimSpace(out.String())
}

func (f *vfx) rewriteRecord(fn func(string) string) {
	f.t.Helper()
	b, err := os.ReadFile(f.record)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.record, []byte(fn(string(b))), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func TestVerifyVerified(t *testing.T) {
	clean := ocrJSON("complete", "openrouter", "m1")
	low := ocrJSON("complete", "openrouter", "m2", cm{path: "cmd/x.go", sev: "medium", cat: "c", content: "meh", start: 1, end: 1})
	f := newVerifyFixtureNames(t, "pass", "standard", 3, 4, []string{"cmd/x.go", "cmd/y.go", "cmd/z.go", "README.md"}, []string{"cmd/x.go", "cmd/y.go", "cmd/z.go"}, clean, low)
	code, out := f.verify("pid1")
	if code != 0 || out != "inloop: ocr m1,m2, covered 3 of 4 files, tier standard, round 2" {
		t.Errorf("exit %d: %q", code, out)
	}
	// full coverage with no names still verifies; so does docs under docs/ left out
	f = newVerifyFixture(t, "pass", "standard", 1, 1, []string{"cmd/x.go"}, clean)
	if code, out := f.verify("pid1"); code != 0 {
		t.Errorf("full coverage without names: exit %d: %q", code, out)
	}
	f = newVerifyFixtureNames(t, "pass", "standard", 1, 3, []string{"cmd/x.go", "docs/a/b.txt", "pkg/notes.md"}, []string{"cmd/x.go"}, clean)
	if code, out := f.verify("pid1"); code != 0 {
		t.Errorf("docs left out: exit %d: %q", code, out)
	}
	if strings.Contains(out, "\n") {
		t.Errorf("not one line: %q", out)
	}
}

func TestVerifyNotVerified(t *testing.T) {
	clean := ocrJSON("complete", "p", "m1")
	high := ocrJSON("complete", "p", "m2", cm{path: "cmd/x.go", sev: "high", cat: "c", content: "bad", start: 1, end: 1})
	medium := ocrJSON("complete", "p", "m2", cm{path: "internal/sap/x.go", sev: "medium", cat: "c", content: "meh", start: 1, end: 1})
	std := []string{"cmd/x.go"}
	for _, tc := range []struct {
		name    string
		fix     func() *vfx
		after   func(*vfx)
		patchid string
		want    string
	}{
		{name: "no record", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) }, patchid: "other", want: "no record"},
		{name: "recorded blocked", fix: func() *vfx { return newVerifyFixture(t, "blocked", "standard", 1, 1, std, clean) }, want: "verdict is blocked, not pass"},
		{name: "recorded cap", fix: func() *vfx { return newVerifyFixture(t, "cap", "standard", 1, 1, std, clean) }, want: "verdict is cap, not pass"},
		{name: "recorded tier security-critical", fix: func() *vfx { return newVerifyFixture(t, "pass", "security-critical", 1, 1, std, clean) }, want: "security-critical is never waived"},
		{name: "files in security-critical, record says standard", fix: func() *vfx {
			return newVerifyFixture(t, "pass", "standard", 1, 1, []string{"internal/sap/x.go"}, clean)
		}, want: "security-critical is never waived"},
		{name: "medium under security-critical files", fix: func() *vfx {
			return newVerifyFixture(t, "pass", "standard", 1, 1, []string{"internal/sap/x.go"}, medium)
		}, want: "security-critical is never waived"},
		{name: "zero coverage", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 0, 2, std, clean) }, want: "zero coverage"},
		{name: "total differs from the changed files", fix: func() *vfx {
			return newVerifyFixture(t, "pass", "standard", 2, 2, std, clean)
		}, want: "record covers a different diff (total 2, changed 1)"},
		{name: "empty changed list", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, nil, clean) },
			want: "record covers a different diff (total 1, changed 0)"},
		{name: "partial coverage, an uncovered test file", fix: func() *vfx {
			return newVerifyFixtureNames(t, "pass", "standard", 1, 3, []string{"cmd/x.go", "cmd/y.go", "cmd/x_test.go"}, []string{"cmd/x.go"}, clean)
		}, want: "2 changed file(s) were not reviewed by OCR and are not documentation: cmd/y.go, cmd/x_test.go; the server-side review must cover them"},
		{name: "partial coverage names only the first three", fix: func() *vfx {
			ch := []string{"cmd/a.go", "cmd/b.go", "cmd/c.go", "cmd/d.go", "cmd/e.go"}
			return newVerifyFixtureNames(t, "pass", "standard", 1, 5, ch, ch[:1], clean)
		}, want: "4 changed file(s) were not reviewed by OCR and are not documentation: cmd/b.go, cmd/c.go, cmd/d.go; the"},
		{name: "partial coverage with no names", fix: func() *vfx {
			return newVerifyFixture(t, "pass", "standard", 1, 2, []string{"cmd/x.go", "README.md"}, clean)
		}, want: "1 changed file(s) were not reviewed by OCR and the record names no covered files"},
		{name: "a covered name that is not a changed file", fix: func() *vfx {
			return newVerifyFixtureNames(t, "pass", "standard", 1, 1, []string{"cmd/x.go"}, []string{"cmd/other.go"}, clean)
		}, want: "record covers a different diff (covered file cmd/other.go is not changed)"},
		{name: "no coverage lines", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) {
				f.rewriteRecord(func(s string) string { return strings.NewReplacer("covered: 1\n", "", "total: 1\n", "").Replace(s) })
			}, want: "no coverage recorded"},
		{name: "recorded pass over a high under standard", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean, high) },
			want: "recomputed from the evidence is not a pass"},
		{name: "evidence tampered", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) { writeFile(t, f.record+".evidence-1.json", ocrJSON("complete", "p", "m1")+" ") }, want: "run 1 differs from its recorded sha256"},
		{name: "evidence missing", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) { _ = os.Remove(f.record + ".evidence-1.json") }, want: "evidence missing: run 1"},
		{name: "no evidence at all", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std) }, want: "evidence missing"},
		{name: "run not complete", fix: func() *vfx {
			return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean, ocrJSON("failed", "p", "m2"))
		},
			want: "run 2 is not complete"},
		{name: "malformed record", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) { f.rewriteRecord(func(string) string { return "garbage without keys\n" }) }, want: "malformed record"},
		{name: "empty record", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) { f.rewriteRecord(func(string) string { return "" }) }, want: "malformed record"},
		{name: "non-numeric covered", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) {
				f.rewriteRecord(func(s string) string { return strings.Replace(s, "covered: 1", "covered: x", 1) })
			}, want: "malformed record"},
		{name: "run line without a sha", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) {
				f.rewriteRecord(func(s string) string { return strings.Replace(s, "run-1: ", "run-1: short ", 1) })
			}, want: "malformed record"},
		{name: "evidence count above the run lines", fix: func() *vfx { return newVerifyFixture(t, "pass", "standard", 1, 1, std, clean) },
			after: func(f *vfx) {
				f.rewriteRecord(func(s string) string { return strings.Replace(s, "evidence: 1", "evidence: 2", 1) })
			}, want: "malformed record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fix()
			if tc.after != nil {
				tc.after(f)
			}
			pid := tc.patchid
			if pid == "" {
				pid = "pid1"
			}
			code, out := f.verify(pid)
			if code != 1 || !strings.HasPrefix(out, "not verified: ") || !strings.Contains(out, tc.want) {
				t.Errorf("exit %d, out %q, want a reason containing %q", code, out, tc.want)
			}
		})
	}
}

// The round cap never turns into a pass: a policy whose cap is 1 and a finding at round 1 is exit 3 from verdict, and
// verify refuses it.
func TestVerifyCapIsNotAPass(t *testing.T) {
	f := newVerifyFixture(t, "pass", "standard", 1, 1, []string{"cmd/x.go"},
		ocrJSON("complete", "p", "m", cm{path: "cmd/x.go", sev: "high", cat: "c", content: "bad", start: 1, end: 1}))
	f.policy = writeFile(t, filepath.Join(t.TempDir(), "p.json"), `{"min_complete":1,"default_tier":"standard","tiers":[{"name":"standard","block":["blocker","high"],"round_cap":1,"paths":["cmd/**"]}]}`)
	if code, out := f.verify("pid1"); code != 1 || !strings.Contains(out, "exit 3") {
		t.Errorf("exit %d: %q", code, out)
	}
}

func TestVerifyUsageAndBadInputs(t *testing.T) {
	var o, e bytes.Buffer
	if code := run([]string{"verify", "-store", "x"}, &o, &e); code != 2 {
		t.Errorf("missing flags: exit %d", code)
	}
	f := newVerifyFixture(t, "pass", "standard", 1, 1, []string{"cmd/x.go"}, ocrJSON("complete", "p", "m"))
	f.policy = filepath.Join(t.TempDir(), "nope.json")
	if code, out := f.verify("pid1"); code != 1 || !strings.HasPrefix(out, "not verified: ") {
		t.Errorf("missing policy: exit %d %q", code, out)
	}
	f = newVerifyFixture(t, "pass", "standard", 1, 1, []string{"cmd/x.go"}, ocrJSON("complete", "p", "m"))
	if code, out := f.verify("../pid1"); code != 1 || !strings.HasPrefix(out, "not verified: ") {
		t.Errorf("unsafe patch id: exit %d %q", code, out)
	}
}

func TestPolicyQuestionsValid(t *testing.T) {
	dir := t.TempDir()
	p, err := loadPolicy(writeFile(t, filepath.Join(dir, "p.json"), `{"default_tier":"a","tiers":[{"name":"a","round_cap":1}],"questions":[{"id":"q","severity":"high","text":"t?"}]}`))
	if err != nil || len(p.Questions) != 1 || p.Questions[0].ID != "q" {
		t.Fatalf("valid question rejected: %v %+v", err, p)
	}
}

func TestPolicyModelsValid(t *testing.T) {
	const head = `{"min_complete":1,"default_tier":"s","tiers":[{"name":"s","block":["blocker"],"round_cap":1,"paths":["**"]}],`
	for _, c := range []struct {
		name, tail string
		ok         bool
	}{
		{"both lists", `"models":[{"provider":"p","model":"m"}],"fallback_models":[{"provider":"p","model":"f"}]}`, true},
		{"none", `"models":[]}`, true},
		{"empty model", `"models":[{"provider":"p","model":""}]}`, false},
		{"empty provider", `"models":[{"provider":" ","model":"m"}]}`, false},
		{"fallback empty provider", `"fallback_models":[{"provider":"","model":"m"}]}`, false},
		{"fallback empty model", `"models":[],"fallback_models":[{"provider":"p"}]}`, false},
	} {
		p, err := loadPolicy(writeFile(t, filepath.Join(t.TempDir(), "p.json"), head+c.tail))
		if (err == nil) != c.ok {
			t.Errorf("%s: err %v, want ok=%v", c.name, err, c.ok)
		}
		if c.name == "both lists" && err == nil && (len(p.FallbackModels) != 1 || p.FallbackModels[0].Model != "f") {
			t.Errorf("fallback not parsed: %v", p.FallbackModels)
		}
	}
}

// ---- known weaknesses inherited from the source ----
//
// The two tests below pin behaviour that ADR 0080 ("Known weaknesses inherited from the source") records as
// weaknesses. They assert today's behaviour on purpose. When the source project fixes them and mash pulls the fix, these
// tests must be flipped deliberately, not deleted or loosened.

// TestKnownWeakness_ForgedReceiptVerifies pins that a receipt written by hand, with no review behind it, verifies: verify
// checks the evidence against hashes held in the same writable receipt.
func TestKnownWeakness_ForgedReceiptVerifies(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	rec := filepath.Join(store, "o_r", "forged1")
	clean := ocrJSON("complete", "openrouter", "never-ran")
	writeFile(t, rec+".evidence-1.json", clean)
	body := fmt.Sprintf("verdict: pass\ntier: standard\nround: 1\nmodels: never-ran\ntime: 2026-11-01T10:00:00Z\ncovered: 1\ntotal: 1\ncovered-file: cmd/x.go\nevidence: 1\nrun-1: %x  \n", sha256.Sum256([]byte(clean)))
	writeFile(t, rec, body)
	changed := writeFile(t, filepath.Join(dir, "changed.txt"), "cmd/x.go")
	var out, errb bytes.Buffer
	code := run([]string{"verify", "-store", store, "-slug", "o/r", "-patchid", "forged1", "-policy", filepath.Join("..", "..", "docs", "review", "policy.json"), "-changed", changed}, &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "inloop: ocr never-ran") {
		t.Errorf("known weakness changed (forged receipt no longer verifies; flip this test): exit %d: %q %s", code, out.String(), errb.String())
	}
}

// TestKnownWeakness_ReceiptIgnoresRange pins that verify never compares the recorded exact_range or source artifact hash
// with anything: a receipt whose recorded range and source hash are rewritten to other values still verifies, and verify
// takes no range at all, only the patch-id and the changed file names.
func TestKnownWeakness_ReceiptIgnoresRange(t *testing.T) {
	withManifest := `{"status":"complete","llm":{"provider":"openrouter","model":"m1"},"message":"","summary":{},"comments":[],` +
		`"manifest":{"input":{"exact_range":"aaaa..bbbb","source_artifact_sha256":"` + strings.Repeat("a", 64) + `"}}}`
	f := newVerifyFixture(t, "pass", "standard", 1, 1, []string{"cmd/x.go"}, withManifest)
	b, err := os.ReadFile(f.record)
	if err != nil || !strings.Contains(string(b), "aaaa..bbbb") {
		t.Fatalf("fixture did not record the range: %v %q", err, b)
	}
	if code, out := f.verify("pid1"); code != 0 {
		t.Fatalf("baseline: exit %d: %q", code, out)
	}
	f.rewriteRecord(func(s string) string {
		s = strings.ReplaceAll(s, "aaaa..bbbb", "cccc..dddd")
		return strings.ReplaceAll(s, strings.Repeat("a", 64)+"\n", strings.Repeat("f", 64)+"\n")
	})
	if code, out := f.verify("pid1"); code != 0 {
		t.Errorf("known weakness changed (verify now checks the range; flip this test): exit %d: %q", code, out)
	}
}
