package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func leasePresent(t *testing.T, js jetstream.JetStream, name string) bool {
	t.Helper()
	lease, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lease.Get(context.Background(), name)
	return err == nil
}

func TestDeprovisionIfUserMatchRemoves(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err == nil {
		t.Fatal("inbox still exists")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "o_dev_alpha"); err == nil {
		t.Fatal("record still exists")
	}
	if !leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("the lease was not fenced")
	}
	// The address is retired for good (INV-002): a re-provision is refused.
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), "INV-002") {
		t.Fatalf("re-provision: %v, want the retired refusal", err)
	}
}

func TestDeprovisionIfUserOtherUserRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	_, other := e.Mint(t, ep(t, "beta"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	before, _ := kv.Get(ctx, "o_dev_alpha")
	err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), other)
	if !errors.Is(err, admin.ErrRecordOtherUser) {
		t.Fatalf("err %v, want ErrRecordOtherUser", err)
	}
	want := "mash: refusing to deprovision o_dev_alpha: its record belongs to another user key (left in place)"
	if err.Error() != want {
		t.Fatalf("message %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), user[:9]) || strings.Contains(err.Error(), other[:9]) {
		t.Fatalf("message prints more of a key than 8 characters: %v", err)
	}
	after, gerr := kv.Get(ctx, "o_dev_alpha")
	if gerr != nil || after.Revision() != before.Revision() {
		t.Fatalf("record touched: %v", gerr)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err != nil {
		t.Fatalf("inbox touched: %v", err)
	}
	if leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("a refused call fenced the lease")
	}
}

func TestDeprovisionIfUserMetadataChangeRetriesAndRemoves(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	// Another admin changes the metadata (same user, new revision) once between the guard's read and its delete.
	changes := 0
	prev := admin.SetAfterDeprovisionGet(func() {
		if changes++; changes > 1 {
			return
		}
		if _, err := a.SetMeta(ctx, ep(t, "alpha"), admin.MetaUpdate{Role: sp("replaced")}); err != nil {
			t.Error(err)
		}
	})
	defer func() { admin.SetAfterDeprovisionGet(prev) }()
	if err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user); err != nil {
		t.Fatalf("a metadata-only change refused the deprovision: %v", err)
	}
	if _, err := store.GetRecord(ctx, kv, "o_dev_alpha"); err == nil {
		t.Fatal("record still present")
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err == nil {
		t.Fatal("inbox not deleted")
	}
	if !leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("lease not fenced")
	}
}

func TestDeprovisionIfUserUserChangeBetweenReadAndPurgeRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	_, other := e.Mint(t, ep(t, "beta"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	done := false
	prev := admin.SetAfterDeprovisionGet(func() {
		if done {
			return
		}
		done = true
		old, err := store.GetRecord(ctx, kv, "o_dev_alpha")
		if err != nil {
			t.Error(err)
			return
		}
		old.User = other
		val, _ := json.Marshal(old)
		if _, err := kv.Put(ctx, "o_dev_alpha", val); err != nil {
			t.Error(err)
		}
	})
	defer func() { admin.SetAfterDeprovisionGet(prev) }()
	err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user)
	if !errors.Is(err, admin.ErrRecordChanged) {
		t.Fatalf("err %v, want ErrRecordChanged", err)
	}
	if rec, gerr := store.GetRecord(ctx, kv, "o_dev_alpha"); gerr != nil || rec.User != other {
		t.Fatalf("the record was touched: %+v %v", rec, gerr)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err != nil {
		t.Fatalf("inbox touched: %v", err)
	}
	if leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("a refused call fenced the lease")
	}
}

func TestDeprovisionIfUserRetriesExhaustedRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	n := 0
	prev := admin.SetAfterDeprovisionGet(func() {
		n++
		if _, err := a.SetMeta(ctx, ep(t, "alpha"), admin.MetaUpdate{Role: sp(fmt.Sprintf("r%d", n))}); err != nil {
			t.Error(err)
		}
	})
	defer func() { admin.SetAfterDeprovisionGet(prev) }()
	err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user)
	if !errors.Is(err, admin.ErrRecordChanged) || !strings.Contains(err.Error(), "the record for o_dev_alpha changed; left in place") {
		t.Fatalf("err %v, want the changed-record refusal", err)
	}
	if rec, gerr := store.GetRecord(ctx, kv, "o_dev_alpha"); gerr != nil || rec.User != user {
		t.Fatalf("the record was lost: %+v %v", rec, gerr)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err != nil {
		t.Fatalf("inbox touched: %v", err)
	}
	if leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("a refused call fenced the lease")
	}
}

