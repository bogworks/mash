package apply

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	ks, _, _ = setup(t)
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

// A server that missed an earlier push (it still holds a copy with a signer that a later push removed) is among the
// replies: apply builds from the newest and the removed signer does not come back.
func TestApplyDoesNotResurrectARemovedSignerFromAStaleServer(t *testing.T) {
	ks, fk, c := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	// the newest copy has zone b's key removed by an earlier --remove-zone; the stale server still has it.
	bk, _ := ks.ZoneKey("legacy", true)
	legacy, _ := bk.PublicKey()
	stale := jwt.NewAccountClaims(acct)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = legacy, "legacy"
	stale.SigningKeys.AddScopedSigner(sc)
	staleTok := tokenAt(t, op, stale, 1000)
	newest := tokenAt(t, op, jwt.NewAccountClaims(acct), 2000)
	fk.token = newest
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}}
	m := &multi{fake: fk, copies: []string{staleTok, newest}, healed: map[int]bool{0: true, 1: true}}
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: m, VerifyTries: 1, VerifyWait: 1}, &out)
	if code != 0 || fk.pushes != 1 {
		t.Fatalf("%d pushes %d\n%s", code, fk.pushes, out.String())
	}
	if !strings.Contains(out.String(), "warning: 1 of 2 servers hold an older copy") {
		t.Fatalf("no warning:\n%s", out.String())
	}
	got, err := jwt.DecodeAccountClaims(fk.lastPush)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SigningKeys.GetScope(legacy); ok {
		t.Fatal("the removed signer came back")
	}
}

func TestApplyRefusesSameIatDifferentContent(t *testing.T) {
	ks, fk, c := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	cl := jwt.NewAccountClaims(acct)
	a := tokenAt(t, op, cl, 2000)
	cl.Name = "other"
	b := tokenAt(t, op, cl, 2000)
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	m := &multi{fake: fk, copies: []string{a, b}}
	for _, mode := range []Mode{Apply, DryRun, Diff} {
		var out bytes.Buffer
		code := Run(context.Background(), Params{Mode: mode, Compiled: c, Keys: ks, Resolver: m}, &out)
		if code != 1 || !strings.Contains(out.String(), "2 distinct account JWTs issued at the same time") || fk.pushes != 0 {
			t.Fatalf("%s: %d\n%s", mode, code, out.String())
		}
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("created keys %v", z)
	}
}

// The newest copy changes between the first lookup and the re-check: apply computes again once; a second change is the
// existing 'try again' error.
func TestApplyRestartsOnceWhenTheNewestCopyChanges(t *testing.T) {
	for _, twice := range []bool{false, true} {
		ks, fk, c := setup(t)
		op, _, _ := ks.Operator()
		_, acct, _ := ks.Account()
		v := func(iat int64, name string) string {
			cl := jwt.NewAccountClaims(acct)
			cl.Name = name
			return tokenAt(t, op, cl, iat)
		}
		fk.token = v(1000, "a")
		fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
		m := &multi{fake: fk, copies: []string{fk.token}, onCall: func(n int, m *multi) {
			switch {
			case n == 2:
				m.copies = []string{v(2000, "b")}
			case n == 4 && twice:
				m.copies = []string{v(3000, "c")}
			}
		}}
		m.healed = map[int]bool{0: true}
		var out bytes.Buffer
		code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: m, VerifyTries: 1, VerifyWait: 1}, &out)
		if twice {
			if code != 1 || !strings.Contains(out.String(), "changed twice") || fk.pushes != 0 {
				t.Fatalf("twice: %d pushes %d\n%s", code, fk.pushes, out.String())
			}
			continue
		}
		if code != 0 || fk.pushes != 1 {
			t.Fatalf("once: %d pushes %d\n%s", code, fk.pushes, out.String())
		}
		got, _ := jwt.DecodeAccountClaims(fk.lastPush)
		if got.Name != "b" {
			t.Fatalf("pushed from the old base: name %q", got.Name)
		}
	}
}

