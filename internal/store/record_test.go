package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func testAddr(t *testing.T) sap.Address {
	t.Helper()
	a, err := sap.NewAddress("acme", "ops", "p1")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func testRec(t *testing.T) Record {
	a := testAddr(t)
	return Record{Owner: a.Owner, Group: a.Group, Instance: a.Instance, Agent: "claude-code", Account: "A", User: "U",
		MailSubject: MailSubjectFor(a), Created: "2026-01-01T00:00:00Z"}
}

func TestParseRecordAccepts(t *testing.T) {
	r := testRec(t)
	b, _ := json.Marshal(r)
	got, err := ParseRecord("acme_ops_p1", b)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("%+v %v", got, err)
	}
	a, err := got.Address()
	if err != nil || a != testAddr(t) {
		t.Fatalf("%v %v", a, err)
	}
}

func TestParseRecordRefuses(t *testing.T) {
	good := testRec(t)
	mod := func(f func(*Record)) Record { r := good; f(&r); return r }
	for name, c := range map[string]struct {
		key string
		r   Record
	}{
		"wrong key":       {"acme_ops_p2", good},
		"bare name key":   {"p1", good},
		"wrong subject":   {"acme_ops_p1", mod(func(r *Record) { r.MailSubject = "agents.mail.acme.ops.p2" })},
		"no subject":      {"acme_ops_p1", mod(func(r *Record) { r.MailSubject = "" })},
		"no user":         {"acme_ops_p1", mod(func(r *Record) { r.User = "" })},
		"no group":        {"acme__p1", mod(func(r *Record) { r.Group = "" })},
		"bad owner token": {"Acme_ops_p1", mod(func(r *Record) { r.Owner = "Acme" })},
	} {
		b, _ := json.Marshal(c.r)
		_, err := ParseRecord(c.key, b)
		if err == nil || !strings.Contains(err.Error(), "not an endpoint of this release") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A record of an older release (name and agent_token, no group or instance) is refused.
	old := `{"name":"a","owner":"o","agent":"cc","agent_token":"cc","account":"A","user":"U","mail_subject":"agents.mail.cc.o.a","created":"2026-01-01T00:00:00Z"}`
	if _, err := ParseRecord("a", []byte(old)); err == nil || !strings.Contains(err.Error(), "not an endpoint of this release") {
		t.Fatalf("old record: %v", err)
	}
	if _, err := ParseRecord("a", []byte(`{`)); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

func TestParseRecordMetaAndUnknownKeys(t *testing.T) {
	b, _ := json.Marshal(testRec(t))
	v := strings.TrimSuffix(string(b), "}") + `,"role":"r","host":"h","labels":{"team":"d"},"unknown_future":1}`
	r, err := ParseRecord("acme_ops_p1", []byte(v))
	if err != nil || r.Role != "r" || r.Host != "h" || r.Labels["team"] != "d" || r.User != "U" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestInboxConfigFromAddress(t *testing.T) {
	a := testAddr(t)
	c := InboxConfigFor(a, DefaultLimits())
	if c.Name != "inbox-acme_ops_p1" || !reflect.DeepEqual(c.Subjects, []string{"agents.mail.acme.ops.p1"}) ||
		c.Description != "mash inbox for acme_ops_p1" {
		t.Fatalf("%q %v %q", c.Name, c.Subjects, c.Description)
	}
	if MailSubjectFor(a) != "agents.mail.acme.ops.p1" || SuspendedSubject(a.Flat()) != "mash.suspended.acme_ops_p1" {
		t.Fatal("subjects")
	}
}
