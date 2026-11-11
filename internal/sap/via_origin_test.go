package sap

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

func viaHeaderFor(t *testing.T, id Identity, nonce string) SenderHeader {
	t.Helper()
	h, err := id.Sign(b13Subject, []byte(b13Payload), "n", b13Time(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A relay whose origin is a three-token endpoint, signed by a legacy-named sender, round-trips.
func TestViaOriginFlatKeyFromLegacySigner(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	const origin = "claude_alice_builder"
	v, err := id.SignVia(h, origin, 9)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.HasPrefix(v, origin+"#9.") {
		t.Fatalf("value %q", v)
	}
	if o, seq, ok := VerifyVia(h, v); !ok || o != origin || seq != 9 {
		t.Fatalf("verify: %q %d %v", o, seq, ok)
	}
}

// A three-token signer (the header's subject is a new-form mail subject) relaying a legacy-named origin.
func TestViaOriginLegacyNameFromNewFormSigner(t *testing.T) {
	id := b13Identity(t)
	nf, err := New("claude", "alice", "builder")
	if err != nil {
		t.Fatal(err)
	}
	h, err := id.Sign(nf.Subjects().Mail, []byte("x"), nf.Flat(), b13Time(), "NONCE2")
	if err != nil {
		t.Fatal(err)
	}
	v, err := id.SignVia(h, "origin-a", 3)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if o, seq, ok := VerifyVia(h, v); !ok || o != "origin-a" || seq != 3 {
		t.Fatalf("verify: %q %d %v", o, seq, ok)
	}
	// and a three-token origin from the same three-token signer
	v2, err := id.SignVia(h, "gemini_bob_reviewer", 4)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if o, seq, ok := VerifyVia(h, v2); !ok || o != "gemini_bob_reviewer" || seq != 4 {
		t.Fatalf("verify: %q %d %v", o, seq, ok)
	}
}

func TestViaOriginFlatKeyForgery(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	v, err := id.SignVia(h, "claude_alice_builder", 9)
	if err != nil {
		t.Fatal(err)
	}
	sig := v[strings.IndexByte(v, '.')+1:]
	for _, bad := range []string{
		"claude_alice_other#9." + sig,   // a different flat key
		"builder#9." + sig,              // a legacy name swapped in
		"claude_alice_builder#8." + sig, // another sequence
		"claude_alice_builder#9." + tamperSig(sig),
	} {
		if _, _, ok := VerifyVia(h, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// the origin of another message's header
	other := viaHeaderFor(t, id, "NONCE3")
	ov, err := id.SignVia(other, "claude_alice_builder", 9)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := VerifyVia(h, ov); ok {
		t.Fatal("another message's declaration accepted")
	}
	// one byte of the signature changed
	b := []byte(v)
	b[len(b)-3] ^= 1
	if _, _, ok := VerifyVia(h, string(b)); ok {
		t.Fatal("byte-flipped signature accepted")
	}
}

// Not canonical, not three tokens, or not valid: refused on both sides.
func TestViaOriginMalformedRefused(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	// forge signs viaInput directly, so the signature is VALID for the origin and only the syntax rule can refuse it.
	forge := func(o string) string {
		sig, err := id.kp.Sign(viaInput(h, o, 9))
		if err != nil {
			t.Fatal(err)
		}
		return o + "#" + strconv.FormatUint(9, 10) + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	if o, seq, ok := VerifyVia(h, forge("claude_alice_builder")); !ok || o != "claude_alice_builder" || seq != 9 {
		t.Fatalf("control: validly signed flat origin refused: %q %d %v", o, seq, ok)
	}
	tok32 := strings.Repeat("a", 32)
	for _, o := range []string{
		"claude_alice",                   // two tokens
		"claude_alice_builder_x",         // four tokens
		"_",                              //
		"claude_alice_",                  // empty token
		"_alice_builder",                 // leading underscore
		"claude__builder",                // consecutive underscores
		"claude_alice_builder_",          // trailing underscore
		"Claude_alice_builder",           // upper case
		"claude_Alice_builder",           //
		"claude_alice_builder ",          // whitespace
		"claude alice_builder_x",         //
		"claude_alice_builder\n",         // newline
		"claude_ali.ce_builder",          // dot
		"claude_ali#ce_builder",          // hash
		"claude_1alice_builder",          // token starts with a digit
		"claude_alice-_builder",          // token ends with a dash
		"claude_al--ice_builder",         // consecutive dashes
		"cc_alice_builder",               // the legacy agent token
		tok32 + "_" + tok32 + "_" + "a",  // total over 64
		strings.Repeat("a", 33) + "_b_c", // token over 32
		"claude_alice_builder\x00",
		"é_a_b",
	} {
		if _, err := id.SignVia(h, o, 9); err == nil {
			t.Errorf("signed origin %q", o)
		}
		if _, _, ok := VerifyVia(h, forge(o)); ok {
			t.Errorf("verified origin %q", o)
		}
	}
}

// The predicate itself: a legacy name or a canonical flat key, nothing else.
func TestValidViaOrigin(t *testing.T) {
	for _, o := range []string{"a", "origin-a", strings.Repeat("a", 48), "claude_alice_builder", "x1_y-2_z3",
		strings.Repeat("a", 32) + "_" + strings.Repeat("b", 29) + "_c"} {
		if !validViaOrigin(o) {
			t.Errorf("refused %q", o)
		}
	}
	for _, o := range []string{"", "cc_a_b", "a_b", strings.Repeat("a", 49), "a_b_c_d"} {
		if validViaOrigin(o) {
			t.Errorf("accepted %q", o)
		}
	}
}

// Legacy origins are byte-for-byte what the code before the flat-key change produced (values captured from that code
// with the SAP B.13 identity, nonce NONCE1 and sequence 7; ed25519 signatures are deterministic).
func TestViaLegacyGolden(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	for origin, want := range map[string]string{
		"origin-a": "origin-a#7.IMb5YQ9In0onuS9vqfOARAHLyLx4TzDqziboOeg3a_T1HoMm4xwAZYpBMnXfZofGR4lj0gGhvyXtR016ZTIODQ",
		"c":        "c#7.icJGEMp-3x-myDrXq7CaJkgfa95yjKPB8fovC1T1ekVbqGvn2y47s6Sc8u5l3KVOCEVTor1e_usLSCiF85dVBQ",
		"a1-b2-c3": "a1-b2-c3#7.FsjRWDVZYkxjxPrqtpnv-q9Z_mVOYFgNbWqkleJjNG_OepcbKObvtv9l4BC1UF6EuG9xdIIinuOoK8ExX4kpCg",
	} {
		got, err := id.SignVia(h, origin, 7)
		if err != nil || got != want {
			t.Errorf("%s: %q %v", origin, got, err)
		}
		if o, seq, ok := VerifyVia(h, want); !ok || o != origin || seq != 7 {
			t.Errorf("%s: golden does not verify: %q %d %v", origin, o, seq, ok)
		}
	}
}
