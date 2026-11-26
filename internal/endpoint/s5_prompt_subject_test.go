package endpoint

// A prompt discovery counts and verifies only the answers for the requested subject (codex M1): a one-shot client
// holds its own record and the target's, so a valid answer from any other endpoint fails for want of a local record,
// and counting it would inflate AnswersNotVerified. Answers for other subjects are not this discovery's business.

import (
	"context"
	"testing"
)

func TestS5PromptDiscovery_CountsOnlyTheRequestedSubject(t *testing.T) {
	setS5Hook(t, s5On())
	z := newS5Fix(t)
	genuineBob := z.genuineRegistration(t, z.bob)
	genuineCarol := z.genuineRegistration(t, z.carol)
	forged := z.forgeries(t, genuineBob)

	alice, err := OpenClientWith(context.Background(), z.cfgAt(z.alice), ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(alice.Close)
	if _, ok := alice.dir[z.carol.Flat()]; ok {
		t.Fatal("the client holds carol's record: the test needs an endpoint outside the asker's records")
	}
	alice.ensureRecord(context.Background(), z.bob.Flat())
	h := s5Hook()
	want := mustSubj(z.bob).Prompt

	// a valid answer from an endpoint outside the asker's records: not vouched, not counted
	if alice.judgeAnswerS5(h, genuineCarol, want) || alice.AnswersNotVerified() != 0 {
		t.Fatalf("carol's valid answer: counted %d, want 0 and no vouch", alice.AnswersNotVerified())
	}
	// an invalid answer for a different subject (signed by bob's key, naming carol's prompt subject): not counted
	var other []byte
	for _, f := range forged {
		if f.Name == "subject" {
			other = f.Data
		}
	}
	if other == nil {
		t.Fatal("no forged answer for a different subject")
	}
	if alice.judgeAnswerS5(h, other, want) || alice.AnswersNotVerified() != 0 {
		t.Fatalf("the invalid answer for another subject: counted %d, want 0", alice.AnswersNotVerified())
	}
	// the genuine answer at want is vouched and not counted
	if !alice.judgeAnswerS5(h, genuineBob, want) || alice.AnswersNotVerified() != 0 {
		t.Fatalf("bob's genuine answer: counted %d, want vouched and 0", alice.AnswersNotVerified())
	}
	// an invalid answer at want counts once
	n := 0
	for _, f := range forged {
		if f.Name == "subject" {
			continue
		}
		n++
		if alice.judgeAnswerS5(h, f.Data, want) {
			t.Errorf("the %s answer was vouched", f.Name)
		}
		if got := alice.AnswersNotVerified(); got != n {
			t.Errorf("after the %s answer the count is %d, want %d", f.Name, got, n)
		}
	}
	if n == 0 {
		t.Fatal("no forged answer at the requested subject")
	}
}
