package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// An endpoint is shown as owner.group.instance on every human surface of the client and the serve (ADR 0145); the
// scripted forms (--names, the json name) stay flat.

var nfCfg = config.Config{Name: "sap-go-sdk", Owner: "x", Group: "grp"}

const nfDot = "x.grp.sap-go-sdk"

func TestWhoTextShowsDottedWhoNamesStaysFlat(t *testing.T) {
	r := store.Record{Owner: "acme", Group: "team1", Instance: "ops1"}
	ps := []endpoint.Presence{{Record: r}}
	if got := whoNames(ps); got != "acme_team1_ops1" {
		t.Fatalf("--names is a script contract and stays flat: %q", got)
	}
	text := whoText(ps, false, nil)
	if !strings.HasPrefix(text, "acme.team1.ops1\toffline") || strings.Contains(text, "acme_team1") {
		t.Fatalf("text:\n%s", text)
	}
}

func TestWhoJSONCarriesNameAndIdentity(t *testing.T) {
	r := store.Record{Owner: "acme", Group: "team1", Instance: "ops1"}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoJSON([]endpoint.Presence{{Record: r}}, false, nil)), &arr); err != nil {
		t.Fatal(err)
	}
	if arr[0]["name"] != "acme_team1_ops1" || arr[0]["identity"] != "acme.team1.ops1" {
		t.Fatalf("entry: %v", arr[0])
	}
}

func TestNameLineAndConnectionShowDotted(t *testing.T) {
	if got := nameLine(nfCfg); got != "name: OK - "+nfDot {
		t.Fatalf("nameLine %q", got)
	}
	st := &atomic.Pointer[sessionState]{}
	st.Store(&sessionState{})
	src := &statusSource{cfg: nfCfg, state: st}
	if got := connectionLine(src); got != "connection: STARTING - mash is opening endpoint "+nfDot {
		t.Fatalf("connection %q", got)
	}
	st.Store(&sessionState{waiting: &endpoint.LeaseHeldError{Name: "x_grp_sap-go-sdk", Host: "h", PID: "1", Since: "s"}})
	if got := connectionLine(src); !strings.Contains(got, "the lease for endpoint "+nfDot+" is held") {
		t.Fatalf("waiting %q", got)
	}
}

func TestToolErrorsAndVersionShowDotted(t *testing.T) {
	s := &sessionState{failed: errors.New("boom")}
	_, err := s.usable(nfCfg.DisplayName())
	if err == nil || !strings.Contains(err.Error(), `mash endpoint "`+nfDot+`" could not start`) {
		t.Fatalf("%v", err)
	}
	if got := versionText(nfCfg.DisplayName(), "x", ""); !strings.Contains(got, "endpoint "+nfDot+" (owner x)") {
		t.Fatalf("version %q", got)
	}
}

func TestLiveHolderErrorNamesDotted(t *testing.T) {
	e := &endpoint.LeaseHeldError{Name: "x_grp_sap-go-sdk"}
	if got := e.Error(); !strings.Contains(got, "endpoint "+nfDot+" is already in use") || strings.Contains(got, "x_grp") {
		t.Fatalf("%q", got)
	}
}

func TestNameLineWithoutGroupNamesMASHENDPOINT(t *testing.T) {
	got := nameLine(config.Config{Name: "ops", Owner: "x"})
	if !strings.Contains(got, "name: FAILED") || !strings.Contains(got, config.CodeGroupMissing) || !strings.Contains(got, "MASH_ENDPOINT") {
		t.Fatalf("nameLine %q", got)
	}
}
