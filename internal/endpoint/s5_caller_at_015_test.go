package endpoint

// AT-015 (REQ-016): the caller refuses what it cannot verify. Run on main's own discovery through the S5 seam
// (s5_caller_wire_test.go): presence (applyPresence) and the recipient discovery of prompt (discoverSubject), both
// through mash's one verify step. The views built from the count (mash who's warning line and answers_not_verified in
// --json and the MCP who tool) are tested in internal/cmdcore/cmds/who_test.go; nothing is printed here, and the seam
// reports the count and the accepted registrations, which is what those views are built from.

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
	"github.com/nats-io/nats.go"
)

func s5Ctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestS5AT015_Positive: a valid owner-first mash registration (acme.ops.bob) is accepted.
func TestS5AT015_Positive(t *testing.T) {
	needS5Caller(t, "AT-015 positive")
	z := newS5Fix(t)
	z.serveEcho(t, z.bob, nil)
	cl, c := z.caller(t, z.alice, s5Inputs{})
	got, err := cl.Discover(s5Ctx(t), z.bob)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accepted) != 1 || got.Accepted[0].Address != z.bob || got.Accepted[0].PromptSubject != mustSubj(z.bob).Prompt ||
		got.Accepted[0].UserNKey != z.users[z.bob.Flat()] {
		t.Fatalf("accepted %+v, want exactly bob's registration", got.Accepted)
	}
	if got.NotVerified != 0 || cl.NotVerified() != 0 {
		t.Fatalf("a valid registration was counted as not verified: %d, total %d", got.NotVerified, cl.NotVerified())
	}
	if v := c.violations(); len(v) != 0 {
		t.Fatalf("the caller's connection was refused by the server's permissions: %q", v)
	}
}

// s5Negative is what one invalid answer, on its own, did to a discovery.
type s5Negative struct {
	Accepted    []s5Registration // what the discovery attributed to bob
	NotVerified int              // counted by the discovery
	Total       int              // the caller's cumulative count
	Err         error            // the discovery failed
}

// s5RunNegative publishes the invalid answer f, with no valid answer at all (bob is offline), and runs one discovery.
// It reports what happened and decides nothing, so the mutation test of AT-010 reads the same outcome.
func s5RunNegative(t *testing.T, z *s5Fix, f s5Answer) (s5Negative, s5Caller) {
	t.Helper()
	z.race(t, s5None, []s5Answer{f}, nil)
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	got, err := cl.Discover(s5Ctx(t), z.bob)
	return s5Negative{Accepted: got.Accepted, NotVerified: got.NotVerified, Total: cl.NotVerified(), Err: err}, cl
}

// s5CheckNegative is the whole of AT-015's negative case for f: rejected, counted once, never attributed to bob, and
// no unverified retry (a prompt to bob is refused and nothing reaches his prompt subject).
func s5CheckNegative(t *testing.T, z *s5Fix, tp *s5Tap, f s5Answer) {
	t.Helper()
	got, cl := s5RunNegative(t, z, f)
	if got.Err != nil {
		t.Fatalf("an invalid answer failed the discovery: %v", got.Err)
	}
	if len(got.Accepted) != 0 {
		t.Fatalf("a %s answer was attributed to bob: %+v", f.Name, got.Accepted)
	}
	if got.NotVerified != 1 || got.Total != 1 {
		t.Fatalf("%s: counted %d (total %d) as not verified, want 1", f.Name, got.NotVerified, got.Total)
	}
	// Prompt acts as for an endpoint with no answer: an error, and nothing published to bob.
	if _, err := cl.Prompt(s5Ctx(t), z.bob, "ping"); err == nil {
		t.Fatalf("%s: a prompt to an endpoint with no verified answer succeeded", f.Name)
	}
	if p := tp.where(func(m s5Msg) bool { return m.Subject == mustSubj(z.bob).Prompt }); len(p) != 0 {
		t.Fatalf("%s: a prompt reached bob's prompt subject without a verified registration (%d)", f.Name, len(p))
	}
}

