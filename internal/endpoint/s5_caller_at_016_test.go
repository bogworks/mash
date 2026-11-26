package endpoint

// AT-016 (REQ-015): reply subjects. Under amendment 5 (i) the layout is main's own, secretInbox
// (internal/endpoint/service.go): the owner-scoped prefix _INBOX.<owner>.<flat>. and one token of 128 random bits from
// crypto/rand, 32 hex characters, fresh for each request (CHG-15's client-id and route tokens fell away). The golden is
// admin.Permissions: the subjects must be inside what the owner's connection may subscribe to, and no other owner may
// subscribe to or publish on them.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	mrand "math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
)

// TestS5AT016_ReplyLayout: every discovery and prompt reply subject main used, captured on the server, has the
// owner-scoped prefix and one token of 32 hex characters (128 bits), and each request has a fresh token.
func TestS5AT016_ReplyLayout(t *testing.T) {
	needS5Caller(t, "AT-016 layout")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>", "agents.prompt.>")
	cl, c := z.caller(t, z.alice, s5Inputs{})
	for i := 0; i < 2; i++ {
		if got, err := cl.Discover(s5Ctx(t), z.bob); err != nil || len(got.Accepted) != 1 {
			t.Fatalf("discovery %d: %+v, %v", i, got, err)
		}
	}
	if chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping"); err != nil || s5Join(chunks) != "pong from bob" {
		t.Fatalf("prompt: %q, %v", chunks, err)
	}
	seen := tp.replySubjectsFrom(z.alice)
	if len(seen) < 4 { // two presence probes, the prompt's discovery, the prompt
		t.Fatalf("captured %d reply subjects, want at least 4: %q", len(seen), seen)
	}
	tokens := map[string]bool{}
	for _, s := range seen {
		tok, problem := s5Layout(z.alice, s)
		if problem != "" {
			t.Fatal(problem)
		}
		if tokens[tok] {
			t.Fatalf("token %q used by two requests", tok)
		}
		tokens[tok] = true
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("the golden refused the caller's own subjects: %q", v)
	}
}

// seedTokens points the reply token's random source at a deterministic stream for the test and restores it after. It
// swaps mash's own seam and not crypto/rand's global source (cryptotest.SetGlobalRandom), which the embedded
// nats-server reads on every accepted connection: that races under -race.
func seedTokens(t *testing.T, seed uint64) {
	t.Helper()
	var key [32]byte
	for i := range 8 {
		key[i] = byte(seed >> (8 * i))
	}
	var r io.Reader = mrand.NewChaCha8(key)
	prev := tokenRand.Swap(&r)
	t.Cleanup(func() { tokenRand.Store(prev) })
}

// TestS5AT016_DefaultTokenSourceIsCryptoRand: with no test seam set, the reply token is drawn from crypto/rand.Reader,
// so the seeded tests prove the token is read through the seam and this proves the seam's default is crypto/rand.
func TestS5AT016_DefaultTokenSourceIsCryptoRand(t *testing.T) {
	if tokenRand.Load() != nil {
		t.Fatal("a token seam is set at the start of the test")
	}
	if tokenSource() != rand.Reader {
		t.Fatal("the default token source is not crypto/rand.Reader")
	}
}

// TestS5AT016_SecretInboxBits: secretInbox (service.go) carries 128 bits (16 bytes, 32 hex characters) after the
// owner-scoped prefix, REQ-015's floor, and the bits are drawn from crypto/rand (the token repeats under one
// seed of mash's token seam and changes with another, which a NUID, math/rand or a clock would not do).
func TestS5AT016_SecretInboxBits(t *testing.T) {
	z := newS5Fix(t)
	nc := z.connect(t, z.alice).nc
	token := func(subject string) string {
		tok, problem := s5Layout(z.alice, subject)
		if problem != "" {
			t.Fatal(problem)
		}
		return tok
	}
	seedTokens(t, 1)
	a1 := token(secretInbox(nc))
	seedTokens(t, 1)
	a2 := token(secretInbox(nc))
	seedTokens(t, 2)
	b1 := token(secretInbox(nc))
	if raw, err := hex.DecodeString(a1); err != nil || len(raw)*8 < 128 {
		t.Fatalf("token %q carries %d bits (%v), want at least 128", a1, len(raw)*8, err)
	}
	if a1 != a2 || a1 == b1 {
		t.Fatalf("tokens %q, %q under seed 1 and %q under seed 2: not drawn from crypto/rand", a1, a2, b1)
	}
	a3, a4 := token(secretInbox(nc)), token(secretInbox(nc))
	if a3 == a4 {
		t.Fatalf("two inboxes in a row have the same token %q", a3)
	}
}

