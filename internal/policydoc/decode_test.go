package policydoc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/listset"
)

const testAccount = "ACXTEST"

func doc(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": DocumentSchema, "account": testAccount, "owner": "foo", "generation": gen, "base_generation": base,
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

func TestDecodeRaw(t *testing.T) {
	raw := doc(7, 6, nil)
	d, err := Decode(raw, DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Generation != 7 || d.BaseGeneration != 6 || d.ListSet.Base != 6 || d.ListSet.Owner != "foo" || string(d.Raw) != string(raw) {
		t.Fatalf("%+v", d)
	}
	if len(d.ListSet.Switches) != 4 || d.ListSet.Switches[0] != (listset.SwitchEntry{Group: "foo.c", Verb: listset.Mail, Switch: listset.Shared}) {
		t.Fatalf("switches %+v", d.ListSet.Switches)
	}
	if len(d.ListSet.Senders) != 2 || d.ListSet.Senders[0].Sender != "foo.c.core" || d.ListSet.Senders[1].Deny[0] != "foo.c.core" {
		t.Fatalf("senders %+v", d.ListSet.Senders)
	}
}

// A compact JWS (the withdrawn signed form) is refused like any non-JSON input.
func TestDecodeRefusesJWS(t *testing.T) {
	enc := base64.RawURLEncoding
	tok := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(doc(7, 6, nil)) + "." + enc.EncodeToString([]byte("sig"))
	if _, err := Decode([]byte(tok), DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
		t.Fatalf("JWS-shaped input: %v", err)
	}
}

// next_submitter (the withdrawn rotation field) is an unknown field.
func TestDecodeRefusesNextSubmitter(t *testing.T) {
	_, err := Decode(doc(7, 6, func(m map[string]any) { m["next_submitter"] = "UABC" }), DecodeOptions{})
	if !errors.Is(err, ErrBadDocument) || !strings.Contains(err.Error(), "next_submitter") {
		t.Fatalf("next_submitter: %v", err)
	}
}

// An unknown field is refused, and the refusal names it.
func TestDecodeRefusesAnUnknownField(t *testing.T) {
	_, err := Decode(doc(7, 6, func(m map[string]any) { m["extra_field"] = "UABC" }), DecodeOptions{})
	if !errors.Is(err, ErrBadDocument) || !strings.Contains(err.Error(), "extra_field") {
		t.Fatalf("extra_field: %v", err)
	}
}

func TestDecodeRefusals(t *testing.T) {
	cases := map[string][]byte{
		"unknown field":       doc(7, 6, func(m map[string]any) { m["extra"] = 1 }),
		"unknown group verb":  doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["fax"] = "shared" }),
		"missing verb":        doc(7, 6, func(m map[string]any) { delete(m["groups"].(map[string]any)["c"].(map[string]any), "heartbeat") }),
		"bad switch value":    doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["mail"] = "open" }),
		"wrong schema":        doc(7, 6, func(m map[string]any) { m["schema"] = "mash.listset/1" }),
		"missing schema":      doc(7, 6, func(m map[string]any) { delete(m, "schema") }),
		"missing senders":     doc(7, 6, func(m map[string]any) { delete(m, "senders") }),
		"missing deny":        doc(7, 6, func(m map[string]any) { delete(m["senders"].(map[string]any)["foo.c.core"].(map[string]any), "deny") }),
		"generation skips":    doc(8, 6, nil),
		"generation repeats":  doc(6, 6, nil),
		"bad time":            doc(7, 6, func(m map[string]any) { m["issued_at"] = "yesterday" }),
		"duplicate key":       []byte(strings.Replace(string(doc(7, 6, nil)), `"owner":"foo"`, `"owner":"foo","owner":"bar"`, 1)),
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

func TestDecodeGroupKeyedByAddress(t *testing.T) {
	for name, key := range map[string]string{"address": "lab.h1.l1", "owner.group": "lab.h1"} {
		b := doc(7, 6, func(m map[string]any) {
			m["owner"] = "lab"
			g := m["groups"].(map[string]any)
			g[key] = g["c"]
			delete(g, "c")
		})
		_, err := Decode(b, DecodeOptions{})
		if !errors.Is(err, ErrBadDocument) || !strings.Contains(err.Error(), `group "`+key+`"`) ||
			!strings.Contains(err.Error(), "group part") || strings.Contains(err.Error(), "lab.lab") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}
