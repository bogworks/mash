package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// dirKeys and streamNames snapshot what a refused provision must leave unchanged.
func dirKeys(t *testing.T, js jetstream.JetStream) []string {
	t.Helper()
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := kv.Keys(context.Background())
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		t.Fatal(err)
	}
	slices.Sort(keys)
	return keys
}

func streamNames(t *testing.T, js jetstream.JetStream) []string {
	t.Helper()
	var out []string
	l := js.StreamNames(context.Background())
	for n := range l.Name() {
		if strings.HasPrefix(n, "inbox-") {
			out = append(out, n)
		}
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func untouched(t *testing.T, js jetstream.JetStream, keys, streams []string) {
	t.Helper()
	if k, s := dirKeys(t, js), streamNames(t, js); !slices.Equal(keys, k) || !slices.Equal(streams, s) {
		t.Fatalf("a refused provision wrote: keys %v -> %v, streams %v -> %v", keys, k, streams, s)
	}
}

func provLegacy(t *testing.T, e *testenv.Env, a *admin.Admin, name string, meta admin.Meta) {
	t.Helper()
	_, u := e.Mint(t, name)
	if _, err := a.Provision(context.Background(), name, u, e.Account, "", meta); err != nil {
		t.Fatal(err)
	}
}

// putNewFormRecordAlias writes a new-form record carrying alias straight into the directory.
func putNewFormRecordAlias(t *testing.T, js jetstream.JetStream, user, agent, owner, name string, alias *store.Alias) {
	t.Helper()
	id := newID(t, agent, owner, name)
	rec := store.Record{Name: name, Owner: owner, Agent: sap.AgentName, AgentToken: agent, Account: "A", User: user,
		MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339), Alias: alias}
	val, _ := json.Marshal(rec)
	if _, err := store.ParseRecord(id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
	if _, err := kv.Create(context.Background(), id.Flat(), val); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionIdentityCreatesRecordStreamConsumer(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	rec, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "note", admin.Meta{Role: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "ops1" || rec.Owner != "team1" || rec.AgentToken != "acme" || rec.Agent != sap.AgentName ||
		rec.MailSubject != "agents.mail.acme.team1.ops1" || rec.User != user || rec.Account != e.Account ||
		rec.Zone != "" || rec.Alias != nil || rec.Note != "note" || rec.Role != "r" || rec.Created == "" {
		t.Fatalf("record %+v", rec)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	got, err := store.GetRecord(ctx, kv, "acme_team1_ops1")
	if err != nil || got.Key() != "acme_team1_ops1" {
		t.Fatalf("directory record: %+v %v", got, err)
	}
	if _, err := kv.Get(ctx, "ops1"); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("a record keyed by the bare name exists: %v", err)
	}
	s, err := js.Stream(ctx, "inbox-acme_team1_ops1")
	if err != nil {
		t.Fatal(err)
	}
	c := s.CachedInfo().Config
	if !slices.Equal(c.Subjects, []string{"agents.mail.acme.team1.ops1"}) || c.MaxBytes != store.DefaultLimits().InboxMaxBytes ||
		!c.DenyDelete || !c.DenyPurge || c.SubjectTransform != nil {
		t.Fatalf("inbox config %+v", c)
	}
	if _, err := s.Consumer(ctx, "deliver"); err != nil {
		t.Fatalf("consumer: %v", err)
	}
	if _, err := js.Stream(ctx, "inbox-ops1"); !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("a legacy-named stream exists: %v", err)
	}
}

// A provision that stopped after the stream (the record is written last) is finished by running the same call again,
// and a finished one is refused, as Provision does.
func TestProvisionIdentityIsRecoverableAndRefusesRepeat(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	s, err := js.CreateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("re-run over a half-done provision: %v", err)
	}
	if got := dirKeys(t, js); !slices.Equal(got, []string{"acme_team1_ops1"}) {
		t.Fatalf("keys %v", got)
	}
	if _, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), "already provisioned") {
		t.Fatalf("repeat of a finished provision: %v", err)
	}
}

