package admin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

const (
	zonedSubj = "agents.mail.cc.o.z1.alpha"
	fiveSubj  = "agents.mail.cc.o.alpha"
)

// drainInbox reads every message of alpha's inbox through its durable consumer and returns their payloads.
func drainInbox(t *testing.T, js jetstream.JetStream, want int) []string {
	t.Helper()
	ctx := context.Background()
	s, err := js.Stream(ctx, "inbox-alpha")
	if err != nil {
		t.Fatal(err)
	}
	cons, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		batch, err := cons.Fetch(want-len(got), jetstream.FetchMaxWait(500*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		for m := range batch.Messages() {
			got = append(got, string(m.Data()))
			_ = m.Ack()
		}
	}
	return got
}

func TestUnzoneKeepsQueuedMailAndIsIdempotent(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	user := provisionZoned(t, e, a, js, "alpha", "z1", admin.Meta{Role: "r", Labels: map[string]string{"team": "t"}})
	// Mail queued under both subjects before the move.
	for subj, body := range map[string]string{zonedSubj: "zoned", fiveSubj: "five"} {
		if _, err := js.Publish(ctx, subj, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	changed, err := a.Unzone(ctx, "alpha")
	if err != nil || !changed {
		t.Fatalf("unzone: %v %v", changed, err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, _ := kv.Get(ctx, "alpha")
	rec, err := store.ParseRecord("alpha", ent.Value())
	if err != nil || rec.Zone != "" || rec.MailSubject != fiveSubj || rec.Role != "r" || rec.Note != "n" || rec.User != user || rec.Labels["team"] != "t" {
		t.Fatalf("record %+v %v", rec, err)
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(ent.Value(), &obj)
	if _, has := obj["zone"]; has {
		t.Fatalf("zone key left in %s", ent.Value())
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	// New mail to the five-token subject arrives; the zoned subject no longer has a stream.
	if _, err := js.Publish(ctx, fiveSubj, []byte("new")); err != nil {
		t.Fatalf("five-token publish: %v", err)
	}
	if _, err := js.Publish(ctx, zonedSubj, []byte("late")); err == nil {
		t.Fatal("the zoned subject still lands in the stream")
	}
	got := drainInbox(t, js, 3)
	if len(got) != 3 {
		t.Fatalf("read %v: queued mail lost", got)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	if !seen["zoned"] || !seen["five"] || !seen["new"] {
		t.Fatalf("read %v", got)
	}
	// The strict inbox check is satisfied.
	if made, err := a.EnsureInbox(ctx, "alpha"); err != nil || len(made) != 0 {
		t.Fatalf("EnsureInbox %v %v", made, err)
	}

	// Idempotent.
	if changed, err := a.Unzone(ctx, "alpha"); err != nil || changed {
		t.Fatalf("rerun: %v %v", changed, err)
	}
	// An unknown endpoint is an error.
	if _, err := a.Unzone(ctx, "ghost"); err == nil {
		t.Fatal("unknown endpoint accepted")
	}
}

// A run that stopped after the record was rewritten (the stream still takes the zoned subject) is finished by a re-run,
// and so is one that stopped before it (the record still zoned, the stream already five-token).
func TestUnzoneResumesAfterACrashAtEitherStep(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionZoned(t, e, a, js, "alpha", "z1", admin.Meta{})
	if _, err := a.Unzone(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	// Crash after the record: the stream still has the zoned filter.
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if changed, err := a.Unzone(ctx, "alpha"); err != nil || !changed {
		t.Fatalf("resume: %v %v", changed, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	// Crash before the record: put the zone back on the record only.
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, _ := kv.Get(ctx, "alpha")
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(ent.Value(), &obj)
	obj["zone"], obj["mail_subject"] = json.RawMessage(`"z1"`), json.RawMessage(`"`+zonedSubj+`"`)
	val, _ := json.Marshal(obj)
	if _, err := kv.Put(ctx, "alpha", val); err != nil {
		t.Fatal(err)
	}
	if changed, err := a.Unzone(ctx, "alpha"); err != nil || !changed {
		t.Fatalf("resume 2: %v %v", changed, err)
	}
	ent, _ = kv.Get(ctx, "alpha")
	if rec, _ := store.ParseRecord("alpha", ent.Value()); rec.Zone != "" || rec.MailSubject != fiveSubj {
		t.Fatalf("record %+v", rec)
	}
}

// An inbox that cannot be moved is refused before the directory record changes: the record keeps its zone.
func TestUnzoneChecksTheStreamBeforeTheRecord(t *testing.T) {
	cases := map[string]func(t *testing.T, js jetstream.JetStream){
		"missing stream": func(t *testing.T, js jetstream.JetStream) {
			if err := js.DeleteStream(context.Background(), "inbox-alpha"); err != nil {
				t.Fatal(err)
			}
		},
		"unexpected filter": func(t *testing.T, js jetstream.JetStream) {
			s, _ := js.Stream(context.Background(), "inbox-alpha")
			cfg := s.CachedInfo().Config
			cfg.Subjects = append(cfg.Subjects, "agents.mail.*.o.other")
			if _, err := js.UpdateStream(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
		},
		"suspended": func(t *testing.T, js jetstream.JetStream) {
			s, _ := js.Stream(context.Background(), "inbox-alpha")
			cfg := s.CachedInfo().Config
			saved, _ := json.Marshal(cfg.Subjects)
			cfg.Metadata = map[string]string{store.MetaSuspendedSubjects: string(saved)}
			cfg.Subjects = []string{store.SuspendedSubject("alpha")}
			if _, err := js.UpdateStream(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			provisionZoned(t, e, a, js, "alpha", "z1", admin.Meta{})
			breakIt(t, js)
			kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
			before, _ := kv.Get(ctx, "alpha")
			if changed, err := a.Unzone(ctx, "alpha"); err == nil || changed {
				t.Fatalf("unzone: %v %v", changed, err)
			}
			after, _ := kv.Get(ctx, "alpha")
			if after.Revision() != before.Revision() {
				t.Fatalf("the record was rewritten: %s", after.Value())
			}
			if rec, err := store.ParseRecord("alpha", after.Value()); err != nil || rec.Zone != "z1" {
				t.Fatalf("record %+v %v", rec, err)
			}
		})
	}
}
