package endpoint

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Who lists two records that share an instance token as two entries, in key order, and the presence of one is not the
// other's.
func TestWhoKeepsSameInstanceApart(t *testing.T) {
	a, err := sap.NewAddress("acme", "team1", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := sap.NewAddress("acme", "team2", "ops1")
	if err != nil {
		t.Fatal(err)
	}
	ra := store.Record{Owner: a.Owner, Group: a.Group, Instance: a.Instance, User: "U1", MailSubject: store.MailSubjectFor(a)}
	rb := store.Record{Owner: b.Owner, Group: b.Group, Instance: b.Instance, User: "U2", MailSubject: store.MailSubjectFor(b)}
	e := &Endpoint{dir: map[string]store.Record{a.Flat(): ra, b.Flat(): rb},
		lastSeen: map[string]time.Time{b.Flat(): time.Now()}}
	for i := 0; i < 20; i++ { // map order is random: a sort on the instance alone would tie and flap
		ps := e.Who()
		if len(ps) != 2 || ps[0].Record.User != "U1" || ps[1].Record.User != "U2" {
			t.Fatalf("order %+v", ps)
		}
		if ps[0].Online || !ps[1].Online {
			t.Fatalf("presence crossed: %v %v", ps[0].Online, ps[1].Online)
		}
	}
}
