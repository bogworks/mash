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

	out := cliAs(t, sendEnv, "send", "acme.team1.recv", "by the canonical form")
	if !strings.Contains(out, "sent #1 from acme.team1.sender to acme.team1.recv") {
		t.Fatalf("receipt: %q", out)
	}
	out = cliAs(t, sendEnv, "send", "acme_team1_recv", "by the flat key")
	if !strings.Contains(out, "sent #2 from acme.team1.sender to acme.team1.recv") {
		t.Fatalf("flat receipt: %q", out)
	}

	for name, out := range map[string]string{
		"inbox": cliAs(t, recvEnv, "inbox"),
		"read":  cliAs(t, recvEnv, "read", "1"),
	} {
		if !strings.Contains(out, "from acme.team1.sender [verified]") || strings.Contains(out, "acme_team1_sender") {
			t.Fatalf("%s:\n%s", name, out)
		}
	}

	if got := cliAs(t, sendEnv, "who", "--names"); !strings.Contains(got, "acme_team1_recv") || strings.Contains(got, "acme.team1") {
		t.Fatalf("who --names is flat:\n%s", got)
	}
	if got := cliAs(t, sendEnv, "who"); !strings.Contains(got, "acme.team1.recv\t") || strings.Contains(got, "acme_team1") {
		t.Fatalf("who:\n%s", got)
	}
	if got := cliAs(t, sendEnv, "who", "--json"); !strings.Contains(got, `"name": "acme_team1_recv"`) || !strings.Contains(got, `"identity": "acme.team1.recv"`) {
		t.Fatalf("who --json:\n%s", got)
	}
	if got := cliAs(t, sendEnv, "status"); !strings.Contains(got, "name: OK - acme.team1.sender") {
		t.Fatalf("status:\n%s", got)
	}

	// A canonical form that names nothing is refused as an unknown endpoint, in the form it was typed.
	var o, eb strings.Builder
	if code := run([]string{"send", "acme.team1.nobody", "x"}, envFrom(sendEnv), strings.NewReader(""), &o, &eb); code == 0 ||
		!strings.Contains(eb.String(), "no endpoint named") {
		t.Fatalf("unknown: code %d %s", code, eb.String())
	}
}
