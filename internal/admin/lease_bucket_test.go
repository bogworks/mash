package admin_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func leaseRollupAllowed(t *testing.T, js jetstream.JetStream) bool {
	t.Helper()
	cfg, err := store.LeaseStreamConfig(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.AllowRollup
}

// The lease bucket does not allow rollup: an endpoint's publish to its own lease key with a rollup header is
// refused, and other keys survive.
func TestLeaseBucketRefusesEndpointRollup(t *testing.T) {
	e, _, js := setup(t)
	ctx := context.Background()
	if leaseRollupAllowed(t, js) {
		t.Fatal("a fresh lease bucket allows rollup")
	}
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"alice", "bob", admin.LockKey("bob")}
	for _, k := range keys {
		if _, err := kv.Put(ctx, k, []byte("live")); err != nil {
			t.Fatal(err)
		}
	}
	creds, _ := e.Mint(t, "alice")
	ajs, err := jetstream.New(e.ConnAs(t, creds, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all", "sub"} {
		m := nats.NewMsg("$KV." + store.LeaseBucket + ".alice")
		m.Header.Set("Nats-Rollup", mode)
		m.Data = []byte("replacement")
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := ajs.PublishMsg(pctx, m)
		cancel()
		if err == nil {
			t.Fatalf("rollup %q publish was accepted", mode)
		}
		for _, k := range keys {
			if _, err := kv.Get(ctx, k); err != nil {
				t.Fatalf("after rollup %q: key %s: %v", mode, k, err)
			}
		}
	}
}

// Init hardens a bucket made with the KV default (rollup allowed), keeps its keys, is idempotent, and nothing in it
// re-enables rollup.
func TestInitHardensExistingLeaseBucket(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	a, js := initOn(t, e)
	if _, err := js.CreateKeyValue(ctx, store.LeaseConfigFor(store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	if !leaseRollupAllowed(t, js) {
		t.Fatal("the default lease bucket should allow rollup before Init")
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := kv.Put(ctx, "alice", []byte("live")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if leaseRollupAllowed(t, js) {
			t.Fatalf("Init #%d left the lease bucket allowing rollup", i+1)
		}
		if got, err := kv.Get(ctx, "alice"); err != nil || string(got.Value()) != "live" {
			t.Fatalf("Init #%d lost the key: %v", i+1, err)
		}
	}
	cfg, _ := store.LeaseStreamConfig(ctx, js)
	if cfg.DenyPurge || cfg.MaxBytes != store.LeaseMaxBytes {
		t.Fatalf("stream config after Init: deny_purge=%v max_bytes=%d", cfg.DenyPurge, cfg.MaxBytes)
	}
}

// Lazy creation by a lock yields a hardened bucket, and a lock can be released and taken again.
func TestLazyLeaseBucketIsHardened(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	a, js := initOn(t, e) // no Init: the lock creates the bucket
	release, err := a.AcquireLock(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if leaseRollupAllowed(t, js) {
		t.Fatal("a lazily created lease bucket allows rollup")
	}
	release()
	release, err = a.AcquireLock(ctx, "alpha")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release()
	// An existing default bucket is hardened by the next lock.
	cfg, _ := store.LeaseStreamConfig(ctx, js)
	cfg.AllowRollup = true
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	release, err = a.AcquireLock(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if leaseRollupAllowed(t, js) {
		t.Fatal("an existing lease bucket was not hardened by the lock")
	}
}

// jsStub passes everything through but lets a test change what UpdateStream does.
type jsStub struct {
	jetstream.JetStream
	update func(context.Context, jetstream.StreamConfig) (jetstream.Stream, error)
}

func (s jsStub) UpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	return s.update(ctx, cfg)
}

// The helper fails closed: a refused update, an update the server does not apply, and a missing stream are errors.
func TestHardenLeaseBucketFailsClosed(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	_, js := initOn(t, e)
	if err := store.HardenLeaseBucket(ctx, js); err == nil {
		t.Fatal("no lease bucket: want an error")
	}
	if _, err := js.CreateKeyValue(ctx, store.LeaseConfigFor(store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	refused := jsStub{js, func(context.Context, jetstream.StreamConfig) (jetstream.Stream, error) {
		return nil, errors.New("update refused")
	}}
	if err := store.HardenLeaseBucket(ctx, refused); !errors.Is(err, store.ErrLeaseBucketRollup) {
		t.Fatalf("refused update: %v", err)
	}
	ignored := jsStub{js, func(context.Context, jetstream.StreamConfig) (jetstream.Stream, error) {
		return nil, nil // reports success and changes nothing
	}}
	if err := store.HardenLeaseBucket(ctx, ignored); !errors.Is(err, store.ErrLeaseBucketRollup) {
		t.Fatalf("unapplied update: %v", err)
	}
	if !leaseRollupAllowed(t, js) {
		t.Fatal("the stubs changed the stream")
	}
	if err := store.HardenLeaseBucket(ctx, js); err != nil {
		t.Fatal(err)
	}
	// Already hardened: no update is made.
	never := jsStub{js, func(context.Context, jetstream.StreamConfig) (jetstream.Stream, error) {
		t.Fatal("update on a hardened bucket")
		return nil, nil
	}}
	if err := store.HardenLeaseBucket(ctx, never); err != nil {
		t.Fatal(err)
	}
}

// bumpKV rewrites the key with a Put just before each of the first bumps Deletes, so every such Delete conflicts
// with the revision the caller read.
type bumpKV struct {
	jetstream.KeyValue
	bumps   int
	deletes int
}

func (b *bumpKV) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	b.deletes++
	if b.deletes <= b.bumps {
		if _, err := b.KeyValue.Put(ctx, key, []byte("live")); err != nil {
			return err
		}
	}
	return b.KeyValue.Delete(ctx, key, opts...)
}

// A lease key rewritten between clearLease's read and its delete is held by a live endpoint: after the retries
// clearLease errors, so Provision fails closed. A single conflict is retried and the key is cleared.
func TestClearLeaseRevisionConflict(t *testing.T) {
	_, _, js := setup(t)
	ctx := context.Background()
	lease, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Put(ctx, "held", []byte("live")); err != nil {
		t.Fatal(err)
	}
	always := &bumpKV{KeyValue: lease, bumps: 1 << 30}
	if err := admin.ClearLease(ctx, always, "held"); err == nil {
		t.Fatal("a key that keeps changing was reported cleared")
	}
	if always.deletes != 3 {
		t.Fatalf("deletes = %d, want 3", always.deletes)
	}
	if e, err := lease.Get(ctx, "held"); err != nil || string(e.Value()) != "live" {
		t.Fatalf("the live lease was disturbed: %v", err)
	}

	once := &bumpKV{KeyValue: lease, bumps: 1}
	if err := admin.ClearLease(ctx, once, "held"); err != nil {
		t.Fatalf("single conflict: %v", err)
	}
	if _, err := lease.Get(ctx, "held"); !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted) {
		t.Fatalf("the key was not cleared: %v", err)
	}
	if err := admin.ClearLease(ctx, lease, "never"); err != nil {
		t.Fatalf("absent key: %v", err)
	}
}

// Provision clears a deprovisioned name's lease marker at once, with rollup disabled, and the key is free for an
// endpoint's Create and for a lock.
func TestProvisionClearsLeaseMarkerWithoutRollup(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	lease, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := lease.Get(ctx, "alpha"); err != nil {
		t.Fatalf("no tombstone to clear: %v", err)
	}
	_, user2 := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Get(ctx, "alpha"); !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted) {
		t.Fatalf("the lease key was not cleared: %v", err)
	}
	if _, err := lease.Create(ctx, "alpha", []byte("held")); err != nil {
		t.Fatalf("the name is not free at once: %v", err)
	}
	if leaseRollupAllowed(t, js) {
		t.Fatal("the lease bucket allows rollup")
	}
}
