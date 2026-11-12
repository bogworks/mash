package admin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func provisionFor(t *testing.T, e *testenv.Env, a *admin.Admin, name string) {
	t.Helper()
	_, u := e.Mint(t, name)
	if _, err := a.Provision(context.Background(), name, u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
}

func publishRefused(t *testing.T, js jetstream.JetStream, subj string) {
	t.Helper()
	pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := js.Publish(pctx, subj, []byte("x")); err == nil {
		t.Fatalf("publish to %s was acknowledged on a suspended inbox", subj)
	}
}

func TestSuspendParksSubjectsAndKeepsStoredMail(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	if _, err := js.Publish(ctx, "agents.mail.cc.o.default.alpha", []byte("before")); err != nil {
		t.Fatal(err)
	}
	st, err := a.Suspend(ctx, "alpha")
	if err != nil || !st.Suspended || st.Msgs != 1 || st.LastSeq != 1 || st.Since == "" {
		t.Fatalf("Suspend: %+v %v", st, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"mash.suspended.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	publishRefused(t, js, "agents.mail.cc.o.alpha")
	publishRefused(t, js, "agents.mail.cc.o.default.alpha")
	// The message stored before the suspension is still read through the durable consumer.
	s, _ := js.Stream(ctx, "inbox-alpha")
	c, err := s.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range b.Messages() {
		if string(m.Data()) != "before" {
			t.Fatalf("data %q", m.Data())
		}
		n++
	}
	if n != 1 {
		t.Fatalf("read %d messages", n)
	}
}

func TestResumeRestoresSubjectsAndDelivery(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "legacy")
	for name, subj := range map[string]string{"legacy": "agents.mail.cc.o.legacy"} {
		want := inboxSubjects(t, js, name)
		if _, err := a.Suspend(ctx, name); err != nil {
			t.Fatal(err)
		}
		if err := a.Resume(ctx, name); err != nil {
			t.Fatal(err)
		}
		if got := inboxSubjects(t, js, name); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: subjects %v, want %v", name, got, want)
		}
		s, _ := js.Stream(ctx, "inbox-"+name)
		for _, k := range []string{store.MetaSuspendedSubjects, store.MetaSuspendedAt, store.MetaSuspendedID} {
			if _, ok := s.CachedInfo().Config.Metadata[k]; ok {
				t.Fatalf("%s: metadata %s left", name, k)
			}
		}
		if _, err := js.Publish(ctx, subj, []byte("after")); err != nil {
			t.Fatalf("%s: publish after resume: %v", name, err)
		}
		if st, err := a.Suspended(ctx, name); err != nil || st.Suspended || st.Msgs != 1 {
			t.Fatalf("%s: %+v %v", name, st, err)
		}
	}
}

func TestSuspendResumeIdempotent(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatalf("resume of a live inbox: %v", err)
	}
	first, err := a.Suspend(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // a second suspend must not rewrite the timestamp
	second, err := a.Suspend(ctx, "alpha")
	if err != nil || second != first {
		t.Fatalf("second suspend: %+v %v, first %+v", second, err, first)
	}
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatalf("second resume: %v", err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
}

func TestSuspendLastSeqAfterBurst(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	const n = 25
	for i := 0; i < n; i++ {
		if _, err := js.Publish(ctx, "agents.mail.cc.o.alpha", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	st, err := a.Suspend(ctx, "alpha")
	if err != nil || st.LastSeq != n || st.Msgs != n {
		t.Fatalf("%+v %v", st, err)
	}
	if got, err := a.Suspended(ctx, "alpha"); err != nil || got != st {
		t.Fatalf("Suspended %+v %v, want %+v", got, err, st)
	}
}

func TestSuspendMissingAndBadName(t *testing.T) {
	_, a, _ := setup(t)
	ctx := context.Background()
	if _, err := a.Suspend(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "no inbox stream") {
		t.Fatalf("Suspend: %v", err)
	}
	if err := a.Resume(ctx, "ghost"); err == nil {
		t.Fatal("Resume of a missing stream succeeded")
	}
	if _, err := a.Suspended(ctx, "ghost"); err == nil {
		t.Fatal("Suspended of a missing stream succeeded")
	}
	if _, err := a.Suspend(ctx, "Bad Name"); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestEnsureInboxAcceptsSuspended(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "legacy")
	for _, name := range []string{"legacy"} {
		if _, err := a.Suspend(ctx, name); err != nil {
			t.Fatal(err)
		}
		if made, err := a.EnsureInbox(ctx, name); err != nil || len(made) != 0 {
			t.Fatalf("%s: EnsureInbox: %v %v", name, made, err)
		}
		if got := inboxSubjects(t, js, name); !reflect.DeepEqual(got, []string{"mash.suspended." + name}) {
			t.Fatalf("%s: EnsureInbox changed subjects: %v", name, got)
		}
	}
	// Any other difference on a suspended stream still errors.
	s, _ := js.Stream(ctx, "inbox-legacy")
	cfg := s.CachedInfo().Config
	cfg.MaxMsgs = 5
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "legacy"); err == nil || !strings.Contains(err.Error(), "max_msgs") {
		t.Fatalf("EnsureInbox: %v", err)
	}
}

func TestApplyLimitsKeepsSuspension(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	if _, err := a.Suspend(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	l := store.DefaultLimits()
	l.InboxMaxBytes *= 2
	if err := a.SetLimits(l); err != nil {
		t.Fatal(err)
	}
	lines, err := a.ApplyLimits(ctx, "alpha")
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "max_bytes") {
		t.Fatalf("ApplyLimits: %v %v", lines, err)
	}
	st, err := a.Suspended(ctx, "alpha")
	if err != nil || !st.Suspended {
		t.Fatalf("suspension lost: %+v %v", st, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"mash.suspended.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha", "agents.mail.*.o.default.alpha"}) {
		t.Fatalf("subjects after resume %v", got)
	}
}

func TestProvisionRefusesSuspended(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Suspend(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"mash.suspended.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	// A retried provision (the record is missing) must not clobber the parked stream.
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := kv.Purge(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{}); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("Provision: %v", err)
	}
	if st, err := a.Suspended(ctx, "alpha"); err != nil || !st.Suspended {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestDeprovisionSuspended(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	if _, err := a.Suspend(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-alpha"); err == nil {
		t.Fatal("stream still there")
	}
}

func TestParkedSubjectWithoutMetadataIsNotSuspended(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"mash.suspended.alpha"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if st, err := a.Suspended(ctx, "alpha"); err != nil || st.Suspended {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Fatalf("EnsureInbox: %v", err)
	}
}

func TestSuspendKeepsOtherMetadata(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	cfg.Metadata = map[string]string{"keep": "me"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Suspend(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	s, _ = js.Stream(ctx, "inbox-alpha")
	var saved []string
	md := s.CachedInfo().Config.Metadata
	if err := json.Unmarshal([]byte(md[store.MetaSuspendedSubjects]), &saved); err != nil || md["keep"] != "me" || len(saved) != 1 {
		t.Fatalf("metadata %v", md)
	}
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	s, _ = js.Stream(ctx, "inbox-alpha")
	md = s.CachedInfo().Config.Metadata
	_, a1 := md[store.MetaSuspendedSubjects]
	_, a2 := md[store.MetaSuspendedAt]
	if md["keep"] != "me" || a1 || a2 {
		t.Fatalf("metadata after resume %v", md)
	}
}

func updateInbox(t *testing.T, js jetstream.JetStream, name string, f func(*jetstream.StreamConfig)) {
	t.Helper()
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	f(&cfg)
	if _, err := js.UpdateStream(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestSuspendReparksWhenMetadataButSubjectsNotParked(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) {
		c.Metadata = map[string]string{store.MetaSuspendedSubjects: `["stale.subject"]`, store.MetaSuspendedAt: "2000-01-01T00:00:00Z", store.MetaSuspendedID: "stale-id"}
	})
	st, err := a.Suspend(ctx, "alpha")
	if err != nil || !st.Suspended || st.Since == "2000-01-01T00:00:00Z" || st.ID == "" || st.ID == "stale-id" {
		t.Fatalf("Suspend: %+v %v", st, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"mash.suspended.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha"}) {
		t.Fatalf("subjects after resume %v", got)
	}
}

func TestResumeParkedWithoutMetadataErrors(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) { c.Subjects = []string{"mash.suspended.alpha"} })
	if st, err := a.Suspended(ctx, "alpha"); err != nil || st.Suspended {
		t.Fatalf("Suspended: %+v %v", st, err)
	}
	if err := a.Resume(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Resume: %v", err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"mash.suspended.alpha"}) {
		t.Fatalf("subjects changed: %v", got)
	}
	// Unusable metadata is the same as none.
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) {
		c.Metadata = map[string]string{store.MetaSuspendedSubjects: `not json`}
	})
	if err := a.Resume(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Resume with bad metadata: %v", err)
	}
}

func TestResumeClearsStaleMetadataKeepsSubjects(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	want := inboxSubjects(t, js, "alpha")
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) {
		c.Metadata = map[string]string{"keep": "me", store.MetaSuspendedSubjects: `["x.y"]`, store.MetaSuspendedAt: "2000-01-01T00:00:00Z"}
	})
	if err := a.Resume(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, want) {
		t.Fatalf("subjects %v, want %v", got, want)
	}
	s, _ := js.Stream(ctx, "inbox-alpha")
	md := s.CachedInfo().Config.Metadata
	if _, ok := md[store.MetaSuspendedSubjects]; ok || md["keep"] != "me" {
		t.Fatalf("metadata %v", md)
	}
}

func TestEndpointCannotUpdateItsStream(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	creds, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(e.URL, natsauth.UserCredentials(creds), nats.CustomInboxPrefix("_INBOX.alpha"))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	denied(t, nc, "$JS.API.STREAM.UPDATE.inbox-alpha", `{"name":"inbox-alpha","subjects":["mash.suspended.alpha"]}`, "stream update of its own inbox")
	for _, p := range admin.Permissions(e.Owner, "alpha").Pub.Allow {
		if strings.Contains(p, "STREAM.UPDATE") {
			t.Fatalf("permissions allow %s", p)
		}
	}
}

func TestSuspendRefusesParkedWithoutSavedSubjects(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) { c.Subjects = []string{store.SuspendedSubject("alpha")} })
	if _, err := a.Suspend(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "saved subjects are missing") {
		t.Fatalf("got %v", err)
	}
}

