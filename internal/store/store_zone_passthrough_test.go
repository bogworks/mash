package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// REQ-009 / AT-007: a record's owner and zone are what the record says. ParseRecord checks them and returns them
// unchanged; it derives neither from the name, the mail subject or the other.
func TestParseRecordOwnerZoneUnchanged(t *testing.T) {
	cases := []struct{ owner, zone, name string }{
		{"o", "", "a"},
		{"o", "z", "a"},
		{"a-b-c", "x-y-z", "d-e"},
		{"o1", "z2", "n3"},
		{"same", "same", "same"},
		{"prod-ops", "prod", "ops"},
		{"acme", "default", "a"},
		{"acme", strings.Repeat("z", 32), "a"},
	}
	for _, c := range cases {
		rec := Record{Name: c.name, Owner: c.owner, Agent: "claude-code", AgentToken: "cc", Account: "A", User: "U",
			MailSubject: MailSubjectFor(c.owner, c.zone, c.name), Created: "2026-01-01T00:00:00Z", Zone: c.zone}
		b, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseRecord(c.name, b)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if got.Owner != c.owner || got.Zone != c.zone || got.Name != c.name || got.MailSubject != rec.MailSubject {
			t.Fatalf("%+v: parsed %+v", c, got)
		}
		if c.zone == "" && strings.Count(got.MailSubject, ".") != 4 {
			t.Fatalf("a record with no zone got a zoned subject %q", got.MailSubject)
		}
		if c.zone == "" && strings.Contains(string(b), `"zone"`) {
			t.Fatalf("an empty zone was written out: %s", b)
		}
	}
}

// A record with no zone stays without one: ParseRecord does not fill in a default zone, and a zone that disagrees
// with the mail subject is refused, not corrected.
func TestParseRecordZoneNotInferred(t *testing.T) {
	r, err := ParseRecord("a", []byte(`{`+baseRecord+`}`))
	if err != nil || r.Zone != "" || r.Owner != "o" {
		t.Fatalf("%+v %v", r, err)
	}
	zoned := `"name":"a","owner":"o","agent":"cc","agent_token":"t","account":"A","user":"U","created":"2026-01-01T00:00:00Z"`
	if _, err := ParseRecord("a", []byte(`{`+zoned+`,"zone":"z","mail_subject":"agents.mail.cc.o.a"}`)); err == nil {
		t.Fatal("a zone with the legacy subject was accepted")
	}
	if _, err := ParseRecord("a", []byte(`{`+zoned+`,"mail_subject":"agents.mail.cc.o.z.a"}`)); err == nil {
		t.Fatal("a zoned subject with no zone was accepted: the zone would have to be read from the subject")
	}
	if _, err := ParseRecord("a", []byte(`{`+zoned+`,"zone":"Z","mail_subject":"agents.mail.cc.o.Z.a"}`)); err == nil {
		t.Fatal("an invalid zone was accepted")
	}
}
