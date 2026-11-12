package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proseWords(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) + "." }

// goodSection is a section body that passes the opening test.
func goodSection() string {
	return "### Summary\n\n" + proseWords(45) + "\n\n### Upgrading\n\nNothing to do.\n\n### Added\n\n- One thing.\n"
}

func changelogWith(sections ...[2]string) string {
	cl := "# Changelog\n\n## [Unreleased]\n\n"
	for _, s := range sections {
		cl += "## [" + s[0] + "] - 2026-11-12\n\n" + s[1] + "\n"
	}
	return cl
}

// notesRepo is a repository whose one commit holds CHANGELOG.md and, when cutover is not empty, docs/release-cutover.
func notesRepo(t *testing.T, cutover, changelog string) (Options, string) {
	t.Helper()
	var body *string
	if cutover != "" {
		body = strp(cutover + "\n")
	}
	repo, x := cutoverRepo(t, body)
	write(t, repo, "CHANGELOG.md", changelog)
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "changelog")
	x = gitT(t, repo, "rev-parse", "HEAD")
	return Options{Repo: repo}, x
}

func TestChangelogSectionOpens(t *testing.T) {
	good := goodSection()
	short := "### Summary\n\n" + proseWords(39)[:len(proseWords(39))-1] + "\n\n### Upgrading\n\nNothing to do.\n"
	cases := []struct {
		name    string
		cutover string
		version string
		cl      string
		want    string // "" passes
	}{
		{"a good section at the cutover passes", "v0.4.0", "0.4.0", changelogWith([2]string{"0.4.0", good}), ""},
		{"a good section above the cutover passes", "v0.4.0", "0.4.1", changelogWith([2]string{"0.4.1", good}), ""},
		{"a short summary is refused", "v0.4.0", "0.4.0", changelogWith([2]string{"0.4.0", short}), "39 words"},
		{"a laundry list is refused", "v0.4.0", "0.4.0", changelogWith([2]string{"0.4.0", "### Added\n\n- a\n- b\n"}), "not \"### Summary\""},
		{"a missing Upgrading is refused", "v0.4.0", "0.4.0", changelogWith([2]string{"0.4.0", "### Summary\n\n" + proseWords(45) + "\n\n### Added\n\n- a\n"}), "no \"### Upgrading\""},
		{"the placeholder is refused", "v0.4.0", "0.4.0", changelogWith([2]string{"0.4.0", "### Summary\n\nREPLACE THIS PARAGRAPH: " + proseWords(45) + "\n\n### Upgrading\n\nNothing to do.\n"}), "REPLACE THIS"},
		{"no section for the version is refused", "v0.4.0", "0.4.1", changelogWith([2]string{"0.4.0", good}), "has no section"},
		{"a version below the cutover is not judged", "v0.4.0", "0.3.3", changelogWith([2]string{"0.3.3", "### Added\n\n- a\n"}), ""},
		{"a version below the cutover with no section is not judged", "v0.4.0", "0.3.3", changelogWith([2]string{"0.4.0", good}), ""},
		{"no cutover record is not judged", "", "0.4.0", changelogWith([2]string{"0.4.0", "### Added\n\n- a\n"}), ""},
		{"an older neighbouring section is not judged", "v0.4.0", "0.4.1", changelogWith([2]string{"0.4.1", good}, [2]string{"0.4.0", "### Added\n\n- a\n"}, [2]string{"0.3.3", "- b\n"}), ""},
		{"a bad neighbour does not excuse the version", "v0.4.0", "0.4.1", changelogWith([2]string{"0.4.1", "### Added\n\n- a\n"}, [2]string{"0.4.0", good}), "not \"### Summary\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, x := notesRepo(t, c.cutover, c.cl)
			o.Version = c.version
			err := changelogSectionOpens(o, x)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
	t.Run("the working tree is never read", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", changelogWith([2]string{"0.4.0", "### Added\n\n- a\n"}))
		o.Version = "0.4.0"
		write(t, o.Repo, "CHANGELOG.md", changelogWith([2]string{"0.4.0", goodSection()}))
		if err := changelogSectionOpens(o, x); err == nil {
			t.Fatal("the working tree's CHANGELOG.md was read")
		}
	})
	t.Run("a missing CHANGELOG.md is refused", func(t *testing.T) {
		repo, x := cutoverRepo(t, strp("v0.4.0\n"))
		if err := changelogSectionOpens(Options{Repo: repo, Version: "0.4.0"}, x); err == nil || !strings.Contains(err.Error(), "CHANGELOG.md is missing") {
			t.Fatalf("got %v", err)
		}
	})
}

// A step in the pre-tag gate list judges the section: a candidate with a bad section fails Check.
func TestCheck_changelogSectionStep(t *testing.T) {
	f := newFixture(t)
	f.o.Version = "0.1.0"
	write(t, f.repo, cutoverFile, "v0.1.0\n")
	write(t, f.repo, "CHANGELOG.md", changelogWith([2]string{"0.1.0", "### Added\n\n- a\n"}))
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "-m", "bad notes")
	gitT(t, f.repo, "push", "-q", "origin", "main")
	f.o.X = gitT(t, f.repo, "rev-parse", "HEAD")
	f.setCI()
	err := f.check()
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("err = %v", err)
	}
	out := f.o.Out.(interface{ String() string }).String()
	if !strings.Contains(out, "FAIL  the change log section for 0.1.0 opens with a Summary and an Upgrading section") {
		t.Fatalf("the step is not reported:\n%s", out)
	}
}

