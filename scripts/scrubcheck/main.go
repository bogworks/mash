// Command scrubcheck is a one-time verification: it finds the words in words.txt in the shipped surfaces.
//
//	scrubcheck [-root dir] [-words file] [-exceptions file] [-archive path]...
//
// Surfaces: every file git tracks under -root (default "."), which covers source, docs, the plugin package, scripts and
// workflows (a tracked symlink is followed and its target scanned under the symlink's path; a dangling one is an
// error), plus each -archive (repeatable): a release archive (.tar.gz, .tgz, .tar or .zip) whose members are scanned
// one by one, or, for any other extension, a plain file such as a bare release binary. The tool's own directory,
// scripts/scrubcheck/, is skipped entirely: it holds the word list itself, its fixtures and the exceptions inventory.
//
// A word matches case-insensitively and as a whole word: a run of letters and digits (so a hyphen, an underscore or any
// other character is a boundary) equal to a listed word, or to a listed word plus "s". "shepherd" and "herdsman" do not
// match "herd". Plurals are generated, not listed.
//
// Output, on stdout, one line per hit, sorted by file, then line, then column:
//
//	file:line:word          word is lowercased; a hit in a member reads archive.tgz!member/path:line:word
//	file:strings:word       a file with a NUL byte in its first 8000 bytes is binary and is scanned as its printable
//	                        strings (runs of at least four bytes that are printable ASCII or non-ASCII, so UTF-8 letters stay
//	                        part of a word), which have no line number
//
// exceptions.tsv holds tab-separated path, pattern and reason. The path is a literal file path as printed (for a member,
// archive!member); a directory ("/" suffix) or a glob (* ? [) is rejected. The pattern is a literal substring; it covers a
// hit only when the hit's own occurrence lies inside an occurrence of the pattern on that line (or printable run), so
// "quoted herd" excepts that herd and nothing else on the line. The reason is required. An entry that matches no hit is reported, so a stale entry
// cannot linger. Problems print as exceptions.tsv:LINE: message. A missing exceptions file means no exceptions.
//
// The last line is the summary "N hits, M excepted, K exception problems". Exit 0: nothing to report. Exit 1: an
// unexcepted hit or an exception problem. Exit 2: usage (including a stray positional argument) or an unreadable input.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	selfDir         = "scripts/scrubcheck/"
	defaultWords    = selfDir + "words.txt"
	defaultExcept   = selfDir + "exceptions.tsv"
	binarySniff     = 8000
	minPrintableRun = 4
)

// hit is one found word. line 0 marks a printable-strings hit.
type hit struct {
	file string
	line int
	col  int // byte offset of the word within text
	off  int // orders hits within a line (or, for strings, a file): col, or the file offset of the word
	n    int // byte length of the word as written (before lowercasing)
	word string
	text string // the line, or printable run, the word sits on: an exception's pattern must cover the word's span in it
}

// exception is one parsed exceptions.tsv entry.
type exception struct {
	line                  int
	path, pattern, reason string
}

// errUsage marks an error that is a usage or input problem (exit 2), not findings (exit 1).
type errUsage struct{ err error }

func (e errUsage) Error() string { return e.err.Error() }
func (e errUsage) Unwrap() error { return e.err }

// errFindings is returned by run when there is an unexcepted hit or an exception problem.
var errFindings = errors.New("scrubcheck: findings")

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	err := run(os.Args[1:], os.Stdout)
	if err == nil {
		return
	}
	if errors.Is(err, errFindings) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

