package store_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func replicaMap(t *testing.T, ctx context.Context, js jetstream.JetStream) map[string]int {
	t.Helper()
	st, err := store.ReplicaState(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]int{}
	for _, o := range st {
		m[o.Object] = o.Replicas
	}
	return m
}

func streamConfigs(t *testing.T, ctx context.Context, js jetstream.JetStream) map[string]jetstream.StreamConfig {
	t.Helper()
	out := map[string]jetstream.StreamConfig{}
	for n := range replicaMap(t, ctx, js) {
		s, err := js.Stream(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		info, err := s.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = info.Config
	}
	return out
}

// The raise on a live three-node cluster: refusals change nothing, a missing node changes nothing, a raise changes
// only the replica count and keeps the lease's rollup off, it is resumable after a stop, idempotent and never lowers,
// and the checks accept the raised count and refuse a smaller one.
func TestRaiseReplicasLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	c := testenv.StartCluster(t, 3)
	nc := c.Conn(t, c.AdminCreds)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// A deployment at one replica: every mash-owned object, two inboxes, a retired marker and a lease.
	l := store.DefaultLimits()
	if _, err := js.CreateKeyValue(ctx, store.DirectoryConfigFor(l)); err != nil {
		t.Fatal(err)
	}
	lkv, err := store.CreateLeaseBucket(ctx, js, store.LeaseConfigFor(l))
	if err != nil {
		t.Fatal(err)
	}
	rkv, err := js.CreateKeyValue(ctx, store.RetiredConfigFor(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIssuedBucket(ctx, js, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAdoptionBucket(ctx, js, 1); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		if _, err := js.CreateStream(ctx, store.InboxConfigFor("o", n, l)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rkv.Put(ctx, "agent_o_old", []byte(`{"state":"final"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := lkv.Put(ctx, "a", []byte(`held`)); err != nil {
		t.Fatal(err)
	}
	before := streamConfigs(t, ctx, js)
	if len(before) != 7 {
		t.Fatalf("objects: %v", replicaMap(t, ctx, js))
	}
	for n, r := range replicaMap(t, ctx, js) {
		if r != 1 {
			t.Fatalf("%s starts at %d", n, r)
		}
	}
	retiredBefore, _ := rkv.Get(ctx, "agent_o_old")

	// The checks refuse the R1 objects when 3 is wanted, and accept them when 1 is.
	st, err := rkv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyRetiredStatusAt(st, 1); err != nil {
		t.Errorf("retired at 1 wanted 1: %v", err)
	}
	if err := store.VerifyRetiredStatusAt(st, 3); err == nil || !strings.Contains(err.Error(), "1 replica(s) where 3 are wanted") {
		t.Errorf("retired R1 wanted 3: %v", err)
	}
	if err := store.VerifyIssuedBucketAt(ctx, js, 3); err == nil || !strings.Contains(err.Error(), "where 3 are wanted") {
		t.Errorf("issued R1 wanted 3: %v", err)
	}
	if err := store.VerifyAdoptionBucketAt(ctx, js, 3); err == nil || !strings.Contains(err.Error(), "where 3 are wanted") {
		t.Errorf("adoption R1 wanted 3: %v", err)
	}
	if err := store.VerifyIssuedBucket(ctx, js); err != nil {
		t.Errorf("issued unchanged check: %v", err)
	}

	// Refusals change nothing: a target the cluster does not declare, one mash does not support, and a lower one.
	unchanged := func(what string) {
		t.Helper()
		if got := streamConfigs(t, ctx, js); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s changed a stream configuration", what)
		}
	}
	for _, c := range []struct {
		target, servers int
		want            string
	}{{3, 2, "needs at least 3 servers"}, {5, 5, "auto, 1 and 3"}, {2, 3, "auto, 1 and 3"}, {3, 0, "needs at least 3 servers"}} {
		if err := store.RaiseReplicas(ctx, js, c.target, c.servers, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("RaiseReplicas(%d, %d): %v", c.target, c.servers, err)
		}
		unchanged("a refused target")
	}
	// Target 1 never lowers and is a no-op here.
	if err := store.RaiseReplicas(ctx, js, 1, 3, nil); err != nil {
		t.Fatal(err)
	}
	unchanged("target 1")

	// A node down: the cluster cannot place three replicas now, and nothing is changed.
	c.Stop(2)
	time.Sleep(2 * time.Second)
	store.PlacementWait = 4 * time.Second
	t.Cleanup(func() { store.PlacementWait = 10 * time.Second })
	if err := store.RaiseReplicas(ctx, js, 3, 3, nil); err == nil {
		t.Error("RaiseReplicas succeeded with a node down")
	} else {
		t.Logf("with a node down: %v", err)
	}
	c.Start(2)
	waitPlace(t, ctx, js)
	store.PlacementWait = 30 * time.Second // the restarted node may still be catching up on a slow runner
	unchanged("a raise with a node down")  // read once the node is back: an R1 object on it was unreadable meanwhile

	// Resumable: stop after the first object (the context is cancelled once the directory is current), then run again.
	stopCtx, stop := context.WithCancel(ctx)
	var seen []string
	err = store.RaiseReplicas(stopCtx, js, 3, 3, func(s store.Step) {
		seen = append(seen, s.Object+" "+s.Action)
		if s.Object == "KV_"+store.DirectoryBucket && s.Action == "done" {
			stop()
		}
	})
	stop()
	if err == nil {
		t.Logf("the stop landed after the last step: %v", seen)
	}
	m := replicaMap(t, ctx, js)
	if m["KV_"+store.DirectoryBucket] != 3 {
		t.Fatalf("the directory was not raised before the stop: %v", m)
	}
	if m["KV_"+store.RetiredBucket] != 1 || m[sap.InboxStream("a")] != 1 {
		t.Fatalf("the stop did not leave a partial run: %v", m)
	}
	var steps []store.Step
	if err := store.RaiseReplicas(ctx, js, 3, 3, func(s store.Step) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if steps[0].Object != "KV_"+store.DirectoryBucket || steps[0].Action != "skip" {
		t.Errorf("the resumed run does not skip the directory first: %+v", steps[0])
	}
	var raised []string
	for _, s := range steps {
		if s.Action == "raise" {
			raised = append(raised, s.Object)
		}
	}
	want := []string{store.LeaseStreamName, store.IssuedStreamName, store.AdoptionStreamName, "KV_" + store.RetiredBucket, "inbox-a", "inbox-b"}
	if !reflect.DeepEqual(raised, want) {
		t.Errorf("raised in the order %v, want %v", raised, want)
	}
	for n, r := range replicaMap(t, ctx, js) {
		if r != 3 {
			t.Errorf("%s has %d replicas", n, r)
		}
	}

	// Only the replica count changed, on every object; the lease still refuses rollup.
	after := streamConfigs(t, ctx, js)
	for n, b := range before {
		a := after[n]
		if a.Replicas != 3 {
			t.Errorf("%s: %d", n, a.Replicas)
		}
		a.Replicas, b.Replicas = 0, 0
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: more than the replica count changed:\nbefore %+v\nafter  %+v", n, b, a)
		}
	}
	if err := store.VerifyLeaseBucket(ctx, js); err != nil {
		t.Errorf("lease bucket after the raise: %v", err)
	}
	roll := nats.NewMsg("$KV." + store.LeaseBucket + ".a")
	roll.Data = []byte("x")
	roll.Header.Set("Nats-Rollup", "sub")
	if _, err := js.PublishMsg(ctx, roll); err == nil {
		t.Error("a rollup write to the lease bucket was accepted after the raise")
	}
	if e, err := lkv.Get(ctx, "a"); err != nil || string(e.Value()) != "held" {
		t.Errorf("the lease: %v", err)
	}
	// The retired bucket: the marker and its revision are as they were, and no key was added.
	if e, err := rkv.Get(ctx, "agent_o_old"); err != nil || e.Revision() != retiredBefore.Revision() || string(e.Value()) != string(retiredBefore.Value()) {
		t.Errorf("retired marker: %v", err)
	}
	if keys, err := rkv.Keys(ctx); err != nil || len(keys) != 1 {
		t.Errorf("retired keys: %v %v", keys, err)
	}

	// Idempotent: everything is skipped. And never lowers: target 1 changes nothing.
	steps = nil
	if err := store.RaiseReplicas(ctx, js, 3, 3, func(s store.Step) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Action != "skip" {
			t.Errorf("a second raise did %+v", s)
		}
	}
	if err := store.RaiseReplicas(ctx, js, 1, 3, nil); err != nil {
		t.Fatal(err)
	}
	for n, r := range replicaMap(t, ctx, js) {
		if r != 3 {
			t.Errorf("%s was lowered to %d", n, r)
		}
	}

	// The checks accept the raised count whether 1 or 3 is wanted (larger is fine), and refuse 5... never smaller.
	st, err = rkv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{1, 3} {
		if err := store.VerifyRetiredStatusAt(st, w); err != nil {
			t.Errorf("retired R3 wanted %d: %v", w, err)
		}
		if err := store.VerifyIssuedBucketAt(ctx, js, w); err != nil {
			t.Errorf("issued R3 wanted %d: %v", w, err)
		}
		if err := store.VerifyAdoptionBucketAt(ctx, js, w); err != nil {
			t.Errorf("adoption R3 wanted %d: %v", w, err)
		}
	}
}

// waitPlace waits until a three-replica stream can be placed again after a node came back.
func waitPlace(t *testing.T, ctx context.Context, js jetstream.JetStream) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		_, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "place-probe", Subjects: []string{"place.probe"}, Storage: jetstream.MemoryStorage, Replicas: 3})
		if err == nil {
			_ = js.DeleteStream(ctx, "place-probe")
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("three replicas cannot be placed after the node came back")
}
