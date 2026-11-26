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

var nfCfg = config.Config{Name: "alice", Owner: "acme", Group: "dev"}

const nfDot = "acme.dev.alice"

func TestToolErrorsShowDotted(t *testing.T) {
	s := &sessionState{failed: errors.New("boom")}
	_, err := s.usable(nfCfg.DisplayName())
	if err == nil || !strings.Contains(err.Error(), `mash endpoint "`+nfDot+`" could not start`) {
		t.Fatalf("%v", err)
	}
}

func TestLiveHolderErrorNamesDotted(t *testing.T) {
	e := &endpoint.LeaseHeldError{Name: "acme_dev_alice"}
	if got := e.Error(); !strings.Contains(got, "endpoint "+nfDot+" is already in use") || strings.Contains(got, "acme_dev") {
		t.Fatalf("%q", got)
	}
}
