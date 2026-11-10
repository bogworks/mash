package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func aliasRec(agent, owner, name, aowner, aname string) Record {
	r := newRec(agent, owner, name)
	r.Alias = &Alias{Owner: aowner, Name: aname}
	return r
}

func TestParseRecordAlias(t *testing.T) {
	good := aliasRec("acme", "team1", "ops1", "legowner", "ops1")
	b := recJSON(t, good)
	got, err := ParseRecord(good.Key(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Alias == nil || *got.Alias != *good.Alias {
		t.Fatalf("alias lost: %+v", got.Alias)
	}
	id, ok, err := got.AliasIdentity()
	if err != nil || !ok || !id.IsLegacy() || id.Owner != "legowner" || id.Name != "ops1" {
		t.Fatalf("AliasIdentity = %v %v %v", id, ok, err)
	}
	if own, err := got.Identity(); err != nil || own.IsLegacy() || own.Flat() != "acme_team1_ops1" {
		t.Fatalf("Identity changed: %v %v", own, err)
	}
	// round trip is stable
	if b2 := recJSON(t, got); !bytes.Equal(b, b2) {
		t.Fatalf("round trip differs:\n%s\n%s", b, b2)
	}
	// no alias: not reported, no error
	if _, ok, err := newRec("acme", "team1", "ops1").AliasIdentity(); ok || err != nil {
		t.Fatalf("no alias: ok=%v err=%v", ok, err)
	}

	bad := []struct {
		name string
		rec  Record
		want string
	}{
		{"legacy record with alias", func() Record { r := legacyRec("legowner", "", "ops1"); r.Alias = &Alias{"o2", "n2"}; return r }(), "a legacy record cannot carry an alias"},
		{"alias owner equals own owner", aliasRec("acme", "team1", "ops1", "team1", "ops1"), "equals the record's own owner"},
		{"alias owner not legacy", aliasRec("acme", "team1", "ops1", "Bad", "ops1"), "invalid alias"},
		{"alias name not legacy", aliasRec("acme", "team1", "ops1", "legowner", "bad_name"), "invalid alias"},
		{"alias empty", aliasRec("acme", "team1", "ops1", "", ""), "invalid alias"},
	}
	for _, c := range bad {
		key := c.rec.Name
		if c.rec.AgentToken != "cc" {
			key = c.rec.AgentToken + "_" + c.rec.Owner + "_" + c.rec.Name
		}
		if _, err := ParseRecord(key, recJSON(t, c.rec)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestAliasEmptyObjectIsError(t *testing.T) {
	r := newRec("acme", "team1", "ops1")
	var m map[string]any
	_ = json.Unmarshal(recJSON(t, r), &m)
	m["alias"] = map[string]any{}
	b, _ := json.Marshal(m)
	if _, err := ParseRecord("acme_team1_ops1", b); err == nil {
		t.Fatal("empty alias object accepted")
	}
	m["alias"] = nil
	b, _ = json.Marshal(m)
	if _, err := ParseRecord("acme_team1_ops1", b); err != nil {
		t.Fatalf("null alias: %v", err)
	}
}

func TestLegacyRecordBytesUnchangedByAlias(t *testing.T) {
	b := recJSON(t, legacyRec("acme", "", "ops1"))
	if bytes.Contains(b, []byte("alias")) {
		t.Fatalf("legacy record marshals an alias: %s", b)
	}
	b = recJSON(t, newRec("acme", "team1", "ops1"))
	if bytes.Contains(b, []byte("alias")) {
		t.Fatalf("record without alias marshals one: %s", b)
	}
}

func TestOldReaderIgnoresAlias(t *testing.T) {
	r := aliasRec("acme", "team1", "ops1", "legowner", "ops1")
	// The old reader cannot read a new-form record at all; the alias must not change what it makes of one it could.
	// Check the field in isolation on a legacy-shaped value: the unknown key is ignored.
	var m map[string]any
	_ = json.Unmarshal(recJSON(t, legacyRec("legowner", "", "ops1")), &m)
	m["alias"] = map[string]any{"owner": r.Alias.Owner, "name": r.Alias.Name}
	b, _ := json.Marshal(m)
	got, err := legacyOnlyParse("ops1", b)
	if err != nil || got.Name != "ops1" {
		t.Fatalf("old reader: %v", err)
	}
}

func TestAdmitWithAliasMatrix(t *testing.T) {
	leg := legacyRec("legowner", "", "ops1")
	zoned := legacyRec("legowner", "z", "ops1")
	id := mustNew(t, "acme", "team1", "ops1")
	al := mustLegacy(t, "legowner", "ops1")
	cases := []struct {
		name     string
		id       sap.EndpointIdentity
		alias    *sap.EndpointIdentity
		zone     string
		existing []Record
		want     string
	}{
		{"alias ok", id, &al, "", []Record{leg}, ""},
		{"alias ok, re-admit itself", id, &al, "", []Record{leg, aliasRec("acme", "team1", "ops1", "legowner", "ops1")}, ""},
		{"alias names no legacy record", id, &al, "", nil, "the alias names no legacy endpoint"},
		{"alias names other owner's legacy record", id, &al, "", []Record{legacyRec("other", "", "ops1")}, "the alias names no legacy endpoint"},
		{"alias names other name", id, &al, "", []Record{legacyRec("legowner", "", "ops2")}, "the alias names no legacy endpoint"},
		{"alias names a zoned legacy record", id, &al, "", []Record{zoned}, "zoned legacy endpoint present"},
		{"two records claim one alias", mustNew(t, "acme", "team2", "ops2"), &al, "", []Record{leg, aliasRec("acme", "team1", "ops1", "legowner", "ops1")}, "already claimed"},
		{"second alias for other legacy name", mustNew(t, "acme", "team2", "ops2"), func() *sap.EndpointIdentity { a := mustLegacy(t, "legowner", "ops2"); return &a }(), "", []Record{leg, legacyRec("legowner", "", "ops2"), aliasRec("acme", "team1", "ops1", "legowner", "ops1")}, ""},
		{"alias owner equals id owner", id, func() *sap.EndpointIdentity { a := mustLegacy(t, "team1", "ops1"); return &a }(), "", []Record{legacyRec("team1", "", "ops1")}, "alias owner equals"},
		{"alias is not legacy", id, &id, "", []Record{leg}, "not a legacy identity"},
		{"legacy id cannot carry alias", al, &al, "", []Record{leg}, "cannot carry an alias"},
		{"alias overlaps a new-form record", id, func() *sap.EndpointIdentity { a := mustLegacy(t, "team3", "ops9"); return &a }(), "", []Record{legacyRec("team3", "", "ops9"), newRec("beta", "team3", "ops9")}, "overlap"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AdmitWithAlias(c.id, c.alias, c.zone, c.existing)
			if c.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestAdmitAgainstExistingAlias(t *testing.T) {
	holder := aliasRec("acme", "team1", "ops1", "legowner", "ops1")
	named := legacyRec("legowner", "", "ops1")
	// the legacy endpoint the alias names is allowed (it is already there, or is being re-admitted)
	if err := Admit(mustLegacy(t, "legowner", "ops1"), "", []Record{holder, named}); err != nil {
		t.Fatalf("named legacy endpoint: %v", err)
	}
	if err := Admit(mustLegacy(t, "legowner", "ops1"), "", []Record{holder}); err != nil {
		t.Fatalf("named legacy endpoint, not yet present: %v", err)
	}
	// a legacy endpoint that is not the named one but shares its subjects is refused: same owner and name is the
	// named one, so build an overlap through a new-form identity instead.
	if err := Admit(mustNew(t, "beta", "legowner", "ops1"), "", []Record{holder, named}); err == nil {
		t.Fatal("new-form identity overlapping an alias admitted")
	}
	// a new-form identity with the alias's owner is refused by the owner rule and by the alias overlap
	err := Admit(mustNew(t, "beta", "legowner", "ops1"), "", []Record{holder})
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("err = %v, want the alias overlap named", err)
	}
	// an unrelated identity is unaffected
	if err := Admit(mustNew(t, "beta", "team9", "ops1"), "", []Record{holder, named}); err != nil {
		t.Fatalf("unrelated: %v", err)
	}
	if err := Admit(mustLegacy(t, "legowner", "ops2"), "", []Record{holder, named}); err != nil {
		t.Fatalf("other legacy name: %v", err)
	}
	// an existing record whose alias is not a valid legacy identity fails closed
	badAlias := aliasRec("acme", "team1", "ops1", "Bad", "x")
	if err := Admit(mustNew(t, "beta", "team9", "ops1"), "", []Record{badAlias}); err == nil || !strings.Contains(err.Error(), "unmodellable alias") {
		t.Fatalf("err = %v", err)
	}
}

func TestAdmitWithNilAliasIsAdmit(t *testing.T) {
	ex := []Record{legacyRec("team1", "", "ops1")}
	id := mustNew(t, "acme", "team1", "ops1")
	a, b := Admit(id, "", ex), AdmitWithAlias(id, nil, "", ex)
	if a == nil || b == nil || a.Error() != b.Error() {
		t.Fatalf("Admit %v, AdmitWithAlias %v", a, b)
	}
}
