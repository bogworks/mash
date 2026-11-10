package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// putNewFormRecord writes a new-form directory record straight into the directory KV.
func putNewFormRecord(t *testing.T, js jetstream.JetStream, user, agent, owner, name string) store.Record {
	t.Helper()
	ctx := context.Background()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	rec := store.Record{Name: name, Owner: owner, Agent: "claude", AgentToken: agent, Account: "A", User: user,
		MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339)}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ParseRecord(id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestProvisionRefusesOverlapWithNewFormRecord(t *testing.T) {
	e, a, js := setup(t) // owner "o"
	ctx := context.Background()
	_, user := e.Mint(t, "ops1")
	putNewFormRecord(t, js, user, "acme", e.Owner, "ops1")

	_, err := a.Provision(ctx, "ops1", user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), "overlap") || !strings.Contains(err.Error(), "acme_o_ops1") {
		t.Fatalf("want refusal naming the overlap rule and the existing key, got %v", err)
	}
	if _, err := js.Stream(ctx, sap.InboxStream("ops1")); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("a refused provision created a stream: %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "ops1"); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("a refused provision created a record: %v", err)
	}
}

func TestProvisionAdmitsNonOverlappingWithNewFormRecord(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "ops1")
	putNewFormRecord(t, js, user, "acme", "team1", "ops1") // owner differs from the admin's "o"
	_, u2 := e.Mint(t, "ops2")
	if _, err := a.Provision(ctx, "ops1", u2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("a legacy name under another owner than the new-form record is admissible: %v", err)
	}
}

func TestProvisionRefusesZonedWhileNewFormExists(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	putNewFormRecord(t, js, user, "acme", "team1", "ops1")
	_, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{Zone: "default"})
	if err == nil || !strings.Contains(err.Error(), "zoned legacy endpoint") {
		t.Fatalf("got %v", err)
	}
	if _, err := js.Stream(ctx, sap.InboxStream("alpha")); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("stream created: %v", err)
	}
}

func TestProvisionLegacyOnlyUnchanged(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, ua := e.Mint(t, "alpha")
	_, ub := e.Mint(t, "beta")
	_, uz := e.Mint(t, "zed")
	if _, err := a.Provision(ctx, "alpha", ua, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, "beta", ub, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, "zed", uz, e.Account, "", admin.Meta{Zone: "default"}); err != nil {
		t.Fatal(err)
	}
	// Already provisioned keeps its message.
	if _, err := a.Provision(ctx, "alpha", ua, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), "already provisioned") {
		t.Fatalf("got %v", err)
	}
	_ = js
}

// Delivery: an admissible pair has disjoint streams, and each message lands only in its own stream. The overlapping
// pair cannot both have streams: the server refuses the second, and Admit refuses it first.
func TestAdmissiblePairDeliversDisjointly(t *testing.T) {
	e, _, js := setup(t)
	ctx := context.Background()
	l := store.DefaultLimits()

	legacy, _ := sap.Legacy("legowner", "ops1")
	nf, _ := sap.New("acme", "team1", "ops1")
	existing := []store.Record{{Name: "ops1", Owner: "legowner", Agent: "claude-code", AgentToken: "cc", Account: "A", User: "U",
		MailSubject: store.MailSubjectFor("legowner", "", "ops1")}}
	if err := store.Admit(nf, "", existing); err != nil {
		t.Fatalf("the pair is admissible: %v", err)
	}

	if _, err := js.CreateStream(ctx, store.InboxConfigFor("legowner", "ops1", l)); err != nil {
		t.Fatal(err)
	}
	cfg := store.InboxConfigFor("legowner", "ops1", l)
	cfg.Name = "inbox-" + nf.Flat()
	cfg.Subjects = []string{nf.MailFilter()}
	if _, err := js.CreateStream(ctx, cfg); err != nil {
		t.Fatalf("a disjoint new-form stream must be creatable: %v", err)
	}

	for _, id := range []sap.EndpointIdentity{legacy, nf} {
		if _, err := js.Publish(ctx, id.Subjects().Mail, []byte(id.String())); err != nil {
			t.Fatalf("publish to %s: %v", id, err)
		}
	}
	for stream, want := range map[string]uint64{"inbox-ops1": 1, "inbox-" + nf.Flat(): 1} {
		s, err := js.Stream(ctx, stream)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := s.Info(ctx)
		if info.State.Msgs != want {
			t.Fatalf("%s holds %d messages, want %d", stream, info.State.Msgs, want)
		}
	}
	// Each holds its own message.
	for stream, body := range map[string]string{"inbox-ops1": "ops1", "inbox-" + nf.Flat(): "acme.team1.ops1"} {
		s, _ := js.Stream(ctx, stream)
		m, err := s.GetMsg(ctx, 1)
		if err != nil || string(m.Data) != body {
			t.Fatalf("%s first message %q (%v), want %q", stream, m.Data, err, body)
		}
	}
	_ = e
}

