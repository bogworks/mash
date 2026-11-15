package sap

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/synadia-agents.go/identity"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Compatibility gate: mash's sender-header classifier (sender.go) against the SAP Go SDK's (identity, v0.1.0), on every
// header shape mash can have stored. Each case names the label each classifier gives today; a case where they differ
// carries a `why`. The test fails if either classifier moves off its expectation, or if the two differ and no `why`
// says so, so a later swap of the implementation cannot change a label unnoticed.
//
// Label mapping for the SDK: (nil, nil) is absent, a *Sender is its Trust, an *identity.Error with code 400 is
// malformed and with code 401 is forged. mash's unknown-signer and stale are directory refinements made later, not by
// the classifier, and never occur here.
//
// DIFFERENCES FOUND (mash label / SDK label). The signature in every "valid sig" row verifies, so each is a
// strictness difference in the SDK's parser, not a disagreement about who signed:
//
//   - header over 2048 bytes: mash verified / SDK malformed (no size cap in mash).
//   - ts with a +01:00 offset: mash verified / SDK malformed (the SDK wants UTC ending in Z; fractional seconds
//     with Z are accepted by both).
//   - name with a control character: mash verified / SDK malformed.
//   - name of 65 characters: mash verified / SDK malformed (limit 64 UTF-16 units).
//   - name with an unpaired surrogate escape: mash verified / SDK malformed.
//   - repeated JSON member name (two "user", two "name"): mash takes the last and verifies / SDK malformed.
//   - sig that is not base64url of 64 bytes: mash forged / SDK malformed (mash decodes inside the signature check).
//   - sub with a space in it (not a NATS subject shape), validly signed: mash verified / SDK malformed.
//   - an Agent-Sender header present with an empty value: mash absent / SDK malformed.
//   - more than one Agent-Sender header value: mash reads the first value / SDK malformed.
//
// Everything else agrees, including a wrong arrival subject (forged), a tampered payload (forged), an unknown v
// (absent), a string v (malformed), an account of "$G" or a name (verified), and extra unknown members (verified).
// None of the differences is a mash leniency over an invalid signature: where mash says verified and the SDK does not,
// the signature is valid and only the header's form is outside the SDK's stricter rules. They are the open decision
// for adopting the SDK's parser: after a swap those stored headers would read malformed instead of verified.

type compatCase struct {
	name       string
	hdr        string // the Agent-Sender value; empty with noHdr false is an empty value
	noHdr      bool   // no Agent-Sender header at all
	extraHdr   string // a second Agent-Sender value
	subject    string // arrival subject
	payload    string
	expectMash Trust
	expectSDK  Trust
	why        string
}

type compatKit struct {
	t       *testing.T
	id      Identity
	account string
}

func newCompatKit(t *testing.T) compatKit {
	t.Helper()
	acc, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	apub, _ := acc.PublicKey()
	usr, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := usr.Seed()
	id, err := NewIdentity(seed, apub)
	if err != nil {
		t.Fatal(err)
	}
	return compatKit{t: t, id: id, account: apub}
}

var compatNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// signed is a header from mash's own signer.
func (k compatKit) signed(subject, payload, name string) string {
	k.t.Helper()
	h, err := k.id.Sign(subject, []byte(payload), name, compatNow, "nonce-1")
	if err != nil {
		k.t.Fatal(err)
	}
	return h.Encode()
}

