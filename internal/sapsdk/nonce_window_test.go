package sapsdk

import (
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
)

// A header can verify from ts-LiveSkew to ts+LiveSkew, so a nonce first seen at the early edge must be remembered for
// 2*LiveSkew. These tests run VerifyLive with the set's window at its floor.
func TestNonceWindowCoversTheWholeValidSpan(t *testing.T) {
	const subj = "agents.prompt.cc.o.n"
	pay := []byte(`{"prompt":"hi"}`)
	for _, tc := range []struct {
		name   string
		tsOff  time.Duration // header ts relative to the first receipt
		replay []time.Duration
	}{
		{"ts 29s ahead", 29 * time.Second, []time.Duration{1 * time.Second, 30 * time.Second, 31 * time.Second, 58 * time.Second}},
		{"ts 29s behind", -29 * time.Second, []time.Duration{1 * time.Second, 30 * time.Second}},
		{"ts 30s ahead, last valid moment", 30 * time.Second, []time.Duration{30 * time.Second, 59 * time.Second, 60 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newCompatKit(t)
			h, err := k.old.Sign(subj, pay, "alice", compatNow.Add(tc.tsOff), "n-w")
			if err != nil {
				t.Fatal(err)
			}
			hdr := h.Encode()
			set := NewNonceSet(0) // floored
			if r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil); r.Outcome != Admitted {
				t.Fatalf("first: %+v", r)
			}
			for _, d := range tc.replay {
				r := VerifyLive([]string{hdr}, subj, pay, compatNow.Add(d), set, nil)
				if r.Outcome != NotVerified || r.Class != "not verified" || r.Trust != sap.TrustForged {
					t.Errorf("replay at +%v: %+v", d, r)
				}
			}
		})
	}
}

// A pair is forgotten after 2*LiveSkew only when its header can no longer verify: past ts+LiveSkew the ts check
// refuses it, whether or not the set remembers it.
func TestNonceForgottenOnlyWhenHeaderCannotVerify(t *testing.T) {
	const subj = "agents.prompt.cc.o.n"
	pay := []byte(`{"prompt":"hi"}`)
	k := newCompatKit(t)
	h, err := k.old.Sign(subj, pay, "alice", compatNow.Add(29*time.Second), "n-f")
	if err != nil {
		t.Fatal(err)
	}
	hdr := h.Encode()
	set := NewNonceSet(0)
	if r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil); r.Outcome != Admitted {
		t.Fatal(r)
	}
	// ts+30s is now+59s: still verifies on time, so the set must still hold the pair.
	if !set.Contains(h.User, "n-f") {
		t.Fatal("pair forgotten while the header can still verify")
	}
	late := compatNow.Add(2*LiveSkew + time.Second) // past ts+LiveSkew
	r := VerifyLive([]string{hdr}, subj, pay, late, set, nil)
	if r.Outcome != NotVerified {
		t.Fatalf("late replay: %+v", r)
	}
	if set.Seen(h.User, "n-f", late) != NonceFresh {
		t.Fatal("pair still held after 2*LiveSkew: the set keeps it longer than needed")
	}
}
