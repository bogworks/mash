package endpoint

import (
	"reflect"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func whoIdentitySet(t *testing.T) []Presence {
	t.Helper()
	nf := func(agent, owner, name string) Presence {
		id, err := sap.New(agent, owner, name)
		if err != nil {
			t.Fatal(err)
		}
		return Presence{Record: store.Record{Name: name, Owner: owner, Agent: "claude", AgentToken: agent, Account: "A", User: "U",
			MailSubject: id.Subjects().Mail, Created: "2026-01-01T00:00:00Z"}}
	}
	lg := Presence{Record: store.Record{Name: "x", Owner: "acme", Agent: "cc", AgentToken: "cc", Account: "A", User: "U2",
		MailSubject: store.MailSubjectFor("acme", "x"), Created: "2026-01-01T00:00:00Z"}}
	return []Presence{nf("acme", "team1", "x"), nf("beta", "team1", "x"), lg}
}

// A dotted agent.owner.name or a flat key given to --name or --match names one three-token endpoint by its full
// identity; anything else filters on the name as it always did (ADR 0081).
func TestWhoFilterAcceptsAThreeTokenIdentity(t *testing.T) {
	ps := whoIdentitySet(t)
	keys := func(ps []Presence) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, p.Record.KeyOrName())
		}
		return out
	}
	for _, c := range []struct {
		name string
		f    WhoFilter
		want []string
	}{
		{"name canonical", WhoFilter{Name: "acme.team1.x"}, []string{"acme_team1_x"}},
		{"name flat", WhoFilter{Name: "acme_team1_x"}, []string{"acme_team1_x"}},
		{"name canonical, case", WhoFilter{Name: "ACME.team1.X"}, []string{"acme_team1_x"}},
		{"match canonical", WhoFilter{Match: "beta.team1.x"}, []string{"beta_team1_x"}},
		{"match flat", WhoFilter{Match: "beta_team1_x"}, []string{"beta_team1_x"}},
		{"name canonical, no such identity", WhoFilter{Name: "acme.team2.x"}, []string{}},
		{"legacy name token unchanged", WhoFilter{Name: "x"}, []string{"acme_team1_x", "beta_team1_x", "x"}},
		{"legacy substring unchanged", WhoFilter{Match: "x"}, []string{"acme_team1_x", "beta_team1_x", "x"}},
		{"not a three-token string stays a substring", WhoFilter{Name: "acme.team1"}, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := keys(c.f.Apply(ps)); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