// TestS5AT016_TokenComesFromCryptoRand replaces the entropy source in main's real path: with
// mash's token seam (tokenRand, service.go) fixed to one seed, two callers built on two
// fresh connections emit the same token for their first request; a different seed gives different ones. A token drawn
// from anywhere but crypto/rand (math/rand, a NUID, a clock) would not repeat under the seed, or would not change with it.
func TestS5AT016_TokenComesFromCryptoRand(t *testing.T) {
	needS5Caller(t, "AT-016 entropy source")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>")
	first := func(seed uint64) string {
		env, _ := z.env(t, z.alice, s5Inputs{})
		before := len(tp.replySubjectsFrom(z.alice))
		seedTokens(t, seed)
		cl := newS5Caller(t, env)
		if got, err := cl.Discover(s5Ctx(t), z.bob); err != nil || len(got.Accepted) != 1 {
			t.Fatalf("discovery under seed %d: %+v, %v", seed, got, err)
		}
		_ = cl.Close()
		subs := tp.replySubjectsFrom(z.alice)
		if len(subs) <= before {
			t.Fatalf("no discovery request captured under seed %d", seed)
		}
		tok, problem := s5Layout(z.alice, subs[before])
		if problem != "" {
			t.Fatal(problem)
		}
		return tok
	}
	t1, t2, t3 := first(1), first(1), first(2)
	if t1 != t2 {
		t.Fatalf("the same crypto/rand seed gave different tokens (%s, %s): the token does not come from crypto/rand", t1, t2)
	}
	if t1 == t3 {
		t.Fatalf("a different crypto/rand seed gave the same token (%s, %s): the token does not come from crypto/rand", t1, t3)
	}
}

// s5ObserverCases is AT-016's observer test and AT-022 item 3 (the caller must already be needed by the caller of
// this function). A same-owner observer (carol) sees the reply subject of the requester's discovery on $SRV.>, then
// publishes to the NUID successors and the simple increments of the per-request token, and to random tokens of its
// form, all under the prefix main uses. The requester accepts none.
func s5ObserverCases(t *testing.T) {
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)

	t.Run("discovery", func(t *testing.T) {
		// Bob is offline. Carol replays bob's genuine (valid) registration on every guess, so a guess that hit the
		// requester's reply subject would make bob look online.
		attacks := z.observe(t, genuine)
		cl, _ := z.caller(t, z.alice, s5Inputs{})
		got, err := cl.Discover(s5Ctx(t), z.bob)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Accepted) != 0 {
			t.Fatalf("a guessed reply subject was accepted: %+v", got.Accepted)
		}
		if attacks() == 0 {
			t.Fatal("the observer never saw the discovery request: the test did not attack")
		}
	})

	t.Run("prompt", func(t *testing.T) {
		release := make(chan struct{})
		z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
			<-release
			_ = ep.Reply(p.RequestID, "genuine", true)
		})
		attacks := z.observe(t, nil)
		cl, _ := z.caller(t, z.alice, s5Inputs{})
		go func() { time.Sleep(3500 * time.Millisecond); close(release) }()
		chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
		if err != nil {
			t.Fatalf("the genuine answer did not arrive: %v", err)
		}
		if strings.Contains(s5Join(chunks), "EVIL") {
			t.Fatalf("an observer's guess was accepted as a reply: %q", chunks)
		}
		if s5Join(chunks) != "genuine" {
			t.Fatalf("prompt answered %q, want the genuine answer", chunks)
		}
		if attacks() == 0 {
			t.Fatal("the observer never saw a discovery request: the test did not attack")
		}
	})
}

func TestS5AT016_ObserverGuessesAreNotAccepted(t *testing.T) {
	needS5Caller(t, "AT-016 observer cases")
	s5ObserverCases(t)
}

