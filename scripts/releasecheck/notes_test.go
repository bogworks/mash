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
	wrongLink := strings.ReplaceAll(link, "v0.4.0...", "v0.3.3...")
	sec := goodSection() // the change-log section at X, as releasenotes.SectionBody returns it
	published := []releaseRow{{TagName: "v0.4.0"}, {TagName: "v0.3.3"}, {TagName: "v0.4.2", IsDraft: true}}
	const same = "same text"
	cases := []struct {
		name    string
		cutover string // "" is v0.4.0, "-" is no record
		tag     string // "" is v0.4.1
		sec     string // the section in CHANGELOG.md at X; "" is a good one
		body    string // the release body; "" is sec, then a blank line and tail when tail is not empty
		tail    string
		want    string // "" passes
		notWant string // a substring the error must not hold
	}{
		{name: "a good body passes", tail: link},
		{name: "the section alone with no compare link: refused for the link only", want: "no link to the full changes", notWant: same},
		{name: "trailing blank space is tolerated", tail: link + "\n  \n\n"},
		{name: "CRLF line endings are tolerated", body: strings.ReplaceAll(sec+"\n"+link, "\n", "\r\n")},
		{name: "no summary", sec: "### Added\n\n- a\n", tail: link, want: "not \"### Summary\"", notWant: same},
		{name: "a short summary", sec: strings.Replace(sec, proseWords(45), proseWords(10), 1), tail: link, want: "10 words", notWant: same},
		{name: "no Upgrading", sec: "### Summary\n\n" + proseWords(45) + "\n", tail: link, want: "no \"### Upgrading\"", notWant: same},
		{name: "the compare link names the wrong previous release", tail: wrongLink, want: "compare/v0.4.0...v0.4.1", notWant: same},
		{name: "a line ending in a commit hash", sec: sec + "- fix the thing (a1b2c3d)\n", tail: link, want: "commit hash", notWant: same},
		{name: "the placeholder", sec: sec + "REPLACE THIS\n", tail: link, want: "REPLACE THIS", notWant: same},
		{name: "edited prose that is otherwise valid", body: strings.Replace(sec, proseWords(45), strings.TrimSpace(strings.Repeat("other ", 45))+".", 1) + "\n" + link, want: same},
		{name: "an extra paragraph after the Full changes paragraph", tail: link + "\nAnother paragraph.\n", want: same},
		{name: "an extra paragraph before the Full changes paragraph", tail: "Another paragraph.\n\n" + link, want: same},
		{name: "a second Full changes line in the same paragraph", tail: link + link, want: same},
		{name: "the cutover release is not judged", tag: "v0.4.0", body: "- raw (a1b2c3d)\n"},
		{name: "a release below the cutover is not judged", tag: "v0.3.3", body: "- raw (a1b2c3d)\n"},
		{name: "no cutover record is not judged", cutover: "-", body: "- raw (a1b2c3d)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cutover, tag, csec, body := c.cutover, c.tag, c.sec, c.body
			switch cutover {
			case "":
				cutover = "v0.4.0"
			case "-":
				cutover = ""
			}
			if tag == "" {
				tag = "v0.4.1"
			}
			if csec == "" {
				csec = sec
			}
			if body == "" {
				body = csec
				if c.tail != "" {
					body += "\n" + c.tail
				}
			}
			version := strings.TrimPrefix(tag, "v")
			o, x := notesRepo(t, cutover, changelogWith([2]string{version, csec}))
			o.GH, o.GHRepo, o.Version = fakeNotesGH(t, body, published), "bogworks/mash", version
			err := releaseBodyReads(o, x, tag)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if c.notWant != "" && strings.Contains(err.Error(), c.notWant) {
				t.Fatalf("got %v, which must not hold %q", err, c.notWant)
			}
		})
	}
	t.Run("with no earlier release only a compare link is required", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", changelogWith([2]string{"0.4.1", sec}))
		o.GH, o.GHRepo, o.Version = fakeNotesGH(t, sec+"\n"+link, nil), "bogworks/mash", "0.4.1"
		if err := releaseBodyReads(o, x, "v0.4.1"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a CHANGELOG.md at X with no section for the version is refused", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", changelogWith([2]string{"0.4.0", sec}))
		o.GH, o.GHRepo, o.Version = fakeNotesGH(t, sec+"\n"+link, published), "bogworks/mash", "0.4.1"
		if err := releaseBodyReads(o, x, "v0.4.1"); err == nil || !strings.Contains(err.Error(), "has no section") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an unreadable body is the gate's failure, not a refusal", func(t *testing.T) {
		o, x := notesRepo(t, "v0.4.0", changelogWith([2]string{"0.4.1", sec}))
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
	sec := goodSection()
	// X must hold the change-log section the body is compared with.
	withChangelog := func(f *fixture) {
		write(t, f.repo, "CHANGELOG.md", changelogWith([2]string{"0.1.0", sec}))
	}
	good := sec + "\nFull changes: [v0.0.9...v0.1.0](https://github.com/bogworks/mash/compare/v0.0.9...v0.1.0)\n"
	t.Run("a body that is a commit list is refused above the cutover", func(t *testing.T) {
		f := keylessFixture(t)
		withChangelog(f)
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
		withChangelog(f)
		f.makeKeyless(t, "v0.0.9")
		setAssets(t, f, nil)
		setBody(t, f, good)
		if err := checkAssets(context.Background(), f.o); err != nil {
			t.Fatalf("%v\n%s", err, f.o.Out.(interface{ String() string }).String())
		}
	})
	t.Run("a body edited away from the change-log section is refused above the cutover", func(t *testing.T) {
		f := keylessFixture(t)
		withChangelog(f)
		f.makeKeyless(t, "v0.0.9")
		setAssets(t, f, nil)
		setBody(t, f, strings.Replace(good, proseWords(45), "other "+proseWords(44), 1))
		err := checkAssets(context.Background(), f.o)
		out := f.o.Out.(interface{ String() string }).String()
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "same text") {
			t.Fatalf("err = %v\n%s", err, out)
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

// rule23Entry is a change-log entry body that passes releasenotes.ChangelogEntry for 0.5.7.
func rule23Entry() string {
	return "### Summary\n\n" + proseWords(45) + "\n\n### Upgrading\n\nNothing to do.\n\n### Added\n\n- One thing. [#1](https://github.com/bogworks/mash/pull/1)\n\n" +
		"Known issues: 2, [the list](https://github.com/bogworks/mash/issues?q=label%3Aknown-0.5.7)\n" +
		"Links: [release notes](https://github.com/bogworks/mash/blob/v0.5.7/docs/release-notes/0.5.7.md), [compare](https://github.com/bogworks/mash/compare/v0.5.6...v0.5.7), [verify](https://github.com/bogworks/mash/blob/v0.5.7/docs/release-notes/0.5.7.md#verify-what-you-download)\n"
}

// rule23Notes is a release notes file that shares rule23Entry's Summary and Upgrading.
func rule23Notes() string {
	return "### Summary\n\n" + proseWords(45) + "\n\n### Why this release\n\nBecause.\n\n### Upgrading\n\nNothing to do.\n\n### Verify what you download\n\nRun the check.\n"
}

// notesRepo23 is notesRepo with docs/release-notes/0.5.7.md committed too (notes "" commits none).
func notesRepo23(t *testing.T, changelog, notes string) (Options, string) {
	t.Helper()
	o, _ := notesRepo(t, "v0.4.0", changelog)
	if notes != "" {
		write(t, o.Repo, "docs/release-notes/0.5.7.md", notes)
		gitT(t, o.Repo, "add", "-A")
		gitT(t, o.Repo, "commit", "-q", "-m", "release notes")
	}
	o.Version = "0.5.7"
	return o, gitT(t, o.Repo, "rev-parse", "HEAD")
}

func TestChangelogSectionOpens_rule23(t *testing.T) {
	entry := rule23Entry()
	cases := []struct {
		name    string
		entry   string
		notes   string
		version string // "" is 0.5.7
		want    string // "" passes
	}{
		{"a conforming entry and its notes pass", entry, rule23Notes(), "", ""},
		{"an entry with no Known issues line is refused", strings.Replace(entry, "Known issues:", "Issues:", 1), rule23Notes(), "", "rubric 10"},
		{"a quick start in the entry is refused", entry + "\n```\nmash up\n```\n", rule23Notes(), "", "rubric 8"},
		{"no release notes file is refused", entry, "", "", "docs/release-notes/0.5.7.md is missing"},
		{"notes with another Summary are refused", entry, strings.Replace(rule23Notes(), "word", "other", 1), "", "Summary of the release notes file differs"},
		{"notes with another Upgrading are refused", entry, strings.Replace(rule23Notes(), "Nothing to do.", "Run it.", 1), "", "Upgrading section of the release notes file differs"},
		{"notes with a placeholder are refused", entry, rule23Notes() + "REPLACE THIS\n", "", "REPLACE THIS"},
		{"a version below 0.5.7 keeps the old rule: a bare section passes with no notes file", goodSection(), "", "0.5.6", ""},
		{"a version below 0.5.7 is not held to the rubric", goodSection() + "- no link at all\n", "", "0.5.6", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.version
			if v == "" {
				v = "0.5.7"
			}
			o, x := notesRepo23(t, changelogWith([2]string{v, c.entry}), c.notes)
			o.Version = v
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
	t.Run("the notes file is read from X, not the working tree", func(t *testing.T) {
		o, x := notesRepo23(t, changelogWith([2]string{"0.5.7", entry}), "")
		write(t, o.Repo, "docs/release-notes/0.5.7.md", rule23Notes())
		if err := changelogSectionOpens(o, x); err == nil {
			t.Fatal("the working tree's notes file was read")
		}
	})
	t.Run("the step names say what they judge", func(t *testing.T) {
		if got := changelogStepName("0.5.7"); !strings.Contains(got, "one screen") || !strings.Contains(got, "docs/release-notes/0.5.7.md") {
			t.Error(got)
		}
		if got := changelogStepName("0.5.6"); !strings.Contains(got, "opens with a Summary") {
			t.Error(got)
		}
		if got := releaseBodyStepName("0.5.7"); !strings.Contains(got, "docs/release-notes/0.5.7.md") {
			t.Error(got)
		}
	})
}

func TestReleaseBodyReads_rule23(t *testing.T) {
	link := "Full changes: [v0.5.6...v0.5.7](https://github.com/bogworks/mash/compare/v0.5.6...v0.5.7)\n"
	notes := rule23Notes()
	published := []releaseRow{{TagName: "v0.5.6"}}
	cases := []struct {
		name  string
		body  string
		notes string // the file at X; "" is rule23Notes
		want  string
	}{
		{"the notes file with the compare link passes", notes + "\n" + link, "", ""},
		{"the change log entry as the body is refused", rule23Entry() + "\n" + link, "", "not docs/release-notes/0.5.7.md"},
		{"an edited body is refused", strings.Replace(notes, "Because.", "Other.", 1) + "\n" + link, "", "not docs/release-notes/0.5.7.md"},
		{"an extra paragraph after the compare link is refused", notes + "\n" + link + "\nMore.\n", "", "not docs/release-notes/0.5.7.md"},
		{"the notes file alone lacks the compare link", notes, "", "no link to the full changes"},
		{"a placeholder in the file and body is refused", notes + "REPLACE THIS\n" + "\n" + link, notes + "REPLACE THIS\n", "REPLACE THIS"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := c.notes
			if file == "" {
				file = notes
			}
			o, x := notesRepo23(t, changelogWith([2]string{"0.5.7", rule23Entry()}), file)
			o.GH, o.GHRepo = fakeNotesGH(t, c.body, published), "bogworks/mash"
			err := releaseBodyReads(o, x, "v0.5.7")
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
	t.Run("a missing notes file is refused", func(t *testing.T) {
		o, x := notesRepo23(t, changelogWith([2]string{"0.5.7", rule23Entry()}), "")
		o.GH, o.GHRepo = fakeNotesGH(t, notes+"\n"+link, published), "bogworks/mash"
		if err := releaseBodyReads(o, x, "v0.5.7"); err == nil || !strings.Contains(err.Error(), "is missing") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("0.5.6 keeps the change-log-section rule", func(t *testing.T) {
		o, x := notesRepo23(t, changelogWith([2]string{"0.5.6", goodSection()}), "")
		o.Version = "0.5.6"
		o.GH, o.GHRepo = fakeNotesGH(t, goodSection()+"\nFull changes: [v0.5.5...v0.5.6](https://github.com/bogworks/mash/compare/v0.5.5...v0.5.6)\n", []releaseRow{{TagName: "v0.5.5"}}), "bogworks/mash"
		if err := releaseBodyReads(o, x, "v0.5.6"); err != nil {
			t.Fatal(err)
		}
	})
}
