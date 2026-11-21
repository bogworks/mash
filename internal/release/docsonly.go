package release

import (
	"errors"
	"fmt"
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

// Commit is one commit of main's first-parent history with the paths it changed against its first parent.
type Commit struct {
	SHA   string
	Paths []string
}

// MaxDocsWalk bounds how many commits the gate walks back from a docs-only candidate to a tested commit.
const MaxDocsWalk = 50

// untested reports whether every required check is absent or skipped on commit x (its heavy jobs were skipped
// because the change was docs-only), with nothing else wrong. A failure, a cancellation, a spoof or a run for
// another commit is not "untested": it is returned as an error and the commit is refused.
func untested(runs []CheckRun, x string) (bool, error) {
	if err := CheckCI(runs, x); err == nil {
		return false, nil
	}
	latest := map[string]CheckRun{}
	for _, r := range runs {
		if r.HeadSHA != x {
			return false, fmt.Errorf("CI check run %q (id %d) is for commit %q, not %s", r.Name, r.ID, r.HeadSHA, x)
		}
		if r.App.Slug != CIApp {
			for _, n := range RequiredChecks {
				if r.Name == n {
					return false, fmt.Errorf("CI check run %q (id %d) comes from app %q, not %s", r.Name, r.ID, r.App.Slug, CIApp)
				}
			}
			continue
		}
		if cur, ok := latest[r.Name]; !ok || r.ID > cur.ID {
			latest[r.Name] = r
		}
	}
	for _, name := range RequiredChecks {
		r, ok := latest[name]
		if ok && !(r.Status == "completed" && r.Conclusion == "skipped") {
			return false, fmt.Errorf("CI check %q: status %q, conclusion %q", name, r.Status, r.Conclusion)
		}
	}
	return true, nil
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
		skipped, uerr := untested(runs, c.SHA)
		if uerr != nil {
			if i == 0 {
				return CheckCI(runs, c.SHA) // the candidate's own problem, worded as before
			}
			return fmt.Errorf("commit %s on the way back: %w", short(c.SHA), uerr)
		}
		if !skipped {
			if err := CheckCI(runs, c.SHA); err != nil {
				return err
			}
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