func TestProvisionIdentityRefusesBadInputs(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "ops1")
	good := newID(t, "acme", "team1", "ops1")
	for name, c := range map[string]struct {
		id         sap.EndpointIdentity
		user, acct string
		meta       admin.Meta
	}{
		"zero identity": {sap.EndpointIdentity{}, user, e.Account, admin.Meta{}},
		"legacy":        {legID(t, "o", "ops1"), user, e.Account, admin.Meta{}},
		"bad user":      {good, "nope", e.Account, admin.Meta{}},
		"bad account":   {good, user, "nope", admin.Meta{}},
		"bad meta":      {good, user, e.Account, admin.Meta{Role: "a\tb"}},
		"zone":          {good, user, e.Account, admin.Meta{Zone: "z1"}},
	} {
		if _, err := a.ProvisionIdentity(ctx, c.id, nil, c.user, c.acct, "", c.meta); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	untouched(t, js, nil, nil)
}

func TestProvisionIdentityAliasVerification(t *testing.T) {
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ops1")
	ghost := store.Alias{Owner: "o", Name: "ghost"}
	for name, c := range map[string]struct {
		pre   func(t *testing.T, e *testenv.Env, a *admin.Admin)
		id    sap.EndpointIdentity
		alias store.Alias
		want  string
	}{
		"no legacy record": {nil, id, store.Alias{Owner: "o", Name: "ghost"}, "no legacy endpoint"},
		"legacy record is zoned": {func(t *testing.T, e *testenv.Env, a *admin.Admin) {
			provLegacy(t, e, a, "ghost", admin.Meta{Zone: "default"})
		}, id, ghost, "zoned"},
		"claimed by another new-form record": {func(t *testing.T, e *testenv.Env, a *admin.Admin) {
			provLegacy(t, e, a, "ghost", admin.Meta{})
			_, u := e.Mint(t, "x")
			if _, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team2", "other"), &ghost, u, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
		}, id, ghost, "already claimed"},
		"alias owner equals the record's owner": {func(t *testing.T, e *testenv.Env, a *admin.Admin) {
			provLegacy(t, e, a, "ghost", admin.Meta{})
		}, newID(t, "acme", "o", "ops1"), ghost, "alias owner"},
		"alias name fails the legacy rule":  {nil, id, store.Alias{Owner: "o", Name: "Bad_Name"}, "legacy name"},
		"alias owner fails the legacy rule": {nil, id, store.Alias{Owner: "9x", Name: "ghost"}, "legacy owner"},
	} {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			if c.pre != nil {
				c.pre(t, e, a)
			}
			keys, streams := dirKeys(t, js), streamNames(t, js)
			_, user := e.Mint(t, "ops1")
			_, err := a.ProvisionIdentity(ctx, c.id, &c.alias, user, e.Account, "", admin.Meta{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal containing %q, got %v", c.want, err)
			}
			untouched(t, js, keys, streams)
		})
	}
}

