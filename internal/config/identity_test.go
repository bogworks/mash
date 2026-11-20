package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func loadIdentity(t *testing.T, fileBody string, env map[string]string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	if fileBody != "" {
		writeFile(t, home, fileBody)
	}
	return load(envOf(env), home)
}

const idFile = `{"servers":"tls://x:4222"`

func TestAddressIsBuiltFromOwnerGroupInstance(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_ENDPOINT": "alice.dev.design"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := sap.NewAddress("alice", "dev", "design")
	if id != want || id.String() != "alice.dev.design" || id.Flat() != "alice_dev_design" {
		t.Fatalf("got %+v", id)
	}
	if c.Group != "dev" || c.Source["endpoint"] != "MASH_ENDPOINT" {
		t.Fatalf("group %q source %v", c.Group, c.Source)
	}
}

func TestEndpointWithoutGroupIsRefused(t *testing.T) {
	for _, ep := range []string{"a", "o.a", "o..a"} {
		_, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_ENDPOINT": ep})
		if err == nil || !strings.Contains(err.Error(), "MASH_ENDPOINT") {
			t.Fatalf("MASH_ENDPOINT=%q: got %v, want an error naming MASH_ENDPOINT", ep, err)
		}
	}
	// Identity refuses it too, for a Config built by hand.
	_, err := (Config{Owner: "o", Name: "a"}).Identity()
	if !errors.Is(err, ErrGroupMissing) {
		t.Fatalf("Identity: got %v", err)
	}
	for _, want := range []string{"mash: no endpoint", "MASH_ENDPOINT", "alice.dev.design"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if CodeGroupMissing != "group_missing" {
		t.Errorf("CodeGroupMissing = %q", CodeGroupMissing)
	}
}

func TestGroupIsNotAConfigFileKey(t *testing.T) {
	// "group" in config.json is an unknown key, like any other: ignored, never read as the group.
	if err := ValidateFile([]byte(idFile + `,"group":"dev"}`)); err != nil {
		t.Fatalf("ValidateFile: %v", err)
	}
	_, err := loadIdentity(t, idFile+`,"group":"dev"}`, map[string]string{"MASH_ENDPOINT": "a"})
	if err == nil {
		t.Fatalf("a config.json group was read: %v", err)
	}
	c, err := loadIdentity(t, idFile+`,"group":"dev"}`, nil)
	if err != nil || c.Group != "" || c.FileValues["group"] != "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestInertConfigNeedsNoGroup(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Identity(); err == nil || !strings.Contains(err.Error(), "no identity") {
		t.Fatalf("got %v", err)
	}
}

func TestCredsNameIsOwnerGroupInstance(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
	if err != nil {
		t.Fatal(err)
	}
	if c.CredsName() != "o.g.a" {
		t.Fatalf("CredsName = %q", c.CredsName())
	}
	if want := filepath.Join(c.Dir, "o.g.a.creds"); c.Creds != want {
		t.Fatalf("Creds = %q, want %q", c.Creds, want)
	}
}

func TestInvalidAddressTokens(t *testing.T) {
	long := func(n int) string { return "a" + strings.Repeat("b", n-1) }
	for _, tc := range []struct{ owner, group, name, want string }{
		{"Upper", "g", "a", "owner token"},
		{long(33), "g", "a", "owner token"},
		{"o", "Upper", "a", "group token"},
		{"o", "under_score", "a", "group token"},
		{"o", long(33), "a", "group token"},
		{"o", "g", long(33), "instance token"},
		{"o", "g", "n_x", "instance token"},
		{long(32), long(32), long(2), "MASH_ENDPOINT"},
	} {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://x:4222"}`)
		_, err := load(envOf(map[string]string{"MASH_ENDPOINT": tc.owner + "." + tc.group + "." + tc.name}), home)
		if err == nil || !strings.HasPrefix(err.Error(), "mash: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v", tc, err)
		}
	}
}
