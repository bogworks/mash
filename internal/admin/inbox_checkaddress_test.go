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
	id := newID(t, "acme", e.Owner, "ops1")

	// Nothing provisioned: no stream, no consumer, no error, and nothing created.
	keys, streams := dirKeys(t, js), streamNames(t, js)
	got, err := a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "" || got.Consumer {
		t.Fatalf("empty server: %+v %v", got, err)
	}
	untouched(t, js, keys, streams)

	// An endpoint made the way `mashctl add` makes one is a legacy one: its inbox is inbox-<name>, filtered on the
	// five-token subject of its owner and name, which receives mail for every agent token.
	provLegacy(t, e, a, "ops1", admin.Meta{})
	keys, streams = dirKeys(t, js), streamNames(t, js)
	got, err = a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "inbox-ops1" || !got.Consumer || !got.Legacy {
		t.Fatalf("legacy endpoint: %+v %v", got, err)
	}
	untouched(t, js, keys, streams)

	// Another owner's address is not served by it.
	got, err = a.CheckInbox(ctx, newID(t, "acme", "other", "ops1"))
	if err != nil || got.Stream != "" {
		t.Fatalf("another owner: %+v %v", got, err)
	}

	// A new-form endpoint's own inbox is inbox-<flat>.
	_, user := e.Mint(t, "ops2")
	id2 := newID(t, "acme", "team1", "ops2")
	if _, err := a.ProvisionIdentity(ctx, id2, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	got, err = a.CheckInbox(ctx, id2)
	if err != nil || got.Stream != "inbox-acme_team1_ops2" || !got.Consumer || got.Legacy {
		t.Fatalf("new-form endpoint: %+v %v", got, err)
	}
}

func TestCheckInboxReportsAMissingConsumerAndAChangedStream(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provLegacy(t, e, a, "ops1", admin.Meta{})
	id := newID(t, "acme", e.Owner, "ops1")
	if err := js.DeleteConsumer(ctx, "inbox-ops1", store.Consumer); err != nil {
		t.Fatal(err)
	}
	got, err := a.CheckInbox(ctx, id)
	if err != nil || got.Stream != "inbox-ops1" || got.Consumer {
		t.Fatalf("missing consumer: %+v %v", got, err)
	}

	// A stream that differs in a field mail depends on is an error naming the field; nothing is changed.
	s, err := js.Stream(ctx, "inbox-ops1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	cfg.Subjects = []string{"agents.mail.*." + e.Owner + ".elsewhere"}
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CheckInbox(ctx, id); err == nil || !strings.Contains(err.Error(), "subjects") {
		t.Fatalf("changed stream: %v", err)
	}
	if _, err := js.Consumer(ctx, "inbox-ops1", store.Consumer); err != jetstream.ErrConsumerNotFound {
		t.Fatalf("the check created a consumer: %v", err)
	}
}
