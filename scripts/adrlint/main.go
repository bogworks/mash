// Command adrlint enforces the architecture decision records under docs/decisions (adr-lint.yml's checker).
//
//	adrlint docs [--root r] [--base <git ref>] [dir]   lint every NNNN-title.md in dir (default docs/decisions)
//	adrlint pr --files <path> [--root r] [--dir dir]   check a pull request's "Decision:" line
//
// --root (default ".") is the tree being judged: dir resolves under it and, with --base, git runs as "git -C root". The
// workflow runs a checker built from the base branch with --root pointing at the pull request's checkout, so a pull
// request cannot change the rules it is judged by.
//
// docs rules (each violation is one file:line: message line on stdout, all are reported):
//
//	a  MADR front matter: status (proposed|accepted|rejected|deprecated|superseded by ADR-NNNN, optionally followed by
//	   ";" and a free-text note; an ADR-NNNN in the note must exist and be listed), date (YYYY-MM-DD) and
//	   decision-makers; a "superseded by ADR-NNNN" status must name an ADR file that exists and is listed in README.md
//	b  the first heading is "# <title>"; the "##" sections are present and in order; "### Consequences" and
//	   "### Confirmation" sit under Decision Outcome
//	c  README.md links every ADR and every README link to an ADR resolves; every relative link in an ADR resolves;
//	   numbers are unique (gaps are allowed: a number may be reserved by an in-flight PR)
//	d  no consumer vocabulary (see vocabRE; the front matter block is not checked); a line carrying <!-- adrlint:allow vocabulary --> is exempt
//	e  with --base: an ADR that was accepted at <ref> may change only its status line (to superseded by ADR-NNNN) and
//	   add or change a "Superseded by [..](..)" line, whose link must resolve to an existing ADR file
//
// pr reads PR_BODY and PR_LABELS (comma-separated) from the environment and the changed paths, one per line, from
// --files. The body needs exactly one "Decision: none" or "Decision: ADR-NNNN" line; the ADR must exist, be listed in
// README.md and be among the changed paths (the pull request adds or changes that record); "none" on a change under a
// protected path (docs/decisions/ included) needs the "decision-confirmed" label.
//
// Exit 0: ok. Exit 1: violations. Exit 2: usage or an unreadable input.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDir     = "docs/decisions"
	confirmedLabel = "decision-confirmed"
	allowVocab     = "<!-- adrlint:allow vocabulary -->"
)

// protected are the paths where "Decision: none" needs a reviewer's decision-confirmed label. A trailing slash is a
// directory prefix; anything else is one file. They are the decision-bearing code: the SAP wire, the store, permissions,
// config, the endpoint, the plugin, the MCP server (ADR 0023), the host adapter seam (ADR 0032), the spool (ADR 0025), the
// render compatibility contract (ADR 0034), and the records themselves.
var protected = []string{
	"internal/sap/", "internal/store/", "internal/admin/permissions.go", "internal/config/", "internal/endpoint/", "plugin/",
	"internal/mcp/", "internal/host/", "internal/spool/", "internal/render/", "docs/decisions/",
}

// sections are the required "##" headings, in order.
var sections = []string{
	"Context and Problem Statement", "Decision Drivers", "Considered Options", "Decision Outcome",
	"Pros and Cons of the Options", "More Information",
}

// outcomeSubs are the "###" headings required under Decision Outcome.
var outcomeSubs = []string{"Consequences", "Confirmation"}

var (
	nameRE      = regexp.MustCompile(`^(\d{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	dateRE      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	statusRE    = regexp.MustCompile(`(?i)^(proposed|accepted|rejected|deprecated|superseded by ADR-\d{4})(?:\s*;\s*(.*))?$`)
	noteRefRE   = regexp.MustCompile(`(?i)\bADR-(\d{4})\b`)
	linkRE      = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]*)[^)]*\)`)
	vocabRE     = regexp.MustCompile(`(?i)\b(herds?|herdr|lanes?|lead|orchestrator)\b`)
	supersedeLn = regexp.MustCompile(`^Superseded by \[[^\]]+\]\([^)]+\)\.?$`)
	decisionRE  = regexp.MustCompile(`(?i)^decision:\s*(none|ADR-\d{4})\s*$`)
	adrRefRE    = regexp.MustCompile(`^(?i:ADR)-(\d{4})$`)
)

// gitFunc runs git with args and returns its stdout; tests replace it.
type gitFunc func(args ...string) ([]byte, error)

