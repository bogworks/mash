package policyintake_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

const (
	testRange     = "foo"
	retryInitial  = 5 * time.Millisecond
	retryMax      = 20 * time.Millisecond
	waitLimit     = 10 * time.Second
	settleTime    = 300 * time.Millisecond
	pollEvery     = 10 * time.Millisecond
	goroutineSlop = 3
)

type keyPair struct {
	kp  nkeys.KeyPair
	pub string
}

func newUser(t *testing.T) keyPair {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return keyPair{kp, pub}
}

// payload is the test envelope's document.
type payload struct {
	Schema         int    `json:"schema"`
	Account        string `json:"account"`
	Range          string `json:"range"`
	Generation     uint64 `json:"generation"`
	BaseGeneration uint64 `json:"base_generation"`
	NextSubmitter  string `json:"next_submitter,omitempty"`
	Unverified     bool   `json:"unverified,omitempty"`
}

// sign makes base64(json) '.' base64(signature) '.' signer.
func sign(t *testing.T, k keyPair, p payload) []byte {
	t.Helper()
	if p.Schema == 0 {
		p.Schema = 1
	}
	b, _ := json.Marshal(p)
	body := base64.RawURLEncoding.EncodeToString(b)
	sig, err := k.kp.Sign([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(body + "." + base64.RawURLEncoding.EncodeToString(sig) + "." + k.pub)
}

func decode(raw []byte, expected string) (policyintake.Doc, error) {
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		return policyintake.Doc{}, fmt.Errorf("%w: the document is not signed", policyintake.ErrSignature)
	}
	if parts[2] != expected {
		return policyintake.Doc{}, fmt.Errorf("%w: wrong signer", policyintake.ErrSignature)
	}
	pk, err := nkeys.FromPublicKey(expected)
	if err != nil {
		return policyintake.Doc{}, fmt.Errorf("%w: %v", policyintake.ErrSignature, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || pk.Verify([]byte(parts[0]), sig) != nil {
		return policyintake.Doc{}, fmt.Errorf("%w: bad signature", policyintake.ErrSignature)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	var p payload
	if err != nil || json.Unmarshal(b, &p) != nil {
		return policyintake.Doc{}, fmt.Errorf("%w: malformed", policyintake.ErrDocument)
	}
	if p.Schema != 1 {
		return policyintake.Doc{}, fmt.Errorf("%w: unknown schema %d", policyintake.ErrDocument, p.Schema)
	}
	return policyintake.Doc{Account: p.Account, Range: p.Range, Generation: p.Generation, BaseGeneration: p.BaseGeneration,
		Verified: !p.Unverified, NextSubmitter: p.NextSubmitter}, nil
}

type fakeApplier struct {
	mu      sync.Mutex
	docs    []policyintake.Doc
	err     error
	written bool
	// onApply, when set, runs first inside Apply (a seam for a concurrent change).
	onApply func(ctx context.Context)
}

func (f *fakeApplier) Apply(ctx context.Context, d policyintake.Doc) (policyintake.Result, error) {
	f.mu.Lock()
	hook := f.onApply
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs = append(f.docs, d)
	if f.err != nil {
		return policyintake.Result{StatusWritten: f.written}, f.err
	}
	return policyintake.Result{StatusWritten: true}, nil
}

func (f *fakeApplier) applied() []policyintake.Doc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]policyintake.Doc(nil), f.docs...)
}

// inForce is the digest of the last document the applier took without error.
func (f *fakeApplier) inForce(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil || len(f.docs) == 0 {
		return "", nil
	}
	h := sha256.Sum256(f.docs[len(f.docs)-1].Raw)
	return hex.EncodeToString(h[:]), nil
}

type fakeReporter struct {
	mu sync.Mutex
	rs []policyintake.Refusal
}

func (f *fakeReporter) Refused(_ context.Context, r policyintake.Refusal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rs = append(f.rs, r)
	return nil
}

func (f *fakeReporter) refusals() []policyintake.Refusal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]policyintake.Refusal(nil), f.rs...)
}

// flakyMarker wraps a MarkerStore and fails its first calls.
type flakyMarker struct {
	policyintake.MarkerStore
	mu                                    sync.Mutex
	currentFails, setNextFails, promFails int
	setNextCalls, promoteCalls            int
	currentCalls                          int
	// onCurrent, when set, runs before each Current is answered, with the 1-based call number.
	onCurrent func(n int)
}