func intact(t *testing.T, a *admin.Admin, js jetstream.JetStream, name string) {
	t.Helper()
	if _, err := js.Stream(context.Background(), "inbox-"+name); err != nil {
		t.Fatalf("%s: stream gone: %v", name, err)
	}
	recs, err := a.List(context.Background())
	if err != nil || len(recs) != 1 || recs[0].Name != name {
		t.Fatalf("%s: records %v %v", name, recs, err)
	}
}

func TestDeprovisionDrained(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "d")
	if _, err := js.Publish(ctx, "agents.mail.cc.o.d", []byte("one")); err != nil {
		t.Fatal(err)
	}
	// Not suspended.
	if err := a.DeprovisionDrained(ctx, "d", "x", 1); err == nil || !strings.Contains(err.Error(), "not suspended; run suspend first") {
		t.Fatalf("not suspended: %v", err)
	}
	intact(t, a, js, "d")
	st, err := a.Suspend(ctx, "d")
	if err != nil || st.LastSeq != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	// Wrong sequence.
	if err := a.DeprovisionDrained(ctx, "d", st.ID, 0); err == nil || !strings.Contains(err.Error(), "last_seq 1, not the drained 0") {
		t.Fatalf("seq differs: %v", err)
	}
	intact(t, a, js, "d")
	// Another admin resumes in between.
	if err := a.Resume(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "d", st.ID, st.LastSeq); err == nil || !strings.Contains(err.Error(), "not suspended") {
		t.Fatalf("after resume: %v", err)
	}
	intact(t, a, js, "d")
	// Suspended and matching.
	st2, err := a.Suspend(ctx, "d")
	if err != nil || st2.ID == "" || st2.ID == st.ID || len(st2.ID) != 32 {
		t.Fatalf("resume then suspend: %+v (first %q) %v", st2, st.ID, err)
	}
	if err := a.DeprovisionDrained(ctx, "d", st.ID, 1); err == nil || !strings.Contains(err.Error(), "not the suspension you drained") {
		t.Fatalf("old id: %v", err)
	}
	intact(t, a, js, "d")
	if err := a.DeprovisionDrained(ctx, "d", "", 1); err == nil || !strings.Contains(err.Error(), "not the suspension you drained") {
		t.Fatalf("empty id: %v", err)
	}
	intact(t, a, js, "d")
	if again, err := a.Suspend(ctx, "d"); err != nil || again.ID != st2.ID {
		t.Fatalf("idempotent suspend id: %+v %v", again, err)
	}
	if err := a.DeprovisionDrained(ctx, "d", st2.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-d"); err == nil {
		t.Fatal("stream still there")
	}
	if recs, err := a.List(ctx); err != nil || len(recs) != 0 {
		t.Fatalf("records %v %v", recs, err)
	}
	if err := a.DeprovisionDrained(ctx, "Bad Name", "x", 0); err == nil {
		t.Fatal("bad name accepted")
	}
}

