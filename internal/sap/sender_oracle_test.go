package sap

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

// The code below is mash's own signing and parsing before it moved to the SAP Go SDK (P1-2), copied unchanged as an
// oracle: the known-answer tests compare the SDK-backed implementation with it, byte for byte.

func signedInput(account, user, subject, ts, nonce string, payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return []byte("AGENT-SENDER-V1\n" + account + "\n" + user + "\n" + subject + "\n" +
		ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:]) + "\n")
}

func oldSign(id Identity, subject string, payload []byte, name string, now time.Time, nonce string) (SenderHeader, error) {
	ts := now.UTC().Format("2006-01-02T15:04:05Z")
	sig, err := id.kp.Sign(signedInput(id.Account, id.User, subject, ts, nonce, payload))
	if err != nil {
		return SenderHeader{}, err
	}
	return SenderHeader{V: 1, Account: id.Account, User: id.User, Name: name, Sub: subject,
		TS: ts, Nonce: nonce, Sig: base64.RawURLEncoding.EncodeToString(sig)}, nil
}

func oldIDInput(user, account, agent, owner, promptSubject string) []byte {
	return []byte("AGENT-ID-V1\n" + user + "\n" + account + "\n" + agent + "\n" + owner + "\n" + promptSubject + "\n")
}

func oldIDSig(id Identity, agent, owner, promptSubject string) (string, error) {
	sig, err := id.kp.Sign(oldIDInput(id.User, id.Account, agent, owner, promptSubject))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

func oldVerifyIDSig(user, account, agent, owner, promptSubject, sig string) bool {
	return verifySig(user, oldIDInput(user, account, agent, owner, promptSubject), sig)
}

func oldValidAccount(a string) bool {
	if strings.HasPrefix(a, "A") && len(a) == 56 && nkeys.IsValidPublicAccountKey(a) {
		return true
	}
	if a == "$G" {
		return true
	}
	if strings.HasPrefix(a, "A") && len(a) == 56 {
		return false
	}
	return oldAccNameRE(a)
}

func oldAccNameRE(a string) bool {
	if a == "" {
		return false
	}
	for _, r := range a {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func oldParse(hdr string) (SenderHeader, Trust) {
	if hdr == "" {
		return SenderHeader{}, TrustAbsent
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(hdr), &raw); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	vRaw, ok := raw["v"]
	if !ok {
		return SenderHeader{}, TrustMalformed
	}
	dec := json.NewDecoder(bytes.NewReader(vRaw))
	dec.UseNumber()
	var vAny any
	if err := dec.Decode(&vAny); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	v, isNum := vAny.(json.Number)
	if !isNum {
		return SenderHeader{}, TrustMalformed
	}
	if v.String() != "1" {
		return SenderHeader{}, TrustAbsent
	}
	str := func(k string) (string, bool, bool) {
		r, present := raw[k]
		if !present {
			return "", false, true
		}
		var s string
		if err := json.Unmarshal(r, &s); err != nil {
			return "", true, false
		}
		return s, true, true
	}
	var h SenderHeader
	h.V = 1
	fields := []struct {
		key string
		dst *string
	}{{"account", &h.Account}, {"user", &h.User}, {"name", &h.Name}, {"sub", &h.Sub},
		{"ts", &h.TS}, {"nonce", &h.Nonce}, {"sig", &h.Sig}}
	present := map[string]bool{}
	for _, f := range fields {
		s, p, ok := str(f.key)
		if !ok {
			return SenderHeader{}, TrustMalformed
		}
		*f.dst, present[f.key] = s, p
	}
	if !present["account"] || !present["user"] || !oldValidAccount(h.Account) || !nkeys.IsValidPublicUserKey(h.User) {
		return SenderHeader{}, TrustMalformed
	}
	if !present["sig"] {
		return h, TrustClaimed
	}
	if !present["sub"] || !present["ts"] || !present["nonce"] || h.Sub == "" || !nonceRE.MatchString(h.Nonce) {
		return SenderHeader{}, TrustMalformed
	}
	if _, err := time.Parse(time.RFC3339Nano, h.TS); err != nil {
		return SenderHeader{}, TrustMalformed
	}
	return h, TrustVerified
}

func oldVerifyStored(hdr, arrivalSubject string, payload []byte) (SenderHeader, Trust) {
	h, tr := oldParse(hdr)
	if tr != TrustVerified {
		return h, tr
	}
	if h.Sub != arrivalSubject ||
		!verifySig(h.User, signedInput(h.Account, h.User, h.Sub, h.TS, h.Nonce, payload), h.Sig) {
		return h, TrustForged
	}
	return h, TrustVerified
}

// Known answers: the SDK-backed Sign, IDSig and VerifyIDSig give the oracle's bytes for the same inputs.

const oracleSeed = "SUAMLK2ZNL35WSMW37E7UD4VZ7ELPKW7DHC3BWBSD2GCZ7IUQQXZIORRBU"

func oracleIdentity(t *testing.T, account string) Identity {
	t.Helper()
	id, err := NewIdentity([]byte(oracleSeed), account)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSignMatchesOldImplementation(t *testing.T) {
	accKey, _ := nkeys.CreateAccount()
	apub, _ := accKey.PublicKey()
	for _, account := range []string{apub, "$G", "acme-corp_1"} {
		id := oracleIdentity(t, account)
		for _, c := range []struct {
			name, subject, payload, hname, nonce string
			now                                  time.Time
		}{
			{"plain", "agents.mail.cc.o.n", `{"text":"hello"}`, "claude-code", "nonce-1", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)},
			{"no name, empty payload", "agents.mail.a.o.n", "", "", "N0", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
			{"fractional now is cut to the second", "agents.mail.cc.o.n", "x", "Zoë 日本", "abcDEF_-0123", time.Date(2026, 11, 15, 12, 0, 0, 987654321, time.UTC)},
			{"non-UTC now", "agents.mail.cc.o.n", "x", "n", "nonce-2", time.Date(2026, 11, 15, 13, 0, 0, 0, time.FixedZone("CET", 3600))},
			{"nonce of 64", "agents.mail.cc.o.n", "x", "n", strings.Repeat("n", 64), time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)},
		} {
			t.Run(c.name, func(t *testing.T) {
				want, err := oldSign(id, c.subject, []byte(c.payload), c.hname, c.now, c.nonce)
				if err != nil {
					t.Fatal(err)
				}
				got, err := id.Sign(c.subject, []byte(c.payload), c.hname, c.now, c.nonce)
				if err != nil {
					t.Fatal(err)
				}
				if got != want || got.Encode() != want.Encode() {
					t.Fatalf("got  %s\nwant %s", got.Encode(), want.Encode())
				}
			})
		}
	}
}

// The header for one fixed input, written out: the wire bytes of a signed header do not move.
func TestSignKnownAnswer(t *testing.T) {
	id := oracleIdentity(t, "$G")
	got, err := id.Sign("agents.mail.cc.o.n", []byte(`{"text":"hello"}`), "claude-code", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := oldSign(id, "agents.mail.cc.o.n", []byte(`{"text":"hello"}`), "claude-code", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), "nonce-1")
	if got.Encode() != want.Encode() {
		t.Fatalf("got  %s\nwant %s", got.Encode(), want.Encode())
	}
	if got.TS != "2026-11-15T12:00:00Z" || got.Nonce != "nonce-1" || got.V != 1 || got.Account != "$G" {
		t.Fatalf("unexpected fields: %+v", got)
	}
}

func TestSignEmptyNonceIsFresh(t *testing.T) {
	id := oracleIdentity(t, "$G")
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	a, err := id.Sign("agents.mail.cc.o.n", nil, "", now, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := id.Sign("agents.mail.cc.o.n", nil, "", now, "")
	if len(a.Nonce) != 22 || a.Nonce == b.Nonce || !nonceRE.MatchString(a.Nonce) {
		t.Fatalf("nonces %q %q", a.Nonce, b.Nonce)
	}
	if _, tr := VerifyStored(a.Encode(), "agents.mail.cc.o.n", nil); tr != TrustVerified {
		t.Fatalf("own signature reads %s", tr)
	}
}

func TestIDSigMatchesOldImplementation(t *testing.T) {
	accKey, _ := nkeys.CreateAccount()
	apub, _ := accKey.PublicKey()
	for _, account := range []string{apub, "$G", "acme"} {
		id := oracleIdentity(t, account)
		want, err := oldIDSig(id, "claude", "homerange", "agents.prompt.claude.homerange.x")
		if err != nil {
			t.Fatal(err)
		}
		got, err := id.IDSig("claude", "homerange", "agents.prompt.claude.homerange.x")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("id_sig %s, old %s", got, want)
		}
		if !VerifyIDSig(id.User, id.Account, "claude", "homerange", "agents.prompt.claude.homerange.x", want) ||
			!oldVerifyIDSig(id.User, id.Account, "claude", "homerange", "agents.prompt.claude.homerange.x", got) {
			t.Fatal("a valid id_sig does not verify")
		}
		// Each signed field is bound.
		for i, args := range [][5]string{
			{id.User, id.Account, "other", "homerange", "agents.prompt.claude.homerange.x"},
			{id.User, id.Account, "claude", "other", "agents.prompt.claude.homerange.x"},
			{id.User, id.Account, "claude", "homerange", "agents.prompt.other"},
			{id.User, "ACCOUNTX", "claude", "homerange", "agents.prompt.claude.homerange.x"},
		} {
			if VerifyIDSig(args[0], args[1], args[2], args[3], args[4], want) != oldVerifyIDSig(args[0], args[1], args[2], args[3], args[4], want) {
				t.Fatalf("variant %d: new and old disagree", i)
			}
			if VerifyIDSig(args[0], args[1], args[2], args[3], args[4], want) {
				t.Fatalf("variant %d verifies", i)
			}
		}
	}
	// A signature by another key, a short one and a non-base64 one all fail in both.
	id := oracleIdentity(t, "$G")
	other := otherUser(t)
	for _, sig := range []string{"", "!!", "AAAA", base64.RawURLEncoding.EncodeToString(make([]byte, 64))} {
		if VerifyIDSig(id.User, "$G", "a", "o", "p", sig) || VerifyIDSig(other, "$G", "a", "o", "p", sig) {
			t.Fatalf("sig %q verifies", sig)
		}
	}
}

// For every ordinary header the new classifier gives the oracle's class and claimed header.
func TestVerifyStoredMatchesOldImplementation(t *testing.T) {
	k := newCompatKit(t)
	const sub, pay = "agents.mail.cc.o.n", `{"text":"hello"}`
	ok := k.signed(sub, pay, "claude-code")
	for _, c := range []struct{ name, hdr, subject, payload string }{
		{"valid", ok, sub, pay},
		{"tampered payload", ok, sub, "other"},
		{"wrong arrival subject", ok, "agents.mail.zz.o.n", pay},
		{"no header", "", sub, pay},
		{"claimed", `{"v":1,"account":"$G","user":"` + k.id.User + `","name":"x"}`, sub, pay},
		{"unknown v", `{"v":2,"account":"$G"}`, sub, pay},
		{"not json", "x", sub, pay},
	} {
		t.Run(c.name, func(t *testing.T) {
			wh, wt := oldVerifyStored(c.hdr, c.subject, []byte(c.payload))
			gh, gt := VerifyStored(c.hdr, c.subject, []byte(c.payload))
			if gh != wh || gt != wt {
				t.Fatalf("new (%+v, %s), old (%+v, %s)", gh, gt, wh, wt)
			}
		})
	}
}