// fakeNotesGH serves `release view <tag> --json body` and `release list`.
func fakeNotesGH(t *testing.T, body string, releases []releaseRow) string {
	t.Helper()
	dir := t.TempDir()
	bj, _ := json.Marshal(map[string]string{"body": body})
	rj, _ := json.Marshal(releases)
	write(t, dir, "body.json", string(bj))
	write(t, dir, "releases.json", string(rj))
	gh := filepath.Join(dir, "gh")
	write(t, dir, "gh", "#!/bin/sh\ncase \"$1 $2\" in\n  \"release view\") cat '"+dir+"/body.json' ;;\n  \"release list\") cat '"+dir+"/releases.json' ;;\n  *) echo \"unexpected gh $*\" >&2; exit 1 ;;\nesac\n")
	if err := os.Chmod(gh, 0o755); err != nil {
		t.Fatal(err)
	}
	return gh
}

func TestReleaseBodyReads(t *testing.T) {
	link := "Full changes: [v0.4.0...v0.4.1](https://github.com/bogworks/mash/compare/v0.4.0...v0.4.1)\n"
	good := goodSection() + "\n" + link
	published := []releaseRow{{TagName: "v0.4.0"}, {TagName: "v0.3.3"}, {TagName: "v0.4.2", IsDraft: true}}
	cases := []struct {
		name    string
		cutover string
		tag     string
		body    string
		want    string // "" passes
	}{
		{"a good body passes", "v0.4.0", "v0.4.1", good, ""},
		{"no summary", "v0.4.0", "v0.4.1", "### Added\n\n- a\n\n" + link, "not \"### Summary\""},
		{"a short summary", "v0.4.0", "v0.4.1", strings.Replace(good, proseWords(45), proseWords(10), 1), "10 words"},
		{"no Upgrading", "v0.4.0", "v0.4.1", "### Summary\n\n" + proseWords(45) + "\n\n" + link, "no \"### Upgrading\""},
		{"no compare link", "v0.4.0", "v0.4.1", goodSection(), "no link to the full changes"},
		{"the compare link names the wrong previous release", "v0.4.0", "v0.4.1", strings.ReplaceAll(good, "v0.4.0...", "v0.3.3..."), "compare/v0.4.0...v0.4.1"},
		{"a line ending in a commit hash", "v0.4.0", "v0.4.1", good + "- fix the thing (a1b2c3d)\n", "commit hash"},
		{"the placeholder", "v0.4.0", "v0.4.1", good + "REPLACE THIS\n", "REPLACE THIS"},
		{"the cutover release is not judged", "v0.4.0", "v0.4.0", "- raw (a1b2c3d)\n", ""},
		{"a release below the cutover is not judged", "v0.4.0", "v0.3.3", "- raw (a1b2c3d)\n", ""},
		{"no cutover record is not judged", "", "v0.4.1", "- raw (a1b2c3d)\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, x := notesRepo(t, c.cutover, "# Changelog\n")
			o.GH, o.GHRepo, o.Version = fakeNotesGH(t, c.body, published), "bogworks/mash", strings.TrimPrefix(c.tag, "v")
			err := releaseBodyReads(o, x, c.tag)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
	t.Run("with no earlier release only a compare link is required", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", "# Changelog\n")
		o.GH, o.GHRepo, o.Version = fakeNotesGH(t, good, nil), "bogworks/mash", "0.4.1"
		if err := releaseBodyReads(o, x, "v0.4.1"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an unreadable body is the gate's failure, not a refusal", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", "# Changelog\n")
		o.GH, o.GHRepo, o.Version = "/nonexistent/gh", "bogworks/mash", "0.4.1"
		var oe opError
		if err := releaseBodyReads(o, x, "v0.4.1"); err == nil || !asOp(err, &oe) {
			t.Fatalf("got %v", err)
		}
	})
}

func asOp(err error, target *opError) bool {
	for err != nil {
		if e, ok := err.(opError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestPreviousRelease(t *testing.T) {
	rs := []releaseRow{{TagName: "v0.3.9"}, {TagName: "v0.3.10"}, {TagName: "v0.4.2", IsDraft: true}, {TagName: "v0.5.0"}, {TagName: "nightly"}}
	if got := previousRelease("v0.4.1", rs); got != "0.3.10" {
		t.Errorf("got %q", got)
	}
	if got := previousRelease("v0.3.9", nil); got != "" {
		t.Errorf("got %q", got)
	}
}

// --assets judges the body of a release strictly above the cutover, through the same gate publish runs.
func TestCheckAssets_releaseBodyScope(t *testing.T) {
	setBody := func(t *testing.T, f *fixture, body string) {
		t.Helper()
		path := filepath.Join(f.ghDir(), "assets.json")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		m["body"] = body
		nb, _ := json.Marshal(m)
		write(t, f.ghDir(), "assets.json", string(nb))
	}
	good := goodSection() + "\nFull changes: [v0.0.9...v0.1.0](https://github.com/bogworks/mash/compare/v0.0.9...v0.1.0)\n"
	t.Run("a body that is a commit list is refused above the cutover", func(t *testing.T) {
		f := keylessFixture(t)
		f.makeKeyless(t, "v0.0.9") // v0.1.0 is strictly above it
		setAssets(t, f, nil)
		setBody(t, f, "# v0.1.0\n\n- raw subject (a1b2c3d)\n")
		err := checkAssets(context.Background(), f.o)
		out := f.o.Out.(interface{ String() string }).String()
		if err == nil || exitCode(err) != 3 || !strings.Contains(out, "FAIL  the release notes open") {
			t.Fatalf("err = %v\n%s", err, out)
		}
	})
	t.Run("a good body passes above the cutover", func(t *testing.T) {
		f := keylessFixture(t)
		f.makeKeyless(t, "v0.0.9")
		setAssets(t, f, nil)
		setBody(t, f, good)
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatalf("%v\n%s", err, f.o.Out.(interface{ String() string }).String())
		}
	})
	t.Run("the cutover release itself is not judged", func(t *testing.T) {
		f := keylessFixture(t) // cutover v0.1.0, version 0.1.0
		setBody(t, f, "# v0.1.0\n\n- raw subject (a1b2c3d)\n")
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatalf("%v\n%s", err, f.o.Out.(interface{ String() string }).String())
		}
	})
}
