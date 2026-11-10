// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// ValidName is the one rule for both an endpoint name and an owner (Load applies it to each).
func TestGoldenValidNameBoundaries(t *testing.T) {
	for _, s := range []string{
		"a", "z", "a0", "a-b", "a-b-c", "a1-2b", "ab-1", "build-1", "acme-corp",
		"abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl", // 48
		strings.Repeat("a", 48), "a" + strings.Repeat("0", 47), strings.Repeat("a-", 23) + "ab",
	} {
		if len(s) > 48 || !ValidName(s) {
			t.Errorf("ValidName(%q) (len %d) = false, today it accepts it", s, len(s))
		}
	}
	for _, s := range []string{
		"", strings.Repeat("a", 49), "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklm",
		"A", "aB", "Ab", "a_b", "_a", "a.b", ".", "a.", "1a", "0", "9-a", "-a", "a-", "a--b", "a---b", "-",
		"a b", " a", "a ", "a\n", "a*", "a>", "é", "aé",
	} {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, today it refuses it", s)
		}
	}
}

func TestGoldenLoadAppliesValidNameToOwnerAndName(t *testing.T) {
	n48 := strings.Repeat("a", 48)
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","owner":"`+n48+`"}`)
	c, err := Load(envOf(map[string]string{"MASH_NAME": n48}), home)
	if err != nil {
		t.Fatalf("48-character owner and name refused: %v", err)
	}
	if c.Owner != n48 || c.Name != n48 {
		t.Errorf("owner %q name %q", c.Owner, c.Name)
	}
	// the credentials file mash derives from the name
	if want := filepath.Join(home, ".config", "mash", n48+".creds"); c.Creds != want {
		t.Errorf("creds %q, want %q", c.Creds, want)
	}
	n49 := n48 + "a"
	if _, err := Load(envOf(map[string]string{"MASH_NAME": n49}), home); err == nil {
		t.Error("49-character name accepted")
	}
	writeFile(t, home, `{"url":"tls://x:4222","owner":"`+n49+`"}`)
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home); err == nil {
		t.Error("49-character owner accepted")
	}
	for _, bad := range []string{"Up", "a_b", "a.b", "1a", "a--b", "a-"} {
		writeFile(t, home, `{"url":"tls://x:4222","owner":"`+bad+`"}`)
		if _, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home); err == nil {
			t.Errorf("owner %q accepted", bad)
		}
	}
}
