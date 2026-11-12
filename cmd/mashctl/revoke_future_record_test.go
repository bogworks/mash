package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
	"github.com/nats-io/nats.go/jetstream"
)

// recordIssuedAt rewrites the issue record of pub so its issued_at is at.
func (h *addEnv) recordIssuedAt(pub string, at time.Time) {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := h.issued()[pub]
	rec.IssuedAt = at.UTC().Format(time.RFC3339)
	if err := adm.RecordIssued(context.Background(), rec); err != nil {
		h.t.Fatal(err)
	}
}

// withRevokeNow replaces the clock a deprovision revocation reads, for the test.
func withRevokeNow(t *testing.T, f func() time.Time) {
	t.Helper()
	prev := ctl.RevokeNow
	ctl.RevokeNow = f
	t.Cleanup(func() { ctl.RevokeNow = prev })
}

func TestDeprovisionRefusesAKeyTheRecordDatesInTheFuture(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
		t.Fatal(errb)
	}
	_, pub := h.credsClaims("n")
	h.recordIssuedAt(pub, time.Now().Add(clockBehind))
	calls := fakeRevoke(t, nil)
	recs := h.records()

	out, errb, code := h.run("deprovision", "n")
	if code != 1 || out != "" || !strings.Contains(errb, "n's current creds JWT claims it was issued in the future (") || !strings.Contains(errb, "fix this machine's clock") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if len(*calls) != 0 || len(h.records()) != len(recs) || h.issued()[pub].RevokedAt != "" {
		t.Fatalf("something changed: calls %+v records %v", *calls, h.records())
	}

	// Control: with the record dated in the past the same command revokes and removes.
	h.recordIssuedAt(pub, time.Now().Add(-clockBehind))
	out, errb, code = h.run("deprovision", "n")
	if code != 0 || len(*calls) != 1 || (*calls)[0].users[0] != pub || h.issued()[pub].RevokedAt == "" {
		t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls)
	}
}

func TestDeprovisionWarnsAndRevokesWithoutARecord(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	u := h.provisionDeclared("a") // not minted by mashctl add: no issue record
	calls := fakeRevoke(t, nil)
	out, errb, code := h.run("deprovision", "a")
	if code != 0 || len(*calls) != 1 || (*calls)[0].users[0] != u {
		t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls)
	}
	if want := "warning: user key " + ctl.ShortKey(u) + "... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway\n"; !strings.Contains(errb, want) {
		t.Fatalf("stderr %q lacks %q", errb, want)
	}
	if strings.Count(errb, "no issue record") != 1 {
		t.Fatalf("want one warning: %q", errb)
	}
}

// The check and the revocation share one reading of the clock.
func TestDeprovisionRevokesAtTheCheckedTime(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
		t.Fatal(errb)
	}
	calls := fakeRevoke(t, nil)
	base := time.Now().Add(time.Hour)
	reads := 0
	withRevokeNow(t, func() time.Time { // each reading is an hour later than the last
		reads++
		return base.Add(time.Duration(reads-1) * time.Hour)
	})
	if out, errb, code := h.run("deprovision", "n"); code != 0 || len(*calls) != 1 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if reads != 1 {
		t.Fatalf("clock read %d times, want 1", reads)
	}
	if got := (*calls)[0].at; !got.Equal(base) {
		t.Fatalf("revoked at %v, want the checked time %v", got, base)
	}
}