func TestDeprovisionIfUserRecreatedByOtherLeavesIt(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	_, other := e.Mint(t, ep(t, "beta"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	// Admins now serialise per name, so the replacement is written straight to the directory, as an admin without
	// the lock would (an older mash, or a lock that expired).
	prev := admin.SetAfterDeprovisionGet(func() {
		old, err := store.GetRecord(ctx, kv, "o_dev_alpha")
		if err != nil {
			t.Error(err)
			return
		}
		if err := kv.Purge(ctx, "o_dev_alpha"); err != nil {
			t.Error(err)
		}
		old.User = other
		val, _ := json.Marshal(old)
		if _, err := kv.Create(ctx, "o_dev_alpha", val); err != nil {
			t.Error(err)
		}
	})
	defer func() { admin.SetAfterDeprovisionGet(prev) }()
	if err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user); !errors.Is(err, admin.ErrRecordChanged) {
		t.Fatalf("err %v, want ErrRecordChanged", err)
	}
	if rec, err := store.GetRecord(ctx, kv, "o_dev_alpha"); err != nil || rec.User != other {
		t.Fatalf("the other admin's record was lost: %+v %v", rec, err)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err != nil {
		t.Fatalf("inbox touched: %v", err)
	}
}

func TestDeprovisionIfUserNoRecordRefusedStreamKept(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	// A provision that failed after creating the stream, before the record.
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := kv.Purge(ctx, "o_dev_alpha"); err != nil {
		t.Fatal(err)
	}
	err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user)
	if !errors.Is(err, admin.ErrNoRecord) || !strings.Contains(err.Error(), "refusing to deprovision o_dev_alpha") {
		t.Fatalf("err %v, want the no-record refusal", err)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err != nil {
		t.Fatalf("the stream was deleted: %v", err)
	}
	if leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("a refused call fenced the lease")
	}
	// Never provisioned at all: also a refusal.
	if err := a.DeprovisionIfUser(ctx, ep(t, "ghost"), user); !errors.Is(err, admin.ErrNoRecord) {
		t.Fatalf("ghost: %v", err)
	}
	// Without a key the plain deprovision still clears it.
	if err := a.Deprovision(ctx, ep(t, "alpha")); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err == nil {
		t.Fatal("plain deprovision left the stream")
	}
}

// Another admin provisions the address after the guarded record delete and before the fence. The address's lock makes
// that Provision wait for the whole deprovision; it then finds the address retired (INV-002) and is refused, so the
// first call's fence and stream delete are never undone and nothing of the address is recreated.
func TestDeprovisionIfUserHoldsNameAcrossPurgeAndFence(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, ep(t, "alpha"))
	_, other := e.Mint(t, ep(t, "beta"))
	if _, err := a.Provision(ctx, ep(t, "alpha"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	b, _ := initOn(t, e)
	done := make(chan error, 1)
	var completedInSeam bool
	prev := admin.SetAfterDeprovisionPurge(func() {
		go func() {
			_, err := b.Provision(ctx, ep(t, "alpha"), other, e.Account, "", admin.Meta{})
			done <- err
		}()
		select {
		case <-done:
			completedInSeam = true
		case <-time.After(1500 * time.Millisecond):
		}
	})
	defer func() { admin.SetAfterDeprovisionPurge(prev) }()
	if err := a.DeprovisionIfUser(ctx, ep(t, "alpha"), user); err != nil {
		t.Fatal(err)
	}
	if completedInSeam {
		t.Fatal("a second admin's Provision completed between the record delete and the fence")
	}
	select {
	case err := <-done:
		wantRetired(t, err, ep(t, "alpha"))
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting Provision never finished")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := store.GetRecord(ctx, kv, "o_dev_alpha"); err == nil {
		t.Fatal("a record exists for the retired address")
	}
	if !leasePresent(t, js, "o_dev_alpha") {
		t.Fatal("the lease was not fenced")
	}
	if _, err := js.Stream(ctx, "inbox-o_dev_alpha"); err == nil {
		t.Fatal("the inbox was recreated for the retired address")
	}
}
