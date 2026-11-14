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
	rev := func() uint64 { _, r, _, _ := a.AdoptionRev(ctx); return r }

	if _, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0); !errors.Is(err, admin.ErrNotAdopted) {
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
		if _, _, err := a.SetNextSubmitter(ctx, bad.key, rev(), 0); !errors.Is(err, bad.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0)
	if err != nil || m.NextSubmitter != s2 || m.Submitter != s1 || m.Action != store.ActionRotate || !slices.Equal(m.Exempt, []string{ex}) || m.By != by {
		t.Fatalf("%+v %v", m, err)
	}
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	h1, _ := kv.History(ctx, store.AdoptionKey)
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0); err != nil { // idempotent
		t.Fatal(err)
	}
	if h2, _ := kv.History(ctx, store.AdoptionKey); len(h2) != len(h1) {
		t.Error("an idempotent SetNext wrote a revision")
	}
	if _, _, err := a.SetNextSubmitter(ctx, s3, rev(), 0); !errors.Is(err, admin.ErrNextPending) {
		t.Fatalf("%v", err)
	}
	if _, _, err := a.PromoteSubmitter(ctx, s3, rev(), 0); !errors.Is(err, admin.ErrNextMismatch) {
		t.Fatalf("%v", err)
	}
	m, _, err = a.PromoteSubmitter(ctx, s2, rev(), 0)
	if err != nil || m.Submitter != s2 || m.NextSubmitter != "" || m.Action != store.ActionRotate || !slices.Equal(m.Exempt, []string{ex}) {
		t.Fatalf("%+v %v", m, err)
	}
	if _, _, err := a.PromoteSubmitter(ctx, s2, rev(), 0); err != nil { // idempotent after promotion
		t.Fatal(err)
	}
	if h, _ := kv.History(ctx, store.AdoptionKey); len(h) != 3 {
		t.Errorf("history %d, want 3", len(h))
	}
	// A recovery adopt clears a pending key; a promotion of it is then a mismatch.
	if _, _, err := a.SetNextSubmitter(ctx, s3, rev(), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Adopt(ctx, acct, s1, by, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.PromoteSubmitter(ctx, s3, rev(), 0); !errors.Is(err, admin.ErrNextMismatch) {
		t.Fatalf("%v", err)
	}
}

// The rotation writes are bound to the marker revision the document was verified under (H2).
func TestRotationIsBoundToTheVerifiedRevision(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	s1, s2, rec := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	if _, _, err := a.Adopt(ctx, acct, s1, by, nil); err != nil {
		t.Fatal(err)
	}
	_, rev0, _, err := a.AdoptionRev(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	history := func() int { h, _ := kv.History(ctx, store.AdoptionKey); return len(h) }

	// A recovery adopt moves the marker after the document was verified at rev0.
	if _, _, err := a.Adopt(ctx, acct, rec, by, nil); err != nil {
		t.Fatal(err)
	}
	n := history()
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev0, 0); !errors.Is(err, admin.ErrMarkerMoved) {
		t.Fatalf("SetNext on a moved marker: %v", err)
	}
	if _, _, err := a.PromoteSubmitter(ctx, s2, rev0, 0); !errors.Is(err, admin.ErrMarkerMoved) {
		t.Fatalf("Promote on a moved marker: %v", err)
	}
	if m, _, _, _ := a.AdoptionRev(ctx); m.Submitter != rec || m.NextSubmitter != "" || history() != n {
		t.Fatalf("a moved-marker write changed the marker: %+v", m)
	}

	// An unmoved marker records as before, and the write returns its new revision.
	_, rev1, _, _ := a.AdoptionRev(ctx)
	m, rev2, err := a.SetNextSubmitter(ctx, s2, rev1, 0)
	if err != nil || m.NextSubmitter != s2 || rev2 <= rev1 {
		t.Fatalf("%+v %d %v", m, rev2, err)
	}
	// The same call again (the lost-answer retry) is idempotent only against our own write's revision.
	n = history()
	if _, got, err := a.SetNextSubmitter(ctx, s2, rev1, rev2); err != nil || got != rev2 || history() != n {
		t.Fatalf("idempotent retry: %d %v", got, err)
	}
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev1, 0); !errors.Is(err, admin.ErrMarkerMoved) {
		t.Fatalf("a stale revision with the end state present must still be refused: %v", err)
	}
	m, rev3, err := a.PromoteSubmitter(ctx, s2, rev2, 0)
	if err != nil || m.Submitter != s2 || rev3 <= rev2 {
		t.Fatalf("%+v %v", m, err)
	}
	if _, got, err := a.PromoteSubmitter(ctx, s2, rev2, rev3); err != nil || got != rev3 {
		t.Fatalf("idempotent promote: %d %v", got, err)
	}
}
