package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o"}`)
	c, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://x:4222" || c.Owner != "o" || c.Name != "a" {
		t.Fatalf("got %+v", c)
	}
	if c.Creds != filepath.Join(home, ".config", "mash", "a.creds") {
		t.Fatalf("creds %q", c.Creds)
	}
	if c.SettleMS != 100 || c.BatchMsgs != 10 || c.BatchBytes != 16384 {
		t.Fatalf("delivery defaults %+v", c)
	}
	if c.Dir != filepath.Join(home, ".config", "mash") {
		t.Fatalf("dir %q", c.Dir)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://file:4222","owner":"o","settle_ms":5}`)
	c, err := Load(envOf(map[string]string{
		"MASH_NAME": "a", "MASH_URL": "tls://env:4222", "MASH_OWNER": "p",
		"MASH_CREDS": "/x/y.creds", "MASH_SETTLE_MS": "7", "MASH_BATCH_MSGS": "3",
		"MASH_BATCH_BYTES": "99", "MASH_ADMIN_CREDS": "/a.creds",
	}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://env:4222" || c.Owner != "p" || c.Creds != "/x/y.creds" ||
		c.SettleMS != 7 || c.BatchMsgs != 3 || c.BatchBytes != 99 || c.AdminCreds != "/a.creds" {
		t.Fatalf("got %+v", c)
	}
}

func TestFileSettingsUsedWithoutEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://f:4222","owner":"o","settle_ms":5,"batch_msgs":2,"batch_bytes":64}`)
	c, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.SettleMS != 5 || c.BatchMsgs != 2 || c.BatchBytes != 64 {
		t.Fatalf("got %+v", c)
	}
}

func TestInertWithoutName(t *testing.T) {
	home := t.TempDir()
	c, err := Load(envOf(nil), home)
	if err != nil {
		t.Fatalf("inert load must not fail: %v", err)
	}
	if !c.Inert() {
		t.Fatal("expected inert")
	}
}

func TestMissingURLIsErrorUnlessInert(t *testing.T) {
	home := t.TempDir()
	_, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_OWNER": "o"}), home)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("want url error, got %v", err)
	}
	_, err = Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "tls://x:1"}), home)
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("want owner error, got %v", err)
	}
}

func TestRejectsBadNames(t *testing.T) {
	bad := []string{"", "A", "a--b", "-a", "a-", "1a", "a_b", "a.b", strings.Repeat("a", 49)}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
	good := []string{"a", "p-ops2", "mash-alpha", strings.Repeat("a", 48)}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	home := t.TempDir()
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "Bad", "MASH_URL": "u", "MASH_OWNER": "o"}), home); err == nil {
		t.Fatal("bad MASH_NAME must be rejected")
	}
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "O"}), home); err == nil {
		t.Fatal("bad owner must be rejected")
	}
}

func TestBadNumbersRejected(t *testing.T) {
	home := t.TempDir()
	_, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "o", "MASH_SETTLE_MS": "x"}), home)
	if err == nil {
		t.Fatal("non-numeric MASH_SETTLE_MS must be rejected")
	}
}