// A re-provisioned endpoint of the same name with the same last_seq must not fall to an old drain command.
func TestDeprovisionDrainedRefusesOldSuspensionOfReprovisionedName(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "r")
	old, err := a.Suspend(ctx, "r")
	if err != nil || old.ID == "" {
		t.Fatalf("%+v %v", old, err)
	}
	if err := a.DeprovisionDrained(ctx, "r", old.ID, old.LastSeq); err != nil {
		t.Fatal(err)
	}
	provisionFor(t, e, a, "r")
	cur, err := a.Suspend(ctx, "r")
	if err != nil || cur.LastSeq != old.LastSeq || cur.ID == old.ID {
		t.Fatalf("new suspension %+v old %+v %v", cur, old, err)
	}
	if err := a.DeprovisionDrained(ctx, "r", old.ID, old.LastSeq); err == nil || !strings.Contains(err.Error(), "not the suspension you drained") {
		t.Fatalf("old id accepted: %v", err)
	}
	intact(t, a, js, "r")
	if got, err := a.Suspended(ctx, "r"); err != nil || !got.Suspended || got.ID != cur.ID {
		t.Fatalf("state after refusal: %+v %v", got, err)
	}
}

// A hand-made suspension without an id is suspended, with an empty ID, and never satisfies a drained guard.
func TestDeprovisionDrainedEmptyIDNeverMatches(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "h")
	updateInbox(t, js, "h", func(c *jetstream.StreamConfig) {
		c.Metadata = map[string]string{store.MetaSuspendedSubjects: `["agents.mail.cc.o.h"]`}
		c.Subjects = []string{store.SuspendedSubject("h")}
	})
	st, err := a.Suspended(ctx, "h")
	if err != nil || !st.Suspended || st.ID != "" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := a.DeprovisionDrained(ctx, "h", "", st.LastSeq); err == nil || !strings.Contains(err.Error(), "not the suspension you drained") {
		t.Fatalf("empty id matched: %v", err)
	}
	intact(t, a, js, "h")
}