// TestS5AT015_NegativesEachRejectedNeverAttributed: each invalid answer on its own, with no valid answer at all (bob
// is offline): rejected, counted once, and bob is not found (he shows as with no answer from the peer). No prompt is
// published (no unverified retry).
func TestS5AT015_NegativesEachRejectedNeverAttributed(t *testing.T) {
	needS5Caller(t, "AT-015 negatives")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	tp := z.tap(t, "agents.prompt.>")
	for _, f := range z.refused(t, genuine) {
		t.Run(f.Name, func(t *testing.T) { s5CheckNegative(t, z, tp, f) })
	}
}

// TestS5AT015_OwnerDifferenceRefused is the owner case of the negative list: a registration validly signed by bob's
// key whose metadata owner differs from the directory record's owner. REQ-016 refuses it, behind the S5 switch.
func TestS5AT015_OwnerDifferenceRefused(t *testing.T) {
	needS5Caller(t, "AT-015 negatives, owner")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	tp := z.tap(t, "agents.prompt.>")
	for _, f := range z.forgeries(t, genuine) {
		if f.Name == "owner" {
			s5CheckNegative(t, z, tp, f)
		}
	}
}

// TestS5AT015_RaceInvalidNeverDeniesTheValidAnswer is SEC-12: a same-owner peer (carol) publishes each invalid answer,
// one at a time and then all together, before the valid one (delayed until hers have been received) and then after
// it. Every run: the valid discovery succeeds with bob found once, and every invalid answer is rejected and counted.
// Then a prompt to bob works.
func TestS5AT015_RaceInvalidNeverDeniesTheValidAnswer(t *testing.T) {
	needS5Caller(t, "AT-015 race (SEC-12)")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	forged := z.refused(t, genuine)
	sets := []struct {
		name string
		set  []s5Answer
	}{}
	for _, f := range forged {
		sets = append(sets, struct {
			name string
			set  []s5Answer
		}{"one-" + f.Name, []s5Answer{f}})
	}
	sets = append(sets, struct {
		name string
		set  []s5Answer
	}{"all-together", forged})
	for _, mode := range []s5RaceMode{s5Before, s5After} {
		for _, s := range sets {
			t.Run(string(mode)+"/"+s.name, func(t *testing.T) {
				z.race(t, mode, s.set, genuine)
				cl, _ := z.caller(t, z.alice, s5Inputs{})
				got, err := cl.Discover(s5Ctx(t), z.bob)
				if err != nil {
					t.Fatalf("invalid answers failed the discovery: %v", err)
				}
				if len(got.Accepted) != 1 || got.Accepted[0].Address != z.bob {
					t.Fatalf("the valid answer did not win: accepted %+v", got.Accepted)
				}
				// Counting (REQ-016, SEC-12): every invalid answer sent is counted exactly once, whether it arrives
				// before or after the valid one, and the valid answer wins.
				if got.NotVerified != len(s.set) {
					t.Fatalf("counted %d answers as not verified, want %d (each once, %s the valid one)", got.NotVerified, len(s.set), mode)
				}
			})
		}
	}
}

// TestS5AT015_PromptWorksThroughTheRace: the same race, then the prompt itself, with bob's real service as the valid
// answerer and the invalid answers published at the same time ("mash prompt to it works").
func TestS5AT015_PromptWorksThroughTheRace(t *testing.T) {
	needS5Caller(t, "AT-015 race, prompt")
	z := newS5Fix(t)
	forged := z.refused(t, z.genuineRegistration(t, z.bob))
	z.serveEcho(t, z.bob, nil)
	z.race(t, s5None, forged, nil) // carol's answers; bob's real service answers for himself
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	chunks, err := cl.Prompt(s5Ctx(t), z.bob, "ping")
	if err != nil {
		t.Fatalf("invalid answers denied the prompt: %v", err)
	}
	if got := s5Join(chunks); got != "pong from bob" {
		t.Fatalf("prompt answered %q", got)
	}
	// The prompt does not wait for the rest of the window once bob has answered; the invalid answers that follow are
	// counted as they arrive, so the count is read when they have.
	// Only the answers for the requested subject are counted (codex M1): the forgery that names another subject is
	// not this discovery's business.
	want, atWant := mustSubj(z.bob).Prompt, 0
	for _, f := range forged {
		if s5PromptSubjectOf(t, f.Data) == want {
			atWant++
		}
	}
	if atWant == 0 || atWant == len(forged) {
		t.Fatalf("%d of %d forged answers name bob's subject: the list must hold one that names another", atWant, len(forged))
	}
	s5Wait(t, "carol's invalid answers to be counted", 10*time.Second, func() bool { return cl.NotVerified() >= atWant })
}

