package main

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

// The CLI against a live server: a three-token sender and recipient are shown as agent.owner.name (send receipt, who,
// inbox, read), a recipient may be typed canonical or flat, and the scripted forms stay flat (ADR 0081).
func TestCLIShowsAndAcceptsTheCanonicalForm(t *testing.T) {
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, recvEnv := provisionNewForm(t, e, "acme", "team1", "recv")
	sid, sendEnv := provisionNewForm(t, e, "acme", "team1", "sender")
	if sid.Flat() != "acme_team1_sender" {
		t.Fatalf("flat %q", sid.Flat())
	}

	out := cliAs(t, sendEnv, "send", "acme.team1.recv", "by the canonical form", "-v")
	if !strings.Contains(out, "Mail sent (number ") || !strings.Contains(out, "to:      acme.team1.recv\n") || !strings.Contains(out, "kind:    task\n") {
		t.Fatalf("receipt: %q", out)
	}
	out = cliAs(t, sendEnv, "send", "acme.team1.recv", "by the canonical form again", "-v")
	if !strings.Contains(out, "Mail sent (number ") || !strings.Contains(out, "to:      acme.team1.recv\n") || !strings.Contains(out, "kind:    task\n") {
		t.Fatalf("second receipt: %q", out)
	}

	for name, out := range map[string]string{
		"inbox": cliAs(t, recvEnv, "inbox", "--wide"),
		"read":  cliAs(t, recvEnv, "read", "1"),
	} {
		if !strings.Contains(out, "acme.team1.sender") || !strings.Contains(out, "verified") || strings.Contains(out, "acme_team1_sender") {
			t.Fatalf("%s:\n%s", name, out)
		}
	}

	if got := cliAs(t, sendEnv, "who"); !whoShows(got, "acme.team1.recv", "offline") && !whoShows(got, "acme.team1.recv", "online") || strings.Contains(got, "acme_team1") {
		t.Fatalf("who:\n%s", got)
	}
	if got := cliAs(t, sendEnv, "who", "--json"); !strings.Contains(got, `"address":"acme.team1.recv"`) || strings.Contains(got, "acme_team1") {
		t.Fatalf("who --json:\n%s", got)
	}
	if got := cliAs(t, sendEnv, "status"); !strings.HasPrefix(got, "acme.team1.sender: ready, ") {
		t.Fatalf("status:\n%s", got)
	}

	// A canonical form that names nothing is refused as an unknown endpoint, in the form it was typed (exit 1).
	var o, eb strings.Builder
	if code := run([]string{"send", "acme.team1.nobody", "x"}, envFrom(sendEnv), strings.NewReader(""), &o, &eb); code == 0 ||
		!strings.Contains(eb.String(), "no endpoint has the address acme.team1.nobody") {
		t.Fatalf("unknown: code %d %s", code, eb.String())
	}
}