func (f *flakyMarker) Current(ctx context.Context) (store.Marker, uint64, bool, error) {
	f.mu.Lock()
	f.currentCalls++
	n, hook := f.currentCalls, f.onCurrent
	if f.currentFails > 0 {
		f.currentFails--
		f.mu.Unlock()
		return store.Marker{}, 0, false, errors.New("marker store down")
	}
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return f.MarkerStore.Current(ctx)
}

func (f *flakyMarker) SetNext(ctx context.Context, next string, expectedRev, ownRev uint64) (uint64, error) {
	f.mu.Lock()
	f.setNextCalls++
	if f.setNextFails > 0 {
		f.setNextFails--
		f.mu.Unlock()
		return 0, errors.New("marker write down")
	}
	f.mu.Unlock()
	return f.MarkerStore.SetNext(ctx, next, expectedRev, ownRev)
}

func (f *flakyMarker) Promote(ctx context.Context, next string, expectedRev, ownRev uint64) (uint64, error) {
	f.mu.Lock()
	f.promoteCalls++
	if f.promFails > 0 {
		f.promFails--
		f.mu.Unlock()
		return 0, errors.New("marker write down")
	}
	f.mu.Unlock()
	return f.MarkerStore.Promote(ctx, next, expectedRev, ownRev)
}

// testLock wraps an AccountLock, counts locks and releases, can report the lock busy, and can run a hook before
// the lock is taken.
type testLock struct {
	inner    policyintake.AccountLock
	mu       sync.Mutex
	locks    int
	releases int
	busy     int // the next calls fail with admin.ErrAccountBusy
	attempts int
	before   func()
}

func (l *testLock) Lock(ctx context.Context) (policyintake.Release, func(context.Context) error, error) {
	l.mu.Lock()
	l.attempts++
	if l.busy > 0 {
		l.busy--
		l.mu.Unlock()
		return nil, nil, fmt.Errorf("%w (test)", admin.ErrAccountBusy)
	}
	before := l.before
	l.mu.Unlock()
	if before != nil {
		before()
	}
	rel, held, err := l.inner.Lock(ctx)
	if err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	l.locks++
	l.mu.Unlock()
	return func() {
		rel()
		l.mu.Lock()
		l.releases++
		l.mu.Unlock()
	}, held, nil
}

func (l *testLock) counts() (locks, releases int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.locks, l.releases
}

// neverLock is a lock that never blocks and is always held: the lock deliberately disabled.
type neverLock struct{}

func (neverLock) Lock(context.Context) (policyintake.Release, func(context.Context) error, error) {
	return func() {}, func(context.Context) error { return nil }, nil
}

type rig struct {
	lock      *testLock
	t         *testing.T
	env       *testenv.Env
	adm       *admin.Admin
	kv        jetstream.KeyValue
	account   string
	submitter keyPair
	marker    *flakyMarker
	app       *fakeApplier
	rep       *fakeReporter
	logbuf    *syncBuf
	inForceFn func(context.Context) (string, error)
	by        string
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// newRig starts a server, creates the policy bucket and adopts an account with a fresh submitter.
func newRig(t *testing.T, adopt bool) *rig {
	t.Helper()
	e := testenv.Start(t)
	nc := e.Conn(t, e.AdminCreds)
	a, err := admin.New(nc, e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(nc)
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "homerange_" + testRange + "_policy", History: 8})
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	r := &rig{t: t, env: e, adm: a, kv: kv, account: apub, submitter: newUser(t), app: &fakeApplier{}, rep: &fakeReporter{}, logbuf: &syncBuf{}}
	r.by = newUser(t).pub
	if adopt {
		if _, _, err := a.Adopt(ctx, apub, r.submitter.pub, r.by, nil); err != nil {
			t.Fatal(err)
		}
	}
	r.marker = &flakyMarker{MarkerStore: policyintake.AdminMarker{Admin: a}}
	r.lock = &testLock{inner: policyintake.AdminLock{Admin: a}}
	r.inForceFn = r.app.inForce
	return r
}

func (r *rig) intake() *policyintake.Intake {
	return &policyintake.Intake{Account: r.account, Range: testRange, KV: r.kv, Marker: r.marker, Lock: r.lock, Decode: decode,
		InForce: func(ctx context.Context) (string, error) { return r.inForceFn(ctx) }, Apply: r.app, Report: r.rep,
		Log: log.New(r.logbuf, "", 0), RetryInitial: retryInitial, RetryMax: retryMax}
}

