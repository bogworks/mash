package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// A retired bucket whose stream evicts old messages when full would let a full bucket drop a marker and free a name.
func TestRetiredBucketWithEvictionIsRefused(t *testing.T) {
	cases := map[string]func(*jetstream.StreamConfig){
		"discard-old": func(c *jetstream.StreamConfig) { c.Discard = jetstream.DiscardOld },
		"max-msgs":    func(c *jetstream.StreamConfig) { c.MaxMsgs = 10 },
		"max-age":     func(c *jetstream.StreamConfig) { c.MaxAge = time.Hour },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			s, err := js.Stream(ctx, "KV_"+store.RetiredBucket)
			if err != nil {
				t.Fatal(err)
			}
			if c := s.CachedInfo().Config; c.Discard != jetstream.DiscardNew || c.MaxAge != 0 {
				t.Fatalf("mash created the bucket with discard %v max age %v", c.Discard, c.MaxAge)
			}
			cfg := s.CachedInfo().Config
			mut(&cfg)
			if _, err := js.UpdateStream(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			if err := a.Init(ctx); err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
				t.Fatalf("Init must refuse, got %v", err)
			}
			if _, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team1", "ops2"), nil, user, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
				t.Fatalf("open must refuse, got %v", err)
			}
			rev := a.WithRevoker(func(context.Context, string) error { t.Error("revoked"); return nil })
			if err := rev.Deprovision(ctx, id.Flat()); err == nil || !strings.Contains(err.Error(), store.RetiredBucket) {
				t.Fatalf("deprovision must refuse, got %v", err)
			}
			recordIntact(t, js, id, user)
		})
	}
}

// A retired bucket held in memory would lose every marker on a server restart and let a retired name be reassigned. A
// stream's storage cannot be changed by an update, so the bucket is deleted and recreated memory-backed.
func TestRetiredBucketInMemoryIsRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := js.DeleteKeyValue(ctx, store.RetiredBucket); err != nil {
		t.Fatal(err)
	}
	cfg := store.RetiredConfig()
	cfg.Storage = jetstream.MemoryStorage
	if _, err := js.CreateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	wantMem := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), store.RetiredBucket) || !strings.Contains(err.Error(), "memory") {
			t.Fatalf("%s must refuse a memory-backed bucket, got %v", what, err)
		}
	}
	wantMem("Init", a.Init(ctx))
	_, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team1", "ops2"), nil, user, e.Account, "", admin.Meta{})
	wantMem("open", err)
	rev := a.WithRevoker(func(context.Context, string) error { t.Error("revoked"); return nil })
	wantMem("deprovision", rev.Deprovision(ctx, id.Flat()))
	recordIntact(t, js, id, user)
}

// An existing retired bucket that passes verification is never altered: Init must not update its stream (ADR 0088), even
// where the stored cap or description differs from what RetiredConfig would set.
func TestInitLeavesAVerifiedRetiredBucketUntouched(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := js.DeleteKeyValue(ctx, store.RetiredBucket); err != nil {
		t.Fatal(err)
	}
	cfg := store.RetiredConfig()
	cfg.MaxBytes = 32 << 20
	cfg.Description = "operator-tuned retired bucket"
	kv, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "marker-key", []byte("keep")); err != nil {
		t.Fatal(err)
	}
	if err := a.Init(ctx); err != nil {
		t.Fatalf("Init must accept a verified bucket, got %v", err)
	}
	s, err := js.Stream(ctx, "KV_"+store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.CachedInfo().Config; c.MaxBytes != 32<<20 || c.Description != "operator-tuned retired bucket" {
		t.Fatalf("Init altered the retired bucket: max bytes %d, description %q", c.MaxBytes, c.Description)
	}
	got, err := kv.Get(ctx, "marker-key")
	if err != nil || string(got.Value()) != "keep" {
		t.Fatalf("marker lost: %v", err)
	}
}

// A purge the server carried out whose reply was lost leaves the marker pending; a re-run finalizes it.
func TestAmbiguousPurgeLeavesMarkerPendingAndRerunFinalizes(t *testing.T) {
	for _, path := range bothPaths {
		for _, revokes := range []bool{false, true} {
			name := path + "/no-revoker"
			if revokes {
				name = path + "/revoker"
			}
			t.Run(name, func(t *testing.T) {
				e, a, js := setup(t)
				ctx := context.Background()
				id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
				x := a
				if revokes {
					x = a.WithRevoker(func(context.Context, string) error { return nil })
				}
				prev := admin.SetDirectoryPurgeFault(func() error { return context.DeadlineExceeded })
				err := runPath(ctx, x, path, id.Flat(), user)
				admin.SetDirectoryPurgeFault(prev)
				if err == nil || !strings.Contains(err.Error(), "unfinished") || !strings.Contains(err.Error(), "run") {
					t.Fatalf("want an unfinished-retirement error, got %v", err)
				}
				wantMarker(t, js, id.Flat(), store.RetiredPending, user)
				_, perr := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
				wantPendingBlock(t, perr, id)
				if err := runPath(ctx, x, path, id.Flat(), user); err != nil {
					t.Fatalf("re-run: %v", err)
				}
				wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
				gone(t, js, id.Flat())
			})
		}
	}
}

