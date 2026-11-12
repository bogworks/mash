package admin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
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

func userKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

// issuedSubjects are the subjects a client would use to write, read, list or remove the issue record.
func issuedSubjects(key string) []string {
	return []string{
		"$KV." + store.IssuedBucket + "." + key,
		"$KV." + store.IssuedBucket + ".>",
		"$JS.API.DIRECT.GET." + store.IssuedStreamName + ".$KV." + store.IssuedBucket + "." + key,
		"$JS.API.DIRECT.GET." + store.IssuedStreamName,
		"$JS.API.STREAM.INFO." + store.IssuedStreamName,
		"$JS.API.STREAM.PURGE." + store.IssuedStreamName,
		"$JS.API.STREAM.DELETE." + store.IssuedStreamName,
		"$JS.API.STREAM.UPDATE." + store.IssuedStreamName,
		"$JS.API.STREAM.MSG.GET." + store.IssuedStreamName,
		"$JS.API.STREAM.MSG.DELETE." + store.IssuedStreamName,
		"$JS.API.CONSUMER.CREATE." + store.IssuedStreamName,
		"$JS.API.CONSUMER.CREATE." + store.IssuedStreamName + ".x.$KV." + store.IssuedBucket + ".>",
		"$JS.API.CONSUMER.DURABLE.CREATE." + store.IssuedStreamName + ".x",
	}
}

// No permission list mash gives an endpoint grants anything on the issue record: not the legacy or the new-form
// endpoint list, not a recipient-narrowed one, and not the template of a zone's scoped signing key.
func TestEndpointPermissionsGrantNothingOnIssuedRecord(t *testing.T) {
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
			if strings.Contains(g, "mash-issued") || strings.Contains(g, "mash_issued") {
				t.Errorf("%s: a grant names the issue record: %q", name, g)
			}
			for _, s := range issuedSubjects(userKey(t)) {
				if server.SubjectsCollide(g, s) {
					t.Errorf("%s: grant %q covers issue record subject %q", name, g, s)
				}
			}
		}
	}
}

// A live endpoint connection is refused every write, read, listing and removal on the issue record, and the record
// is unchanged afterwards.
func TestLiveEndpointCannotTouchIssuedRecord(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	key := userKey(t)
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Tags: []string{"t"}}); err != nil {
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

	for _, subj := range issuedSubjects(key) {
		if strings.HasSuffix(subj, ".>") {
			continue // a wildcard is not a publishable subject
		}
		if err := nc.PublishMsg(&nats.Msg{Subject: subj, Reply: nats.NewInbox(), Data: []byte(`{"public_key":"` + key + `","kind":"admin"}`)}); err != nil {
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
	// A subscription on the record's subjects is refused too.
	if _, err := nc.SubscribeSync("$KV." + store.IssuedBucket + ".>"); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	select {
	case err := <-violations:
		if !errors.Is(err, nats.ErrPermissionViolation) {
			t.Errorf("subscribe: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a subscription on the record was not refused")
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if kv, err := js.KeyValue(cctx, store.IssuedBucket); err == nil {
		if _, err := kv.Get(cctx, key); err == nil {
			t.Error("an endpoint read the issue record")
		}
	}

	recs, present, err := a.Issued(ctx)
	if err != nil || !present || len(recs) != 1 || recs[0].PublicKey != key || recs[0].Kind != store.IssuedEndpoint {
		t.Fatalf("the record changed: %+v %v %v", recs, present, err)
	}
}

// Init makes the bucket as the record needs it: history 8, no TTL, file storage, and a stream that refuses rollup,
// delete and purge, even to the admin.
func TestInitCreatesHardenedIssuedBucket(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	s, err := js.Stream(ctx, store.IssuedStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := info.Config
	if c.AllowRollup || !c.DenyDelete || !c.DenyPurge || c.MaxAge != 0 || c.Storage != jetstream.FileStorage || c.MaxMsgsPerSubject != 8 || c.Discard != jetstream.DiscardNew {
		t.Fatalf("stream config %+v", c)
	}
	key := userKey(t)
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint}); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx); err == nil {
		t.Error("the admin purged the issue record")
	}
	if err := s.DeleteMsg(ctx, 1); err == nil {
		t.Error("the admin deleted a message of the issue record")
	}
	if err := a.Init(ctx); err != nil { // idempotent, keeps the record
		t.Fatal(err)
	}
	if recs, _, err := a.Issued(ctx); err != nil || len(recs) != 1 {
		t.Fatalf("after a second init: %+v %v", recs, err)
	}
}

// A re-issue of one key adds a revision; nothing is removed, and the entry names the new time.
func TestRecordIssuedIsAppendOnly(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	key := userKey(t)
	for i := 0; i < 3; i++ {
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Tags: []string{"v" + string(rune('0'+i))}}); err != nil {
			t.Fatal(err)
		}
	}
	kv, err := js.KeyValue(ctx, store.IssuedBucket)
	if err != nil {
		t.Fatal(err)
	}
	h, err := kv.History(ctx, key)
	if err != nil || len(h) != 3 {
		t.Fatalf("history %d %v", len(h), err)
	}
	recs, _, err := a.Issued(ctx)
	if err != nil || len(recs) != 1 || recs[0].Tags[0] != "v2" {
		t.Fatalf("%+v %v", recs, err)
	}
	if recs[0].Owner != "o" || recs[0].IssuedAt == "" {
		t.Errorf("owner and time not filled: %+v", recs[0])
	}
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: "not-a-key", Kind: "x"}); err == nil {
		t.Error("a bad key was recorded")
	}
}

// An admin connection of a deployment from before the record creates the bucket on first use, hardened; a bucket
// that something else made, allowing rollup, is refused rather than written.
func TestRecordIssuedCreatesTheBucketOnDemandAndRefusesAnUnhardenedOne(t *testing.T) {
	e := testenv.Start(t)
	a, js := initOn(t, e)
	ctx := context.Background()
	if _, present, err := a.Issued(ctx); err != nil || present {
		t.Fatalf("present before any write: %v %v", present, err)
	}
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: userKey(t), Address: "x", Kind: store.IssuedEndpoint}); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyIssuedBucket(ctx, js); err != nil {
		t.Fatal(err)
	}

	e2 := testenv.Start(t)
	a2, js2 := initOn(t, e2)
	if _, err := js2.CreateKeyValue(ctx, store.IssuedConfig()); err != nil { // the client default allows rollup
		t.Fatal(err)
	}
	if err := a2.RecordIssued(ctx, store.Issued{PublicKey: userKey(t), Address: "x", Kind: store.IssuedEndpoint}); !errors.Is(err, store.ErrIssuedBucket) {
		t.Fatalf("a bucket that allows rollup was written to: %v", err)
	}
	if err := a2.Init(ctx); !errors.Is(err, store.ErrIssuedBucket) {
		t.Fatalf("init accepted it: %v", err)
	}
}