// start runs the intake until the returned stop is called (or the test ends); stop waits for Run's result.
func (r *rig) start(in *policyintake.Intake) (stop func() error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	var once sync.Once
	var res error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case res = <-done:
			case <-time.After(waitLimit):
				r.t.Error("Run did not return after the context ended")
			}
		})
		return res
	}
	r.t.Cleanup(func() { _ = stop() })
	return stop
}

func (r *rig) put(raw []byte) uint64 {
	r.t.Helper()
	rev, err := r.kv.Put(context.Background(), policyintake.KeyPrefix+r.account, raw)
	if err != nil {
		r.t.Fatal(err)
	}
	return rev
}

func (r *rig) doc(gen, base uint64) payload {
	return payload{Account: r.account, Range: testRange, Generation: gen, BaseGeneration: base}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(pollEvery)
	}
}

func (r *rig) waitApplied(n int) {
	r.t.Helper()
	eventually(r.t, fmt.Sprintf("%d applied", n), func() bool { return len(r.app.applied()) >= n })
}

func (r *rig) waitRefused(n int) []policyintake.Refusal {
	r.t.Helper()
	eventually(r.t, fmt.Sprintf("%d refusals", n), func() bool { return len(r.rep.refusals()) >= n })
	return r.rep.refusals()
}

func (r *rig) current() store.Marker {
	m, found, err := r.adm.Adoption(context.Background())
	if err != nil || !found {
		r.t.Fatalf("marker: %v %v", found, err)
	}
	return m
}

// noMore checks nothing more is applied or reported after the intake has settled.
func (r *rig) settle(applied, refused int) {
	r.t.Helper()
	time.Sleep(settleTime)
	if got := len(r.app.applied()); got != applied {
		r.t.Errorf("applied %d documents, want %d", got, applied)
	}
	if got := len(r.rep.refusals()); got != refused {
		r.t.Errorf("%d refusals, want %d: %+v", got, refused, r.rep.refusals())
	}
}

func TestGoodDocAppliesOnceWithTheRightDoc(t *testing.T) {
	r := newRig(t, true)
	stop := r.start(r.intake())
	raw := sign(t, r.submitter, r.doc(1, 0))
	r.put(raw)
	r.waitApplied(1)
	d := r.app.applied()[0]
	if d.Account != r.account || d.Range != testRange || d.Generation != 1 || d.BaseGeneration != 0 || !d.Verified || !bytes.Equal(d.Raw, raw) {
		t.Fatalf("doc %+v", d)
	}
	r.settle(1, 0)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestTwoDocsApplyInOrder(t *testing.T) {
	r := newRig(t, true)
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	r.put(sign(t, r.submitter, r.doc(3, 2)))
	r.waitApplied(3)
	for i, d := range r.app.applied() {
		if d.Generation != uint64(i+1) {
			t.Fatalf("order: %d at %d", d.Generation, i)
		}
	}
	r.settle(3, 0)
}

func TestRefusals(t *testing.T) {
	other := newUser(t)
	other2 := newUser(t)
	tests := []struct {
		name  string
		raw   func(r *rig) []byte
		stage string
		gen   uint64
	}{
		{"bad signature", func(r *rig) []byte {
			raw := sign(t, r.submitter, r.doc(1, 0))
			parts := strings.Split(string(raw), ".")
			p2 := r.doc(9, 0)
			b, _ := json.Marshal(p2)
			parts[0] = base64.RawURLEncoding.EncodeToString(b) // body changed, signature kept
			return []byte(strings.Join(parts, "."))
		}, policyintake.StageSignature, 0},
		{"unsigned raw json", func(r *rig) []byte { b, _ := json.Marshal(r.doc(1, 0)); return b }, policyintake.StageSignature, 0},
		{"wrong signer", func(r *rig) []byte { return sign(t, other, r.doc(1, 0)) }, policyintake.StageSignature, 0},
		{"unknown schema", func(r *rig) []byte { p := r.doc(1, 0); p.Schema = 7; return sign(t, r.submitter, p) }, policyintake.StageDocument, 0},
		{"malformed", func(r *rig) []byte {
			body := base64.RawURLEncoding.EncodeToString([]byte("{not json"))
			sig, _ := r.submitter.kp.Sign([]byte(body))
			return []byte(body + "." + base64.RawURLEncoding.EncodeToString(sig) + "." + r.submitter.pub)
		}, policyintake.StageDocument, 0},
		{"not verified", func(r *rig) []byte { p := r.doc(4, 0); p.Unverified = true; return sign(t, r.submitter, p) }, policyintake.StageSignature, 4},
		{"account mismatch", func(r *rig) []byte { p := r.doc(5, 0); p.Account = other2.pub; return sign(t, r.submitter, p) }, policyintake.StageAccount, 5},
		{"range mismatch", func(r *rig) []byte { p := r.doc(6, 0); p.Range = "bar"; return sign(t, r.submitter, p) }, policyintake.StageRange, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, true)
			r.start(r.intake())
			raw := tc.raw(r)
			r.put(raw)
			rs := r.waitRefused(1)
			got := rs[0]
			h := sha256.Sum256(raw)
			if got.Stage != tc.stage || got.Generation != tc.gen || got.Account != r.account || got.Digest != hex.EncodeToString(h[:]) || got.Detail == "" {
				t.Fatalf("refusal %+v", got)
			}
			r.settle(0, 1)
		})
	}
}