// After the push, a server that still holds an older copy is not a match: PARTIAL, naming it, exit 1.
func TestReadBackWithOneServerStillOlderIsPartial(t *testing.T) {
	ks, fk, c := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	old := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	fk.token = old
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}}
	fk.applyPush = true
	m := &multi{fake: fk, copies: []string{old, old}, healed: map[int]bool{0: true}} // s2 never takes the update
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: m, VerifyTries: 2, VerifyWait: 1}, &out)
	s := out.String()
	if code != 1 || !strings.Contains(s, "PARTIAL") || !strings.Contains(s, "1 of 2 answering servers do not hold the pushed JWT") || strings.Contains(s, "pushed: ") {
		t.Fatalf("%d\n%s", code, s)
	}
}

// revoke builds from the newest copy too: a stale server's copy, from before the user was revoked, does not undo it.
func TestRevokeDoesNotResurrectFromAStaleServer(t *testing.T) {
	ks, fk, _ := setup(t)
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

// The policy already matches (nothing to change): the unchanged JWT is still checked against the newest copy before it
// is sent again, so a revocation or another update made after the first lookup is never overwritten by the old token.
func TestApplyUnchangedPathRechecksTheBaseBeforePushing(t *testing.T) {
	for _, mode := range []string{"same", "revoked", "twice"} {
		ks, fk, c := setup(t)
		op, _, _ := ks.Operator()
		_, acct, _ := ks.Account()
		fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
		// first apply: brings the account to the policy; the token it leaves is the unchanged base
		if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 1, VerifyWait: 1}, &bytes.Buffer{}); code != 0 {
			t.Fatalf("setup apply: %d", code)
		}
		base := fk.token
		baseClaims, err := jwt.DecodeAccountClaims(base)
		if err != nil {
			t.Fatal(err)
		}
		newer := func(iat int64, user string) string {
			cl, _ := jwt.DecodeAccountClaims(base)
			cl.Revocations = jwt.RevocationList{}
			cl.Revocations.Revoke(user, time.Unix(iat, 0))
			return tokenAt(t, op, cl, baseClaims.IssuedAt+iat)
		}
		fk.pushes, fk.lastPush = 0, "" // multi heals from the first push on: count afresh
		pre := 0
		m := &multi{fake: fk, copies: []string{base}, healed: map[int]bool{0: true}}
		var second string
		switch mode {
		case "revoked":
			second = newer(10, userKey(t))
			m.onCall = func(n int, m *multi) {
				if n == 2 {
					m.copies = []string{second}
				}
			}
		case "twice":
			second = newer(10, userKey(t))
			third := newer(20, userKey(t))
			m.onCall = func(n int, m *multi) {
				switch n {
				case 2:
					m.copies = []string{second}
				case 4:
					m.copies = []string{third}
				}
			}
		}
		var out bytes.Buffer
		code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: m, VerifyTries: 1, VerifyWait: 1}, &out)
		pushes := fk.pushes - pre
		switch mode {
		case "same":
			if code != 0 || pushes != 1 || fk.lastPush != base || !strings.Contains(out.String(), "account claims already up to date") {
				t.Fatalf("same: %d pushes %d\n%s", code, pushes, out.String())
			}
		case "revoked":
			if code != 0 || pushes != 1 || fk.lastPush == base {
				t.Fatalf("revoked: %d pushes %d stale=%v\n%s", code, pushes, fk.lastPush == base, out.String())
			}
			got, _ := jwt.DecodeAccountClaims(fk.lastPush)
			if len(got.Revocations) != 1 {
				t.Fatalf("the newer revocation was dropped: %v", got.Revocations)
			}
		case "twice":
			if code != 1 || pushes != 0 || !strings.Contains(out.String(), "changed twice") {
				t.Fatalf("twice: %d pushes %d\n%s", code, pushes, out.String())
			}
		}
		_ = acct
	}
}

// partialErr answers LookupAll with some replies and an error: the replies that arrived do not stand for the servers
// that failed.
type partialErr struct {
	*fake
	got []string
	err error
}

func (p partialErr) LookupAll(context.Context, string, int) ([]string, error) { return p.got, p.err }

