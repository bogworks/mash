package release

import (
	"fmt"
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
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "EVENT=pull_request", "HEAD_REF=pr/x/y"}
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
		{"an empty path list is code", []step{{x, nil, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "no changed paths"},
		{"a docs commit that touches .github is code", []step{{x, []string{"docs/a.md", ".github/workflows/ci.yml"}, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "not docs-only"},
		{"a docs commit that touches .mergify.yml or scripts is code", []step{{x, []string{"scripts/notes.md"}, runsFor(x, allSkipped())}, {p1, code, runsFor(p1, allOK())}}, "not docs-only"},
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