func TestNotAdoptedReportsAndNeverApplies(t *testing.T) {
	r := newRig(t, false)
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageAdoption || rs[0].Detail != "this account is not adopted: list sets are ignored" {
		t.Fatalf("%+v", rs[0])
	}
	r.settle(0, 1)
}

// The marker is read for every revision: after a recovery adopt between two documents the old submitter is refused.
func TestOldSubmitterRefusedAfterRecoveryAdopt(t *testing.T) {
	r := newRig(t, true)
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	newSub := newUser(t)
	if _, action, err := r.adm.Adopt(context.Background(), r.account, newSub.pub, r.by, nil); err != nil || action != store.ActionRecover {
		t.Fatal(action, err)
	}
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[0])
	}
	r.put(sign(t, newSub, r.doc(2, 1)))
	r.waitApplied(2)
	r.settle(2, 1)
}

// A restart replays the applied document: it is skipped, with no Apply and no refusal.
func TestReplayOfAppliedDocIsSkipped(t *testing.T) {
	r := newRig(t, true)
	stop := r.start(r.intake())
	raw := sign(t, r.submitter, r.doc(1, 0))
	r.put(raw)
	r.waitApplied(1)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	r.start(r.intake()) // a new Intake over the same bucket
	r.settle(1, 0)
	if !strings.Contains(r.logbuf.String(), "debug") {
		t.Error("the skip was not logged")
	}
	// A new document after the restart still applies.
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	r.waitApplied(2)
	r.settle(2, 0)
}

func TestMarkerReadErrorRetriesThenApplies(t *testing.T) {
	r := newRig(t, true)
	r.marker.currentFails = 3
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	r.settle(1, 0)
	if n := strings.Count(r.logbuf.String(), "reading the adoption marker failed"); n != 3 {
		t.Errorf("logged %d attempts, want 3", n)
	}
}

func TestMarkerReadErrorAbandonedForNewerRevision(t *testing.T) {
	r := newRig(t, true)
	r.marker.currentFails = 1 << 30
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	eventually(t, "a retry", func() bool { return strings.Contains(r.logbuf.String(), "failed (attempt 2)") })
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	eventually(t, "revision 1 abandoned", func() bool { return strings.Contains(r.logbuf.String(), "abandoned") })
	r.marker.mu.Lock()
	r.marker.currentFails = 0
	r.marker.mu.Unlock()
	r.waitApplied(1)
	r.settle(1, 0)
	if d := r.app.applied()[0]; d.Generation != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestInForceErrorRetries(t *testing.T) {
	r := newRig(t, true)
	var mu sync.Mutex
	fails := 3
	r.inForceFn = func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return "", errors.New("status unreadable")
		}
		return r.app.inForce(ctx)
	}
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	r.settle(1, 0)
	if n := strings.Count(r.logbuf.String(), "reading the document in force failed"); n != 3 {
		t.Errorf("logged %d attempts, want 3", n)
	}
}

func TestApplyErrorReporting(t *testing.T) {
	long := strings.Repeat("x", 2000)
	t.Run("status not written is reported once, bounded", func(t *testing.T) {
		r := newRig(t, true)
		r.app.err = errors.New(long)
		r.start(r.intake())
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		rs := r.waitRefused(1)
		if rs[0].Stage != policyintake.StageApply || rs[0].Generation != 1 || len(rs[0].Detail) > policyintake.DetailMaxBytes {
			t.Fatalf("%+v", rs[0])
		}
		r.settle(1, 1)
	})
	t.Run("status written is only logged", func(t *testing.T) {
		r := newRig(t, true)
		r.app.err, r.app.written = errors.New("stale base"), true
		r.start(r.intake())
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		r.waitApplied(1)
		r.settle(1, 0)
		if !strings.Contains(r.logbuf.String(), "stale base") {
			t.Error("not logged")
		}
	})
}

