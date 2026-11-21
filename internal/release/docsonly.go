package release

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// IsDocsPath reports whether a repository path is documentation that no test, build or release tool reads. It is
// scripts/ci-changes' is_docs, ported; docsonly_test.go runs both over the same table so they cannot drift. Everything
// not listed is code: the default is code, so a new top-level path is code.
func IsDocsPath(p string) bool {
	p = strings.TrimSpace(p)
	lower := strings.ToLower(p)
	for _, pre := range []string{".github/", ".claude-plugin/", "plugin/", "scripts/", "cmd/", "internal/", "ci/", "testdata/", "changelog.d/", "spec/", "docs/review/", "docs/reference/"} {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	// Agent instructions: a skill or a briefing an agent executes is code in effect (ADR 0157), wherever it sits.
	if isAgentInstruction(lower) {
		return false
	}
	switch p {
	case ".mergify.yml", "go.mod", "go.sum", "CHANGELOG.md", "docs/branch-protection.md", "docs/allowed_signers", "docs/skipped-releases.txt", "docs/release-cutover":
		return false
	}
	if strings.HasSuffix(p, "/go.mod") || strings.HasSuffix(p, "/go.sum") || strings.HasSuffix(p, ".go") {
		return false
	}
	if strings.Contains(p, "/testdata/") || strings.Contains(p, "conformance") {
		return false
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".ico", ".pdf", ".woff", ".woff2"} {
		if strings.HasPrefix(lower, "docs/") && strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return strings.HasPrefix(p, "docs/") || strings.HasSuffix(p, ".md")
}

// isAgentInstruction: a path an agent reads as instructions is code in effect (ADR 0157), wherever it sits and in any letter case
// (lower is the lower-cased path): the named files, any *.tmpl, mash-setup/, and any path with a directory component named like
// the places agents read instructions. Same rule as scripts/ci-changes.
func isAgentInstruction(lower string) bool {
	base := lower[strings.LastIndex(lower, "/")+1:]
	switch base {
	case "agents.md", "claude.md", "gemini.md", "skill.md", ".cursorrules", ".windsurfrules":
		return true
	}
	if strings.HasSuffix(base, ".tmpl") || strings.HasPrefix(lower, "mash-setup/") || strings.Contains(lower, "/mash-setup/") {
		return true
	}
	if i := strings.LastIndex(lower, "/"); i >= 0 {
		for _, c := range strings.Split(lower[:i], "/") {
			switch c {
			case "skills", "commands", "agents", "hooks", "prompts", "briefings", "plugins", ".claude-plugin":
				return true
			}
		}
	}
	return false
}

// Commit is one commit of main's first-parent history with the paths it changed against its first parent.
type Commit struct {
	SHA   string
	Paths []string
}

// MaxDocsWalk bounds how many commits the gate walks back from a docs-only candidate to a tested commit.
const MaxDocsWalk = 50

// ciState is what a commit's check runs say about the required checks.
type ciState int

const (
	ciPassed   ciState = iota // every required check has a run that tested and succeeded
	ciUntested                // no required check has a run that tested: every run is skipped, or there is none
)

// evaluate judges one commit's check runs as CheckCI's validation does (a run for another commit, a spoofed app, one run id
// reported twice with different results are refused FIRST), then per required check takes the latest run that actually tested,
// that is not skipped. A later skipped run (a re-run of a docs-only workflow) never hides an earlier failure: a failure stands
// until a newer run that tested supersedes it. Passed: every required check's latest tested run succeeded. Untested: none has
// a tested run. Anything else is an error: a failed or unfinished tested run, a spoof, a duplicate id, one check tested and
// another missing.
func evaluate(runs []CheckRun, x string) (ciState, error) {
	byID := map[int64]CheckRun{}
	tested := map[string]CheckRun{}
	var problems []string
	for _, r := range runs {
		if r.HeadSHA != x {
			problems = append(problems, fmt.Sprintf("CI check run %q (id %d) is for commit %q, not %s", r.Name, r.ID, r.HeadSHA, x))
			continue
		}
		if r.App.Slug != CIApp {
			if slices.Contains(RequiredChecks, r.Name) {
				problems = append(problems, fmt.Sprintf("CI check run %q (id %d) comes from app %q, not %s", r.Name, r.ID, r.App.Slug, CIApp))
			}
			continue
		}
		if prev, ok := byID[r.ID]; ok && prev != r {
			problems = append(problems, fmt.Sprintf("CI check run id %d is reported twice with different results", r.ID))
			continue
		}
		byID[r.ID] = r
	}
	if len(problems) > 0 {
		return 0, joined(problems)
	}
	for _, r := range byID {
		if !slices.Contains(RequiredChecks, r.Name) || (r.Status == "completed" && r.Conclusion == "skipped") {
			continue
		}
		if cur, ok := tested[r.Name]; !ok || r.ID > cur.ID {
			tested[r.Name] = r
		}
	}
	if len(tested) == 0 {
		return ciUntested, nil
	}
	for _, name := range RequiredChecks {
		r, ok := tested[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("CI check %q: no run that tested on this commit", name))
		case r.Status != "completed" || r.Conclusion != "success":
			problems = append(problems, fmt.Sprintf("CI check %q: status %q, conclusion %q", name, r.Status, r.Conclusion))
		}
	}
	if len(problems) > 0 {
		return 0, joined(problems)
	}
	return ciPassed, nil
}

// CheckCIWalk is CheckCI for a candidate that may be docs-only. If x's required checks passed, it is CheckCI. If they
// were skipped or never ran, x must change only documentation (IsDocsPath), and so must every commit back along main's
// first parent until one whose required checks ran and succeeded; that commit decides. A docs-only commit therefore
// can neither break the gate nor stand in for tests: the code it carries is the tested ancestor's code. A code commit
// without passing checks, a failed or cancelled run, a spoofed run, or no tested ancestor within MaxDocsWalk is refused.
// chain lists x first, then its first parents, newest first; runsFor reads one commit's check runs.
func CheckCIWalk(x string, chain []Commit, runsFor func(sha string) ([]CheckRun, error)) error {
	if len(chain) == 0 || chain[0].SHA != x {
		return errors.New("the first-parent chain does not start at the candidate")
	}
	for i, c := range chain {
		if i >= MaxDocsWalk {
			return fmt.Errorf("CI: no commit with passing required checks within %d commits of the candidate", MaxDocsWalk)
		}
		runs, err := runsFor(c.SHA)
		if err != nil {
			return err
		}
		state, eerr := evaluate(runs, c.SHA)
		if eerr != nil {
			if i == 0 {
				return eerr
			}
			return fmt.Errorf("commit %s on the way back: %w", short(c.SHA), eerr)
		}
		if state == ciPassed {
			return nil // tested, and every commit above it was docs-only
		}
		if len(c.Paths) == 0 { // scripts/ci-changes: an empty path list is code
			return fmt.Errorf("commit %s has no passing checks and no changed paths to judge as docs-only", short(c.SHA))
		}
		for _, p := range c.Paths {
			if !IsDocsPath(p) {
				if i == 0 {
					return fmt.Errorf("CI check runs did not run on the candidate and it is not docs-only (%s)", p)
				}
				return fmt.Errorf("commit %s on the way back has no passing checks and is not docs-only (%s)", short(c.SHA), p)
			}
		}
	}
	return fmt.Errorf("CI: no commit with passing required checks in the %d commits given", len(chain))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
