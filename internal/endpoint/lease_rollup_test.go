package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// allowLeaseRollup puts the lease bucket in the state an unhardened deployment has: the stream allows rollup.
func (f *fixture) allowLeaseRollup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.LeaseStreamConfig(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AllowRollup = true
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg, err = store.LeaseStreamConfig(ctx, js); err != nil || !cfg.AllowRollup {
		t.Fatalf("setup: rollup not allowed: %v", err)
	}
}

func TestLeaseRollupWarnsOnAnUnhardenedBucketAndTheOpenSucceeds(t *testing.T) {
	f := newFixture(t, "a")
	f.allowLeaseRollup(t)
	ep, _ := f.run(t, "a") // the open itself never fails over the bucket
	if !ep.LeaseRollupWarning(context.Background()) {
		t.Fatal("no warning for a bucket that allows rollup")
	}
	line, ok := ep.LeaseRollupLine(context.Background())
	if !ok || line != "lease bucket: WARN - it allows rollup writes; run mash admin init" {
		t.Fatalf("status line: %q %v", line, ok)
	}
}

func TestLeaseRollupIsSilentOnAHardenedBucket(t *testing.T) {
	f := newFixture(t, "a")
	ep, _ := f.run(t, "a")
	if ep.LeaseRollupWarning(context.Background()) {
		t.Fatal("warning for a hardened bucket")
	}
	if line, ok := ep.LeaseRollupLine(context.Background()); ok {
		t.Fatalf("line for a hardened bucket: %q", line)
	}
}

// An endpoint that may not read the stream info says nothing, and quickly.
func TestLeaseRollupIsSilentWhenInfoIsNotPermitted(t *testing.T) {
	f := newFixture(t, "a")
	f.allowLeaseRollup(t)
	ep := f.client(t, "a")
	if !ep.LeaseRollupWarning(context.Background()) {
		t.Fatal("the warning should show with the endpoint's own permissions")
	}
	creds, _ := f.e.MintRaw(t, "nopriv", jwt.Permissions{
		Pub: jwt.Permission{Allow: []string{"_INBOX.>"}}, Sub: jwt.Permission{Allow: []string{"_INBOX.>"}}})
	js, err := jetstream.New(f.e.ConnAs(t, creds, "nopriv"))
	if err != nil {
		t.Fatal(err)
	}
	ep.js = js
	start := time.Now()
	if ep.LeaseRollupWarning(context.Background()) {
		t.Fatal("warned although the info read was denied")
	}
	if line, ok := ep.LeaseRollupLine(context.Background()); ok || line != "" {
		t.Fatalf("info denied: %q %v", line, ok)
	}
	if d := time.Since(start); d > 3*leaseRollupTimeout {
		t.Fatalf("took %s", d)
	}
}
