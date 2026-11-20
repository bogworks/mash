package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/endpoint"
)

// addRestricted adds endpoint "s" to w, allowed to mail only the named recipients (admin.Permissions with the
// owner-wide mail line replaced by their subjects).
func addRestricted(t *testing.T, w *world, allowed ...string) {
	t.Helper()
	p := admin.Permissions(tAddr("s"))
	wide := "agents.mail." + w.e.Owner + ".*.*"
	var pub []string
	for _, s := range p.Pub.Allow {
		if s != wide {
			pub = append(pub, s)
			continue
		}
		for _, n := range allowed {
			pub = append(pub, tAddr(n).Subjects().Mail)
		}
	}
	p.Pub.Allow = pub
	c, u := w.e.MintRaw(t, tAddr("s").Flat(), *p)
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), tAddr("s"), u, w.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	w.env["s"] = map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": w.e.Owner + "." + testGroup + ".s", "MASH_SERVER": w.e.URL,
		"MASH_CREDS": c, "MASH_SETTLE_MS": "20",
		"MASH_INSECURE_PLAINTEXT": "1"} // REQ-014: these tests are not about TLS; the override is explicit
}

const refusalBase = "refused by policy: you may not mail o.dev.denied." +
	" If the recipient is an endpoint of this owner, your credentials may be older than the permissions mash issues now, or your permissions were narrowed:" +
	" ask the admin to check them."

// refusal is the CLI's text for a send with --id k1; the retry sentence names that key.
var refusal = "error: " + refusalBase + " If you retry, use --id k1 (the send tool's id): it sends at most once within " + endpoint.DuplicateWindowText() + "."

// The CLI prints a refusal verbatim, fast, exits 1, and says nothing about a message that may have been delivered.
func TestCLISendRefusedByPolicy(t *testing.T) {
	w := newWorld(t, "ok", "denied")
	addRestricted(t, w, "ok")
	start := time.Now()
	out, code := w.cli(t, "s", "send", tTo("denied"), "--id", "k1", "hello")
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
	if out, code := w.cli(t, "s", "send", tTo("ok"), "hello"); code != 0 || !strings.Contains(out, "Mail sent (number ") {
		t.Fatalf("allowed send: %d %q", code, out)
	}
}

// The MCP send tool returns the refusal as one line the model can act on.
func TestServeSendToolRefusedByPolicy(t *testing.T) {
	w := newWorld(t, "ok", "denied")
	addRestricted(t, w, "ok")
	s := startServe(t, w.env["s"])
	s.initialize(t)
	r := s.call(t, 7, "send", `{"address":"o.dev.denied","message":"hello","id":"k1"}`)
	if r["isError"] != true || strings.TrimSpace(resultText(r)) != refusal {
		t.Fatalf("result %v, want an error with the single line %q", r, refusal)
	}
	r = s.call(t, 8, "send", `{"address":"o.dev.ok","message":"hello"}`)
	if r["isError"] == true || !isReceipt(resultText(r), "Mail sent") {
		t.Fatalf("allowed send: %v", r)
	}
}

// An endpoint with no group (MASH_ENDPOINT is not owner.group.name) is a config error naming MASH_ENDPOINT at every
// entry that runs an endpoint (serve stays up with its status and doctor tools); a hook never fails the session; status reports it.
func TestMalformedEndpointStopsTheSession(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": "nats://127.0.0.1:1", "MASH_ENDPOINT": "o.a", "MASH_CREDS": "/nonexistent"}
	for _, args := range [][]string{{"serve"}, {"send", "o.dev.b", "x"}, {"inbox"}, {"who"}, {"read", "1"}, {"prompt", "o.dev.b", "x"}} {
		var out, errb strings.Builder
		code := run(args, envFrom(env), strings.NewReader(""), &out, &errb)
		// serve, like for any config error, stays up with version, status and doctor only and says why on stderr.
		if (code == 0 && args[0] != "serve") || !strings.Contains(errb.String(), "MASH_ENDPOINT") || !strings.Contains(errb.String(), "three") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out.String(), errb.String())
		}
	}
	var out, errb strings.Builder
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader("{}"), &out, &errb); code != 0 || out.String() != "" {
		t.Errorf("hook: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"status"}, envFrom(env), strings.NewReader(""), &out, &errb); code != 1 ||
		!strings.Contains(out.String(), "not ready: MASH_ENDPOINT: ") {
		t.Errorf("status: exit %d, %q", code, out.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"doctor"}, envFrom(env), strings.NewReader(""), &out, &errb); code != 1 ||
		!strings.Contains(out.String(), "config    FAIL  ") || !strings.Contains(errb.String(), "MASH_ENDPOINT") {
		t.Errorf("doctor: exit %d, %q, %q", code, out.String(), errb.String())
	}
}