func TestPlannedRotationEndToEnd(t *testing.T) {
	r := newRig(t, true)
	next := newUser(t)
	r.start(r.intake())

	// Doc 1 carries next_submitter: applied, then the marker has it and the old key stays the submitter.
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	r.waitApplied(1)
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	m := r.current()
	if m.Submitter != r.submitter.pub || m.Action != store.ActionRotate || m.By != r.by {
		t.Fatalf("%+v", m)
	}

	// The old key's next document is refused from then on.
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature || strings.Contains(rs[0].Detail, r.submitter.pub) {
		t.Fatalf("%+v", rs[0])
	}

	// The new key's document applies, and the marker is promoted.
	p3 := r.doc(2, 1)
	p3.NextSubmitter = newUser(t).pub // ignored: it starts a rotation only on the next document
	r.put(sign(t, next, p3))
	r.waitApplied(2)
	eventually(t, "promoted", func() bool { m := r.current(); return m.Submitter == next.pub && m.NextSubmitter == "" })
	if m := r.current(); m.Action != store.ActionRotate {
		t.Fatalf("%+v", m)
	}

	// After promotion the old key is refused too.
	r.put(sign(t, r.submitter, r.doc(3, 2)))
	rs = r.waitRefused(2)
	if rs[1].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[1])
	}
	// And the new key works.
	r.put(sign(t, next, r.doc(3, 2)))
	r.waitApplied(3)
	r.settle(3, 2)
	hist, _ := r.kvHistory()
	if hist != 3 { // adopt, set next, promote
		t.Errorf("marker history %d, want 3", hist)
	}
}

func (r *rig) kvHistory() (int, error) {
	nc := r.env.Conn(r.t, r.env.AdminCreds)
	js, _ := jetstream.New(nc)
	kv, err := js.KeyValue(context.Background(), store.AdoptionBucket)
	if err != nil {
		return 0, err
	}
	h, err := kv.History(context.Background(), store.AdoptionKey)
	return len(h), err
}

func TestRotationKeepsTheExemptSet(t *testing.T) {
	r := newRig(t, false)
	ex := newUser(t).pub
	if _, _, err := r.adm.Adopt(context.Background(), r.account, r.submitter.pub, r.by, []string{ex}); err != nil {
		t.Fatal(err)
	}
	next := newUser(t)
	r.start(r.intake())
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	r.put(sign(t, next, r.doc(2, 1)))
	eventually(t, "promoted", func() bool { return r.current().Submitter == next.pub })
	if m := r.current(); len(m.Exempt) != 1 || m.Exempt[0] != ex || m.Account != r.account {
		t.Fatalf("%+v", m)
	}
}

func TestInvalidNextSubmitterIsRefusedBeforeApply(t *testing.T) {
	r := newRig(t, true)
	r.start(r.intake())
	bad := []string{"hello", r.submitter.pub, r.account, r.by}
	for i, b := range bad {
		p := r.doc(uint64(i+1), 0)
		p.NextSubmitter = b
		r.put(sign(t, r.submitter, p))
	}
	rs := r.waitRefused(len(bad))
	for _, x := range rs {
		if x.Stage != policyintake.StageDocument || !strings.Contains(x.Detail, "next_submitter") {
			t.Fatalf("%+v", x)
		}
	}
	r.settle(0, len(bad))
	if m := r.current(); m.NextSubmitter != "" {
		t.Fatal("a next was recorded")
	}
}

func TestSetNextFailureRetriesUntilItWorks(t *testing.T) {
	r := newRig(t, true)
	r.marker.setNextFails = 4
	next := newUser(t)
	r.start(r.intake())
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	r.waitApplied(1)
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	r.marker.mu.Lock()
	calls := r.marker.setNextCalls
	r.marker.mu.Unlock()
	if calls != 5 {
		t.Errorf("SetNext called %d times, want 5", calls)
	}
	r.settle(1, 0) // the applied status stands: nothing reported
	if !strings.Contains(r.logbuf.String(), "ERROR") {
		t.Error("the failure was not logged loudly")
	}
}

func TestPromoteFailureRetries(t *testing.T) {
	r := newRig(t, true)
	next := newUser(t)
	r.marker.promFails = 2
	r.start(r.intake())
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	r.put(sign(t, next, r.doc(2, 1)))
	eventually(t, "promoted", func() bool { return r.current().Submitter == next.pub })
	r.settle(2, 0)
}

