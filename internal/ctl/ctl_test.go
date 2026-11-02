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
	args := []string{"provision", "a", "--describe", "d", "--cap", "q", "--cap", "p", "--role", "r"}
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
	r := store.Record{Name: "a", Zone: hostile, Description: hostile, Caps: []string{hostile}}
	for _, got := range []string{zoneSuffix(r), metaSuffix(r), labelList(map[string]string{hostile: hostile}), NoRevokeWarning("a", hostile)} {
		if strings.ContainsAny(got, "\x1b\r") || !strings.Contains(got, want) {
			t.Errorf("not escaped: %q", got)
		}
	}
}
