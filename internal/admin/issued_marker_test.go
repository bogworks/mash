package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// setIssuedStream rewrites the issue stream's history limit and its marker ("" removes the marker), the way an older
// deployment's bucket looks.
func setIssuedStream(t *testing.T, js jetstream.JetStream, limit int, marker string) {
	t.Helper()
	sc := issuedStreamConfig(t, js)
	md := map[string]string{}
	for k, v := range sc.Metadata {
		md[k] = v
	}
	if marker == "" {
		delete(md, store.IssuedMarkerKey)
	} else {
		md[store.IssuedMarkerKey] = marker
	}
	sc.Metadata = md
	sc.MaxMsgsPerSubject = int64(limit)
	if _, err := js.UpdateStream(context.Background(), sc); err != nil {
		t.Fatal(err)
	}
}

func issuedStreamInfo(t *testing.T, js jetstream.JetStream) *jetstream.StreamInfo {
	t.Helper()
	s, err := js.Stream(context.Background(), store.IssuedStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func issuedStreamConfig(t *testing.T, js jetstream.JetStream) jetstream.StreamConfig {
	t.Helper()
	return issuedStreamInfo(t, js).Config
}

func issuedLastSeq(t *testing.T, js jetstream.JetStream) uint64 {
	t.Helper()
	return issuedStreamInfo(t, js).State.LastSeq
}

func recordN(t *testing.T, a *admin.Admin, key string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := a.RecordIssued(context.Background(), store.Issued{PublicKey: key, Address: "x", Kind: store.IssuedEndpoint, IssuedAt: "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateIssuedBucketWritesTheFullMarker(t *testing.T) {
	_, _, js := setup(t)
	c := issuedStreamConfig(t, js)
	if c.Metadata[store.IssuedMarkerKey] != "full" || !c.DenyDelete || !c.DenyPurge {
		t.Fatalf("config %+v", c)
	}
}

// Whether a key's retained history is provably complete follows the bucket's marker. Each case writes `before`
// revisions while the bucket keeps 8 and has no marker, sets the limit and marker (raised:8:SEQ takes the stream's
// last sequence at that moment), then writes `more`.
func TestIssuedHistoryByKeyMarkerStates(t *testing.T) {
	ctx := context.Background()
	type tcase struct {
		name         string
		marker       string
		before, more int
		wantErr      bool
	}
	cases := []tcase{
		{"full marker", "full", 30, 0, false},
		{"raised, fewer than N before the raise", "raised:8:SEQ", 5, 20, false},
		{"raised, exactly N before the raise", "raised:8:SEQ", 8, 0, true},
		{"raised, more than N written before, some evicted", "raised:8:SEQ", 12, 3, true},
		{"no marker, 7 revisions", "", 7, 0, false},
		{"no marker, 8 revisions", "", 8, 0, true},
		{"unreadable marker", "raised:x", 2, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, a, js := setup(t)
			key := userKey(t)
			setIssuedStream(t, js, 8, "") // an old bucket: 8 revisions, no marker
			recordN(t, a, key, tc.before)
			marker := tc.marker
			if marker == "raised:8:SEQ" {
				marker = store.IssuedMarkerRaised(8, issuedLastSeq(t, js))
			}
			setIssuedStream(t, js, store.IssuedHistory, marker)
			recordN(t, a, key, tc.more)
			got, found, err := a.IssuedHistoryByKey(ctx, key)
			if tc.wantErr {
				var inc *admin.IncompleteHistoryError
				if !errors.As(err, &inc) || found {
					t.Fatalf("want an incomplete-history error, got %+v found=%v err=%v", got, found, err)
				}
				return
			}
			if err != nil || !found {
				t.Fatalf("%+v found=%v err=%v", got, found, err)
			}
		})
	}
}

// Init writes raised:<N>:<S> when it raises a bucket with no marker, once, and keeps the hardening.
func TestInitRaiseWritesTheRaisedMarkerOnce(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	key := userKey(t)
	setIssuedStream(t, js, 8, "")
	recordN(t, a, key, 10) // 8 retained, 2 evicted
	last := issuedLastSeq(t, js)
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	c := issuedStreamConfig(t, js)
	want := store.IssuedMarkerRaised(8, last)
	if c.MaxMsgsPerSubject != store.IssuedHistory || c.Metadata[store.IssuedMarkerKey] != want || !c.DenyDelete || !c.DenyPurge || c.AllowRollup || c.Discard != jetstream.DiscardNew {
		t.Fatalf("after the raise %+v, want marker %q", c, want)
	}
	if _, _, err := a.IssuedHistoryByKey(ctx, key); !errors.As(err, new(*admin.IncompleteHistoryError)) {
		t.Fatalf("the key lost revisions before the raise: %v", err)
	}
	// More writes and a second init change nothing.
	recordN(t, a, userKey(t), 3)
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if c := issuedStreamConfig(t, js); c.Metadata[store.IssuedMarkerKey] != want {
		t.Fatalf("the marker changed: %q", c.Metadata[store.IssuedMarkerKey])
	}
}

// A raise never replaces an existing marker, and a bucket already at the full limit gets none.
func TestInitRaiseKeepsAnExistingMarker(t *testing.T) {
	ctx := context.Background()
	for _, marker := range []string{"full", "raised:8:3"} {
		_, a, js := setup(t)
		setIssuedStream(t, js, 8, marker)
		if err := a.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if c := issuedStreamConfig(t, js); c.Metadata[store.IssuedMarkerKey] != marker || c.MaxMsgsPerSubject != store.IssuedHistory {
			t.Fatalf("marker %q became %+v", marker, c.Metadata)
		}
	}
	_, a, js := setup(t)
	setIssuedStream(t, js, store.IssuedHistory, "")
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if c := issuedStreamConfig(t, js); c.Metadata[store.IssuedMarkerKey] != "" {
		t.Fatalf("a bucket already at the limit was marked: %q", c.Metadata[store.IssuedMarkerKey])
	}
}

// The raise comes first and S is read after it, so S is at least the sequence before the raise and equals the stream's
// last sequence afterwards.
func TestInitRaiseReadsTheSequenceAfterTheRaise(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	setIssuedStream(t, js, 8, "")
	recordN(t, a, userKey(t), 10)
	before := issuedLastSeq(t, js)
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	after := issuedLastSeq(t, js)
	got := issuedStreamConfig(t, js).Metadata[store.IssuedMarkerKey]
	if want := store.IssuedMarkerRaised(8, after); got != want || after < before {
		t.Fatalf("marker %q, want %q (before %d, after %d)", got, want, before, after)
	}
}

// A fault between the two updates leaves the limit raised and no marker; init reports it and the legacy 8-revision
// rule applies.
func TestInitRaiseFaultBetweenUpdatesLeavesNoMarker(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	key := userKey(t)
	setIssuedStream(t, js, 8, "")
	recordN(t, a, key, 8)
	prev := admin.SetIssuedMarkerFault(func() error { return errors.New("injected") })
	err := a.Init(ctx)
	admin.SetIssuedMarkerFault(prev)
	if err == nil {
		t.Fatal("init reported no error")
	}
	c := issuedStreamConfig(t, js)
	if c.MaxMsgsPerSubject != store.IssuedHistory || !c.DenyDelete || !c.DenyPurge {
		t.Fatalf("the raise did not stay: %+v", c)
	}
	if v, ok := c.Metadata[store.IssuedMarkerKey]; ok {
		t.Fatalf("a marker was written: %q", v)
	}
	if _, _, err := a.IssuedHistoryByKey(ctx, key); !errors.As(err, new(*admin.IncompleteHistoryError)) {
		t.Fatalf("8 revisions with no marker must not be shown complete: %v", err)
	}
	other := userKey(t)
	recordN(t, a, other, 7)
	if _, found, err := a.IssuedHistoryByKey(ctx, other); err != nil || !found {
		t.Fatalf("7 revisions with no marker: found=%v err=%v", found, err)
	}
}
