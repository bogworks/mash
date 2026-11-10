package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// keyedPair is a new-form record and a legacy one with the same name token: they are different directory entries.
func keyedPair(t *testing.T) (newForm, legacy store.Record) {
	t.Helper()
	id, err := sap.New("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	newForm = store.Record{Name: "ops1", Owner: "team1", Agent: "claude", AgentToken: "acme", Account: "A", User: "U1",
		MailSubject: id.Subjects().Mail, Created: "2026-01-01T00:00:00Z"}
	legacy = store.Record{Name: "ops1", Owner: "acme", Agent: "cc", AgentToken: "cc", Account: "A", User: "U2",
		MailSubject: store.MailSubjectFor("acme", "", "ops1"), Created: "2026-01-01T00:00:00Z"}
	if newForm.Key() != id.Flat() || legacy.Key() != "ops1" {
		t.Fatalf("keys %q %q", newForm.Key(), legacy.Key())
	}
	return
}

func TestWhoKeepsNewFormAndLegacySameNameApart(t *testing.T) {
	nf, lg := keyedPair(t)
	ps := []endpoint.Presence{{Record: nf}, {Record: lg}}
	if got := whoNames(ps); got != nf.Key()+"\nops1" {
		t.Fatalf("names %q", got)
	}
	live := map[string]endpoint.LeaseInfo{"ops1": {Host: "legacy-host"}}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoJSON(ps, true, live)), &arr); err != nil {
		t.Fatal(err)
	}
	if arr[0]["name"] != nf.Key() || arr[1]["name"] != "ops1" {
		t.Fatalf("json names %v %v", arr[0]["name"], arr[1]["name"])
	}
	if arr[0]["lease"] != nil || arr[1]["lease"] == nil {
		t.Fatalf("the legacy lease was joined to the new-form record: %v", arr)
	}
	lines := strings.Split(whoText(ps, true, live), "\n")
	if !strings.HasPrefix(lines[0], nf.Key()+"\t") || strings.Contains(lines[0], "lease host") ||
		!strings.HasPrefix(lines[1], "ops1\t") || !strings.Contains(lines[1], "lease host legacy-host") {
		t.Fatalf("text:\n%s", strings.Join(lines, "\n"))
	}
}