// A recovery adopt while a promotion is being retried ends the retry: the marker moved.
func TestPromoteStopsWhenTheMarkerMoved(t *testing.T) {
	r := newRig(t, true)
	next := newUser(t)
	r.start(r.intake())
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	_, staleRev, _, _ := r.marker.Current(context.Background())
	// Recovery adopt clears the pending key; a doc under it can no longer be verified, so go to Promote directly.
	rec := newUser(t)
	if _, _, err := r.adm.Adopt(context.Background(), r.account, rec.pub, r.by, nil); err != nil {
		t.Fatal(err)
	}
	_, err := r.marker.Promote(context.Background(), next.pub, staleRev, 0)
	if !errors.Is(err, policyintake.ErrMarkerMoved) || !errors.Is(err, admin.ErrMarkerMoved) {
		t.Fatalf("%v", err)
	}
	if m := r.current(); m.Submitter != rec.pub {
		t.Fatalf("%+v", m)
	}
}

func TestDeleteAndPurgeAreIgnored(t *testing.T) {
	r := newRig(t, true)
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	key := policyintake.KeyPrefix + r.account
	if err := r.kv.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	r.put(sign(t, r.submitter, r.doc(2, 1)))
	r.waitApplied(2)
	if err := r.kv.Purge(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	r.settle(2, 0)
	if n := strings.Count(r.logbuf.String(), "delete or purge"); n != 2 {
		t.Errorf("logged %d, want 2", n)
	}
}

func TestCancelReturnsNilAndLeaksNothing(t *testing.T) {
	r := newRig(t, true)
	before := runtime.NumGoroutine()
	stop := r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	if err := stop(); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	eventually(t, "goroutines to end", func() bool { return runtime.NumGoroutine() <= before+goroutineSlop })
}

func TestRunFailsWhenTheWatchCannotBeCreated(t *testing.T) {
	r := newRig(t, true)
	in := r.intake()
	in.KV = nil
	if err := in.Run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	in = r.intake()
	in.Account = ""
	if err := in.Run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

// What the reporter and the log receive: never the raw document, never a full key.
func TestReporterNeverSeesRawBytesOrFullKeys(t *testing.T) {
	r := newRig(t, true)
	wrong := newUser(t)
	// A decoder whose message carries a full key, as a careless adapter's could.
	in := r.intake()
	in.Decode = func(raw []byte, expected string) (policyintake.Doc, error) {
		return policyintake.Doc{}, fmt.Errorf("%w: key %s and signer %s, body %s", policyintake.ErrDocument, wrong.pub, expected, string(raw))
	}
	r.start(in)
	raw := sign(t, r.submitter, r.doc(1, 0))
	r.put(raw)
	rs := r.waitRefused(1)
	for _, s := range []string{rs[0].Detail, r.logbuf.String()} {
		if strings.Contains(s, wrong.pub) || strings.Contains(s, r.submitter.pub) {
			t.Fatalf("a full key leaked: %q", s)
		}
	}
	if len(rs[0].Detail) > policyintake.DetailMaxBytes {
		t.Fatal("detail too long")
	}
	if !strings.Contains(rs[0].Detail, wrong.pub[:8]) {
		t.Errorf("the short key is missing: %q", rs[0].Detail)
	}
}

func TestSignatureRefusalNamesOnlyAShortKey(t *testing.T) {
	r := newRig(t, true)
	r.start(r.intake())
	r.put(sign(t, newUser(t), r.doc(1, 0)))
	rs := r.waitRefused(1)
	if strings.Contains(rs[0].Detail, r.submitter.pub) || !strings.Contains(rs[0].Detail, r.submitter.pub[:8]) {
		t.Fatalf("%q", rs[0].Detail)
	}
}

// --- the account lock and the marker revision (ADR 0112, Concurrency) ---

// recoveryAdopt replaces the submitter, as `mashctl policy adopt --submitter` does.
func (r *rig) recoveryAdopt(rec keyPair) error {
	_, _, err := r.adm.Adopt(context.Background(), r.account, rec.pub, r.by, nil)
	return err
}

// A recovery adopt attempted while an apply is in flight waits for the apply (the account lock); the document that
// was verified under the old marker completes, and the adopt lands after it.
func TestRecoveryAdoptWaitsForAnApplyInFlight(t *testing.T) {
	r := newRig(t, true)
	rec := newUser(t)
	adopted := make(chan error, 1)
	var early atomicBool
	r.app.onApply = func(context.Context) {
		go func() { adopted <- r.recoveryAdopt(rec) }()
		select {
		case err := <-adopted:
			early.set()
			adopted <- err
		case <-time.After(600 * time.Millisecond):
		}
	}
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	select {
	case err := <-adopted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitLimit):
		t.Fatal("the recovery adopt never completed after the apply returned")
	}
	if early.get() {
		t.Fatal("the recovery adopt completed while the apply was in flight")
	}
	if m := r.current(); m.Submitter != rec.pub {
		t.Fatalf("the adopt did not land: %+v", m)
	}
	r.settle(1, 0)
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) set()      { a.mu.Lock(); a.v = true; a.mu.Unlock() }
func (a *atomicBool) get() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// A recovery adopt that completes before the intake takes the lock makes the old submitter's document a refusal.
func TestRecoveryAdoptBeforeTheLockRefusesTheOldSubmitter(t *testing.T) {
	r := newRig(t, true)
	rec := newUser(t)
	var once sync.Once
	r.lock.before = func() {
		once.Do(func() {
			if err := r.recoveryAdopt(rec); err != nil {
				t.Error(err)
			}
		})
	}
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[0])
	}
	r.settle(0, 1)
}

