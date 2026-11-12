package admin_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// The stream CreateLeaseBucket makes is the one CreateKeyValue makes for the same configuration, except that it
// refuses rollup, and it never allows rollup, not even right after creation.
func TestCreateLeaseBucketMatchesKeyValueStreamExceptRollup(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	_, js := initOn(t, e)
	cfg := store.LeaseConfigFor(store.DefaultLimits())

	if _, err := store.CreateLeaseBucket(ctx, js, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := store.LeaseStreamConfig(ctx, js) // read right after creation
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowRollup {
		t.Fatal("the lease stream allowed rollup right after creation")
	}

	ref := cfg
	ref.Bucket = "reference"
	if _, err := js.CreateKeyValue(ctx, ref); err != nil {
		t.Fatal(err)
	}
	rs, err := js.Stream(ctx, "KV_reference")
	if err != nil {
		t.Fatal(err)
	}
	ri, err := rs.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := ri.Config
	if !want.AllowRollup {
		t.Fatal("the reference stream should allow rollup (CreateKeyValue default)")
	}
	want.AllowRollup = false
	want.Name, got.Name = "", ""
	want.Subjects, got.Subjects = nil, nil
	want.Description, got.Description = "", ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stream config differs:\n got  %+v\n want %+v", got, want)
	}
}

// watchUpdateWait bounds how long the lease watch test waits for one update.
const watchUpdateWait = 10 * time.Second

// nextUpdate reads the next update of the watcher, skipping the nil initial-values marker, and checks that it
// carries the operation op and, when rev is not zero, the revision rev. The lease bucket keeps History 1, so the
// server drops each older revision of a key as the next write lands: a watcher that is read only after several
// writes may never see the middle ones. A test therefore reads each write's update before it issues the next write.
func nextUpdate(t *testing.T, w jetstream.KeyWatcher, op jetstream.KeyValueOp, rev uint64) jetstream.KeyValueEntry {
	t.Helper()
	timeout := time.After(watchUpdateWait)
	for {
		select {
		case u := <-w.Updates():
			if u == nil {
				continue
			}
			if u.Operation() != op || (rev != 0 && u.Revision() != rev) {
				t.Fatalf("watch update: operation %v revision %d, want %v revision %d", u.Operation(), u.Revision(), op, rev)
			}
			return u
		case <-timeout:
			t.Fatalf("watch saw no update for operation %v revision %d", op, rev)
		}
	}
}

func TestCreateLeaseBucketWorksAsKV(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	_, js := initOn(t, e)
	kv, err := store.CreateLeaseBucket(ctx, js, store.LeaseConfigFor(store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	st, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.TTL() != store.LeaseTTL || st.History() != 1 || st.Bucket() != store.LeaseBucket {
		t.Fatalf("status: ttl %v history %d bucket %s", st.TTL(), st.History(), st.Bucket())
	}
	w, err := kv.Watch(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	rev, err := kv.Create(ctx, "k", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	nextUpdate(t, w, jetstream.KeyValuePut, rev)
	if _, err := kv.Create(ctx, "k", []byte("b")); err == nil {
		t.Fatal("second Create succeeded")
	}
	rev, err = kv.Update(ctx, "k", []byte("c"), rev)
	if err != nil {
		t.Fatal(err)
	}
	nextUpdate(t, w, jetstream.KeyValuePut, rev)
	if _, err := kv.Update(ctx, "k", []byte("d"), rev-1); err == nil {
		t.Fatal("stale Update succeeded")
	}
	putRev, err := kv.Put(ctx, "k", []byte("e"))
	if err != nil {
		t.Fatal(err)
	}
	nextUpdate(t, w, jetstream.KeyValuePut, putRev)
	ent, err := kv.Get(ctx, "k")
	if err != nil || string(ent.Value()) != "e" {
		t.Fatalf("get: %v %v", ent, err)
	}
	if err := kv.Delete(ctx, "k", jetstream.LastRevision(ent.Revision())); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get(ctx, "k"); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if del := nextUpdate(t, w, jetstream.KeyValueDelete, 0); del.Revision() <= putRev {
		t.Fatalf("delete marker revision %d, want above %d", del.Revision(), putRev)
	}
}

func TestCreateLeaseBucketConcurrentAndExisting(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	_, js := initOn(t, e)
	cfg := store.LeaseConfigFor(store.DefaultLimits())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kv, err := store.CreateLeaseBucket(ctx, js, cfg)
			if err == nil && kv == nil {
				err = errors.New("nil bucket")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if leaseRollupAllowed(t, js) {
		t.Fatal("rollup allowed after concurrent create")
	}

	// An existing bucket that allows rollup is hardened by a create over it.
	if err := js.DeleteKeyValue(ctx, store.LeaseBucket); err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if !leaseRollupAllowed(t, js) {
		t.Fatal("setup: expected rollup allowed")
	}
	if _, err := store.CreateLeaseBucket(ctx, js, cfg); err != nil {
		t.Fatal(err)
	}
	if leaseRollupAllowed(t, js) {
		t.Fatal("existing bucket not hardened")
	}

	other := cfg
	other.Bucket = "other"
	if _, err := store.CreateLeaseBucket(ctx, js, other); err == nil {
		t.Fatal("a bucket with another name was accepted")
	}
}
