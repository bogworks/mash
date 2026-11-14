package admin_test

import (
	"context"
	"errors"
	"fmt"
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
// endpoint list, not a recipient-narrowed one.
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
		"Permissions":   admin.Permissions("o", "a"),
		"PermissionsTo": admin.PermissionsTo("o", "a", []string{"b", "c"}),
		"NewForm":       nf,
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

// Init makes the bucket as the record needs it: history 64, no TTL, file storage, and a stream that refuses rollup,
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
	if c.AllowRollup || !c.DenyDelete || !c.DenyPurge || c.MaxAge != 0 || c.Storage != jetstream.FileStorage || c.MaxMsgsPerSubject != store.IssuedHistory || c.Discard != jetstream.DiscardNew {
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

// The purpose is a closed set: issue, rotation and probe are accepted, anything else is refused, and none given is an
// ordinary issue.
func TestRecordIssuedPurpose(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	for _, p := range []string{store.PurposeIssue, store.PurposeRotation, store.PurposeProbe, ""} {
		key := userKey(t)
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Purpose: p}); err != nil {
			t.Fatalf("purpose %q: %v", p, err)
		}
		recs, _, err := a.Issued(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := p
		if want == "" {
			want = store.PurposeIssue
		}
		i := slices.IndexFunc(recs, func(r store.Issued) bool { return r.PublicKey == key })
		if i < 0 || recs[i].Purpose != want {
			t.Errorf("purpose %q read back as %+v", p, recs)
		}
	}
	bad := userKey(t)
	for _, p := range []string{"bogus", "Issue", "probe "} {
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: bad, Address: "x", Kind: store.IssuedEndpoint, Purpose: p}); err == nil {
			t.Errorf("purpose %q was recorded", p)
		}
	}
	recs, _, _ := a.Issued(ctx)
	if slices.ContainsFunc(recs, func(r store.Issued) bool { return r.PublicKey == bad }) {
		t.Error("a refused write left a record")
	}
}

