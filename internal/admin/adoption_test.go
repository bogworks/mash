package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func pubKey(t *testing.T, create func() (nkeys.KeyPair, error)) string {
	t.Helper()
	kp, err := create()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

// adoptionSubjects are the subjects a client would use to write, read, list or remove the adoption marker.
func adoptionSubjects() []string {
	return []string{
		"$KV." + store.AdoptionBucket + "." + store.AdoptionKey,
		"$KV." + store.AdoptionBucket + ".>",
		"$JS.API.DIRECT.GET." + store.AdoptionStreamName + ".$KV." + store.AdoptionBucket + "." + store.AdoptionKey,
		"$JS.API.DIRECT.GET." + store.AdoptionStreamName,
		"$JS.API.STREAM.INFO." + store.AdoptionStreamName,
		"$JS.API.STREAM.PURGE." + store.AdoptionStreamName,
		"$JS.API.STREAM.DELETE." + store.AdoptionStreamName,
		"$JS.API.STREAM.UPDATE." + store.AdoptionStreamName,
		"$JS.API.STREAM.MSG.GET." + store.AdoptionStreamName,
		"$JS.API.STREAM.MSG.DELETE." + store.AdoptionStreamName,
		"$JS.API.CONSUMER.CREATE." + store.AdoptionStreamName,
		"$JS.API.CONSUMER.CREATE." + store.AdoptionStreamName + ".x.$KV." + store.AdoptionBucket + ".>",
		"$JS.API.CONSUMER.DURABLE.CREATE." + store.AdoptionStreamName + ".x",
	}
}

// No permission list mash gives an endpoint grants anything on the adoption marker.
func TestEndpointPermissionsGrantNothingOnAdoptionMarker(t *testing.T) {
	id, err := sap.New("claude", "o", "a")
	if err != nil {
		t.Fatal(err)
	}
	nf, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string]jwt.Permissions{
		"Permissions":     admin.Permissions("o", "a"),
		"PermissionsTo":   admin.PermissionsTo("o", "a", []string{"b", "c"}),
		"ZonePermissions": admin.ZonePermissions("o", "red", []string{"blue"}),
		"NewForm":         nf,
	}
	for name, p := range lists {
		allow := append(slices.Clone([]string(p.Pub.Allow)), p.Sub.Allow...)
		for _, g := range allow {
			if strings.Contains(g, "mash-adoption") || strings.Contains(g, "mash_adoption") {
				t.Errorf("%s: a grant names the adoption marker: %q", name, g)
			}
			for _, s := range adoptionSubjects() {
				if server.SubjectsCollide(g, s) {
					t.Errorf("%s: grant %q covers adoption subject %q", name, g, s)
				}
			}
		}
	}
}

