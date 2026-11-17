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

// A relay whose origin is an endpoint's flat key round-trips.
func TestViaOriginFlatKey(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	const origin = "alice_ops_builder"
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

func TestViaOriginFlatKeyForgery(t *testing.T) {
	id := b13Identity(t)
	h := viaHeaderFor(t, id, "NONCE1")
	v, err := id.SignVia(h, "alice_ops_builder", 9)
	if err != nil {
		t.Fatal(err)
	}
	sig := v[strings.IndexByte(v, '.')+1:]
	for _, bad := range []string{
		"alice_ops_other#9." + sig,   // a different flat key
		"builder#9." + sig,           // a bare name swapped in
		"alice_ops_builder#8." + sig, // another sequence
		"alice_ops_builder#9." + tamperSig(sig),
	} {
		if _, _, ok := VerifyVia(h, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// the origin of another message's header
	other := viaHeaderFor(t, id, "NONCE3")
	ov, err := id.SignVia(other, "alice_ops_builder", 9)
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
	if o, seq, ok := VerifyVia(h, forge("alice_ops_builder")); !ok || o != "alice_ops_builder" || seq != 9 {
		t.Fatalf("control: validly signed flat origin refused: %q %d %v", o, seq, ok)
	}
	tok32 := strings.Repeat("a", 32)
	for _, o := range []string{
		"claude_alice",                   // two tokens
		"alice_ops_builder_x",            // four tokens
		"_",                              //
		"claude_alice_",                  // empty token
		"_alice_builder",                 // underscore first
		"claude__builder",                // consecutive underscores
		"alice_ops_builder_",             // trailing underscore
		"Claude_alice_builder",           // upper case
		"claude_Alice_builder",           //
		"alice_ops_builder ",             // whitespace
		"claude alice_builder_x",         //
		"alice_ops_builder\n",            // newline
		"claude_ali.ce_builder",          // dot
		"claude_ali#ce_builder",          // hash
		"claude_1alice_builder",          // token starts with a digit
		"claude_alice-_builder",          // token ends with a dash
		"claude_al--ice_builder",         // consecutive dashes
		"origin-a",                       // a bare name
		tok32 + "_" + tok32 + "_" + "a",  // total over 64
		strings.Repeat("a", 33) + "_b_c", // token over 32
		"alice_ops_builder\x00",
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

// The predicate itself: a canonical flat key, nothing else.
func TestValidViaOrigin(t *testing.T) {
	for _, o := range []string{"alice_ops_builder", "x1_y-2_z3",
		strings.Repeat("a", 32) + "_" + strings.Repeat("b", 29) + "_c"} {
		if !validViaOrigin(o) {
			t.Errorf("refused %q", o)
		}
	}
	for _, o := range []string{"", "a", "origin-a", "a_b", "a_b_c_d"} {
		if validViaOrigin(o) {
			t.Errorf("accepted %q", o)
		}
	}
}
