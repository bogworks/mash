package admin_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func TestPlannedRotation(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	s1, s2, s3 := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	ex := pubKey(t, nkeys.CreateUser)

	if _, err := a.SetNextSubmitter(ctx, s2); !errors.Is(err, admin.ErrNotAdopted) {
		t.Fatalf("not adopted: %v", err)
	}
	if _, _, err := a.Adopt(ctx, acct, s1, by, []string{ex}); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]struct {
		key  string
		want error
	}{
		"invalid": {"hello", admin.ErrSubmitterInvalid}, "account": {acct, admin.ErrSubmitterIsAccount},
		"current": {s1, admin.ErrNextIsCurrent}, "admin": {by, admin.ErrNextIsMashKey}, "exempt": {ex, admin.ErrNextIsMashKey},
	} {
		if _, err := a.SetNextSubmitter(ctx, bad.key); !errors.Is(err, bad.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m, err := a.SetNextSubmitter(ctx, s2)
	if err != nil || m.NextSubmitter != s2 || m.Submitter != s1 || m.Action != store.ActionRotate || !slices.Equal(m.Exempt, []string{ex}) || m.By != by {
		t.Fatalf("%+v %v", m, err)
	}
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	h1, _ := kv.History(ctx, store.AdoptionKey)
	if _, err := a.SetNextSubmitter(ctx, s2); err != nil { // idempotent
		t.Fatal(err)
	}
	if h2, _ := kv.History(ctx, store.AdoptionKey); len(h2) != len(h1) {
		t.Error("an idempotent SetNext wrote a revision")
	}
	if _, err := a.SetNextSubmitter(ctx, s3); !errors.Is(err, admin.ErrNextPending) {
		t.Fatalf("%v", err)
	}
	if _, err := a.PromoteSubmitter(ctx, s3); !errors.Is(err, admin.ErrNextMismatch) {
		t.Fatalf("%v", err)
	}
	m, err = a.PromoteSubmitter(ctx, s2)
	if err != nil || m.Submitter != s2 || m.NextSubmitter != "" || m.Action != store.ActionRotate || !slices.Equal(m.Exempt, []string{ex}) {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := a.PromoteSubmitter(ctx, s2); err != nil { // idempotent after promotion
		t.Fatal(err)
	}
	if h, _ := kv.History(ctx, store.AdoptionKey); len(h) != 3 {
		t.Errorf("history %d, want 3", len(h))
	}
	// A recovery adopt clears a pending key; a promotion of it is then a mismatch.
	if _, err := a.SetNextSubmitter(ctx, s3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Adopt(ctx, acct, s1, by, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PromoteSubmitter(ctx, s3); !errors.Is(err, admin.ErrNextMismatch) {
		t.Fatalf("%v", err)
	}
}
