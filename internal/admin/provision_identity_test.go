package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
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
func TestProvisionIdentityCreatesRecordStreamConsumer(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, id)
	rec, err := a.Provision(ctx, id, user, e.Account, "note", admin.Meta{Role: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Owner != "acme" || rec.Group != "team1" || rec.Instance != "ops1" || rec.Agent != sap.AgentName ||
		rec.MailSubject != "agents.mail.acme.team1.ops1" || rec.User != user || rec.Account != e.Account ||
		rec.Note != "note" || rec.Role != "r" || rec.Created == "" {
		t.Fatalf("record %+v", rec)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	got, err := store.GetRecord(ctx, kv, "acme_team1_ops1")
	if err != nil {
		t.Fatalf("directory record: %+v %v", got, err)
	}
	if got2, aerr := got.Address(); aerr != nil || got2 != id {
		t.Fatalf("directory record address: %+v %v", got, aerr)
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
		t.Fatalf("a bare-named stream exists: %v", err)
	}
}

// A provision that stopped after the stream (the record is written last) is finished by running the same call again,
// and a finished one is refused.
func TestProvisionIdentityIsRecoverableAndRefusesRepeat(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, id)
	s, err := js.CreateStream(ctx, store.InboxConfig(id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("re-run over a half-done provision: %v", err)
	}
	if got := dirKeys(t, js); !slices.Equal(got, []string{"acme_team1_ops1"}) {
		t.Fatalf("keys %v", got)
	}
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), "already provisioned") {
		t.Fatalf("repeat of a finished provision: %v", err)
	}
}

func TestProvisionIdentityRefusesBadInputs(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	good := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, good)
	for name, c := range map[string]struct {
		id         sap.Address
		user, acct string
		meta       admin.Meta
	}{
		"zero address": {sap.Address{}, user, e.Account, admin.Meta{}},
		"bad user":     {good, "nope", e.Account, admin.Meta{}},
		"bad account":  {good, user, "nope", admin.Meta{}},
		"bad meta":     {good, user, e.Account, admin.Meta{Role: "a\tb"}},
	} {
		if _, err := a.Provision(ctx, c.id, c.user, c.acct, "", c.meta); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	untouched(t, js, nil, nil)
}

// The flat key's lock is held at the point the stream is written and released when the call returns.
func TestProvisionIdentityHoldsLockAndReleasesIt(t *testing.T) {
	quickLocks(t, longWait)
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, id)
	var held bool
	prev := admin.SetAfterStreamRead(func(int64) {
		kv, _ := js.KeyValue(ctx, store.LeaseBucket)
		_, err := kv.Get(ctx, admin.LockKey(id.Flat()))
		held = err == nil
	})
	t.Cleanup(func() { admin.SetAfterStreamRead(prev) })
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the flat key's lock was not held while the inbox was written")
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := kv.Get(ctx, admin.LockKey(id.Flat())); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("lock not released: %v", err)
	}
}

// With the flat key's lock held elsewhere the call waits on it, refuses naming it, and writes nothing.
func TestProvisionIdentityWaitsFlatKeyLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, id)
	putLock(t, js, id.Flat(), time.Now().Add(time.Hour))
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), id.Flat()) {
		t.Fatalf("with the lock held the call waits on it, got %v", err)
	}
	untouched(t, js, nil, nil)
}

// provNewForm provisions the address owner.group.instance with a fresh user key.
func provNewForm(t *testing.T, e *testenv.Env, a *admin.Admin, owner, group, instance string) (sap.Address, string) {
	t.Helper()
	id := epIn(t, owner, group, instance)
	_, user := e.Mint(t, id)
	if _, err := a.Provision(context.Background(), id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	return id, user
}

func wantRetired(t *testing.T, err error, id sap.Address) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "INV-002") || !strings.Contains(err.Error(), id.String()) {
		t.Fatalf("want an INV-002 refusal naming %s, got %v", id, err)
	}
}

// Provision then Deprovision removes the record, the inbox stream and the lock, leaves a final marker, and the same
// address is then refused.
func TestProvisionDeprovisionRoundTrip(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if got := dirKeys(t, js); !slices.Equal(got, []string{id.Flat()}) {
		t.Fatalf("keys %v", got)
	}
	if got := streamNames(t, js); !slices.Equal(got, []string{"inbox-" + id.Flat()}) {
		t.Fatalf("streams %v", got)
	}
	if err := a.Deprovision(ctx, id); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	wantMarker(t, js, id.Flat(), store.RetiredFinal, user)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if _, err := kv.Get(ctx, admin.LockKey(id.Flat())); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("lock left behind: %v", err)
	}
	if ok, err := a.Exists(ctx, id); err != nil || ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// A burned flat key (a final marker already in the retired bucket, no record) is refused with INV-002 naming the
