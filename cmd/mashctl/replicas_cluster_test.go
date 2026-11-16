package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// clusterEnv is a three-node operator-mode cluster, a config directory and a key store holding the cluster's operator
// and account keys, so the real mashctl verbs (add, retire, audit) run against it. env is the configuration: tests
// change it between runs (the declared server count, the replicas setting).
type clusterEnv struct {
	t   *testing.T
	c   *testenv.Cluster
	dir string
	env map[string]string
	nc  *nats.Conn
	js  jetstream.JetStream
}

func newClusterEnv(t *testing.T, servers int, extra map[string]string) *clusterEnv {
	t.Helper()
	return newClusterEnvInterval(t, servers, extra, 0)
}

// newClusterEnvInterval is newClusterEnv with the resolver sync interval set (0: the harness default).
func newClusterEnvInterval(t *testing.T, servers int, extra map[string]string, interval time.Duration) *clusterEnv {
	t.Helper()
	var c *testenv.Cluster
	if interval > 0 {
		c = testenv.StartClusterInterval(t, 3, interval)
	} else {
		c = testenv.StartCluster(t, 3)
	}
	// The package's tests revoke against a stub; these tests run the real revocation through the system account.
	prevRevoke := revokeUsers
	revokeUsers = revokeUserKeys
	t.Cleanup(func() { revokeUsers = prevRevoke })
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &clusterEnv{t: t, c: c, dir: dir, env: map[string]string{
		"HOME": t.TempDir(), "MASH_URL": c.URL, "MASH_OWNER": c.Owner, "MASH_ADMIN_CREDS": c.AdminCreds, "MASH_INSECURE_LOOPBACK": "1",
		"MASH_CONFIG_DIR": dir, "MASH_KEYS_DIR": filepath.Join(dir, "keys"), "MASH_SYS_CREDS": c.SysCreds,
		"MASH_SERVERS": fmt.Sprint(servers),
	}}
	for k, v := range extra {
		h.env[k] = v
	}
	ka, err := ctlkeys.Open(h.env["MASH_KEYS_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.ImportOperator(c.OperatorSeed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.ImportAccount(c.AccountSeed); err != nil {
		t.Fatal(err)
	}
	h.nc = c.Conn(t, c.AdminCreds)
	if h.js, err = jetstream.New(h.nc); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *clusterEnv) run(args ...string) (string, string, int) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return h.env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

func (h *clusterEnv) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	h.t.Cleanup(cancel)
	return ctx
}

// replicaMap is the replica count of every mash-owned object that exists.
func (h *clusterEnv) replicaMap() map[string]int {
	h.t.Helper()
	st, err := store.ReplicaState(h.ctx(), h.js)
	if err != nil {
		h.t.Fatal(err)
	}
	m := map[string]int{}
	for _, o := range st {
		m[o.Object] = o.Replicas
	}
	return m
}

func (h *clusterEnv) wantAll(want int, objects ...string) {
	h.t.Helper()
	m := h.replicaMap()
	for _, o := range objects {
		if got, ok := m[o]; !ok || got != want {
			h.t.Errorf("%s has %d replica(s) (present %v), want %d; all: %v", o, got, ok, want, m)
		}
	}
}

