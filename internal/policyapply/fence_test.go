package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

var errFenced = errors.New("lock expired")

// fenceRevoker records each revocation and makes the servers hold the target with the keys revoked.
type fenceRevoker struct {
	w  *world14
	t  *testing.T
	ev *events
}

func (r fenceRevoker) Revoke(_ context.Context, pubs []string) (string, error) {
	r.ev.add("revoke")
	tok := r.w.k.build(r.t, r.w.c2, append([]string{r.w.k.revoked}, pubs...)...)
	for _, s := range r.w.cl.names {
		r.w.cl.set(s, tok)
	}
	return tok, nil
}

// fenceWorld is the world of AT-014 with a Provisioner and an AfterPush that revokes the keys of foo.c.core through
// the RotationsAfterPush Revoker, and a hook that records what it is asked and refuses the nth call of label refuse.
func fenceWorld(t *testing.T, refuse string, nth int) (*world14, *events) {
	t.Helper()
	w := newWorld14(t)
	rec := &MemIssueRecord{}
	if err := rec.Add(context.Background(), IssueEntry{Address: "foo.c.core", PublicKey: userPub(t), Purpose: PurposeIssue, IssuedAt: fixedNow()}); err != nil {
		t.Fatal(err)
	}
	plan := &RotationPlan{Leaving: []string{"foo.c.core"}, Record: rec, Revoker: fenceRevoker{w, t, w.ev}, Generation: 2, Now: fixedNow}
	w.in.AfterPush = RotationsAfterPush(plan)
	w.in.OldKeys = map[string][]string{"foo.c.docs": {}}
	seen := map[string]int{}
	w.in.BeforeWrite = func(_ context.Context, what string) error {
		w.ev.add("fence:" + what)
		seen[what]++
		if what == refuse && seen[what] == nth {
			return errFenced
		}
		return nil
	}
	return w, w.ev
}

func labels(ev []string) []string {
	var out []string
	for _, e := range ev {
		if l, ok := strings.CutPrefix(e, "fence:"); ok {
			out = append(out, l)
		}
	}
	return out
}

func pendingGen(t *testing.T, l *MemLedger) (uint64, bool) {
	t.Helper()
	rec, ok, err := l.Pending()
	if err != nil {
		t.Fatal(err)
	}
	return rec.Generation, ok
}

func hasFence(st Status) bool {
	return slices.ContainsFunc(st.Failing, func(f Failure) bool { return f.Check == CheckFence })
}

