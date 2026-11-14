package policydoc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

const testAccount = "ACXTEST"

func doc(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": DocumentSchema, "account": testAccount, "range": "foo", "generation": gen, "base_generation": base,
		"issued_at": "2026-11-12T17:00:00Z",
		"groups": map[string]any{
			"c": map[string]any{"mail": "shared", "prompt": "closed", "status": "shared", "heartbeat": "shared"},
		},
		"senders": map[string]any{
			"foo.c.core": map[string]any{"allow": []string{"foo.d.build"}, "deny": []string{}},
			"foo.c.lead": map[string]any{"allow": []string{}, "deny": []string{"foo.c.core"}},
		},
	}
	if mod != nil {
		mod(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func sign(t *testing.T, kp nkeys.KeyPair, payload []byte) []byte {
	t.Helper()
	enc := base64.RawURLEncoding
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	sig, err := kp.Sign([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(in + "." + enc.EncodeToString(sig))
}

func submitter(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

func TestDecodeRaw(t *testing.T) {
	raw := doc(7, 6, nil)
	d, err := Decode(raw, DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verified || d.Generation != 7 || d.BaseGeneration != 6 || d.ListSet.Base != 6 || d.ListSet.Range != "foo" || string(d.Raw) != string(raw) {
		t.Fatalf("%+v", d)
	}
	if len(d.ListSet.Switches) != 4 || d.ListSet.Switches[0] != (listset.SwitchEntry{Group: "foo.c", Verb: listset.Mail, Switch: listset.Shared}) {
		t.Fatalf("switches %+v", d.ListSet.Switches)
	}
	if len(d.ListSet.Senders) != 2 || d.ListSet.Senders[0].Sender != "foo.c.core" || d.ListSet.Senders[1].Deny[0] != "foo.c.core" {
		t.Fatalf("senders %+v", d.ListSet.Senders)
	}
	if _, err := Decode(raw, DecodeOptions{Submitter: "x"}); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("raw with a submitter: %v", err)
	}
}

func TestDecodeJWS(t *testing.T) {
	kp, pub := submitter(t)
	tok := sign(t, kp, doc(7, 6, nil))
	d, err := Decode(tok, DecodeOptions{Submitter: pub})
	if err != nil || !d.Verified || string(d.Raw) != string(tok) {
		t.Fatalf("verified decode: %+v, %v", d, err)
	}
	d, err = Decode(append(tok, '\n'), DecodeOptions{})
	if err != nil || d.Verified {
		t.Fatalf("unchecked decode: %+v, %v", d, err)
	}
	_, other := submitter(t)
	if _, err := Decode(tok, DecodeOptions{Submitter: other}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("other key: %v", err)
	}
	// A payload changed after signing.
	parts := strings.Split(string(tok), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString(doc(8, 7, nil))
	forged := []byte(strings.Join(parts, "."))
	if _, err := Decode(forged, DecodeOptions{Submitter: pub}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged payload: %v", err)
	}
	if d, err := Decode(forged, DecodeOptions{}); err != nil || d.Verified {
		t.Fatalf("forged payload without a submitter must decode unverified: %+v, %v", d, err)
	}
	if _, err := Decode(tok, DecodeOptions{Submitter: "not a key"}); err == nil {
		t.Fatal("a bad submitter key was accepted")
	}
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."
	if _, err := Decode([]byte(none), DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
		t.Fatalf("alg none: %v", err)
	}
}

func TestDecodeRefusals(t *testing.T) {
	cases := map[string][]byte{
		"unknown field":       doc(7, 6, func(m map[string]any) { m["extra"] = 1 }),
		"unknown group verb":  doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["fax"] = "shared" }),
		"missing verb":        doc(7, 6, func(m map[string]any) { delete(m["groups"].(map[string]any)["c"].(map[string]any), "heartbeat") }),
		"bad switch value":    doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["mail"] = "open" }),
		"wrong schema":        doc(7, 6, func(m map[string]any) { m["schema"] = "mash.listset/2" }),
		"missing schema":      doc(7, 6, func(m map[string]any) { delete(m, "schema") }),
		"missing senders":     doc(7, 6, func(m map[string]any) { delete(m, "senders") }),
		"missing deny":        doc(7, 6, func(m map[string]any) { delete(m["senders"].(map[string]any)["foo.c.core"].(map[string]any), "deny") }),
		"generation skips":    doc(8, 6, nil),
		"generation repeats":  doc(6, 6, nil),
		"bad time":            doc(7, 6, func(m map[string]any) { m["issued_at"] = "yesterday" }),
		"duplicate key":       []byte(strings.Replace(string(doc(7, 6, nil)), `"range":"foo"`, `"range":"foo","range":"bar"`, 1)),
		"duplicate nested":    []byte(strings.Replace(string(doc(7, 6, nil)), `"mail":"shared"`, `"mail":"shared","mail":"closed"`, 1)),
		"trailing data":       append(doc(7, 6, nil), []byte(` {}`)...),
		"not json":            []byte("hello"),
		"empty":               nil,
		"base is max uint64":  []byte(strings.Replace(string(doc(7, 6, nil)), `"base_generation":6`, `"base_generation":18446744073709551615`, 1)),
		"negative generation": []byte(strings.Replace(string(doc(7, 6, nil)), `"generation":7`, `"generation":-1`, 1)),
	}
	for name, b := range cases {
		if _, err := Decode(b, DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestNextSubmitter(t *testing.T) {
	d, err := Decode(doc(7, 6, nil), DecodeOptions{})
	if err != nil || d.NextSubmitter != "" {
		t.Fatalf("absent: %+v, %v", d, err)
	}
	_, next := submitter(t)
	d, err = Decode(doc(7, 6, func(m map[string]any) { m["next_submitter"] = next }), DecodeOptions{})
	if err != nil || d.NextSubmitter != next {
		t.Fatalf("valid key: %+v, %v", d, err)
	}
	for name, v := range map[string]any{"invalid": "not a key", "empty": "", "number": 5, "null-ish prefix": "ACXTEST"} {
		if _, err := Decode(doc(7, 6, func(m map[string]any) { m["next_submitter"] = v }), DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := Decode(doc(7, 6, func(m map[string]any) { m["next_submitter"] = next; m["other"] = 1 }), DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
		t.Errorf("other unknown field beside next_submitter: %v", err)
	}
}

func TestNextSubmitterSigned(t *testing.T) {
	kp, pub := submitter(t)
	_, next := submitter(t)
	tok := sign(t, kp, doc(7, 6, func(m map[string]any) { m["next_submitter"] = next }))
	d, err := Decode(tok, DecodeOptions{Submitter: pub})
	if err != nil || !d.Verified || d.NextSubmitter != next {
		t.Fatalf("signed: %+v, %v", d, err)
	}
	// Swapping the field after signing breaks the signature.
	_, other := submitter(t)
	parts := strings.Split(string(tok), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString(doc(7, 6, func(m map[string]any) { m["next_submitter"] = other }))
	if _, err := Decode([]byte(strings.Join(parts, ".")), DecodeOptions{Submitter: pub}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered next_submitter: %v", err)
	}
}
