package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func policyTestJS(t *testing.T) jetstream.JetStream {
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
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestCreatePolicyBucketIsHardenedAndIdempotent(t *testing.T) {
	js := policyTestJS(t)
	ctx := context.Background()
	kv, err := CreatePolicyBucket(ctx, js, "mash-policy")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPolicyBucket(ctx, js, "mash-policy"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	s, err := js.Stream(ctx, PolicyStreamName("mash-policy"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := info.Config
	if c.Storage != jetstream.FileStorage || c.MaxMsgsPerSubject != PolicyHistory || c.MaxBytes != PolicyMaxBytes || c.MaxAge != 0 ||
		c.AllowRollup || !c.DenyDelete || !c.DenyPurge || c.Discard != jetstream.DiscardNew {
		t.Errorf("config = %+v", c)
	}
	// Writes the ledger makes work: create, update at a revision, a value near the default max payload.
	rev, err := kv.Create(ctx, "status.A", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Update(ctx, "status.A", make([]byte, 1<<20-16<<10), rev); err != nil {
		t.Fatalf("a full-size value: %v", err)
	}
	// A second create returns the same bucket and keeps its data.
	kv2, err := CreatePolicyBucket(ctx, js, "mash-policy")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if e, err := kv2.Get(ctx, "status.A"); err != nil || len(e.Value()) != 1<<20-16<<10 {
		t.Fatalf("data after a second create: %v", err)
	}
}

func TestPolicyBucketWrongConfigIsReportedAndNotChanged(t *testing.T) {
	js := policyTestJS(t)
	ctx := context.Background()
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: "other", History: 8}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPolicyBucket(ctx, js, "other"); !errors.Is(err, ErrPolicyBucket) {
		t.Fatalf("verify = %v", err)
	}
	if _, err := CreatePolicyBucket(ctx, js, "other"); !errors.Is(err, ErrPolicyBucket) {
		t.Fatalf("create = %v", err)
	}
	s, _ := js.Stream(ctx, PolicyStreamName("other"))
	info, _ := s.Info(ctx)
	if info.Config.DenyPurge || !info.Config.AllowRollup || info.Config.MaxMsgsPerSubject != 8 || info.Config.MaxBytes != -1 {
		t.Errorf("an existing bucket was reconfigured: %+v", info.Config)
	}
	if err := VerifyPolicyBucket(ctx, js, "missing"); err == nil {
		t.Error("verify of a missing bucket succeeded")
	}
}