func realGit(args ...string) ([]byte, error) {
	out, err := exec.Command("git", args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return out, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return out, err
}

// env is everything run reads from outside: tests inject it.
// forbiddenWordsEnv names the variable holding extra forbidden words (comma-separated, case-insensitive, whole words),
// so the words themselves need not appear in the tree.
const forbiddenWordsEnv = "MASH_FORBIDDEN_WORDS"

// extraVocab builds the whole-word, case-insensitive matcher for a comma-separated word list; nil when it names none.
func extraVocab(list string) *regexp.Regexp {
	var ws []string
	for _, w := range strings.Split(list, ",") {
		if w = strings.TrimSpace(w); w != "" {
			ws = append(ws, regexp.QuoteMeta(w))
		}
	}
	if len(ws) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)\b(` + strings.Join(ws, "|") + `)\b`)
}

type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	git            gitFunc
}

func main() {
	os.Exit(run(os.Args[1:], env{os.Stdout, os.Stderr, os.Getenv, realGit}))
}

func run(args []string, e env) int {
	if len(args) == 0 {
		return usage(e, "a mode is required")
	}
	switch args[0] {
	case "docs":
		return runDocs(args[1:], e)
	case "pr":
		return runPR(args[1:], e)
	}
	return usage(e, "unknown mode "+args[0])
}

func usage(e env, msg string) int {
	fmt.Fprintf(e.stderr, "adrlint: %s\nusage: adrlint docs [--root r] [--base <git ref>] [dir]\n       adrlint pr --files <path> [--root r] [--dir dir]\n", msg)
	return 2
}

// parseFlags parses args so flags may come before or after the positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func report(e env, vs []string) int {
	for _, v := range vs {
		fmt.Fprintln(e.stdout, v)
	}
	if len(vs) > 0 {
		return 1
	}
	return 0
}

func runDocs(args []string, e env) int {
	fs := flag.NewFlagSet("docs", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	base := fs.String("base", "", "git ref; accepted ADRs at this ref become immutable")
	root := fs.String("root", ".", "the tree to judge; dir and git resolve under it")
	pos, err := parseFlags(fs, args)
	if err != nil || len(pos) > 1 {
		return usage(e, "docs takes [--root r] [--base <ref>] [dir]")
	}
	dir := defaultDir
	if len(pos) == 1 {
		dir = pos[0]
	}
	fsDir, rel := resolveDir(*root, dir)
	git := e.git
	if *root != "." {
		git = func(a ...string) ([]byte, error) { return e.git(append([]string{"-C", *root}, a...)...) }
	}
	vs, err := lintDocs(fsDir, extraVocab(e.getenv(forbiddenWordsEnv)))
	if err != nil {
		fmt.Fprintln(e.stderr, "adrlint:", err)
		return 2
	}
	if *base != "" {
		if _, err := git("rev-parse", "--verify", "--quiet", *base+"^{commit}"); err != nil {
			fmt.Fprintf(e.stderr, "adrlint: base %q is not a commit: %v\n", *base, err)
			return 2
		}
		iv, err := lintImmutable(fsDir, rel, *base, git)
		if err != nil {
			fmt.Fprintln(e.stderr, "adrlint:", err)
			return 2
		}
		vs = append(vs, iv...)
	}
	return report(e, vs)
}

// resolveDir returns the directory's path on disk and its slash path relative to root (how git and the changed-path
// list name it). A relative dir is under root; an absolute one is used as is.
func resolveDir(root, dir string) (fsDir, rel string) {
	if !filepath.IsAbs(dir) {
		return filepath.Join(root, dir), filepath.ToSlash(filepath.Clean(dir))
	}
	rel = dir
	if r, err := filepath.Rel(root, dir); err == nil {
		rel = r
	}
	return dir, filepath.ToSlash(rel)
}

// adrFiles returns the NNNN-title.md names in dir, sorted, and the names that start with a digit but do not fit.
func adrFiles(dir string) (good, bad []string, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, en := range ents {
		n := en.Name()
		if en.IsDir() || !strings.HasSuffix(n, ".md") || n[0] < '0' || n[0] > '9' {
			continue
		}
		if nameRE.MatchString(n) {
			good = append(good, n)
		} else {
			bad = append(bad, n)
		}
	}
	sort.Strings(good)
	sort.Strings(bad)
	return good, bad, nil
}

func splitLines(b []byte) []string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type kv struct {
	val  string
	line int
}

// frontMatter is the parsed block between the opening and closing --- lines.
type frontMatter struct {
	ok   bool // an opening and a closing --- were found
	end  int  // 1-based line of the closing ---
	keys map[string]kv
}

func parseFrontMatter(lines []string) frontMatter {
	fm := frontMatter{keys: map[string]kv{}}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			fm.ok, fm.end = true, i+1
			return fm
		}
		if k, v, ok := strings.Cut(lines[i], ":"); ok && !strings.HasPrefix(lines[i], " ") {
			fm.keys[strings.TrimSpace(k)] = kv{strings.Trim(strings.TrimSpace(v), `"'`), i + 1}
		}
	}
	return fm
}

