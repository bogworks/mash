package ctl

import (
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
)

func TestAdminDeadline(t *testing.T) {
	if adminDeadline(config.Config{}) != 30*time.Second || adminDeadline(config.Config{AdminTimeout: 2 * time.Minute}) != 2*time.Minute {
		t.Fatal("adminDeadline")
	}
}

func TestShortKey(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", "12345678": "12345678", "UABCDEFGHIJK": "UABCDEFG"} {
		if got := ShortKey(in); got != want {
			t.Errorf("ShortKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenAdminMissingSettings(t *testing.T) {
	_, _, _, err := OpenAdminWith("mashctl status", config.Config{Owner: "acme", AdminCreds: "/x/admin.creds"})
	want := `mashctl: no server is configured: set MASH_SERVER, or "servers" in config.json`
	if err == nil || err.Error() != want {
		t.Fatalf("no URL: %v", err)
	}
	if strings.Contains(err.Error(), "MASH_ADMIN_CREDS") {
		t.Errorf("names a setting that is set: %v", err)
	}
	_, _, _, err = OpenAdminWith("mashctl status", config.Config{Owner: "acme", URL: "tls://x:4222"})
	want = `mashctl: no admin credentials: set MASH_ADMIN_CREDS, or "admin_creds" in config.json`
	if err == nil || err.Error() != want {
		t.Fatalf("no creds: %v", err)
	}
	if strings.Contains(err.Error(), "MASH_SERVER") {
		t.Errorf("names a setting that is set: %v", err)
	}
}
