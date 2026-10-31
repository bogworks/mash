package store

import (
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestDefaultLimitsEqualTheConstants(t *testing.T) {
	d := DefaultLimits()
	if d.InboxMaxBytes != 16<<20 || d.InboxMaxAge != 30*24*time.Hour || d.DirectoryMaxBytes != 16<<20 ||
		d.LeaseMaxBytes != 4<<20 || d.InboxMaxConsumers != -1 {
		t.Fatalf("defaults %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(InboxConfig("o", "a"), InboxConfigFor("o", "a", d)) ||
		!reflect.DeepEqual(DirectoryConfig(), DirectoryConfigFor(d)) || !reflect.DeepEqual(LeaseConfig(), LeaseConfigFor(d)) {
		t.Fatal("wrappers differ from the *For functions")
	}
	if c := InboxConfig("o", "a"); c.MaxBytes != InboxMaxBytes || c.MaxAge != InboxMaxAge || c.MaxConsumers != -1 {
		t.Fatalf("inbox %+v", c)
	}
	if DirectoryConfig().MaxBytes != DirectoryMaxBytes || LeaseConfig().MaxBytes != LeaseMaxBytes {
		t.Fatal("bucket caps")
	}
}

func TestLimitsValidate(t *testing.T) {
	for _, c := range []struct {
		field string
		mod   func(*Limits)
	}{
		{"inbox_max_bytes", func(l *Limits) { l.InboxMaxBytes = 2*MaxMsgSize - 1 }},
		{"inbox_max_age", func(l *Limits) { l.InboxMaxAge = time.Hour - 1 }},
		{"directory_max_bytes", func(l *Limits) { l.DirectoryMaxBytes = 1<<20 - 1 }},
		{"lease_max_bytes", func(l *Limits) { l.LeaseMaxBytes = 1<<20 - 1 }},
		{"inbox_max_consumers", func(l *Limits) { l.InboxMaxConsumers = 0 }},
		{"inbox_max_consumers", func(l *Limits) { l.InboxMaxConsumers = -2 }},
	} {
		l := DefaultLimits()
		c.mod(&l)
		if err := l.Validate(); err == nil || !strings.Contains(err.Error(), c.field) {
			t.Errorf("%s: %v", c.field, err)
		}
	}
	l := Limits{InboxMaxBytes: 2 * MaxMsgSize, InboxMaxAge: time.Hour, DirectoryMaxBytes: 1 << 20, LeaseMaxBytes: 1 << 20, InboxMaxConsumers: 1}
	if err := l.Validate(); err != nil {
		t.Fatalf("minimums: %v", err)
	}
}
