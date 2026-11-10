package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// A three-token endpoint is shown as agent.owner.name on every human surface of the client and the serve (ADR 0081);
// a legacy endpoint prints exactly what it always did.

var (
	nfCfg  = config.Config{Name: "sap-go-sdk", Owner: "x", AgentToken: "bogworks"}
	legCfg = config.Config{Name: "team-a", Owner: "acme"}
)

const nfDot = "bogworks.x.sap-go-sdk"

func TestWhoTextShowsCanonicalWhoNamesStaysFlat(t *testing.T) {
	nf, lg := keyedPair(t)
	ps := []endpoint.Presence{{Record: nf}, {Record: lg}}
	if got := whoNames(ps); got != nf.Key()+"\nops1" {
		t.Fatalf("--names is a script contract and stays flat: %q", got)
	}
	lines := strings.Split(whoText(ps, false, nil), "\n")
	if !strings.HasPrefix(lines[0], "acme.team1.ops1\toffline") || !strings.HasPrefix(lines[1], "ops1\toffline") {
		t.Fatalf("text:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(lines[0], nf.Key()) {
		t.Fatalf("flat key in the text: %q", lines[0])
	}
}

func TestWhoJSONGainsIdentityForThreeTokenOnly(t *testing.T) {
	nf, lg := keyedPair(t)
	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoJSON([]endpoint.Presence{{Record: nf}, {Record: lg}}, false, nil)), &arr); err != nil {
		t.Fatal(err)
	}
	if arr[0]["name"] != nf.Key() || arr[0]["identity"] != "acme.team1.ops1" {
		t.Fatalf("three-token entry: %v", arr[0])
	}
	if arr[1]["name"] != "ops1" {
		t.Fatalf("legacy entry: %v", arr[1])
	}
	if _, ok := arr[1]["identity"]; ok {
		t.Fatalf("a legacy entry has no identity field: %v", arr[1])
	}
}

func TestNameLineAndConnectionShowCanonical(t *testing.T) {
	if got := nameLine(nfCfg); got != "name: OK - "+nfDot {
		t.Fatalf("nameLine %q", got)
	}
	if got := nameLine(legCfg); got != "name: OK - team-a" {
		t.Fatalf("legacy nameLine %q", got)
	}
	st := &atomic.Pointer[sessionState]{}
	st.Store(&sessionState{})
	src := &statusSource{cfg: nfCfg, state: st}
	if got := connectionLine(src); got != "connection: STARTING - mash is opening endpoint "+nfDot {
		t.Fatalf("connection %q", got)
	}
	st.Store(&sessionState{waiting: &endpoint.LeaseHeldError{Name: "bogworks_x_sap-go-sdk", Host: "h", PID: "1", Since: "s"}})
	if got := connectionLine(src); !strings.Contains(got, "the lease for endpoint "+nfDot+" is held") {
		t.Fatalf("waiting %q", got)
	}
	src.cfg = legCfg
	if got := connectionLine(src); !strings.Contains(got, "the lease for endpoint team-a is held") {
		t.Fatalf("legacy waiting %q", got)
	}
}

func TestToolErrorsAndVersionShowCanonical(t *testing.T) {
	s := &sessionState{failed: errors.New("boom")}
	_, err := s.usable(nfCfg.DisplayName())
	if err == nil || !strings.Contains(err.Error(), `mash endpoint "`+nfDot+`" could not start`) {
		t.Fatalf("%v", err)
	}
	if got := versionText(nfCfg.DisplayName(), "x", ""); !strings.Contains(got, "endpoint "+nfDot+" (owner x)") {
		t.Fatalf("version %q", got)
	}
}

func TestLiveHolderErrorNamesCanonical(t *testing.T) {
	e := &endpoint.LeaseHeldError{Name: "bogworks_x_sap-go-sdk"}
	if got := e.Error(); !strings.Contains(got, "endpoint "+nfDot+" is already in use") || strings.Contains(got, "bogworks_x") {
		t.Fatalf("%q", got)
	}
}
