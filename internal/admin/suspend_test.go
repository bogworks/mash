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

func provisionFor(t *testing.T, e *testenv.Env, a *admin.Admin, name, zone string) {
	t.Helper()
	_, u := e.Mint(t, name)
	if _, err := a.Provision(context.Background(), name, u, e.Account, "", admin.Meta{Zone: zone}); err != nil {
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
	provisionFor(t, e, a, "alpha", "default")
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
	provisionFor(t, e, a, "legacy", "")
	provisionFor(t, e, a, "zoned", "default")
	for name, subj := range map[string]string{"legacy": "agents.mail.cc.o.legacy", "zoned": "agents.mail.cc.o.default.zoned"} {
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
		for _, k := range []string{store.MetaSuspendedSubjects, store.MetaSuspendedAt} {
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "legacy", "")
	provisionFor(t, e, a, "zoned", "default")
	for _, name := range []string{"legacy", "zoned"} {
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
	// A zoned record whose suspended stream saved only the legacy subject is a subjects difference.
	s, _ = js.Stream(ctx, "inbox-zoned")
	cfg = s.CachedInfo().Config
	cfg.Metadata[store.MetaSuspendedSubjects] = `["agents.mail.*.o.zoned"]`
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "zoned"); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Fatalf("EnsureInbox: %v", err)
	}
}

func TestApplyLimitsKeepsSuspension(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha", "default")
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

func TestSetZoneAndProvisionRefuseSuspended(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", u, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Suspend(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetZone(ctx, "alpha", "default"); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("SetZone: %v", err)
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "alpha", "")
	updateInbox(t, js, "alpha", func(c *jetstream.StreamConfig) {
		c.Metadata = map[string]string{store.MetaSuspendedSubjects: `["stale.subject"]`, store.MetaSuspendedAt: "2000-01-01T00:00:00Z"}
	})
	st, err := a.Suspend(ctx, "alpha")
	if err != nil || !st.Suspended || st.Since == "2000-01-01T00:00:00Z" {
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
	provisionFor(t, e, a, "alpha", "")
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
	provisionFor(t, e, a, "alpha", "default")
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
	provisionFor(t, e, a, "d", "")
	if _, err := js.Publish(ctx, "agents.mail.cc.o.d", []byte("one")); err != nil {
		t.Fatal(err)
	}
	// Not suspended.
	if err := a.DeprovisionDrained(ctx, "d", 1); err == nil || !strings.Contains(err.Error(), "not suspended; run suspend first") {
		t.Fatalf("not suspended: %v", err)
	}
	intact(t, a, js, "d")
	st, err := a.Suspend(ctx, "d")
	if err != nil || st.LastSeq != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	// Wrong sequence.
	if err := a.DeprovisionDrained(ctx, "d", 0); err == nil || !strings.Contains(err.Error(), "last_seq 1, not the drained 0") {
		t.Fatalf("seq differs: %v", err)
	}
	intact(t, a, js, "d")
	// Another admin resumes in between.
	if err := a.Resume(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "d", st.LastSeq); err == nil || !strings.Contains(err.Error(), "not suspended") {
		t.Fatalf("after resume: %v", err)
	}
	intact(t, a, js, "d")
	// Suspended and matching.
	if _, err := a.Suspend(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeprovisionDrained(ctx, "d", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-d"); err == nil {
		t.Fatal("stream still there")
	}
	if recs, err := a.List(ctx); err != nil || len(recs) != 0 {
		t.Fatalf("records %v %v", recs, err)
	}
	if err := a.DeprovisionDrained(ctx, "Bad Name", 0); err == nil {
		t.Fatal("bad name accepted")
	}
}
