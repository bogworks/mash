package sapsdk

import (
	"errors"
	"io"
	"log"
	"os"
	"strconv"

	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/nats-io/jwt/v2"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
)

// The oracle. Every fixture of internal/sap's compatibility corpus (compat_corpus_test.go; the case table below is a
// copy of its builder, because the corpus lives in a _test.go file of another package and nothing is exported from
// non-test code) has two columns:
//
//   - old: expectMash, the verdict of mash's lenient parser, as data. It is asserted on the old code only
//     (TestOldColumnOnTheOldCode), never on this package.
//   - strict: expectSDK, the verdict of the SDK's parser, plus the class this package gives. It is asserted on this
//     package's adapter (TestStrictColumn). The ten shapes are the cases where the two columns differ.
//
// The missing-header control ("no header") is absent in both columns.

type compatCase struct {
	name       string
	hdr        string // the Agent-Sender value; empty with noHdr false is an empty value
	noHdr      bool   // no Agent-Sender header at all
	extraHdr   string // a second Agent-Sender value
	subject    string // arrival subject
	payload    string
	expectMash sap.Trust // old column
	expectSDK  sap.Trust // strict column
	why        string
}

// values is every Agent-Sender value of the case's message, as a caller of the adapter passes them.
func (c compatCase) values() []string {
	if c.noHdr {
		return nil
	}
	v := []string{c.hdr}
	if c.extraHdr != "" {
		v = append(v, c.extraHdr)
	}
	return v
}

type compatKit struct {
	t       *testing.T
	kp      nkeys.KeyPair
	old     sap.Identity // the oracle
	id      Identity     // the adapter's
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
	old, err := sap.NewIdentity(seed, apub)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewIdentity(seed, apub)
	if err != nil {
		t.Fatal(err)
	}
	return compatKit{t: t, kp: usr, old: old, id: id, account: apub}
}

var compatNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// oldSignedInput is §13.6.1, written out here so the fixtures do not depend on the code under test.
func oldSignedInput(account, user, subject, ts, nonce string, payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return []byte("AGENT-SENDER-V1\n" + account + "\n" + user + "\n" + subject + "\n" +
		ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:]) + "\n")
}

// signed is a header from the OLD signer, so the fixtures are what a released mash writes.
func (k compatKit) signed(subject, payload, name string) string {
	k.t.Helper()
	h, err := k.old.Sign(subject, []byte(payload), name, compatNow, "nonce-1")
	if err != nil {
		k.t.Fatal(err)
	}
	return h.Encode()
}

