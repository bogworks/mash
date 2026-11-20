// Characterisation of ValidName, and of the token rule Load applies through the address.

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// ValidName is the name rule for a name outside an address (an owner given to init and adopt): 1 to 48 characters.
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

func TestLoadAppliesTokenRuleToOwnerGroupAndInstance(t *testing.T) {
	o32, i28 := strings.Repeat("a", 32), strings.Repeat("c", 28)
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222"}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": o32 + ".g." + i28}), home)
	if err != nil {
		t.Fatalf("32-character owner and 28-character instance refused: %v", err)
	}
	if c.Owner != o32 || c.Name != i28 {
		t.Errorf("owner %q name %q", c.Owner, c.Name)
	}
	if want := filepath.Join(home, ".config", "mash", o32+".g."+i28+".creds"); c.Creds != want {
		t.Errorf("creds %q, want %q", c.Creds, want)
	}
	// The whole address is at most 64 characters: 32 + 1 + 29 + 2 = 64 is the bound, 65 is refused.
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": o32 + ".g." + i28 + "c"}), home); err != nil {
		t.Errorf("64-character address refused: %v", err)
	}
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": o32 + ".g." + i28 + "cc"}), home); err == nil {
		t.Error("65-character address accepted")
	}
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g." + strings.Repeat("a", 33)}), home); err == nil {
		t.Error("33-character instance accepted")
	}
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o." + strings.Repeat("g", 33) + ".a"}), home); err == nil {
		t.Error("33-character group accepted")
	}
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": o32 + "a.g.a"}), home); err == nil {
		t.Error("33-character owner accepted")
	}
	for _, bad := range []string{"Up", "a_b", "a.b", "1a", "a--b", "a-"} {
		if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": bad + ".g.a"}), home); err == nil {
			t.Errorf("owner %q accepted", bad)
		}
	}
}
