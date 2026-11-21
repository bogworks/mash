// Command doclint checks that the repository's Markdown files conform to their syntax: the one check a docs-only
// change needs (ci.yml's docs-lint job; Randy 2026-11-21: "as long as they properly conform to their syntax, I don't see
// why anything else needs to run"). It reads every tracked *.md file and reports each violation as file:line: message on
// stdout; the exit status is 1 when there is one, 2 when it could not run.
//
//	doclint [--root r]
//
// Rules:
//
//	fence  a code fence (``` or ~~~) is closed by a fence of the same character, at least as long, before the end of the file
//	link   a relative link [text](path) or ![alt](path) names a file or folder that exists (the anchor and the query are not
//	       checked); absolute URLs, mailto: and a bare #anchor are not checked; links inside a code fence or an inline code
//	       span are not links
//	nul    no NUL byte outside a code fence
//
// Files under testdata/, vendor/, receipts/ and docs/evidence/ are not read: fixtures and captured records hold what
// they hold on purpose (their links point into other checkouts). It takes seconds and needs no network.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errw io.Writer) int {
	root := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			if i+1 >= len(args) {
				fmt.Fprintln(errw, "doclint: --root needs a folder")
				return 2
			}
			root = args[i+1]
			i++
		default:
			fmt.Fprintf(errw, "doclint: unknown argument %q\n", args[i])
			return 2
		}
	}
	files, err := markdownFiles(root)
	if err != nil {
		fmt.Fprintln(errw, "doclint:", err)
		return 2
	}
	var problems []string
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			fmt.Fprintln(errw, "doclint:", err)
			return 2
		}
		problems = append(problems, Lint(root, f, data)...)
	}
	sort.Strings(problems)
	for _, p := range problems {
		fmt.Fprintln(out, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(errw, "doclint: %d problem(s) in %d file(s) checked\n", len(problems), len(files))
		return 1
	}
	fmt.Fprintf(out, "doclint: %d Markdown file(s) conform\n", len(files))
	return 0
}

// markdownFiles lists the tracked *.md files (git ls-files), else every *.md under root, minus testdata and vendor.
func markdownFiles(root string) ([]string, error) {
	var files []string
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.md")
	if b, err := cmd.Output(); err == nil {
		for _, f := range strings.Split(string(b), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	} else {
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				rel, _ := filepath.Rel(root, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	var keep []string
	for _, f := range files {
		if skipped(f) {
			continue
		}
		if st, err := os.Lstat(filepath.Join(root, f)); err != nil || !st.Mode().IsRegular() {
			continue // deleted in the working tree, or a symlink: nothing to read
		}
		keep = append(keep, f)
	}
	return keep, nil
}

func skipped(f string) bool {
	if strings.HasPrefix(f, "receipts/") || strings.HasPrefix(f, "docs/evidence/") {
		return true
	}
	for _, seg := range strings.Split(f, "/") {
		if seg == "testdata" || seg == "vendor" {
			return true
		}
	}
	return false
}

var (
	fenceRE  = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	linkRE   = regexp.MustCompile(`!?\[[^\]\n]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	inlineRE = regexp.MustCompile("`+[^`\n]*`+")
)

// Lint returns the violations of one file as "path:line: message". root is the tree relative links resolve in.
func Lint(root, path string, data []byte) []string {
	var problems []string
	add := func(line int, msg string) { problems = append(problems, fmt.Sprintf("%s:%d: %s", path, line, msg)) }
	dir := filepath.Dir(filepath.Join(root, path))
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var open string // the opening fence run, "" outside a fence
	openLine := 0
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSuffix(sc.Text(), "\r")
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			run := m[1]
			switch {
			case open == "":
				if run[0] == '`' && strings.Contains(m[2], "`") {
					break // an inline code span that starts a line, not a fence
				}
				open, openLine = run, n
				continue
			case run[0] == open[0] && len(run) >= len(open) && strings.TrimSpace(m[2]) == "":
				open = ""
				continue
			}
		}
		if open != "" {
			continue
		}
		if strings.ContainsRune(line, 0) {
			add(n, "NUL byte")
		}
		stripped := inlineRE.ReplaceAllString(line, "")
		for _, m := range linkRE.FindAllStringSubmatch(stripped, -1) {
			if msg := checkLink(dir, root, m[1]); msg != "" {
				add(n, msg)
			}
		}
	}
	if open != "" {
		add(openLine, fmt.Sprintf("code fence %q is never closed", open))
	}
	return problems
}

// checkLink returns a message when the relative target does not exist, else "".
func checkLink(dir, root, target string) string {
	t := strings.Trim(target, "<>")
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	if u, err := url.Parse(t); err == nil && (u.Scheme != "" || u.Host != "") {
		return "" // https:, mailto:, //host
	}
	if i := strings.IndexAny(t, "#?"); i >= 0 {
		t = t[:i]
	}
	if t == "" {
		return ""
	}
	if dec, err := url.PathUnescape(t); err == nil {
		t = dec
	}
	base := dir
	if strings.HasPrefix(t, "/") {
		base, t = root, strings.TrimPrefix(t, "/")
	}
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(t))); err != nil {
		return fmt.Sprintf("link %q: no such file or folder", target)
	}
	return ""
}
