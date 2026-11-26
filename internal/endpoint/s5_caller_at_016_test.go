package endpoint

// AT-016 (REQ-015): reply subjects. The layout ruled in #3593 (OPEN-05 (a), CHG-15) is
// _INBOX.<owner>.<flat>.<client-id>.<route>, the route token fresh for each request with at least 128 bits from
// crypto/rand (the pinned SDK's randToken, caller/mux.go in v0.1.1, 26 base32 characters, 130 bits). The golden is
// admin.Permissions: the subjects must be inside what the owner's connection may subscribe to, and no other owner may
// subscribe to or publish on them.

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/bogworks/mash/internal/admin"
)

// TestS5AT016_ReplyLayout: every discovery and prompt reply subject the caller used, captured on the server, has the
// prefix and the two tokens; the client-id token is shared by all of the caller's requests; each route token is
// fresh and at least 26 base32 characters; and the caller's own report of its reply subjects is the captured set.
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
	if len(seen) < 4 { // two discoveries, the prompt's discovery, the prompt
		t.Fatalf("captured %d reply subjects, want at least 4: %q", len(seen), seen)
	}
	cid, routes := "", map[string]bool{}
	for _, s := range seen {
		id, route, problem := s5Layout(z.alice, s)
		if problem != "" {
			t.Fatal(problem)
		}
		if cid == "" {
			cid = id
		}
		if id != cid {
			t.Fatalf("client-id token changed between requests: %q then %q", cid, id)
		}
		if routes[route] {
			t.Fatalf("route token %q used by two requests", route)
		}
		routes[route] = true
	}
	// whatever the caller reports of its own use is part of what the server saw; it may report none (the SDK may not
	// expose a reply subject), and the server's view above is the evidence
	reported := cl.ReplySubjects()
	for _, r := range reported {
		found := false
		for _, s := range seen {
			found = found || s == r
		}
		if !found {
			t.Fatalf("the caller reports reply subject %q that the server never saw", r)
		}
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("the golden refused the caller's own subjects: %q", v)
	}
}

// TestS5AT016_RouteTokenComesFromCryptoRand replaces the entropy source in the caller's real path: with
// cryptotest.SetGlobalRandom (which replaces crypto/rand process-wide) fixed to one seed, two callers built on two
// fresh connections emit the same client-id and route token for their first request; a different seed gives different
// ones. Within one run the routes differ request to request. A token drawn from anywhere but crypto/rand (math/rand,
// a NUID, a clock) would not repeat under the seed, or would not change with it. This is the "counter" check of
// AT-016 in the form the SDK allows: v0.1.1 has no entropy-source option, so the replacement is crypto/rand itself.
func TestS5AT016_RouteTokenComesFromCryptoRand(t *testing.T) {
	needS5Caller(t, "AT-016 entropy source")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	tp := z.tap(t, "$SRV.INFO.>")
	first := func(seed uint64) (cid, route string) {
		env, _ := z.env(t, z.alice, s5Inputs{})
		before := len(tp.replySubjectsFrom(z.alice))
		cryptotest.SetGlobalRandom(t, seed) // after every connect: the server draws from crypto/rand when one is accepted
		cl := newS5Caller(t, env)
		if got, err := cl.Discover(s5Ctx(t), z.bob); err != nil || len(got.Accepted) != 1 {
			t.Fatalf("discovery under seed %d: %+v, %v", seed, got, err)
		}
		_ = cl.Close()
		subs := tp.replySubjectsFrom(z.alice)
		if len(subs) <= before {
			t.Fatalf("no discovery request captured under seed %d", seed)
		}
		id, route, problem := s5Layout(z.alice, subs[before])
		if problem != "" {
			t.Fatal(problem)
		}
		return id, route
	}
	c1, r1 := first(1)
	c2, r2 := first(1)
	c3, r3 := first(2)
	if c1 != c2 || r1 != r2 {
		t.Fatalf("the same crypto/rand seed gave different tokens (%s.%s, %s.%s): the tokens do not come from crypto/rand", c1, r1, c2, r2)
	}
	if c1 == c3 || r1 == r3 {
		t.Fatalf("a different crypto/rand seed gave the same token (%s.%s, %s.%s): the tokens do not come from crypto/rand", c1, r1, c3, r3)
	}
}

// s5ObserverCases is AT-016's observer test and AT-022 item 3 (the caller must already be needed by the caller of
// this function). A same-owner observer (carol) sees the reply subject of the requester's discovery on $SRV.>, then
// publishes to the NUID successors and the simple increments of the per-request token, and to the client-id token it
// saw with guessed route tokens (also one token under the flat prefix, main's layout). The requester accepts none.
func s5ObserverCases(t *testing.T) {
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)

	t.Run("discovery", func(t *testing.T) {
		// Bob is offline. Carol replays bob's genuine (valid) registration on every guess, so a guess that hit the
		// requester's route would make bob look online.
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

// TestS5AT016_PermissionsAllowTheNewReplyLayout runs now, against the real server's permissions (admin.Permissions is
// what every JWT carries, checked when the fixture is built). The subjects of the ruled layout, and main's one-token
// layout, are inside the owner's grant; another owner can neither subscribe to nor publish on them; a same-owner peer
// cannot subscribe to them but, as REQ-021 records, can publish on them.
func TestS5AT016_PermissionsAllowTheNewReplyLayout(t *testing.T) {
	z := newS5Fix(t)
	subjects := map[string]string{
		"ruled layout":  z.alice.InboxPrefix() + ".abcdefghijklmnop." + "abcdefghijklmnopqrstuvwxyz",
		"main's layout": z.alice.InboxPrefix() + "." + hex.EncodeToString(bytes.Repeat([]byte{0xa5}, 16)),
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

var s5Hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

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
	if err := Prompt(s5Ctx(t), c.nc, sdkIdentity(t, z.creds[z.alice.Flat()]), z.alice.Flat(), subj, "ping", &out); err != nil || out.String() != "pong from bob" {
		t.Fatalf("prompt: %q, %v", out.String(), err)
	}
	d, p := tp.requestsFrom(z.alice)
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
