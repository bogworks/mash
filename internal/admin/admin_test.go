package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/admin"
	"github.com/randybias/mash/internal/store"
	"github.com/randybias/mash/internal/testenv"
)

func setup(t *testing.T) (*testenv.Env, *admin.Admin, jetstream.JetStream) {
	t.Helper()
	e := testenv.Start(t)
	nc := e.Conn(t, e.AdminCreds)
	a, err := admin.New(nc, e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(nc)
	return e, a, js
}

func TestProvisionCreatesInboxConsumerRecord(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	rec, err := a.Provision(ctx, "alpha", user, e.Account, "test endpoint")
	if err != nil {
		t.Fatal(err)
	}
	if rec.MailSubject != "agents.mail.cc.o.alpha" || rec.User != user || rec.Account != e.Account ||
		rec.Agent != "claude-code" || rec.AgentToken != "cc" || rec.Owner != "o" || rec.Note != "test endpoint" || rec.Created == "" {
		t.Fatalf("record %+v", rec)
	}
	s, err := js.Stream(ctx, "inbox-alpha")
	if err != nil {
		t.Fatal(err)
	}
	c := s.CachedInfo().Config
	if len(c.Subjects) != 1 || c.Subjects[0] != "agents.mail.*.o.alpha" || c.Storage != jetstream.FileStorage ||
		c.Retention != jetstream.LimitsPolicy || c.MaxAge != 30*24*time.Hour || c.MaxBytes != 16<<20 ||
		c.MaxMsgSize != 1<<20 || c.Discard != jetstream.DiscardOld || c.Duplicates != 2*time.Minute ||
		!c.DenyDelete || !c.DenyPurge || !c.AllowDirect || c.SubjectTransform != nil {
		t.Fatalf("inbox config %+v", c)
	}
	cons, err := s.Consumer(ctx, "deliver")
	if err != nil {
		t.Fatal(err)
	}
	cc := cons.CachedInfo().Config
	if cc.Durable != "deliver" || cc.AckPolicy != jetstream.AckExplicitPolicy || cc.AckWait != 30*time.Second ||
		cc.MaxAckPending != 1000 || cc.DeliverPolicy != jetstream.DeliverAllPolicy {
		t.Fatalf("consumer config %+v", cc)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	got, err := store.GetRecord(ctx, kv, "alpha")
	if err != nil || got != rec {
		t.Fatalf("directory: %+v %v", got, err)
	}
	lst, err := a.List(ctx)
	if err != nil || len(lst) != 1 || lst[0].Name != "alpha" {
		t.Fatalf("list %+v %v", lst, err)
	}
}

func TestProvisionRejectsBadNameAndDuplicate(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	for _, bad := range []string{"Alpha", "a--b", ""} {
		if _, err := a.Provision(ctx, bad, user, e.Account, ""); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	if _, err := a.Provision(ctx, "alpha", "Unotakey", e.Account, ""); err == nil {
		t.Error("bad user key accepted")
	}
	if _, err := a.Provision(ctx, "alpha", user, e.Account, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(ctx, "alpha", user, e.Account, ""); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestDeprovisionRemovesAll(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := js.Stream(ctx, "inbox-alpha"); err == nil {
		t.Fatal("inbox still exists")
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := store.GetRecord(ctx, kv, "alpha"); err == nil {
		t.Fatal("record still exists")
	}
	if _, err := a.Provision(ctx, "alpha", user, e.Account, ""); err != nil {
		t.Fatalf("re-provision after deprovision: %v", err)
	}
}

func TestInitIdempotent(t *testing.T) {
	_, a, js := setup(t)
	ctx := context.Background()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := lease.Status(ctx)
	if st.TTL() != 90*time.Second || st.History() != 1 {
		t.Fatalf("lease bucket ttl=%v history=%d", st.TTL(), st.History())
	}
	dir, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ds, _ := dir.Status(ctx)
	if ds.TTL() != 0 || ds.History() != 1 {
		t.Fatalf("directory ttl=%v history=%d", ds.TTL(), ds.History())
	}
}

// denied asserts that a raw request on subj gets no successful answer.
func denied(t *testing.T, nc *nats.Conn, subj, body, what string) {
	t.Helper()
	m, err := nc.Request(subj, []byte(body), time.Second)
	if err == nil && m.Header.Get("Status") == "" && !strings.Contains(string(m.Data), `"error"`) {
		t.Errorf("%s: allowed (%s)", what, m.Data)
	}
}

func TestEndpointPermissionDenials(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	credsA, userA := e.Mint(t, "a")
	_, userB := e.Mint(t, "b")
	for n, u := range map[string]string{"a": userA, "b": userB} {
		if _, err := a.Provision(ctx, n, u, e.Account, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, to := range []string{"a", "b"} {
		if _, err := js.Publish(ctx, "agents.mail.cc.o."+to, []byte("seed")); err != nil {
			t.Fatal(err)
		}
	}
	nc, err := nats.Connect(e.URL, nats.UserCredentials(credsA), nats.CustomInboxPrefix("_INBOX_a"))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	denied(t, nc, "$JS.API.DIRECT.GET.inbox-b", `{"seq":1}`, "direct get of another inbox")
	denied(t, nc, "$JS.API.STREAM.MSG.GET.inbox-b", `{"seq":1}`, "msg get of another inbox")
	denied(t, nc, "$JS.API.STREAM.MSG.GET.inbox-a", `{"seq":1}`, "msg get (non-direct) of own inbox")
	denied(t, nc, "$JS.API.CONSUMER.MSG.NEXT.inbox-b.deliver", `{"batch":1,"expires":500000000}`, "fetch from another inbox")
	denied(t, nc, "$KV.mash-leases.b", `{}`, "another endpoint's lease")
	denied(t, nc, "$KV.mash-directory.a", `{}`, "writing the directory")
	denied(t, nc, "$JS.API.STREAM.MSG.DELETE.inbox-a", `{"seq":1}`, "deleting a message")
	denied(t, nc, "$JS.API.STREAM.PURGE.inbox-a", `{}`, "purging own inbox")
	denied(t, nc, "$JS.API.STREAM.DELETE.inbox-a", ``, "deleting own inbox")
	denied(t, nc, "$JS.API.STREAM.CREATE.x", `{"name":"x","subjects":["x"]}`, "creating a stream")

	// Inbox privacy: replies (JetStream deliveries, KV reads, prompt answers)
	// travel on inbox subjects, so a may subscribe only to its own prefix.
	for _, subj := range []string{"_INBOX.>", "_INBOX_b.>", "_INBOX_b.x"} {
		errs := make(chan error, 1)
		sc, err := nats.Connect(e.URL, nats.UserCredentials(credsA), nats.CustomInboxPrefix("_INBOX_a"),
			nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
				select {
				case errs <- err:
				default:
				}
			}))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = sc.SubscribeSync(subj)
		sc.Flush()
		select {
		case err := <-errs:
			if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") {
				t.Errorf("subscribe %s: unexpected error %v", subj, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("subscribe %s: allowed; another endpoint's replies would be readable", subj)
		}
		sc.Close()
	}

	// And the allowed path: a reads its own inbox by direct get.
	m, err := nc.Request("$JS.API.DIRECT.GET.inbox-a", []byte(`{"seq":1}`), 2*time.Second)
	if err != nil {
		t.Fatalf("own direct get: %v", err)
	}
	if string(m.Data) != "seed" {
		t.Fatalf("own direct get: %q", m.Data)
	}
}
