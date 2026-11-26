package endpoint

// AT-022 (REQ-021): the owner boundary and forgery, on the two-owner server of newS5Fix (owner acme with alice, bob and
// carol; owner zeta with mallory; every JWT is exactly admin.Permissions, checked when the fixture is built). Run on
// the S5 constructor under a test driver; the repeat on W's binary is the switch-on gate's. Each observation is its
// own test. Item 4 is recorded and never asserted.

import (
	"strings"
	"testing"
	"time"
)

// TestS5AT022_Fixture runs now: the fixture is what AT-022 describes, and the authorised flow works on main's code.
// Both ways, alice and bob mail each other, find each other and prompt each other with today's client code.
func TestS5AT022_FixtureAndTodaysFlow(t *testing.T) {
	z := newS5Fix(t)
	_, aliceEv := z.serveEcho(t, z.alice, nil)
	_, bobEv := z.serveEcho(t, z.bob, nil)
	type pair struct {
		from, to string
	}
	for _, p := range []pair{{"alice", "bob"}, {"bob", "alice"}} {
		from, to := z.alice, z.bob
		toEv := bobEv
		if p.from == "bob" {
			from, to, toEv = z.bob, z.alice, aliceEv
		}
		client := z.clientAt(t, from)
		body := "hello from " + p.from
		if _, err := client.Send(s5Ctx(t), to.String(), body, 0); err != nil {
			t.Fatalf("%s mails %s: %v", p.from, p.to, err)
		}
		s5Wait(t, p.from+"'s mail to "+p.to, 15*time.Second, func() bool {
			for _, e := range toEv.all() {
				if strings.Contains(e, body) {
					return true
				}
			}
			return false
		})
		c := z.connect(t, from)
		subj, err := Discover(s5Ctx(t), c.nc, to)
		if err != nil || subj != mustSubj(to).Prompt {
			t.Fatalf("%s finds %s: %q, %v", p.from, p.to, subj, err)
		}
		var out strings.Builder
		if err := Prompt(s5Ctx(t), c.nc, identity(t, z.creds[from.Flat()]), from.Flat(), subj, "ping", &out); err != nil || out.String() != "pong from "+to.Instance {
			t.Fatalf("%s prompts %s: %q, %v", p.from, p.to, out.String(), err)
		}
		if v := c.violations(); len(v) != 0 {
			t.Fatalf("%s's connection was refused: %q", p.from, v)
		}
	}
}