func TestOverlappingPairCannotBothHaveStreams(t *testing.T) {
	_, _, js := setup(t)
	ctx := context.Background()
	l := store.DefaultLimits()

	nf, _ := sap.New("acme", "team1", "ops1")
	// Legacy owner team1, name ops1: its wildcard filter agents.mail.*.team1.ops1 contains acme.team1.ops1.
	existing := []store.Record{{Name: "ops1", Owner: "team1", Agent: "claude-code", AgentToken: "cc", Account: "A", User: "U",
		MailSubject: store.MailSubjectFor("team1", "", "ops1")}}
	if _, err := js.CreateStream(ctx, store.InboxConfigFor("team1", "ops1", l)); err != nil {
		t.Fatal(err)
	}
	cfg := store.InboxConfigFor("team1", "ops1", l)
	cfg.Name = "inbox-" + nf.Flat()
	cfg.Subjects = []string{nf.MailFilter()}
	_, err := js.CreateStream(ctx, cfg)
	var apiErr *jetstream.APIError
	if err == nil || !errors.As(err, &apiErr) {
		t.Fatalf("the server must refuse overlapping stream subjects, got %v", err)
	}
	t.Logf("server refusal: %v", err)
	if err := store.Admit(nf, "", existing); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("Admit must refuse the overlapping pair first, got %v", err)
	}
}

func TestProvisionRefusesOwnerOfNewFormRecord(t *testing.T) {
	e, a, js := setup(t) // owner "o"
	ctx := context.Background()
	_, user := e.Mint(t, "other")
	putNewFormRecord(t, js, user, "acme", e.Owner, "ops1")
	_, err := a.Provision(ctx, "other", user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), "owner equals a new-form owner") || !strings.Contains(err.Error(), "acme_o_ops1") {
		t.Fatalf("want refusal naming the owner rule and the existing key, got %v", err)
	}
}

// putRawRecord writes value under key in the directory without parsing it.
func putRawRecord(t *testing.T, js jetstream.JetStream, key, value string) {
	t.Helper()
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Create(context.Background(), key, []byte(value)); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionSkipsMalformedLegacyKeyWithoutNewForm(t *testing.T) {
	e, a, js := setup(t)
	putRawRecord(t, js, "oddname", "not json")
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(context.Background(), "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("legacy-only deployment must be unchanged: %v", err)
	}
}

func TestProvisionRefusesMalformedLegacyKeyWhenNewFormPresent(t *testing.T) {
	e, a, js := setup(t)
	_, user := e.Mint(t, "alpha")
	putNewFormRecord(t, js, user, "acme", "team1", "ops1")
	putRawRecord(t, js, "oddname", "not json")
	_, err := a.Provision(context.Background(), "alpha", user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), "oddname") || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("want refusal naming the malformed key, got %v", err)
	}
}

func TestConcurrentProvisionOfDifferentNamesSucceeds(t *testing.T) {
	quickLocks(t, longWait)
	e, a, _ := setup(t)
	ctx := context.Background()
	names := []string{"alpha", "beta", "gamma", "delta"}
	errs := make(chan error, len(names))
	for _, n := range names {
		_, u := e.Mint(t, n)
		go func() {
			_, err := a.Provision(ctx, n, u, e.Account, "", admin.Meta{})
			errs <- err
		}()
	}
	for range names {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestProvisionWaitsOnTheAdmissionLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	until := time.Now().Add(time.Hour)
	putLock(t, js, admin.AdmissionLockName, until)
	_, user := e.Mint(t, "alpha")
	_, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), "another admin") || !strings.Contains(err.Error(), until.UTC().Format(time.RFC3339)) {
		t.Fatalf("want the lock-held refusal, got %v", err)
	}
	if _, err := js.Stream(ctx, sap.InboxStream("alpha")); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("a refused provision created a stream: %v", err)
	}
}

// The admission lock is held while Admit runs: a second provision of another name waits for it.
func TestAdmissionLockHeldDuringAdmit(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	var held atomic.Bool
	prev := admin.SetAfterAdmissionLock(func() {
		if held.CompareAndSwap(false, true) {
			kv, _ := js.KeyValue(ctx, store.LeaseBucket)
			if _, err := kv.Get(ctx, admin.LockKey(admin.AdmissionLockName)); err != nil {
				t.Errorf("admission lock not held at the hook: %v", err)
			}
		}
	})
	t.Cleanup(func() { admin.SetAfterAdmissionLock(prev) })
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if !held.Load() {
		t.Fatal("hook never ran")
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := kv.Get(ctx, admin.LockKey(admin.AdmissionLockName)); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("admission lock not released: %v", err)
	}
}
