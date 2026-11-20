package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// An endpoint is shown as owner.group.instance on every human surface of the client and the serve (ADR 0145); the
// scripted forms (--names, the json name) stay flat.

var nfCfg = config.Config{Name: "sap-go-sdk", Owner: "x", Group: "grp"}

const nfDot = "x.grp.sap-go-sdk"

func TestToolErrorsShowDotted(t *testing.T) {
	s := &sessionState{failed: errors.New("boom")}
	_, err := s.usable(nfCfg.DisplayName())
	if err == nil || !strings.Contains(err.Error(), `mash endpoint "`+nfDot+`" could not start`) {
		t.Fatalf("%v", err)
	}
}

func TestLiveHolderErrorNamesDotted(t *testing.T) {
	e := &endpoint.LeaseHeldError{Name: "x_grp_sap-go-sdk"}
	if got := e.Error(); !strings.Contains(got, "endpoint "+nfDot+" is already in use") || strings.Contains(got, "x_grp") {
		t.Fatalf("%q", got)
	}
}
