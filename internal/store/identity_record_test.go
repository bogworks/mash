package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func recJSON(t *testing.T, r Record) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func legacyRec(owner, zone, name string) Record {
	return Record{Name: name, Owner: owner, Agent: "cc", AgentToken: "cc", Account: "A", User: "U",
		MailSubject: MailSubjectFor(owner, zone, name), Zone: zone, Created: "2026-01-01T00:00:00Z"}
}

func newRec(agent, owner, name string) Record {
	id, err := sap.New(agent, owner, name)
	if err != nil {
		panic(err)
	}
	return Record{Name: name, Owner: owner, Agent: "claude", AgentToken: agent, Account: "A", User: "U",
		MailSubject: id.Subjects().Mail, Created: "2026-01-01T00:00:00Z"}
}

// legacyOnlyParse is ParseRecord as it was before new-form records.
func legacyOnlyParse(key string, value []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(value, &r); err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", key, err)
	}
	if r.Name != key || r.User == "" || r.MailSubject == "" {
		return Record{}, fmt.Errorf("directory %s: incomplete record", key)
	}
	if r.Zone != "" && !sap.ValidZone(r.Zone) {
		return Record{}, fmt.Errorf("directory %s: invalid zone %q", key, r.Zone)
	}
	if want := MailSubjectFor(r.Owner, r.Zone, r.Name); r.MailSubject != want {
		return Record{}, fmt.Errorf("directory %s: mail subject %q is not %q", key, r.MailSubject, want)
	}
	return r, nil
}

func TestLegacyRecordsUnchanged(t *testing.T) {
	long := strings.Repeat("a", 48)
	for _, r := range []Record{
		legacyRec("acme", "", "ops1"),
		legacyRec("acme", "east", "ops1"),
		legacyRec(long, "", long),
		{Name: "a", Owner: "o", Agent: "cc", AgentToken: "t", User: "U", MailSubject: "agents.mail.cc.o.a"},
	} {
		b := recJSON(t, r)
		got, err := ParseRecord(r.Name, b)
		if err != nil {
			t.Fatalf("%s: %v", r.Name, err)
		}
		if _, err := legacyOnlyParse(r.Name, b); err != nil {
			t.Fatalf("old logic rejects %s: %v", r.Name, err)
		}
		id, err := got.Identity()
		if err != nil || !id.IsLegacy() || id.Name != r.Name || id.Owner != r.Owner {
			t.Fatalf("%s identity: %+v %v", r.Name, id, err)
		}
		if got.Key() != r.Name {
			t.Fatalf("key %q want %q", got.Key(), r.Name)
		}
	}
}

func TestNewFormRecordsAccepted(t *testing.T) {
	for _, c := range []struct{ agent, owner, name string }{
		{"acme", "team1", "ops1"},
		{"acme", "team1", "sap-go-sdk"},
		{strings.Repeat("a", 32), strings.Repeat("b", 29), "c"},
		{strings.Repeat("a", 1), strings.Repeat("b", 32), strings.Repeat("c", 29)},
		{strings.Repeat("a", 20), strings.Repeat("b", 20), strings.Repeat("c", 22)},
	} {
		r := newRec(c.agent, c.owner, c.name)
		key := c.agent + "_" + c.owner + "_" + c.name
		b := recJSON(t, r)
		got, err := ParseRecord(key, b)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		id, err := got.Identity()
		if err != nil || id.IsLegacy() || id.Flat() != key {
			t.Fatalf("%s identity: %+v %v", key, id, err)
		}
		if got.Key() != key {
			t.Fatalf("key %q want %q", got.Key(), key)
		}
		again, err := ParseRecord(got.Key(), recJSON(t, got))
		if err != nil || again.Name != r.Name || again.MailSubject != r.MailSubject {
			t.Fatalf("round trip %s: %+v %v", key, again, err)
		}
		if _, err := legacyOnlyParse(key, b); err == nil {
			t.Fatalf("old reader accepted new-form %s", key)
		}
	}
	r := newRec("acme", "team1", "ops1")
	if r.MailSubject != "agents.mail.acme.team1.ops1" {
		t.Fatalf("subject %q", r.MailSubject)
	}
	if _, err := ParseRecord("acme_team1_ops1", recJSON(t, r)); err != nil {
		t.Fatal(err)
	}
}