func TestCheckNotFutureIssued(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	var pubs []string
	for _, n := range []string{"p", "q", "r"} {
		if _, errb, code := h.run("add", n, "--zone", "green"); code != 0 {
			t.Fatal(errb)
		}
		_, pub := h.credsClaims(n)
		pubs = append(pubs, pub)
	}
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().Add(time.Hour)
	targets := []ctl.RevokeTarget{{Name: "p", Key: pubs[0]}, {Name: "q", Key: pubs[1]}, {Name: "r", Key: pubs[2]}}

	var errb strings.Builder
	if err := ctl.CheckNotFutureIssued(ctx, adm, targets, now, &errb); err != nil || errb.String() != "" {
		t.Fatalf("all in the past: %v %q", err, errb.String())
	}

	// One future-dated key refuses the whole set, and an unrecorded key beside it only warns.
	h.recordIssuedAt(pubs[1], now.Add(time.Hour))
	unrecorded := testenvKey(t, h.e)
	errb.Reset()
	err = ctl.CheckNotFutureIssued(ctx, adm, append([]ctl.RevokeTarget{{Key: unrecorded}}, targets...), now, &errb)
	if err == nil || !strings.HasPrefix(err.Error(), "q's current creds JWT claims it was issued in the future (") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("one future key: %v", err)
	}
	if !strings.Contains(errb.String(), "user key "+ctl.ShortKey(unrecorded)+"... has no issue record") {
		t.Fatalf("no warning for the unrecorded key: %q", errb.String())
	}
	// Without a name the short key is shown.
	err = ctl.CheckNotFutureIssued(ctx, adm, []ctl.RevokeTarget{{Key: pubs[1]}}, now, &errb)
	if err == nil || !strings.HasPrefix(err.Error(), ctl.ShortKey(pubs[1])+"...'s current creds JWT") {
		t.Fatalf("unnamed: %v", err)
	}
}

// A missing bucket is an absent record: warn and go on.
func TestCheckNotFutureIssuedMissingBucketWarns(t *testing.T) {
	e := testenv.Start(t) // no issue-record bucket at all
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	var errb strings.Builder
	if err := ctl.CheckNotFutureIssued(context.Background(), adm, []ctl.RevokeTarget{{Key: testenvKey(t, e)}}, time.Now(), &errb); err != nil || !strings.Contains(errb.String(), "has no issue record") {
		t.Fatalf("missing bucket: %v %q", err, errb.String())
	}
}

// A lookup that fails for any other reason refuses.
func TestCheckNotFutureIssuedLookupErrorRefuses(t *testing.T) {
	e := testenv.Start(t)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	key := testenvKey(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var errb strings.Builder
	err = ctl.CheckNotFutureIssued(ctx, adm, []ctl.RevokeTarget{{Key: key}}, time.Now(), &errb)
	if err == nil || !strings.Contains(err.Error(), "user key "+ctl.ShortKey(key)+"...: its issue record could not be read") || !strings.Contains(err.Error(), "mashctl reissue NAME") || !strings.Contains(err.Error(), "nothing was changed") || errb.String() != "" {
		t.Fatalf("failed lookup: %v %q", err, errb.String())
	}
}

// A record whose issued_at is missing or not a time refuses deprovision before anything is revoked or removed.
func TestDeprovisionRefusesAMalformedIssueTime(t *testing.T) {
	for _, bad := range []string{"", "yesterday-ish"} {
		h := newAddEnv(t, addPolicy)
		if _, errb, code := h.run("add", "n", "--zone", "green"); code != 0 {
			t.Fatal(errb)
		}
		_, pub := h.credsClaims("n")
		js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
		if err != nil {
			t.Fatal(err)
		}
		kv, err := js.KeyValue(context.Background(), store.IssuedBucket)
		if err != nil {
			t.Fatal(err)
		}
		rec := h.issued()[pub]
		rec.IssuedAt = bad // written straight to the bucket: RecordIssued fills an empty time
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Put(context.Background(), pub, raw); err != nil {
			t.Fatal(err)
		}
		calls := fakeRevoke(t, nil)
		recs := h.records()
		out, errb, code := h.run("deprovision", "n")
		if code != 1 || out != "" || !strings.Contains(errb, "user key "+ctl.ShortKey(pub)+"...") || !strings.Contains(errb, "missing or malformed issued_at") || !strings.Contains(errb, "mashctl reissue NAME") || !strings.Contains(errb, "nothing was changed") {
			t.Fatalf("%q: %d %q %q", bad, code, out, errb)
		}
		if len(*calls) != 0 || len(h.records()) != len(recs) || h.issued()[pub].RevokedAt != "" {
			t.Fatalf("%q: something changed: calls %+v records %v", bad, *calls, h.records())
		}
	}
}

func testenvKey(t *testing.T, e *testenv.Env) string {
	t.Helper()
	_, pub := e.Mint(t, "x")
	return pub
}