// Item 1: the owner boundary, against captured subjects. Alice's caller runs a discovery and a prompt to bob (the
// prompt held in flight so the live reply subject can be attacked) and the test captures every reply subject the
// caller used. Mallory, with her own grants, subscribes to $SRV.> (allowed), then tries to subscribe to and to publish
// on each captured subject: the connection reports a permissions violation naming the subject for every attempt,
// and alice receives nothing from mallory. Carol's subscribe to each captured subject is refused the same way.
func TestS5AT022_Item1_OwnerBoundaryOnCapturedSubjects(t *testing.T) {
	needS5Caller(t, "AT-022 item 1 (owner boundary on captured subjects)")
	z := newS5Fix(t)
	release := make(chan struct{})
	z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
		<-release
		_ = ep.Reply(p.RequestID, "genuine", true)
	})
	tp := z.tap(t, "$SRV.INFO.>", "agents.prompt.>", "_INBOX.>")
	cl, aliceConn := z.caller(t, z.alice, s5Inputs{})

	type result struct {
		chunks []string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
		done <- result{chunks, err}
	}()
	var captured []string
	s5Wait(t, "alice's discovery and prompt requests", 20*time.Second, func() bool {
		d, p := tp.requestsFrom(z.alice)
		if len(d) >= 1 && len(p) >= 1 {
			captured = tp.replySubjectsFrom(z.alice)
			return true
		}
		return false
	})
	// also what the caller says it used
	for _, s := range cl.ReplySubjects() {
		found := false
		for _, c := range captured {
			found = found || c == s
		}
		if !found {
			captured = append(captured, s)
		}
	}

	mallory, carol := z.connect(t, z.mallory), z.connect(t, z.carol)
	if _, err := mallory.nc.SubscribeSync("$SRV.>"); err != nil {
		t.Fatal(err)
	}
	mallory.settle(t)
	if v := mallory.violations(); len(v) != 0 {
		t.Fatalf("mallory's $SRV.> subscription was refused: %q", v)
	}
	for _, s := range captured {
		if _, err := mallory.nc.SubscribeSync(s); err != nil {
			t.Fatal(err)
		}
		if !mallory.violated("subscription", s, 5*time.Second) {
			t.Errorf("mallory's subscribe to %s was not refused", s)
		}
		_ = mallory.nc.Publish(s, s5Evil)
		_ = mallory.nc.Publish(s, nil)
		if !mallory.violated("publish", s, 5*time.Second) {
			t.Errorf("mallory's publish to %s was not refused", s)
		}
		if _, err := carol.nc.SubscribeSync(s); err != nil {
			t.Fatal(err)
		}
		if !carol.violated("subscription", s, 5*time.Second) {
			t.Errorf("carol's subscribe to %s was not refused", s)
		}
	}

	close(release)
	select {
	case r := <-done:
		if r.err != nil || s5Join(r.chunks) != "genuine" {
			t.Fatalf("alice's prompt: %q, %v (mallory's attempts must not change it)", r.chunks, r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("alice's prompt did not finish")
	}
	if s5Contains(tp.all(), "EVIL") {
		t.Fatal("a message from mallory reached the server's subscribers")
	}
	if v := aliceConn.violations(); len(v) != 0 {
		t.Fatalf("alice's own connection was refused: %q", v)
	}
}

// Item 2: registration forgery. Carol publishes, on a discovery reply subject she observed, an unsigned
// registration, one with a forged id_sig, and one validly signed by her own key for bob's address. The caller rejects
// each, counts it, and does not attribute it to bob. AT-015's race is run here: carol's invalid answer ahead of the
// valid one, which the caller survives.
func TestS5AT022_Item2_RegistrationForgery(t *testing.T) {
	needS5Caller(t, "AT-022 item 2 (registration forgery by a same-owner peer)")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	var three []s5Answer
	for _, f := range z.forgeries(t, genuine) {
		if f.Name == "unsigned" || f.Name == "forged-sig" || f.Name == "wrong-key" {
			three = append(three, f)
		}
	}
	for _, f := range three {
		t.Run("only/"+f.Name, func(t *testing.T) {
			z.race(t, s5None, []s5Answer{f}, nil)
			cl, _ := z.caller(t, z.alice, s5Inputs{})
			got, err := cl.Discover(s5Ctx(t), z.bob)
			if err != nil || len(got.Accepted) != 0 || got.NotVerified != 1 || cl.NotVerified() != 1 {
				t.Fatalf("%s: %+v, %v, total %d (want nothing accepted, one counted)", f.Name, got, err, cl.NotVerified())
			}
		})
		t.Run("ahead-of-valid/"+f.Name, func(t *testing.T) {
			z.race(t, s5Before, []s5Answer{f}, genuine)
			cl, _ := z.caller(t, z.alice, s5Inputs{})
			got, err := cl.Discover(s5Ctx(t), z.bob)
			if err != nil || len(got.Accepted) != 1 || got.Accepted[0].Address != z.bob || got.NotVerified != 1 {
				t.Fatalf("%s ahead of the valid answer: %+v, %v (want bob found, the forgery counted once)", f.Name, got, err)
			}
		})
	}
	t.Run("all-three-ahead-of-valid", func(t *testing.T) {
		z.race(t, s5Before, three, genuine)
		cl, _ := z.caller(t, z.alice, s5Inputs{})
		got, err := cl.Discover(s5Ctx(t), z.bob)
		if err != nil || len(got.Accepted) != 1 || got.NotVerified != len(three) {
			t.Fatalf("%+v, %v (want bob found, %d counted)", got, err, len(three))
		}
	})
}

// Item 3: guessed-subject forgery: AT-016's observer cases, run on this fixture.
func TestS5AT022_Item3_GuessedSubjectForgery(t *testing.T) {
	needS5Caller(t, "AT-022 item 3 (guessed-subject forgery)")
	s5ObserverCases(t)
}

// Item 4 is RECORDED and not asserted (REQ-021): carol is given a live prompt reply subject by the test (her grants
// do not let her see one) and publishes a reply chunk on it. The result is logged. The comparison with the last 0.5.x
// release belongs to W's run.
func TestS5AT022_Item4_SameOwnerReplyRaceRecorded(t *testing.T) {
	needS5Caller(t, "AT-022 item 4 (same-owner reply race, recorded not asserted)")
	z := newS5Fix(t)
	release := make(chan struct{})
	z.serveEcho(t, z.bob, func(ep *Endpoint, p PromptEvent) {
		<-release
		_ = ep.Reply(p.RequestID, "genuine", true)
	})
	tp := z.tap(t, "agents.prompt.>")
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	type result struct {
		chunks []string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
		done <- result{chunks, err}
	}()
	var live string
	s5Wait(t, "alice's prompt request", 20*time.Second, func() bool {
		if _, p := tp.requestsFrom(z.alice); len(p) > 0 {
			live = p[0].Reply
			return true
		}
		return false
	})
	carol := z.connect(t, z.carol)
	_ = carol.nc.Publish(live, []byte(`{"type":"response","data":"CAROL"}`))
	_ = carol.nc.Flush()
	time.Sleep(300 * time.Millisecond)
	close(release)
	select {
	case r := <-done:
		t.Logf("AT-022 item 4 RECORDED (S5 constructor): carol's chunk on the live reply subject %s; prompt result chunks=%q err=%v; carol's violations=%q",
			live, r.chunks, r.err, carol.violations())
	case <-time.After(30 * time.Second):
		t.Logf("AT-022 item 4 RECORDED (S5 constructor): prompt did not finish within 30 s after carol's chunk on %s", live)
	}
}

// Item 5: the authorised flow. Alice and bob mail, find and prompt each other, both ways, on the same server; the
// asker is the constructor's caller, mail goes through main's client code (the send path is W's).
func TestS5AT022_Item5_AuthorisedFlow(t *testing.T) {
	needS5Caller(t, "AT-022 item 5 (authorised flow, both ways)")
	z := newS5Fix(t)
	_, aliceEv := z.serveEcho(t, z.alice, nil)
	_, bobEv := z.serveEcho(t, z.bob, nil)
	for _, d := range []struct {
		from, to string
	}{{"alice", "bob"}, {"bob", "alice"}} {
		from, to, toEv := z.alice, z.bob, bobEv
		if d.from == "bob" {
			from, to, toEv = z.bob, z.alice, aliceEv
		}
		t.Run(d.from+"->"+d.to, func(t *testing.T) {
			body := "hello from " + d.from
			if _, err := z.clientAt(t, from).Send(s5Ctx(t), to.String(), body, 0); err != nil {
				t.Fatalf("mail: %v", err)
			}
			s5Wait(t, "the mail", 15*time.Second, func() bool {
				for _, e := range toEv.all() {
					if strings.Contains(e, body) {
						return true
					}
				}
				return false
			})
			cl, c := z.caller(t, from, s5Inputs{})
			got, err := cl.Discover(s5Ctx(t), to)
			if err != nil || len(got.Accepted) != 1 || got.Accepted[0].Address != to || got.NotVerified != 0 {
				t.Fatalf("find: %+v, %v", got, err)
			}
			chunks, err := cl.Prompt(s5Ctx(t), to, "ping")
			if err != nil || s5Join(chunks) != "pong from "+to.Instance {
				t.Fatalf("prompt: %q, %v", chunks, err)
			}
			if v := c.violations(); len(v) != 0 {
				t.Fatalf("the caller's connection was refused by the server's permissions: %q", v)
			}
		})
	}
}