// run is the whole command. It returns nil only when there is nothing to report.
func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("scrubcheck", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "repository root whose tracked files are scanned")
	words := fs.String("words", "", "word list (default "+defaultWords+" under -root)")
	exc := fs.String("exceptions", "", "exceptions file (default "+defaultExcept+" under -root)")
	var archives multiFlag
	fs.Var(&archives, "archive", "release archive (.tar.gz, .tgz, .tar, .zip) or plain file to scan too; repeatable")
	const usage = "usage: scrubcheck [-root dir] [-words file] [-exceptions file] [-archive path]...\nskips " + selfDir + " (the word list, fixtures and inventory live there)"
	if err := fs.Parse(args); err != nil {
		return errUsage{fmt.Errorf("scrubcheck: %w\n%s", err, usage)}
	}
	if fs.NArg() > 0 {
		return errUsage{fmt.Errorf("scrubcheck: unexpected argument %q (flags only; a flag after it would be ignored)\n%s", fs.Arg(0), usage)}
	}
	if *words == "" {
		*words = filepath.Join(*root, defaultWords)
	}
	if *exc == "" {
		*exc = filepath.Join(*root, defaultExcept)
	}
	wordsData, err := os.ReadFile(*words)
	if err != nil {
		return errUsage{err}
	}
	ws, err := parseWords(string(wordsData))
	if err != nil {
		return errUsage{err}
	}
	var excData []byte
	if b, err := os.ReadFile(*exc); err == nil {
		excData = b
	} else if !errors.Is(err, os.ErrNotExist) {
		return errUsage{err}
	}
	excs, problems := parseExceptions(string(excData))

	files, err := listFiles(*root)
	if err != nil {
		return errUsage{err}
	}
	var hits []hit
	for _, rel := range files {
		if strings.HasPrefix(rel, selfDir) {
			continue
		}
		p := filepath.Join(*root, filepath.FromSlash(rel))
		fi, err := os.Lstat(p)
		if err != nil {
			continue // deleted in the work tree
		}
		if fi.Mode()&os.ModeSymlink == 0 && !fi.Mode().IsRegular() {
			continue // a submodule
		}
		data, err := os.ReadFile(p) // follows a symlink: its target is scanned under the symlink's own path
		if err != nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return errUsage{fmt.Errorf("tracked symlink %s: target cannot be read: %w", rel, err)}
			}
			return errUsage{err}
		}
		hits = append(hits, scanText(rel, data, ws)...)
	}
	for _, a := range archives {
		hs, err := scanArchive(a, ws)
		if err != nil {
			return errUsage{err}
		}
		hits = append(hits, hs...)
	}
	sortHits(hits)

	rest, excepted, unmatched := applyExceptions(hits, excs)
	problems = append(problems, unmatched...)
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].line < problems[j].line })
	for _, h := range rest {
		fmt.Fprintln(stdout, formatHit(h))
	}
	excName := filepath.Base(*exc)
	for _, p := range problems {
		fmt.Fprintf(stdout, "%s:%d: %s\n", excName, p.line, p.msg)
	}
	fmt.Fprintf(stdout, "%d hits, %d excepted, %d exception problems\n", len(rest)+excepted, excepted, len(problems))
	if len(rest) > 0 || len(problems) > 0 {
		return errFindings
	}
	return nil
}

// listFiles returns the repo-relative paths git tracks under root.
func listFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// parseWords reads the word list: one word per line, "#" lines and blank lines ignored. Words are lowercased.
func parseWords(s string) ([]string, error) {
	var ws []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.IndexFunc(l, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) >= 0 {
			return nil, fmt.Errorf("word list: %q is not a single word", l)
		}
		ws = append(ws, strings.ToLower(l))
	}
	if len(ws) == 0 {
		return nil, errors.New("word list is empty")
	}
	return ws, nil
}

// problem is one exceptions.tsv complaint.
type problem struct {
	line int
	msg  string
}

// parseExceptions parses exceptions.tsv. Entries with a missing reason are returned (and reported) so they still apply.
func parseExceptions(s string) ([]exception, []problem) {
	var es []exception
	var ps []problem
	for i, l := range strings.Split(s, "\n") {
		n := i + 1
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != 3 || f[0] == "" || f[1] == "" {
			ps = append(ps, problem{n, "malformed entry: want path<TAB>pattern<TAB>reason"})
			continue
		}
		if strings.HasSuffix(f[0], "/") || strings.ContainsAny(f[0], "*?[") {
			ps = append(ps, problem{n, "whole-file or whole-directory entry: path must be one literal file path"})
			continue
		}
		reason := strings.TrimSpace(f[2])
		if reason == "" {
			ps = append(ps, problem{n, "no reason"})
		}
		es = append(es, exception{line: n, path: f[0], pattern: f[1], reason: reason})
	}
	return es, ps
}