func TestProvisionIdentityWithAliasRecordsIt(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	rec, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Alias == nil || *rec.Alias != (store.Alias{Owner: "o", Name: "ghost"}) {
		t.Fatalf("alias %+v", rec.Alias)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	got, err := store.GetRecord(ctx, kv, "acme_team1_ops1")
	if err != nil || got.Alias == nil || got.Alias.Name != "ghost" {
		t.Fatalf("stored %+v %v", got, err)
	}
	if _, err := store.GetRecord(ctx, kv, "ghost"); err != nil {
		t.Fatalf("the legacy endpoint changed: %v", err)
	}
}

func TestProvisionIdentityAdmission(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		id   sap.EndpointIdentity
		pre  func(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, user string)
		want string
	}{
		"owner equals a legacy owner present": {newID(t, "acme", "o", "ops1"), func(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, user string) {
			provLegacy(t, e, a, "peer", admin.Meta{}) // owner "o"
		}, "owner equals a legacy owner"},
		"overlaps an existing new-form record's alias": {newID(t, "acme", "legowner", "ghost"), func(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, user string) {
			putNewFormRecordAlias(t, js, user, "acme", "team1", "ops1", &store.Alias{Owner: "legowner", Name: "ghost"})
		}, "alias"},
		"unreadable record": {newID(t, "acme", "team1", "ops1"), func(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, user string) {
			putRawRecord(t, js, "oddname", "not json")
		}, "oddname"},
		"zoned legacy present": {newID(t, "acme", "team1", "ops1"), func(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, user string) {
			provLegacy(t, e, a, "zed", admin.Meta{Zone: "default"})
		}, "zoned legacy"},
	} {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			_, user := e.Mint(t, "ops1")
			c.pre(t, e, a, js, user)
			keys, streams := dirKeys(t, js), streamNames(t, js)
			_, err := a.ProvisionIdentity(ctx, c.id, nil, user, e.Account, "", admin.Meta{})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal containing %q, got %v", c.want, err)
			}
			untouched(t, js, keys, streams)
		})
	}
}

// Two provisions of different names that claim the same legacy alias must not both succeed, even when each reads the
// directory before the other writes: the admission lock makes list-admit-create atomic.
func TestProvisionIdentityAdmissionLockSerialisesOverlap(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	prev := admin.SetAfterDirectoryGet(func(string) { time.Sleep(400 * time.Millisecond) })
	t.Cleanup(func() { admin.SetAfterDirectoryGet(prev) })

	ids := []sap.EndpointIdentity{newID(t, "acme", "team1", "ops1"), newID(t, "acme", "team2", "ops2")}
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		_, u := e.Mint(t, id.Name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, u, e.Account, "", admin.Meta{})
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly one winner, got %d (errors %v)", wins, errs)
	}
	n := 0
	for _, k := range dirKeys(t, js) {
		if strings.Contains(k, "_") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one new-form record, keys %v", dirKeys(t, js))
	}
}

// The name lock is taken first and the admission lock second: both are held at the hook, in that order of need.
func TestProvisionIdentityHoldsBothLocksAndReleasesThem(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	var seen []string
	prev := admin.SetAfterAdmissionLock(func() {
		kv, _ := js.KeyValue(ctx, store.LeaseBucket)
		for _, n := range []string{id.Flat(), admin.AdmissionLockName} {
			if _, err := kv.Get(ctx, admin.LockKey(n)); err == nil {
				seen = append(seen, n)
			}
		}
	})
	t.Cleanup(func() { admin.SetAfterAdmissionLock(prev) })
	if _, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{id.Flat(), admin.AdmissionLockName}) {
		t.Fatalf("locks held at the admission hook: %v", seen)
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	for _, n := range []string{id.Flat(), admin.AdmissionLockName} {
		if _, err := kv.Get(ctx, admin.LockKey(n)); !errors.Is(err, jetstream.ErrKeyNotFound) {
			t.Fatalf("lock %s not released: %v", n, err)
		}
	}
}

// Behavioural lock order: with both locks held elsewhere the name lock is the one waited on; with only the admission
// lock held it is waited on second, while this call already holds the name lock.
func TestProvisionIdentityWaitsNameLockThenAdmissionLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	until := time.Now().Add(time.Hour)

	putLock(t, js, id.Flat(), until)
	putLock(t, js, admin.AdmissionLockName, until)
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), id.Flat()) || strings.Contains(err.Error(), admin.AdmissionLockName) {
		t.Fatalf("with both held the name lock is waited on first, got %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if err := kv.Purge(ctx, admin.LockKey(id.Flat())); err != nil {
		t.Fatal(err)
	}
	_, err = a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), admin.AdmissionLockName) {
		t.Fatalf("with only the admission lock held it is waited on second, got %v", err)
	}
	if _, err := kv.Get(ctx, admin.LockKey(id.Flat())); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("the name lock was left behind: %v", err)
	}
	untouched(t, js, nil, nil)
}

