package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// InboxCheck is what CheckInbox found for an address.
type InboxCheck struct {
	// Stream is the inbox stream that receives the address's mail; empty when the address has no directory record or
	// its record has no stream.
	Stream string
	// Consumer is whether the stream has the durable consumer the mail reader uses.
	Consumer bool
	// Suspended is whether the inbox is parked (ADR 0057): its stream receives no mail until it is resumed. A
	// suspended inbox is still the endpoint's own, so its saved configuration is checked like any other.
	Suspended bool
}

// Exists is whether mail to the address has a stream and a consumer to land in.
func (c InboxCheck) Exists() bool { return c.Stream != "" && c.Consumer }

// CheckInbox reads whether addr has the inbox mail depends on, and creates nothing and writes no record. The address
// is served by its own inbox, inbox-<flat>, when the directory holds a record under its flat key. A stream or consumer
// that exists but differs from the configuration mail depends on is an error naming the field, the same check
// EnsureInbox makes; nothing is changed.
func (a *Admin) CheckInbox(ctx context.Context, addr sap.Address) (InboxCheck, error) {
	if err := addr.Validate(); err != nil {
		return InboxCheck{}, fmt.Errorf("mash: check inbox: %w", err)
	}
	found, err := a.HasRecord(ctx, addr)
	if err != nil || !found {
		return InboxCheck{}, err
	}
	flat := addr.Flat()
	s, err := a.js.Stream(ctx, sap.InboxStream(flat))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return InboxCheck{}, nil
	}
	if err != nil {
		return InboxCheck{}, fmt.Errorf("mash: inbox stream: %w", err)
	}
	want := store.InboxConfigFor(addr, a.limits)
	got := s.CachedInfo().Config
	saved, suspended := suspendedSubjects(flat, got)
	if suspended { // a suspended inbox is still the endpoint's own
		got.Subjects = saved
	}
	if err := checkInboxStream(flat, got, want); err != nil {
		return InboxCheck{}, err
	}
	out := InboxCheck{Stream: sap.InboxStream(flat), Suspended: suspended}
	c, err := s.Consumer(ctx, store.Consumer)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
		return out, nil
	case errors.Is(err, jetstream.ErrNotPullConsumer):
		return InboxCheck{}, firstInboxDiff("consumer", flat, []inboxField{{"deliver_subject", "(set)", "(empty: a pull consumer)"}})
	case err != nil:
		return InboxCheck{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	if err := checkInboxConsumer(flat, c.CachedInfo().Config, store.ConsumerConfig()); err != nil {
		return InboxCheck{}, err
	}
	out.Consumer = true
	return out, nil
}

// HasRecord reports whether the directory holds a readable record under addr's flat key that is addr itself, and
// reads nothing else. A record that cannot be read is an error.
func (a *Admin) HasRecord(ctx context.Context, addr sap.Address) (bool, error) {
	if err := addr.Validate(); err != nil {
		return false, fmt.Errorf("mash: has record: %w", err)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return false, err
	}
	key := addr.Flat()
	e, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mash: directory %s: %w", config.Clean(key), err)
	}
	rec, err := store.ParseRecord(key, e.Value())
	if err != nil {
		return false, fmt.Errorf("mash: directory record %s is not readable: %v", config.Clean(key), err)
	}
	rid, err := rec.Address()
	if err != nil {
		return false, fmt.Errorf("mash: directory record %s: %v", config.Clean(key), err)
	}
	return rid == addr, nil
}