// parseStatus splits a status value into its lower-case keyword (proposed, accepted, rejected, deprecated or
// "superseded by adr-nnnn") and the free-text note after an optional ";". ok is false when the keyword is none of those.
func parseStatus(s string) (keyword, note string, ok bool) {
	m := statusRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), m[2], true
}

func viol(file string, line int, format string, a ...any) string {
	return fmt.Sprintf("%s:%d: %s", file, line, fmt.Sprintf(format, a...))
}

// lintDocs applies rules a to d to every ADR in dir.
func lintDocs(dir string, extra *regexp.Regexp) ([]string, error) {
	files, bad, err := adrFiles(dir)
	if err != nil {
		return nil, err
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return nil, err
	}
	var vs []string
	for _, n := range bad {
		vs = append(vs, viol(filepath.Join(dir, n), 1, "file name must be NNNN-kebab-title.md (4 digits)"))
	}

	// c: numbers unique (gaps are allowed).
	byNum := map[int][]string{}
	for _, n := range files {
		num, _ := strconv.Atoi(n[:4])
		byNum[num] = append(byNum[num], n)
	}
	nums := make([]int, 0, len(byNum))
	for num := range byNum {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for _, num := range nums {
		if ns := byNum[num]; len(ns) > 1 {
			vs = append(vs, viol(filepath.Join(dir, ns[1]), 1, "number %04d is used by more than one ADR: %s", num, strings.Join(ns, ", ")))
		}
	}

	// c: README links.
	listed := map[string]bool{}
	inFence := false
	for i, ln := range splitLines(readme) {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
		}
		if inFence {
			continue
		}
		for _, m := range linkRE.FindAllStringSubmatch(ln, -1) {
			target := linkTarget(m[1])
			if target == "" || !nameRE.MatchString(filepath.Base(target)) {
				continue
			}
			listed[filepath.Base(target)] = true
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(target))); err != nil {
				vs = append(vs, viol(filepath.Join(dir, "README.md"), i+1, "link to %s does not resolve", target))
			}
		}
	}

	for _, n := range files {
		path := filepath.Join(dir, n)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !listed[n] {
			vs = append(vs, viol(path, 1, "not listed in README.md (needs a link whose target is %s)", n))
		}
		vs = append(vs, lintADR(path, dir, splitLines(b), extra, func(num string) string {
			for _, f := range files {
				if f[:4] == num {
					if !listed[f] {
						return fmt.Sprintf("ADR-%s (%s) is not listed in README.md", num, f)
					}
					return ""
				}
			}
			return fmt.Sprintf("ADR-%s has no file %s-*.md", num, num)
		})...)
	}
	return vs, nil
}

// linkTarget returns the file part of a markdown link target, or "" for an external link or a bare anchor.
func linkTarget(t string) string {
	t = strings.TrimSpace(t)
	l := strings.ToLower(t)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:") {
		return ""
	}
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t = t[:i]
	}
	return t
}

type heading struct {
	level int
	text  string
	line  int
}

func headings(lines []string, from int) []heading {
	var hs []heading
	inFence := false
	for i := from; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(ln, "#") {
			continue
		}
		lv := len(ln) - len(strings.TrimLeft(ln, "#"))
		if lv > 6 || len(ln) <= lv || ln[lv] != ' ' {
			continue
		}
		hs = append(hs, heading{lv, strings.TrimSpace(ln[lv:]), i + 1})
	}
	return hs
}

// sectionName returns the required section a "##" heading text names: the name itself, or the name followed by a
// space and "(" (a parenthetical note), or "" when it names none.
func sectionName(text string) string {
	for _, s := range sections {
		if text == s || strings.HasPrefix(text, s+" (") {
			return s
		}
	}
	return ""
}

