package issue

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/store"
)

const (
	hardenTTL      = time.Minute
	hardenManyReqs = 500
	hardenCapTakes = 20000
	// hardenDeadline is generous: the work under it is microseconds; a scan of a full set per request is not.
	hardenDeadline = 2 * time.Second
)

var bg = context.Background()

// reqFor builds a request for address and public key with the given nonce, proved by the fixture's wrangler.
func (f *fixture) reqFor(t *testing.T, address, pub, nonce string) Request {
	t.Helper()
	r := Request{Version: 1, Address: address, PublicKey: pub, Host: "h", Nonce: nonce, Expires: f.now.Add(hardenTTL).Unix(), Wrangler: f.wpub}
	var err error
	if r.Proof, err = SignProof(f.wrangler, r.Address, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
		t.Fatal(err)
	}
	return r
}

func userPub(t *testing.T) string {
	t.Helper()
	u, _ := nkeys.CreateUser()
	pub, _ := u.PublicKey()
	return pub
}

// H1: the signing key must be published as a scoped signer with the list set's template.
func TestSigningKeyMustBePublishedScopedWithTheTable(t *testing.T) {
	other := f0Perms("agents.>")
	for _, c := range []struct {
		name    string
		publish func(f *fixture)
		want    string
	}{
		{"unscoped", func(f *fixture) { f.claims.publish(f.gpub, nil) }, CodeNoScope},
		{"absent", func(f *fixture) { f.claims.publish(otherAccountKey(t), &f.tpl) }, CodeNoScope},
		{"another template", func(f *fixture) { f.claims.publish(f.gpub, &other) }, CodeTemplate},
		{"the equal template", func(f *fixture) { f.claims.publish(f.gpub, &f.tpl) }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.publish(f)
			r := f.req(t, "foo.c.core", hardenTTL)
			out, err := f.is.Issue(bg, r, store.PurposeIssue)
			if code(err) != c.want || (c.want != "" && (out != nil || len(f.rec.recs) != 0)) {
				t.Fatalf("%v %v", out, err)
			}
			if c.want == "" && (err != nil || out == nil) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func f0Perms(allow string) jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow.Add(allow)
	return p
}

func otherAccountKey(t *testing.T) string {
	k, _ := nkeys.CreateAccount()
	p, _ := k.PublicKey()
	return p
}

// The verdict is served for ScopeTTL and not longer: a key unpublished after a positive verdict is refused once the TTL
// has passed, and requests within it do not each read the account JWT.
func TestScopeVerdictIsCachedForTheTTLOnly(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 5; i++ {
		if _, err := f.is.Issue(bg, f.req(t, "foo.c.core", hardenTTL), store.PurposeIssue); err != nil {
			t.Fatal(err)
		}
	}
	if f.claims.n != 1 {
		t.Fatalf("%d reads of the account JWT for 5 requests", f.claims.n)
	}
	f.claims.publish(f.gpub, nil)
	f.now = f.now.Add(ScopeTTL / 2)
	if _, err := f.is.Issue(bg, f.req(t, "foo.c.core", hardenTTL), store.PurposeIssue); err != nil {
		t.Fatalf("within the TTL the verdict stands: %v", err)
	}
	f.now = f.now.Add(ScopeTTL)
	if _, err := f.is.Issue(bg, f.req(t, "foo.c.core", hardenTTL), store.PurposeIssue); code(err) != CodeNoScope {
		t.Fatalf("after the TTL: %v", err)
	}
}

// H2: a revocation that lands between the lookup and the write refuses the issue and returns nothing.
func TestRevocationBetweenLookupAndWriteRefuses(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", hardenTTL)
	f.rec.recs[r.PublicKey] = store.Issued{PublicKey: r.PublicKey, Address: r.Address}
	f.rec.afterLookup = func() {
		f.rec.mu.Lock()
		defer f.rec.mu.Unlock()
		cur := f.rec.recs[r.PublicKey]
		cur.RevokedAt = "2026-01-01T00:00:00Z"
		f.rec.recs[r.PublicKey] = cur
	}
	out, err := f.is.Issue(bg, r, store.PurposeIssue)
	if out != nil || code(err) != CodeRevoked {
		t.Fatalf("%v %v", out, err)
	}
}

// H2: another issuer binding the key to another address between lookup and write refuses this one.
func TestBindingElsewhereBetweenLookupAndWriteRefuses(t *testing.T) {
	f := newFixture(t)
	r := f.req(t, "foo.c.core", hardenTTL)
	f.rec.afterLookup = func() {
		f.rec.mu.Lock()
		defer f.rec.mu.Unlock()
		f.rec.recs[r.PublicKey] = store.Issued{PublicKey: r.PublicKey, Address: "foo.c.lead"}
	}
	out, err := f.is.Issue(bg, r, store.PurposeIssue)
	if out != nil || code(err) != CodeBound {
		t.Fatalf("%v %v", out, err)
	}
}

// H2: two issuers (two processes) racing for one key and two addresses: exactly one gets a credential.
func TestTwoIssuersRacingForOneKeyExactlyOneSucceeds(t *testing.T) {
	for round := 0; round < 20; round++ {
		f := newFixture(t)
		second := &Issuer{Registry: f.is.Registry, ListSet: f.is.ListSet, Signer: f.is.Signer, Record: f.is.Record, Claims: f.is.Claims,
			AccountPub: f.is.AccountPub, Log: f.is.Log, Now: f.is.Now}
		key := userPub(t)
		reqs := []Request{f.reqFor(t, "foo.c.core", key, nonceN(round, 0)), f.reqFor(t, "foo.c.lead", key, nonceN(round, 1))}
		issuers := []*Issuer{f.is, second}
		var wg sync.WaitGroup
		var ok, bound int
		var mu sync.Mutex
		start := make(chan struct{})
		for i := range reqs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				out, err := issuers[i].Issue(bg, reqs[i], store.PurposeIssue)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil && out != nil:
					ok++
				case code(err) == CodeBound:
					bound++
				}
			}()
		}
		close(start)
		wg.Wait()
		if ok != 1 || bound != 1 {
			t.Fatalf("round %d: %d succeeded, %d refused as bound", round, ok, bound)
		}
	}
}