func TestBeforeWriteCalledInOrderOnSuccess(t *testing.T) {
	w, ev := fenceWorld(t, "", 0)
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil {
		t.Fatal(err)
	}
	if os.Status.State != StateApplied {
		t.Fatalf("status = %+v", os.Status)
	}
	if got, want := labels(ev.get()), []string{FencePush, FenceRevoke, FenceProvision, FenceProvision, FenceCommit}; !slices.Equal(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
	log := ev.get()
	if want := []string{"fence:push", "push", "fence:revoke", "revoke"}; !slices.Equal(log[:4], want) {
		t.Errorf("events = %v", log)
	}
	// Each label comes immediately before its write.
	for i, e := range log {
		switch e {
		case "fence:provision":
			if !strings.HasPrefix(log[i+1], "create:") && !strings.HasPrefix(log[i+1], "issue:") {
				t.Errorf("provision hook not followed by its write: %v", log)
			}
		case "fence:commit":
			if log[i+1] != "commit" {
				t.Errorf("commit hook not followed by the commit: %v", log)
			}
		}
	}
}

func TestBeforeWriteNilIsNoop(t *testing.T) {
	w, _ := fenceWorld(t, "", 0)
	w.in.BeforeWrite = nil
	if os, err := RunOrdered(context.Background(), w.in, w.prov); err != nil || os.Status.State != StateApplied {
		t.Fatalf("%+v %v", os, err)
	}
}

func TestBeforeWriteRefusals(t *testing.T) {
	cases := []struct {
		label                                     string
		nth                                       int
		pushes, revokes, creates, issues, commits int
		pending                                   bool
	}{
		{FencePush, 1, 0, 0, 0, 0, 0, false},
		{FenceRevoke, 1, 1, 0, 0, 0, 0, true},
		{FenceProvision, 1, 1, 1, 0, 0, 0, true},
		{FenceProvision, 2, 1, 1, 1, 0, 0, true},
		{FenceCommit, 1, 1, 1, 1, 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.label+"-"+string(rune('0'+c.nth)), func(t *testing.T) {
			w, ev := fenceWorld(t, c.label, c.nth)
			os, err := RunOrdered(context.Background(), w.in, w.prov)
			if !errors.Is(err, errFenced) {
				t.Fatalf("err = %v, want the hook's error", err)
			}
			var fe *FenceError
			if !errors.As(err, &fe) || fe.What != c.label {
				t.Errorf("err = %v, want a FenceError for %q", err, c.label)
			}
			count := func(p string) int {
				n := 0
				for _, e := range ev.get() {
					if e == p || strings.HasPrefix(e, p+":") {
						n++
					}
				}
				return n
			}
			if got := len(w.cl.pushes); got != c.pushes {
				t.Errorf("pushes = %d, want %d", got, c.pushes)
			}
			if got := count("revoke"); got != c.revokes {
				t.Errorf("revokes = %d, want %d", got, c.revokes)
			}
			if got, want := [3]int{count("create"), count("issue"), count("commit")}, [3]int{c.creates, c.issues, c.commits}; got != want {
				t.Errorf("create/issue/commit = %v, want %v", got, want)
			}
			if rec, _ := w.ledger.InForce(); rec.Generation != 1 {
				t.Errorf("in force = %d: something was committed", rec.Generation)
			}
			if _, p := pendingGen(t, w.ledger); p != c.pending {
				t.Errorf("pending = %v, want %v", p, c.pending)
			}
			if c.pending {
				if os.Status.State != StatePartial || os.Status.InForce || !hasFence(os.Status) {
					t.Errorf("status = %+v, want partial with a fence failure", os.Status)
				}
			} else if os.Status.State != "" {
				t.Errorf("status = %+v, want none: nothing happened", os.Status)
			}
		})
	}
}

// A refusal of the revoke label stops every later revocation, however many senders are left.
func TestBeforeWriteRevokeRefusalStopsFurtherRevokes(t *testing.T) {
	w, ev := fenceWorld(t, FenceRevoke, 1)
	rec := &MemIssueRecord{}
	for _, a := range []string{"foo.c.core", "foo.c.docs"} {
		if err := rec.Add(context.Background(), IssueEntry{Address: a, PublicKey: userPub(t), Purpose: PurposeIssue, IssuedAt: fixedNow()}); err != nil {
			t.Fatal(err)
		}
	}
	plan := &RotationPlan{Leaving: []string{"foo.c.core", "foo.c.docs"}, Record: rec, Revoker: fenceRevoker{w, t, ev}, Generation: 2, Now: fixedNow}
	w.in.AfterPush = RotationsAfterPush(plan)
	if _, err := RunOrdered(context.Background(), w.in, w.prov); !errors.Is(err, errFenced) {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(ev.get(), "revoke") {
		t.Errorf("a revocation was pushed after the refusal: %v", ev.get())
	}
}

// convergeFence makes s2 refuse every push, so the first gate fails and Converge re-pushes and then rolls back.
func convergeFence(t *testing.T, refuse string, nth int) (*world14, *events, Status, error) {
	t.Helper()
	w, ev := fenceWorld(t, refuse, nth)
	w.in.AfterPush = nil
	w.in.OldKeys = map[string][]string{"foo.c.core": {w.k.revoked}, "foo.c.docs": {}}
	w.cl.failPush["s2"] = true
	st, err := Run(context.Background(), w.in)
	return w, ev, st, err
}

func TestBeforeWriteRollbackRefusal(t *testing.T) {
	w, ev, st, err := convergeFence(t, FenceRollback, 1)
	if !errors.Is(err, errFenced) {
		t.Fatalf("err = %v", err)
	}
	if got := labels(ev.get()); !slices.Equal(got, []string{FencePush, FencePush, FenceRollback}) {
		t.Errorf("labels = %v", got)
	}
	if got := len(w.cl.pushes); got != 2 { // the target and its re-push; no rollback push
		t.Errorf("pushes = %d, want 2", got)
	}
	if _, p := pendingGen(t, w.ledger); !p {
		t.Error("the generation is not pending")
	}
	if st.State != StatePartial || st.InForce || !hasFence(st) {
		t.Errorf("status = %+v", st)
	}
	if rec, _ := w.ledger.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d", rec.Generation)
	}
}

func TestBeforeWriteRepushRefusal(t *testing.T) {
	w, _, st, err := convergeFence(t, FencePush, 2)
	if !errors.Is(err, errFenced) {
		t.Fatalf("err = %v", err)
	}
	if got := len(w.cl.pushes); got != 1 {
		t.Errorf("pushes = %d, want 1: the re-push was refused", got)
	}
	if _, p := pendingGen(t, w.ledger); !p {
		t.Error("the generation is not pending")
	}
	if !hasFence(st) {
		t.Errorf("status = %+v", st)
	}
}

func TestApplyPassesBeforeWrite(t *testing.T) {
	w, l, deps := applyWorld(t)
	deps.BeforeWrite = func(context.Context, string) error { return errFenced }
	if _, err := Apply(context.Background(), applyReq(), deps); !errors.Is(err, errFenced) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != 0 {
		t.Errorf("pushes = %d", len(w.cluster.pushes))
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d", rec.Generation)
	}
}