// lintADR applies rules a, b, d and the link half of c to one ADR. target reports a problem with the ADR number a
// "superseded by" status names ("" when it exists and is listed).
func lintADR(path, dir string, lines []string, extra *regexp.Regexp, target func(num string) string) []string {
	var vs []string

	// a: front matter.
	fm := parseFrontMatter(lines)
	body := 0
	if !fm.ok {
		vs = append(vs, viol(path, 1, "front matter between --- lines is missing or not closed"))
	} else {
		body = fm.end
		for _, k := range []string{"status", "date", "decision-makers"} {
			if _, ok := fm.keys[k]; !ok {
				vs = append(vs, viol(path, 1, "front matter has no %s key", k))
			}
		}
		if s, ok := fm.keys["status"]; ok {
			kw, note, valid := parseStatus(s.val)
			if !valid {
				vs = append(vs, viol(path, s.line, "status %q must be proposed, accepted, rejected, deprecated or superseded by ADR-NNNN, optionally followed by \"; note\"", s.val))
			} else {
				refs := noteRefRE.FindAllStringSubmatch(note, -1)
				if strings.HasPrefix(kw, "superseded by") {
					refs = append([][]string{{"", kw[len(kw)-4:]}}, refs...)
				}
				for _, m := range refs {
					if msg := target(m[1]); msg != "" {
						vs = append(vs, viol(path, s.line, "status %q names a missing or unlisted record: %s", s.val, msg))
					}
				}
			}
		}
		if d, ok := fm.keys["date"]; ok {
			if _, err := time.Parse("2006-01-02", d.val); !dateRE.MatchString(d.val) || err != nil {
				vs = append(vs, viol(path, d.line, "date %q must be a valid YYYY-MM-DD", d.val))
			}
		}
		if d, ok := fm.keys["decision-makers"]; ok && d.val == "" {
			vs = append(vs, viol(path, d.line, "decision-makers is empty"))
		}
	}

	// b: headings.
	hs := headings(lines, body)
	if len(hs) == 0 || hs[0].level != 1 {
		line := 1
		if len(hs) > 0 {
			line = hs[0].line
		}
		vs = append(vs, viol(path, line, "the first heading must be a \"# <title>\""))
	}
	pos := map[string]heading{}
	for _, h := range hs {
		if h.level != 2 {
			continue
		}
		if name := sectionName(h.text); name != "" {
			if _, dup := pos[name]; !dup {
				pos[name] = h
			}
		}
	}
	lastSeen := -1
	for _, s := range sections {
		h, ok := pos[s]
		if !ok {
			vs = append(vs, viol(path, 1, "missing section \"## %s\"", s))
			continue
		}
		if h.line < lastSeen {
			vs = append(vs, viol(path, h.line, "section \"## %s\" is out of order (the order is %s)", s, strings.Join(sections, ", ")))
		}
		lastSeen = max(lastSeen, h.line)
	}
	if out, ok := pos["Decision Outcome"]; ok {
		var subs []string
		for _, h := range hs {
			if h.line <= out.line {
				continue
			}
			if h.level <= 2 {
				break
			}
			if h.level == 3 {
				subs = append(subs, h.text)
			}
		}
		for _, s := range outcomeSubs {
			if !slices.Contains(subs, s) {
				vs = append(vs, viol(path, out.line, "\"## Decision Outcome\" has no \"### %s\"", s))
			}
		}
	}

	// c: relative links; d: vocabulary.
	inFence := false
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
		}
		if !inFence {
			for _, m := range linkRE.FindAllStringSubmatch(ln, -1) {
				t := linkTarget(m[1])
				if t == "" {
					continue
				}
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(t))); err != nil {
					vs = append(vs, viol(path, i+1, "link %q does not resolve", m[1]))
				}
			}
		}
		if i >= body && !strings.Contains(ln, allowVocab) { // the front matter is metadata, not prose
			ms := vocabRE.FindAllString(ln, -1)
			if extra != nil {
				ms = append(ms, extra.FindAllString(ln, -1)...)
			}
			for _, m := range ms {
				vs = append(vs, viol(path, i+1, "forbidden vocabulary %q (add %s on the line if it is deliberate)", m, allowVocab))
			}
		}
	}
	return vs
}

// numbered is a line with its 1-based line number in its file.
type numbered struct {
	text string
	line int
}

