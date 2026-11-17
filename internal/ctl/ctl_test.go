package ctl

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

func TestAdminDeadline(t *testing.T) {
	if adminDeadline(config.Config{}) != 30*time.Second || adminDeadline(config.Config{AdminTimeout: 2 * time.Minute}) != 2*time.Minute {
		t.Fatal("adminDeadline")
	}
}

func TestAdminMetaFlags(t *testing.T) {
	u, err := setMetaUpdate("mashctl", []string{"set-meta", "a", "--describe", "hi there", "--cap", "x", "--cap", "y", "--uncap", "z"})
	if err != nil || u.Description == nil || *u.Description != "hi there" || !reflect.DeepEqual(u.AddCaps, []string{"x", "y"}) || !reflect.DeepEqual(u.DropCaps, []string{"z"}) || u.ClearCaps {
		t.Fatalf("%+v %v", u, err)
	}
	u, err = setMetaUpdate("mashctl", []string{"set-meta", "a", "--describe", "-", "--cap", "-"})
	if err != nil || *u.Description != "" || !u.ClearCaps || len(u.AddCaps) != 0 {
		t.Fatalf("%+v %v", u, err)
	}
	for _, bad := range [][]string{{"--cap"}, {"--describe", "a", "--describe", "b"}, {"--bogus", "x"}} {
		if _, err := setMetaUpdate("mashctl", append([]string{"set-meta", "a"}, bad...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	args := []string{"provision", "o.g.a", "--describe", "d", "--cap", "q", "--cap", "p", "--role", "r"}
	opt := func(name string) string {
		for i := 2; i+1 < len(args); i++ {
			if args[i] == name {
				return args[i+1]
			}
		}
		return ""
	}
	m, err := provisionMeta("mashctl", args, opt)
	if err != nil || m.Description != "d" || !reflect.DeepEqual(m.Caps, []string{"q", "p"}) || m.Role != "r" {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestShortKey(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "abc", "12345678": "12345678", "UABCDEFGHIJK": "UABCDEFG"} {
		if got := ShortKey(in); got != want {
			t.Errorf("ShortKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListFieldsAreEscaped(t *testing.T) {
	hostile := "x\x1b[2K\r\nREFUSED"
	want := `x\x1b[2K\x0d\x0aREFUSED`
	r := store.Record{Owner: "o", Group: "g", Instance: "a", Description: hostile, Caps: []string{hostile}}
	for _, got := range []string{metaSuffix(r), labelList(map[string]string{hostile: hostile}), NoRevokeWarning("a", hostile)} {
		if strings.ContainsAny(got, "\x1b\r") || !strings.Contains(got, want) {
			t.Errorf("not escaped: %q", got)
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