// A record written before the field has no purpose and reads as issue; an unknown value already stored is shown as it is.
func TestIssuedReadsAMissingPurposeAsIssue(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	kv, err := js.KeyValue(ctx, store.IssuedBucket)
	if err != nil {
		t.Fatal(err)
	}
	old, odd := userKey(t), userKey(t)
	if _, err := kv.Put(ctx, old, []byte(`{"public_key":"`+old+`","address":"o","kind":"endpoint","tags":[],"generation":0,"issued_at":"2026-01-01T00:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, odd, []byte(`{"public_key":"`+odd+`","address":"o","kind":"endpoint","tags":[],"generation":0,"issued_at":"2026-01-01T00:00:00Z","purpose":"future"}`)); err != nil {
		t.Fatal(err)
	}
	recs, _, err := a.Issued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range recs {
		got[r.PublicKey] = r.Purpose
	}
	if got[old] != store.PurposeIssue || got[odd] != "future" {
		t.Errorf("purposes %v", got)
	}
}

// MarkRevoked sets revoked_at once, never moves or clears it, and says not recorded for a key mash never issued.
func TestMarkRevokedSetsOnceAndIsIdempotent(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	key := userKey(t)
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Tags: []string{"t"}, Purpose: store.PurposeRotation}); err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("x", 3600))
	if err := a.MarkRevoked(ctx, key, t1); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.IssuedBucket)
	h1, _ := kv.History(ctx, key)
	if err := a.MarkRevoked(ctx, key, t1.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h2, _ := kv.History(ctx, key)
	if len(h2) != len(h1) {
		t.Errorf("a second mark wrote %d new revision(s)", len(h2)-len(h1))
	}
	recs, _, err := a.Issued(ctx)
	if err != nil || len(recs) != 1 {
		t.Fatalf("%+v %v", recs, err)
	}
	r := recs[0]
	if r.RevokedAt != "2026-11-12T09:00:00Z" || r.Purpose != store.PurposeRotation || r.Address != "x" || len(r.Tags) != 1 {
		t.Errorf("record %+v", r)
	}
	// A key mash never recorded, and a key that is not a user key.
	if err := a.MarkRevoked(ctx, userKey(t), t1); !errors.Is(err, admin.ErrNotRecorded) {
		t.Errorf("unrecorded key: %v", err)
	}
	if err := a.MarkRevoked(ctx, "nope", t1); err == nil || errors.Is(err, admin.ErrNotRecorded) {
		t.Errorf("bad key: %v", err)
	}
	// Recording the key again keeps the revocation.
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint}); err != nil {
		t.Fatal(err)
	}
	if recs, _, _ := a.Issued(ctx); recs[0].RevokedAt != "2026-11-12T09:00:00Z" {
		t.Errorf("a re-record dropped revoked_at: %+v", recs[0])
	}
}

// With no bucket yet, a mark is not-recorded and creates nothing.
func TestMarkRevokedWithoutABucket(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	if err := js.DeleteStream(ctx, store.IssuedStreamName); err != nil {
		t.Skipf("cannot remove the stream: %v", err)
	}
	if err := a.MarkRevoked(ctx, userKey(t), time.Now()); !errors.Is(err, admin.ErrNotRecorded) {
		t.Errorf("%v", err)
	}
}

// Marks and records of one key made at the same time lose no field: every writer retries on a lost race.
func TestMarkRevokedAndRecordIssuedRaceLoseNothing(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		key := userKey(t)
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		for i := 0; i < 6; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				errs <- a.MarkRevoked(ctx, key, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			}()
			go func(i int) {
				defer wg.Done()
				errs <- a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Purpose: store.PurposeRotation, Tags: []string{"r" + string(rune('0'+i))}})
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		recs, _, err := a.Issued(ctx)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(recs, func(r store.Issued) bool { return r.PublicKey == key })
		if i < 0 || recs[i].RevokedAt != "2026-01-01T00:00:00Z" || recs[i].Purpose != store.PurposeRotation || len(recs[i].Tags) != 1 {
			t.Fatalf("round %d: a field was lost: %+v", round, recs)
		}
	}
}

// More than eight successive records of one key all stay, up to the cap of store.IssuedHistory; the cap is 64.
func TestRecordIssuedKeepsRevisionsUpToTheCap(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	if store.IssuedHistory != 64 {
		t.Fatalf("the cap is %d, want 64", store.IssuedHistory)
	}
	key := userKey(t)
	for i := 0; i < store.IssuedHistory+3; i++ {
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, Tags: []string{fmt.Sprint(i)}}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		kv, err := js.KeyValue(ctx, store.IssuedBucket)
		if err != nil {
			t.Fatal(err)
		}
		h, err := kv.History(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if want := min(i+1, store.IssuedHistory); len(h) != want {
			t.Fatalf("after %d records the history holds %d, want %d", i+1, len(h), want)
		}
	}
}

// A bucket from the first version (history 8) is accepted, and Init raises it to the cap without losing a record.
func TestInitRaisesAnOldIssuedBucketHistory(t *testing.T) {
	e := testenv.Start(t)
	a, js := initOn(t, e)
	ctx := context.Background()
	if _, err := store.CreateIssuedBucket(ctx, js); err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(ctx, store.IssuedStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sc := info.Config
	sc.MaxMsgsPerSubject = 8
	if _, err := js.UpdateStream(ctx, sc); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyIssuedBucket(ctx, js); err != nil {
		t.Fatalf("a history 8 bucket was refused: %v", err)
	}
	key := userKey(t)
	for i := 0; i < 5; i++ {
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	info, err = s.Info(ctx)
	if err != nil || info.Config.MaxMsgsPerSubject != store.IssuedHistory {
		t.Fatalf("history after init: %+v %v", info.Config.MaxMsgsPerSubject, err)
	}
	kv, _ := js.KeyValue(ctx, store.IssuedBucket)
	if h, err := kv.History(ctx, key); err != nil || len(h) != 5 {
		t.Fatalf("records after the raise: %d %v", len(h), err)
	}
}

// IssuedHistoryByKey reports the maximum issued_at over every retained revision, not only the latest, and marks a
// history at the cap as truncated.
func TestIssuedHistoryByKey(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	key := userKey(t)
	if _, found, err := a.IssuedHistoryByKey(ctx, key); err != nil || found {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
	rec := func(at string) {
		t.Helper()
		if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, IssuedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	rec("2030-01-01T00:00:00Z")
	rec("2026-01-01T00:00:00Z") // a later write with an earlier time
	got, found, err := a.IssuedHistoryByKey(ctx, key)
	if err != nil || !found || got.Revisions != 2 || got.Truncated {
		t.Fatalf("%+v found=%v err=%v", got, found, err)
	}
	if got.Max.Format(time.RFC3339) != "2030-01-01T00:00:00Z" || got.Latest.Format(time.RFC3339) != "2026-01-01T00:00:00Z" {
		t.Fatalf("max %v latest %v", got.Max, got.Latest)
	}
	for i := 0; i < store.IssuedHistory; i++ {
		rec("2026-01-01T00:00:00Z")
	}
	got, _, err = a.IssuedHistoryByKey(ctx, key)
	if err != nil || got.Revisions != store.IssuedHistory || !got.Truncated || got.Max.Year() != 2026 {
		t.Fatalf("at the cap: %+v err=%v", got, err)
	}
}

// A bucket status that cannot be read refuses the history read: the history cap is unknown, so an evicted future-dated
// record cannot be ruled out.
func TestIssuedHistoryByKeyStatusFailure(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	key := userKey(t)
	if err := a.RecordIssued(ctx, store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, IssuedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	prev := admin.SetIssuedStatusFault(func() error { return errors.New("status unavailable") })
	_, found, err := a.IssuedHistoryByKey(ctx, key)
	admin.SetIssuedStatusFault(prev)
	if err == nil || found || !strings.Contains(err.Error(), "status") {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, found, err := a.IssuedHistoryByKey(ctx, key); err != nil || !found {
		t.Fatalf("after the fault: found=%v err=%v", found, err)
	}
}
