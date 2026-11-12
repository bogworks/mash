package policyapply

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

// startJetStream runs an in-process JetStream server on a port it picks and returns its URL.
func startJetStream(t *testing.T) string {
	t.Helper()
	s, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("the JetStream server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	return s.ClientURL()
}

func connectJS(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// newJetStreamConn is a connection to a JetStream server of its own.
func newJetStreamConn(t *testing.T) *nats.Conn { return connectJS(t, startJetStream(t)) }

const kvRange = "r1"

var kvAccount = func() string {
	kp, _ := nkeys.CreateAccount()
	pub, _ := kp.PublicKey()
	return pub
}()

// kvEnv is a server with the policy bucket made, as operations makes it.
type kvEnv struct {
	t   *testing.T
	url string
	kv  jetstream.KeyValue
}

func newKVEnv(t *testing.T) *kvEnv {
	t.Helper()
	url := startJetStream(t)
	js, err := jetstream.New(connectJS(t, url))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{Bucket: PolicyBucket(kvRange), History: 8})
	if err != nil {
		t.Fatal(err)
	}
	return &kvEnv{t: t, url: url, kv: kv}
}

// ledger is a KVLedger on a connection of its own.
func (e *kvEnv) ledger(opts KVLedgerOptions) *KVLedger {
	e.t.Helper()
	js, err := jetstream.New(connectJS(e.t, e.url))
	if err != nil {
		e.t.Fatal(err)
	}
	l, err := NewKVLedger(context.Background(), js, kvRange, kvAccount, opts)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func testRecord(gen uint64) listset.Record {
	return listset.Record{Generation: gen, Received: listset.Digest{1}, Content: listset.Digest{2}, Signed: listset.Digest{3}}
}

func TestKVLedgerMissingBucket(t *testing.T) {
	js, err := jetstream.New(newJetStreamConn(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewKVLedger(context.Background(), js, "nope", kvAccount, KVLedgerOptions{})
	if !errors.Is(err, ErrNoPolicyBucket) {
		t.Fatalf("err = %v", err)
	}
	if _, err := js.KeyValue(context.Background(), PolicyBucket("nope")); err == nil {
		t.Error("the ledger created the bucket")
	}
}

func TestKVLedgerRecords(t *testing.T) {
	e := newKVEnv(t)
	l := e.ledger(KVLedgerOptions{})
	if rec, err := l.InForce(); err != nil || rec != (listset.Record{}) {
		t.Fatalf("empty: %+v %v", rec, err)
	}
	if err := l.CompareAndCommit(0, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	if rec, err := l.InForce(); err != nil || rec != testRecord(1) {
		t.Fatalf("in force: %+v %v", rec, err)
	}
	if err := l.CompareAndCommit(0, testRecord(2)); !errors.Is(err, listset.ErrStaleBase) {
		t.Fatalf("stale commit: %v", err)
	}
	if err := l.SetPending(testRecord(2)); err != nil {
		t.Fatal(err)
	}
	if rec, ok, err := l.Pending(); err != nil || !ok || rec != testRecord(2) {
		t.Fatalf("pending: %+v %v %v", rec, ok, err)
	}
	if rec, _ := e.ledger(KVLedgerOptions{}).InForce(); rec != testRecord(1) {
		t.Errorf("another ledger reads %+v", rec)
	}
	if err := l.ClearPending(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.Pending(); ok {
		t.Error("pending after clear")
	}
	if rec, _ := l.InForce(); rec != testRecord(1) {
		t.Errorf("clearing pending changed the record in force: %+v", rec)
	}
}

func TestKVLedgerUnknownSchemaRefused(t *testing.T) {
	e := newKVEnv(t)
	l := e.ledger(KVLedgerOptions{})
	if _, err := e.kv.Put(context.Background(), StatusKey(kvAccount), []byte(`{"schema":"mash.policy.status/9"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.InForce(); !errors.Is(err, ErrUnknownSchema) {
		t.Errorf("InForce: %v", err)
	}
	if err := l.CompareAndCommit(0, testRecord(1)); !errors.Is(err, ErrUnknownSchema) {
		t.Errorf("commit: %v", err)
	}
	if err := l.WriteStatus(context.Background(), Status{}); !errors.Is(err, ErrUnknownSchema) {
		t.Errorf("status: %v", err)
	}
	if err := l.SetPending(testRecord(1)); !errors.Is(err, ErrUnknownSchema) {
		t.Errorf("pending: %v", err)
	}
}

func TestKVLedgerLeaseTakeoverAndWait(t *testing.T) {
	e := newKVEnv(t)
	ctx := context.Background()
	// An expired lease of a crashed holder is taken over.
	old, _ := json.Marshal(leaseValue{Holder: "crashed", ExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)})
	if _, err := e.kv.Create(ctx, LeaseKey(kvAccount), old); err != nil {
		t.Fatal(err)
	}
	a, b := e.ledger(KVLedgerOptions{Holder: "a"}), e.ledger(KVLedgerOptions{Holder: "b", Poll: 20 * time.Millisecond})
	unlockA, err := a.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := e.kv.Get(ctx, LeaseKey(kvAccount))
	if err != nil {
		t.Fatal(err)
	}
	var lv leaseValue
	if err := json.Unmarshal(entry.Value(), &lv); err != nil || lv.Holder != "a" {
		t.Fatalf("lease %s %v", entry.Value(), err)
	}
	// A live lease is waited for until ctx is done, and then is free again after the holder unlocks.
	short, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := b.Lock(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock while held: %v", err)
	}
	got := make(chan error, 1)
	go func() {
		unlock, err := b.Lock(ctx)
		if err == nil {
			unlock()
		}
		got <- err
	}()
	time.Sleep(100 * time.Millisecond)
	unlockA()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("b never got the lease after a unlocked")
	}
}

func TestKVLedgerLeaseRenewedAndLost(t *testing.T) {
	e := newKVEnv(t)
	ctx := context.Background()
	a := e.ledger(KVLedgerOptions{Holder: "a", LeaseTTL: 300 * time.Millisecond})
	b := e.ledger(KVLedgerOptions{Holder: "b", Poll: 20 * time.Millisecond})
	unlock, err := a.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	// Held for several TTLs: renewal keeps it, so b cannot take it and a can still commit.
	time.Sleep(1 * time.Second)
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := b.Lock(short); err == nil {
		t.Fatal("b took a lease that a renews")
	}
	// Another writer changes the lease: the next renewal loses the compare-and-set, and a commit is refused.
	entry, err := e.kv.Get(ctx, LeaseKey(kvAccount))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.kv.Update(ctx, LeaseKey(kvAccount), entry.Value(), entry.Revision()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.Lock()
		lost := a.lost
		a.mu.Unlock()
		if lost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewal never noticed the lost lease")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := a.CompareAndCommit(0, testRecord(1)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("commit after loss: %v", err)
	}
	if rec, _ := a.InForce(); rec.Generation != 0 {
		t.Errorf("committed after the lease was lost: %+v", rec)
	}
}

// A commit is refused at once when the lease changed, without waiting for a renewal to notice.
func TestKVLedgerCommitChecksLease(t *testing.T) {
	e := newKVEnv(t)
	ctx := context.Background()
	a := e.ledger(KVLedgerOptions{Holder: "a"})
	unlock, err := a.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := a.CompareAndCommit(0, testRecord(1)); err != nil {
		t.Fatalf("commit under the lease: %v", err)
	}
	entry, _ := e.kv.Get(ctx, LeaseKey(kvAccount))
	if _, err := e.kv.Update(ctx, LeaseKey(kvAccount), entry.Value(), entry.Revision()); err != nil {
		t.Fatal(err)
	}
	if err := a.CompareAndCommit(1, testRecord(2)); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("commit after the lease changed: %v", err)
	}
}

// pushWatch counts the pushes of a cluster and the most that ran at once.
type pushWatch struct {
	Cluster
	active, max, total atomic.Int32
}

func (p *pushWatch) Push(ctx context.Context, jwt string, expect []string) ([]apply.ServerAck, error) {
	n := p.active.Add(1)
	defer p.active.Add(-1)
	p.total.Add(1)
	for {
		m := p.max.Load()
		if n <= m || p.max.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	return p.Cluster.Push(ctx, jwt, expect)
}

func TestKVLedgerConcurrentRunsSingleWinner(t *testing.T) {
	e := newKVEnv(t)
	w := newWorld(t)
	watch := &pushWatch{Cluster: w.cluster}
	ledgers := []*KVLedger{e.ledger(KVLedgerOptions{Poll: 10 * time.Millisecond}), e.ledger(KVLedgerOptions{Poll: 10 * time.Millisecond})}
	if err := ledgers[0].CompareAndCommit(0, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := w.run(ledgers[i], set2(), "bytes-"+string(rune('a'+i)))
			in.Cluster = watch
			_, errs[i] = Run(context.Background(), in)
		}()
	}
	wg.Wait()
	var ok, stale int
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, listset.ErrStaleBase), errors.Is(err, ErrPendingGeneration):
			stale++
		default:
			t.Errorf("unexpected error %v", err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("ok=%d stale=%d (%v)", ok, stale, errs)
	}
	if watch.max.Load() > 1 {
		t.Errorf("%d pushes ran at once", watch.max.Load())
	}
	if rec, _ := ledgers[1].InForce(); rec.Generation != 2 {
		t.Errorf("in force %d", rec.Generation)
	}
}

// statusOf reads the status value with a plain KV Get.
func (e *kvEnv) statusOf() map[string]any {
	e.t.Helper()
	entry, err := e.kv.Get(context.Background(), StatusKey(kvAccount))
	if err != nil {
		e.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(entry.Value(), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestKVLedgerStatusWritten(t *testing.T) {
	e := newKVEnv(t)
	w := newWorld(t)
	l := e.ledger(KVLedgerOptions{})
	if err := l.CompareAndCommit(0, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	st, err := Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil || st.State != StateApplied {
		t.Fatalf("%+v %v", st, err)
	}
	m := e.statusOf()
	s, _ := m["status"].(map[string]any)
	if m["schema"] != StatusSchema || s["state"] != "applied" || s["in_force"] != true || s["generation"] != float64(2) ||
		s["content"] != st.Content || s["signed"] != st.Signed || s["updated_at"] == "" {
		t.Fatalf("applied value = %v", m)
	}
	for _, k := range []string{"refusals", "servers", "not_connected", "failing"} {
		if _, ok := s[k].([]any); !ok {
			t.Errorf("status.%s = %v, want a list", k, s[k])
		}
	}
	if f, _ := m["in_force"].(map[string]any); f["generation"] != float64(2) {
		t.Errorf("record in force = %v", m["in_force"])
	}
	if _, has := m["pending"]; has {
		t.Errorf("pending left after an applied run: %v", m["pending"])
	}

	// A partial run: a check fails, nothing is committed, the status says partial and the record in force stays.
	w2 := newWorld(t)
	w2.prober.allow["s1"] = true
	l2 := e.ledger(KVLedgerOptions{})
	if err := l2.CompareAndCommit(2, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	st, err = Run(context.Background(), w2.run(l2, set2(), "bytes-2"))
	if err != nil || st.State != StatePartial {
		t.Fatalf("%+v %v", st, err)
	}
	m = e.statusOf()
	s, _ = m["status"].(map[string]any)
	if s["state"] != "partial" || s["in_force"] != false {
		t.Fatalf("partial value = %v", m)
	}
	if f, _ := m["in_force"].(map[string]any); f["generation"] != float64(1) {
		t.Errorf("record in force = %v", m["in_force"])
	}
	if p, _ := m["pending"].(map[string]any); p["generation"] != float64(2) {
		t.Errorf("pending = %v", m["pending"])
	}
	if failing, _ := s["failing"].([]any); len(failing) == 0 {
		t.Errorf("no failures in %v", s)
	}
}

func TestKVLedgerStatusRefusedWritten(t *testing.T) {
	e := newKVEnv(t)
	w := newWorld(t)
	l := e.ledger(KVLedgerOptions{})
	if err := l.CompareAndCommit(0, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	ls := set2()
	ls.Senders = append(ls.Senders, listset.SenderLists{Sender: "bar.c.x"}) // refused: a sender outside the range
	st, err := Run(context.Background(), w.run(l, ls, "bytes-bad"))
	if err != nil || st.State != StateRefused {
		t.Fatalf("%+v %v", st, err)
	}
	if s, _ := e.statusOf()["status"].(map[string]any); s["state"] != "refused" {
		t.Fatalf("status = %v", s)
	}
}

// fakeIssued is an IssuedAdmin over a map.
type fakeIssued struct {
	recs  map[string]store.Issued
	order []string
	err   error
}

func (f *fakeIssued) RecordIssued(_ context.Context, r store.Issued) error {
	if f.err != nil {
		return f.err
	}
	if f.recs == nil {
		f.recs = map[string]store.Issued{}
	}
	if cur, ok := f.recs[r.PublicKey]; ok {
		r.RevokedAt = cur.RevokedAt
	} else {
		f.order = append(f.order, r.PublicKey)
	}
	if r.Purpose == "" {
		r.Purpose = store.PurposeIssue
	}
	f.recs[r.PublicKey] = r
	return nil
}

func (f *fakeIssued) MarkRevoked(_ context.Context, pub string, at time.Time) error {
	r, ok := f.recs[pub]
	if !ok {
		return admin.ErrNotRecorded
	}
	if r.RevokedAt == "" {
		r.RevokedAt = at.UTC().Format(time.RFC3339)
		f.recs[pub] = r
	}
	return nil
}

func (f *fakeIssued) Issued(context.Context) ([]store.Issued, bool, error) {
	var out []store.Issued
	for _, k := range f.order {
		out = append(out, f.recs[k])
	}
	return out, true, f.err
}

func TestAdminIssueRecordMapping(t *testing.T) {
	f := &fakeIssued{}
	r := NewAdminIssueRecord(f)
	ctx := context.Background()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("x", 3600))
	if err := r.Add(ctx, IssueEntry{Address: "foo.c.core", PublicKey: "UA", Purpose: PurposeProbe, Tags: []string{"t1"},
		Generation: 7, IssuedAt: at, Expires: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got := f.recs["UA"]
	want := store.Issued{PublicKey: "UA", Address: "foo.c.core", Owner: "c", Kind: store.IssuedEndpoint, Tags: []string{"t1"},
		Generation: 7, IssuedAt: "2026-03-04T04:06:07Z", Purpose: store.PurposeProbe}
	if got.PublicKey != want.PublicKey || got.Address != want.Address || got.Owner != want.Owner || got.Kind != want.Kind ||
		got.Generation != want.Generation || got.IssuedAt != want.IssuedAt || got.Purpose != want.Purpose || len(got.Tags) != 1 {
		t.Fatalf("stored %+v, want %+v", got, want)
	}
	if err := r.Add(ctx, IssueEntry{Address: "foo.c.core", PublicKey: "UA"}); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("duplicate: %v", err)
	}
	if err := r.MarkRevoked(ctx, "UA", at); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(ctx, IssueEntry{Address: "foo.c.core", PublicKey: "UA"}); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("revoked: %v", err)
	}
	if err := r.MarkRevoked(ctx, "UZ", at); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown: %v", err)
	}
	if err := r.Add(ctx, IssueEntry{Address: "foo.c.sdk", PublicKey: "UB", Purpose: PurposeRotation, IssuedAt: at.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(ctx, IssueEntry{Address: "foo.c.x", PublicKey: "UC", Generation: 1<<63 + 5}); err == nil {
		t.Error("a generation that overflows an int was accepted")
	}
	es, err := r.ByAddress(ctx, "foo.c.core")
	if err != nil || len(es) != 1 {
		t.Fatalf("%v %v", es, err)
	}
	e := es[0]
	if e.PublicKey != "UA" || e.Purpose != PurposeProbe || e.Generation != 7 || !e.IssuedAt.Equal(at) || e.RevokedAt == nil ||
		!e.RevokedAt.Equal(at) || !e.Expires.IsZero() || len(e.Tags) != 1 {
		t.Errorf("read back %+v", e)
	}
	if es, _ := r.ByAddress(ctx, "foo.c.sdk"); len(es) != 1 || es[0].Purpose != PurposeRotation || es[0].RevokedAt != nil {
		t.Errorf("sdk entries %+v", es)
	}
	if es, _ := r.ByAddress(ctx, "foo.c.none"); len(es) != 0 {
		t.Errorf("none: %+v", es)
	}
}
