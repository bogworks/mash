package release

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// IsDocsPath must say what scripts/ci-changes' is_docs says: the script is the classifier the workflows run, the Go port is the
// release gate's. The same table goes through both.
func TestIsDocsPath_matchesTheClassifierScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	paths := []string{
		"README.md", "docs/faq.md", "docs/decisions/0001-x.md", "docs/images/a.svg", "docs/images/a.PNG", "docs/a.pdf",
		"docs/branch-protection.md", "docs/reference/cli.md", "docs/review/policy.json", "docs/allowed_signers", "docs/skipped-releases.txt",
		"docs/release-cutover", "spec/rules.json", "spec/x.md", ".github/workflows/ci.yml", ".github/pull_request_template.md",
		".mergify.yml", "scripts/ci-changes", "scripts/notes.md", "cmd/mash/main.go", "cmd/mash/README.md", "internal/x/y.go", "internal/x/README.md",
		"plugin/hooks.md", ".claude-plugin/plugin.json", "ci/x.md", "go.mod", "go.sum", "sub/go.mod", "x/go.sum", "main.go", "CHANGELOG.md",
		"changelog.d/frag.md", "testdata/a.md", "internal/testdata/a.md", "docs/testdata/a.md", "docs/conformance.md", "docs/a/conformance/b.md",
		"AGENTS.md", "docs/AGENTS.md", "x/y/CLAUDE.md", "CLAUDE.md", "mash-setup/skills/setup/SKILL.md", "mash-setup/README.md", "docs/x.go", "docs/NOTES.MD", "README.MD", "docs/guide.Md", "docs/a b.md", "GEMINI.md", "x/gemini.md", "docs/Gemini.MD", "SKILL.md", "docs/x/skill.md", "a/Skill.md", ".cursorrules", "docs/.CursorRules", ".windsurfrules", "x/.windsurfrules",
		"docs/prompt.tmpl", "x.TMPL", "docs/skills/a.md", "docs/Skills/b.md", "docs/commands/c.md", "docs/agents/d.md", "docs/Agents/e.md", "docs/hooks/f.md", "docs/prompts/g.md",
		"docs/briefings/h.md", "docs/plugins/i.md", "docs/.claude-plugin/j.md", "docs/a/skills/b/c.md", "Mash-Setup/x.md", "MASH-SETUP/y.txt", "x/Mash-Setup/z.md", "docs/mash-setup/a.md", "docs/mash-setups.md", "mash-setup/skills/y.md",
		"README.md", "docs/install.md", "SECURITY.md", "CONTRIBUTING.md", "docs/agents.md", "docs/MYAGENTS.md", "docs/agentsmd.md", "docs/skill-guide.md", "docs/skills.md",
		"NEW-TOP-LEVEL.txt", "LICENSE", "Makefile", "docs/notes.txt", "notes/a.md", "docs/sub/dir/page.md",
	}
	for _, p := range paths {
		cmd := exec.Command("bash", "../../scripts/ci-changes")
		cmd.Stdin = strings.NewReader(p + "\n")
		cmd.Env = append(os.Environ(), "EVENT=pull_request", "HEAD_REF=pr/x/y")
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("scripts/ci-changes %q: %v", p, err)
		}
		want := strings.TrimSpace(string(b)) == "code=false"
		if got := IsDocsPath(p); got != want {
			t.Errorf("IsDocsPath(%q) = %v, scripts/ci-changes says docs-only=%v", p, got, want)
		}
	}
}

func fp(sha string) string { return strings.Repeat(sha, 40)[:40] }

func runsFor(sha string, conclusions map[string]string) []CheckRun {
	var out []CheckRun
	i := int64(1)
	for _, n := range RequiredChecks {
		c, ok := conclusions[n]
		if !ok {
			continue
		}
		r := CheckRun{ID: i, HeadSHA: sha, Name: n, Status: "completed", Conclusion: c}
		r.App.Slug = CIApp
		out = append(out, r)
		i++
	}
	return out
}

func allOK() map[string]string {
	m := map[string]string{}
	for _, n := range RequiredChecks {
		m[n] = "success"
	}
	return m
}
func allSkipped() map[string]string {
	m := map[string]string{}
	for _, n := range RequiredChecks {
		m[n] = "skipped"
	}
	return m
}

// skippedAt / okAt: one run per required check with ids starting at base, so they are newer than runsFor's ids 1 and 2.
func runsAt(sha, conclusion string, base int64) []CheckRun {
	var out []CheckRun
	for i, n := range RequiredChecks {
		r := CheckRun{ID: base + int64(i), HeadSHA: sha, Name: n, Status: "completed", Conclusion: conclusion}
		r.App.Slug = CIApp
		out = append(out, r)
	}
	return out
}
func skippedAt(sha string, base int64) []CheckRun { return runsAt(sha, "skipped", base) }
func okAt(sha string, base int64) []CheckRun      { return runsAt(sha, "success", base) }