// With the lock deliberately disabled, a marker that moves between the verification and the apply stops the apply;
// the revision is processed again and the old submitter's document is refused.
func TestMarkerRevisionRecheckPreventsTheApply(t *testing.T) {
	r := newRig(t, true)
	rec := newUser(t)
	r.marker.onCurrent = func(n int) {
		if n == 2 { // the re-read just before the apply
			if err := r.recoveryAdopt(rec); err != nil {
				t.Error(err)
			}
		}
	}
	in := r.intake()
	in.Lock = neverLock{}
	r.start(in)
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	rs := r.waitRefused(1)
	if rs[0].Stage != policyintake.StageSignature {
		t.Fatalf("%+v", rs[0])
	}
	r.settle(0, 1)
	if !strings.Contains(r.logbuf.String(), "processing it again") {
		t.Error("the reprocess was not logged")
	}
}

// A lock that is no longer held before the apply stops the apply too, and the revision is processed again.
func TestLostLockPreventsTheApply(t *testing.T) {
	r := newRig(t, true)
	var lost atomicBool
	lk := &heldLock{lost: &lost}
	in := r.intake()
	in.Lock = lk
	lk.onHeld = func() { lost.set() }
	r.start(in)
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1) // the second pass finds the lock held again and applies
	if lk.heldCalls() < 2 {
		t.Errorf("Held was called %d times, want a recheck", lk.heldCalls())
	}
	r.settle(1, 0)
}

// heldLock reports the lock lost on its first Held check.
type heldLock struct {
	mu     sync.Mutex
	n      int
	lost   *atomicBool
	onHeld func()
}

func (h *heldLock) Lock(context.Context) (policyintake.Release, func(context.Context) error, error) {
	return func() {}, func(context.Context) error {
		h.mu.Lock()
		h.n++
		n := h.n
		h.mu.Unlock()
		if n == 1 {
			return errors.New("the lock was lost")
		}
		return nil
	}, nil
}

func (h *heldLock) heldCalls() int { h.mu.Lock(); defer h.mu.Unlock(); return h.n }

// H2: the marker moved after the apply and before the rotation write: nothing is recorded and the recovery key
// stays the expected signer.
func TestSetNextAbandonedWhenTheMarkerMovedAfterTheApply(t *testing.T) {
	r := newRig(t, true)
	rec, next := newUser(t), newUser(t)
	r.app.onApply = func(context.Context) {
		if err := r.recoveryAdopt(rec); err != nil {
			t.Error(err)
		}
	}
	in := r.intake()
	in.Lock = neverLock{}
	r.start(in)
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	r.waitApplied(1)
	eventually(t, "the abandonment to be logged", func() bool { return strings.Contains(r.logbuf.String(), "abandoned") })
	r.settle(1, 0)
	m := r.current()
	if m.NextSubmitter != "" || m.Submitter != rec.pub || m.Action != store.ActionRecover {
		t.Fatalf("the old document's next key was recorded or the recovery key lost: %+v", m)
	}
	r.marker.mu.Lock()
	calls := r.marker.setNextCalls
	r.marker.mu.Unlock()
	if calls != 1 {
		t.Errorf("SetNext called %d times, want 1 (never retried blindly)", calls)
	}
	if !strings.Contains(r.logbuf.String(), "ERROR") {
		t.Error("not logged loudly")
	}
}