// lintImmutable is rule e: every ADR that was accepted at base is compared with the working file. dir is the
// directory on disk, rel the same directory as git names it (slash path relative to the repository root).
func lintImmutable(dir, rel, base string, git gitFunc) ([]string, error) {
	files, _, err := adrFiles(dir)
	if err != nil {
		return nil, err
	}
	// The names to check are the ones in the working tree plus the ones at base, so a deleted accepted ADR is seen.
	names := map[string]bool{}
	for _, n := range files {
		names[n] = true
	}
	if out, err := git("ls-tree", "--name-only", base, "./"+rel+"/"); err == nil {
		for _, p := range strings.Fields(string(out)) {
			if n := filepath.Base(p); nameRE.MatchString(n) {
				names[n] = true
			}
		}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	var vs []string
	for _, n := range sorted {
		path := filepath.Join(dir, n)
		old, err := git("show", base+":./"+rel+"/"+n)
		if err != nil {
			continue // not at base: a new file is free
		}
		oldLines := splitLines(old)
		ofm := parseFrontMatter(oldLines)
		if kw, _, _ := parseStatus(ofm.keys["status"].val); !ofm.ok || kw != "accepted" {
			continue // not accepted at base: free
		}
		cur, err := os.ReadFile(path)
		if err != nil {
			vs = append(vs, viol(path, 1, "accepted ADR (at %s) was deleted; supersede it instead", base))
			continue
		}
		vs = append(vs, compareAccepted(path, base, oldLines, ofm, splitLines(cur))...)
	}
	return vs, nil
}

// compareAccepted reports every change to an accepted ADR other than its status keyword (to superseded-by or
// deprecated, note unchanged only if the keyword stays) and one exact "Superseded by [..](..)." body line. Only the first body difference is reported: later lines shift with it.
func compareAccepted(path, base string, oldLines []string, ofm frontMatter, curLines []string) []string {
	cfm := parseFrontMatter(curLines)
	if !cfm.ok {
		return []string{viol(path, 1, "accepted ADR (at %s) lost its front matter", base)}
	}
	var vs []string
	skipCur := -1
	newKw := ""
	if cs, ok := cfm.keys["status"]; ok {
		skipCur = cs.line
		kw, _, ok := parseStatus(cs.val)
		newKw = kw
		switch {
		case !ok || (kw != "accepted" && kw != "deprecated" && !strings.HasPrefix(kw, "superseded by")):
			vs = append(vs, viol(path, cs.line, "accepted ADR (at %s): status may only change to \"superseded by ADR-NNNN\" or \"deprecated\", not %q", base, cs.val))
		case kw == "accepted" && curLines[cs.line-1] != oldLines[ofm.keys["status"].line-1]:
			vs = append(vs, viol(path, cs.line, "accepted ADR (at %s): the status line may not change (note included) while the status stays accepted", base))
		}
	} else {
		vs = append(vs, viol(path, 1, "accepted ADR (at %s) lost its status key", base))
	}
	links := 0
	for i, ln := range curLines {
		if i+1 <= cfm.end || !supersedeLn.MatchString(ln) {
			continue
		}
		if links++; links > 1 {
			vs = append(vs, viol(path, i+1, "accepted ADR (at %s): at most one \"Superseded by\" line may be added", base))
		}
		if !strings.HasPrefix(newKw, "superseded by") {
			vs = append(vs, viol(path, i+1, "accepted ADR (at %s): a \"Superseded by\" line needs the status \"superseded by ADR-NNNN\"", base))
		}
		for _, m := range linkRE.FindAllStringSubmatch(ln, -1) {
			t := linkTarget(m[1])
			if t == "" || !nameRE.MatchString(filepath.Base(t)) {
				vs = append(vs, viol(path, i+1, "the \"Superseded by\" link %q must point to an ADR file", m[1]))
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(t))); err != nil {
				vs = append(vs, viol(path, i+1, "the \"Superseded by\" link %q names an ADR file that does not exist", m[1]))
			}
		}
	}
	strip := func(lines []string, skip, end int) []numbered {
		var out []numbered
		for i, ln := range lines {
			if i+1 != skip && (i+1 <= end || !supersedeLn.MatchString(ln)) {
				out = append(out, numbered{ln, i + 1})
			}
		}
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1].text) == "" {
			out = out[:len(out)-1] // the blank line that sets a Superseded by line apart is not a change
		}
		return out
	}
	o, c := strip(oldLines, ofm.keys["status"].line, ofm.end), strip(curLines, skipCur, cfm.end)
	for i := 0; i < len(o) || i < len(c); i++ {
		if i >= len(c) {
			return append(vs, viol(path, len(curLines), "accepted ADR (at %s) is immutable: text was removed (was line %d: %q)", base, o[i].line, o[i].text))
		}
		if i >= len(o) || o[i].text != c[i].text {
			was := ""
			if i < len(o) {
				was = fmt.Sprintf(" (was %q)", o[i].text)
			}
			return append(vs, viol(path, c[i].line, "accepted ADR (at %s) is immutable: only the status keyword and one whole-line \"Superseded by [..](..)\" line may change%s", base, was))
		}
	}
	return vs
}

