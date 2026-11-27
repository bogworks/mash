package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

// naming21Provision provisions owner.group.instance through Admin.Provision on the permissions the admin layer
// computes, and returns the environment its mash commands run with (its own credentials, never the admin's).
func naming21Provision(t *testing.T, e *testenv.Env, adm *admin.Admin, owner, group, instance string) (sap.Address, map[string]string) {
	t.Helper()
	id, err := sap.NewAddress(owner, group, instance)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := e.MintRaw(t, id.Flat(), *admin.Permissions(id))
	if _, err := adm.Provision(context.Background(), id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return id, map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": id.Owner + "." + id.Group + "." + id.Instance,
		"MASH_SERVER": e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20", "MASH_IDLE_WAKE": "5s",
		"MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION": "1"} // REQ-014: these tests are not about TLS; the override is explicit
}

// shownIdentities returns every identity printed in out in the canonical dotted form owner.group.instance. Ruling
// (a ruling of 2026-11-10): the flat key (owner_group_instance) is a storage encoding (ADR 0074), not a display form; people
// see the canonical form, tokens visibly separate, so the flat key is deliberately NOT recognised here.
func shownIdentities(out string) []sap.Address {
	var ids []sap.Address
	for _, field := range strings.FieldsFunc(out, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ':' || r == '"' || r == '(' || r == ')' || r == '[' || r == ']'
	}) {
		if p := strings.Split(field, "."); len(p) == 3 {
			if id, err := sap.NewAddress(p[0], p[1], p[2]); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// assertShows fails unless out shows want with each of its three tokens byte-identical.
func assertShows(t *testing.T, what, out string, want sap.Address) {
	t.Helper()
	for _, got := range shownIdentities(out) {
		if got == want {
			return
		}
	}
	t.Fatalf("%s does not show %s/%s/%s byte-identical (shown %v):\n%s", what, want.Owner, want.Group, want.Instance, shownIdentities(out), out)
}

// (b) who, inbox, the message header and the trust line show the identity with its three tokens intact and [verified],
// and mail wakes the serving session.
func TestNaming21_TokensShownByteIdentical(t *testing.T) {
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	me, meEnv := naming21Provision(t, e, adm, "acme", "dev", "alice-design")
	peer, peerEnv := naming21Provision(t, e, adm, "acme", "dev", "bob")
	if me.Flat() != "acme_dev_alice-design" {
		t.Fatalf("flat %q", me.Flat())
	}

	ns := startServe(t, meEnv)
	ns.initialize(t)
	ns.tools(t)
	eventually(t, 10*time.Second, "me online", func() bool {
		// Readiness only, display-agnostic: the display assertions below pin the form.
		out := cliAsQuiet(peerEnv, "who")
		return whoShows(out, me.String(), "online")
	})

	// who, from the peer's side: the endpoint, with its tokens.
	who := cliAs(t, peerEnv, "who")
	assertShows(t, "who", who, me)
	assertShows(t, "who", who, peer)
	if !strings.Contains(who, "acme") || !strings.Contains(who, "alice-design") {
		t.Fatalf("who lacks a token:\n%s", who)
	}

	// An installed hook turns the channel event into a one-line wake; an empty spool prints nothing.
	if got := runHook(t, meEnv, "UserPromptSubmit"); got != "" {
		t.Fatalf("empty spool printed %q", got)
	}

	// send prints the recipient; the wake carries the sender.
	sent := cliAs(t, peerEnv, "send", me.String(), "naming21 payload", "--detail") // -v names the recipient
	assertShows(t, "send", sent, me)
	wake := content(ns.await(t, 15*time.Second, isChannel))
	if wake != "1 message from "+peer.String()+" (task)" {
		t.Fatalf("wake %q", wake)
	}
	assertShows(t, "wake", wake, peer)

	// Message header and trust line, as the hook shows them. The header is the one line render.One formats as
	// "--- #<seq> from <sender> [<trust>] at <time>"; it is selected by that format, never by searching the whole output.
	hook := runHook(t, meEnv, "PostToolUse")
	var headers []string
	for _, l := range strings.Split(hook, "\n") {
		if strings.HasPrefix(l, "--- #") && strings.Contains(l, " from ") {
			headers = append(headers, l)
		}
	}
	if len(headers) != 1 {
		t.Fatalf("want exactly one message header line, got %q in:\n%s", headers, hook)
	}
	header := headers[0]
	assertShows(t, "hook header", header, peer)
	if !strings.Contains(hook, "> naming21 payload") {
		t.Fatalf("hook lacks the quoted body:\n%s", hook)
	}
	// The trust line: the label sits directly after the sender on that header line, and says verified.
	trust := regexp.MustCompile(`^--- #\d+ from (\S+) \[([^\]]+)\] at `).FindStringSubmatch(header)
	if trust == nil {
		t.Fatalf("header %q does not match the message header format", header)
	}
	if trust[2] != "verified" {
		t.Fatalf("trust label %q on header %q, want verified", trust[2], header)
	}
	assertShows(t, "trust line sender", trust[1], peer)
	for _, bad := range []string{"[claimed]", "[unknown-signer]", "[absent]", "[stale]", "[forged]"} {
		if strings.Contains(hook, bad) {
			t.Fatalf("hook shows %s:\n%s", bad, hook)
		}
	}

	// inbox: the sender and its trust label.
	inbox := cliAs(t, meEnv, "inbox", "--wide")
	assertShows(t, "inbox", inbox, peer)
	if !strings.Contains(inbox, "verified") {
		t.Fatalf("inbox lacks verified:\n%s", inbox)
	}
	read := cliAs(t, meEnv, "read", "1")
	assertShows(t, "read", read, peer)
	if !strings.Contains(read, "trust:    verified\n") {
		t.Fatalf("read lacks the trust:\n%s", read)
	}
}
