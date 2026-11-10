package endpoint

import (
	"context"
	"strings"
	"testing"
)

// The recipient error names the canonical form and says the flat key is accepted too (ADR 0081), on both paths that
// parse a recipient: the directory lookup of a send (newform_run_test.go), and service discovery for a prompt.
func TestRecipientErrorNamesTheCanonicalForm(t *testing.T) {
	_, err := DiscoverRecipient(context.Background(), nil, "acme", "ag.team")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{`"ag.team" is not an endpoint key`, "agent.owner.name", "agent_owner_name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestLeaseHeldErrorNamesCanonical(t *testing.T) {
	e := &LeaseHeldError{Name: "ag_team_a", Host: "h", PID: "1", Since: "s"}
	if got := e.Error(); !strings.Contains(got, "endpoint ag.team.a is already in use") {
		t.Fatal(got)
	}
	l := &LeaseHeldError{Name: "ops1"}
	if got := l.Error(); !strings.Contains(got, "endpoint ops1 is already") {
		t.Fatal(got)
	}
}

func TestEndpointDisplay(t *testing.T) {
	f := newFixture(t, "a")
	id := f.addNewForm(t, "ag", "team", "z", newFormOpts{})
	if got := f.clientNF(t, id).Display(); got != "ag.team.z" {
		t.Fatalf("new form %q", got)
	}
	if got := f.client(t, "a").Display(); got != "a" {
		t.Fatalf("legacy %q", got)
	}
}