// A live endpoint connection is refused every write, read, listing and removal on the marker, and the marker is
// unchanged afterwards.
func TestLiveEndpointCannotTouchAdoptionMarker(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	acct, sub, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	if _, _, err := a.Adopt(ctx, acct, sub, by, nil); err != nil {
		t.Fatal(err)
	}
	creds, _ := e.Mint(t, "alpha")
	violations := make(chan error, 64)
	nc, err := nats.Connect(e.URL, natsauth.UserCredentials(creds), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		select {
		case violations <- err:
		default:
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	forged := `{"v":1,"account":"` + acct + `","submitter":"` + pubKey(t, nkeys.CreateUser) + `","adopted_at":"2026-01-01T00:00:00Z","by":"` + by + `","action":"adopt"}`
	for _, subj := range adoptionSubjects() {
		if strings.HasSuffix(subj, ".>") {
			continue // a wildcard is not a publishable subject
		}
		if err := nc.PublishMsg(&nats.Msg{Subject: subj, Reply: nats.NewInbox(), Data: []byte(forged)}); err != nil {
			t.Fatal(err)
		}
		_ = nc.Flush()
		select {
		case err := <-violations:
			if !errors.Is(err, nats.ErrPermissionViolation) {
				t.Errorf("%s: %v, want a permissions violation", subj, err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: not refused", subj)
		}
	}
	if _, err := nc.SubscribeSync("$KV." + store.AdoptionBucket + ".>"); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	select {
	case err := <-violations:
		if !errors.Is(err, nats.ErrPermissionViolation) {
			t.Errorf("subscribe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a subscription on the marker was not refused")
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if kv, err := js.KeyValue(cctx, store.AdoptionBucket); err == nil {
		if _, err := kv.Get(cctx, store.AdoptionKey); err == nil {
			t.Error("an endpoint read the adoption marker")
		}
		if _, err := kv.Put(cctx, store.AdoptionKey, []byte(forged)); err == nil {
			t.Error("an endpoint wrote the adoption marker")
		}
	}

	m, found, err := a.Adoption(ctx)
	if err != nil || !found || m.Submitter != sub || m.Action != store.ActionAdopt {
		t.Fatalf("the marker changed: %+v %v %v", m, found, err)
	}
}

// Init makes the bucket hardened, even against the admin, and is idempotent.
func TestInitCreatesHardenedAdoptionBucket(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	s, err := js.Stream(ctx, store.AdoptionStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := info.Config
	if c.AllowRollup || !c.DenyDelete || !c.DenyPurge || c.MaxAge != 0 || c.Storage != jetstream.FileStorage ||
		c.MaxMsgsPerSubject != store.AdoptionHistory || c.Discard != jetstream.DiscardNew || c.MaxBytes != store.AdoptionMaxBytes {
		t.Fatalf("stream config %+v", c)
	}
	acct, sub, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	if _, _, err := a.Adopt(ctx, acct, sub, by, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx); err == nil {
		t.Error("the admin purged the adoption marker")
	}
	if err := s.DeleteMsg(ctx, 1); err == nil {
		t.Error("the admin deleted a message of the adoption marker")
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.Adoption(ctx); err != nil || !found {
		t.Fatalf("after a second init: %v %v", found, err)
	}
}

func TestAdoptWritesTheMarkerAndRecovers(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	if _, found, err := a.Adoption(ctx); err != nil || found {
		t.Fatalf("a fresh account: %v %v", found, err)
	}
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	s1, s2 := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateOperator)

	m, action, err := a.Adopt(ctx, acct, s1, by, nil)
	if err != nil || action != store.ActionAdopt {
		t.Fatalf("%+v %q %v", m, action, err)
	}
	got, found, err := a.Adoption(ctx)
	if err != nil || !found || !reflect.DeepEqual(got, m) || got.V != 1 || got.Account != acct || got.Submitter != s1 || got.By != by || got.Action != "adopt" || got.NextSubmitter != "" {
		t.Fatalf("%+v %v %v", got, found, err)
	}
	if _, err := time.Parse(time.RFC3339, got.AdoptedAt); err != nil || !strings.HasSuffix(got.AdoptedAt, "Z") {
		t.Errorf("adopted_at %q is not RFC 3339 UTC", got.AdoptedAt)
	}

	// The same key again writes nothing.
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	before, _ := kv.Get(ctx, store.AdoptionKey)
	if _, action, err := a.Adopt(ctx, acct, s1, by, nil); err != nil || action != admin.AdoptUnchanged {
		t.Fatalf("again: %q %v", action, err)
	}
	if after, _ := kv.Get(ctx, store.AdoptionKey); after.Revision() != before.Revision() {
		t.Error("an adopt with the same key wrote a revision")
	}

	// A different key is the recovery rotation: the old key is refused at once (the next read names the new one).
	m2, action, err := a.Adopt(ctx, acct, s2, by, nil)
	if err != nil || action != store.ActionRecover || m2.Submitter != s2 {
		t.Fatalf("%+v %q %v", m2, action, err)
	}
	if got, _, _ := a.Adoption(ctx); got.Submitter != s2 || got.Action != "recover" {
		t.Fatalf("%+v", got)
	}
	hist, err := kv.History(ctx, store.AdoptionKey)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history %d %v: the revisions are the audit trail", len(hist), err)
	}
}

func TestAdoptRefusals(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	seed := func() string {
		kp, _ := nkeys.CreateUser()
		s, _ := kp.Seed()
		return string(s)
	}()
	for name, sub := range map[string]string{"empty": "", "garbage": "UABC", "a seed": seed} {
		_, _, err := a.Adopt(ctx, acct, sub, by, nil)
		if !errors.Is(err, admin.ErrSubmitterInvalid) {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && sub != "" && strings.Contains(err.Error(), sub) {
			t.Errorf("%s: the error echoes the value", name)
		}
	}
	if _, _, err := a.Adopt(ctx, acct, acct, by, nil); !errors.Is(err, admin.ErrSubmitterIsAccount) {
		t.Errorf("the account's own key: %v", err)
	}
	if _, found, err := a.Adoption(ctx); err != nil || found {
		t.Fatalf("a refused adopt wrote a marker: %v %v", found, err)
	}
	// Any nkey public key type is accepted.
	for _, mk := range []func() (nkeys.KeyPair, error){nkeys.CreateUser, nkeys.CreateOperator, nkeys.CreateServer} {
		if _, _, err := a.Adopt(ctx, acct, pubKey(t, mk), by, nil); err != nil {
			t.Errorf("%v", err)
		}
	}
}

// Two operators adopting different keys at once: both writes land as revisions in some order, none is lost, and the
// last one wins.
func TestAdoptRacesAreCompareAndSet(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	const writers = 4
	subs := make([]string, writers)
	for i := range subs {
		subs[i] = pubKey(t, nkeys.CreateUser)
	}
	var wg sync.WaitGroup
	for _, s := range subs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := a.Adopt(ctx, acct, s, by, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	hist, err := kv.History(ctx, store.AdoptionKey)
	if err != nil || len(hist) != writers {
		t.Fatalf("%d revisions, %v: want %d", len(hist), err, writers)
	}
	actions := map[string]int{}
	for _, h := range hist {
		var m store.Marker
		if err := json.Unmarshal(h.Value(), &m); err != nil {
			t.Fatal(err)
		}
		actions[m.Action]++
	}
	if actions["adopt"] != 1 || actions["recover"] != writers-1 {
		t.Errorf("actions %v", actions)
	}
}

// A marker that cannot be read, or a bucket that is not as mash makes it, is an error, never "not adopted".
func TestAdoptionFailsClosed(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, sub, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	if _, _, err := a.Adopt(ctx, acct, sub, by, nil); err != nil {
		t.Fatal(err)
	}
	// A closed context.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, found, err := a.Adoption(cctx); err == nil || found {
		t.Errorf("a cancelled read: %v %v", found, err)
	}
	// A value this version does not understand.
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)
	for name, val := range map[string]string{
		"not json":       "nope",
		"unknown schema": `{"v":2,"account":"` + acct + `","submitter":"` + sub + `","adopted_at":"x","by":"` + by + `","action":"adopt"}`,
		"no submitter":   `{"v":1,"account":"` + acct + `","adopted_at":"x","by":"` + by + `","action":"adopt"}`,
	} {
		if _, err := kv.Put(ctx, store.AdoptionKey, []byte(val)); err != nil {
			t.Fatal(err)
		}
		if _, found, err := a.Adoption(ctx); err == nil || found {
			t.Errorf("%s: %v %v", name, found, err)
		}
	}
	// A bucket that allows rollup and deletes: the plain key-value default, as a stranger would have made it.
	e2 := setupBare(t)
	if _, err := e2.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: store.AdoptionBucket, History: 64}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := e2.a.Adoption(ctx); err == nil || found || !errors.Is(err, store.ErrAdoptionBucket) {
		t.Errorf("an unhardened bucket: %v %v", found, err)
	}
	if _, _, err := e2.a.Adopt(ctx, acct, sub, by, nil); !errors.Is(err, store.ErrAdoptionBucket) {
		t.Errorf("adopt into an unhardened bucket: %v", err)
	}
}

type bareEnv struct {
	a  *admin.Admin
	js jetstream.JetStream
}

// setupBare is a server and an Admin whose Init was not run.
func setupBare(t *testing.T) bareEnv {
	t.Helper()
	e := testenv.Start(t)
	nc := e.Conn(t, e.AdminCreds)
	a, err := admin.New(nc, e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(nc)
	return bareEnv{a, js}
}

// The exempt set is sorted, de-duplicated and validated; the same submitter with the same set writes nothing, with
// another set writes an adopt revision, a different submitter is a recovery that refreshes the set; an older marker
// without the field reads as empty.
func TestAdoptRecordsTheExemptSet(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	acct, by := pubKey(t, nkeys.CreateAccount), pubKey(t, nkeys.CreateUser)
	s1, s2 := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	u1, u2 := pubKey(t, nkeys.CreateUser), pubKey(t, nkeys.CreateUser)
	kv, _ := js.KeyValue(ctx, store.AdoptionBucket)

	for _, bad := range []string{"hello", pubKey(t, nkeys.CreateOperator), pubKey(t, nkeys.CreateAccount), ""} {
		if _, _, err := a.Adopt(ctx, acct, s1, by, []string{u1, bad}); !errors.Is(err, admin.ErrExemptInvalid) {
			t.Errorf("exempt %q: %v", bad, err)
		}
	}
	if _, found, _ := a.Adoption(ctx); found {
		t.Fatal("a refused adopt wrote a marker")
	}

	m, action, err := a.Adopt(ctx, acct, s1, by, []string{u2, u1, u2})
	want := []string{u1, u2}
	slices.Sort(want)
	if err != nil || action != store.ActionAdopt || !slices.Equal(m.Exempt, want) || !m.ExemptRecorded() {
		t.Fatalf("%+v %q %v", m, action, err)
	}
	r1, _ := kv.Get(ctx, store.AdoptionKey)
	// Same submitter, same set (in another order): nothing written.
	if _, action, err := a.Adopt(ctx, acct, s1, by, []string{u1, u2}); err != nil || action != admin.AdoptUnchanged {
		t.Fatalf("%q %v", action, err)
	}
	if r2, _ := kv.Get(ctx, store.AdoptionKey); r2.Revision() != r1.Revision() {
		t.Error("the same set wrote a revision")
	}
	// Same submitter, another set: a new adopt revision.
	m, action, err = a.Adopt(ctx, acct, s1, by, []string{u1})
	if err != nil || action != admin.AdoptExemptUpdated || m.Action != store.ActionAdopt || !slices.Equal(m.Exempt, []string{u1}) {
		t.Fatalf("%+v %q %v", m, action, err)
	}
	// A different submitter is a recovery and refreshes the set.
	m, action, err = a.Adopt(ctx, acct, s2, by, []string{u2})
	if err != nil || action != store.ActionRecover || !slices.Equal(m.Exempt, []string{u2}) {
		t.Fatalf("%+v %q %v", m, action, err)
	}
	if hist, _ := kv.History(ctx, store.AdoptionKey); len(hist) != 3 {
		t.Errorf("history %d, want 3", len(hist))
	}

	// An older marker without the field is readable, with an empty set; the same submitter and a set updates it.
	old := `{"v":1,"account":"` + acct + `","submitter":"` + s2 + `","adopted_at":"2026-01-01T00:00:00Z","by":"` + by + `","action":"adopt"}`
	if _, err := kv.Put(ctx, store.AdoptionKey, []byte(old)); err != nil {
		t.Fatal(err)
	}
	got, found, err := a.Adoption(ctx)
	if err != nil || !found || got.Submitter != s2 || len(got.Exempt) != 0 || got.ExemptRecorded() {
		t.Fatalf("%+v %v %v", got, found, err)
	}
	if _, action, err := a.Adopt(ctx, acct, s2, by, []string{u1}); err != nil || action != admin.AdoptExemptUpdated {
		t.Fatalf("%q %v", action, err)
	}
}
