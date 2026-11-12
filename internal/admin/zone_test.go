package admin_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func inboxSubjects(t *testing.T, js jetstream.JetStream, name string) []string {
	t.Helper()
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return s.CachedInfo().Config.Subjects
}

func TestProvisionZoned(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	rec, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{Zone: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Zone != "default" || rec.MailSubject != "agents.mail.cc.o.default.alpha" {
		t.Fatalf("record %+v", rec)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha", "agents.mail.*.o.default.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	// Both the zoned and the legacy subject land.
	for _, subj := range []string{"agents.mail.cc.o.default.alpha", "agents.mail.cc.o.alpha"} {
		if _, err := js.Publish(ctx, subj, []byte("x")); err != nil {
			t.Fatalf("publish %s: %v", subj, err)
		}
	}
	s, _ := js.Stream(ctx, "inbox-alpha")
	if info, _ := s.Info(ctx); info.State.Msgs != 2 {
		t.Fatalf("%d messages", info.State.Msgs)
	}
	// The strict check accepts the zoned shape; EnsureInbox reports nothing to make.
	if made, err := a.EnsureInbox(ctx, "alpha"); err != nil || len(made) != 0 {
		t.Fatalf("EnsureInbox: %v %v", made, err)
	}
	// A legacy provision is untouched.
	_, u2 := e.Mint(t, "beta")
	rec, err = a.Provision(ctx, "beta", u2, e.Account, "", admin.Meta{})
	if err != nil || rec.Zone != "" || rec.MailSubject != "agents.mail.cc.o.beta" {
		t.Fatalf("%+v %v", rec, err)
	}
	if got := inboxSubjects(t, js, "beta"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.beta"}) {
		t.Fatalf("subjects %v", got)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if ent, _ := kv.Get(ctx, "beta"); strings.Contains(string(ent.Value()), `"zone"`) {
		t.Fatalf("legacy record carries a zone: %s", ent.Value())
	}
	// A bad zone is refused before anything is created.
	_, u3 := e.Mint(t, "gamma")
	for _, bad := range []string{"A", "a.b", "a*", "a b", strings.Repeat("x", 33)} {
		if _, err := a.Provision(ctx, "gamma", u3, e.Account, "", admin.Meta{Zone: bad}); err == nil {
			t.Errorf("zone %q accepted", bad)
		}
	}
	if _, err := js.Stream(ctx, "inbox-gamma"); err == nil {
		t.Fatal("a refused provision created a stream")
	}
}

func TestEnsureInboxStillStrictOnOtherDrift(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{Zone: "z1"}); err != nil {
		t.Fatal(err)
	}
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	// Another subject set is refused, naming the field.
	for _, subjects := range [][]string{
		{"agents.mail.*.o.*.alpha"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha", "agents.mail.*.o.extra"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.*.alpha"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.z2.alpha"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha", "agents.mail.*.o.z2.alpha"},
		{"agents.mail.*.o.alpha"},
	} {
		c := cfg
		c.Subjects = subjects
		if _, err := js.UpdateStream(ctx, c); err != nil {
			t.Fatal(err)
		}
		if _, err := a.EnsureInbox(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "subjects") {
			t.Errorf("subjects %v: %v", subjects, err)
		}
	}
	// Restored, then a different field drifts: still refused.
	c := cfg
	c.MaxMsgs = 5
	if _, err := js.UpdateStream(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "max_msgs") {
		t.Fatalf("max_msgs drift: %v", err)
	}
}

// A zoned record promises its zoned subject, so the stream must carry both subjects; a legacy record accepts either shape.
func TestEnsureInboxRequiresDualSubjectsForAZonedRecord(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	// The record becomes zoned while the stream keeps only the legacy subject.
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, err := kv.Get(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ent.Value(), &obj); err != nil {
		t.Fatal(err)
	}
	obj["zone"] = json.RawMessage(`"z1"`)
	obj["mail_subject"] = json.RawMessage(`"agents.mail.cc.o.z1.alpha"`)
	raw, _ := json.Marshal(obj)
	if _, err := kv.Update(ctx, "alpha", raw, ent.Revision()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Fatalf("zoned record, legacy-only stream: %v", err)
	}
	// Adding the zone filter makes it pass.
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err != nil {
		t.Fatalf("zoned record, dual stream: %v", err)
	}
	// A legacy record with a dual-subject stream (an unzone in progress) still passes.
	_, u2 := e.Mint(t, "beta")
	if _, err := a.Provision(ctx, "beta", u2, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	s, _ = js.Stream(ctx, "inbox-beta")
	cfg = s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*.o.beta", "agents.mail.*.o.z9.beta"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "beta"); err != nil {
		t.Fatalf("legacy record, dual stream: %v", err)
	}
}

// A failed directory read must not be mistaken for "no zone": repairing a zoned endpoint's missing stream from an
// unreadable record would recreate it with the legacy subject only.
func TestEnsureInboxReportsADirectoryErrorInsteadOfAssumingLegacy(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{Zone: "z1"}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, err := kv.Get(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	good := append([]byte(nil), ent.Value()...)
	if err := js.DeleteStream(ctx, "inbox-alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "alpha", []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err == nil {
		t.Fatal("EnsureInbox accepted an unreadable directory record")
	}
	if _, err := js.Stream(ctx, "inbox-alpha"); err == nil {
		t.Fatal("the repair recreated a stream from an unreadable record")
	}
	// With the record restored, the repair brings back both subjects.
	if _, err := kv.Put(ctx, "alpha", good); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, "alpha"); err != nil {
		t.Fatalf("EnsureInbox after restoring the record: %v", err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	// No record at all is not an error: the legacy stream is made.
	if _, err := a.EnsureInbox(ctx, "ghost"); err != nil {
		t.Fatalf("EnsureInbox without a record: %v", err)
	}
	if got := inboxSubjects(t, js, "ghost"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.ghost"}) {
		t.Fatalf("subjects %v", got)
	}
}

// A zoned endpoint's stream takes its own zone and the legacy subject, never another zone's: a sender gated into
// zone za must not reach an endpoint in zone zb by addressing it as <za>.<name>.
func TestZonedInboxRejectsMailAddressedToAnotherZone(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "b")
	if _, err := a.Provision(ctx, "b", user, e.Account, "", admin.Meta{Zone: "zb"}); err != nil {
		t.Fatal(err)
	}
	s, _ := js.Stream(ctx, "inbox-b")
	msgs := func() uint64 {
		info, err := s.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return info.State.Msgs
	}
	if _, err := js.Publish(ctx, "agents.mail.cc.o.za.b", []byte("forged")); err == nil {
		t.Fatal("mail addressed to the wrong zone was stored")
	}
	if n := msgs(); n != 0 {
		t.Fatalf("%d messages after a wrong-zone publish", n)
	}
	for _, subj := range []string{"agents.mail.cc.o.zb.b", "agents.mail.cc.o.b"} {
		if _, err := js.Publish(ctx, subj, []byte("ok")); err != nil {
			t.Fatalf("publish %s: %v", subj, err)
		}
	}
	if n := msgs(); n != 2 {
		t.Fatalf("%d messages, want 2", n)
	}
}