// s5SendToOfflineBobAndAssertStored sends one mail from alice to bob, who is offline (nothing serves him), by main's
// production send path (Endpoint.Send), then reads bob's inbox with his own client and requires exactly one copy:
// from alice, verified, with the body sent.
func s5SendToOfflineBobAndAssertStored(t *testing.T, z *s5Fix, body string) {
	t.Helper()
	ctx := s5Ctx(t)
	seq, err := z.clientAt(t, z.alice).Send(ctx, z.bob.Flat(), body, 0)
	if err != nil {
		t.Fatalf("a send to the offline endpoint was refused: %v", err)
	}
	ms, err := z.clientAt(t, z.bob).Inbox(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("bob's inbox holds %d messages, want exactly the one sent: %+v", len(ms), ms)
	}
	if m := ms[0]; m.Seq != seq || m.From != z.alice.Flat() || m.Trust != sap.TrustVerified || string(m.Body) != body {
		t.Fatalf("stored %+v, want #%d from %s [verified] with body %q", m, seq, z.alice.Flat(), body)
	}
}

// TestS5Today_MailToOfflineEndpointIsStored runs now: with no forged answer at all, mail to an offline endpoint is
// stored. It proves the send-and-assert half of the AT-015 offline test below works today.
func TestS5Today_MailToOfflineEndpointIsStored(t *testing.T) {
	z := newS5Fix(t)
	s5SendToOfflineBobAndAssertStored(t, z, "hello offline bob")
}

// TestS5AT015_AnswerNamingOfflineEndpointDoesNotStopMail: forged answers that name an endpoint that is offline (bob;
// signed by others) are published and rejected through the constructor's Discover, which finds no registration and
// returns no error; then a send to bob, which main would store, is stored: one copy, from alice, with the body.
func TestS5AT015_AnswerNamingOfflineEndpointDoesNotStopMail(t *testing.T) {
	needS5Caller(t, "AT-015 forged answer naming an offline endpoint")
	z := newS5Fix(t)
	forged := z.refused(t, z.genuineRegistration(t, z.bob))
	z.race(t, s5None, forged, nil)
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	got, err := cl.Discover(s5Ctx(t), z.bob)
	if err != nil || len(got.Accepted) != 0 {
		t.Fatalf("discover of the offline endpoint: %+v, %v (want no registration, no error)", got, err)
	}
	s5SendToOfflineBobAndAssertStored(t, z, "hello offline bob, after forged answers")
}

// TestS5Presence_VerdictOnTheNegativeList pins what presence does with each answer of AT-015's negative list under the
// S5 switch: it refuses every one, the owner difference included, counting each once as not verified, and counts the
// genuine answer as presence.
func TestS5Presence_VerdictOnTheNegativeList(t *testing.T) {
	setS5Hook(t, s5On())
	z := newS5Fix(t)
	forged := z.forgeries(t, z.genuineRegistration(t, z.bob))
	alice, err := OpenClientWith(context.Background(), z.cfgAt(z.alice), ClientOptions{FullDirectory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(alice.Close)
	for _, f := range forged {
		before := alice.AnswersNotVerified()
		if got := alice.applyPresence(&nats.Msg{Data: f.Data}); got != 0 || alice.AnswersNotVerified() != before+1 {
			t.Errorf("the %s answer counted %d and rejected %d, want 0 and 1", f.Name, got, alice.AnswersNotVerified()-before)
		}
	}
	before := alice.AnswersNotVerified()
	if got := alice.applyPresence(&nats.Msg{Data: z.genuineRegistration(t, z.bob)}); got != 1 || alice.AnswersNotVerified() != before {
		t.Errorf("the genuine answer counted %d and rejected %d, want 1 and 0", got, alice.AnswersNotVerified()-before)
	}
	if n := alice.AnswersNotVerified(); n != len(forged) {
		t.Errorf("the endpoint's total is %d, want %d (every refused answer once)", n, len(forged))
	}
}