// applyExceptions drops the hits an exception covers. It returns the remaining hits, how many were excepted, and a
// "matches nothing" problem for each entry that covered no hit.
func applyExceptions(hits []hit, excs []exception) ([]hit, int, []problem) {
	used := make([]bool, len(excs))
	var rest []hit
	excepted := 0
	for _, h := range hits {
		covered := false
		for i, e := range excs {
			if e.path == h.file && covers(h, e.pattern) {
				used[i] = true
				covered = true
			}
		}
		if covered {
			excepted++
		} else {
			rest = append(rest, h)
		}
	}
	var ps []problem
	for i, e := range excs {
		if !used[i] {
			ps = append(ps, problem{e.line, "matches nothing"})
		}
	}
	return rest, excepted, ps
}

// covers reports whether the pattern occurs in the hit's text at some position p with p <= col and
// col+len <= p+len(pattern): the hit's own occurrence lies inside the pattern's occurrence.
func covers(h hit, pattern string) bool {
	for from := 0; from <= len(h.text); {
		i := strings.Index(h.text[from:], pattern)
		if i < 0 {
			return false
		}
		p := from + i
		if p > h.col {
			return false
		}
		if h.col+h.n <= p+len(pattern) {
			return true
		}
		from = p + 1
	}
	return false
}

func sortHits(hs []hit) {
	sort.SliceStable(hs, func(i, j int) bool {
		a, b := hs[i], hs[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.off < b.off
	})
}

// formatHit renders file:line:word, or file:strings:word for a printable-strings hit. The word is lowercased.
func formatHit(h hit) string {
	if h.line == 0 {
		return h.file + ":strings:" + h.word
	}
	return h.file + ":" + strconv.Itoa(h.line) + ":" + h.word
}

// scanText scans one file's bytes: by line, or, when a NUL byte is in the first 8000 bytes, as printable strings.
func scanText(name string, data []byte, words []string) []hit {
	head := data
	if len(head) > binarySniff {
		head = head[:binarySniff]
	}
	var hits []hit
	if bytes.IndexByte(head, 0) >= 0 {
		start := -1
		flush := func(end int) {
			if start >= 0 && end-start >= minPrintableRun {
				for _, m := range matchLine(string(data[start:end]), words) {
					hits = append(hits, hit{file: name, col: m.col, off: start + m.col, n: m.n, word: m.word, text: string(data[start:end])})
				}
			}
			start = -1
		}
		for i, b := range data {
			if (b >= 0x20 && b <= 0x7e) || b == '\t' || b >= 0x80 {
				if start < 0 {
					start = i
				}
			} else {
				flush(i)
			}
		}
		flush(len(data))
		return hits
	}
	for i, l := range strings.Split(string(data), "\n") {
		for _, m := range matchLine(l, words) {
			hits = append(hits, hit{file: name, line: i + 1, col: m.col, off: m.col, n: m.n, word: m.word, text: l})
		}
	}
	return hits
}

type match struct {
	col  int
	n    int
	word string
}

// matchLine finds whole-word matches: each maximal run of letters and digits that, lowercased, is a listed word or a
// listed word plus "s". col is the byte offset of the run in the line.
func matchLine(line string, words []string) []match {
	var ms []match
	start := -1
	check := func(end int) {
		if start < 0 {
			return
		}
		tok := strings.ToLower(line[start:end])
		for _, w := range words {
			if tok == w || tok == w+"s" {
				ms = append(ms, match{start, end - start, tok})
				break
			}
		}
		start = -1
	}
	for i, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
		} else {
			check(i)
		}
	}
	check(len(line))
	return ms
}

// scanArchive scans a release archive member by member (reported as path!member), or a plain file as itself.
func scanArchive(path string, words []string) ([]hit, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		defer gz.Close()
		return scanTar(path, gz, words)
	case strings.HasSuffix(lower, ".tar"):
		return scanTar(path, bytes.NewReader(data), words)
	case strings.HasSuffix(lower, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var hits []hit
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			hits = append(hits, scanText(path+"!"+f.Name, b, words)...)
		}
		return hits, nil
	}
	return scanText(path, data, words), nil
}

func scanTar(path string, r io.Reader, words []string) ([]hit, error) {
	var hits []hit
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return hits, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		hits = append(hits, scanText(path+"!"+h.Name, b, words)...)
	}
}
