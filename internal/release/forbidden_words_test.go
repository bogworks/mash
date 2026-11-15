package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbiddenWordRE matches, as whole words, the names of layers above mash. mash carries none of them in shipped code,
// flags, field names, error texts or docs (ADR 0133).
var forbiddenWordRE = regexp.MustCompile(`(?i)\b(wrangler|herd|lane|homerange|rangehand)\b`)

// forbiddenLinkRE matches a markdown link target into the ADRs: an ADR file name is history and may carry a word.
var forbiddenLinkRE = regexp.MustCompile(`\]\([^)\s]*decisions/[^)\s]*\)`)

// forbiddenExempt lists the tracked paths that may carry the words: a path ending in / is a directory prefix.
var forbiddenExempt = []struct{ path, why string }{
	{"docs/decisions/", "ADRs are history"},
	{"docs/plans/", "history: plans as written"},
	{"docs/specs/", "history: specs as written"},
	{"docs/evidence/", "history: recorded evidence"},
	{"CHANGELOG.md", "history of released changes"},
	{"changelog.d/", "history: release-note fragments"},
	{"scripts/scrubcheck/words.txt", "the scrub list itself"},
	{"internal/release/forbidden_words_test.go", "this guard names the words"},
}

func forbiddenScanned(rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return false // fixtures and tests are cleaned by hand; the guard covers shipped code and docs
	}
	switch filepath.Ext(rel) {
	case ".go", ".md":
		return true
	}
	return false
}

func forbiddenExempted(rel string) bool {
	for _, e := range forbiddenExempt {
		if rel == e.path || (strings.HasSuffix(e.path, "/") && strings.HasPrefix(rel, e.path)) {
			return true
		}
	}
	return false
}

// TestNoWordOfALayerAboveInShippedCodeAndDocs: ADR 0133.
func TestNoWordOfALayerAboveInShippedCodeAndDocs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	var bad []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || !forbiddenScanned(rel) || forbiddenExempted(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue // a deleted or non-regular tracked path
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := forbiddenWordRE.FindString(forbiddenLinkRE.ReplaceAllString(line, "](x)")); m != "" {
				bad = append(bad, fmt.Sprintf("%s:%d: %q", rel, i+1, m))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("words of a layer above mash in shipped code or docs:\n  %s\nrename the concept in mash's own terms (ADR 0133)",
			strings.Join(bad, "\n  "))
	}
}
