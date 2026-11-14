package store

import (
	"encoding/json"
	"fmt"
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

func TestRetiredBucketCapIs64MiB(t *testing.T) {
	if RetiredMaxBytes != 64<<20 || RetiredConfig().MaxBytes != RetiredMaxBytes {
		t.Fatalf("retired bucket cap %d, config %d; want %d", RetiredMaxBytes, RetiredConfig().MaxBytes, 64<<20)
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
	for i := 0; i < MaxLabels; i++ {
		eight[fmt.Sprintf("k%d", i)] = "v"
	}
	nine := map[string]string{"over": "v"}
	for k, v := range eight {
		nine[k] = v
	}
	for name, l := range map[string]map[string]string{
		"nil": nil, "one": {"team": "acme"}, "at the cap": eight, "dotted key": {"a.b_c-d9": "x"},
		"empty value": {"k": ""}, "32-char key": {"a" + strings.Repeat("b", 31): "x"},
	} {
		if err := ValidLabels(l); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for name, l := range map[string]map[string]string{
		"over the cap": nine, "upper key": {"Team": "x"}, "digit first": {"1a": "x"}, "empty key": {"": "x"},
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

func TestValidCapsAndDescription(t *testing.T) {
	nine := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	for name, c := range map[string][]string{"nil": nil, "one": {"nats-accounts"}, "eight": nine[:8], "dotted": {"a.b_c-d9"}, "32 chars": {"a" + strings.Repeat("b", 31)}} {
		if err := ValidCaps(c); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
	for name, c := range map[string][]string{"over the cap": nine, "upper": {"Nats"}, "digit first": {"1a"}, "empty": {""}, "33 chars": {"a" + strings.Repeat("b", 32)},
		"space": {"a b"}, "duplicate": {"a", "a"}} {
		if err := ValidCaps(c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, ok := range []string{"", "runs the nats account tooling", strings.Repeat("x", 256), strings.Repeat("é", 256)} {
		if err := ValidDescription(ok); err != nil {
			t.Errorf("%.20q: %v", ok, err)
		}
	}
	for _, bad := range []string{"a\tb", "a\nb", "\x00", strings.Repeat("x", 257)} {
		if err := ValidDescription(bad); err == nil {
			t.Errorf("%.20q accepted", bad)
		}
	}
	if !ValidCap("a") || ValidCap("A") {
		t.Fatal("ValidCap")
	}
}

func TestRecordZone(t *testing.T) {
	// A record written by an older mash has no zone and loads, with its legacy subject.
	r, err := ParseRecord("a", []byte(`{`+baseRecord+`}`))
	if err != nil || r.Zone != "" {
		t.Fatalf("legacy: %+v %v", r, err)
	}
	if b, _ := json.Marshal(r); strings.Contains(string(b), `"zone"`) {
		t.Fatalf("a record without a zone writes the key: %s", b)
	}
	// A zoned record round trips.
	zoned := strings.Replace(baseRecord, "agents.mail.cc.o.a", "agents.mail.cc.o.z1.a", 1)
	r, err = ParseRecord("a", []byte(`{`+zoned+`,"zone":"z1"}`))
	if err != nil || r.Zone != "z1" || r.MailSubject != "agents.mail.cc.o.z1.a" {
		t.Fatalf("zoned: %+v %v", r, err)
	}
	b, _ := json.Marshal(r)
	if r2, err := ParseRecord("a", b); err != nil || !reflect.DeepEqual(r, r2) {
		t.Fatalf("round trip: %+v %v", r2, err)
	}
	// Mismatches are refused.
	for name, v := range map[string]string{
		"zone with legacy subject":   `{` + baseRecord + `,"zone":"z1"}`,
		"zoned subject without zone": `{` + zoned + `}`,
		"zone for another subject":   `{` + zoned + `,"zone":"z2"}`,
		"bad zone token":             `{` + strings.Replace(baseRecord, "agents.mail.cc.o.a", "agents.mail.cc.o.Z.a", 1) + `,"zone":"Z"}`,
	} {
		if _, err := ParseRecord("a", []byte(v)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestInboxConfigIsOneSubject(t *testing.T) {
	c := InboxConfigFor("o", "a", DefaultLimits())
	if !reflect.DeepEqual(c.Subjects, []string{"agents.mail.*.o.a"}) {
		t.Fatalf("%v", c.Subjects)
	}
}

func TestValidLabelsBoundary(t *testing.T) {
	mk := func(n int) map[string]string {
		m := map[string]string{}
		for i := 0; i < n; i++ {
			m[fmt.Sprintf("k%d", i)] = "v"
		}
		return m
	}
	if err := ValidLabels(mk(MaxLabels)); err != nil {
		t.Fatalf("%d labels: %v", MaxLabels, err)
	}
	if err := ValidLabels(mk(65)); err == nil || err.Error() != "mash: 65 labels; at most 64" {
		t.Fatalf("65 labels: %v", err)
	}
}

// The cap is enforced on write only: a stored record with more labels still parses (ADR 0072).
func TestParseRecordAcceptsManyLabels(t *testing.T) {
	var p []string
	for i := 0; i < 20; i++ {
		p = append(p, fmt.Sprintf(`"k%d":"v"`, i))
	}
	r, err := ParseRecord("a", []byte(`{`+baseRecord+`,"labels":{`+strings.Join(p, ",")+`}}`))
	if err != nil || len(r.Labels) != 20 {
		t.Fatalf("%d labels, %v", len(r.Labels), err)
	}
}
