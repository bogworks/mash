package admin_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// inboxSubjects are the subjects of an endpoint's inbox stream. name is the instance of ep(t, name), or a flat key.
func inboxSubjects(t *testing.T, js jetstream.JetStream, name string) []string {
	t.Helper()
	if !strings.Contains(name, "_") {
		name = "o_dev_" + name
	}
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	return s.CachedInfo().Config.Subjects
}

func TestProvisionInboxTakesOnlyTheMailSubject(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := ep(t, "alpha")
	_, user := e.Mint(t, id)
	rec, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{})
	if err != nil || rec.MailSubject != "agents.mail.o.dev.alpha" {
		t.Fatalf("%+v %v", rec, err)
	}
	if got := inboxSubjects(t, js, "alpha"); !reflect.DeepEqual(got, []string{"agents.mail.o.dev.alpha"}) {
		t.Fatalf("subjects %v", got)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Get(ctx, "o_dev_alpha"); err != nil {
		t.Fatalf("directory record under the flat key: %v", err)
	}
	// Another instance's subject is not this inbox's mail.
	if _, err := js.Publish(ctx, "agents.mail.o.dev.other", []byte("x")); err == nil {
		t.Fatal("another instance's subject landed in the inbox")
	}
	if _, err := js.Publish(ctx, "agents.mail.o.dev.alpha", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if made, err := a.EnsureInbox(ctx, id); err != nil || len(made) != 0 {
		t.Fatalf("EnsureInbox: %v %v", made, err)
	}
}

func TestEnsureInboxStillStrictOnOtherDrift(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := ep(t, "alpha")
	_, user := e.Mint(t, id)
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	s, _ := js.Stream(ctx, "inbox-o_dev_alpha")
	cfg := s.CachedInfo().Config
	// Another subject set is refused, naming the field.
	for _, subjects := range [][]string{
		{"agents.mail.o.dev.*"},
		{"agents.mail.o.dev.alpha", "agents.mail.o.dev.beta"},
		{"agents.mail.o.dev.alpha", "agents.mail.o.other.alpha"},
		{"agents.mail.o.dev.beta"},
	} {
		c := cfg
		c.Subjects = subjects
		if _, err := js.UpdateStream(ctx, c); err != nil {
			t.Fatal(err)
		}
		if _, err := a.EnsureInbox(ctx, id); err == nil || !strings.Contains(err.Error(), "subjects") {
			t.Errorf("subjects %v: %v", subjects, err)
		}
	}
	// Restored, then a different field drifts: still refused.
	c := cfg
	c.MaxMsgs = 5
	if _, err := js.UpdateStream(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := a.EnsureInbox(ctx, id); err == nil || !strings.Contains(err.Error(), "max_msgs") {
		t.Fatalf("max_msgs drift: %v", err)
	}
}
