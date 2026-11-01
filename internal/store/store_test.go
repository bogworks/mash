package store

import (
	"strings"
	"testing"
)

const baseRecord = `"name":"a","owner":"o","agent":"cc","agent_token":"t","account":"A","user":"U","mail_subject":"agents.mail.cc.o.a","created":"2026-01-01T00:00:00Z"`

func TestParseRecordWithAndWithoutMeta(t *testing.T) {
	r, err := ParseRecord("a", []byte(`{`+baseRecord+`}`))
	if err != nil || r.Role != "" || r.Host != "" || r.Labels != nil {
		t.Fatalf("no meta: %+v %v", r, err)
	}
	r, err = ParseRecord("a", []byte(`{`+baseRecord+`,"role":"r","host":"h","labels":{"team":"d"},"unknown_future":1}`))
	if err != nil || r.Role != "r" || r.Host != "h" || r.Labels["team"] != "d" || len(r.Labels) != 1 || r.User != "U" {
		t.Fatalf("meta: %+v %v", r, err)
	}
	// The required-field checks still apply to a record that has the new fields.
	if _, err := ParseRecord("a", []byte(`{"name":"a","role":"r"}`)); err == nil {
		t.Fatal("incomplete record parsed")
	}
	if _, err := ParseRecord("b", []byte(`{`+baseRecord+`,"role":"r"}`)); err == nil {
		t.Fatal("wrong key parsed")
	}
}

func TestValidMeta(t *testing.T) {
	for _, ok := range []string{"", "reviewer", "my laptop", "héron", strings.Repeat("x", 64)} {
		if err := ValidMeta("role", ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"a\tb", "a\nb", "\x00", strings.Repeat("x", 65)} {
		if err := ValidMeta("role", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUnknownKeyIgnored(t *testing.T) {
	r, err := ParseRecord("a", []byte(`{`+baseRecord+`,"herd":"acme","role":"r"}`))
	if err != nil || r.Name != "a" || r.Role != "r" || len(r.Labels) != 0 {
		t.Fatalf("unknown key: %+v %v", r, err)
	}
}

func TestValidLabels(t *testing.T) {
	eight := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		eight[k] = "v"
	}
	nine := map[string]string{"i": "v"}
	for k, v := range eight {
		nine[k] = v
	}
	for name, l := range map[string]map[string]string{
		"nil": nil, "one": {"team": "acme"}, "eight": eight, "dotted key": {"a.b_c-d9": "x"},
		"empty value": {"k": ""}, "32-char key": {"a" + strings.Repeat("b", 31): "x"},
	} {
		if err := ValidLabels(l); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for name, l := range map[string]map[string]string{
		"nine": nine, "upper key": {"Team": "x"}, "digit first": {"1a": "x"}, "empty key": {"": "x"},
		"33-char key": {"a" + strings.Repeat("b", 32): "x"}, "space in key": {"a b": "x"},
		"tab value": {"k": "a\tb"}, "newline value": {"k": "a\nb"}, "long value": {"k": strings.Repeat("x", 65)},
	} {
		if err := ValidLabels(l); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