// current waits until every mash-owned stream reports all its replicas current (after a node came back).
func (h *clusterEnv) current(want int) {
	h.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		ok := true
		st, err := store.ReplicaState(h.ctx(), h.js)
		if err != nil {
			last, ok = err.Error(), false
		}
		for _, o := range st {
			s, err := h.js.Stream(h.ctx(), o.Object)
			if err != nil {
				last, ok = err.Error(), false
				continue
			}
			info, err := s.Info(h.ctx())
			if err != nil || info.Cluster == nil || len(info.Cluster.Replicas) < want-1 {
				last, ok = fmt.Sprintf("%s: %v %+v", o.Object, err, info.Cluster), false
				continue
			}
			for _, p := range info.Cluster.Replicas {
				if !p.Current || p.Offline {
					last, ok = fmt.Sprintf("%s: replica %s not current (lag %d)", o.Object, p.Name, p.Lag), false
				}
			}
		}
		if ok {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	h.t.Fatalf("replicas did not catch up: %s", last)
}

// send stores one message in endpoint name's inbox and returns once the stream acknowledged it (every replica a
// quorum needs has it).
func (h *clusterEnv) send(name, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := h.js.Publish(ctx, store.MailSubjectFor(h.c.Owner, name), []byte(body))
	return err
}

// receive reads the messages of name's inbox through the deliver consumer and acks them.
func (h *clusterEnv) receive(name string, n int) []string {
	h.t.Helper()
	ctx := h.ctx()
	cons, err := h.js.CreateOrUpdateConsumer(ctx, sap.InboxStream(name), store.ConsumerConfig())
	if err != nil {
		h.t.Fatal(err)
	}
	var got []string
	deadline := time.Now().Add(30 * time.Second)
	for len(got) < n && time.Now().Before(deadline) {
		batch, err := cons.Fetch(n-len(got), jetstream.FetchMaxWait(3*time.Second))
		if err != nil {
			continue
		}
		for m := range batch.Messages() {
			got = append(got, string(m.Data()))
			_ = m.DoubleAck(ctx)
		}
	}
	return got
}

// A three-node cluster, objects created with replicas auto and three declared servers: every mash-owned object is R3.
// With one node down the real verbs work with MASH_SERVERS still 3 (the write quorum is 2 of 3, ADR 0142), and the node
// comes back, catches up and the audit is clean again once it holds the revocation. TestLiveWriteQuorumOneNodeDown has
// the full sequence; this one keeps the replica side of it.
func TestLiveThreeNodesR3OneNodeDown(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	h := newClusterEnv(t, 3, nil)
	if out, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	h.wantAll(3, "KV_"+store.DirectoryBucket, store.LeaseStreamName, store.IssuedStreamName, store.AdoptionStreamName, "KV_"+store.RetiredBucket)
	if out, errb, code := h.run("add", "a"); code != 0 {
		t.Fatalf("add a: %d %s %s", code, out, errb)
	}
	h.wantAll(3, sap.InboxStream("a"))
	if out, errb, code := h.run("audit", "--strict"); code != 0 {
		t.Fatalf("audit before: %d %s %s", code, out, errb)
	}

	// Stop one node. The record of which one: index 2 (n3).
	const down = 2
	h.c.Stop(down)
	time.Sleep(3 * time.Second) // leader election

	if out, errb, code := h.run("add", "b"); code != 0 {
		t.Fatalf("add b with a node down (servers %s): %d %s %s", h.env["MASH_SERVERS"], code, out, errb)
	}
	t.Logf("inbox of b with a node down: %d replica(s)", h.replicaMap()[sap.InboxStream("b")])
	if err := h.send("b", "hello from the cluster"); err != nil {
		t.Fatalf("send with a node down: %v", err)
	}
	if got := h.receive("b", 1); len(got) != 1 || got[0] != "hello from the cluster" {
		t.Fatalf("receive with a node down: %v", got)
	}
	if out, errb, code := h.run("retire", "b"); code != 0 {
		t.Fatalf("retire b with a node down (servers %s): %d %s %s", h.env["MASH_SERVERS"], code, out, errb)
	}

	h.c.Start(down)
	h.current(3)
	h.wantAll(3, "KV_"+store.DirectoryBucket, store.LeaseStreamName, store.IssuedStreamName, store.AdoptionStreamName, "KV_"+store.RetiredBucket, sap.InboxStream("a"))
	h.auditClean(90 * time.Second)
}

// R1 on three nodes (replicas 1), a directory record, an issue record, a lease and mail; raise to R3 while a sender
// keeps sending; nothing is lost, the lease is unchanged, rollup stays refused, every object is R3 and current, and the
// retired bucket still holds its markers.
func TestLiveRaiseR1ToR3WhileSending(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	h := newClusterEnv(t, 3, map[string]string{"MASH_REPLICAS": "1"})
	if out, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	for _, n := range []string{"a", "b"} {
		if out, errb, code := h.run("add", n); code != 0 {
			t.Fatalf("add %s: %d %s %s", n, code, out, errb)
		}
	}
	// A retired marker: retire a new-form identity is the only writer, so write one the way deprovision does it.
	ctx := h.ctx()
	rkv, err := h.js.KeyValue(ctx, store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rkv.Put(ctx, "agent_o_old", []byte(`{"agent":"agent","owner":"o","name":"old","retired_at":"2026-11-16T00:00:00Z","state":"final"}`)); err != nil {
		t.Fatal(err)
	}
	lkv, err := h.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lkv.Put(ctx, "a", []byte(`{"holder":"live-lease"}`)); err != nil {
		t.Fatal(err)
	}
	dkv, err := h.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	dirBefore, err := dkv.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	ikv, err := h.js.KeyValue(ctx, store.IssuedBucket)
	if err != nil {
		t.Fatal(err)
	}
	issuedKeys, err := ikv.Keys(ctx)
	if err != nil || len(issuedKeys) == 0 {
		t.Fatalf("issue record: %v %v", issuedKeys, err)
	}
	h.wantAll(1, "KV_"+store.DirectoryBucket, store.LeaseStreamName, store.IssuedStreamName, store.AdoptionStreamName, "KV_"+store.RetiredBucket, sap.InboxStream("a"), sap.InboxStream("b"))

	// With replicas auto and three servers declared, audit says the objects should be raised: one finding.
	h.env["MASH_REPLICAS"] = "auto"
	if out, _, code := h.run("audit", "--strict"); code != 1 || !strings.Contains(out, "raise the replica count of mash-owned state to 3") {
		t.Fatalf("audit before the raise: %d %q", code, out)
	}
	// issued check gives the same finding for the issue record.
	if out, _, code := h.run("issued", "check"); code != 1 || !strings.Contains(out, "REPLICAS: the issue record") || !strings.Contains(out, "raise the replica count of mash-owned state to 3") {
		t.Fatalf("issued check before the raise: %d %q", code, out)
	}
	h.env["MASH_REPLICAS"] = "1"
	if out, errb, code := h.run("issued", "check"); code != 0 {
		t.Fatalf("issued check at replicas 1: %d %s %s", code, out, errb)
	}
	if out, errb, code := h.run("audit", "--strict"); code != 0 {
		t.Fatalf("audit at replicas 1: %d %s %s", code, out, errb)
	}

	for i := 0; i < 5; i++ {
		if err := h.send("a", fmt.Sprintf("before-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	// A sender keeps sending while the raise runs. A send that fails (election) is retried with the same id, which the
	// stream's duplicate window makes safe, so every accepted send is stored exactly once.
	var sent atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("during-%d", i)
			for {
				sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, err := h.js.Publish(sctx, store.MailSubjectFor(h.c.Owner, "a"), []byte(id), jetstream.WithMsgID(id))
				cancel()
				if err == nil {
					sent.Add(1)
					break
				}
				select {
				case <-stop:
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	var steps []store.Step
	err = store.RaiseReplicas(h.ctx(), h.js, 3, 3, func(s store.Step) { steps = append(steps, s) })
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("RaiseReplicas: %v\n%+v", err, steps)
	}
	for _, s := range steps {
		t.Logf("%s %s %d->%d %s", s.Object, s.Action, s.From, s.To, s.Note)
	}
	if steps[0].Object != "KV_"+store.DirectoryBucket {
		t.Errorf("the directory is not raised first: %+v", steps[0])
	}

	h.wantAll(3, "KV_"+store.DirectoryBucket, store.LeaseStreamName, store.IssuedStreamName, store.AdoptionStreamName, "KV_"+store.RetiredBucket, sap.InboxStream("a"), sap.InboxStream("b"))
	h.current(3)

	// Mail: five before, every accepted send during, none lost, none twice.
	s, err := h.js.Stream(ctx, sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(5 + sent.Load()); info.State.Msgs != want {
		t.Errorf("messages in the inbox: %d, want %d (5 before and %d sent during the raise)", info.State.Msgs, want, sent.Load())
	}
	if sent.Load() == 0 {
		t.Error("the sender never sent during the raise")
	}
	// The directory record, the issue record and the lease are as they were.
	if e, err := dkv.Get(ctx, "a"); err != nil || string(e.Value()) != string(dirBefore.Value()) || e.Revision() != dirBefore.Revision() {
		t.Errorf("directory record changed: %v", err)
	}
	if e, err := lkv.Get(ctx, "a"); err != nil || string(e.Value()) != `{"holder":"live-lease"}` {
		t.Errorf("lease changed: %v", err)
	}
	if keys, err := ikv.Keys(ctx); err != nil || len(keys) != len(issuedKeys) {
		t.Errorf("issue record: %v %v", keys, err)
	}
	// Rollup is still refused on the lease bucket.
	if err := store.VerifyLeaseBucket(ctx, h.js); err != nil {
		t.Errorf("lease bucket: %v", err)
	}
	lc, err := store.LeaseStreamConfig(ctx, h.js)
	if err != nil || lc.AllowRollup {
		t.Errorf("lease stream allows rollup after the raise: %v", err)
	}
	rollup := nats.NewMsg("$KV." + store.LeaseBucket + ".a")
	rollup.Data = []byte("x")
	rollup.Header.Set("Nats-Rollup", "sub")
	if _, err := h.js.PublishMsg(ctx, rollup); err == nil {
		t.Error("a rollup write to the lease bucket was accepted after the raise")
	}
	// The retired bucket holds its marker, and only its replica count changed.
	if e, err := rkv.Get(ctx, "agent_o_old"); err != nil || !strings.Contains(string(e.Value()), `"state":"final"`) {
		t.Errorf("retired marker: %v", err)
	}
	rs, _ := h.js.Stream(ctx, "KV_"+store.RetiredBucket)
	ri, _ := rs.Info(ctx)
	if ri.State.Msgs != 1 {
		t.Errorf("retired bucket holds %d messages, want 1", ri.State.Msgs)
	}
	// Idempotent: a second raise changes nothing.
	var again []store.Step
	if err := store.RaiseReplicas(ctx, h.js, 3, 3, func(s store.Step) { again = append(again, s) }); err != nil {
		t.Fatal(err)
	}
	for _, s := range again {
		if s.Action != "skip" {
			t.Errorf("second raise did something: %+v", s)
		}
	}
	// The real verbs accept the raised state, and audit is clean at three replicas.
	h.env["MASH_REPLICAS"] = "auto"
	if out, errb, code := h.run("audit", "--strict"); code != 0 {
		t.Fatalf("audit after the raise: %d %s %s", code, out, errb)
	}
	if out, errb, code := h.run("issued", "check"); code != 0 {
		t.Fatalf("issued check after the raise: %d %s %s", code, out, errb)
	}
}
