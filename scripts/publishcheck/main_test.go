package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const now = "2026-10-31T12:00:00Z"

// rel is one release object of gh api repos/R/releases; published is "" for a draft.
func rel(tag string, draft, pre bool, created, published string) string {
	pub := "null"
	if published != "" {
		pub = `"` + published + `"`
	}
	return `{"tag_name":"` + tag + `","draft":` + boolJSON(draft) + `,"prerelease":` + boolJSON(pre) + `,"created_at":"` + created + `","published_at":` + pub + `}`
}

// withAsset adds one asset (created, updated) to a rel() release.
func withAsset(r, created, updated string) string {
	return strings.TrimSuffix(r, "}") + `,"assets":[{"created_at":"` + created + `","updated_at":"` + updated + `"}]}`
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func tagsJSON(tags ...string) string {
	var refs []string
	for _, t := range tags {
		refs = append(refs, `{"ref":"refs/tags/`+t+`"}`)
	}
	return "[[" + strings.Join(refs, ",") + "]]"
}

func relsJSON(rs ...string) string { return "[[" + strings.Join(rs, ",") + "]]" }

// Each case is one repository state: the exit code and the violation lines, in order.
func TestRun(t *testing.T) {
	for name, tc := range map[string]struct {
		tags, rels string
		want       int
		lines      []string
	}{
		"clean": {
			tagsJSON("v0.2.0", "v0.2.1"),
			relsJSON(rel("v0.2.1", false, false, "2026-10-02T00:00:00Z", "2026-10-02T00:10:00Z"), rel("v0.2.0", false, false, "2026-10-01T00:00:00Z", "2026-10-01T00:10:00Z")),
			0, nil,
		},
		"tag with no release": {
			tagsJSON("v0.2.8"), relsJSON(),
			1, []string{"R1 o/r v0.2.8: tag has no release"},
		},
		"non-rc draft-only tag younger than 2h": {
			tagsJSON("v0.2.8"), relsJSON(rel("v0.2.8", true, false, "2026-10-31T11:30:00Z", "")),
			0, nil,
		},
		"non-rc draft-only tag older than 2h is R3 only": {
			tagsJSON("v0.2.8"), relsJSON(rel("v0.2.8", true, false, "2026-10-31T09:00:00Z", "")),
			1, []string{"R3 o/r v0.2.8: draft release last touched 2026-10-31T09:00:00Z, 3h0m0s old (limit 2h0m0s)"},
		},
		"old commit, fresh asset upload keeps a draft young": {
			tagsJSON("v0.2.8"), relsJSON(withAsset(rel("v0.2.8", true, false, "2026-09-01T00:00:00Z", ""), "2026-10-31T11:00:00Z", "2026-10-31T11:20:00Z")),
			0, nil,
		},
		"old commit, old asset: R3 shows the asset time": {
			tagsJSON("v0.2.8"), relsJSON(withAsset(rel("v0.2.8", true, false, "2026-09-01T00:00:00Z", ""), "2026-10-31T08:00:00Z", "2026-10-31T08:30:00Z")),
			1, []string{"R3 o/r v0.2.8: draft release last touched 2026-10-31T08:30:00Z, 3h30m0s old (limit 2h0m0s)"},
		},
		"rc tag with no release is exempt": {
			tagsJSON("v0.3.0-rc1"), relsJSON(),
			0, nil,
		},
		"rc draft older than 2h": {
			tagsJSON("v0.3.0-rc1"), relsJSON(rel("v0.3.0-rc1", true, true, "2026-10-31T09:59:00Z", "")),
			1, []string{"R3 o/r v0.3.0-rc1: draft release last touched 2026-10-31T09:59:00Z, 2h1m0s old (limit 2h0m0s)"},
		},
		"rc draft younger than 2h": {
			tagsJSON("v0.3.0-rc1"), relsJSON(rel("v0.3.0-rc1", true, true, "2026-10-31T10:30:00Z", "")),
			0, nil,
		},
		"draft exactly 2h is not a violation": {
			tagsJSON("v0.2.9"),
			relsJSON(rel("v0.2.9", false, false, "2026-10-01T00:00:00Z", "2026-10-01T00:10:00Z"), rel("v0.3.0", true, false, "2026-10-31T10:00:00Z", "")),
			0, nil,
		},
		"out of order publication": {
			tagsJSON("v0.2.9", "v0.2.10"),
			relsJSON(
				rel("v0.2.10", false, false, "2026-10-01T00:00:00Z", "2026-10-01T00:10:00Z"),
				rel("v0.2.9", false, false, "2026-10-02T00:00:00Z", "2026-10-02T00:10:00Z")),
			1, []string{"R2 o/r v0.2.9: published 2026-10-02T00:10:00Z, after v0.2.10 (2026-10-01T00:10:00Z)"},
		},
		"rc releases are outside the order": {
			tagsJSON("v0.2.0", "v0.3.0-rc1"),
			relsJSON(
				rel("v0.3.0-rc1", false, true, "2026-10-01T00:00:00Z", "2026-10-03T00:10:00Z"),
				rel("v0.2.0", false, false, "2026-10-02T00:00:00Z", "2026-10-02T00:10:00Z")),
			0, nil,
		},
		"not a semver tag": {
			tagsJSON("v1", "v0.2.0-beta"), relsJSON(),
			1, []string{"R1 o/r v0.2.0-beta: not a vX.Y.Z[-rcN] tag", "R1 o/r v1: not a vX.Y.Z[-rcN] tag"},
		},
		"malformed releases json": {
			tagsJSON(), `{"not":"pages"`,
			2, nil,
		},
		"malformed tags json": {
			`[{"ref":1}]`, relsJSON(),
			2, nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tf, rf := filepath.Join(dir, "tags.json"), filepath.Join(dir, "releases.json")
			if err := os.WriteFile(tf, []byte(tc.tags), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rf, []byte(tc.rels), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			got := run([]string{"-repo", "o/r", "-tags", tf, "-releases", rf, "-now", now}, &out, &errb)
			if got != tc.want {
				t.Fatalf("exit %d, want %d (stdout %q stderr %q)", got, tc.want, out.String(), errb.String())
			}
			want := strings.Join(tc.lines, "\n")
			if want != "" {
				want += "\n"
			}
			if out.String() != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", out.String(), want)
			}
		})
	}
}

func TestRunUsageIsExit2(t *testing.T) {
	var out, errb bytes.Buffer
	if got := run([]string{"-repo", "o/r"}, &out, &errb); got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
	if got := run([]string{"-tags", "a", "-releases", "b", "-now", "yesterday"}, &out, &errb); got != 2 {
		t.Errorf("bad -now: exit %d, want 2", got)
	}
}

// Versions compare by number, not text, and an rc sorts below its release.
func TestCompare(t *testing.T) {
	order := []string{"v0.2.9", "v0.2.10", "v0.3.0-rc1", "v0.3.0-rc2", "v0.3.0", "v1.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := parseVersion(order[i-1])
		b, _ := parseVersion(order[i])
		if compare(a, b) >= 0 || compare(b, a) <= 0 {
			t.Errorf("%s should sort below %s", order[i-1], order[i])
		}
	}
}