// address, and nothing is written.
func TestProvisionIdentityBurnedFlatKeyIsRefused(t *testing.T) {
	e, a, js := setup(t)
	id := epIn(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, id)
	putMarker(t, js, id, user, store.RetiredFinal)
	_, err := a.Provision(context.Background(), id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
	// Only that address is burned: a neighbour in the same group and the same instance in another group are free.
	if _, err := a.Provision(context.Background(), epIn(t, "acme", "team1", "ops2"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(context.Background(), epIn(t, "acme", "team2", "ops1"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

// (a) A deprovisioned identity is refused at once, writes a marker, and fences only that address (f).
func TestProvisionIdentityRetiredNameIsRefused(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, id); err != nil {
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
	if err := json.Unmarshal(ent.Value(), &m); err != nil || m.Owner != "acme" || m.Group != "team1" || m.Instance != "ops1" ||
		m.User != user || m.RetiredAt == "" || strings.Contains(string(ent.Value()), `"SU`) {
		t.Fatalf("marker %s %v", ent.Value(), err)
	}
	_, err = a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
	// (f) another instance in the same group, and the same instance in another group, are not blocked.
	if _, err := a.Provision(ctx, epIn(t, "acme", "team1", "ops2"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, epIn(t, "acme", "team2", "ops1"), user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

// DeprovisionIfUser burns the marker too.
func TestProvisionIdentityRetiredByDeprovisionIfUser(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.DeprovisionIfUser(ctx, id, user); err != nil {
		t.Fatal(err)
	}
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
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
	if err := a.Deprovision(ctx, id); err != nil {
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
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
}

// (c) The marker is checked under the flat key's lock: with that lock held elsewhere the call waits on it and refuses
// on the lock without reading the marker; once it is free the retired address is refused and no lock is left behind.
func TestProvisionIdentityRetiredCheckedUnderLock(t *testing.T) {
	quickLocks(t, 300*time.Millisecond)
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	if err := a.Deprovision(ctx, id); err != nil {
		t.Fatal(err)
	}
	putLock(t, js, id.Flat(), time.Now().Add(time.Hour))
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	if err == nil || !strings.Contains(err.Error(), id.Flat()) || strings.Contains(err.Error(), "INV-002") {
		t.Fatalf("with the lock held the call waits on it before reading the marker, got %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	if err := kv.Delete(ctx, admin.LockKey(id.Flat())); err != nil {
		t.Fatal(err)
	}
	_, err = a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	untouched(t, js, nil, nil)
	if _, err := kv.Get(ctx, admin.LockKey(id.Flat())); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("lock left behind: %v", err)
	}
}

// (e) A crash after the marker and before the record goes: the address is already burned, and running deprovision
// again finishes the removal.
func TestDeprovisionRetiredMarkerCrashIsRecoverable(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	// State left by a crash between the marker and the record removal: marker, record and stream all present.
	rk, _ := js.KeyValue(ctx, store.RetiredBucket)
	val, _ := json.Marshal(store.Retired{Owner: "acme", Group: "team1", Instance: "ops1", User: user, RetiredAt: "2026-11-10T00:00:00Z"})
	if _, err := rk.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	_, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := a.Deprovision(ctx, id); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	if got, err := rk.Get(ctx, id.Flat()); err != nil || string(got.Value()) != string(val) {
		t.Fatalf("the first marker must be kept: %v", err)
	}
	if err := a.Deprovision(ctx, id); err != nil { // idempotent
		t.Fatal(err)
	}
	_, err = a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// (e) Through the failure seam: the connection dies right after the record is purged, before the marker (a guarded
// removal writes it after the purge); the same removal run from a fresh connection writes it and finishes.
func TestDeprovisionIfUserCrashAfterPurgeIsRecoverable(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id, user := provNewForm(t, e, a, "acme", "team1", "ops1")
	prev := admin.SetAfterDeprovisionPurge(func() { a.Conn().Close() })
	if err := a.DeprovisionIfUser(ctx, id, user); err == nil {
		t.Fatal("the removal cannot have finished on a closed connection")
	}
	admin.SetAfterDeprovisionPurge(prev)
	b, js := initOn(t, e)
	kv, _ := js.KeyValue(ctx, store.LeaseBucket)
	_ = kv.Delete(ctx, admin.LockKey(id.Flat())) // the dead admin's lock would otherwise wait out its TTL
	if err := b.DeprovisionIfUser(ctx, id, user); err != nil {
		t.Fatal(err)
	}
	_, err := b.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
	if err := b.Deprovision(ctx, id); err != nil {
		t.Fatal(err)
	}
	untouched(t, js, nil, nil)
	_, err = b.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	wantRetired(t, err, id)
}

// An address with no record burns nothing: a mistyped address is not retired for good.
func TestDeprovisionWithoutRecordWritesNoMarker(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	id := epIn(t, "acme", "team1", "ghost")
	if err := a.Deprovision(ctx, id); err != nil {
		t.Fatal(err)
	}
	rk, _ := js.KeyValue(ctx, store.RetiredBucket)
	if _, err := rk.Get(ctx, id.Flat()); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("a marker for an address with no record: %v", err)
	}
}

// (g) An endpoint user, with the permissions provisioning gives an endpoint, has no access to the retired bucket: a
// request on each of its subjects gets no reply while the same kind of request on the directory does.
func TestEndpointUserCannotReachRetiredBucket(t *testing.T) {
	e, a, _ := setup(t)
	id, _ := provNewForm(t, e, a, "acme", "team1", "ops1")
	perms := admin.Permissions(id)
	if perms == nil {
		t.Fatal("no permissions for a valid address")
	}
	for _, s := range append(append([]string{}, perms.Pub.Allow...), perms.Sub.Allow...) {
		if strings.Contains(s, store.RetiredBucket) {
			t.Fatalf("grant %q reaches the retired bucket", s)
		}
	}
	creds, _ := e.MintRaw(t, id.Flat(), *perms)
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
