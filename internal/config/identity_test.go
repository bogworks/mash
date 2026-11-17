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

const idFile = `{"url":"tls://x:4222","owner":"o"`

func TestAddressIsBuiltFromOwnerGroupInstance(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "design", "MASH_GROUP": "dev", "MASH_OWNER": "alice"})
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
	if c.Group != "dev" || c.Source["group"] != "MASH_GROUP" || c.Source["owner"] != "MASH_OWNER" {
		t.Fatalf("group %q source %v", c.Group, c.Source)
	}
}

func TestMissingGroupIsRefused(t *testing.T) {
	_, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a"})
	if !errors.Is(err, ErrGroupMissing) {
		t.Fatalf("Load: got %v, want ErrGroupMissing", err)
	}
	for _, want := range []string{"mash: no group", "MASH_GROUP", "no default", "alice.dev.design"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if CodeGroupMissing != "group_missing" {
		t.Errorf("CodeGroupMissing = %q", CodeGroupMissing)
	}
	// An empty MASH_GROUP reads as unset.
	if _, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_GROUP": ""}); !errors.Is(err, ErrGroupMissing) {
		t.Fatalf("empty MASH_GROUP: got %v", err)
	}
	// Identity refuses it too, for a Config built by hand.
	if _, err := (Config{Owner: "o", Name: "a"}).Identity(); !errors.Is(err, ErrGroupMissing) {
		t.Fatalf("Identity: got %v", err)
	}
}

func TestGroupIsNotAConfigFileKey(t *testing.T) {
	// "group" in config.json is an unknown key, like any other: ignored, never read as the group.
	if err := ValidateFile([]byte(idFile + `,"group":"dev"}`)); err != nil {
		t.Fatalf("ValidateFile: %v", err)
	}
	_, err := loadIdentity(t, idFile+`,"group":"dev"}`, map[string]string{"MASH_NAME": "a"})
	if !errors.Is(err, ErrGroupMissing) {
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
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_GROUP": "g"})
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
		{"Upper", "g", "a", "owner"},
		{long(33), "g", "a", "owner"},
		{"o", "Upper", "a", "MASH_GROUP"},
		{"o", "under_score", "a", "MASH_GROUP"},
		{"o", long(33), "a", "MASH_GROUP"},
		{"o", "g", long(33), "MASH_NAME"},
		{"o", "g", "n_x", "MASH_NAME"},
		{long(32), long(32), long(2), "MASH_OWNER, MASH_GROUP and MASH_NAME"},
	} {
		home := t.TempDir()
		writeFile(t, home, `{"url":"tls://x:4222","owner":"`+tc.owner+`"}`)
		_, err := load(envOf(map[string]string{"MASH_NAME": tc.name, "MASH_GROUP": tc.group}), home)
		if err == nil || !strings.HasPrefix(err.Error(), "mash: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v", tc, err)
		}
	}
}