// H2 for the promotion.
func TestPromoteAbandonedWhenTheMarkerMovedAfterTheApply(t *testing.T) {
	r := newRig(t, true)
	rec, next := newUser(t), newUser(t)
	in := r.intake()
	in.Lock = neverLock{}
	r.start(in)
	p := r.doc(1, 0)
	p.NextSubmitter = next.pub
	r.put(sign(t, r.submitter, p))
	eventually(t, "next recorded", func() bool { return r.current().NextSubmitter == next.pub })
	r.app.mu.Lock()
	r.app.onApply = func(context.Context) {
		if err := r.recoveryAdopt(rec); err != nil {
			t.Error(err)
		}
	}
	r.app.mu.Unlock()
	r.put(sign(t, next, r.doc(2, 1)))
	r.waitApplied(2)
	eventually(t, "the abandonment to be logged", func() bool { return strings.Contains(r.logbuf.String(), "abandoned") })
	r.settle(2, 0)
	if m := r.current(); m.Submitter != rec.pub || m.NextSubmitter != "" {
		t.Fatalf("%+v", m)
	}
	r.marker.mu.Lock()
	calls := r.marker.promoteCalls
	r.marker.mu.Unlock()
	if calls != 1 {
		t.Errorf("Promote called %d times, want 1", calls)
	}
}

// The lock is released on every path.
func TestLockIsReleasedOnEveryPath(t *testing.T) {
	check := func(t *testing.T, r *rig, wantAtLeast int) {
		t.Helper()
		eventually(t, "the lock to be released", func() bool {
			l, rel := r.lock.counts()
			return l >= wantAtLeast && l == rel
		})
	}
	t.Run("refusal", func(t *testing.T) {
		r := newRig(t, true)
		r.start(r.intake())
		r.put(sign(t, newUser(t), r.doc(1, 0)))
		r.waitRefused(1)
		check(t, r, 1)
	})
	t.Run("apply error", func(t *testing.T) {
		r := newRig(t, true)
		r.app.err = errors.New("boom")
		r.start(r.intake())
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		r.waitRefused(1)
		check(t, r, 1)
	})
	t.Run("success", func(t *testing.T) {
		r := newRig(t, true)
		r.start(r.intake())
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		r.waitApplied(1)
		check(t, r, 1)
		// and an adopt can take the lock afterwards
		if err := r.recoveryAdopt(newUser(t)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not adopted", func(t *testing.T) {
		r := newRig(t, false)
		r.start(r.intake())
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		r.waitRefused(1)
		check(t, r, 1)
	})
	t.Run("context cancelled during the apply", func(t *testing.T) {
		r := newRig(t, true)
		in := r.intake()
		ctx, cancel := context.WithCancel(context.Background())
		entered := make(chan struct{})
		r.app.onApply = func(ctx context.Context) { close(entered); <-ctx.Done() }
		done := make(chan error, 1)
		go func() { done <- in.Run(ctx) }()
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		<-entered
		cancel()
		select {
		case <-done:
		case <-time.After(waitLimit):
			t.Fatal("Run did not return")
		}
		check(t, r, 1)
	})
	t.Run("panic in the apply", func(t *testing.T) {
		r := newRig(t, true)
		in := r.intake()
		ctx, cancel := context.WithCancel(context.Background())
		r.app.onApply = func(context.Context) { cancel(); panic("boom") }
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			_ = in.Run(ctx)
		}()
		r.put(sign(t, r.submitter, r.doc(1, 0)))
		select {
		case v := <-done:
			if v == nil {
				t.Fatal("no panic reached the caller")
			}
		case <-time.After(waitLimit):
			t.Fatal("Run did not return")
		}
		check(t, r, 1)
	})
}

// A busy lock is waited for, never a refusal: the document applies when the lock frees.
func TestBusyLockRetriesAndDoesNotRefuse(t *testing.T) {
	r := newRig(t, true)
	r.lock.busy = 3
	r.start(r.intake())
	r.put(sign(t, r.submitter, r.doc(1, 0)))
	r.waitApplied(1)
	r.settle(1, 0)
	r.lock.mu.Lock()
	attempts := r.lock.attempts
	r.lock.mu.Unlock()
	if attempts != 4 {
		t.Errorf("%d lock attempts, want 4", attempts)
	}
	if !strings.Contains(r.logbuf.String(), "an adoption or policy change is in progress") {
		t.Error("the wait was not logged")
	}
}
