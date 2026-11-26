package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AT-001 (REQ-006, TO-guide): docs/troubleshooting.md opens with the section "A refused message", and the section says the
// three things a reader needs: the message is kept until it ages out, nothing needs doing, and the sender is not shown.
func TestTroubleshootingHasRefusedMessageSection(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "troubleshooting.md"))
	if err != nil {
		t.Fatal(err)
	}
	const heading = "## A refused message\n"
	lines := strings.SplitAfter(string(b), "\n")
	first := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			first = l
			break
		}
	}
	if first != heading {
		t.Fatalf("the first section of docs/troubleshooting.md is %q, want %q", first, heading)
	}
	_, rest, _ := strings.Cut(string(b), heading)
	section, _, _ := strings.Cut(rest, "\n## ")
	for _, fact := range []string{
		"until it ages out",
		"nothing needs doing",
		"The sender is not shown",
	} {
		if !strings.Contains(section, fact) {
			t.Errorf("the section %q lacks %q", strings.TrimSpace(heading), fact)
		}
	}
}
