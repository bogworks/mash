package admin_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func bucketBytes(t *testing.T, js jetstream.JetStream, bucket string) int64 {
	t.Helper()
	kv, err := js.KeyValue(context.Background(), bucket)
	if err != nil {
		t.Fatal(err)
	}
	st, err := kv.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st.(*jetstream.KeyValueBucketStatus).StreamInfo().Config.MaxBytes
}

func initOn(t *testing.T, e *testenv.Env) (*admin.Admin, jetstream.JetStream) {
	t.Helper()
	nc := e.Conn(t, e.AdminCreds)
	a, err := admin.New(nc, e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(nc)
	return a, js
}

func TestInitFreshBucketsHaveMaxBytes(t *testing.T) {
	_, _, js := setup(t)
	if got := bucketBytes(t, js, store.DirectoryBucket); got != 16<<20 {
		t.Fatalf("directory MaxBytes %d", got)
	}
	if got := bucketBytes(t, js, store.LeaseBucket); got != 4<<20 {
		t.Fatalf("lease MaxBytes %d", got)
	}
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	st, _ := kv.Status(context.Background())
	c := st.(*jetstream.KeyValueBucketStatus).StreamInfo().Config
	if c.MaxMsgsPerSubject != 1 || c.MaxAge != store.LeaseTTL {
		t.Fatalf("lease history/TTL changed: %+v", c)
	}
}

func TestInitOnServerThatRequiresMaxBytes(t *testing.T) {
	e := testenv.StartWith(t, testenv.Options{RequireMaxBytes: true})
	a, js := initOn(t, e)
	// The harness really rejects the old, unbounded config (Synadia's err_code 10113).
	old := store.DirectoryConfig()
	old.MaxBytes = 0
	if _, err := js.CreateKeyValue(context.Background(), old); err == nil {
		t.Fatal("server accepted a bucket with no MaxBytes")
	}
	if err := a.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bucketBytes(t, js, store.DirectoryBucket) != 16<<20 || bucketBytes(t, js, store.LeaseBucket) != 4<<20 {
		t.Fatal("buckets not bounded")
	}
}

func TestInitBoundsExistingUnboundedBucketKeepingKeys(t *testing.T) {
	e := testenv.Start(t)
	a, js := initOn(t, e)
	ctx := context.Background()
	for _, cfg := range []jetstream.KeyValueConfig{store.DirectoryConfig(), store.LeaseConfig()} {
		cfg.MaxBytes = 0 // the buckets as 0.2.x created them
		kv, err := js.CreateKeyValue(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Put(ctx, "k", []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if bucketBytes(t, js, store.DirectoryBucket) != -1 {
		t.Fatal("setup: bucket is bounded")
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]int64{store.DirectoryBucket: store.DirectoryMaxBytes, store.LeaseBucket: store.LeaseMaxBytes} {
		if got := bucketBytes(t, js, b); got != want {
			t.Fatalf("%s MaxBytes %d, want %d", b, got, want)
		}
		kv, _ := js.KeyValue(ctx, b)
		if e, err := kv.Get(ctx, "k"); err != nil || string(e.Value()) != "v" {
			t.Fatalf("%s lost its key: %v", b, err)
		}
	}
}

func TestInitNeverResizesAnExplicitMaxBytes(t *testing.T) {
	e := testenv.Start(t)
	a, js := initOn(t, e)
	ctx := context.Background()
	cfg := store.DirectoryConfig()
	cfg.MaxBytes = 1 << 20 // smaller than the default
	if _, err := js.CreateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg = store.LeaseConfig()
	cfg.MaxBytes = 64 << 20 // larger than the default
	if _, err := js.CreateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if got := bucketBytes(t, js, store.DirectoryBucket); got != 1<<20 {
		t.Fatalf("directory MaxBytes %d", got)
	}
	if got := bucketBytes(t, js, store.LeaseBucket); got != 64<<20 {
		t.Fatalf("lease MaxBytes %d", got)
	}
}

func TestInitRerunChangesNothing(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	cfgs := func() []jetstream.StreamConfig {
		var out []jetstream.StreamConfig
		for _, b := range []string{store.DirectoryBucket, store.LeaseBucket} {
			kv, _ := js.KeyValue(ctx, b)
			st, _ := kv.Status(ctx)
			out = append(out, st.(*jetstream.KeyValueBucketStatus).StreamInfo().Config)
		}
		return out
	}
	before := cfgs()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	_ = e
	if after := cfgs(); !reflect.DeepEqual(before, after) {
		t.Fatalf("re-running init changed the buckets:\n%+v\n%+v", before, after)
	}
}
