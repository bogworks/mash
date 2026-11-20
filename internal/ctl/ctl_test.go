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

func TestParseAddr(t *testing.T) {
	for _, in := range []string{"acme.ops.recv", "acme_ops_recv"} {
		a, err := parseAddr(in)
		if err != nil || a.String() != "acme.ops.recv" {
			t.Errorf("%q: %v %v", in, a, err)
		}
	}
	for _, in := range []string{"", "recv", "a.b", "a.b.c.d", "a_b", "A.b.c", "a.b.c_d", "a.b."} {
		_, err := parseAddr(in)
		want := "mash: not an endpoint address (the value is not shown): give owner.group.instance"
		if err == nil || err.Error() != want {
			t.Errorf("%q: %v", in, err)
		}
	}
}
