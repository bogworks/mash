package apply

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// tokenAt re-issues claims signed by op with the issued-at iat (Encode always stamps the present second), so a test can
// hold copies of different ages in the same second.
func tokenAt(t *testing.T, op nkeys.KeyPair, cl *jwt.AccountClaims, iat int64) string {
	t.Helper()
	tok, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["iat"] = iat
	delete(m, "jti")
	raw, _ = json.Marshal(m)
	signing := parts[0] + "." + base64.RawURLEncoding.EncodeToString(raw)
	sig, err := op.Sign([]byte(signing))
	if err != nil {
		t.Fatal(err)
	}
	out := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	if got, err := jwt.DecodeAccountClaims(out); err != nil || got.IssuedAt != iat {
		t.Fatalf("tokenAt built an unreadable token: %v", err)
	}
	return out
}

// multi answers LookupAll with the given copies, one per server, whatever was pushed; a push replaces the copies of
// the servers in healed (the others keep the old copy: a server that missed the update).
type multi struct {
	*fake
	copies []string
	healed map[int]bool
	calls  int
	// onCall, when set, may replace the copies before the n-th LookupAll answers.
	onCall func(n int, m *multi)
}

func (m *multi) LookupAll(_ context.Context, _ string, _ int) ([]string, error) {
	m.fake.mu.Lock()
	defer m.fake.mu.Unlock()
	m.calls++
	if m.onCall != nil {
		m.onCall(m.calls, m)
	}
	out := append([]string(nil), m.copies...)
	if m.fake.pushes > 0 {
		for i := range out {
			if m.healed[i] {
				out[i] = m.fake.lastPush
			}
		}
	}
	return out, nil
}

func baseEnv(t *testing.T) (ks *ctlkeys.Authority, op nkeys.KeyPair, acct string) {
	t.Helper()
	ks, _ = setup(t)
	op, _, _ = ks.Operator()
	_, acct, _ = ks.Account()
	return
}

func TestLookupBasePicksTheNewestNotTheFirst(t *testing.T) {
	ks, op, acct := baseEnv(t)
	cl := jwt.NewAccountClaims(acct)
	old, mid, newest := tokenAt(t, op, cl, 1000), tokenAt(t, op, cl, 2000), tokenAt(t, op, cl, 3000)
	trust, _ := LoadTrust(ks)
	for _, order := range [][]string{{old, mid, newest}, {newest, old, mid}, {mid, newest, old}} {
		b, err := lookupBase(context.Background(), &multi{fake: &fake{}, copies: order}, acct, trust, 3, "look up")
		if err != nil || b.token != newest {
			t.Fatalf("%v %v", err, b.token == newest)
		}
		if !strings.Contains(b.warn, "2 of 3 servers hold an older copy (iat 1970-01-01T00:16:40Z, 1970-01-01T00:33:20Z); building from the newest") {
			t.Fatalf("warn %q", b.warn)
		}
	}
	// agreement: no warning; no JWT anywhere: empty token, no error; a server with none counts as older
	b, err := lookupBase(context.Background(), &multi{fake: &fake{}, copies: []string{newest, newest}}, acct, trust, 2, "x")
	if err != nil || b.token != newest || b.warn != "" {
		t.Fatalf("%v %+v", err, b)
	}
	b, err = lookupBase(context.Background(), &multi{fake: &fake{}, copies: []string{"", ""}}, acct, trust, 2, "x")
	if err != nil || b.token != "" {
		t.Fatalf("%v %+v", err, b)
	}
	b, _ = lookupBase(context.Background(), &multi{fake: &fake{}, copies: []string{newest, ""}}, acct, trust, 2, "x")
	if !strings.Contains(b.warn, "1 of 2 servers hold an older copy (iat no JWT)") {
		t.Fatalf("%q", b.warn)
	}
}

func TestLookupBaseRefusesSameIatDifferentContentAndForeignReplies(t *testing.T) {
	ks, op, acct := baseEnv(t)
	cl := jwt.NewAccountClaims(acct)
	a := tokenAt(t, op, cl, 5000)
	cl.Name = "other"
	b := tokenAt(t, op, cl, 5000)
	trust, _ := LoadTrust(ks)
	_, err := lookupBase(context.Background(), &multi{fake: &fake{}, copies: []string{a, b, a}}, acct, trust, 3, "x")
	if err == nil || !strings.Contains(err.Error(), "2 distinct account JWTs issued at the same time") {
		t.Fatalf("%v", err)
	}
	// an unverifiable reply is refused, even when another reply is newer and fine
	foreign, _ := nkeys.CreateOperator()
	cl.Name = ""
	bad := tokenAt(t, foreign, cl, 1)
	_, err = lookupBase(context.Background(), &multi{fake: &fake{}, copies: []string{a, bad}}, acct, trust, 2, "x")
	if err == nil || !strings.Contains(err.Error(), "not an accepted issuer") {
		t.Fatalf("%v", err)
	}
}

// revoke builds from the newest copy too: a stale server's copy, from before the user was revoked, does not undo it.
func TestRevokeDoesNotResurrectFromAStaleServer(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	u1, u2 := userKey(t), userKey(t)
	newCl := jwt.NewAccountClaims(acct)
	newCl.Revocations = jwt.RevocationList{}
	newCl.Revocations.Revoke(u1, revokeAt) // an earlier revocation the stale server missed
	newest := tokenAt(t, op, newCl, 2000)
	stale := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	fk.token = newest
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}}
	m := &multi{fake: fk, copies: []string{stale, newest}, healed: map[int]bool{0: true, 1: true}}
	res, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: m, VerifyTries: 1, Now: func() time.Time { return revokeAt.Add(time.Hour) }}, []string{u2})
	if err != nil || !strings.Contains(res.Warning, "1 of 2 servers hold an older copy") {
		t.Fatalf("%v %+v", err, res)
	}
	if !revokedIn(t, fk.lastPush, u1) || !revokedIn(t, fk.lastPush, u2) {
		t.Fatal("the push lost the earlier revocation or the new one")
	}
	// and the same-iat ambiguity refuses revoke as well
	a := tokenAt(t, op, jwt.NewAccountClaims(acct), 3000)
	cl := jwt.NewAccountClaims(acct)
	cl.Name = "x"
	b := tokenAt(t, op, cl, 3000)
	pushes := fk.pushes
	_, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: &multi{fake: fk, copies: []string{a, b}}}, []string{u2})
	if err == nil || !strings.Contains(err.Error(), "issued at the same time") || fk.pushes != pushes {
		t.Fatalf("%v", err)
	}
}
