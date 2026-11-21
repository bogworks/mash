package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// The walk through a real git history: firstParentChain reads the first-parent commits and the paths each changed against its
// first parent, release.CheckCIWalk judges them. Tested commits are the ones with passing runs; everything else is skipped.
func walkRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "r")
	gitT(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	return repo
}

func walkCommit(t *testing.T, repo string, files map[string]string, msg string) string {
	t.Helper()
	for name, body := range files {
		write(t, repo, name, body)
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", msg)
	return gitT(t, repo, "rev-parse", "HEAD")
}

func judgeWalk(t *testing.T, repo, x string, tested ...string) error {
	t.Helper()
	chain, err := firstParentChain(Options{Repo: repo}, x, release.MaxDocsWalk)
	if err != nil {
		t.Fatal(err)
	}
	ok := map[string]bool{}
	for _, s := range tested {
		ok[s] = true
	}
	return release.CheckCIWalk(x, chain, func(sha string) ([]release.CheckRun, error) {
		var out []release.CheckRun
		for i, n := range release.RequiredChecks {
			r := release.CheckRun{ID: int64(i + 1), HeadSHA: sha, Name: n, Status: "completed", Conclusion: "skipped"}
			if ok[sha] {
				r.Conclusion = "success"
			}
			r.App.Slug = release.CIApp
			out = append(out, r)
		}
		return out, nil
	})
}

func TestDocsWalk_realHistory(t *testing.T) {
	t.Run("two docs commits over a tested code commit are accepted", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n", "README.md": "r\n"}, "code")
		walkCommit(t, repo, map[string]string{"docs/faq.md": "faq\n"}, "docs 1")
		x := walkCommit(t, repo, map[string]string{"README.md": "r2\n", "docs/images/n.txt": "n\n"}, "docs 2")
		if err := judgeWalk(t, repo, x, tested); err != nil {
			t.Fatalf("want accepted: %v", err)
		}
	})
	t.Run("a code commit between is refused", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n"}, "code")
		walkCommit(t, repo, map[string]string{"internal/b.go": "package a\n"}, "untested code")
		x := walkCommit(t, repo, map[string]string{"docs/faq.md": "faq\n"}, "docs")
		err := judgeWalk(t, repo, x, tested)
		if err == nil || !strings.Contains(err.Error(), "not docs-only") {
			t.Fatalf("want a refusal for the untested code commit, got %v", err)
		}
	})
	t.Run("a merge commit is read against its first parent: a docs-only side branch is accepted", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n"}, "code")
		gitT(t, repo, "switch", "-q", "-c", "side")
		walkCommit(t, repo, map[string]string{"docs/side.md": "s\n"}, "docs on the side")
		gitT(t, repo, "switch", "-q", "main")
		walkCommit(t, repo, map[string]string{"docs/main.md": "m\n"}, "docs on main")
		gitT(t, repo, "merge", "-q", "--no-ff", "-m", "merge side", "side")
		x := gitT(t, repo, "rev-parse", "HEAD")
		if err := judgeWalk(t, repo, x, tested); err != nil {
			t.Fatalf("want accepted: %v", err)
		}
	})
	t.Run("a merge commit that brings code in is refused", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n"}, "code")
		gitT(t, repo, "switch", "-q", "-c", "side")
		walkCommit(t, repo, map[string]string{"internal/side.go": "package a\n"}, "code on the side")
		gitT(t, repo, "switch", "-q", "main")
		walkCommit(t, repo, map[string]string{"docs/main.md": "m\n"}, "docs on main")
		gitT(t, repo, "merge", "-q", "--no-ff", "-m", "merge side", "side")
		x := gitT(t, repo, "rev-parse", "HEAD")
		err := judgeWalk(t, repo, x, tested)
		if err == nil || !strings.Contains(err.Error(), "not docs-only") {
			t.Fatalf("want a refusal: the merge carries code, got %v", err)
		}
	})
	t.Run("a rename of a code file into docs is refused (both paths are listed)", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n\nfunc F() {}\n", "docs/keep.md": "k\n"}, "code")
		gitT(t, repo, "mv", "internal/a.go", "docs/a.md")
		gitT(t, repo, "commit", "-q", "-m", "rename code into docs")
		x := gitT(t, repo, "rev-parse", "HEAD")
		err := judgeWalk(t, repo, x, tested)
		if err == nil || !strings.Contains(err.Error(), "internal/a.go") {
			t.Fatalf("want a refusal naming internal/a.go, got %v", err)
		}
	})
	t.Run("a rename of a docs file between docs paths is accepted", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n", "docs/old.md": "o\n"}, "code and docs")
		gitT(t, repo, "mv", "docs/old.md", "docs/new.md")
		gitT(t, repo, "commit", "-q", "-m", "rename docs")
		x := gitT(t, repo, "rev-parse", "HEAD")
		if err := judgeWalk(t, repo, x, tested); err != nil {
			t.Fatalf("want accepted: %v", err)
		}
	})
	t.Run("agent instructions are documentation (rules 20 and 21)", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n"}, "code")
		x := walkCommit(t, repo, map[string]string{"mash-setup/skills/setup/SKILL.md": "s\n", "AGENTS.md": "a\n"}, "skill")
		if err := judgeWalk(t, repo, x, tested); err != nil {
			t.Fatalf("a Markdown change under mash-setup/ is documentation: want accepted: %v", err)
		}
	})
	t.Run("a script under mash-setup is code", func(t *testing.T) {
		repo := walkRepo(t)
		tested := walkCommit(t, repo, map[string]string{"internal/a.go": "package a\n"}, "code")
		x := walkCommit(t, repo, map[string]string{"mash-setup/setup.sh": "echo hi\n"}, "script")
		if err := judgeWalk(t, repo, x, tested); err == nil {
			t.Fatal("a non-Markdown change under mash-setup/ must not be judged docs-only")
		}
	})
}
