// Command changelog keeps CHANGELOG.md out of pull requests (ADR 0067): a user-facing change adds a fragment file
// changelog.d/<slug>.<kind>.md, and release preparation folds the fragments into CHANGELOG.md.
//
//	changelog check [--root r] [--dir changelog.d]
//	changelog assemble --version X.Y.Z --date YYYY-MM-DD [--root r] [--dir changelog.d] [--changelog CHANGELOG.md] [--dry-run]
//
// check lints the fragments and prints one "file: message" line per violation. A fragment is named <slug>.<kind>.md,
// the slug being lower-case letters, digits, hyphens and underscores and the kind one of added, changed, deprecated,
// removed, fixed or security (the Keep a Changelog sections); it is not empty after trimming and its text does not
// start with a bullet ("- " or "* ") or a heading ("#"). README.md in the directory is ignored. A missing directory has
// no fragments.
//
// assemble folds every fragment, and every bullet already under "## [Unreleased]", into a new "## [X.Y.Z] - date"
// section placed directly below the emptied Unreleased heading. Subsections come in Keep a Changelog order (Added,
// Changed, Deprecated, Removed, Fixed, Security), then any other subsection the Unreleased section already had, in the
// order it had them; only those with entries appear. Within a subsection the bullets that were already under Unreleased
// come first, in their order, then the fragments sorted by slug; a fragment becomes "- <text>" with continuation lines
// indented by two spaces. CHANGELOG.md is written atomically first and the fragment files are removed after. It refuses,
// writing nothing, a version that already has a section, a bad version or date, a fragment that fails check, a
// CHANGELOG.md with no "## [Unreleased]" heading, and a release with nothing in it. --dry-run prints the new section.
//
// Exit 0: ok. Exit 1: check found violations, or assemble refused. Exit 2: usage or an unreadable input.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultDir       = "changelog.d"
	defaultChangelog = "CHANGELOG.md"
	unreleasedHeader = "## [Unreleased]"
)

// kinds are the fragment kinds, in the order of the sections they land in.
var kinds = []string{"added", "changed", "deprecated", "removed", "fixed", "security"}

var (
	fragmentRE = regexp.MustCompile(`^([a-z0-9_-]+)\.([a-z]+)\.md$`)
	versionRE  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	dateRE     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type env struct{ stdout, stderr io.Writer }

func main() { os.Exit(run(os.Args[1:], env{os.Stdout, os.Stderr})) }

func run(args []string, e env) int {
	if len(args) == 0 {
		return usage(e, "a mode is required")
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], e)
	case "assemble":
		return runAssemble(args[1:], e)
	}
	return usage(e, "unknown mode "+args[0])
}

func usage(e env, msg string) int {
	fmt.Fprintf(e.stderr, "changelog: %s\nusage: changelog check [--root r] [--dir changelog.d]\n       changelog assemble --version X.Y.Z --date YYYY-MM-DD [--root r] [--dir changelog.d] [--changelog CHANGELOG.md] [--dry-run]\n", msg)
	return 2
}

func runCheck(args []string, e env) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	root := fs.String("root", ".", "the tree to judge")
	dir := fs.String("dir", defaultDir, "the fragment directory (under --root unless absolute)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usage(e, "check takes [--root r] [--dir changelog.d]")
	}
	_, vs, err := readFragments(resolve(*root, *dir))
	if err != nil {
		fmt.Fprintln(e.stderr, "changelog:", err)
		return 2
	}
	for _, v := range vs {
		fmt.Fprintln(e.stdout, v)
	}
	if len(vs) > 0 {
		return 1
	}
	return 0
}