func TestDeprovisionDrainedRefusesMissingStream(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteStream(ctx, "inbox-alpha"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "alpha", "x", 0); err == nil || !strings.Contains(err.Error(), "no inbox stream") {
		t.Fatalf("got %v", err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "alpha"); err != nil {
		t.Fatalf("record gone: %v", err)
	}
}

// putUser rewrites name's directory record with another user key, as a hand edit of the bucket would.
func putUser(t *testing.T, js jetstream.JetStream, name, user string) {
	t.Helper()
	ctx := context.Background()
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.GetRecord(ctx, kv, name)
	if err != nil {
		t.Fatal(err)
	}
	rec.User = user
	b, _ := json.Marshal(rec)
	if _, err := kv.Put(ctx, name, b); err != nil {
		t.Fatal(err)
	}
}

func parked(t *testing.T, a *admin.Admin, name, id string) {
	t.Helper()
	if got, err := a.Suspended(context.Background(), name); err != nil || !got.Suspended || got.ID != id {
		t.Fatalf("not left parked: %+v %v", got, err)
	}
}

func TestDeprovisionDrainedRefusesReplacedRecord(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "p")
	st, err := a.Suspend(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	_, other := e.Mint(t, "other")
	putUser(t, js, "p", other)
	if err := a.DeprovisionDrained(ctx, "p", st.ID, st.LastSeq); err == nil || !strings.Contains(err.Error(), "different user key") {
		t.Fatalf("replaced record: %v", err)
	}
	intact(t, a, js, "p")
	parked(t, a, "p", st.ID)
}

func TestDeprovisionDrainedRefusesNoRecord(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "n")
	st, err := a.Suspend(ctx, "n")
	if err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := kv.Purge(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "n", st.ID, st.LastSeq); err == nil || !strings.Contains(err.Error(), "has no directory record") {
		t.Fatalf("no record: %v", err)
	}
	if _, err := js.Stream(ctx, "inbox-n"); err != nil {
		t.Fatalf("stream gone: %v", err)
	}
	parked(t, a, "n", st.ID)
}

