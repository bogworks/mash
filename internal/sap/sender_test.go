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
