package policy

import (
	"strconv"
	"strings"
	"testing"
)

func TestNonStringKeysRefused(t *testing.T) {
	cases := []struct {
		name, src string
		line      int
		key, kind string
	}{
		{"endpoint null", "endpoints:\n  null: {}\n", 2, "null", "null"},
		{"endpoint Null", "endpoints:\n  Null: {}\n", 2, "Null", "null"},
		{"endpoint NULL", "endpoints:\n  NULL: {}\n", 2, "NULL", "null"},
		{"endpoint tilde", "endpoints:\n  ~: {}\n", 2, "~", "null"},
		{"label null", "endpoints:\n  a:\n    labels:\n      null: x\n", 4, "null", "null"},
		{"label in flow map", "endpoints:\n  a: {labels: {~: x}}\n", 2, "~", "null"},
		{"in rule list", "rules:\n  - name: r\n    action: allow\n    from: {}\n    to:\n      ~: x\n", 6, "~", "null"},
		{"top null", "null: x\n", 1, "null", "null"},
		{"top tilde", "~: x\n", 1, "~", "null"},
		{"int", "endpoints:\n  123: {}\n", 2, "123", "int"},
		{"bool", "endpoints:\n  true: {}\n", 2, "true", "bool"},
		{"bool True", "endpoints:\n  True: {}\n", 2, "True", "bool"},
		{"float", "endpoints:\n  1.5: {}\n", 2, "1.5", "float"},
		{"timestamp", "endpoints:\n  2001-12-14: {}\n", 2, "2001-12-14", "timestamp"},
		{"merge", "endpoints:\n  <<: {}\n", 2, "<<", "merge"},
		{"mapping key", "endpoints:\n  ? {a: b}\n  : {}\n", 2, "a mapping", "mapping"},
		{"sequence key", "endpoints:\n  ? [a]\n  : {}\n", 2, "a sequence", "sequence"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.src))
			if err == nil {
				t.Fatalf("parse succeeded, want a refusal\n%s", c.src)
			}
			want := "line " + strconv.Itoa(c.line) + ": map key " + c.key + " is not a plain string (it reads as " + c.kind + ")"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("got %v, want it to contain %q", err, want)
			}
		})
	}
}

func TestQuotedKeysAreStrings(t *testing.T) {
	// "null" and "true" are valid endpoint names, so the whole file parses.
	f := mustParse(t, "endpoints:\n  \"null\": {}\n  \"true\": {}\n")
	for _, n := range []string{"null", "true"} {
		if _, ok := f.Endpoints[n]; !ok {
			t.Fatalf("endpoint %q missing: %v", n, f.Endpoints)
		}
	}
	// "~" and "123" are not valid endpoint names, so Check refuses them, but not as keys.
	for _, name := range []string{`"~"`, `"123"`} {
		_, err := Parse([]byte("endpoints:\n  " + name + ": {}\n"))
		if err != nil && strings.Contains(err.Error(), "not a plain string") {
			t.Fatalf("quoted %s refused as a non-string key: %v", name, err)
		}
	}
	mustParse(t, "endpoints:\n  a:\n    labels:\n      \"null\": x\n")
}

func TestDuplicateKeyStillReported(t *testing.T) {
	_, err := Parse([]byte("endpoints:\n  a: {}\n  a: {}\n"))
	if err == nil || !strings.Contains(err.Error(), `line 3: duplicate key "a" (first at line 2)`) {
		t.Fatalf("got %v", err)
	}
}

// yaml.v3 itself rejects a bare empty key; it must never parse.
func TestEmptyKeyRefused(t *testing.T) {
	for _, src := range []string{": x\n", "endpoints:\n  : {}\n", "endpoints:\n  a:\n    labels:\n      : x\n"} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Fatalf("parsed %q", src)
		}
	}
}
