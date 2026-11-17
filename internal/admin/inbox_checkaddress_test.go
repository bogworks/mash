package admin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// CheckInbox says whether an address has the inbox mail depends on, and writes nothing, whichever answer it gives.
func TestCheckInboxFindsAnInboxAndCreatesNothing(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := ep(t, "ops1")

	// Nothing provisioned: no stream, no consumer, no error, and nothing created.
	keys, streams := dirKeys(t, js), streamNames(t, js)
	got, err := a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "" || got.Consumer {
		t.Fatalf("empty server: %+v %v", got, err)
	}
	untouched(t, js, keys, streams)

	// A provisioned endpoint's inbox is inbox-<flat>.
	_, user := e.Mint(t, id)
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	keys, streams = dirKeys(t, js), streamNames(t, js)
	got, err = a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "inbox-o_dev_ops1" || !got.Consumer {
		t.Fatalf("endpoint: %+v %v", got, err)
	}
	untouched(t, js, keys, streams)

	// Another owner's address is not served by it.
	got, err = a.CheckInbox(ctx, epIn(t, "other", "dev", "ops1"))
	if err != nil || got.Stream != "" {
		t.Fatalf("another owner: %+v %v", got, err)
	}
}

func TestCheckInboxReportsAMissingConsumerAndAChangedStream(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	id := ep(t, "ops1")
	_, user := e.Mint(t, id)
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteConsumer(ctx, "inbox-o_dev_ops1", store.Consumer); err != nil {
		t.Fatal(err)
	}
	got, err := a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "inbox-o_dev_ops1" || got.Consumer {
		t.Fatalf("missing consumer: %+v %v", got, err)
	}

	// A stream that differs in a field mail depends on is an error naming the field; nothing is changed.
	s, err := js.Stream(ctx, "inbox-o_dev_ops1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail." + e.Owner + ".dev.elsewhere"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CheckInbox(ctx, id); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Fatalf("changed stream: %v", err)
	}
	if _, err := js.Consumer(ctx, "inbox-o_dev_ops1", store.Consumer); err != jetstream.ErrConsumerNotFound {
		t.Fatalf("the check created a consumer: %v", err)
	}
}

// A suspended inbox receives no mail, so the check reports it as suspended rather than as the endpoint's usable inbox.
func TestCheckInboxReportsASuspendedInbox(t *testing.T) {
	e, a, _ := setup(t)
	ctx := context.Background()
	id := ep(t, "ops3")
	_, user := e.Mint(t, id)
	if _, err := a.Provision(ctx, id, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	got, err := a.CheckInbox(ctx, id)
	if err != nil || got.Suspended || !got.Exists() {
		t.Fatalf("before: %+v %v", got, err)
	}
	if _, err := a.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err = a.CheckInbox(ctx, id)
	if err != nil || !got.Suspended || got.Stream != "inbox-o_dev_ops3" {
		t.Fatalf("suspended: %+v %v", got, err)
	}
}
