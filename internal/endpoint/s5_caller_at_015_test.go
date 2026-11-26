package endpoint

// AT-015 (REQ-016): the caller refuses what it cannot verify. Run on the S5 constructor under a test driver; the
// discovery under test is mash's own (presence, applyPresence, the $SRV.INFO answer handling), not the SDK's. The
// real-W-binary variant (mash who, mash send discovery, mash prompt; the warning line, answers_not_verified in
// --json, the stored send) is W's: the seam reports the count and the accepted registrations, which is what those
// views are built from.

import (
	"context"
	"testing"
	"time"

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

// TestS5AT015_NegativesEachRejectedNeverAttributed: each invalid answer on its own, with no valid answer at all (bob
// is offline): rejected, counted once, and bob is not found (he shows as with no answer from the peer). No prompt is
// published (no unverified retry).
func TestS5AT015_NegativesEachRejectedNeverAttributed(t *testing.T) {
	needS5Caller(t, "AT-015 negatives (the rejected-answer count: mash's discovery does not count rejected answers yet: REQ-016/TO-12)")
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	forged := z.forgeries(t, genuine)
	tp := z.tap(t, "agents.prompt.>")
	for _, f := range forged {
		t.Run(f.Name, func(t *testing.T) {
			z.race(t, s5None, []s5Answer{f}, nil)
			cl, _ := z.caller(t, z.alice, s5Inputs{})
			got, err := cl.Discover(s5Ctx(t), z.bob)
			if err != nil {
				t.Fatalf("an invalid answer failed the discovery: %v", err)
			}
			if len(got.Accepted) != 0 {
				t.Fatalf("a %s answer was attributed to bob: %+v", f.Name, got.Accepted)
			}
			if got.NotVerified != 1 || cl.NotVerified() != 1 {
				t.Fatalf("%s: counted %d (total %d) as not verified, want 1", f.Name, got.NotVerified, cl.NotVerified())
			}
			// Prompt acts as for an endpoint with no answer: an error, and nothing published to bob.
			if _, err := cl.Prompt(s5Ctx(t), z.bob, "ping"); err == nil {
				t.Fatalf("%s: a prompt to an endpoint with no verified answer succeeded", f.Name)
			}
			if p := tp.where(func(m s5Msg) bool { return m.Subject == mustSubj(z.bob).Prompt }); len(p) != 0 {
				t.Fatalf("%s: a prompt reached bob's prompt subject without a verified registration (%d)", f.Name, len(p))
			}
		})
	}
}

// TestS5AT015_RaceInvalidNeverDeniesTheValidAnswer is SEC-12: a same-owner peer (carol) publishes each invalid answer,
// one at a time and then all together, before the valid one (delayed until hers have been received) and then after
// it. Every run: the valid discovery succeeds with bob found once, and every invalid answer is rejected and counted.
// Then a prompt to bob works.
func TestS5AT015_RaceInvalidNeverDeniesTheValidAnswer(t *testing.T) {
	needS5Caller(t, "AT-015 race (SEC-12; the late-invalid count: mash's discovery does not count rejected answers yet: REQ-016/TO-12, S5/W)")
	// Why this is skipped today: mash's discovery does not count rejected answers yet (REQ-016/TO-12, S5/W). Main's
	// applyPresence drops an invalid answer silently, before or after the valid one.
	z := newS5Fix(t)
	genuine := z.genuineRegistration(t, z.bob)
	forged := z.forgeries(t, genuine)
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
				// before or after the valid one, and the valid answer wins. This is mash's own discovery's job. Main
				// does not count rejected answers yet (applyPresence only returns the count it accepted), so the
				// late-invalid count is skip-guarded until S5/W lands it (TO-12).
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
	forged := z.forgeries(t, z.genuineRegistration(t, z.bob))
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
	if cl.NotVerified() < len(forged) {
		t.Fatalf("counted %d as not verified, want at least %d", cl.NotVerified(), len(forged))
	}
}

// TestS5AT015_AnswerNamingOfflineEndpointDoesNotStopMail: a forged answer that names an endpoint that is offline
// (mallory's address, signed by carol) is rejected and counted, and the endpoint stays unfound (so a send that main
// would store is stored: the send path is W's; here the caller must report no registration and no error).
func TestS5AT015_AnswerNamingOfflineEndpointDoesNotStopMail(t *testing.T) {
	needS5Caller(t, "AT-015 forged answer naming an offline endpoint")
	z := newS5Fix(t)
	forged := z.forgeries(t, z.genuineRegistration(t, z.bob))
	z.race(t, s5None, forged, nil)
	cl, _ := z.caller(t, z.alice, s5Inputs{})
	got, err := cl.Discover(s5Ctx(t), z.bob)
	if err != nil || len(got.Accepted) != 0 {
		t.Fatalf("discover of the offline endpoint: %+v, %v (want no registration, no error)", got, err)
	}
}

// TestS5Today_PresenceVerdictOnTheNegativeList pins what main's presence check (applyPresence) does with each answer
// of AT-015's negative list, so the contract's tightening is visible and deliberate. It runs now. Main counts the
// answer as presence when the id_sig verifies and the key is the directory's key for the address in the prompt
// subject. So main refuses the unsigned, forged-sig, wrong-key, subject and user-nkey answers, and it ACCEPTS the
// owner and account differences (it does not compare metadata owner or account with the directory record; the code
// comment says metadata owner is not a filter). REQ-016 asks the new caller to refuse those two as well.
func TestS5Today_PresenceVerdictOnTheNegativeList(t *testing.T) {
	z := newS5Fix(t)
	forged := z.forgeries(t, z.genuineRegistration(t, z.bob))
	alice, err := OpenClientWith(context.Background(), z.cfgAt(z.alice), ClientOptions{FullDirectory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(alice.Close)
	today := map[string]int{"unsigned": 0, "forged-sig": 0, "wrong-key": 0, "subject": 0, "user-nkey": 0, "owner": 1, "account": 1}
	for _, f := range forged {
		want, ok := today[f.Name]
		if !ok {
			t.Fatalf("no recorded verdict for %s", f.Name)
		}
		if got := alice.applyPresence(&nats.Msg{Data: f.Data}); got != want {
			t.Errorf("main's presence check counted %d for the %s answer, recorded %d", got, f.Name, want)
		}
	}
	// the valid answer is counted
	if got := alice.applyPresence(&nats.Msg{Data: z.genuineRegistration(t, z.bob)}); got != 1 {
		t.Errorf("main's presence check counted %d for the genuine answer, want 1", got)
	}
}
