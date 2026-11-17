package endpoint

import (
	"reflect"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func whoIdentitySet(t *testing.T) []Presence {
	t.Helper()
	rec := func(owner, group, instance string) Presence {
		a, err := sap.NewAddress(owner, group, instance)
		if err != nil {
			t.Fatal(err)
		}
		return Presence{Record: store.Record{Owner: owner, Group: group, Instance: instance, Agent: sap.AgentName,
			Account: "A", User: "U", MailSubject: store.MailSubjectFor(a), Created: "2026-01-01T00:00:00Z"}}
	}
	return []Presence{rec("acme", "team1", "x"), rec("beta", "team1", "x"), rec("acme", "team1", "xray")}
}

// A dotted owner.group.instance or a flat key given to --name or --match names one endpoint by its full address;
// anything else filters as a substring of the dotted address.
func TestWhoFilterAcceptsAFullAddress(t *testing.T) {
	ps := whoIdentitySet(t)
	keys := func(ps []Presence) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, flatOf(p.Record))
		}
		return out
	}
	for _, c := range []struct {
		name string
		f    WhoFilter
		want []string
	}{
		{"name dotted", WhoFilter{Name: "acme.team1.x"}, []string{"acme_team1_x"}},
		{"name flat", WhoFilter{Name: "acme_team1_x"}, []string{"acme_team1_x"}},
		{"name dotted, case", WhoFilter{Name: "ACME.team1.X"}, []string{"acme_team1_x"}},
		{"match dotted", WhoFilter{Match: "beta.team1.x"}, []string{"beta_team1_x"}},
		{"match flat", WhoFilter{Match: "beta_team1_x"}, []string{"beta_team1_x"}},
		{"name dotted, no such address", WhoFilter{Name: "acme.team2.x"}, []string{}},
		{"instance token is a substring", WhoFilter{Name: "x"}, []string{"acme_team1_x", "beta_team1_x", "acme_team1_xray"}},
		{"two parts stay a substring", WhoFilter{Name: "acme.team1"}, []string{"acme_team1_x", "acme_team1_xray"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := keys(c.f.Apply(ps)); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
