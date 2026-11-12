package endpoint

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Who lists a new-form record and a legacy one with the same name token as two entries, in key order, and the
// presence of one is not the other's.
func TestWhoKeepsNewFormAndLegacySameNameApart(t *testing.T) {
	id, err := sap.New("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	nf := store.Record{Name: "ops1", Owner: "team1", AgentToken: "acme", User: "U1", MailSubject: id.Subjects().Mail}
	lg := store.Record{Name: "ops1", Owner: "acme", AgentToken: "cc", User: "U2", MailSubject: store.MailSubjectFor("acme", "ops1")}
	e := &Endpoint{dir: map[string]store.Record{id.Flat(): nf, "ops1": lg},
		lastSeen: map[string]time.Time{"ops1": time.Now()}}
	for i := 0; i < 20; i++ { // map order is random: a sort on Name alone would tie and flap
		ps := e.Who()
		if len(ps) != 2 || ps[0].Record.User != "U1" || ps[1].Record.User != "U2" {
			t.Fatalf("order %+v", ps)
		}
		if ps[0].Online || !ps[1].Online {
			t.Fatalf("presence crossed: %v %v", ps[0].Online, ps[1].Online)
		}
	}
}