// sig signs the given fields as the SAP does, for headers the signer would not produce.
func (k compatKit) sig(account, sub, ts, nonce, payload string) string {
	k.t.Helper()
	b, err := k.kp.Sign(oldSignedInput(account, k.old.User, sub, ts, nonce, []byte(payload)))
	if err != nil {
		k.t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

// members renders a JSON object from raw members in the given order.
func members(ms ...string) string { return "{" + strings.Join(ms, ",") + "}" }

func (k compatKit) base(account, sub, ts, nonce, payload string) []string {
	return []string{`"v":1`, `"account":` + q(account), `"user":` + q(k.old.User), `"sub":` + q(sub),
		`"ts":` + q(ts), `"nonce":` + q(nonce), `"sig":` + q(k.sig(account, sub, ts, nonce, payload))}
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

// badCRC changes one character in the body of an account key, always to a different one, so the checksum fails.
func badCRC(key string) string {
	c := byte('A')
	if key[10] == c {
		c = 'B'
	}
	return key[:10] + string(c) + key[11:]
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
	vv := sap.Trust("verified")
	c := []compatCase{
		{name: "signed name claude-code", hdr: k.signed(subCC, pay, "claude-code"), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed name with spaces and unicode", hdr: k.signed(subCC, pay, "Zoë the agent 日本"), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed no name", hdr: k.signed(subCC, pay, ""), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed three-token agent subject", hdr: k.signed(sub3, pay, "a-o-n"), subject: sub3, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "signed empty payload", hdr: k.signed(subCC, "", "x"), subject: subCC, payload: "", expectMash: vv, expectSDK: vv},
		{name: "account $G", hdr: members(k.base("$G", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "account name form", hdr: members(k.base("acme-corp_1", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "extra unknown members", hdr: std(`"future":{"a":[1,2]}`, `"x":"y"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},

		{name: "no header", noHdr: true, subject: subCC, payload: pay, expectMash: sap.TrustAbsent, expectSDK: sap.TrustAbsent},
		{name: "empty header value", hdr: "", subject: subCC, payload: pay, expectMash: sap.TrustAbsent, expectSDK: sap.TrustMalformed,
			why: "an Agent-Sender header present with an empty value: mash reads it as no header, the SDK as not a JSON object"},
		{name: "claimed, no sig", hdr: unsigned(`"name":"x"`), subject: subCC, payload: pay, expectMash: sap.TrustClaimed, expectSDK: sap.TrustClaimed},
		{name: "unknown v 2", hdr: strings.Replace(std(), `"v":1`, `"v":2`, 1), subject: subCC, payload: pay, expectMash: sap.TrustAbsent, expectSDK: sap.TrustAbsent},
		{name: "v as a string", hdr: strings.Replace(std(), `"v":1`, `"v":"1"`, 1), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "v missing", hdr: strings.Replace(std(), `"v":1,`, ``, 1), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "not JSON", hdr: "not json", subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "JSON array", hdr: "[1]", subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "account missing", hdr: members(`"v":1`, `"user":`+q(k.id.User)), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "user missing", hdr: members(`"v":1`, `"account":`+q(acct)), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "bad user key", hdr: strings.Replace(std(), k.id.User, "UNOTAKEY", 1), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "bad account", hdr: members(k.base("A!bad", subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "NKEY-shaped account with bad CRC", hdr: members(k.base(badCRC(acct), subCC, ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},

		{name: "tampered payload", hdr: std(), subject: subCC, payload: `{"text":"HELLO"}`, expectMash: sap.TrustForged, expectSDK: sap.TrustForged},
		{name: "wrong sub (signed for another subject)", hdr: members(k.base(acct, "agents.mail.zz.o.n", ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustForged},
		{name: "arrival subject differs from sub", hdr: std(), subject: "agents.mail.other.o.n", payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustForged},
		{name: "signature valid base64url, wrong length", hdr: strings.Replace(std(), `"sig":"`, `"sig":"AAAA`, 1), subject: subCC, payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustMalformed,
			why: "a sig that is base64url but 4 characters over the 86 of 64 bytes: mash only fails the signature check (forged), the SDK rejects the length first (malformed)"},
		{name: "signature of right form, does not verify", hdr: strings.Replace(std(), k.sig(acct, subCC, ts, nonce, pay), base64.RawURLEncoding.EncodeToString(make([]byte, 64)), 1), subject: subCC, payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustForged},
		{name: "signature not base64", hdr: strings.Replace(std(), `"sig":"`, `"sig":"!!`, 1), subject: subCC, payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustMalformed,
			why: "sig is not base64url: mash forged (verifySig false), SDK malformed"},
		{name: "wrong signer key", hdr: strings.Replace(std(), k.id.User, otherUser(k.t), 1), subject: subCC, payload: pay, expectMash: sap.TrustForged, expectSDK: sap.TrustForged},
		{name: "sig present, sub missing", hdr: members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User), `"ts":`+q(ts), `"nonce":`+q(nonce), `"sig":`+q(k.sig(acct, "", ts, nonce, pay))), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "empty sub with sig", hdr: members(k.base(acct, "", ts, nonce, pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "sig present, nonce missing", hdr: members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User), `"sub":`+q(subCC), `"ts":`+q(ts), `"sig":`+q(k.sig(acct, subCC, ts, "", pay))), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "nonce with a bad character", hdr: members(k.base(acct, subCC, ts, "bad nonce!", pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},
		{name: "nonce 64 chars", hdr: members(k.base(acct, subCC, ts, strings.Repeat("n", 64), pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "nonce 65 chars", hdr: members(k.base(acct, subCC, ts, strings.Repeat("n", 65), pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},

		{name: "ts with fractional seconds and Z", hdr: members(k.base(acct, subCC, "2026-11-15T12:00:00.123Z", nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "ts with +01:00 offset", hdr: members(k.base(acct, subCC, "2026-11-15T13:00:00+01:00", nonce, pay)...), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "ts must be UTC with Z; mash accepts any offset"},
		{name: "ts not RFC3339", hdr: members(k.base(acct, subCC, "yesterday", nonce, pay)...), subject: subCC, payload: pay, expectMash: sap.TrustMalformed, expectSDK: sap.TrustMalformed},

		{name: "duplicate user member", hdr: strings.Replace(std(), `"user":`, `"user":"UXXX","user":`, 1), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "a repeated member name is malformed; mash's map decode keeps the last"},
		{name: "duplicate name member", hdr: withName(`"name":"a","name":"b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "a repeated member name is malformed; mash's map decode keeps the last"},
		{name: "header over 2048 bytes", hdr: std(`"pad":` + q(strings.Repeat("p", 2100))), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "value over 2048 bytes is malformed; mash has no size cap"},
		{name: "name with a control character", hdr: withName(`"name":"a\u0001b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "control characters are not allowed in name"},
		{name: "name of 64 chars", hdr: withName(`"name":` + q(strings.Repeat("n", 64))), subject: subCC, payload: pay, expectMash: vv, expectSDK: vv},
		{name: "name of 65 chars", hdr: withName(`"name":` + q(strings.Repeat("n", 65))), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "name over 64 UTF-16 units"},
		{name: "name with an unpaired surrogate escape", hdr: withName(`"name":"a\ud800b"`), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "a lone surrogate escape in name"},
		{name: "sub with a space", hdr: members(k.base(acct, "agents.mail.a b.o.n", ts, nonce, pay)...), subject: "agents.mail.a b.o.n", payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: strict + "sub must have the shape of a NATS subject (no whitespace)"},
		{name: "two Agent-Sender values", hdr: k.signed(subCC, pay, "x"), extraHdr: k.signed(subCC, pay, "y"), subject: subCC, payload: pay, expectMash: vv, expectSDK: sap.TrustMalformed,
			why: "mash reads the first value of the header; the SDK refuses more than one value"},
	}
	return c
}

// shapeClass maps each case where the old and the strict column differ to the class of the ten shapes it is. A
// differing case outside this map is an eleventh shape: a contract amendment, so the test fails until it is made one.
var shapeClass = map[string]string{
	"empty header value":                      "empty sender header",
	"signature valid base64url, wrong length": "signature unreadable",
	"signature not base64":                    "signature unreadable",
	"ts with +01:00 offset":                   "time not in UTC",
	"duplicate user member":                   "a field appears twice",
	"duplicate name member":                   "a field appears twice",
	"header over 2048 bytes":                  "header too long",
	"name with a control character":           "control character in name",
	"name of 65 chars":                        "name too long",
	"name with an unpaired surrogate escape":  "broken character in name",
	"sub with a space":                        "space in subject",
	"two Agent-Sender values":                 "two sender headers",
}

// tenClasses are the ten shapes' class strings, frozen by the contract (REQ-017). Spelled out, not taken from the
// constants, so a reworded constant fails here.
var tenClasses = []string{"header too long", "time not in UTC", "control character in name", "name too long",
	"broken character in name", "a field appears twice", "space in subject", "signature unreadable",
	"empty sender header", "two sender headers"}

// otherClass is the class of each fixture that both columns call malformed (the cases mash refuses today).
var otherClass = map[string]string{
	"v as a string":                    "version is not a number",
	"v missing":                        "version missing",
	"not JSON":                         "header is not a JSON object",
	"JSON array":                       "header is not a JSON object",
	"account missing":                  "account or user not valid",
	"user missing":                     "account or user not valid",
	"bad user key":                     "account or user not valid",
	"bad account":                      "account or user not valid",
	"NKEY-shaped account with bad CRC": "account or user not valid",
	"sig present, sub missing":         "signed header incomplete",
	"empty sub with sig":               "signed header incomplete",
	"sig present, nonce missing":       "signed header incomplete",
	"nonce with a bad character":       "message id not valid",
	"nonce 65 chars":                   "message id not valid",
	"ts not RFC3339":                   "time not valid",
}

// wantClass is the strict column's class for a case.
func wantClass(c compatCase) string {
	if s, ok := shapeClass[c.name]; ok {
		return s
	}
	if o, ok := otherClass[c.name]; ok {
		return o
	}
	if c.expectSDK == sap.TrustVerified {
		return ""
	}
	return "not verified"
}

func TestCorpusIsTheWholeCorpus(t *testing.T) {
	if n := len(newCompatKit(t).cases()); n != 46 {
		t.Fatalf("%d cases, the corpus has 46", n)
	}
}

// TestOldColumnOnTheOldCode asserts the old column on the old verifier only, so the data is known to be the old verdicts.
func TestOldColumnOnTheOldCode(t *testing.T) {
	for _, c := range newCompatKit(t).cases() {
		t.Run(c.name, func(t *testing.T) {
			first := ""
			if v := c.values(); len(v) > 0 {
				first = v[0] // mash read the first value of the header
			}
			if _, got := sap.VerifyStored(first, c.subject, []byte(c.payload)); got != c.expectMash {
				t.Errorf("old verifier %s, old column %s", got, c.expectMash)
			}
		})
	}
}

// TestStrictColumn drives every fixture through the adapter. The strict column equals the SDK's verdict; the ten
// shapes are malformed with their class; the missing-header control is absent.
func TestStrictColumn(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range newCompatKit(t).cases() {
		t.Run(c.name, func(t *testing.T) {
			_, trust := VerifyStored(c.values(), c.subject, []byte(c.payload))
			if trust != c.expectSDK {
				t.Errorf("adapter trust %s, strict column %s", trust, c.expectSDK)
			}
			ct, class := Classify(c.subject, c.values(), []byte(c.payload))
			if ct != c.expectSDK {
				t.Errorf("Classify trust %s, strict column %s", ct, c.expectSDK)
			}
			if want := wantClass(c); class != want {
				t.Errorf("class %q, want %q", class, want)
			}
			if c.expectMash != c.expectSDK {
				shape, ok := shapeClass[c.name]
				if !ok {
					t.Fatalf("old %s and strict %s differ outside the ten shapes (an eleventh: a contract amendment)", c.expectMash, c.expectSDK)
				}
				if c.why == "" {
					t.Errorf("the columns differ with no why")
				}
				if c.expectSDK != sap.TrustMalformed || class != shape {
					t.Errorf("shape %q must be malformed with its class, got %s %q", shape, c.expectSDK, class)
				}
				covered[shape] = true
			} else if _, ok := shapeClass[c.name]; ok {
				t.Errorf("a shape case on which the columns agree")
			}
		})
	}
	if t.Failed() {
		return
	}
	for _, class := range tenClasses {
		if !covered[class] {
			t.Errorf("no fixture drives the shape %q", class)
		}
	}
}

func TestMissingHeaderControl(t *testing.T) {
	for _, vals := range [][]string{nil, {}} {
		h, trust := VerifyStored(vals, "agents.mail.a.b.c", []byte("x"))
		if trust != sap.TrustAbsent || h != (sap.SenderHeader{}) {
			t.Fatalf("no value: %s %v", trust, h)
		}
	}
	for _, c := range newCompatKit(t).cases() {
		if c.name == "no header" && (c.expectMash != sap.TrustAbsent || c.expectSDK != sap.TrustAbsent) {
			t.Fatalf("the control is absent in both columns, got %s and %s", c.expectMash, c.expectSDK)
		}
	}
	if _, class := Classify("s", []string{""}, nil); class != "empty sender header" {
		t.Fatalf("empty value: %q", class)
	}
	if tr, class := Classify("s", []string{"{}", "{}"}, nil); tr != sap.TrustMalformed || class != "two sender headers" {
		t.Fatalf("two values: %s %q", tr, class)
	}
}

func TestClassCodesAreFrozen(t *testing.T) {
	got := []string{ClassHeaderTooLong, ClassTimeNotUTC, ClassControlChar, ClassNameTooLong, ClassBrokenChar,
		ClassFieldTwice, ClassSpaceInSubject, ClassSigUnreadable, ClassEmpty, ClassTwoHeaders}
	for i := range tenClasses {
		if got[i] != tenClasses[i] {
			t.Errorf("class %d is %q, frozen as %q", i, got[i], tenClasses[i])
		}
	}
	if ClassUnreadable != "unreadable sender header" || ClassNotVerified != "not verified" {
		t.Error("a catch-all class moved")
	}
	// Every class a fixture can take is on the documented list.
	listed := map[string]bool{}
	for _, c := range MalformedClasses {
		listed[c] = true
	}
	for name, c := range otherClass {
		if !listed[c] {
			t.Errorf("%s: class %q is not in MalformedClasses", name, c)
		}
	}
}

// TestClassifyHasNoSideEffect: the same input gives the same answer, nothing is written beside the process (the
// working directory stays empty), nothing is logged and no nonce is recorded anywhere.
func TestClassifyHasNoSideEffect(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var logged strings.Builder
	restore := captureLogs(&logged)
	k := newCompatKit(t)
	for _, c := range k.cases() {
		t1, c1 := Classify(c.subject, c.values(), []byte(c.payload))
		t2, c2 := Classify(c.subject, c.values(), []byte(c.payload))
		if t1 != t2 || c1 != c2 {
			t.Errorf("%s: two calls differ: %s %q, %s %q", c.name, t1, c1, t2, c2)
		}
	}
	// A verified message classifies as verified every time: a stored check records no nonce, so there is no replay.
	h := k.signed("agents.mail.cc.o.n", "p", "x")
	for i := 0; i < 3; i++ {
		if tr, class := Classify("agents.mail.cc.o.n", []string{h}, []byte("p")); tr != sap.TrustVerified || class != "" {
			t.Fatalf("call %d: %s %q", i, tr, class)
		}
	}
	ents, err := readDirNames(dir)
	if err != nil || len(ents) != 0 {
		t.Errorf("the working directory has %v (%v)", ents, err)
	}
	restore()
	if logged.Len() != 0 {
		t.Errorf("logged: %s", logged.String())
	}
}

// TestSignAndIDSigAreByteIdentical: the same inputs through the old signer and the adapter give the same bytes.
func TestSignAndIDSigAreByteIdentical(t *testing.T) {
	k := newCompatKit(t)
	cases := []struct{ subject, payload, name string }{
		{"agents.mail.cc.o.n", `{"text":"hello"}`, "claude-code"},
		{"agents.mail.a.o.n", "", ""},
		{"agents.mail.a.o.n", "binary\x00\xff", "Zoë the agent 日本"},
		{"agents.prompt.a.o.n", strings.Repeat("x", 4096), strings.Repeat("n", 64)},
	}
	for _, now := range []time.Time{compatNow, compatNow.Add(1234567 * time.Nanosecond), compatNow.Add(-time.Hour)} {
		for _, c := range cases {
			want, err := k.old.Sign(c.subject, []byte(c.payload), c.name, now, "nonce-xyz_9")
			if err != nil {
				t.Fatal(err)
			}
			got, err := k.id.Sign(c.subject, []byte(c.payload), c.name, now, "nonce-xyz_9")
			if err != nil {
				t.Fatal(err)
			}
			if got != want || got.Encode() != want.Encode() {
				t.Errorf("header differs:\n old %s\n new %s", want.Encode(), got.Encode())
			}
		}
	}
	// A fresh nonce is the SDK's: a valid one, and different each call.
	a, _ := k.id.Sign("agents.mail.cc.o.n", nil, "x", compatNow, "")
	b, _ := k.id.Sign("agents.mail.cc.o.n", nil, "x", compatNow, "")
	if a.Nonce == "" || a.Nonce == b.Nonce || len(a.Nonce) > 64 {
		t.Errorf("nonces %q %q", a.Nonce, b.Nonce)
	}
	if _, tr := sap.VerifyStored(a.Encode(), "agents.mail.cc.o.n", nil); tr != sap.TrustVerified {
		t.Errorf("the old verifier says %s of a header the adapter signed", tr)
	}
	if _, tr := VerifyStored([]string{a.Encode()}, "agents.mail.cc.o.n", nil); tr != sap.TrustVerified {
		t.Errorf("the adapter says %s of its own header", tr)
	}

	for _, c := range []struct{ agent, owner, subject string }{
		{"claude-code", "acme", "agents.prompt.acme.ops.alpha"}, {"", "", ""}, {"日本", "o", "s.t"},
	} {
		want, err := k.old.IDSig(c.agent, c.owner, c.subject)
		if err != nil {
			t.Fatal(err)
		}
		got, err := k.id.IDSig(c.agent, c.owner, c.subject)
		if err != nil || got != want {
			t.Fatalf("id_sig %q, old %q (%v)", got, want, err)
		}
		if !VerifyIDSig(k.id.User, k.account, c.agent, c.owner, c.subject, got) ||
			!sap.VerifyIDSig(k.id.User, k.account, c.agent, c.owner, c.subject, got) {
			t.Error("id_sig does not verify on both sides")
		}
		// the verdicts agree on a bad one, too
		other := otherUser(t)
		for _, bad := range []struct{ user, acct, agent, sig string }{
			{other, k.account, c.agent, got}, {k.id.User, k.account, c.agent + "x", got},
			{k.id.User, k.account + "x", c.agent, got}, {k.id.User, k.account, c.agent, got[:80]}, {k.id.User, k.account, c.agent, "!!"},
		} {
			o := sap.VerifyIDSig(bad.user, bad.acct, bad.agent, c.owner, c.subject, bad.sig)
			n := VerifyIDSig(bad.user, bad.acct, bad.agent, c.owner, c.subject, bad.sig)
			if o || n {
				t.Errorf("a bad id_sig verifies: old %v new %v (%+v)", o, n, bad)
			}
		}
	}
	if k.id.ID() != k.old.ID() {
		t.Error("ID differs")
	}
}

func TestIdentityFromCredsMatchesTheOld(t *testing.T) {
	k := newCompatKit(t)
	seed, _ := k.kp.Seed()
	creds := credsFor(t, k.kp, k.account)
	old, err := sap.IdentityFromCredsAt("x.creds", creds)
	if err != nil {
		t.Fatal(err)
	}
	got, err := IdentityFromCredsAt("x.creds", creds)
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != old.Account || got.User != old.User || got.ID() != old.ID() {
		t.Fatalf("%v vs %v", got, old)
	}
	want, _ := old.Sign("agents.mail.cc.o.n", nil, "x", compatNow, "n1")
	have, err := got.Sign("agents.mail.cc.o.n", nil, "x", compatNow, "n1")
	if err != nil || have != want {
		t.Fatalf("%v %v", have, err)
	}
	if _, err := IdentityFromCredsAt("x.creds", []byte("junk")); err == nil {
		t.Fatal("junk creds accepted")
	}
	if _, err := NewIdentity([]byte("junk"), k.account); err == nil {
		t.Fatal("junk seed accepted")
	}
	pub, _ := k.kp.PublicKey()
	if pub != got.User || len(seed) == 0 {
		t.Fatal("user")
	}
}

// TestSigningRefusalsMatchTheSDK pins each refusal class to the pinned SDK's behaviour. The SDK is asked directly, so a
// pin bump that rewords a refusal fails here and not silently in production.
func TestSigningRefusalsMatchTheSDK(t *testing.T) {
	k := newCompatKit(t)
	seed, _ := k.kp.Seed()
	badAcct, err := NewIdentity(seed, "A!bad")
	if err != nil {
		t.Fatal(err)
	}
	crcAcct, _ := NewIdentity(seed, badCRC(k.account))
	const sub = "agents.mail.cc.o.n"
	for _, c := range []struct {
		name  string
		id    Identity
		sub   string
		nm    string
		nonce string
		want  SignRefusal
	}{
		{"account with a bad character", badAcct, sub, "x", "n", RefusedAccount},
		{"nkey-shaped account, bad checksum", crcAcct, sub, "x", "n", RefusedAccount},
		{"empty account", Identity{Account: "", User: k.id.User, signer: k.id.signer}, sub, "x", "n", RefusedAccount},
		{"subject with a space", k.id, "agents.mail.a b.o.n", "x", "n", RefusedSubject},
		{"empty subject", k.id, "", "x", "n", RefusedSubject},
		{"subject with an empty token", k.id, "agents..o.n", "x", "n", RefusedSubject},
		{"name of 65", k.id, sub, strings.Repeat("n", 65), "n", RefusedName},
		{"name of 33 astral characters (66 units)", k.id, sub, strings.Repeat("😀", 33), "n", RefusedName},
		{"nonce with a bad character", k.id, sub, "x", "bad nonce!", RefusedMessageID},
		{"nonce of 65", k.id, sub, "x", strings.Repeat("n", 65), RefusedMessageID},
		{"name with a control character (SDK refuses, length is fine)", k.id, sub, "a\x01b", "n", RefusedUnspecified},
		{"identity with no seed", Identity{Account: k.account, User: k.id.User}, sub, "x", "n", RefusedUnspecified},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, err := c.id.Sign(c.sub, []byte("p"), c.nm, compatNow, c.nonce)
			var se *SignError
			if !asSignError(err, &se) {
				t.Fatalf("want a *SignError, got %v", err)
			}
			if se.Class != c.want {
				t.Errorf("class %q, want %q", se.Class, c.want)
			}
			if h != (sap.SenderHeader{}) {
				t.Errorf("a refusal returned a header")
			}
			for _, leak := range []string{"identity:", "NATS subject", "display name", "nonce must", "[A-Za-z", "A!bad"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the error text %q carries %q", err.Error(), leak)
				}
			}
		})
	}
	// The classes' words are the words of TO-8.
	for class, words := range map[SignRefusal]string{RefusedAccount: "your account is not valid", RefusedSubject: "the subject is not valid",
		RefusedName: "your name is too long", RefusedMessageID: "the message id is not valid"} {
		if string(class) != words {
			t.Errorf("%q moved", words)
		}
		if got := (&SignError{Class: class}).Error(); got != "could not sign: "+words {
			t.Errorf("error text %q", got)
		}
	}
	if got := (&SignError{Class: RefusedUnspecified}).Error(); got != "could not sign" {
		t.Errorf("unspecified: %q", got)
	}
	// IDSig refuses an invalid account the same way and never panics on an identity with no seed.
	if _, err := badAcct.IDSig("a", "o", "s"); err == nil {
		t.Error("IDSig signed for an invalid account")
	} else if se := (*SignError)(nil); !asSignError(err, &se) || se.Class != RefusedAccount {
		t.Errorf("IDSig refusal %v", err)
	}
	if _, err := (Identity{}).IDSig("a", "o", "s"); err == nil {
		t.Error("IDSig signed with no seed")
	}
}

// liveFixture is a verified live request: the header's ts is compatNow.
func liveFixture(k compatKit, nonce string) (hdr, subject string, payload []byte) {
	const subj = "agents.prompt.cc.o.n"
	h, err := k.old.Sign(subj, []byte(`{"prompt":"hi"}`), "alice", compatNow, nonce)
	if err != nil {
		k.t.Fatal(err)
	}
	return h.Encode(), subj, []byte(`{"prompt":"hi"}`)
}

func TestLiveAdmitsAndRefusesReplays(t *testing.T) {
	k := newCompatKit(t)
	hdr, subj, pay := liveFixture(k, "n-1")
	set := NewNonceSet(60 * time.Second)
	r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil)
	if r.Outcome != Admitted || r.Trust != sap.TrustVerified || r.Header.User != k.id.User || r.Status() != 0 || r.Class != "" {
		t.Fatalf("first: %+v", r)
	}
	r = VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil)
	if r.Outcome != NotVerified || r.Trust != sap.TrustForged || r.Status() != 401 || r.Reason() != ReasonSignedRequired || r.Class != "not verified" {
		t.Fatalf("replay: %+v", r)
	}
	// The old code says the same of the same two deliveries.
	oset := sap.NewNonceSet(60 * time.Second)
	if _, tr := sap.VerifyLive(hdr, subj, pay, compatNow, oset); tr != sap.TrustVerified {
		t.Fatal(tr)
	}
	if _, tr := sap.VerifyLive(hdr, subj, pay, compatNow, oset); tr != sap.TrustForged {
		t.Fatal(tr)
	}
}

// TestLiveSkew: the window is 30 s each side, inclusive, as the old code has it, and as the SDK.
func TestLiveSkew(t *testing.T) {
	k := newCompatKit(t)
	hdr, subj, pay := liveFixture(k, "n-skew")
	for sec := -61; sec <= 61; sec++ {
		now := compatNow.Add(time.Duration(sec) * time.Second)
		r := VerifyLive([]string{hdr}, subj, pay, now, NewNonceSet(60*time.Second), nil)
		_, old := sap.VerifyLive(hdr, subj, pay, now, sap.NewNonceSet(60*time.Second))
		want := Outcome(NotVerified)
		if sec >= -30 && sec <= 30 {
			want = Admitted
		}
		if r.Outcome != want || (old == sap.TrustVerified) != (want == Admitted) {
			t.Fatalf("skew %ds: adapter %s, old %s, want %s", sec, r.Outcome, old, want)
		}
	}
}

// TestLiveCorpus runs every fixture through VerifyLive with a fresh set and the directory listing everyone.
func TestLiveCorpus(t *testing.T) {
	for _, c := range newCompatKit(t).cases() {
		t.Run(c.name, func(t *testing.T) {
			r := VerifyLive(c.values(), c.subject, []byte(c.payload), compatNow, NewNonceSet(time.Minute), nil)
			want := map[sap.Trust]Outcome{sap.TrustVerified: Admitted, sap.TrustMalformed: Malformed, sap.TrustForged: NotVerified,
				sap.TrustClaimed: Claimed, sap.TrustAbsent: Absent}[c.expectSDK]
			if r.Outcome != want {
				t.Fatalf("outcome %s, want %s", r.Outcome, want)
			}
			if r.Trust != c.expectSDK {
				t.Errorf("trust %s, want %s", r.Trust, c.expectSDK)
			}
			if r.Class != wantClass(c) {
				t.Errorf("class %q, want %q", r.Class, wantClass(c))
			}
			switch r.Outcome {
			case Malformed:
				if r.Status() != 400 || r.Reason() != "malformed Agent-Sender header" {
					t.Errorf("reply %d %q", r.Status(), r.Reason())
				}
			case Admitted:
			default:
				if r.Status() != 401 || r.Reason() != "a signed Agent-Sender header is required (min_sender_trust=signed)" {
					t.Errorf("reply %d %q", r.Status(), r.Reason())
				}
			}
		})
	}
}

// countingSet is a NonceSet the test counts.
type countingSet struct {
	inner *SDKNonceSet
	calls int
}

func (c *countingSet) Seen(user, nonce string, now time.Time) NonceStatus {
	c.calls++
	return c.inner.Seen(user, nonce, now)
}

// TestUnlistedSenderRecordsNoNonce is AT-021's run in the test process (REQ-020, CHG-13): a sender with a valid key the
// directory does not list gets 403, its replay gets 403 too, another unlisted request gets 403, no set was asked and none
// holds an entry; a listed sender is served.
func TestUnlistedSenderRecordsNoNonce(t *testing.T) {
	k := newCompatKit(t)
	set := &countingSet{inner: NewNonceSet(time.Minute)}
	var asked []string
	listed := func(user, account string) bool { asked = append(asked, user); return false }
	hdr, subj, pay := liveFixture(k, "n-unlisted")
	for i := 0; i < 2; i++ {
		r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, listed)
		if r.Outcome != NotListed || r.Status() != 403 || r.Reason() != "the sender is not a mash endpoint" {
			t.Fatalf("request %d: %+v", i+1, r)
		}
		if r.Trust != sap.TrustVerified || r.Header.User != k.id.User {
			t.Errorf("the verified key should be reported: %+v", r)
		}
	}
	h2, _, _ := liveFixture(k, "n-unlisted-2")
	if r := VerifyLive([]string{h2}, subj, pay, compatNow, set, listed); r.Outcome != NotListed || r.Status() != 403 {
		t.Fatalf("another request: %+v", r)
	}
	if set.calls != 0 || set.inner.Len() != 0 || set.inner.Contains(k.id.User, "n-unlisted") {
		t.Fatalf("the set was asked %d times and holds %d entries", set.calls, set.inner.Len())
	}
	if len(asked) != 3 {
		t.Fatalf("the directory was asked %d times, want 3", len(asked))
	}
	// A listed sender is served, and then its nonce is held.
	in := func(user, account string) bool { return user == k.id.User }
	if r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, in); r.Outcome != Admitted || r.Status() != 0 {
		t.Fatalf("listed: %+v", r)
	}
	if set.calls != 1 || !set.inner.Contains(k.id.User, "n-unlisted") || set.inner.Len() != 1 {
		t.Fatalf("calls %d, len %d", set.calls, set.inner.Len())
	}
}

// TestDirectoryIsAskedAfterTheSignature: a header that fails verification never reaches the directory or the set.
func TestDirectoryIsAskedAfterTheSignature(t *testing.T) {
	k := newCompatKit(t)
	set := &countingSet{inner: NewNonceSet(time.Minute)}
	asked := 0
	listed := func(user, account string) bool { asked++; return true }
	hdr, subj, pay := liveFixture(k, "n-order")
	bad := []struct {
		name string
		vals []string
		sub  string
		pay  []byte
		now  time.Time
	}{
		{"tampered payload", []string{hdr}, subj, []byte("other"), compatNow},
		{"wrong arrival subject", []string{hdr}, "agents.prompt.zz.o.n", pay, compatNow},
		{"stale", []string{hdr}, subj, pay, compatNow.Add(time.Hour)},
		{"malformed", []string{strings.Replace(hdr, `"v":1`, `"v":"1"`, 1)}, subj, pay, compatNow},
		{"no header", nil, subj, pay, compatNow},
		{"two headers", []string{hdr, hdr}, subj, pay, compatNow},
	}
	for _, c := range bad {
		if r := VerifyLive(c.vals, c.sub, c.pay, c.now, set, listed); r.Outcome == Admitted || r.Outcome == NotListed {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	if asked != 0 || set.calls != 0 || set.inner.Len() != 0 {
		t.Fatalf("directory asked %d, set asked %d, entries %d", asked, set.calls, set.inner.Len())
	}
}

// TestNonceCapIsItsOwnReason: a correctly signed, listed sender past a cap is refused with 401 and the cap's own text,
// and is never told a signed header is required (CHG-7, TO-11).
func TestNonceCapIsItsOwnReason(t *testing.T) {
	k := newCompatKit(t)
	const wrong = "a signed Agent-Sender header is required (min_sender_trust=signed)"
	for _, c := range []struct {
		name       string
		total, per int
	}{{"per sender", 100, 2}, {"in all", 2, 100}} {
		t.Run(c.name+" cap", func(t *testing.T) {
			set := NewNonceSetCaps(time.Minute, c.total, c.per)
			var last LiveResult
			for i, n := range []string{"c-1", "c-2", "c-3"} {
				hdr, subj, pay := liveFixture(k, n)
				last = VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil)
				if i < 2 && last.Outcome != Admitted {
					t.Fatalf("request %d under the cap: %+v", i+1, last)
				}
			}
			if last.Outcome != OverNonceCap || last.Status() != 401 || last.Reason() != "too many recent prompts: try again shortly" {
				t.Fatalf("past the cap: %+v", last)
			}
			if last.Reason() == wrong || strings.Contains(last.Reason(), "signed") || last.Reason() != ReasonNonceCap {
				t.Errorf("the reply %q tells a correctly signed sender a header is required", last.Reason())
			}
			if last.Trust != sap.TrustVerified || last.Header.User != k.id.User {
				t.Errorf("the sender is verified: %+v", last)
			}
			if set.Len() != 2 {
				t.Errorf("the set grew past its cap: %d", set.Len())
			}
			// A replay of a recorded nonce is still a replay, not a cap refusal.
			hdr, subj, pay := liveFixture(k, "c-1")
			if r := VerifyLive([]string{hdr}, subj, pay, compatNow, set, nil); r.Outcome != NotVerified {
				t.Errorf("replay at the cap: %+v", r)
			}
		})
	}
	// The missing-header 401 keeps its text.
	if r := VerifyLive(nil, "s", nil, compatNow, NewNonceSet(time.Minute), nil); r.Status() != 401 || r.Reason() != wrong {
		t.Errorf("no header: %d %q", r.Status(), r.Reason())
	}
}

// TestSDKNonceSetCaps: the defaults are the SDK's (10,000 for one sender, 100,000 in all), and the set says fresh, seen
// and full, which the old bool cannot.
func TestSDKNonceSetCaps(t *testing.T) {
	now := compatNow
	s := NewNonceSet(time.Minute)
	if s.Seen("UA", "n", now) != NonceFresh || s.Seen("UA", "n", now) != NonceSeen {
		t.Fatal("fresh then seen")
	}
	for i := 1; i < 10_000; i++ {
		if st := s.Seen("UA", "n"+itoa(i), now); st != NonceFresh {
			t.Fatalf("nonce %d: %v", i, st)
		}
	}
	if st := s.Seen("UA", "over", now); st != NonceFull {
		t.Fatalf("the 10,001st nonce of one sender: %v", st)
	}
	if s.Seen("UB", "n", now) != NonceFresh {
		t.Fatal("another sender is still served")
	}
	if s.Seen("UA", "n", now) != NonceSeen {
		t.Fatal("a recorded pair is seen, not full")
	}
	all := NewNonceSet(time.Minute)
	for i := 0; i < 100_000; i++ {
		if st := all.Seen("U"+itoa(i/10_000), "n"+itoa(i), now); st != NonceFresh {
			t.Fatalf("entry %d: %v", i, st)
		}
	}
	if st := all.Seen("UZ", "n", now); st != NonceFull {
		t.Fatalf("the 100,001st entry: %v", st)
	}
	// Entries leave with the window.
	if st := s.Seen("UA", "over", now.Add(2*time.Minute)); st != NonceFresh {
		t.Fatalf("after the window: %v", st)
	}
	if NewNonceSet(time.Second).set.Window() != LiveSkew {
		t.Fatal("the window is at least the replay window")
	}
}

// TestEleventhShapeCandidates pins what the adapter does with headers outside the ten shapes on which the SDK's parser
// and mash's old one also differ. They are NOT in the corpus and NOT among the contract's ten. Each row says the old
// verdict (asserted on the old code) and the strict one. If any of these is to be refused or accepted otherwise, that is
// a contract amendment.
func TestEleventhShapeCandidates(t *testing.T) {
	k := newCompatKit(t)
	const subj, pay = "agents.mail.cc.o.n", `{"text":"hello"}`
	base := k.base(k.account, subj, "2026-11-15T12:00:00Z", "nonce-1", pay)
	withV := func(v string) string { return strings.Replace(members(base...), `"v":1`, `"v":`+v, 1) }
	unsigned := func(ms ...string) string {
		return members(append([]string{`"v":1`, `"account":` + q(k.account), `"user":` + q(k.id.User)}, ms...)...)
	}
	for _, c := range []struct {
		name        string
		hdr         string
		old, strict sap.Trust
		class       string
		notAShape   bool // true: SDK-only strictness outside the ten
	}{
		{"v as 1.0", withV("1.0"), sap.TrustAbsent, sap.TrustVerified, "", true},
		{"v as 1e0", withV("1e0"), sap.TrustAbsent, sap.TrustVerified, "", true},
		{"invalid UTF-8 in the value", strings.Replace(members(base...), `"v":1`, "\"v\":1,\"x\":\"\xff\"", 1), sap.TrustVerified, sap.TrustMalformed, "unreadable sender header", true},
		{"claim with a ts that is not a time", unsigned(`"ts":"yesterday"`), sap.TrustClaimed, sap.TrustMalformed, "time not valid", true},
		{"claim with a ts with an offset", unsigned(`"ts":"2026-11-15T13:00:00+01:00"`), sap.TrustClaimed, sap.TrustMalformed, "time not in UTC", false},
		{"claim with a bad nonce", unsigned(`"nonce":"bad nonce!"`), sap.TrustClaimed, sap.TrustMalformed, "message id not valid", true},
		{"claim with a sub with a space", unsigned(`"sub":"a b"`), sap.TrustClaimed, sap.TrustMalformed, "space in subject", false},
		{"claim with an empty sub", unsigned(`"sub":""`), sap.TrustClaimed, sap.TrustMalformed, "unreadable sender header", true},
		{"claim with a control character in the name", unsigned(`"name":"a\u0001"`), sap.TrustClaimed, sap.TrustMalformed, "control character in name", false},
		{"sub with an empty token", members(k.base(k.account, "agents..n", "2026-11-15T12:00:00Z", "nonce-1", pay)...), sap.TrustVerified, sap.TrustMalformed, "unreadable sender header", true},
		{"sub with a control character", members(k.base(k.account, "agents.a\x01.n", "2026-11-15T12:00:00Z", "nonce-1", pay)...), sap.TrustVerified, sap.TrustMalformed, "unreadable sender header", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			subject := subj
			if strings.Contains(c.hdr, "agents..n") {
				subject = "agents..n"
			} else if strings.Contains(c.hdr, `agents.a\u0001.n`) || strings.Contains(c.hdr, "agents.a\x01.n") {
				subject = "agents.a\x01.n"
			}
			if _, got := sap.VerifyStored(c.hdr, subject, []byte(pay)); got != c.old {
				t.Errorf("old verifier %s, expected %s", got, c.old)
			}
			trust, class := Classify(subject, []string{c.hdr}, []byte(pay))
			if trust != c.strict || class != c.class {
				t.Errorf("adapter %s %q, expected %s %q", trust, class, c.strict, c.class)
			}
		})
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func asSignError(err error, target **SignError) bool { return errors.As(err, target) }

func readDirNames(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out, err
}

// captureLogs sends the standard logger (and so slog's default handler) to w until the returned function runs.
func captureLogs(w io.Writer) func() {
	prev := log.Writer()
	log.SetOutput(w)
	return func() { log.SetOutput(prev) }
}

// credsFor is a credentials file for the user key in the given account, as sap's own tests write them: the JWT's
// issuer is the account.
func credsFor(t *testing.T, kp nkeys.KeyPair, account string) []byte {
	t.Helper()
	pub, _ := kp.PublicKey()
	seed, _ := kp.Seed()
	acc, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	_ = acc
	uc := jwt.NewUserClaims(pub)
	uc.IssuerAccount = account
	// Sign with a throwaway account key: IdentityFromCreds does not check the issuer's signature, only the shape.
	tok, err := uc.Encode(acc)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("-----BEGIN NATS USER JWT-----\n" + tok + "\n------END NATS USER JWT------\n\n" +
		"-----BEGIN USER NKEY SEED-----\n" + string(seed) + "\n------END USER NKEY SEED------\n")
}
