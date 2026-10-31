package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func bigLimits() store.Limits {
	return store.Limits{InboxMaxBytes: 32 << 20, InboxMaxAge: 60 * 24 * time.Hour,
		DirectoryMaxBytes: 32 << 20, LeaseMaxBytes: 8 << 20, InboxMaxConsumers: 8}
}

func TestSetLimitsValidates(t *testing.T) {
	_, a, _ := setup(t)
	bad := store.DefaultLimits()
	bad.InboxMaxAge = time.Minute
	if err := a.SetLimits(bad); err == nil || !strings.Contains(err.Error(), "inbox_max_age") {
		t.Fatalf("got %v", err)
	}
	if err := a.SetLimits(bigLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestInitRaisesInPlaceAndNeverLowers(t *testing.T) {
	for _, c := range []struct {
		name          string
		existingDir   int64
		existingLease int64
		wantDir       int64
		wantLease     int64
	}{
		{"raise", 1 << 20, 1 << 20, 32 << 20, 8 << 20},
		{"keep larger", 64 << 20, 64 << 20, 64 << 20, 64 << 20},
		{"equal", 32 << 20, 8 << 20, 32 << 20, 8 << 20},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := testenv.Start(t)
			a, js := initOn(t, e)
			ctx := context.Background()
			for _, b := range []struct {
				cfg jetstream.KeyValueConfig
				n   int64
			}{{store.DirectoryConfig(), c.existingDir}, {store.LeaseConfig(), c.existingLease}} {
				b.cfg.MaxBytes = b.n
				kv, err := js.CreateKeyValue(ctx, b.cfg)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := kv.Put(ctx, "k", []byte("v")); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.SetLimits(bigLimits()); err != nil {
				t.Fatal(err)
			}
			if err := a.Init(ctx); err != nil {
				t.Fatal(err)
			}
			if got := bucketBytes(t, js, store.DirectoryBucket); got != c.wantDir {
				t.Fatalf("directory %d, want %d", got, c.wantDir)
			}
			if got := bucketBytes(t, js, store.LeaseBucket); got != c.wantLease {
				t.Fatalf("lease %d, want %d", got, c.wantLease)
			}
			kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
			if _, err := kv.Get(ctx, "k"); err != nil {
				t.Fatalf("key lost: %v", err)
			}
		})
	}
}

func TestProvisionWithCustomLimits(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	if err := a.SetLimits(bigLimits()); err != nil {
		t.Fatal(err)
	}
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(ctx, "inbox-alpha")
	if err != nil {
		t.Fatal(err)
	}
	c := s.CachedInfo().Config
	if c.MaxBytes != 32<<20 || c.MaxAge != 60*24*time.Hour || c.MaxConsumers != 8 {
		t.Fatalf("config %+v", c)
	}
	// EnsureInbox is checked against the configured limits: fine with them, an error with the defaults.
	if _, err := a.EnsureInbox(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetLimits(store.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "max_age") {
		t.Fatalf("EnsureInbox with other limits: %v", err)
	}
}

func provisionTwo(t *testing.T, e *testenv.Env, a *admin.Admin) {
	t.Helper()
	for _, n := range []string{"alpha", "beta"} {
		_, user := e.Mint(t, n)
		if _, err := a.Provision(context.Background(), n, user, e.Account, "", admin.Meta{}); err != nil {
			t.Fatal(err)
		}
	}
}

func inboxCfg(t *testing.T, js jetstream.JetStream, name string) jetstream.StreamConfig {
	t.Helper()
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return s.CachedInfo().Config
}

func TestApplyLimitsRaisesAndIsIdempotent(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionTwo(t, e, a)
	if lines, err := a.ApplyLimits(ctx); err != nil || len(lines) != 2 || !strings.HasSuffix(lines[0], "unchanged") {
		t.Fatalf("defaults: %v %v", lines, err)
	}
	if err := a.SetLimits(bigLimits()); err != nil {
		t.Fatal(err)
	}
	lines, err := a.ApplyLimits(ctx, "alpha")
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "alpha: ") ||
		!strings.Contains(lines[0], "max_bytes") || !strings.Contains(lines[0], "max_age") || !strings.Contains(lines[0], "max_consumers") {
		t.Fatalf("named: %v %v", lines, err)
	}
	if c := inboxCfg(t, js, "alpha"); c.MaxBytes != 32<<20 || c.MaxAge != 60*24*time.Hour || c.MaxConsumers != 8 {
		t.Fatalf("alpha %+v", c)
	}
	if c := inboxCfg(t, js, "beta"); c.MaxBytes != 16<<20 {
		t.Fatalf("beta touched: %+v", c)
	}
	lines, err = a.ApplyLimits(ctx) // all; alpha is already there
	if err != nil || len(lines) != 2 || lines[0] != "alpha: unchanged" || !strings.HasPrefix(lines[1], "beta: max_bytes") {
		t.Fatalf("all: %v %v", lines, err)
	}
	if lines, err = a.ApplyLimits(ctx); err != nil || lines[0] != "alpha: unchanged" || lines[1] != "beta: unchanged" {
		t.Fatalf("idempotent: %v %v", lines, err)
	}
}

func TestApplyLimitsRefusesToLower(t *testing.T) {
	for _, c := range []struct {
		field string
		mod   func(*store.Limits)
	}{
		{"max_bytes", func(l *store.Limits) { l.InboxMaxBytes = 4 << 20 }},
		{"max_age", func(l *store.Limits) { l.InboxMaxAge = 24 * time.Hour }},
	} {
		t.Run(c.field, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			provisionTwo(t, e, a)
			l := store.DefaultLimits()
			c.mod(&l)
			l.InboxMaxConsumers = 3 // would change, but must not when the endpoint is refused
			if err := a.SetLimits(l); err != nil {
				t.Fatal(err)
			}
			before := inboxCfg(t, js, "alpha")
			lines, err := a.ApplyLimits(ctx, "alpha")
			if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), c.field) || len(lines) != 0 {
				t.Fatalf("lines %v err %v", lines, err)
			}
			if after := inboxCfg(t, js, "alpha"); after.MaxBytes != before.MaxBytes || after.MaxAge != before.MaxAge || after.MaxConsumers != before.MaxConsumers {
				t.Fatalf("changed despite refusal: %+v -> %+v", before, after)
			}
			// With several endpoints the refusal is reported for each.
			if _, err := a.ApplyLimits(ctx); err == nil || !strings.Contains(err.Error(), "beta") {
				t.Fatalf("all: %v", err)
			}
		})
	}
}
