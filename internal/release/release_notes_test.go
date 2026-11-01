package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func needJQ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
}

func runScript(t *testing.T, dir, script string, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", script), args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatal(err)
	return "", -1
}

func TestDraftGuard(t *testing.T) {
	needJQ(t)
	const (
		noDrafts   = `[{"tagName":"v0.2.8","isDraft":false}]`
		lowerDraft = `[{"tagName":"v0.2.9","isDraft":true},{"tagName":"v0.2.8","isDraft":true},{"tagName":"v0.2.10","isDraft":false}]`
		sameDraft  = `[{"tagName":"v0.2.10","isDraft":true},{"tagName":"v0.2.9","isDraft":false}]`
		higher     = `[{"tagName":"v0.3.0","isDraft":true}]`
		nonSemver  = `[{"tagName":"nightly","isDraft":true},{"tagName":"v0.2","isDraft":true},{"tagName":"v0.2.10-rc1","isDraft":true},{"tagName":"v0.1.0.1","isDraft":true}]`
		published  = `[{"tagName":"v0.2.9","isDraft":false}]`
	)
	cases := []struct {
		name, json, tag string
		want            int
		mention         []string
	}{
		{"empty list", `[]`, "v0.2.10", 0, nil},
		{"no drafts", noDrafts, "v0.2.10", 0, nil},
		{"lower draft refused, numerically (v0.2.9 < v0.2.10)", lowerDraft, "v0.2.10", 1, []string{"v0.2.9", "v0.2.8"}},
		{"same-tag draft ok", sameDraft, "v0.2.10", 0, nil},
		{"higher draft ok", higher, "v0.2.10", 0, nil},
		{"non-semver ignored", nonSemver, "v0.2.10", 0, nil},
		{"published lower ok", published, "v0.2.10", 0, nil},
		{"lower draft refused across major", `[{"tagName":"v1.9.9","isDraft":true}]`, "v2.0.0", 1, []string{"v1.9.9"}},
		{"bad tag", `[]`, "v0.2", 2, nil},
		{"bad json", `nope`, "v0.2.10", 2, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := runScript(t, t.TempDir(), "draft-guard", c.json, c.tag)
			if code != c.want {
				t.Fatalf("exit %d, want %d\n%s", code, c.want, out)
			}
			for _, m := range c.mention {
				if !strings.Contains(out, m) {
					t.Errorf("message does not name %s:\n%s", m, out)
				}
			}
		})
	}
	t.Run("file argument", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "r.json")
		if err := os.WriteFile(f, []byte(lowerDraft), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, code := runScript(t, dir, "draft-guard", "", "v0.2.10", f); code != 1 {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
}

// runAfterCreate runs `draft-guard --after-create <tag>` against a fake gh that serves list and logs every call.
func runAfterCreate(t *testing.T, tag, listJSON string) (out string, code int, ghLog string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(dir, "list.json")
	log := filepath.Join(dir, "gh.log")
	if err := os.WriteFile(list, []byte(listJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\necho \"$*\" >> " + log + "\nif [ \"$1 $2\" = \"release list\" ]; then cat " + list + "; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "draft-guard"), "--after-create", tag)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	b, err := cmd.CombinedOutput()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	l, _ := os.ReadFile(log)
	return string(b), code, string(l)
}

// --after-create never deletes: on a conflict it names the release in the way, leaves the draft and exits 1.
func TestDraftGuardAfterCreate(t *testing.T) {
	needJQ(t)
	pair := `[{"tagName":"v0.2.10","isDraft":true},{"tagName":"v0.2.9","isDraft":true}]`
	conflict := func(t *testing.T, tag, list, names string) {
		t.Helper()
		out, code, log := runAfterCreate(t, tag, list)
		if code != 1 || strings.Contains(log, "release delete") || strings.Contains(log, "cleanup-tag") {
			t.Fatalf("exit %d, gh calls:\n%s\n%s", code, log, out)
		}
		if !strings.Contains(out, names) || !strings.Contains(out, "left in place") || !strings.Contains(out, "gh release delete "+tag) {
			t.Errorf("message:\n%s", out)
		}
	}
	t.Run("a lower draft: exit 1, nothing deleted", func(t *testing.T) { conflict(t, "v0.2.10", pair, "draft v0.2.9") })
	t.Run("a higher published release: exit 1, nothing deleted", func(t *testing.T) {
		conflict(t, "v0.2.10", `[{"tagName":"v0.2.10","isDraft":true},{"tagName":"v0.3.0","isDraft":false}]`, "published v0.3.0")
	})
	t.Run("the lower of a racing pair: exit 0, nothing deleted", func(t *testing.T) {
		out, code, log := runAfterCreate(t, "v0.2.9", pair)
		if code != 0 || strings.Contains(log, "release delete") {
			t.Fatalf("exit %d, gh calls:\n%s\n%s", code, log, out)
		}
	})
	t.Run("no conflicting release: exit 0, nothing deleted", func(t *testing.T) {
		out, code, log := runAfterCreate(t, "v0.2.10", `[{"tagName":"v0.2.10","isDraft":true},{"tagName":"v0.2.9","isDraft":false},{"tagName":"v0.3.0","isDraft":true},{"tagName":"nightly","isDraft":true}]`)
		if code != 0 || strings.Contains(log, "release delete") {
			t.Fatalf("exit %d, gh calls:\n%s\n%s", code, log, out)
		}
	})
}

func TestNotesBase(t *testing.T) {
	needJQ(t)
	dir := t.TempDir()
	g := func(args ...string) string {
		t.Helper()
		out, err := testenv.RunGit(append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "tag.gpgSign=false", "-c", "commit.gpgSign=false"}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g("init", "-q", "-b", "main")
	commit := func(msg string) { g("commit", "-q", "--allow-empty", "-m", msg) }
	// main: a(v0.2.8 draft) - b(v0.2.9 draft) - c(v0.2.10 published) - d(v0.2.11, being released)
	// side branch off a: s(v0.2.12 published, not an ancestor of d)
	commit("a")
	g("tag", "v0.2.8")
	g("branch", "side")
	commit("b")
	g("tag", "v0.2.9")
	commit("c")
	g("tag", "v0.2.10")
	commit("d")
	g("tag", "v0.2.11")
	g("checkout", "-q", "side")
	commit("s")
	g("tag", "v0.2.12")
	g("tag", "v0.2.13-rc1")
	g("checkout", "-q", "main")
	d := g("rev-parse", "HEAD")

	rel := func(tag string, draft, pre bool) string {
		b := func(v bool) string {
			if v {
				return "true"
			}
			return "false"
		}
		return `{"tagName":"` + tag + `","isDraft":` + b(draft) + `,"isPrerelease":` + b(pre) + `}`
	}
	list := func(rs ...string) string { return "[" + strings.Join(rs, ",") + "]" }
	cases := []struct {
		name, json, want string
	}{
		{"none published: whole history", list(rel("v0.2.8", true, false), rel("v0.2.11", true, false)), ""},
		{"empty list", `[]`, ""},
		{"drafts skipped, highest published ancestor wins", list(rel("v0.2.8", false, false), rel("v0.2.9", true, false), rel("v0.2.10", false, false), rel("v0.2.11", true, false)), "v0.2.10"},
		{"unpublished tag in between is not the base", list(rel("v0.2.8", false, false), rel("v0.2.9", true, false), rel("v0.2.11", true, false)), "v0.2.8"},
		{"numeric order (v0.2.10 > v0.2.9)", list(rel("v0.2.9", false, false), rel("v0.2.10", false, false)), "v0.2.10"},
		{"non-ancestor skipped", list(rel("v0.2.10", false, false), rel("v0.2.12", false, false)), "v0.2.10"},
		{"prerelease and non-semver skipped", list(rel("v0.2.8", false, false), rel("v0.2.10", false, true), rel("nightly", false, false)), "v0.2.8"},
		{"published tag unknown locally skipped", list(rel("v0.2.8", false, false), rel("v9.9.9", false, false)), "v0.2.8"},
		{"the tag itself is never its own base", list(rel("v0.2.11", false, false), rel("v0.2.9", false, false)), "v0.2.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := runScript(t, dir, "notes-base", c.json, "v0.2.11", d)
			if code != 0 || strings.TrimSpace(out) != c.want {
				t.Fatalf("exit %d, got %q, want %q", code, out, c.want)
			}
		})
	}
	if out, code := runScript(t, dir, "notes-base", "nope", "v0.2.11", d); code != 2 {
		t.Fatalf("bad json: exit %d\n%s", code, out)
	}
}
