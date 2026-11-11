package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/store"
)

// drift names a three-token record in the canonical agent.owner.name form (ADR 0081), in its subject and in the text; a
// command it suggests keeps the directory key, which is what the tools take.
func TestDriftNamesThreeTokenRecordCanonically(t *testing.T) {
	nf, lg := keyedRecords(t)
	c := &policy.Compiled{Owner: "acme", Endpoints: []policy.CompiledEndpoint{{Name: "x", Zone: "z"}}}
	out, _ := directoryFindings(c, []store.Record{nf, lg})
	if len(out) != 1 {
		t.Fatalf("%+v", out)
	}
	f := out[0]
	if f.Subject != nf.Key() || f.Identity != "acme.team1.x" || !strings.Contains(f.Detail, "acme.team1.x looks like an interrupted add") ||
		!strings.Contains(f.Detail, "no endpoint acme.team1.x)") || !strings.Contains(f.Detail, "mashctl retire "+nf.Key()+" --force") {
		t.Fatalf("%+v", f)
	}
	// the text shows the canonical form, the JSON keeps the flat subject and adds identity
	var buf bytes.Buffer
	printState(&buf, "directory", newState(out))
	if !strings.Contains(buf.String(), "record-extra acme.team1.x:") || strings.Contains(buf.String(), nf.Key()+":") {
		t.Fatalf("text:\n%s", buf.String())
	}
	js, err := json.Marshal(newState(out))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"subject":"`+nf.Key()+`"`) || !strings.Contains(string(js), `"identity":"acme.team1.x"`) {
		t.Fatalf("json: %s", js)
	}
	// a legacy record is reported exactly as before: no identity
	out, _ = directoryFindings(&policy.Compiled{Owner: "acme"}, []store.Record{lg})
	if len(out) != 1 || out[0].Subject != "x" || out[0].Identity != "" || !strings.Contains(out[0].Detail, "x looks like an interrupted add") {
		t.Fatalf("%+v", out)
	}
}