func TestDeprovisionDrainedRefusesSuspendedWithoutRecord(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if err := kv.Purge(ctx, "w"); err != nil {
		t.Fatal(err)
	}
	st, err := a.Suspend(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}
	// A record appears afterwards; the suspension saved no user, so it proves nothing.
	provisionFor(t, e, a, "w2")
	_ = st
	rec, err := store.GetRecord(ctx, kv, "w2")
	if err != nil {
		t.Fatal(err)
	}
	rec.Name, rec.MailSubject = "w", store.MailSubjectFor(rec.Owner, "w")
	b, _ := json.Marshal(rec)
	if _, err := kv.Put(ctx, "w", b); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "w", st.ID, st.LastSeq); err == nil || !strings.Contains(err.Error(), "suspended without a record") {
		t.Fatalf("suspended without record: %v", err)
	}
	if _, err := js.Stream(ctx, "inbox-w"); err != nil {
		t.Fatalf("stream gone: %v", err)
	}
	if _, err := kv.Get(ctx, "w"); err != nil {
		t.Fatalf("record gone: %v", err)
	}
	parked(t, a, "w", st.ID)
}

func TestDeprovisionDrainedRefusesRecordChangedBeforePurge(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	provisionFor(t, e, a, "c")
	st, err := a.Suspend(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	prev := admin.SetAfterDeprovisionGet(func() {
		role := "changed"
		if _, err := a.SetMeta(ctx, "c", admin.MetaUpdate{Role: &role}); err != nil {
			t.Error(err)
		}
	})
	defer admin.SetAfterDeprovisionGet(prev)
	if err := a.DeprovisionDrained(ctx, "c", st.ID, st.LastSeq); err == nil || !strings.Contains(err.Error(), "the record changed; left in place") {
		t.Fatalf("changed record: %v", err)
	}
	intact(t, a, js, "c")
	parked(t, a, "c", st.ID)
}

func TestSuspendSavesUserAndResumeClearsIt(t *testing.T) {
	ctx := context.Background()
	e, a, js := setup(t)
	_, user := e.Mint(t, "u")
	if _, err := a.Provision(ctx, "u", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Suspend(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	s, _ := js.Stream(ctx, "inbox-u")
	info, _ := s.Info(ctx)
	if got := info.Config.Metadata[store.MetaSuspendedUser]; got != user {
		t.Fatalf("saved user %q, want %q", got, user)
	}
	// Idempotent suspend keeps what is saved.
	putUser(t, js, "u", "UOTHER")
	if _, err := a.Suspend(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	info, _ = s.Info(ctx)
	if got := info.Config.Metadata[store.MetaSuspendedUser]; got != user {
		t.Fatalf("idempotent suspend changed user to %q", got)
	}
	if err := a.Resume(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	info, _ = s.Info(ctx)
	if _, ok := info.Config.Metadata[store.MetaSuspendedUser]; ok {
		t.Fatalf("resume left the user: %v", info.Config.Metadata)
	}
}
