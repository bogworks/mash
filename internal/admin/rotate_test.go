package admin_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
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

	if _, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0, nil); !errors.Is(err, admin.ErrNotAdopted) {
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
		if _, _, err := a.SetNextSubmitter(ctx, bad.key, rev(), 0, nil); !errors.Is(err, bad.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	m, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0, nil)
	if err != nil || m.NextSubmitter != s2 || m.Submitter != s1 || m.Action != store.ActionRotate || !slices.Equal(m.Exempt, []string{ex}) || m.By != by {
		t.Fatalf("%+v %v", m, err)
	}
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	h1, _ := kv.History(ctx, store.AdoptionKey)
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev(), 0, nil); err != nil { // idempotent
		t.Fatal(err)
	}
	if h2, _ := kv.History(ctx, store.AdoptionKey); len(h2) != len(h1) {
		t.Error("an idempotent SetNext wrote a revision")
	}
	if _, _, err := a.SetNextSubmitter(ctx, s3, rev(), 0, nil); !errors.Is(err, admin.ErrNextPending) {
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
	if _, _, err := a.SetNextSubmitter(ctx, s3, rev(), 0, nil); err != nil {
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
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev0, 0, nil); !errors.Is(err, admin.ErrMarkerMoved) {
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
	m, rev2, err := a.SetNextSubmitter(ctx, s2, rev1, 0, nil)
	if err != nil || m.NextSubmitter != s2 || rev2 <= rev1 {
		t.Fatalf("%+v %d %v", m, rev2, err)
	}
	// The same call again (the lost-answer retry) is idempotent only against our own write's revision.
	n = history()
	if _, got, err := a.SetNextSubmitter(ctx, s2, rev1, rev2, nil); err != nil || got != rev2 || history() != n {
		t.Fatalf("idempotent retry: %d %v", got, err)
	}
	if _, _, err := a.SetNextSubmitter(ctx, s2, rev1, 0, nil); !errors.Is(err, admin.ErrMarkerMoved) {
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

// H1: a next submitter that is a key mash holds is refused, whatever kind of key it is, and nothing is written.
func TestNextSubmitterRefusesMashHeldKeys(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	s1, fresh := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	if _, _, err := a.Adopt(ctx, acct, s1, by, nil); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	history := func() int { h, _ := kv.History(ctx, store.AdoptionKey); return len(h) }
	rev := func() uint64 { _, r, _, _ := a.AdoptionRev(ctx); return r }
	classes := map[string]string{
		"operator": pubKey(t, nkeys.CreateOperator), "system": pubKey(t, nkeys.CreateAccount), "account": pubKey(t, nkeys.CreateAccount),
		"zone": pubKey(t, nkeys.CreateAccount), "admin": pubKey(t, nkeys.CreateUser),
	}
	var held []string
	for _, k := range classes {
		held = append(held, k)
	}
	n := history()
	for name, k := range classes {
		// ValidateNextSubmitter sees the key itself first: a non-user key is invalid, a user key is a mash key.
		_, _, err := a.SetNextSubmitter(ctx, k, rev(), 0, held)
		if !errors.Is(err, admin.ErrNextIsMashKey) && !errors.Is(err, admin.ErrSubmitterInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := a.SetNextSubmitter(ctx, classes["admin"], rev(), 0, held); !errors.Is(err, admin.ErrNextIsMashKey) {
		t.Errorf("a held user key: %v", err)
	}
	if history() != n {
		t.Fatal("a refused key wrote a revision")
	}
	if got := admin.ValidateNextSubmitter(acct, store.Marker{Submitter: s1}, classes["admin"], held); !errors.Is(got, admin.ErrNextIsMashKey) {
		t.Errorf("validate: %v", got)
	}
	m, _, err := a.SetNextSubmitter(ctx, fresh, rev(), 0, held)
	if err != nil || m.NextSubmitter != fresh {
		t.Fatalf("a fresh key: %+v %v", m, err)
	}
}

type rotateRig struct {
	a       *admin.Admin
	kv      jetstream.KeyValue
	acct    string
	s1, s2  string
	by      string
	ctx     context.Context
	history func() int
}

func newRotateRig(t *testing.T) *rotateRig {
	t.Helper()
	_, a, js := setup(t)
	ctx := context.Background()
	r := &rotateRig{a: a, ctx: ctx, acct: pubKey(t, nkeys.CreateAccount), by: pubKey(t, nkeys.CreateUser),
		s1: pubKey(t, nkeys.CreateUser), s2: pubKey(t, nkeys.CreateUser)}
	if _, _, err := a.Adopt(ctx, r.acct, r.s1, r.by, nil); err != nil {
		t.Fatal(err)
	}
	r.kv, _ = js.KeyValue(ctx, store.AdoptionBucket)
	r.history = func() int { h, _ := r.kv.History(ctx, store.AdoptionKey); return len(h) }
	return r
}

func (r *rotateRig) rev() uint64 { _, rev, _, _ := r.a.AdoptionRev(r.ctx); return rev }

var errTimeout = errors.New("nats: timeout")

// lostReply runs the write (it commits) and reports a timeout.
func lostReply(write func() (uint64, error)) (uint64, error) { _, _ = write(); return 0, errTimeout }

// H3: JetStream commits the write and the answer is lost. The retry has no own revision; the marker read back is the
// intended end state at the very next revision, so the write is recognised as done.
func TestAmbiguousMarkerWriteIsRecognised(t *testing.T) {
	for _, op := range []string{"setnext", "promote"} {
		t.Run(op, func(t *testing.T) {
			r := newRotateRig(t)
			rev := r.rev()
			if op == "promote" {
				if _, _, err := r.a.SetNextSubmitter(r.ctx, r.s2, rev, 0, nil); err != nil {
					t.Fatal(err)
				}
				rev = r.rev()
			}
			defer admin.SetMarkerWriteFault(lostReply)()
			n := r.history()
			var m store.Marker
			var got uint64
			var err error
			if op == "setnext" {
				m, got, err = r.a.SetNextSubmitter(r.ctx, r.s2, rev, 0, nil)
			} else {
				m, got, err = r.a.PromoteSubmitter(r.ctx, r.s2, rev, 0)
			}
			if err != nil || got != rev+1 || r.rev() != got || r.history() != n+1 {
				t.Fatalf("committed, reply lost: rev %d (want %d) %v", got, rev+1, err)
			}
			if op == "setnext" && (m.NextSubmitter != r.s2 || m.Submitter != r.s1) || op == "promote" && (m.Submitter != r.s2 || m.NextSubmitter != "") {
				t.Fatalf("marker %+v", m)
			}
		})
	}
}

// Not ours: another writer's marker at expectedRev+1 with another end state, and an intervening write at
// expectedRev+2, are ErrMarkerMoved, for both functions.
func TestAmbiguousMarkerWriteNotOursIsMoved(t *testing.T) {
	for _, op := range []string{"setnext", "promote"} {
		call := func(r *rotateRig, rev uint64) error {
			var err error
			if op == "setnext" {
				_, _, err = r.a.SetNextSubmitter(r.ctx, r.s2, rev, 0, nil)
			} else {
				_, _, err = r.a.PromoteSubmitter(r.ctx, r.s2, rev, 0)
			}
			return err
		}
		prep := func(r *rotateRig) uint64 {
			if op == "promote" {
				if _, _, err := r.a.SetNextSubmitter(r.ctx, r.s2, r.rev(), 0, nil); err != nil {
					t.Fatal(err)
				}
			}
			return r.rev()
		}
		t.Run(op+"/rival at the next revision", func(t *testing.T) {
			r := newRotateRig(t)
			rev := prep(r)
			rec := pubKey(t, nkeys.CreateUser)
			// Our write never reaches the server; a recovery adopt lands at rev+1 and the answer is a timeout.
			defer admin.SetMarkerWriteFault(func(func() (uint64, error)) (uint64, error) {
				if _, _, err := r.a.Adopt(r.ctx, r.acct, rec, r.by, nil); err != nil {
					t.Error(err)
				}
				return 0, errTimeout
			})()
			if err := call(r, rev); !errors.Is(err, admin.ErrMarkerMoved) {
				t.Fatalf("%v", err)
			}
			if r.rev() != rev+1 {
				t.Fatalf("revision %d", r.rev())
			}
		})
		t.Run(op+"/intervening write", func(t *testing.T) {
			r := newRotateRig(t)
			rev := prep(r)
			rec := pubKey(t, nkeys.CreateUser)
			// Our write commits, then another writer moves the marker on before the read-back.
			defer admin.SetMarkerWriteFault(func(write func() (uint64, error)) (uint64, error) {
				_, _ = write()
				if _, _, err := r.a.Adopt(r.ctx, r.acct, rec, r.by, nil); err != nil {
					t.Error(err)
				}
				return 0, errTimeout
			})()
			if err := call(r, rev); !errors.Is(err, admin.ErrMarkerMoved) {
				t.Fatalf("%v", err)
			}
			if r.rev() != rev+2 {
				t.Fatalf("revision %d", r.rev())
			}
		})
		t.Run(op+"/other end state at the next revision", func(t *testing.T) {
			r := newRotateRig(t)
			rev := prep(r)
			// Another writer rotates to a different key at rev+1 (a recovery adopt of the same marker shape).
			other := pubKey(t, nkeys.CreateUser)
			defer admin.SetMarkerWriteFault(func(func() (uint64, error)) (uint64, error) {
				if _, _, err := r.a.Adopt(r.ctx, r.acct, other, r.by, nil); err != nil {
					t.Error(err)
				}
				return 0, errTimeout
			})()
			if err := call(r, rev); !errors.Is(err, admin.ErrMarkerMoved) {
				t.Fatalf("%v", err)
			}
		})
	}
}

// A clean failure that committed nothing returns the error as before and writes nothing.
func TestCleanMarkerWriteFailureReturnsTheError(t *testing.T) {
	r := newRotateRig(t)
	rev := r.rev()
	n := r.history()
	defer admin.SetMarkerWriteFault(func(func() (uint64, error)) (uint64, error) { return 0, errTimeout })()
	if _, _, err := r.a.SetNextSubmitter(r.ctx, r.s2, rev, 0, nil); !errors.Is(err, errTimeout) || errors.Is(err, admin.ErrMarkerMoved) {
		t.Fatalf("%v", err)
	}
	if r.rev() != rev || r.history() != n {
		t.Fatal("a failed write changed the marker")
	}
}