// nonceN is a distinct 22-character base64url nonce (16 bytes) for each (round, i).
func nonceN(round, i int) string { return fmt.Sprintf("%022d", round*1000+i) }

// M1: a refused request gives its nonce back, so the corrected request (the proof covers the nonce) is accepted; a
// request that succeeded keeps its nonce used.
func TestRefusedRequestReleasesItsNonce(t *testing.T) {
	f := newFixture(t)
	key := userPub(t)
	n := nonceN(1, 1)
	if out, err := f.is.Issue(bg, f.reqFor(t, "foo.c.nobody", key, n), store.PurposeIssue); out != nil || code(err) != CodeUnlisted {
		t.Fatalf("%v %v", out, err)
	}
	if f.is.nonces.len() != 0 {
		t.Fatalf("%d nonces held after a refusal", f.is.nonces.len())
	}
	ok := f.reqFor(t, "foo.c.core", key, n)
	if _, err := f.is.Issue(bg, ok, store.PurposeIssue); err != nil {
		t.Fatalf("the corrected request: %v", err)
	}
	if _, err := f.is.Issue(bg, ok, store.PurposeIssue); code(err) != CodeReplay {
		t.Fatalf("a successful request's nonce replayed: %v", err)
	}
}

// M1: each refusal after the nonce was taken releases it, and refusals never fill the replay set.
func TestRefusalsNeverHoldNonces(t *testing.T) {
	f := newFixture(t)
	ctx := bg
	key := userPub(t)
	f.rec.recs[key] = store.Issued{PublicKey: key, Address: "foo.c.lead"}
	revoked := userPub(t)
	f.rec.recs[revoked] = store.Issued{PublicKey: revoked, Address: "foo.c.core", RevokedAt: "2026-01-01T00:00:00Z"}
	steps := []func(i int) (Request, string){
		func(i int) (Request, string) {
			return f.reqFor(t, "foo.c.nobody", userPub(t), nonceN(2, i)), CodeUnlisted
		},
		func(i int) (Request, string) { return f.reqFor(t, "foo.c.core", key, nonceN(3, i)), CodeBound },
		func(i int) (Request, string) { return f.reqFor(t, "foo.c.core", revoked, nonceN(4, i)), CodeRevoked },
	}
	for i := 0; i < hardenManyReqs; i++ {
		for _, step := range steps {
			r, want := step(i)
			if _, err := f.is.Issue(ctx, r, store.PurposeIssue); code(err) != want {
				t.Fatalf("%s: %v", want, err)
			}
		}
	}
	f.claims.publish(f.gpub, nil)
	f.now = f.now.Add(ScopeTTL + time.Second)
	if _, err := f.is.Issue(ctx, f.reqFor(t, "foo.c.core", userPub(t), nonceN(5, 0)), store.PurposeIssue); code(err) != CodeNoScope {
		t.Fatal(err)
	}
	f.claims.publish(f.gpub, &f.tpl)
	f.now = f.now.Add(ScopeTTL + time.Second)
	f.rec.fail = true
	if _, err := f.is.Issue(ctx, f.reqFor(t, "foo.c.core", userPub(t), nonceN(6, 0)), store.PurposeIssue); code(err) != CodeRecordFailed {
		t.Fatal(err)
	}
	if n := f.is.nonces.len(); n != 0 {
		t.Fatalf("%d nonces held after only refusals", n)
	}
}

