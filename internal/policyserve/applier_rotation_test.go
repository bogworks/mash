package policyserve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policyintake"
)

// memLedger is a policyapply.MemLedger that also takes a refusal.
type memLedger struct{ *policyapply.MemLedger }

func (memLedger) WriteRefusal(context.Context, policyapply.Status) error { return nil }

type okGuard struct{}

func (okGuard) Before(context.Context, string) error { return nil }

// sameContentApplier returns an Applier whose ledger holds generation 1 and the document that states generation 2 on
// base 1 with the very content in force.
func sameContentApplier(t *testing.T) (*Applier, policyintake.Doc) {
	t.Helper()
	ls := listset.ListSet{Range: "foo"}
	c, err := listset.Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	rec := listset.Record{Generation: 1, Signed: listset.DigestCompiled(c)}
	led := memLedger{policyapply.NewMemLedgerWith(rec, policyapply.InForceDetail{ListSet: &ls})}
	a := &Applier{
		Account: "A", Range: "foo", Ledger: led, Record: anyRecord{}, Servers: 1, URL: "nats://127.0.0.1:4222",
		Keys: memKeys{},
		readServers: func(context.Context) ([]ServerInfo, error) {
			return []ServerInfo{{Name: "s1", Host: "127.0.0.1", Port: 4222}}, nil
		},
	}
	raw := fmt.Sprintf(`{"schema":%q,"account":"A","range":"foo","generation":2,"base_generation":1,"issued_at":"2026-11-14T10:00:00Z","groups":{},"senders":{}}`, "mash.listset/1")
	return a, policyintake.Doc{Raw: []byte(raw)}
}

func TestApplierPassesTheRotationStepToApply(t *testing.T) {
	a, d := sameContentApplier(t)
	d.RotationStep = true
	var got *policyapply.ApplyRequest
	a.applyFn = func(_ context.Context, req policyapply.ApplyRequest, _ policyapply.ApplyDeps) (policyapply.Status, error) {
		got = &req
		return policyapply.Status{State: policyapply.StateApplied, Generation: req.Generation}, nil
	}
	if _, err := a.Apply(context.Background(), d, okGuard{}); err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.RotationStep {
		t.Fatalf("Apply was not called with RotationStep: %+v", got)
	}
}

func TestApplierLeavesAnUnchangedSubmissionToApply(t *testing.T) {
	a, d := sameContentApplier(t)
	var got *policyapply.ApplyRequest
	a.applyFn = func(ctx context.Context, req policyapply.ApplyRequest, deps policyapply.ApplyDeps) (policyapply.Status, error) {
		got = &req
		return policyapply.Apply(ctx, req, deps)
	}
	res, err := a.Apply(context.Background(), d, okGuard{})
	if err == nil {
		t.Fatal("an unchanged submission was applied")
	}
	if got == nil || got.RotationStep {
		t.Fatalf("Apply request: %+v", got)
	}
	var sb *listset.StaleBaseError
	if errors.As(err, &sb) || !strings.Contains(err.Error(), policyapply.StageUnchanged) || !res.StatusWritten {
		t.Fatalf("want an unchanged refusal from Apply, got %v (written %v)", err, res.StatusWritten)
	}
}

func TestApplierStaleBaseStillRefused(t *testing.T) {
	a, d := sameContentApplier(t)
	d.Raw = []byte(strings.NewReplacer(`"base_generation":1`, `"base_generation":0`, `"generation":2`, `"generation":1`).Replace(string(d.Raw)))
	a.applyFn = policyapply.Apply
	_, err := a.Apply(context.Background(), d, okGuard{})
	var sb *listset.StaleBaseError
	if !errors.As(err, &sb) {
		t.Fatalf("want a stale base, got %v", err)
	}
}
