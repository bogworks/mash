package release

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// zoneTokenRE matches what a zone surface looks like in shipped text: a removed command or flag, the removed policy
// field, the replace-zone-key verb, a six-token mail subject (agents.mail. then four more tokens, placeholders such
// as <zone> and wildcards included; the five-token subject agents.mail.<agent>.<owner>.<name> has only three after
// the prefix and does not match), and the <zone> placeholder.
var zoneTokenRE = regexp.MustCompile(
	`set-zone|--zone|subject_label|replace-zone-key|<zone>|` +
		"agents\\.mail\\.[^.\\s\"'`]+\\.[^.\\s\"'`]+\\.[^.\\s\"'`]+\\.[^.\\s\"'`]+")

// zoneAllow is the explicit allow-list: a tracked file is exempt when its path equals path, has the directory prefix
// path (a path ending in /), or matches the glob path. An entry with a line is narrower: only the lines of that
// file containing the line substring are exempt. Keep it short; every entry says why.
var zoneAllow = []struct{ path, line, why string }{
	{"docs/decisions/", "", "ADRs are history; ADR 0091 itself names every removed token"},
	{"CHANGELOG.md", "", "history of released changes"},
	{"changelog.d/", "", "history: release-note fragments"},
	{"docs/evidence/", "", "history: recorded evidence"},
	{"docs/plans/", "", "history: plans as written"},
	{"docs/specs/", "", "history: specs as written (the zone removal spec describes what it removes)"},
	{"cmd/mashctl/unzone_test.go", "", "the unzone test publishes to a zoned subject"},
	{"internal/admin/unzone_test.go", "", "the unzone admin test uses zoned records and subjects"},
	{"cmd/mashctl/main_test.go", "", "TestMashctlNoZone asserts set-zone and --zone are refused"},
	{"internal/sap/", "", "legacy zoned-subject parsing (LegacyZoneOf*) and its golden and subject tests"},
	{"internal/store/", "", "zoned.go reads a zoned directory record for the unzone path; its tests and the golden and store tests use zoned data"},
	{"internal/admin/inbox_subjects_test.go", "", "tests that six-token subjects stay publishable until unzone runs"},
	{"internal/listset/listset_test.go", "", "generic subject-pattern matching cases; the six-token strings are just test data"},
	{"cmd/mashctl/add_test.go", "", "asserts add rejects --zone as an unknown option"},
	{"cmd/mashctl/label_test.go", "", "asserts label output no longer mentions set-zone"},
	{"docs/operations.md", "| set an endpoint's zone |", "the removal table: set-zone as a removed command"},
	{"docs/operations.md", "| placed the endpoint in a zone |", "the removal table: --zone as a removed flag"},
	{"docs/operations.md", "| policy file naming a zone,", "the removal table: subject_label as a removed policy field"},
	{"internal/release/zonetokens_test.go", "", "this guard names the tokens it searches for"},
}

// zoneScanned says whether a tracked path is shipped code or docs worth scanning.
func zoneScanned(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".md", ".yml", ".yaml", ".json", ".sh", ".txt":
		return true
	case "":
		return strings.HasPrefix(rel, "scripts/") || strings.Contains(rel, "/scripts/")
	}
	return false
}

// zoneLineAllowed says whether one line of rel is exempt by a line-substring entry.
func zoneLineAllowed(rel, line string) bool {
	for _, a := range zoneAllow {
		if a.line != "" && a.path == rel && strings.Contains(line, a.line) {
			return true
		}
	}
	return false
}

func zoneAllowed(rel string) bool {
	for _, a := range zoneAllow {
		if a.line != "" {
			continue
		}
		if strings.HasSuffix(a.path, "/") {
			if strings.HasPrefix(rel, a.path) {
				return true
			}
		} else if ok, _ := path.Match(a.path, rel); ok {
			return true
		}
	}
	return false
}

// TestAT018NoZoneTokensOutsideTheAllowList: AT-018 of the zone removal (ADR 0091): no zone surface remains in shipped
// code or docs outside the explicit allow-list.
func TestAT018NoZoneTokensOutsideTheAllowList(t *testing.T) {
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
		if rel == "" || !zoneScanned(rel) || zoneAllowed(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue // a deleted or non-regular tracked path
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := zoneTokenRE.FindString(line); m != "" && !zoneLineAllowed(rel, line) {
				bad = append(bad, fmt.Sprintf("%s:%d: %q", rel, i+1, m))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("zone tokens outside the allow-list:\n  %s\nadd to the allow-list with a reason, or remove the zone reference (ADR 0091)",
			strings.Join(bad, "\n  "))
	}
}
