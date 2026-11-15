package sap

import (
	"encoding/base64"
	"strings"
	"testing"
)

// VerifyStoredWhy gives one reason class per refusal, the same label as VerifyStored, and never any header text.
func TestVerifyStoredWhyClasses(t *testing.T) {
	k := newCompatKit(t)
	const (
		sub   = "agents.mail.cc.o.n"
		pay   = `{"text":"hello"}`
		ts    = "2026-11-15T12:00:00Z"
		nonce = "nonce-1"
	)
	acct := k.account
	base := func() []string { return k.base(acct, sub, ts, nonce, pay) }
	std := members(base()...)
	withName := func(m string) string {
		return members(append([]string{`"v":1`, `"account":` + q(acct), `"user":` + q(k.id.User), m}, base()[3:]...)...)
	}
	zeroSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	cases := []struct {
		name, hdr, subject, payload string
		trust                       Trust
		reason                      string
	}{
		{"verified", std, sub, pay, TrustVerified, ""},
		{"claimed", members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User)), sub, pay, TrustClaimed, ""},
		{"absent", "", sub, pay, TrustAbsent, ""},
		{"size", members(append(base(), `"pad":`+q(strings.Repeat("p", 2100)))...), sub, pay, TrustMalformed, "size"},
		{"encoding not JSON", "not json", sub, pay, TrustMalformed, "encoding"},
		{"encoding not UTF-8", "{\"v\":1,\"x\":\"\xff\"}", sub, pay, TrustMalformed, "encoding"},
		{"duplicate-member", strings.Replace(std, `"user":`, `"user":"UXXX","user":`, 1), sub, pay, TrustMalformed, "duplicate-member"},
		{"version-or-field v missing", strings.Replace(std, `"v":1,`, ``, 1), sub, pay, TrustMalformed, "version-or-field"},
		{"version-or-field v string", strings.Replace(std, `"v":1`, `"v":"1"`, 1), sub, pay, TrustMalformed, "version-or-field"},
		{"version-or-field user missing", members(`"v":1`, `"account":`+q(acct)), sub, pay, TrustMalformed, "version-or-field"},
		{"version-or-field sig without nonce", members(`"v":1`, `"account":`+q(acct), `"user":`+q(k.id.User), `"sub":`+q(sub), `"ts":`+q(ts), `"sig":`+q(k.sig(acct, sub, ts, "", pay))), sub, pay, TrustMalformed, "version-or-field"},
		{"name control character", withName(`"name":"a\u0001b"`), sub, pay, TrustMalformed, "name"},
		{"name too long", withName(`"name":` + q(strings.Repeat("n", 65))), sub, pay, TrustMalformed, "name"},
		{"subject", members(k.base(acct, "agents.mail.a b.o.n", ts, nonce, pay)...), "agents.mail.a b.o.n", pay, TrustMalformed, "subject"},
		{"timestamp", members(k.base(acct, sub, "yesterday", nonce, pay)...), sub, pay, TrustMalformed, "timestamp"},
		{"nonce", members(k.base(acct, sub, ts, "bad nonce!", pay)...), sub, pay, TrustMalformed, "nonce"},
		{"signature-format", strings.Replace(std, `"sig":"`, `"sig":"!!`, 1), sub, pay, TrustMalformed, "signature-format"},
		{"signature tampered payload", std, sub, `{"text":"HELLO"}`, TrustForged, "signature"},
		{"signature wrong arrival subject", std, "agents.mail.other.o.n", pay, TrustForged, "signature"},
		{"signature does not verify", strings.Replace(std, k.sig(acct, sub, ts, nonce, pay), zeroSig, 1), sub, pay, TrustForged, "signature"},
		{"other bad user key", strings.Replace(std, k.id.User, "UNOTAKEY", 1), sub, pay, TrustMalformed, "other"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, tr, why := VerifyStoredWhy(c.hdr, c.subject, []byte(c.payload))
			if tr != c.trust || why != c.reason {
				t.Fatalf("got %s/%q, want %s/%q", tr, why, c.trust, c.reason)
			}
			h2, tr2 := VerifyStored(c.hdr, c.subject, []byte(c.payload))
			if tr2 != tr || h2 != h {
				t.Fatalf("VerifyStored differs: %s vs %s", tr2, tr)
			}
			for _, secret := range []string{k.id.User, "hello", "HELLO", "pppp"} {
				if why != "" && strings.Contains(why, secret) {
					t.Fatalf("reason %q leaks %q", why, secret)
				}
			}
		})
	}
}
