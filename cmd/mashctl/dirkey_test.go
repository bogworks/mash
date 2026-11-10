package main

import (
	"testing"

	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func keyedRecords(t *testing.T) (newForm, legacy store.Record) {
	t.Helper()
	id, err := sap.New("acme", "team1", "x")
	if err != nil {
		t.Fatal(err)
	}
	newForm = store.Record{Name: "x", Owner: "team1", Agent: "claude", AgentToken: "acme", Account: "A", User: "U1",
		MailSubject: id.Subjects().Mail, Created: "2026-01-01T00:00:00Z"}
	legacy = store.Record{Name: "x", Owner: "acme", Agent: "cc", AgentToken: "cc", Account: "A", User: "U2",
		MailSubject: store.MailSubjectFor("acme", "z", "x"), Zone: "z", Created: "2026-01-01T00:00:00Z"}
	return
}

// The policy file's endpoint x is the legacy endpoint x: a new-form record whose name token is x is neither it nor
// evidence that it is provisioned.
func TestDirectoryFindingsNewFormRecordIsNotTheLegacyEndpoint(t *testing.T) {
	nf, lg := keyedRecords(t)
	c := &policy.Compiled{Owner: "acme", Endpoints: []policy.CompiledEndpoint{{Name: "x", Zone: "z"}}}

	out, _ := directoryFindings(c, []store.Record{nf})
	kinds := map[string]string{}
	for _, f := range out {
		kinds[f.Kind] = f.Subject
	}
	if kinds[kindRecordMissing] != "x" || kinds[kindRecordExtra] != nf.Key() || len(out) != 2 {
		t.Fatalf("new-form only: %+v", out)
	}

	for _, recs := range [][]store.Record{{nf, lg}, {lg, nf}} {
		out, _ = directoryFindings(c, recs)
		if len(out) != 1 || out[0].Kind != kindRecordExtra || out[0].Subject != nf.Key() {
			t.Fatalf("both: %+v", out)
		}
	}
}

func TestFindRecordUsesTheDirectoryKey(t *testing.T) {
	nf, lg := keyedRecords(t)
	recs := []store.Record{nf, lg}
	if r, ok := findRecord(recs, "x"); !ok || r.User != "U2" {
		t.Fatalf("x: %+v %v", r, ok)
	}
	if r, ok := findRecord(recs, nf.Key()); !ok || r.User != "U1" {
		t.Fatalf("flat: %+v %v", r, ok)
	}
	if _, ok := findRecord([]store.Record{nf}, "x"); ok {
		t.Fatal("a new-form record answered to the legacy name")
	}
}
