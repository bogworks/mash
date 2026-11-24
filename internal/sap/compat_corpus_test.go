package sap

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
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
		{name: "NKEY-shaped account with bad CRC", hdr: members(k.base(badCRC(acct), subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: TrustMalformed, expectSDK: TrustMalformed},

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

// compatShapes maps each case that differs to one of the ten shapes the 0.6.0 SDK adoption contract names as refused by
// the SDK and accepted (or labelled differently) by mash. A differing case outside this set is an eleventh shape: a
// contract amendment, so the test fails until it is made one.
var compatShapes = map[string]string{
	"empty header value":                      "empty header value",
	"signature valid base64url, wrong length": "sig not base64url of 64 bytes",
	"signature not base64":                    "sig not base64url of 64 bytes",
	"ts with +01:00 offset":                   "ts with an offset",
	"duplicate user member":                   "repeated JSON member",
	"duplicate name member":                   "repeated JSON member",
	"header over 2048 bytes":                  "header over 2048 bytes",
	"name with a control character":           "name with a control character",
	"name of 65 chars":                        "name over 64 UTF-16 units",
	"name with an unpaired surrogate escape":  "name with a lone surrogate escape",
	"sub with a space":                        "sub with a space",
	"two Agent-Sender values":                 "two Agent-Sender values",
}

var compatTenShapes = []string{
	"header over 2048 bytes", "ts with an offset", "name with a control character", "name over 64 UTF-16 units",
	"name with a lone surrogate escape", "repeated JSON member", "sub with a space", "sig not base64url of 64 bytes",
	"empty header value", "two Agent-Sender values",
}

// TestCompatCorpusMashVerdicts is the mash side alone: every case's stored label from mash's verifier equals expectMash,
// which is plain data, so a later test of a strict adapter can use expectMash without calling this verifier.
func TestCompatCorpusMashVerdicts(t *testing.T) {
	k := newCompatKit(t)
	cases := k.cases()
	for _, want := range []string{"no header", "empty header value", "two Agent-Sender values"} {
		found := false
		for _, c := range cases {
			found = found || c.name == want
		}
		if !found {
			t.Fatalf("corpus lacks the case %q", want)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr, _ := compatMessage(c)
			var h nats.Header = compatHeader(c)
			// mash reads the first value of the header, as its callers do.
			if v := h.Get(HeaderName); !c.noHdr && v != hdr {
				t.Fatalf("first header value %q is not the case's", v)
			}
			if _, got := VerifyStored(h.Get(HeaderName), c.subject, []byte(c.payload)); got != c.expectMash {
				t.Errorf("mash %s, expected %s", got, c.expectMash)
			}
		})
	}
}

func compatHeader(c compatCase) nats.Header {
	_, h := compatMessage(c)
	return h
}

// TestCompatCorpusSDKVerdicts runs the SDK's stored verifier over the corpus: it must equal expectSDK, and where the
// two sides differ the case carries a why and one of the ten contract shapes.
func TestCompatCorpusSDKVerdicts(t *testing.T) {
	k := newCompatKit(t)
	seen := map[string]bool{}
	for _, c := range k.cases() {
		t.Run(c.name, func(t *testing.T) {
			s, err := identity.VerifyStored(identity.Message{Subject: c.subject, Data: []byte(c.payload), Header: compatHeader(c)}, identity.StoredOptions{})
			sdk := sdkLabel(t, s, err)
			if sdk != c.expectSDK {
				t.Errorf("SDK %s, expected %s (err %v)", sdk, c.expectSDK, err)
			}
			if c.expectMash != c.expectSDK {
				if c.why == "" {
					t.Errorf("mash %s and SDK %s differ with no why", c.expectMash, c.expectSDK)
				}
				shape, ok := compatShapes[c.name]
				if !ok {
					t.Errorf("a differing case outside the contract's ten shapes (an eleventh: a contract amendment)")
				}
				seen[shape] = true
			} else if c.why != "" || compatShapes[c.name] != "" {
				t.Errorf("why or shape is set but both say %s", c.expectMash)
			}
		})
	}
	if t.Failed() {
		return
	}
	for _, shape := range compatTenShapes {
		if !seen[shape] {
			t.Errorf("none of the corpus cases covers the contract shape %q", shape)
		}
	}
	if len(seen) != len(compatTenShapes) {
		t.Errorf("%d shapes differ, the contract names %d", len(seen), len(compatTenShapes))
	}
}

// TestCompatCorpusStored is the two verifiers side by side, live from the same inputs: mash against expectMash, the
// SDK against expectSDK, and a difference needs a why. It fails if either side moves.
func TestCompatCorpusStored(t *testing.T) {
	k := newCompatKit(t)
	for _, c := range k.cases() {
		t.Run(c.name, func(t *testing.T) {
			nh := compatHeader(c)
			_, mash := VerifyStored(nh.Get(HeaderName), c.subject, []byte(c.payload))
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
	for sec := -61; sec <= 61; sec++ {
		skew := time.Duration(sec) * time.Second
		t.Run(skew.String(), func(t *testing.T) {
			m, s, err := live(skew)
			want := TrustForged
			if sec >= -30 && sec <= 30 { // mash LiveSkew is 30 s on each side, inclusive
				want = TrustVerified
			}
			if m != want || s != want {
				t.Fatalf("mash %s, SDK %s, want both %s, err %v", m, s, want, err)
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

// TestNonceSetUnbounded: mash's NonceSet has no size cap and no per-sender quota, so every distinct nonce from one
// sender is accepted and kept for the window. The SDK's set caps one sender at DefaultMaxNoncesPerSender (10 000) and
// the whole set at DefaultMaxNonces, and refuses the next pair. The difference is policy, not a verdict on any one
// message, and is not among the ten stored-header shapes.
func TestNonceSetUnbounded(t *testing.T) {
	const n = identity.DefaultMaxNoncesPerSender + 2_000
	now := compatNow
	ms := NewNonceSet(60 * time.Second)
	for i := 0; i < n; i++ {
		if ms.Seen("UONESENDER", "nonce-"+strconv.Itoa(i), now) {
			t.Fatalf("mash set called distinct nonce %d a replay", i)
		}
	}
	if got := len(ms.(*nonceSet).at); got != n {
		t.Fatalf("mash set holds %d pairs after %d distinct nonces, want all", got, n)
	}
	if !ms.Seen("UONESENDER", "nonce-0", now) {
		t.Fatal("mash set forgot a nonce inside its window")
	}

	ss := identity.NewNonceSet(identity.NonceSetOptions{Now: func() time.Time { return now }})
	var last identity.RecordResult
	for i := 0; i < n; i++ {
		last = ss.Record("UONESENDER", "nonce-"+strconv.Itoa(i), now)
		if i < identity.DefaultMaxNoncesPerSender && last != identity.Recorded {
			t.Fatalf("SDK set refused nonce %d under its quota: %v", i, last)
		}
		if i == identity.DefaultMaxNoncesPerSender && last != identity.SenderQuotaExceeded {
			t.Fatalf("SDK set did not stop one sender at its quota: %v", last)
		}
	}
	if ss.Len() != identity.DefaultMaxNoncesPerSender {
		t.Fatalf("SDK set holds %d, want the quota %d", ss.Len(), identity.DefaultMaxNoncesPerSender)
	}
}

// badCRC changes one character in the body of an account key, always to a different one, so the checksum fails.
func badCRC(key string) string {
	c := byte('A')
	if key[10] == c {
		c = 'B'
	}
	return key[:10] + string(c) + key[11:]
}
