package endpoint

import (
	"context"
	"strings"
	"testing"
)

// The recipient error names the full address and says the flat key is accepted too, on both paths that parse a
// recipient: the directory lookup of a send, and service discovery for a prompt. A bare instance is not completed.
func TestRecipientErrorNamesTheAddressForm(t *testing.T) {
	for _, arg := range []string{"a", "team.a", "a.b.c.d", "o_dev", "O.dev.a"} {
		_, err := DiscoverRecipient(context.Background(), nil, arg)
		if err == nil {
			t.Fatalf("%q: no error", arg)
		}
		for _, want := range []string{`"` + arg + `" is not an endpoint address`, "owner.group.instance", "owner_group_instance"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: missing %q in %v", arg, want, err)
			}
		}
		if _, err := parseRecipient(arg); err == nil || !strings.Contains(err.Error(), "owner.group.instance") {
			t.Errorf("parseRecipient(%q): %v", arg, err)
		}
	}
}

func TestParseRecipient(t *testing.T) {
	for _, arg := range []string{"acme.team1.ops1", "acme_team1_ops1"} {
		a, err := parseRecipient(arg)
		if err != nil || a.Flat() != "acme_team1_ops1" {
			t.Errorf("%q: %v %v", arg, a, err)
		}
	}
}

func TestLeaseHeldErrorNamesDotted(t *testing.T) {
	e := &LeaseHeldError{Name: "ag_team_a", Host: "h", PID: "1", Since: "s"}
	if got := e.Error(); !strings.Contains(got, "endpoint ag.team.a is already in use") {
		t.Fatal(got)
	}
}

func TestEndpointDisplay(t *testing.T) {
	f := newFixture(t, "a")
	if got := f.client(t, "a").Display(); got != tdot("a") {
		t.Fatalf("display %q", got)
	}
}