func TestNewFormRecordsRejected(t *testing.T) {
	good := newRec("acme", "team1", "ops1")
	flat := "acme_team1_ops1"
	mod := func(f func(*Record)) Record { r := good; f(&r); return r }
	cases := []struct {
		label, key, want string
		rec              Record
	}{
		{"key is the name", "ops1", "is not \"agents.mail.cc.team1.ops1\"", good},
		{"key wrong tokens", "acme_team2_ops1", "flat identity", good},
		{"subject mismatch", flat, "new-form mail subject", mod(func(r *Record) { r.MailSubject = "agents.mail.acme.team1.other" })},
		{"zone set", flat, "must not have a zone", mod(func(r *Record) { r.Zone = "east" })},
		{"agent uppercase", "Acme_team1_ops1", "new-form identity", mod(func(r *Record) { r.AgentToken = "Acme" })},
		{"agent underscore", "ac_me_team1_ops1", "new-form identity", mod(func(r *Record) { r.AgentToken = "ac_me" })},
		{"token over 32", flat, "new-form identity", mod(func(r *Record) { r.Name = strings.Repeat("n", 33) })},
		{"total over 64", flat, "new-form identity", mod(func(r *Record) {
			r.AgentToken, r.Owner, r.Name = strings.Repeat("a", 32), strings.Repeat("b", 32), "cc"
		})},
	}
	for _, c := range cases {
		_, err := ParseRecord(c.key, recJSON(t, c.rec))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want mention of %q", c.label, err, c.want)
		}
	}
}

func TestCCNewFormRejected(t *testing.T) {
	r := Record{Name: "ops1", Owner: "acme", Agent: "claude", AgentToken: "cc", User: "U", MailSubject: "agents.mail.cc.acme.ops1"}
	got, err := ParseRecord("cc_acme_ops1", recJSON(t, r))
	if err == nil || !strings.Contains(err.Error(), "legacy") || !strings.Contains(err.Error(), `"cc"`) {
		t.Fatalf("err %v, want cc named as the legacy agent token", err)
	}
	if got.Key() != "" {
		t.Fatalf("rejected record has key %q", got.Key())
	}
	if _, err := got.Identity(); err == nil {
		t.Fatal("rejected record has an identity")
	}
}

func TestOddLegacyRecordKeepsKey(t *testing.T) {
	r := Record{Name: "a", Owner: "", Agent: "cc", AgentToken: "cc", User: "U", MailSubject: "agents.mail.cc..a"}
	got, err := ParseRecord("a", recJSON(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if got.Key() != "a" {
		t.Fatalf("key %q", got.Key())
	}
	if _, err := got.Identity(); err == nil {
		t.Fatal("odd legacy record has an identity")
	}
}

func TestNoUnderscoreKeepsLegacyError(t *testing.T) {
	r := Record{Name: "a", Owner: "o", AgentToken: "t", User: "U", MailSubject: "agents.mail.t.o.a"}
	b := recJSON(t, r)
	_, want := legacyOnlyParse("a", b)
	_, got := ParseRecord("a", b)
	if want == nil || got == nil || want.Error() != got.Error() || !strings.Contains(got.Error(), "mail subject") {
		t.Fatalf("old %v, new %v", want, got)
	}
}

func TestStrictlyAdditiveRejections(t *testing.T) {
	cases := map[string][]byte{}
	inc := legacyRec("acme", "", "ops1")
	inc.User = ""
	cases["incomplete"] = recJSON(t, inc)
	bz := legacyRec("acme", "", "ops1")
	bz.Zone = "Bad Zone"
	cases["invalid zone"] = recJSON(t, bz)
	ms := legacyRec("acme", "", "ops1")
	ms.MailSubject = "agents.mail.cc.acme.other"
	cases["mail subject"] = recJSON(t, ms)
	tm := Record{Name: "a", Owner: "o", AgentToken: "t", User: "U", MailSubject: "agents.mail.cc.o.zzz"}
	cases["token t mismatch"] = recJSON(t, tm)
	cases["bad json"] = []byte(`{`)
	for label, b := range cases {
		_, want := legacyOnlyParse(legacyKey(b), b)
		_, got := ParseRecord(legacyKey(b), b)
		if want == nil || got == nil || want.Error() != got.Error() {
			t.Errorf("%s: old %v, new %v", label, want, got)
		}
	}
	// A wrong key is also unchanged.
	b := recJSON(t, legacyRec("acme", "", "ops1"))
	_, want := legacyOnlyParse("other", b)
	_, got := ParseRecord("other", b)
	if want == nil || got == nil || want.Error() != got.Error() {
		t.Errorf("wrong key: old %v, new %v", want, got)
	}
}

func legacyKey(b []byte) string {
	var r Record
	_ = json.Unmarshal(b, &r)
	return r.Name
}

func TestOldReaderCannotReadNewForm(t *testing.T) {
	r := newRec("acme", "team1", "ops1")
	b := recJSON(t, r)
	if _, err := legacyOnlyParse("acme_team1_ops1", b); err == nil {
		t.Fatal("pre-change reader accepted a new-form record")
	}
	if _, err := legacyOnlyParse("ops1", b); err == nil {
		t.Fatal("pre-change reader accepted new-form record under its name")
	}
}
