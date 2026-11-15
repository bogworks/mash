package policyserve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
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
	ls := listset.ListSet{}
	c, err := listset.Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	rec := listset.Record{Generation: 1, Signed: listset.DigestCompiled(c)}
	led := memLedger{policyapply.NewMemLedgerWith(rec, policyapply.InForceDetail{ListSet: &ls})}
	a := &Applier{
		Account: "A", Ledger: led, Record: anyRecord{}, Servers: 1, URL: "nats://127.0.0.1:4222",
		Keys: memKeys{},
		readServers: func(context.Context) ([]ServerInfo, error) {
			return []ServerInfo{{Name: "s1", Host: "127.0.0.1", Port: 4222}}, nil
		},
	}
	raw := fmt.Sprintf(`{"schema":%q,"account":"A","generation":2,"base_generation":1,"issued_at":"2026-11-14T10:00:00Z","groups":{},"senders":{}}`, policydoc.DocumentSchema)
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

// newAddressApplier is an Applier with nothing in force and the document of generation 1 that lists one address.
func newAddressApplier(t *testing.T, inboxes InboxChecker) (*Applier, policyintake.Doc, *bool) {
	t.Helper()
	a, d := sameContentApplier(t)
	a.Ledger = memLedger{policyapply.NewMemLedger(listset.Record{})}
	a.Inboxes = inboxes
	a.Keys = makeKeys{memKeys{}}
	d.Raw = []byte(fmt.Sprintf(`{"schema":%q,"account":"A","generation":1,"base_generation":0,"issued_at":"2026-11-14T10:00:00Z",`+
		`"groups":{"c":{"mail":"closed","prompt":"closed","status":"shared","heartbeat":"closed"}},`+
		`"senders":{"c.core":{"allow":[],"deny":[]}}}`, policydoc.DocumentSchema))
	called := new(bool)
	a.applyFn = func(_ context.Context, req policyapply.ApplyRequest, _ policyapply.ApplyDeps) (policyapply.Status, error) {
		*called = true
		return policyapply.Status{State: policyapply.StateApplied, Generation: req.Generation}, nil
	}
	return a, d, called
}

// A change that adds an address with no inbox is refused before Apply runs, with the plain reason.
func TestApplierRefusesAnAddressWithNoInboxBeforeAnythingIsPushed(t *testing.T) {
	a, d, called := newAddressApplier(t, &fakeInboxes{})
	_, err := a.Apply(context.Background(), d, okGuard{})
	if err == nil || !strings.Contains(err.Error(), "c.core") || !strings.Contains(err.Error(), "no inbox yet") {
		t.Fatalf("want the no-inbox refusal, got %v", err)
	}
	if *called {
		t.Fatal("Apply ran for an address with no inbox")
	}
	a, d, called = newAddressApplier(t, nil)
	if _, err := a.Apply(context.Background(), d, okGuard{}); err == nil || *called {
		t.Fatalf("no inbox checker: the change was not refused (%v, called %v)", err, *called)
	}
}

// A change whose new address has an inbox is applied.
func TestApplierAppliesAnAddressWhoseInboxExists(t *testing.T) {
	a, d, called := newAddressApplier(t, &fakeInboxes{answers: map[string]admin.InboxCheck{"c.core": {Stream: "inbox-core", Consumer: true, Legacy: true}}})
	if _, err := a.Apply(context.Background(), d, okGuard{}); err != nil {
		t.Fatal(err)
	}
	if !*called {
		t.Fatal("Apply did not run")
	}
}

// makeKeys is memKeys that makes a key for any owner group.
type makeKeys struct{ memKeys }

func (makeKeys) GroupKey(string, bool) (nkeys.KeyPair, error) { return nkeys.CreateAccount() }

// The production Applier carries the drop fields to the Issuer it builds: on the first apply (nothing in force) a
// non-mash signing key is dropped once the exempt users stay under kept keys, where without the fields it is refused.
func TestApplierDropsANonMashKeyOnTheFirstApplyWhenTheDropFieldsAreSet(t *testing.T) {
	w := newDropWorld(t)
	raw := []byte(fmt.Sprintf(`{"schema":%q,"account":%q,"generation":1,"base_generation":0,"issued_at":"2026-11-14T10:00:00Z",`+
		`"groups":{"c":{"mail":"closed","prompt":"closed","status":"shared","heartbeat":"closed"}},`+
		`"senders":{"c.core":{"allow":[],"deny":[]}}}`, policydoc.DocumentSchema, w.acct))
	exempt := map[string][]string{"UEXEMPT": {w.acct, pubOf(w.keys.groups["c"])}}
	for _, set := range []bool{false, true} {
		a := &Applier{
			Account: w.acct, Ledger: memLedger{policyapply.NewMemLedger(listset.Record{})}, Record: anyRecord{}, Servers: 1,
			URL: "nats://127.0.0.1:4222", Keys: w.keys, Trust: w.iss.Trust, Now: w.iss.Now,
			Provisioner: nopProvisioner{},
			readServers: func(context.Context) ([]ServerInfo, error) {
				return []ServerInfo{{Name: "s1", Host: "127.0.0.1", Port: 4222}}, nil
			},
		}
		if set {
			a.AdminUser, a.AdminIssuer = "UADMIN", w.acct
			a.ExemptIssuers = func(context.Context) (map[string][]string, error) { return exempt, nil }
		}
		var signed string
		var signErr error
		a.applyFn = func(_ context.Context, req policyapply.ApplyRequest, deps policyapply.ApplyDeps) (policyapply.Status, error) {
			iss, ok := deps.Signer.(*Issuer)
			if !ok {
				t.Fatalf("signer is %T", deps.Signer)
			}
			iss.Resolver = oneJWT{w.iss.Resolver.(oneJWT).tok} // the account JWT with the non-mash key; no server to read
			c, err := listset.Compile(req.ListSet)
			if err != nil {
				t.Fatal(err)
			}
			signed, signErr = iss.Sign(listset.Record{Generation: req.Generation}, c)
			return policyapply.Status{State: policyapply.StateApplied, Generation: req.Generation}, nil
		}
		if _, err := a.Apply(context.Background(), policyintake.Doc{Raw: raw}, okGuard{}); err != nil {
			t.Fatal(err)
		}
		if !set {
			if signErr == nil || !strings.Contains(signErr.Error(), "not an owner group key of the key store") {
				t.Fatalf("without the fields: %v", signErr)
			}
			continue
		}
		if signErr != nil {
			t.Fatal(signErr)
		}
		got, err := jwt.DecodeAccountClaims(signed)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.SigningKeys.GetScope(w.plain); ok {
			t.Error("the non-mash key stays")
		}
		scopeOf(t, got, pubOf(w.keys.groups["c"]))
	}
}

type nopProvisioner struct{}

func (nopProvisioner) CreateInbox(context.Context, string) error     { return nil }
func (nopProvisioner) IssueCredential(context.Context, string) error { return nil }