// provNewForm provisions the new-form identity agent.owner.name with a fresh user key.
func provNewForm(t *testing.T, e *testenv.Env, a *admin.Admin, agent, owner, name string) (sap.EndpointIdentity, string) {
	t.Helper()
	id := newID(t, agent, owner, name)
	_, user := e.Mint(t, name)
	if _, err := a.ProvisionIdentity(context.Background(), id, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return id, user
}

func wantRetired(t *testing.T, err error, id sap.EndpointIdentity) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "INV-002") || !strings.Contains(err.Error(), id.String()) {
		t.Fatalf("want an INV-002 refusal naming %s, got %v", id, err)
	}
}

// (a) A deprovisioned new-form identity is refused at once, writes a marker, and fences only that identity (f).
func TestProvisionIdentityRetiredNameIsRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	rk, err := js.KeyValue(ctx, store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := rk.Get(ctx, id.Flat())
	if err != nil {
		t.Fatalf("no marker: %v", err)
	}
	var m store.Retired
	if err := json.Unmarshal(ent.Value(), &m); err != nil || m.Agent != "acme" || m.Owner != "team1" || m.Name != "ops1" ||
		m.User != user || m.RetiredAt == "" || strings.Contains(string(ent.Value()), `"SU`) {
		t.Fatalf("marker %s %v", ent.Value(), err)
	}
	_, err = a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
	// (f) another name under the same owner, and the same name under another owner, are not blocked.
	if _, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team1", "ops2"), nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ProvisionIdentity(ctx, newID(t, "acme", "team2", "ops1"), nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

// DeprovisionIfUser burns the marker too.
func TestProvisionIdentityRetiredByDeprovisionIfUser(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.DeprovisionIfUser(ctx, id.Flat(), user); err != nil {
		t.Fatal(err)
	}
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// (b) The refusal outlives the lease tombstone. The lease bucket's TTL is shortened in place (a bucket update; the
// production constant is untouched) and waited out until the lease key is gone.
func TestProvisionIdentityRetiredSurvivesLeaseExpiry(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	cfg := store.LeaseConfig()
	cfg.TTL = time.Second
	if _, err := js.UpdateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	lease, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := lease.Get(ctx, id.Flat()); err != nil {
		t.Fatalf("no tombstone to expire: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := lease.Get(ctx, id.Flat()); errors.Is(err, jetstream.ErrKeyNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the lease tombstone did not expire")
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
}

// (c) The check sits under the admission lock: with that lock held elsewhere the call waits on it and refuses on the
// lock, and a refused retired name writes nothing.
func TestProvisionIdentityRetiredCheckedUnderAdmissionLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	putLock(t, js, admin.AdmissionLockName, time.Now().Add(time.Hour))
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), admin.AdmissionLockName) || strings.Contains(err.Error(), "INV-002") {
		t.Fatalf("with the admission lock held the call waits on it before reading the marker, got %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if err := kv.Purge(ctx, admin.LockKey(admin.AdmissionLockName)); err != nil {
		t.Fatal(err)
	}
	var held bool
	prev := admin.SetAfterAdmissionLock(func() {
		_, err := kv.Get(ctx, admin.LockKey(admin.AdmissionLockName))
		held = err == nil
	})
	t.Cleanup(func() { admin.SetAfterAdmissionLock(prev) })
	_, err = a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if !held {
		t.Fatal("the admission lock was not held when the marker was checked")
	}
	untouched(t, js, nil, nil)
	for _, n := range []string{id.Flat(), admin.AdmissionLockName} {
		if _, err := kv.Get(ctx, admin.LockKey(n)); !errors.Is(err, jetstream.ErrKeyNotFound) {
			t.Fatalf("lock %s left behind: %v", n, err)
		}
	}
}

// (d) A legacy name stays reusable: no marker is written, and Provision of it again works.
func TestLegacyDeprovisionLeavesNameReusable(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "alpha", admin.Meta{})
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if rk, err := js.KeyValue(ctx, store.RetiredBucket); err == nil {
		if keys, err := rk.Keys(ctx); err == nil && len(keys) != 0 {
			t.Fatalf("a legacy deprovision wrote retired markers %v", keys)
		}
	}
	provLegacy(t, e, a, "alpha", admin.Meta{})
}

