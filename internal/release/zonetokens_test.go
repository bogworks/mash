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
// path (a path ending in /), or matches the glob path. Keep it short; every entry says why.
var zoneAllow = []struct{ path, why string }{
	{"docs/decisions/", "ADRs are history; ADR 0091 itself names every removed token"},
	{"CHANGELOG.md", "history of released changes"},
	{"changelog.d/", "history: release-note fragments"},
	{"docs/evidence/", "history: recorded evidence"},
	{"docs/plans/", "history: plans as written"},
	{"docs/specs/", "history: specs as written (the zone removal spec describes what it removes)"},
	{"cmd/mashctl/zoneguard*.go", "the zone guard and its tests name the removed commands to refuse them"},
	{"cmd/mashctl/unzone*.go", "the unzone command and its tests handle zoned records and subjects"},
	{"internal/admin/unzone*.go", "the unzone admin code and its tests handle zoned records and subjects"},
	{"internal/apply/zonepolicy*.go", "the zone policy check reads the removed template lines"},
	{"cmd/mashctl/main_test.go", "TestMashctlNoZone asserts set-zone and --zone are refused"},
	{"internal/sap/", "legacy zoned-subject parsing (LegacyZoneOf*) and its golden and subject tests"},
	{"internal/store/", "zoned.go reads a zoned directory record for the unzone path; its tests and the golden and store tests use zoned data"},
	{"internal/admin/inbox_subjects_test.go", "tests that six-token subjects stay publishable until unzone runs"},
	{"internal/listset/listset_test.go", "generic subject-pattern matching cases; the six-token strings are just test data"},
	{"cmd/mashctl/add_test.go", "asserts add rejects --zone as an unknown option"},
	{"cmd/mashctl/label_test.go", "asserts label output no longer mentions set-zone"},
	{"cmd/mashctl/policy_adopt.go", "STALE: the adoption note still lists set-zone (a removed command); follow-up to drop it"},
	{"cmd/mashctl/policy_adopt_test.go", "STALE: mirrors the adoption note and a comment on --zone"},
	{"docs/mashctl.md", "STALE: adoption table still lists set-zone and --zone as refused; follow-up to drop them"},
	{"docs/operations.md", "the removal table (removed set-zone, --zone, subject_label) and an adoption sentence that is STALE"},
	{"internal/release/zonetokens_test.go", "this guard names the tokens it searches for"},
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

func zoneAllowed(rel string) bool {
	for _, a := range zoneAllow {
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
			if m := zoneTokenRE.FindString(line); m != "" {
				bad = append(bad, fmt.Sprintf("%s:%d: %q", rel, i+1, m))
			}
		}
	}
	if len(bad) > 0 {
		t.Fatalf("zone tokens outside the allow-list:\n  %s\nadd to the allow-list with a reason, or remove the zone reference (ADR 0091)",
			strings.Join(bad, "\n  "))
	}
}
