package endpoint

import (
	"reflect"
	"testing"

	"github.com/bogworks/mash/internal/store"
)

func whoSet() []Presence {
	mk := func(name, owner, role, note string, online bool, labels map[string]string) Presence {
		return Presence{Online: online, Record: store.Record{Name: name, Owner: owner, Role: role, Note: note, Labels: labels}}
	}
	return []Presence{
		mk("alpha", "o1", "NATS Operator", "", true, map[string]string{"team": "Acme", "tier": "1"}),
		mk("beta", "o1", "reviewer", "watches the broker", false, map[string]string{"team": "acme"}),
		mk("gamma", "o2", "", "nats bridge", true, nil),
		mk("x-ops4", "o2", "builder", "", false, map[string]string{"env": "prod"}),
	}
}

func presenceNames(ps []Presence) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Record.Name)
	}
	return out
}

func TestWhoFilterApply(t *testing.T) {
	for _, c := range []struct {
		name string
		f    WhoFilter
		want []string
	}{
		{"none", WhoFilter{}, []string{"alpha", "beta", "gamma", "x-ops4"}},
		{"name substring, case", WhoFilter{Name: "ALP"}, []string{"alpha"}},
		{"owner exact", WhoFilter{Owner: "o2"}, []string{"gamma", "x-ops4"}},
		{"owner not substring", WhoFilter{Owner: "o"}, []string{}},
		{"role case-insensitive", WhoFilter{Role: "nats"}, []string{"alpha"}},
		{"label key", WhoFilter{Labels: []string{"tier"}}, []string{"alpha"}},
		{"label value exact", WhoFilter{Labels: []string{"team=acme"}}, []string{"beta"}},
		{"label value case-sensitive", WhoFilter{Labels: []string{"team=ACME"}}, []string{}},
		{"two labels AND", WhoFilter{Labels: []string{"team", "tier=1"}}, []string{"alpha"}},
		{"match name role note", WhoFilter{Match: "NATS"}, []string{"alpha", "gamma"}},
		{"match note", WhoFilter{Match: "broker"}, []string{"beta"}},
		{"match label key", WhoFilter{Match: "env"}, []string{"x-ops4"}},
		{"match label value", WhoFilter{Match: "prod"}, []string{"x-ops4"}},
		{"online", WhoFilter{Online: true}, []string{"alpha", "gamma"}},
		{"offline", WhoFilter{Offline: true}, []string{"beta", "x-ops4"}},
		{"AND of filters", WhoFilter{Match: "nats", Online: true, Owner: "o2"}, []string{"gamma"}},
		{"no match", WhoFilter{Name: "zzz"}, []string{}},
		{"both online and offline matches nothing", WhoFilter{Online: true, Offline: true}, []string{}},
		{"invalid label matches nothing", WhoFilter{Labels: []string{"=x"}}, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := presenceNames(c.f.Apply(whoSet())); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestWhoFilterValidate(t *testing.T) {
	for _, bad := range []WhoFilter{{Online: true, Offline: true}, {Labels: []string{""}}, {Labels: []string{"=v"}}, {Labels: []string{"k="}}} {
		if bad.Validate() == nil {
			t.Errorf("%+v validated", bad)
		}
	}
	if err := (WhoFilter{Labels: []string{"k", "k=v"}, Online: true}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWhoFilterCapsAndDescription(t *testing.T) {
	ps := []Presence{
		{Online: true, Record: store.Record{Name: "alpha", Description: "Runs the NATS account tooling", Caps: []string{"nats-accounts", "review"}}},
		{Record: store.Record{Name: "beta", Caps: []string{"review"}}},
		{Record: store.Record{Name: "gamma"}},
	}
	for _, c := range []struct {
		name string
		f    WhoFilter
		want []string
	}{
		{"one cap", WhoFilter{Caps: []string{"review"}}, []string{"alpha", "beta"}},
		{"caps AND", WhoFilter{Caps: []string{"review", "nats-accounts"}}, []string{"alpha"}},
		{"cap is exact", WhoFilter{Caps: []string{"nats"}}, []string{}},
		{"cap with online", WhoFilter{Caps: []string{"review"}, Online: true}, []string{"alpha"}},
		{"match description", WhoFilter{Match: "account TOOLING"}, []string{"alpha"}},
		{"match cap", WhoFilter{Match: "REVIE"}, []string{"alpha", "beta"}},
	} {
		if got := presenceNames(c.f.Apply(ps)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if (WhoFilter{Caps: []string{"x"}}).IsZero() {
		t.Error("caps filter is zero")
	}
	if err := (WhoFilter{Caps: []string{"Bad"}}).Validate(); err == nil {
		t.Error("invalid cap accepted")
	}
	if err := (WhoFilter{Caps: []string{"ok.1"}}).Validate(); err != nil {
		t.Error(err)
	}
}