func resolve(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// fragment is one valid fragment file.
type fragment struct {
	slug, kind, file, text string
}

// readFragments lists dir and returns the valid fragments and the violations of the others. A missing dir is empty.
func readFragments(dir string) ([]fragment, []string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var frags []fragment
	var vs []string
	for _, en := range ents {
		n := en.Name()
		path := filepath.Join(dir, n)
		if n == "README.md" {
			continue
		}
		if en.IsDir() {
			vs = append(vs, path+": a directory: only fragment files belong here")
			continue
		}
		m := fragmentRE.FindStringSubmatch(n)
		if m == nil {
			vs = append(vs, path+": name must be <slug>.<kind>.md (slug: lower-case letters, digits, hyphens, underscores; kind: "+strings.Join(kinds, ", ")+")")
			continue
		}
		if !contains(kinds, m[2]) {
			vs = append(vs, fmt.Sprintf("%s: kind %q must be one of %s", path, m[2], strings.Join(kinds, ", ")))
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		text := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
		switch {
		case text == "":
			vs = append(vs, path+": empty")
		case strings.HasPrefix(text, "- "), strings.HasPrefix(text, "* "), text == "-" || text == "*":
			vs = append(vs, path+": the text must not start with a bullet (\"- \" or \"* \"); assemble adds it")
		case strings.HasPrefix(text, "#"):
			vs = append(vs, path+": the text must not start with \"#\" (a heading); the kind in the file name picks the section")
		default:
			frags = append(frags, fragment{m[1], m[2], path, text})
		}
	}
	sort.Slice(frags, func(i, j int) bool {
		if frags[i].slug != frags[j].slug {
			return frags[i].slug < frags[j].slug
		}
		return frags[i].kind < frags[j].kind
	})
	return frags, vs, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func runAssemble(args []string, e env) int {
	fs := flag.NewFlagSet("assemble", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	version := fs.String("version", "", "the release version, X.Y.Z")
	date := fs.String("date", "", "the release date, YYYY-MM-DD")
	root := fs.String("root", ".", "the tree to change")
	dir := fs.String("dir", defaultDir, "the fragment directory (under --root unless absolute)")
	cl := fs.String("changelog", defaultChangelog, "the change log (under --root unless absolute)")
	dry := fs.Bool("dry-run", false, "print the new section; change nothing")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *version == "" || *date == "" {
		return usage(e, "assemble takes --version X.Y.Z --date YYYY-MM-DD [--root r] [--dir changelog.d] [--changelog CHANGELOG.md] [--dry-run]")
	}
	refuse := func(format string, a ...any) int {
		fmt.Fprintf(e.stdout, "changelog: refused: "+format+"\n", a...)
		return 1
	}
	if !versionRE.MatchString(*version) {
		return refuse("version %q must be X.Y.Z", *version)
	}
	if _, err := time.Parse("2006-01-02", *date); err != nil || !dateRE.MatchString(*date) {
		return refuse("date %q must be a valid YYYY-MM-DD", *date)
	}
	clPath := resolve(*root, *cl)
	old, err := os.ReadFile(clPath)
	if err != nil {
		fmt.Fprintln(e.stderr, "changelog:", err)
		return 2
	}
	frags, vs, err := readFragments(resolve(*root, *dir))
	if err != nil {
		fmt.Fprintln(e.stderr, "changelog:", err)
		return 2
	}
	if len(vs) > 0 {
		for _, v := range vs {
			fmt.Fprintln(e.stdout, v)
		}
		return refuse("fragments fail check")
	}
	out, section, err := assemble(string(old), *version, *date, frags)
	if err != nil {
		return refuse("%v", err)
	}
	if *dry {
		fmt.Fprint(e.stdout, section)
		return 0
	}
	if err := writeAtomic(clPath, []byte(out)); err != nil {
		fmt.Fprintln(e.stderr, "changelog:", err)
		return 2
	}
	for _, f := range frags {
		if err := os.Remove(f.file); err != nil {
			fmt.Fprintln(e.stderr, "changelog:", err)
			return 2
		}
	}
	fmt.Fprintf(e.stdout, "assembled %d fragments into %s as [%s] - %s\n", len(frags), clPath, *version, *date)
	return 0
}

// writeAtomic writes data to a temporary file beside path and renames it over path, keeping path's mode.
func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".changelog-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// subsection is a "### Name" section's entries; each entry is a bullet's lines, the first starting with "- ".
type subsection struct {
	name    string
	entries [][]string
}

// assemble returns the new CHANGELOG.md text and the new section alone.
func assemble(old, version, date string, frags []fragment) (full, section string, err error) {
	lines := strings.Split(strings.ReplaceAll(old, "\r\n", "\n"), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	start, end := -1, len(lines)
	for i, ln := range lines {
		switch {
		case start < 0 && strings.TrimRight(ln, " \t") == unreleasedHeader:
			start = i
		case start >= 0 && strings.HasPrefix(ln, "## "):
			end = i
		}
		if start >= 0 && end != len(lines) {
			break
		}
	}
	if start < 0 {
		return "", "", fmt.Errorf("the change log has no %q heading", unreleasedHeader)
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "## ["+version+"]") {
			return "", "", fmt.Errorf("the change log already has a section for %s", version)
		}
	}

	// The entries already under Unreleased, by subsection (a subsection that appears twice is one).
	var subs []*subsection
	var cur *subsection
	for i := start + 1; i < end; i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "### "):
			name := strings.TrimSpace(ln[4:])
			cur = nil
			for _, s := range subs {
				if strings.EqualFold(s.name, name) {
					cur = s
				}
			}
			if cur == nil {
				cur = &subsection{name: name}
				subs = append(subs, cur)
			}
		case strings.TrimSpace(ln) == "":
		case strings.HasPrefix(ln, "- "):
			if cur == nil {
				return "", "", fmt.Errorf("line %d: a bullet under %s before any \"### \" subsection", i+1, unreleasedHeader)
			}
			cur.entries = append(cur.entries, []string{ln})
		case cur != nil && len(cur.entries) > 0 && strings.HasPrefix(ln, " "):
			last := &cur.entries[len(cur.entries)-1]
			*last = append(*last, ln)
		default:
			return "", "", fmt.Errorf("line %d: cannot parse %q under %s (expected \"### Name\" or a \"- \" bullet)", i+1, ln, unreleasedHeader)
		}
	}

	// Fragments join their kind's subsection after the existing entries.
	for _, f := range frags {
		name := strings.ToUpper(f.kind[:1]) + f.kind[1:]
		var s *subsection
		for _, c := range subs {
			if strings.EqualFold(c.name, name) {
				s = c
			}
		}
		if s == nil {
			s = &subsection{name: name}
			subs = append(subs, s)
		}
		tl := strings.Split(f.text, "\n")
		entry := []string{"- " + tl[0]}
		for _, l := range tl[1:] {
			l = strings.TrimRight(l, " \t")
			if l != "" {
				l = "  " + l
			}
			entry = append(entry, l)
		}
		s.entries = append(s.entries, entry)
	}

	// Keep a Changelog order, then the others as they came.
	var ordered []*subsection
	for _, k := range kinds {
		for _, s := range subs {
			if strings.EqualFold(s.name, k) {
				ordered = append(ordered, s)
			}
		}
	}
	for _, s := range subs {
		if !contains(kinds, strings.ToLower(s.name)) {
			ordered = append(ordered, s)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## [%s] - %s\n\n", version, date)
	n := 0
	for _, s := range ordered {
		if len(s.entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", s.name)
		for _, en := range s.entries {
			b.WriteString(strings.Join(en, "\n") + "\n")
			n++
		}
		b.WriteString("\n")
	}
	if n == 0 {
		return "", "", errors.New("nothing to release: no fragments and no entries under " + unreleasedHeader)
	}
	section = b.String()

	var out strings.Builder
	out.WriteString(strings.Join(lines[:start+1], "\n") + "\n\n")
	out.WriteString(section)
	if end < len(lines) {
		out.WriteString(strings.Join(lines[end:], "\n") + "\n")
	}
	full = out.String()
	if end == len(lines) {
		full = strings.TrimRight(full, "\n") + "\n" // Unreleased was last: the section ends the file
		section = strings.TrimRight(section, "\n") + "\n"
	}
	return full, section, nil
}
