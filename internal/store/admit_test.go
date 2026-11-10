package store

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func mustNew(t *testing.T, a, o, n string) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.New(a, o, n)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustLegacy(t *testing.T, o, n string) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.Legacy(o, n)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdmitMatrix(t *testing.T) {
	// unmodellable: accepted as a legacy record by the legacy checks, but its owner is not a valid legacy token.
	odd := legacyRec("Bad_Owner", "", "oddname")
	if _, err := odd.Identity(); err == nil {
		t.Fatal("test record should be unmodellable")
	}
	cases := []struct {
		name     string
		id       sap.EndpointIdentity
		zone     string
		existing []Record
		want     string // "" admitted, else substring of the error
	}{
		{"new-form vs legacy same owner+name", mustNew(t, "acme", "team1", "ops1"), "", []Record{legacyRec("team1", "", "ops1")}, "overlap"},
		{"new-form owner equals legacy owner, other name", mustNew(t, "acme", "team1", "ops1"), "", []Record{legacyRec("team1", "", "other")}, "owner equals a legacy owner"},
		{"new-form disjoint owner", mustNew(t, "acme", "team1", "ops1"), "", []Record{legacyRec("legowner", "", "ops1")}, ""},
		{"legacy vs existing new-form same owner+name", mustLegacy(t, "team1", "ops1"), "", []Record{newRec("acme", "team1", "ops1")}, "overlap"},
		{"zoned legacy present refuses new-form", mustNew(t, "acme", "team1", "ops1"), "", []Record{legacyRec("legowner", "z", "zed")}, "zoned legacy endpoint present"},
		{"zoned legacy admission with new-form present", mustLegacy(t, "legowner", "zed"), "z", []Record{newRec("acme", "team1", "ops1")}, "zoned legacy endpoint"},
		{"zoned legacy admission with only legacy present", mustLegacy(t, "legowner", "zed"), "z", []Record{legacyRec("legowner", "", "ops1")}, ""},
		{"reverse: legacy owner equals existing new-form owner", mustLegacy(t, "team1", "other"), "", []Record{newRec("acme", "team1", "ops1")}, "owner equals a new-form owner"},
		{"reverse: legacy owner differs from new-form owner", mustLegacy(t, "zzz", "other"), "", []Record{newRec("acme", "team1", "ops1")}, ""},
		{"unmodellable record refuses new-form", mustNew(t, "acme", "team1", "ops1"), "", []Record{odd}, "unmodellable record oddname"},
		{"unmodellable record refuses legacy", mustLegacy(t, "legowner", "ops1"), "", []Record{odd}, "unmodellable record oddname"},
		{"re-admit legacy itself", mustLegacy(t, "legowner", "ops1"), "", []Record{legacyRec("legowner", "", "ops1")}, ""},
		{"re-admit zoned legacy itself", mustLegacy(t, "legowner", "zed"), "z", []Record{legacyRec("legowner", "z", "zed")}, ""},
		{"re-admit new-form itself", mustNew(t, "acme", "team1", "ops1"), "", []Record{newRec("acme", "team1", "ops1")}, ""},
		{"legacy vs legacy, other owner same name", mustLegacy(t, "o2", "ops1"), "", []Record{legacyRec("o1", "", "ops1"), legacyRec("o1", "", "ops2")}, ""},
		{"legacy vs legacy, same owner other name", mustLegacy(t, "o1", "ops3"), "", []Record{legacyRec("o1", "", "ops1")}, ""},
		{"new-form differing in agent", mustNew(t, "beta", "team1", "ops1"), "", []Record{newRec("acme", "team1", "ops1")}, ""},
		{"new-form differing in owner", mustNew(t, "acme", "team2", "ops1"), "", []Record{newRec("acme", "team1", "ops1")}, ""},
		{"new-form differing in name", mustNew(t, "acme", "team1", "ops2"), "", []Record{newRec("acme", "team1", "ops1")}, ""},
		{"no records", mustNew(t, "acme", "team1", "ops1"), "", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Admit(c.id, c.zone, c.existing)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && err == nil:
				t.Fatalf("admitted; want refusal naming %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Fatalf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

func TestAdmitNamesExistingKey(t *testing.T) {
	err := Admit(mustNew(t, "acme", "team1", "ops1"), "", []Record{legacyRec("team1", "", "ops1")})
	if err == nil || !strings.Contains(err.Error(), "ops1") {
		t.Fatalf("error %v", err)
	}
}

func TestAdmitFailsClosedOnZeroIdentity(t *testing.T) {
	if err := Admit(sap.EndpointIdentity{}, "", nil); err == nil {
		t.Fatal("zero identity admitted")
	}
}