// sig signs the given fields as the SAP does, for headers mash's Sign would not produce.
func (k compatKit) sig(account, sub, ts, nonce, payload string) string {
	k.t.Helper()
	b, err := k.id.kp.Sign(signedInput(account, k.id.User, sub, ts, nonce, []byte(payload)))
	if err != nil {
		k.t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

// members renders a JSON object from raw members in the given order.
func members(ms ...string) string { return "{" + strings.Join(ms, ",") + "}" }

func (k compatKit) base(account, sub, ts, nonce, payload string) []string {
	return []string{`"v":1`, `"account":` + q(account), `"user":` + q(k.id.User), `"sub":` + q(sub),
		`"ts":` + q(ts), `"nonce":` + q(nonce), `"sig":` + q(k.sig(account, sub, ts, nonce, payload))}
}

func (k compatKit) cases() []compatCase {
	const (
		subCC  = "agents.mail.cc.o.n"
		sub3   = "agents.mail.a.o.n"
		pay    = `{"text":"hello"}`
		ts     = "2026-11-15T12:00:00Z"
		nonce  = "nonce-1"
		strict = "SDK parser is stricter: "
	)
	acct := k.account
	std := func(extra ...string) string { return members(append(k.base(acct, subCC, ts, nonce, pay), extra...)...) }
	// withName inserts name before sub, as mash's wire order does; JSON member order does not matter to either side.
	withName := func(nameMember string) string {
		return members(append([]string{`"v":1`, `"account":` + q(acct), `"user":` + q(k.id.User), nameMember},
			k.base(acct, subCC, ts, nonce, pay)[3:]...)...)
	}
	unsigned := func(ms ...string) string {
		return members(append([]string{`"v":1`, `"account":` + q(acct), `"user":` + q(k.id.User)}, ms...)...)
	}
	vv := Trust("verified")
	c := []compatCase{
		{name: "signed name claude-code", hdr: k.signed(subCC, pay, "claude-code"), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed name with spaces and unicode", hdr: k.signed(subCC, pay, "Zoë the agent 日本"), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed no name", hdr: k.signed(subCC, pay, ""), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed three-token agent subject", hdr: k.signed(sub3, pay, "a-o-n"), subject: sub3, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed empty payload", hdr: k.signed(subCC, "", "x"), subject: subCC, payload: "", expectMash: vv, expectSDK: vv},
		{name: "account $G", hdr: members(k.base("$G", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "account name form", hdr: members(k.base("acme-corp_1", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "extra unknown members", hdr: std(`"future":{"a":[1,2]}`, `"x":"y"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},

		{name: "no header", noHdr: true, subject: subCC, payload: pay, expectMash: TrustAbsent, expectSDK: TrustAbsent},
		{name: "empty header value", hdr: "", subject: subCC, payload: pay, expectMash: TrustAbsent, expectSDK: TrustMalformed,
			why: "an Agent-Sender header present with an empty value: mash reads it as no header, the SDK as not a JSON object"},
		{name: "claimed, no sig", hdr: unsigned(`"name":"x"`), subject: subCC, payload: pay, expectMash: TrustClaimed, expectSDK: TrustClaimed},
		{name: "unknown v 2", hdr: strings.Replace(std(), `"v":1`, `"v":2`, 1), subject: subCC, payload: pay, expectMash: TrustAbsent, expectSDK: TrustAbsent},
		{name: "v as a string", hdr: strings.Replace(std(), `"v":1`, `"v":"1"`, 1), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "v missing", hdr: strings.Replace(std(), `"v":1,`, ``, 1), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "not JSON", hdr: "not json", subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "JSON array", hdr: "[1]", subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "account missing", hdr: members(`"v":1`, `"user":`+q(k.id.User)), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "user missing", hdr: members(`"v":1`, `"account":`+q(acct)), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "bad user key", hdr: strings.Replace(std(), k.id.User, "UNOTAKEY", 1), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "bad account", hdr: members(k.base("A!bad", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "NKEY-shaped account with bad CRC", hdr: members(k.base(acct[:55]+"A", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},

		{name: "tampered payload", hdr: std(), subject: subCC, payload: `{"text":"HELLO"}`, expectMash: TrustForged, expectSDK: TrustForged},
		{name: "wrong sub (signed for another subject)", hdr: members(k.base(acct, "agents.mail.zz.o.n", ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustForged, expectSDK: TrustForged},
		{name: "arrival subject differs from sub", hdr: std(), subject: "agents.mail.other.o.n", payload: pay, expectMash: TrustForged, expectSDK: TrustForged},
		{name: "signature valid base64url, wrong length", hdr: strings.Replace(std(), `"sig":"`, `"sig":"AAAA`, 1), subject: subCC, payload: pay, expectMash: TrustForged, expectSDK: TrustMalformed,
			why: "a sig that is base64url but 4 characters over the 86 of 64 bytes: mash only fails the signature check (forged), the SDK rejects the length first (malformed)"},
		{name: "signature of right form, does not verify", hdr: strings.Replace(std(), k.sig(acct, subCC, ts, nonce, pay), base64.RawURLEncoding.EncodeToString(make([]byte, 64)), 1), subject: subCC, payload: pay, expectMash: TrustForged, expectSDK: TrustForged},
		{name: "signature not base64", hdr: strings.Replace(std(), `"sig":"`, `"sig":"!!`, 1), subject: subCC, payload: pay, expectMash: TrustForged, expectSDK: TrustMalformed,
			why: "sig is not base64url: mash forged (verifySig false), SDK malformed"},
		{name: "wrong signer key", hdr: strings.Replace(std(), k.id.User, otherUser(k.t), 1), subject: subCC, payload: pay, expectMash: TrustForged, expectSDK: TrustForged},
		{name: "sig present, sub missing", hdr: members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User), `"ts":`+q(ts), `"nonce":`+q(nonce), `"sig":`+q(k.sig(acct, "", ts, nonce, pay))), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "empty sub with sig", hdr: members(k.base(acct, "", ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "sig present, nonce missing", hdr: members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User), `"sub":`+q(subCC), `"ts":`+q(ts), `"sig":`+q(k.sig(acct, subCC, ts, "", pay))), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "nonce with a bad character", hdr: members(k.base(acct, subCC, ts, "bad nonce!", pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},
		{name: "nonce 64 chars", hdr: members(k.base(acct, subCC, ts, strings.Repeat("n", 64), pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "nonce 65 chars", hdr: members(k.base(acct, subCC, ts, strings.Repeat("n", 65), pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},

		{name: "ts with fractional seconds and Z", hdr: members(k.base(acct, subCC, "2026-11-15T12:00:00.123Z", nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "ts with +01:00 offset", hdr: members(k.base(acct, subCC, "2026-11-15T13:00:00+01:00", nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "ts must be UTC with Z; mash accepts any offset"},
		{name: "ts not RFC3339", hdr: members(k.base(acct, subCC, "yesterday", nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},

		{name: "duplicate user member", hdr: strings.Replace(std(), `"user":`, `"user":"UXXX","user":`, 1), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "a repeated member name is malformed; mash's map decode keeps the last"},
		{name: "duplicate name member", hdr: withName(`"name":"a","name":"b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "a repeated member name is malformed; mash's map decode keeps the last"},
		{name: "header over 2048 bytes", hdr: std(`"pad":` + q(strings.Repeat("p", 2100))), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "value over 2048 bytes is malformed; mash has no size cap"},
		{name: "name with a control character", hdr: withName(`"name":"a\u0001b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "control characters are not allowed in name"},
		{name: "name of 64 chars", hdr: withName(`"name":` + q(strings.Repeat("n", 64))), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "name of 65 chars", hdr: withName(`"name":` + q(strings.Repeat("n", 65))), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "name over 64 UTF-16 units"},
		{name: "name with an unpaired surrogate escape", hdr: withName(`"name":"a\ud800b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "a lone surrogate escape in name"},
		{name: "sub with a space", hdr: members(k.base(acct, "agents.mail.a b.o.n", ts, nonce, pay)...), subject: "agents.mail.a b.o.n", payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: strict + "sub must have the shape of a NATS subject (no whitespace)"},
		{name: "two Agent-Sender values", hdr: k.signed(subCC, pay, "x"), extraHdr: k.signed(subCC, pay, "y"), subject: subCC, payload: pay, expectMash: vv, expectSDK: TrustMalformed,
			why: "mash reads the first value of the header; the SDK refuses more than one value"},
	}
	return c
}

func otherUser(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := kp.PublicKey()
	return p
}

// sdkLabel maps the SDK's (sender, error) to the shared label set.
func sdkLabel(t *testing.T, s *identity.Sender, err error) Trust {
	t.Helper()
	if err != nil {
		var e *identity.Error
		if !errors.As(err, &e) {
			t.Fatalf("SDK returned a non-classification error: %v", err)
		}
		switch e.Code {
		case 400:
			return TrustMalformed
		case 401:
			return TrustForged
		}
		t.Fatalf("SDK error code %d: %v", e.Code, err)
	}
	return Trust(identity.TrustOf(s))
}

func compatMessage(c compatCase) (string, nats.Header) {
	h := nats.Header{}
	if !c.noHdr {
		h[HeaderName] = []string{c.hdr}
		if c.extraHdr != "" {
			h[HeaderName] = append(h[HeaderName], c.extraHdr)
		}
	}
	return c.hdr, h
}

func TestCompatCorpusStored(t *testing.T) {
	k := newCompatKit(t)
	for _, c := range k.cases() {
		t.Run(c.name, func(t *testing.T) {
			hdr, nh := compatMessage(c)
			_, mash := VerifyStored(hdr, c.subject, []byte(c.payload))
			s, err := identity.VerifyStored(identity.Message{Subject: c.subject, Data: []byte(c.payload), Header: nh}, identity.StoredOptions{})
			sdk := sdkLabel(t, s, err)
			if mash != c.expectMash {
				t.Errorf("mash %s, expected %s", mash, c.expectMash)
			}
			if sdk != c.expectSDK {
				t.Errorf("SDK %s, expected %s (err %v)", sdk, c.expectSDK, err)
			}
			if mash != sdk && c.why == "" {
				t.Errorf("mash %s and SDK %s differ with no why", mash, sdk)
			}
			if mash == sdk && c.why != "" {
				t.Errorf("why is set but both say %s", mash)
			}
		})
	}
}

// compatSDKNonces is a nonce set configured as the SDK defaults: ReplayWindow and the set's Window both left zero, so
// both are identity.DefaultReplayWindow (30 s), the same skew as mash's LiveSkew. The SDK needs Window >= ReplayWindow.
func compatSDKNonces(now time.Time) *identity.NonceSet {
	return identity.NewNonceSet(identity.NonceSetOptions{Now: func() time.Time { return now }})
}

// Live: the same signed message through each classifier, at several clock offsets and with a replay.
func TestCompatCorpusLive(t *testing.T) {
	k := newCompatKit(t)
	const subj, pay = "agents.mail.cc.o.n", `{"text":"hi"}`
	hdr := k.signed(subj, pay, "live")
	live := func(skew time.Duration) (Trust, Trust, error) { // fresh sets, so each skew is judged alone
		now := compatNow.Add(skew)
		_, m := VerifyLive(hdr, subj, []byte(pay), now, NewNonceSet(60*time.Second))
		s, err := identity.VerifyLive(identity.Message{Subject: subj, Data: []byte(pay), Header: nats.Header{HeaderName: {hdr}}},
			identity.LiveOptions{Nonces: compatSDKNonces(now), Now: func() time.Time { return now }})
		return m, sdkLabel(t, s, err), err
	}
	for _, c := range []struct {
		name       string
		skew       time.Duration
		mash, sdk  Trust
		windowNote string
	}{
		{"fresh", 0, TrustVerified, TrustVerified, ""},
		{"skew 29 s", 29 * time.Second, TrustVerified, TrustVerified, ""},
		{"skew 30 s", 30 * time.Second, TrustVerified, TrustVerified, ""},
		{"skew 31 s", 31 * time.Second, TrustForged, TrustForged, ""},
		{"skew -29 s", -29 * time.Second, TrustVerified, TrustVerified, ""},
		{"skew -30 s", -30 * time.Second, TrustVerified, TrustVerified, ""},
		{"skew -31 s", -31 * time.Second, TrustForged, TrustForged, ""},
		{"skew 61 s", 61 * time.Second, TrustForged, TrustForged, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, s, err := live(c.skew)
			if m != c.mash || s != c.sdk {
				t.Fatalf("mash %s (want %s), SDK %s (want %s), err %v", m, c.mash, s, c.sdk, err)
			}
			if m != s && c.windowNote == "" {
				t.Fatalf("mash %s and SDK %s differ with no note", m, s)
			}
		})
	}
	t.Run("replayed nonce", func(t *testing.T) {
		now := compatNow
		mset := NewNonceSet(60 * time.Second)
		sset := compatSDKNonces(now)
		msg := identity.Message{Subject: subj, Data: []byte(pay), Header: nats.Header{HeaderName: {hdr}}}
		opts := identity.LiveOptions{Nonces: sset, Now: func() time.Time { return now }}
		for i, want := range []Trust{TrustVerified, TrustForged} {
			_, m := VerifyLive(hdr, subj, []byte(pay), now, mset)
			s, err := identity.VerifyLive(msg, opts)
			if sd := sdkLabel(t, s, err); m != want || sd != want {
				t.Fatalf("delivery %d: mash %s, SDK %s, want both %s", i+1, m, sd, want)
			}
		}
	})
}