// TestS5AT016_PermissionsAllowMainsReplyLayout runs against the real server's permissions (admin.Permissions is what
// every JWT carries, checked when the fixture is built). Main's reply subject, the owner-scoped prefix and one token,
// and a deeper one (the grant allows any depth), are inside the owner's grant; another owner can neither subscribe to
// nor publish on them; a same-owner peer cannot subscribe to them but, as REQ-021 records, can publish on them.
func TestS5AT016_PermissionsAllowMainsReplyLayout(t *testing.T) {
	z := newS5Fix(t)
	subjects := map[string]string{
		"main's layout": z.alice.InboxPrefix() + "." + hex.EncodeToString(bytes.Repeat([]byte{0xa5}, 16)),
		"deeper":        z.alice.InboxPrefix() + ".abcdefghijklmnop.abcdefghijklmnopqrstuvwxyz",
	}
	perms := admin.Permissions(z.alice)
	if !strings.Contains(strings.Join(perms.Sub.Allow, " "), z.alice.InboxPrefix()+".>") || !strings.Contains(strings.Join(perms.Pub.Allow, " "), "_INBOX.acme.>") {
		t.Fatalf("admin.Permissions(alice) does not carry the reply grants: %+v", perms)
	}
	alice, carol, mallory := z.connect(t, z.alice), z.connect(t, z.carol), z.connect(t, z.mallory)

	// mallory may subscribe to $SRV.>: that is allowed and is how she sees discovery requests.
	if _, err := mallory.nc.SubscribeSync("$SRV.>"); err != nil {
		t.Fatal(err)
	}
	mallory.settle(t)
	if v := mallory.violations(); len(v) != 0 {
		t.Fatalf("mallory's own $SRV.> subscription was refused: %q", v)
	}

	for name, subj := range subjects {
		t.Run(name, func(t *testing.T) {
			own, err := alice.nc.SubscribeSync(subj)
			if err != nil {
				t.Fatal(err)
			}
			if err := alice.nc.Flush(); err != nil {
				t.Fatal(err)
			}
			// the owner's own connection may subscribe, and a same-owner peer may publish to it
			if err := carol.nc.Publish(subj, []byte("from carol")); err != nil {
				t.Fatal(err)
			}
			if m, err := own.NextMsg(2 * time.Second); err != nil || string(m.Data) != "from carol" {
				t.Fatalf("a same-owner publish on the owner's reply subject did not arrive: %v", err)
			}
			if v := alice.violations(); len(v) != 0 {
				t.Fatalf("alice's own subscription was refused: %q", v)
			}

			// a same-owner peer cannot subscribe
			if _, err := carol.nc.SubscribeSync(subj); err != nil {
				t.Fatal(err)
			}
			if !carol.violated("subscription", subj, 5*time.Second) {
				t.Fatalf("carol's subscribe to alice's reply subject was not refused; errors %q", carol.errs())
			}
			// another owner can neither subscribe nor publish
			if _, err := mallory.nc.SubscribeSync(subj); err != nil {
				t.Fatal(err)
			}
			if !mallory.violated("subscription", subj, 5*time.Second) {
				t.Fatalf("mallory's subscribe was not refused; errors %q", mallory.errs())
			}
			if err := mallory.nc.Publish(subj, []byte("EVIL")); err != nil {
				t.Fatal(err)
			}
			if !mallory.violated("publish", subj, 5*time.Second) {
				t.Fatalf("mallory's publish was not refused; errors %q", mallory.errs())
			}
			if m, err := own.NextMsg(500 * time.Millisecond); err == nil {
				t.Fatalf("alice received %q from mallory", m.Data)
			}
		})
	}
}

// TestS5Today_ReplyInboxIsOneToken records what main sends, the oracle of AT-008 for reply subjects: both the
// discovery and the prompt reply subject are the connection's owner-scoped prefix and one token of 32 hex
// characters (128 random bits from crypto/rand, secretInbox), inside the golden. It runs now.
func TestS5Today_ReplyInboxIsOneToken(t *testing.T) {
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>", "agents.prompt.>")
	c := z.connect(t, z.alice)
	subj, err := Discover(s5Ctx(t), c.nc, z.bob)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Prompt(s5Ctx(t), c.nc, identity(t, z.creds[z.alice.Flat()]), z.alice.Flat(), subj, "ping", &out); err != nil || out.String() != "pong from bob" {
		t.Fatalf("prompt: %q, %v", out.String(), err)
	}
	d, p := tp.waitRequestsFrom(t, z.alice, 1, 1)
	if len(d) != 1 || len(p) != 1 {
		t.Fatalf("captured %d discovery and %d prompt requests, want 1 and 1", len(d), len(p))
	}
	for _, m := range append(d, p...) {
		tok := strings.TrimPrefix(m.Reply, z.alice.InboxPrefix()+".")
		if !s5Hex32.MatchString(tok) {
			t.Fatalf("main's reply subject %q is not the prefix and one 32-hex token", m.Reply)
		}
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("the golden refused main's own subjects: %q", v)
	}
}