// A call that lost its name lock must not delete the marker; the next call finishes with a final marker.
func TestAbortWithLostLockLeavesMarker(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			boom := errors.New("resolver down")
			first := a.WithRevoker(func(context.Context, string) error {
				putLock(t, js, id.Flat(), time.Now().Add(time.Minute)) // the lock now belongs to another call
				return boom
			})
			err := runPath(ctx, first, path, id.Flat(), user)
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), "lock") {
				t.Fatalf("want the refusal and a lock-loss note, got %v", err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredPending, user)
			lease, _ := js.KeyValue(ctx, store.LeaseBucket)
			_ = lease.Purge(ctx, admin.LockKey(id.Flat()))
			second := a.WithRevoker(func(context.Context, string) error { return nil })
			if err := runPath(ctx, second, path, id.Flat(), user); err != nil {
				t.Fatal(err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			gone(t, js, id.Flat())
		})
	}
}

// A marker that vanished between the purge and the finalize is recreated as final: the record is gone, the name burned.
func TestFinalizeRecreatesAMissingMarker(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			prev := admin.SetAfterDeprovisionPurge(func() {
				rk, _ := js.KeyValue(ctx, store.RetiredBucket)
				if err := rk.Purge(ctx, id.Flat()); err != nil {
					t.Error(err)
				}
			})
			err := runPath(ctx, a, path, id.Flat(), user)
			admin.SetAfterDeprovisionPurge(prev)
			if err != nil {
				t.Fatal(err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			_, perr := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
			wantRetired(t, perr, id)
		})
	}
}

// rekey writes the record of name with another user key, bypassing the name lock the calling deprovision holds.
func rekey(t *testing.T, js jetstream.JetStream, name, user string) {
	t.Helper()
	ctx := context.Background()
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	e, err := kv.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var rec store.Record
	if err := json.Unmarshal(e.Value(), &rec); err != nil {
		t.Fatal(err)
	}
	rec.User = user
	v, _ := json.Marshal(rec)
	if _, err := kv.Put(ctx, name, v); err != nil {
		t.Fatal(err)
	}
}

// The guarded purge is refused because the record was re-keyed after our revoke: the retirement did not happen, so the
// marker this call wrote is deleted even though the old user was revoked, and the live name is not blocked.
func TestGuardedPurgeRefusedDeletesMarkerEvenAfterRevoke(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	_, other := e.Mint(t, "someone-else")
	var revoked []string
	rev := a.WithRevoker(func(_ context.Context, u string) error {
		revoked = append(revoked, u)
		rekey(t, js, id.Flat(), other)
		return nil
	})
	err := rev.DeprovisionIfUser(ctx, id.Flat(), user)
	if !errors.Is(err, admin.ErrRecordChanged) {
		t.Fatalf("want the guarded purge refused, got %v", err)
	}
	if len(revoked) != 1 {
		t.Fatalf("revoked %v", revoked)
	}
	if _, ok := readMarker(t, js, id.Flat()); ok {
		t.Fatal("the marker of a retirement that did not happen was left")
	}
	if got := recordUserOf(t, js, id.Flat()); got != other {
		t.Fatalf("record user %s", got)
	}
}

// A pending marker for another user key than a live record's was superseded: it is deleted and the removal goes on.
func TestSupersededPendingMarkerIsResolved(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			_, other := e.Mint(t, "someone-else")
			putMarker(t, js, id, other, store.RetiredPending)
			if err := runPath(context.Background(), a, path, id.Flat(), user); err != nil {
				t.Fatal(err)
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
			gone(t, js, id.Flat())
		})
	}
}

// A final marker with a live record of another user key is a burned name: refused, marker and record untouched.
func TestFinalMarkerWithLiveRecordOfAnotherUserRefuses(t *testing.T) {
	for _, path := range bothPaths {
		t.Run(path, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
			_, other := e.Mint(t, "someone-else")
			putMarker(t, js, id, other, store.RetiredFinal)
			var calls int
			rev := a.WithRevoker(func(context.Context, string) error { calls++; return nil })
			err := runPath(ctx, rev, path, id.Flat(), user)
			if err == nil || !strings.Contains(err.Error(), "INV-002") || !strings.Contains(err.Error(), id.Flat()) {
				t.Fatalf("want an INV-002 refusal naming the burned name, got %v", err)
			}
			if calls != 0 {
				t.Fatal("revoked")
			}
			wantMarker(t, js, id.Flat(), store.RetiredFinal, other)
			recordIntact(t, js, id, user)
		})
	}
}