// sameIDConflict reports run id 1 of the first required check twice: skipped, then failed.
func sameIDConflict(sha string) []CheckRun {
	a := CheckRun{ID: 1, HeadSHA: sha, Name: RequiredChecks[0], Status: "completed", Conclusion: "skipped"}
	b := a
	b.Conclusion = "failure"
	a.App.Slug, b.App.Slug = CIApp, CIApp
	return append([]CheckRun{a, b}, runsFor(sha, map[string]string{RequiredChecks[1]: "skipped"})...)
}

func TestCheckCIWalk(t *testing.T) {
	x, p1, p2 := fp("a"), fp("b"), fp("c")
	docs := []string{"README.md", "docs/faq.md"}
	code := []string{"internal/x/y.go"}
	type step struct {
		sha   string
		paths []string
		runs  []CheckRun
	}
	spoof := func(sha string) []CheckRun {
		r := runsFor(sha, allSkipped())
		r[0].App.Slug = "someone-else"
		return r
	}
	failed := allOK()
	failed[RequiredChecks[0]] = "failure"
	cases := []struct {
		name    string
		chain   []step
		wantErr string
	}{
		{"candidate with its own passing checks (unchanged)", []step{{x, code, runsFor(x, allOK())}}, ""},
		{"docs-only head over a tested commit", []step{{x, docs, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, ""},
		{"docs head over a docs commit over a tested one", []step{{x, docs, runsFor(x, allSkipped())}, {p1, docs, nil}, {p2, code, runsFor(p2, allOK())}}, ""},
		{"a code commit between is refused", []step{{x, docs, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allSkipped())}, {p2, code, runsFor(p2, allOK())}}, "not docs-only"},
		{"a code candidate whose checks never ran is refused", []step{{x, code, nil}, {p1, code, runsFor(p1, allOK())}}, "not docs-only"},
		{"a docs candidate whose tested ancestor failed is refused", []step{{x, docs, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, failed)}}, "on the way back"},
		{"a failed candidate is refused, not walked past", []step{{x, docs, runsFor(x, failed)}, {p1, code, runsFor(p1, allOK())}}, "failure"},
		{"a spoofed run on the candidate is refused", []step{{x, docs, spoof(x)}, {p1, code, runsFor(p1, allOK())}}, "comes from app"},
		{"failed then skipped on the same candidate is refused (a later skipped run does not hide a failure)", []step{{x, docs, append(runsFor(x, failed), skippedAt(x, 10)...)}, {p1, code, runsFor(p1, allOK())}}, "failure"},
		{"skipped then succeeded on the candidate: the later tested run decides", []step{{x, docs, append(skippedAt(x, 1), okAt(x, 10)...)}}, ""},
		{"succeeded then a later skipped re-run: the tested run decides", []step{{x, docs, append(runsFor(x, allOK()), skippedAt(x, 10)...)}}, ""},
		{"failed then a newer success supersedes the failure", []step{{x, docs, append(runsFor(x, failed), okAt(x, 10)...)}}, ""},
		{"the same run id skipped and failed is refused", []step{{x, docs, sameIDConflict(x)}, {p1, code, runsFor(p1, allOK())}}, "twice with different results"},
		{"failed then skipped on an ancestor is refused on the way back", []step{{x, docs, runsFor(x, allSkipped())}, {p1, docs, append(runsFor(p1, failed), skippedAt(p1, 10)...)}, {p2, code, runsFor(p2, allOK())}}, "on the way back"},
		{"an empty path list is code", []step{{x, nil, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "no changed paths"},
		{"a docs commit that touches .github is code", []step{{x, []string{"docs/a.md", ".github/workflows/ci.yml"}, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "not docs-only"},
		{"a docs commit that touches .mergify.yml or scripts is code", []step{{x, []string{"scripts/ci-changes", ".mergify.yml"}, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "not docs-only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var chain []Commit
			byS := map[string][]CheckRun{}
			for _, s := range c.chain {
				chain = append(chain, Commit{SHA: s.sha, Paths: s.paths})
				byS[s.sha] = s.runs
			}
			err := CheckCIWalk(x, chain, func(sha string) ([]CheckRun, error) { return byS[sha], nil })
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want an error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestCheckCIWalk_noTestedAncestorWithinTheCap(t *testing.T) {
	var chain []Commit
	for i := 0; i < MaxDocsWalk+10; i++ {
		chain = append(chain, Commit{SHA: fmt.Sprintf("%040d", i), Paths: []string{"docs/a.md"}})
	}
	x := chain[0].SHA
	// A tested commit exists, but beyond the cap.
	tested := chain[MaxDocsWalk+5].SHA
	err := CheckCIWalk(x, chain, func(sha string) ([]CheckRun, error) {
		if sha == tested {
			return runsFor(sha, allOK()), nil
		}
		return runsFor(sha, allSkipped()), nil
	})
	if err == nil || !strings.Contains(err.Error(), "within") {
		t.Fatalf("want a refusal at the cap, got %v", err)
	}
	// Inside the cap it is accepted.
	tested = chain[MaxDocsWalk-1].SHA
	if err := CheckCIWalk(x, chain, func(sha string) ([]CheckRun, error) {
		if sha == tested {
			return runsFor(sha, allOK()), nil
		}
		return runsFor(sha, allSkipped()), nil
	}); err != nil {
		t.Fatalf("a tested ancestor at the cap's edge: %v", err)
	}
}

func TestCheckCIWalk_chainMustStartAtTheCandidate(t *testing.T) {
	if err := CheckCIWalk(fp("a"), []Commit{{SHA: fp("b"), Paths: []string{"a.md"}}}, nil); err == nil {
		t.Fatal("a chain that does not start at the candidate must be refused")
	}
}

func TestCitedRuns(t *testing.T) {
	x := fp("a")
	n0, n1 := RequiredChecks[0], RequiredChecks[1]
	mk := func(id int64, name, status, concl, app string) CheckRun {
		r := CheckRun{ID: id, HeadSHA: x, Name: name, Status: status, Conclusion: concl}
		r.App.Slug = app
		return r
	}
	cases := []struct {
		name string
		runs []CheckRun
		want map[string]int64
	}{
		{"latest tested wins over a later skipped", []CheckRun{mk(5, n0, "completed", "success", CIApp), mk(9, n0, "completed", "skipped", CIApp)}, map[string]int64{n0: 5}},
		{"queue success then a later push skipped cites the success", []CheckRun{mk(3, n0, "completed", "success", CIApp), mk(4, n1, "completed", "success", CIApp), mk(8, n0, "completed", "skipped", CIApp), mk(9, n1, "completed", "skipped", CIApp)}, map[string]int64{n0: 3, n1: 4}},
		{"the highest id among successes", []CheckRun{mk(2, n0, "completed", "success", CIApp), mk(7, n0, "completed", "success", CIApp)}, map[string]int64{n0: 7}},
		{"another app is never cited", []CheckRun{mk(2, n0, "completed", "success", "someone-else")}, map[string]int64{}},
		{"a failed run is not cited", []CheckRun{mk(2, n0, "completed", "failure", CIApp)}, map[string]int64{}},
		{"an unfinished run is not cited", []CheckRun{mk(2, n0, "in_progress", "", CIApp)}, map[string]int64{}},
		{"a run for another commit is not cited", []CheckRun{func() CheckRun { r := mk(2, n0, "completed", "success", CIApp); r.HeadSHA = fp("b"); return r }()}, map[string]int64{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CitedRuns(c.runs, x)
			if len(got) != len(c.want) {
				t.Fatalf("got %d cited runs %+v, want %v", len(got), got, c.want)
			}
			for _, r := range got {
				if c.want[r.Name] != r.ID {
					t.Errorf("cited %s run %d, want %d", r.Name, r.ID, c.want[r.Name])
				}
			}
		})
	}
	got := CitedRuns(append(runsFor(x, allOK()), skippedAt(x, 10)...), x)
	if len(got) != len(RequiredChecks) || got[0].Name != RequiredChecks[0] {
		t.Errorf("citations are not in RequiredChecks order: %+v", got)
	}
}

func TestCheckCIWalkCited(t *testing.T) {
	x, p1, p2 := fp("a"), fp("b"), fp("c")
	docs, code := []string{"README.md"}, []string{"internal/x/y.go"}
	byS := map[string][]CheckRun{x: runsFor(x, allSkipped()), p1: nil, p2: runsFor(p2, allOK())}
	chain := []Commit{{x, docs}, {p1, docs}, {p2, code}}
	cited, at, above, err := CheckCIWalkCited(x, chain, func(sha string) ([]CheckRun, error) { return byS[sha], nil })
	if err != nil || at != p2 || above != 2 || len(cited) != len(RequiredChecks) {
		t.Fatalf("got cited=%v at=%s above=%d err=%v, want the ancestor %s with 2 above", cited, at, above, err, p2)
	}
	for _, c := range cited {
		if c.HeadSHA != p2 {
			t.Errorf("cited a run of %s, want %s", c.HeadSHA, p2)
		}
	}
	byS[x] = runsFor(x, allOK())
	cited, at, above, err = CheckCIWalkCited(x, chain[:1], func(sha string) ([]CheckRun, error) { return byS[sha], nil })
	if err != nil || at != x || above != 0 || len(cited) != len(RequiredChecks) {
		t.Fatalf("direct: got cited=%v at=%s above=%d err=%v", cited, at, above, err)
	}
}