// (e) A crash after the marker and before the record goes: the name is already burned, and running deprovision again
// finishes the removal.
func TestDeprovisionRetiredMarkerCrashIsRecoverable(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	// State left by a crash between the marker and the record removal: marker, record and stream all present.
	rk, _ := js.KeyValue(ctx, store.RetiredBucket)
	val, _ := json.Marshal(store.Retired{Agent: "acme", Owner: "team1", Name: "ops1", User: user, RetiredAt: "2026-11-10T00:00:00Z"})
	if _, err := rk.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	_, err := a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	if got, err := rk.Get(ctx, id.Flat()); err != nil || string(got.Value()) != string(val) {
		t.Fatalf("the first marker must be kept: %v", err)
	}
	if err := a.Deprovision(ctx, id.Flat()); err != nil { // idempotent
		t.Fatal(err)
	}
	_, err = a.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// (e) Through the failure seam: the connection dies right after the record is purged, before the marker (a guarded
// removal writes it after the purge); the same removal run from a fresh connection writes it and finishes.
func TestDeprovisionIfUserCrashAfterPurgeIsRecoverable(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionIfUser(ctx, id.Flat(), user); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = kv.Purge(ctx, admin.LockKey(id.Flat())) // the dead admin's lock would otherwise wait out its TTL
	if err := b.DeprovisionIfUser(ctx, id.Flat(), user); err != nil {
		t.Fatal(err)
	}
	_, err := b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := b.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	_, err = b.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// A key with no record burns nothing: a mistyped name is not retired for good.
func TestDeprovisionWithoutRecordWritesNoMarker(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	id := newID(t, "acme", "team1", "ghost")
	if err := a.Deprovision(ctx, id.Flat()); err != nil {
		t.Fatal(err)
	}
	rk, _ := js.KeyValue(ctx, store.RetiredBucket)
	if _, err := rk.Get(ctx, id.Flat()); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("a marker for a name with no record: %v", err)
	}
}

// (g) An endpoint user, with the permissions provisioning gives a new-form identity, has no access to the retired
// bucket: a request on each of its subjects gets no reply while the same kind of request on the directory does.
func TestNewFormEndpointUserCannotReachRetiredBucket(t *testing.T) {
	e, a, _ := setup(t)
	id, _ := provNewForm(t, e, a, "acme", "team1", "ops1")
	perms, err := admin.NewFormUserPermissions(id, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range append(append([]string{}, perms.Pub.Allow...), perms.Sub.Allow...) {
		if strings.Contains(s, store.RetiredBucket) {
			t.Fatalf("grant %q reaches the retired bucket", s)
		}
	}
	creds, _ := e.MintRaw(t, "ops1", perms)
	nc := e.ConnAs(t, creds, id.Flat())
	bucket := "KV_" + store.RetiredBucket
	ask := func(subj string, data []byte) error {
		_, err := nc.Request(subj, data, 700*time.Millisecond)
		return err
	}
	if err := ask("$JS.API.DIRECT.GET.KV_"+store.DirectoryBucket+".$KV."+store.DirectoryBucket+"."+id.Flat(), nil); err != nil {
		t.Fatalf("control: the directory read should be answered: %v", err)
	}
	for name, f := range map[string]func() error{
		"stream info": func() error { return ask("$JS.API.STREAM.INFO."+bucket, nil) },
		"direct get":  func() error { return ask("$JS.API.DIRECT.GET."+bucket+".$KV."+store.RetiredBucket+"."+id.Flat(), nil) },
		"write":       func() error { return ask("$KV."+store.RetiredBucket+"."+id.Flat(), []byte("{}")) },
		"delete":      func() error { return ask("$JS.API.STREAM.PURGE."+bucket, nil) },
	} {
		if err := f(); err == nil {
			t.Fatalf("%s: the endpoint user got an answer from the retired bucket", name)
		}
	}
	// The write went nowhere: the bucket holds no marker.
	js, _ := jetstream.New(a.Conn())
	rk, err := js.KeyValue(context.Background(), store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	if keys, err := rk.Keys(context.Background()); err == nil {
		t.Fatalf("keys in the retired bucket: %v", keys)
	}
}

func TestVerifyAlias(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	legRec := func(owner, name, zone string) store.Record {
		return store.Record{Name: name, Owner: owner, Agent: sap.AgentName, AgentToken: "cc", Account: "A", User: "U",
			MailSubject: store.MailSubjectFor(owner, zone, name), Zone: zone}
	}
	newRec := func(owner, name string, a *store.Alias) store.Record {
		nid := newID(t, "acme", owner, name)
		return store.Record{Name: name, Owner: owner, Agent: sap.AgentName, AgentToken: "acme", Account: "A", User: "U",
			MailSubject: nid.Subjects().Mail, Alias: a}
	}
	al := store.Alias{Owner: "legowner", Name: "ghost"}
	leg := legRec("legowner", "ghost", "")
	for name, c := range map[string]struct {
		id    sap.EndpointIdentity
		alias store.Alias
		recs  []store.Record
		want  string // empty: accepted
	}{
		"accepted":                     {id, al, []store.Record{leg}, ""},
		"accepted with unrelated new":  {id, al, []store.Record{leg, newRec("team2", "other", nil)}, ""},
		"accepted, own record present": {id, al, []store.Record{leg, newRec("team1", "ops1", &al)}, ""},
		"no records":                   {id, al, nil, "no legacy endpoint"},
		"legacy with another owner":    {id, al, []store.Record{legRec("elsewhere", "ghost", "")}, "no legacy endpoint"},
		"legacy with another name":     {id, al, []store.Record{legRec("legowner", "other", "")}, "no legacy endpoint"},
		"zoned legacy":                 {id, al, []store.Record{legRec("legowner", "ghost", "z1")}, "zoned"},
		"claimed by another":           {id, al, []store.Record{leg, newRec("team2", "other", &al)}, "already claimed"},
		"owner equal":                  {newID(t, "acme", "legowner", "ops1"), al, []store.Record{leg}, "alias owner"},
		"bad legacy name":              {id, store.Alias{Owner: "legowner", Name: "Bad"}, []store.Record{leg}, "legacy name"},
		"bad legacy owner":             {id, store.Alias{Owner: "", Name: "ghost"}, []store.Record{leg}, "legacy owner"},
		"legacy id":                    {legID(t, "o", "x"), al, []store.Record{leg}, "new-form"},
		"zero id":                      {sap.EndpointIdentity{}, al, []store.Record{leg}, "identity"},
		"names a new-form record":      {id, store.Alias{Owner: "team2", Name: "other"}, []store.Record{leg, newRec("team2", "other", nil)}, "no legacy endpoint"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := admin.VerifyAlias(c.id, c.alias, c.recs)
			if c.want == "" {
				if err != nil || got.Owner != "legowner" || got.Name != "ghost" || !got.IsLegacy() {
					t.Fatalf("%+v %v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func TestNewFormUserPermissions(t *testing.T) {
	id := newID(t, "acme", "team1", "ops1")
	extra := []string{"agents.mail.acme.team2.*"}
	base, err := admin.NewFormPermissions(id, extra)
	if err != nil {
		t.Fatal(err)
	}
	legRec := func(owner, name, zone string) store.Record {
		return store.Record{Name: name, Owner: owner, Agent: sap.AgentName, AgentToken: "cc", Account: "A", User: "U",
			MailSubject: store.MailSubjectFor(owner, zone, name), Zone: zone}
	}
	existing := []store.Record{legRec("legowner", "ghost", "")}
	got, err := admin.NewFormUserPermissions(id, nil, nil, extra)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Pub.Allow, base.Pub.Allow) || !slices.Equal(got.Sub.Allow, base.Sub.Allow) {
		t.Fatalf("without an alias the result is NewFormPermissions: %v vs %v", got, base)
	}

	got, err = admin.NewFormUserPermissions(id, &store.Alias{Owner: "legowner", Name: "ghost"}, existing, extra)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := admin.WindowBridge(id, legID(t, "legowner", "ghost"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"base publish", got.Pub.Allow, base.Pub.Allow}, {"base subscribe", got.Sub.Allow, base.Sub.Allow},
		{"bridge publish", got.Pub.Allow, bridge.Pub.Allow}, {"bridge subscribe", got.Sub.Allow, bridge.Sub.Allow},
	} {
		for _, s := range c.want {
			if !slices.Contains(c.got, s) {
				t.Errorf("missing %s %s", c.what, s)
			}
		}
	}
	for _, l := range [][]string{got.Pub.Allow, got.Sub.Allow} {
		seen := map[string]bool{}
		for _, s := range l {
			if seen[s] {
				t.Errorf("duplicate %s", s)
			}
			seen[s] = true
			if err := admin.ValidateGrant(s); err != nil {
				t.Errorf("grant %s: %v", s, err)
			}
			if tk := strings.Split(s, "."); tk[0] == "agents" && (tk[2] == "*" || tk[2] == ">") {
				t.Errorf("wildcard in the agent position: %s", s)
			}
		}
	}
	if len(got.Pub.Deny)+len(got.Sub.Deny) != 0 {
		t.Fatal("unexpected deny lists")
	}

	if _, err := admin.NewFormUserPermissions(id, &store.Alias{Owner: "team1", Name: "ghost"}, existing, nil); err == nil {
		t.Error("alias owner equal to the owner accepted")
	}
	if _, err := admin.NewFormUserPermissions(id, &store.Alias{Owner: "legowner", Name: "Bad"}, existing, nil); err == nil {
		t.Error("bad alias accepted")
	}
	if _, err := admin.NewFormUserPermissions(legID(t, "o", "x"), nil, nil, nil); err == nil {
		t.Error("legacy identity accepted")
	}
	if _, err := admin.NewFormUserPermissions(id, nil, nil, []string{"agents.mail.*.team2.x"}); err == nil {
		t.Error("wildcard agent grant accepted")
	}

	// The alias is verified here, so it cannot be skipped: an unverified alias is refused before WindowBridge.
	al := store.Alias{Owner: "legowner", Name: "ghost"}
	claimed := store.Record{Name: "other", Owner: "team2", Agent: sap.AgentName, AgentToken: "acme", Account: "A", User: "U",
		MailSubject: newID(t, "acme", "team2", "other").Subjects().Mail, Alias: &al}
	for name, c := range map[string]struct {
		recs []store.Record
		want string
	}{
		"names no existing legacy record": {nil, "no legacy endpoint"},
		"zoned legacy record":             {[]store.Record{legRec("legowner", "ghost", "z1")}, "zoned"},
		"claimed by another new-form":     {[]store.Record{legRec("legowner", "ghost", ""), claimed}, "already claimed"},
	} {
		_, err := admin.NewFormUserPermissions(id, &al, c.recs, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want a refusal containing %q, got %v", name, c.want, err)
		}
	}
}

// The alias's legacy name lock is held by another actor: ProvisionIdentity waits for it (the lock's wait bound), then
// refuses naming the alias, and writes nothing and leaves no lock of its own behind.
func TestProvisionIdentityWaitsAliasNameLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	keys, streams := dirKeys(t, js), streamNames(t, js)
	putLock(t, js, "ghost", time.Now().Add(time.Hour))
	_, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want a refusal on the alias name's lock, got %v", err)
	}
	untouched(t, js, keys, streams)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	for _, n := range []string{id.Flat(), admin.AdmissionLockName} {
		if _, err := kv.Get(ctx, admin.LockKey(n)); !errors.Is(err, jetstream.ErrKeyNotFound) {
			t.Fatalf("lock %s left behind: %v", n, err)
		}
	}
}

// Two new-form identities claim the same legacy alias at once. Exactly one wins, the other is refused as already
// claimed, and one directory record holds the alias.
func TestProvisionIdentityConcurrentSameAliasExactlyOneWins(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	lim := store.DefaultLimits()
	lim.InboxMaxBytes = 2 << 20 // 24 inboxes are made; the default cap would exhaust the test server's storage
	if err := a.SetLimits(lim); err != nil {
		t.Fatal(err)
	}
	const rounds = 8
	for r := 0; r < rounds; r++ {
		legacy := fmt.Sprintf("ghost%d", r)
		provLegacy(t, e, a, legacy, admin.Meta{})
		alias := store.Alias{Owner: "o", Name: legacy}
		ids := []sap.EndpointIdentity{
			newID(t, "acme", "team1", fmt.Sprintf("opsa%d", r)),
			newID(t, "acme", "team1", fmt.Sprintf("opsb%d", r)),
		}
		users := make([]string, len(ids))
		for i := range ids {
			_, users[i] = e.Mint(t, fmt.Sprintf("mint%d-%d", r, i))
		}
		errs := make([]error, len(ids))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = a.ProvisionIdentity(ctx, ids[i], &alias, users[i], e.Account, "", admin.Meta{})
			}()
		}
		close(start)
		wg.Wait()

		var won, refused int
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case strings.Contains(err.Error(), "already claimed"):
				refused++
			default:
				t.Fatalf("round %d: unexpected error %v", r, err)
			}
		}
		if won != 1 || refused != 1 {
			t.Fatalf("round %d: want exactly one winner and one refusal, got errors %v", r, errs)
		}
		kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
		claims := 0
		for _, k := range dirKeys(t, js) {
			rec, err := store.GetRecord(ctx, kv, k)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Alias != nil && *rec.Alias == alias {
				claims++
			}
		}
		if claims != 1 {
			t.Fatalf("round %d: %d directory records claim %s, want 1", r, claims, legacy)
		}
	}
}

// A legacy Deprovision of the alias target racing ProvisionIdentity: the hook fires Deprovision after the snapshot
// has read the alias's record. The outcome is either (record created and the legacy record still present) or
// (provision refused and no record), never an alias to nothing.
func TestProvisionIdentityAliasTargetDeprovisionRace(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ghost", admin.Meta{})
	provLegacy(t, e, a, "zzz", admin.Meta{}) // sorts after ghost, so ghost is already read when zzz is reached
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")

	var once sync.Once
	depDone := make(chan error, 1)
	prev := admin.SetAfterDirectoryGet(func(k string) {
		if k != "zzz" {
			return
		}
		once.Do(func() {
			go func() { depDone <- a.Deprovision(ctx, "ghost") }()
			select { // the unfixed code lets it finish here; a lock on ghost makes it wait
			case err := <-depDone:
				depDone <- err
			case <-time.After(700 * time.Millisecond):
			}
		})
	})
	t.Cleanup(func() { admin.SetAfterDirectoryGet(prev) })

	_, perr := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: "o", Name: "ghost"}, user, e.Account, "", admin.Meta{})
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	_, legErr := kv.Get(ctx, "ghost")
	_, newErr := kv.Get(ctx, id.Flat())
	switch {
	case perr == nil && newErr == nil && legErr == nil: // created, legacy still present when it returned
	case perr != nil && errors.Is(newErr, jetstream.ErrKeyNotFound): // refused, no record
	default:
		t.Fatalf("dangling alias: provision err %v, new record err %v, legacy record err %v", perr, newErr, legErr)
	}
	select {
	case err := <-depDone:
		// Provision won: the alias now guards the legacy record, so the removal is refused (the legacy record is
		// still present, checked above). Provision lost: the removal succeeded.
		if err != nil && !errors.Is(err, admin.ErrAliased) {
			t.Fatalf("deprovision: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("deprovision never finished")
	}
}