func runPR(args []string, e env) int {
	fs := flag.NewFlagSet("pr", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	files := fs.String("files", "", "file listing the changed paths, one per line")
	dir := fs.String("dir", defaultDir, "the decisions directory (under --root unless absolute)")
	root := fs.String("root", ".", "the tree to judge; dir resolves under it")
	pos, err := parseFlags(fs, args)
	if err != nil || len(pos) != 0 || *files == "" {
		return usage(e, "pr takes --files <path> [--root r] [--dir dir]")
	}
	fb, err := os.ReadFile(*files)
	if err != nil {
		fmt.Fprintln(e.stderr, "adrlint:", err)
		return 2
	}
	var changed []string
	for _, ln := range splitLines(fb) {
		if ln = strings.TrimSpace(ln); ln != "" {
			changed = append(changed, ln)
		}
	}
	var labels []string
	for _, l := range strings.Split(e.getenv("PR_LABELS"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			labels = append(labels, l)
		}
	}
	fsDir, rel := resolveDir(*root, *dir)
	vs, err := checkPR(e.getenv("PR_BODY"), changed, labels, fsDir, rel)
	if err != nil {
		fmt.Fprintln(e.stderr, "adrlint:", err)
		return 2
	}
	return report(e, vs)
}

func protectedPath(p string) bool {
	for _, pre := range protected {
		if p == pre || (strings.HasSuffix(pre, "/") && strings.HasPrefix(p, pre)) {
			return true
		}
	}
	return false
}

// checkPR returns the violations of the pull-request rules. dir is the decisions directory on disk, rel its slash path
// relative to the repository root (how the changed paths name it).
func checkPR(body string, changed, labels []string, dir, rel string) ([]string, error) {
	var decisions []string
	for _, ln := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if m := decisionRE.FindStringSubmatch(ln); m != nil {
			decisions = append(decisions, m[1])
		}
	}
	const form = "the PR body needs exactly one line \"Decision: none\" or \"Decision: ADR-NNNN\""
	switch len(decisions) {
	case 0:
		return []string{"PR body: no Decision line: " + form}, nil
	case 1:
	default:
		return []string{fmt.Sprintf("PR body: %d Decision lines: %s", len(decisions), form)}, nil
	}
	d := decisions[0]
	if strings.EqualFold(d, "none") {
		for _, p := range changed {
			if protectedPath(p) && !slices.Contains(labels, confirmedLabel) {
				return []string{fmt.Sprintf("PR body: \"Decision: none\" on a change to %s (a protected path): write an ADR and name it, or have a reviewer apply the %q label to confirm that no decision is involved", p, confirmedLabel)}, nil
			}
		}
		return nil, nil
	}
	num := adrRefRE.FindStringSubmatch(d)[1]
	matches, err := filepath.Glob(filepath.Join(dir, num+"-*.md"))
	if err != nil {
		return nil, err
	}
	file := ""
	for _, m := range matches {
		if nameRE.MatchString(filepath.Base(m)) {
			file = filepath.Base(m)
			break
		}
	}
	if file == "" {
		return []string{fmt.Sprintf("PR body: Decision: %s names no file %s/%s-*.md", d, rel, num)}, nil
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		return nil, err
	}
	listed := false
	for _, m := range linkRE.FindAllStringSubmatch(string(readme), -1) {
		if t := linkTarget(m[1]); t != "" && filepath.Base(t) == file {
			listed = true
			break
		}
	}
	if !listed {
		return []string{fmt.Sprintf("PR body: Decision: %s is not listed in %s/README.md", d, rel)}, nil
	}
	if !slices.Contains(changed, rel+"/"+file) {
		return []string{fmt.Sprintf("PR body: Decision: %s names %s/%s, but this pull request does not add or change that record: it must add or change the ADR it names", d, rel, file)}, nil
	}
	return nil, nil
}
