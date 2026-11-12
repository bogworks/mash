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
	if _, err := kv.Create(ctx, "k", []byte("b")); err == nil {
		t.Fatal("second Create succeeded")
	}
	rev, err = kv.Update(ctx, "k", []byte("c"), rev)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Update(ctx, "k", []byte("d"), rev-1); err == nil {
		t.Fatal("stale Update succeeded")
	}
	if _, err := kv.Put(ctx, "k", []byte("e")); err != nil {
		t.Fatal(err)
	}
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
	seen := 0
	timeout := time.After(5 * time.Second)
	for seen < 4 { // create, update, put, delete (the initial-values marker is nil)
		select {
		case u := <-w.Updates():
			if u != nil {
				seen++
			}
		case <-timeout:
			t.Fatalf("watch saw %d updates", seen)
		}
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
