package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
)

// addRestricted adds endpoint "s" to w, allowed to mail only the named recipients (admin.Permissions with the
// owner-wide mail line replaced by their subjects).
func addRestricted(t *testing.T, w *world, allowed ...string) {
	t.Helper()
	p := admin.Permissions(w.e.Owner, "s")
	wide := "agents.mail.*." + w.e.Owner + ".*"
	var pub []string
	for _, s := range p.Pub.Allow {
		if s != wide {
			pub = append(pub, s)
			continue
		}
		for _, n := range allowed {
			pub = append(pub, sap.For(w.e.Owner, n).Mail)
		}
	}
	p.Pub.Allow = pub
	c, u := w.e.MintRaw(t, "s", p)
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), "s", u, w.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	w.env["s"] = map[string]string{"HOME": t.TempDir(), "MASH_NAME": "s", "MASH_URL": w.e.URL,
		"MASH_OWNER": w.e.Owner, "MASH_CREDS": c, "MASH_SETTLE_MS": "20"}
}

const refusalBase = "refused by policy: you may not mail denied." +
	" If the recipient is an endpoint of this owner, your credentials may be older than the permissions mash issues now:" +
	" ask the admin to run mashctl reissue --changed."

// refusal is the CLI's text for a send with --id k1; the retry sentence names that key.
var refusal = refusalBase + " If you retry, use --id k1 (the send tool's id): it sends at most once within " + endpoint.DuplicateWindowText() + "."

// The CLI prints a refusal verbatim, fast, exits 1, and says nothing about a message that may have been delivered.
func TestCLISendRefusedByPolicy(t *testing.T) {
	w := newWorld(t, "ok", "denied")
	addRestricted(t, w, "ok")
	start := time.Now()
	out, code := w.cli(t, "s", "send", "denied", "--id", "k1", "hello")
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("refusal took %v", took)
	}
	if code != 1 || out != refusal+"\n" {
		t.Fatalf("exit %d, output %q, want exactly %q", code, out, refusal)
	}
	for _, bad := range []string{"may or may not", "timed out", "nats:"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output mentions %q: %q", bad, out)
		}
	}
	if out, code := w.cli(t, "s", "send", "ok", "hello"); code != 0 || !strings.Contains(out, "sent #1 from s to ok") {
		t.Fatalf("allowed send: %d %q", code, out)
	}
}

// The MCP send tool returns the refusal as one line the model can act on.
func TestServeSendToolRefusedByPolicy(t *testing.T) {
	w := newWorld(t, "ok", "denied")
	addRestricted(t, w, "ok")
	s := startServe(t, w.env["s"])
	s.initialize(t)
	r := s.call(t, 7, "send", `{"to":"denied","text":"hello","id":"k1"}`)
	if r["isError"] != true || strings.TrimSpace(resultText(r)) != refusal {
		t.Fatalf("result %v, want an error with the single line %q", r, refusal)
	}
	r = s.call(t, 8, "send", `{"to":"ok","text":"hello"}`)
	if r["isError"] == true || !strings.Contains(resultText(r), "sent #1 to ok") {
		t.Fatalf("allowed send: %v", r)
	}
}
