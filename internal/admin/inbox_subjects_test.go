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
	"github.com/bogworks/mash/internal/testenv"
)

func inboxSubjects(t *testing.T, js jetstream.JetStream, name string) []string {
	t.Helper()
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return s.CachedInfo().Config.Subjects
}

// provisionZoned makes the endpoint name as a zoned release left it: the record carries the zone and the six-token mail
// subject, and the inbox stream takes both the five-token filter and the zone's. Nothing in mash builds such a record
// any more (ADR 0091), so the test writes it by hand, as the guards and unzone must read it.
func provisionZoned(t *testing.T, e *testenv.Env, a *admin.Admin, js jetstream.JetStream, name, zone string, meta admin.Meta) (user string) {
	t.Helper()
	ctx := context.Background()
	_, user = e.Mint(t, name)
	if _, err := a.Provision(ctx, name, user, e.Account, "n", meta); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	ent, err := kv.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ent.Value(), &obj); err != nil {
		t.Fatal(err)
	}
	obj["zone"] = json.RawMessage(`"` + zone + `"`)
	obj["mail_subject"] = json.RawMessage(`"agents.mail.cc.o.` + zone + `.` + name + `"`)
	raw, _ := json.Marshal(obj)
	if _, err := kv.Update(ctx, name, raw, ent.Revision()); err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(ctx, "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*.o." + name, "agents.mail.*.o." + zone + "." + name}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestProvisionIsFiveToken(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	rec, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{})
	if err != nil || rec.Zone != "" || rec.MailSubject != "agents.mail.cc.o.alpha" {
		t.Fatalf("%+v %v", rec, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.*.o.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if ent, _ := kv.Get(ctx, "alpha"); strings.Contains(string(ent.Value()), `"zone"`) {
		t.Fatalf("record carries a zone: %s", ent.Value())
	}
	// A six-token subject is no longer mail: the stream does not take it.
	if _, err := js.Publish(ctx, "agents.mail.cc.o.z1.alpha", []byte("x")); err == nil {
		t.Fatal("a six-token subject landed in the inbox")
	}
	if _, err := js.Publish(ctx, "agents.mail.cc.o.alpha", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if made, err := a.EnsureInbox(ctx, "alpha"); err != nil || len(made) != 0 {
		t.Fatalf("EnsureInbox: %v %v", made, err)
	}
}

func TestEnsureInboxStillStrictOnOtherDrift(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, user := e.Mint(t, "alpha")
	if _, err := a.Provision(ctx, "alpha", user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	s, _ := js.Stream(ctx, "inbox-alpha")
	cfg := s.CachedInfo().Config
	// Another subject set is refused, naming the field; a zone filter is one of them.
	for _, subjects := range [][]string{
		{"agents.mail.*.o.*.alpha"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.z1.alpha"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.extra"},
		{"agents.mail.*.o.alpha", "agents.mail.*.o.*.alpha"},
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