// The issue service signs on the strength of CurrentAccountClaims, so it needs the whole server set: a silent server
// may hold a newer JWT that removed or narrowed the signer.
func TestCurrentAccountClaimsNeedsEveryExpectedServer(t *testing.T) {
	ks, op, acct := baseEnv(t)
	trust, _ := LoadTrust(ks)
	sk, _ := nkeys.CreateAccount()
	signer, _ := sk.PublicKey()
	withSigner := jwt.NewAccountClaims(acct)
	withSigner.SigningKeys.Add(signer)
	older := tokenAt(t, op, withSigner, 1000)
	newer := tokenAt(t, op, jwt.NewAccountClaims(acct), 2000) // the signer removed
	two := ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	ctx := context.Background()

	// Both servers answer: the newest copy is used and it has no signer.
	for _, order := range [][]string{{older, newer}, {newer, older}} {
		cl, err := CurrentAccountClaims(ctx, &multi{fake: &fake{servers: two}, copies: order}, acct, trust, 2)
		if err != nil || cl.IssuedAt != 2000 || cl.SigningKeys.Contains(signer) {
			t.Fatalf("both answered: %v %+v", err, cl)
		}
	}
	// The server holding the newer copy is silent: the older copy that answered is not enough, with the count from the
	// ping or the declared one.
	for _, declared := range []int{0, 2} {
		_, err := CurrentAccountClaims(ctx, &multi{fake: &fake{servers: two}, copies: []string{older}}, acct, trust, declared)
		if err == nil || !strings.Contains(err.Error(), "only 1 of 2 servers answered") {
			t.Fatalf("declared %d: %v", declared, err)
		}
	}
	// A ping that saw one server while 2 are declared: refused before any lookup.
	one := &fake{servers: ServerSet{Names: []string{"s1"}, Expected: 1}}
	if _, err := CurrentAccountClaims(ctx, &multi{fake: one, copies: []string{older}}, acct, trust, 2); err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("declared 2, ping 1: %v", err)
	}
	// A named cluster with nothing declared: refused with the hint, like policy apply.
	named := &fake{servers: ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}}
	_, err := CurrentAccountClaims(ctx, &multi{fake: named, copies: []string{older, newer}}, acct, trust, 0)
	if err == nil || !strings.Contains(err.Error(), "MASH_SERVERS") || !strings.Contains(err.Error(), "--servers") {
		t.Fatalf("named cluster, none declared: %v", err)
	}
	if cl, err := CurrentAccountClaims(ctx, &multi{fake: named, copies: []string{older, newer}}, acct, trust, 2); err != nil || cl.IssuedAt != 2000 {
		t.Fatalf("named cluster, declared: %v", err)
	}
	// A lookup error after some replies arrived is an error, not a skipped reply.
	_, err = CurrentAccountClaims(ctx, partialErr{fake: &fake{servers: two}, got: []string{older}, err: errors.New("reply of 99999 bytes over the limit")}, acct, trust, 2)
	if err == nil || !strings.Contains(err.Error(), "over the limit") {
		t.Fatalf("lookup error: %v", err)
	}
	// A ping that fails is an error.
	if _, err := CurrentAccountClaims(ctx, &multi{fake: &fake{serversErr: errors.New("no ping"), servers: two}, copies: []string{older, newer}}, acct, trust, 2); err == nil {
		t.Fatal("ping error accepted")
	}
	// Complete and consistent: used. A standalone server with nothing declared is complete.
	if cl, err := CurrentAccountClaims(ctx, &multi{fake: &fake{}, copies: []string{older}}, acct, trust, 0); err != nil || !cl.SigningKeys.Contains(signer) {
		t.Fatalf("standalone: %v", err)
	}
	// A copy that does not verify is refused, not skipped.
	rogue, _ := nkeys.CreateOperator()
	bad := tokenAt(t, rogue, jwt.NewAccountClaims(acct), 3000)
	if _, err := CurrentAccountClaims(ctx, &multi{fake: &fake{servers: two}, copies: []string{older, bad}}, acct, trust, 2); err == nil {
		t.Fatal("unverified copy accepted")
	}
}
