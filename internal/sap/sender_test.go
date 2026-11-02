package sap

import (
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// SAP Appendix B.13 known-answer vector (test-only throwaway seed).
const (
	b13Seed    = "SUAEJ6GDK6FSSB54LD45Q7W25AW7NUT7MVLBABIR5MIPFUTBW7ZNPK2KYE"
	b13Account = "AABYLMBR6Q2CDXTLGRQCFA2GP76BGCDF7NZF2OVHH4RQ7L3Y3TZWJDRL"
	b13User    = "UCDUW5V44EBDBIK2FL4CTNDBQFNGBEZVJHSZGQVKRHASN4AV4IWPB5NT"
	b13Subject = "agents.prompt.demo-agent.alice.example"
	b13Payload = `{"prompt":"hello"}`
	b13TS      = "2026-08-28T12:00:00Z"
	b13Nonce   = "nonce-operator-account-signed"
	b13Sig     = "EPGc0tNF0ZQeBqXUduwUp3zdi33k1AKIXOX-PUPSeY9QtS4V3d2EZLq-fHuPuYkw6Cc3HdVn_jVI58Y_3YBZBw"
	b13Header  = `{"v":1,"account":"AABYLMBR6Q2CDXTLGRQCFA2GP76BGCDF7NZF2OVHH4RQ7L3Y3TZWJDRL","user":"UCDUW5V44EBDBIK2FL4CTNDBQFNGBEZVJHSZGQVKRHASN4AV4IWPB5NT","name":"claude-code","sub":"agents.prompt.demo-agent.alice.example","ts":"2026-08-28T12:00:00Z","nonce":"nonce-operator-account-signed","sig":"EPGc0tNF0ZQeBqXUduwUp3zdi33k1AKIXOX-PUPSeY9QtS4V3d2EZLq-fHuPuYkw6Cc3HdVn_jVI58Y_3YBZBw"}`
)

func b13Identity(t *testing.T) Identity {
	t.Helper()
	id, err := NewIdentity([]byte(b13Seed), b13Account)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func b13Time() time.Time {
	ts, _ := time.Parse(time.RFC3339, b13TS)
	return ts
}

func TestSignKnownAnswerB13(t *testing.T) {
	id := b13Identity(t)
	if id.User != b13User || id.Account != b13Account {
		t.Fatalf("identity %s.%s", id.Account, id.User)
	}
	h, err := id.Sign(b13Subject, []byte(b13Payload), "claude-code", b13Time(), b13Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if h.Sig != b13Sig {
		t.Fatalf("sig\n got %s\nwant %s", h.Sig, b13Sig)
	}
	enc := h.Encode()
	if enc != b13Header {
		t.Fatalf("header\n got %s\nwant %s", enc, b13Header)
	}
	if len(enc) != 373 {
		t.Fatalf("header length %d, want 373", len(enc))
	}
}

func TestVerifyStoredClasses(t *testing.T) {
	payload := []byte(b13Payload)
	if _, tr := VerifyStored(b13Header, b13Subject, payload); tr != TrustVerified {
		t.Fatalf("vector: %s", tr)
	}
	if h, _ := VerifyStored(b13Header, b13Subject, payload); h.User != b13User || h.Name != "claude-code" {
		t.Fatalf("parsed header %+v", h)
	}
	flipped := []byte(b13Payload)
	flipped[3] ^= 1
	if _, tr := VerifyStored(b13Header, b13Subject, flipped); tr != TrustForged {
		t.Fatalf("flipped payload: %s", tr)
	}
	if _, tr := VerifyStored(b13Header, "agents.prompt.demo-agent.alice.other", payload); tr != TrustForged {
		t.Fatalf("other subject: %s", tr)
	}
	claimed := `{"v":1,"account":"` + b13Account + `","user":"` + b13User + `","name":"x"}`
	if _, tr := VerifyStored(claimed, b13Subject, payload); tr != TrustClaimed {
		t.Fatalf("no sig: %s", tr)
	}
	if _, tr := VerifyStored("", b13Subject, payload); tr != TrustAbsent {
		t.Fatalf("empty: %s", tr)
	}
	malformed := []string{
		strings.Replace(b13Header, `"v":1`, `"v":"1"`, 1),
		`not json`,
		`{"v":1,"user":"` + b13User + `"}`,
		`{"v":1,"account":"` + b13Account + `","user":"` + b13User + `","sig":"abc"}`,
		`{"v":1,"account":"` + b13Account + `","user":"Unotakey"}`,
		strings.Replace(b13Header, b13Nonce, "bad nonce!", 1),
	}
	for _, m := range malformed {
		if _, tr := VerifyStored(m, b13Subject, payload); tr != TrustMalformed {
			t.Errorf("%s: %s, want malformed", m, tr)
		}
	}
	unknownV := strings.Replace(b13Header, `"v":1`, `"v":2`, 1)
	if _, tr := VerifyStored(unknownV, b13Subject, payload); tr != TrustAbsent {
		t.Fatalf("v=2: %s", tr)
	}
}

func TestVerifyLiveReplayAndSkew(t *testing.T) {
	payload := []byte(b13Payload)
	seen := NewNonceSet(time.Minute)
	if _, tr := VerifyLive(b13Header, b13Subject, payload, b13Time().Add(5*time.Second), seen); tr != TrustVerified {
		t.Fatalf("first: %s", tr)
	}
	if _, tr := VerifyLive(b13Header, b13Subject, payload, b13Time().Add(6*time.Second), seen); tr != TrustForged {
		t.Fatalf("replay: %s", tr)
	}
	if _, tr := VerifyLive(b13Header, b13Subject, payload, b13Time().Add(31*time.Second), NewNonceSet(time.Minute)); tr != TrustForged {
		t.Fatalf("skew: %s", tr)
	}
}

func TestSignVerifyRoundTripFreshKey(t *testing.T) {
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	id, err := NewIdentity(seed, b13Account)
	if err != nil {
		t.Fatal(err)
	}
	h, err := id.Sign("agents.mail.cc.o.a", []byte("hi"), "b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if h.Nonce == "" || len(h.Nonce) > 64 {
		t.Fatalf("generated nonce %q", h.Nonce)
	}
	if _, tr := VerifyStored(h.Encode(), "agents.mail.cc.o.a", []byte("hi")); tr != TrustVerified {
		t.Fatalf("round trip: %s", tr)
	}
}

func TestIDSigRoundTrip(t *testing.T) {
	id := b13Identity(t)
	sig, err := id.IDSig("claude-code", "alice", b13Subject)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyIDSig(b13User, b13Account, "claude-code", "alice", b13Subject, sig) {
		t.Fatal("id_sig does not verify")
	}
	if VerifyIDSig(b13User, b13Account, "claude-code", "bob", b13Subject, sig) {
		t.Fatal("id_sig verified over the wrong owner")
	}
}

func TestInstanceBindingRoundTrip(t *testing.T) {
	id := b13Identity(t)
	h, err := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := id.SignInstance(h, "INST1")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := VerifyInstance(v, h); !ok || got != "INST1" {
		t.Fatalf("verify: %q %v", got, ok)
	}
	// a different message (nonce) must not accept the same binding
	h2, _ := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	if _, ok := VerifyInstance(v, h2); ok {
		t.Fatal("binding replayed onto another message")
	}
	// a tampered instance fails
	if _, ok := VerifyInstance("EVIL"+v[len("INST1"):], h); ok {
		t.Fatal("tampered instance accepted")
	}
	for _, bad := range []string{"", "nodot", ".sig", "INST1."} {
		if _, ok := VerifyInstance(bad, h); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// signed by another key
	other := freshIdentityForTest(t)
	ov, _ := other.SignInstance(h, "INST1")
	if _, ok := VerifyInstance(ov, h); ok {
		t.Fatal("another key's binding accepted")
	}
}

func freshIdentityForTest(t *testing.T) Identity {
	t.Helper()
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	id, err := NewIdentity(seed, b13Account)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUrgentMarkRoundTrip(t *testing.T) {
	id := b13Identity(t)
	h, err := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := id.SignUrgent(h)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyUrgent(v, h) {
		t.Fatal("own mark rejected")
	}
	h2, _ := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	if VerifyUrgent(v, h2) {
		t.Fatal("mark replayed onto another message")
	}
	other := freshIdentityForTest(t)
	if ov, _ := other.SignUrgent(h); VerifyUrgent(ov, h) {
		t.Fatal("another key's mark accepted")
	}
	// An instance binding is not an urgent mark: the two sign under different domains.
	if iv, _ := id.SignInstance(h, "urgent"); VerifyUrgent(iv, h) || VerifyUrgent(strings.TrimPrefix(iv, "urgent."), h) {
		t.Fatal("an instance signature passed as an urgent mark")
	}
	if VerifyUrgent("", h) || VerifyUrgent("junk", h) {
		t.Fatal("accepted an empty or junk mark")
	}
}

func marksFixture(t *testing.T) (Identity, SenderHeader) {
	t.Helper()
	id := b13Identity(t)
	h, err := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	return id, h
}

func TestInReplyToRoundTripAndForgery(t *testing.T) {
	id, h := marksFixture(t)
	v, err := id.SignInReplyTo(h, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "42.") {
		t.Fatalf("value %q", v)
	}
	if seq, ok := VerifyInReplyTo(h, v); !ok || seq != 42 {
		t.Fatalf("verify: %d %v", seq, ok)
	}
	sig := v[3:]
	// moved to another message: another nonce, ts, subject
	other, _ := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	moved := []SenderHeader{other, func() SenderHeader { o := h; o.TS = "2020-01-01T00:00:00Z"; return o }(),
		func() SenderHeader { o := h; o.Sub = "agents.mail.cc.x.y"; return o }()}
	for i, m := range moved {
		if _, ok := VerifyInReplyTo(m, v); ok {
			t.Fatalf("mark moved to message %d accepted", i)
		}
	}
	// signed by another key
	ov, _ := freshIdentityForTest(t).SignInReplyTo(h, 42)
	if _, ok := VerifyInReplyTo(h, ov); ok {
		t.Fatal("another key's mark accepted")
	}
	// another seq under the same signature
	for _, bad := range []string{"43." + sig, "", "nodot", ".sig", "42.", "x." + sig, "042." + sig, "+42." + sig, "0." + sig,
		"-1." + sig, "42", "42." + tamperSig(sig), "42." + sig + "A", "99999999999999999999." + sig} {
		if _, ok := VerifyInReplyTo(h, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// the old bare decimal
	if _, ok := VerifyInReplyTo(h, "42"); ok {
		t.Fatal("bare decimal accepted")
	}
	if _, err := id.SignInReplyTo(h, 0); err == nil {
		t.Fatal("signed seq 0")
	}
	// the mark is not an urgent or instance signature, nor the reverse
	u, _ := id.SignUrgent(h)
	if _, ok := VerifyInReplyTo(h, "42."+u); ok {
		t.Fatal("urgent signature passed as in-reply-to")
	}
}

func TestViaRoundTripAndForgery(t *testing.T) {
	id, h := marksFixture(t)
	v, err := id.SignVia(h, "origin-a", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "origin-a#7.") {
		t.Fatalf("value %q", v)
	}
	if o, seq, ok := VerifyVia(h, v); !ok || o != "origin-a" || seq != 7 {
		t.Fatalf("verify: %q %d %v", o, seq, ok)
	}
	sig := v[len("origin-a#7."):]
	other, _ := id.Sign(b13Subject, []byte("x"), "n", time.Now(), "")
	for i, m := range []SenderHeader{other, func() SenderHeader { o := h; o.TS = "2020-01-01T00:00:00Z"; return o }(),
		func() SenderHeader { o := h; o.Sub = "agents.mail.cc.x.y"; return o }()} {
		if _, _, ok := VerifyVia(m, v); ok {
			t.Fatalf("mark moved to message %d accepted", i)
		}
	}
	ov, _ := freshIdentityForTest(t).SignVia(h, "origin-a", 7)
	if _, _, ok := VerifyVia(h, ov); ok {
		t.Fatal("another key's mark accepted")
	}
	for _, bad := range []string{"origin-a#8." + sig, "origin-b#7." + sig, "", "origin-a", "origin-a#7", "origin-a7." + sig,
		"#7." + sig, "origin-a#." + sig, "origin-a#x." + sig, "origin-a#07." + sig, "origin-a#0." + sig, "Origin-A#7." + sig,
		"origin a#7." + sig, "origin-a\n#7." + sig, "origin-a#7.", "origin-a#7." + tamperSig(sig), "a#b#7." + sig,
		"origin_a#7." + sig} {
		if _, _, ok := VerifyVia(h, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// a via signature is not an in-reply-to signature over the same seq
	if _, ok := VerifyInReplyTo(h, "7."+sig); ok {
		t.Fatal("via signature passed as in-reply-to")
	}
	for _, o := range []string{"", "Bad", "a b", "a\nb", "-a", strings.Repeat("a", 49)} {
		if _, err := id.SignVia(h, o, 7); err == nil {
			t.Fatalf("signed origin %q", o)
		}
	}
	if _, err := id.SignVia(h, "origin-a", 0); err == nil {
		t.Fatal("signed seq 0")
	}
}

// tamperSig changes the last two characters of a signature, whatever they are: a fixed replacement would equal the
// original for one signature in a few thousand and make the forgery tests flaky.
func tamperSig(sig string) string {
	tail := "AA"
	if strings.HasSuffix(sig, tail) {
		tail = "BB"
	}
	return sig[:len(sig)-2] + tail
}
