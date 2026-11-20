package ctl

import (
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