// M2: at the cap a request is refused without a scan, promptly, and the mutex is not held across the work after it.
func TestFullNonceSetRefusesPromptly(t *testing.T) {
	f := newFixture(t)
	exp := f.now.Add(hardenTTL)
	for i := 0; i < MaxNonces; i++ {
		if c := f.is.nonces.take("fill"+strconv.Itoa(i), exp, f.now); c != "" {
			t.Fatal(c)
		}
	}
	// The requests (key generation and signing) are built before the clock starts: only the refusals are timed.
	reqs := make([]Request, hardenManyReqs)
	for i := range reqs {
		reqs[i] = f.reqFor(t, "foo.c.core", userPub(t), nonceN(7, i))
	}
	start := time.Now()
	for i := range reqs {
		if _, err := f.is.Issue(bg, reqs[i], store.PurposeIssue); code(err) != CodeBusy {
			t.Fatalf("%v", err)
		}
	}
	if d := time.Since(start); d > hardenDeadline {
		t.Fatalf("%d refusals at the cap took %s", hardenManyReqs, d)
	}
	// Once the entries expire, requests are served again and the set drains by popping, not scanning.
	f.now = f.now.Add(hardenTTL + time.Second)
	if _, err := f.is.Issue(bg, f.reqFor(t, "foo.c.core", userPub(t), nonceN(8, 0)), store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if f.is.nonces.len() != 1 {
		t.Fatalf("%d", f.is.nonces.len())
	}
}

// M2: a take at the cap costs the peek of the earliest expiry, not a pass over the set: many of them finish well inside
// a deadline that a pass over 65,536 entries each would exceed many times over.
func TestTakeAtTheCapDoesNotScan(t *testing.T) {
	var s nonceSet
	now := time.Unix(1000, 0)
	for i := 0; i < MaxNonces; i++ {
		s.take("fill"+strconv.Itoa(i), now.Add(hardenTTL), now)
	}
	start := time.Now()
	for i := 0; i < hardenCapTakes; i++ {
		if s.take("x"+strconv.Itoa(i), now.Add(hardenTTL), now) != CodeBusy {
			t.Fatal("not refused at the cap")
		}
	}
	if d := time.Since(start); d > hardenDeadline {
		t.Fatalf("%d takes at the cap took %s", hardenCapTakes, d)
	}
}

// M2: the nonce set orders by expiry: a later-expiring nonce reserved first is not dropped before an earlier one, and a
// released nonce leaves no trace.
func TestNonceSetOrdersByExpiryAndReleases(t *testing.T) {
	var s nonceSet
	now := time.Unix(1000, 0)
	s.take("late", now.Add(3*hardenTTL), now)
	s.take("early", now.Add(hardenTTL), now)
	s.take("gone", now.Add(2*hardenTTL), now)
	s.release("gone")
	if s.take("gone", now.Add(2*hardenTTL), now) != "" {
		t.Fatal("a released nonce is still held")
	}
	s.release("gone")
	if s.take("early", now, now.Add(hardenTTL-time.Second)) != CodeReplay {
		t.Fatal("early dropped too soon")
	}
	if s.take("x", now, now.Add(hardenTTL)) != "" || s.len() != 2 { // early dropped; late and x held
		t.Fatalf("%d", s.len())
	}
	if s.take("late", now, now.Add(hardenTTL)) != CodeReplay {
		t.Fatal("late dropped with early")
	}
}
