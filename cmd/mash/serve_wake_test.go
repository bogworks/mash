package main

import (
	"bytes"
	"strings"
	"testing"
)

func wakeEnv(m map[string]string) (func(string) string, func(string) (string, bool)) {
	return func(k string) string { return m[k] }, func(k string) (string, bool) { v, ok := m[k]; return v, ok && v != "" }
}

func TestServeWakeFlagsBothFormsAndTheFlagBeatsTheEnvironment(t *testing.T) {
	env, lookup := wakeEnv(map[string]string{"MASH_WAKE_COUNT": "9", "MASH_WAKE_AGE": "2h", "OTHER": "keep"})
	for _, args := range [][]string{
		{"serve", "--wake-count", "3", "--wake-age", "30m"},
		{"serve", "--wake-count=3", "--wake-age=30m"},
		{"serve", "--wake-age", "30m", "--wake-count=3"},
	} {
		rest, e, l, err := serveFlags(args, env, lookup)
		if err != nil || len(rest) != 1 || rest[0] != "serve" {
			t.Fatalf("%v: %v %v", args, rest, err)
		}
		if e("MASH_WAKE_COUNT") != "3" || e("MASH_WAKE_AGE") != "30m" || e("OTHER") != "keep" {
			t.Fatalf("%v: env %q %q", args, e("MASH_WAKE_COUNT"), e("MASH_WAKE_AGE"))
		}
		if v, ok := l("MASH_WAKE_COUNT"); !ok || v != "3" {
			t.Fatalf("lookup %q %v", v, ok)
		}
		if v, _ := l("OTHER"); v != "keep" {
			t.Fatal("other keys pass through")
		}
	}
	_, e, _, err := serveFlags([]string{"serve", "--wake-count", "0", "--wake-age", "0"}, env, lookup)
	if err != nil || e("MASH_WAKE_COUNT") != "0" || e("MASH_WAKE_AGE") != "0" {
		t.Fatalf("0 turns each off: %v %q %q", err, e("MASH_WAKE_COUNT"), e("MASH_WAKE_AGE"))
	}
	_, e, _, _ = serveFlags([]string{"serve", "--accept-unverified", "--wake-count=7"}, env, lookup)
	if e("MASH_ACCEPT_UNVERIFIED") != "1" || e("MASH_WAKE_COUNT") != "7" || e("MASH_WAKE_AGE") != "2h" {
		t.Fatal("the flags combine; an unset flag leaves its variable")
	}
	rest, e, _, _ := serveFlags([]string{"inbox", "--wake-count", "3"}, env, lookup)
	if len(rest) != 3 || e("MASH_WAKE_COUNT") != "9" {
		t.Fatal("only serve takes the flags")
	}
}

func TestServeWakeFlagValuesAreValidated(t *testing.T) {
	env, lookup := wakeEnv(nil)
	count := "--wake-count must be a whole number, 0 or more"
	age := "--wake-age must be 0 or a duration such as 30m, 1h, 2d or 90s"
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"serve", "--wake-count=-1"}, count},
		{[]string{"serve", "--wake-count", "x"}, count},
		{[]string{"serve", "--wake-count", "1.5"}, count},
		{[]string{"serve", "--wake-count"}, count},
		{[]string{"serve", "--wake-count="}, count},
		{[]string{"serve", "--wake-age=-5m"}, age},
		{[]string{"serve", "--wake-age", "0s"}, age},
		{[]string{"serve", "--wake-age", "1"}, age},
		{[]string{"serve", "--wake-age"}, age},
	} {
		_, _, _, err := serveFlags(c.args, env, lookup)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%v: %v", c.args, err)
		}
	}
	for _, ok := range [][]string{{"serve", "--wake-count", "1000"}, {"serve", "--wake-count=5000"}, {"serve", "--wake-age", "30d"}, {"serve", "--wake-age=31d"}, {"serve", "--wake-age", "30s"}, {"serve", "--wake-age=1m"}, {"serve", "--wake-age", "90d"}, {"serve", "--wake-age=1h30m"}, {"serve", "--wake-age", "1.5h"}, {"serve", "--wake-age", "0"}} {
		if _, _, _, err := serveFlags(ok, env, lookup); err != nil {
			t.Fatalf("%v: %v", ok, err)
		}
	}
}

// The usage error is exit 2 with one line on stderr and nothing attempted.
func TestServeBadWakeFlagExitsTwo(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"serve", "--wake-count", "-1"}, func(string) string { return "" }, strings.NewReader(""), &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "--wake-count must be a whole number, 0 or more") || out.Len() != 0 {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}
