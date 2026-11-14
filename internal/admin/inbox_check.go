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
	// Legacy is whether the inbox is the one of a legacy endpoint (inbox-<name>, made by `mashctl add`) whose filter
	// agents.mail.*.<owner>.<name> also matches the address's new-form subject.
	Legacy bool
	// Suspended is whether the inbox is parked (ADR 0057): its stream receives no mail until it is resumed. A
	// suspended inbox is still the endpoint's own, so its saved configuration is checked like any other.
	Suspended bool
}

// Exists is whether mail to the address has a stream and a consumer to land in.
func (c InboxCheck) Exists() bool { return c.Stream != "" && c.Consumer }

// CheckInbox reads whether id has the inbox mail depends on, and creates nothing and writes no record. The address is
// served by its own inbox, inbox-<flat> with a directory record under the flat key, or, when it has none, by the inbox
// of the legacy endpoint of the same owner and name (`mashctl add` makes those): ADR 0074 refuses to admit both,
// because the legacy filter also matches the new-form subject. A stream or consumer that exists but differs from the
// configuration mail depends on is an error naming the field, the same check EnsureInbox makes; nothing is changed.
func (a *Admin) CheckInbox(ctx context.Context, id sap.EndpointIdentity) (InboxCheck, error) {
	if err := id.Validate(); err != nil {
		return InboxCheck{}, fmt.Errorf("mash: check inbox: %w", err)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return InboxCheck{}, err
	}
	target := id
	legacy := false
	if !id.IsLegacy() {
		ok, err := a.recordServes(ctx, kv, id.Flat(), id)
		if err != nil {
			return InboxCheck{}, err
		}
		if !ok {
			// No record of its own: the legacy endpoint of the same owner and name receives its mail.
			lid, err := sap.Legacy(id.Owner, id.Name)
			if err != nil {
				return InboxCheck{}, nil // the address has no legacy form, so no legacy endpoint
			}
			ok, err := a.recordServes(ctx, kv, lid.Flat(), lid)
			if err != nil {
				return InboxCheck{}, err
			}
			if !ok {
				return InboxCheck{}, nil
			}
			target, legacy = lid, true
		}
	} else if ok, err := a.recordServes(ctx, kv, id.Flat(), id); err != nil || !ok {
		return InboxCheck{}, err
	}
	flat := target.Flat()
	s, err := a.js.Stream(ctx, sap.InboxStream(flat))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return InboxCheck{Legacy: legacy}, nil
	}
	if err != nil {
		return InboxCheck{}, fmt.Errorf("mash: inbox stream: %w", err)
	}
	want := store.InboxConfigIdentity(target, a.limits)
	got := s.CachedInfo().Config
	saved, suspended := suspendedSubjects(flat, got)
	if suspended { // a suspended inbox is still the endpoint's own
		got.Subjects = saved
	}
	if err := checkInboxStream(flat, got, want); err != nil {
		return InboxCheck{}, err
	}
	out := InboxCheck{Stream: sap.InboxStream(flat), Legacy: legacy, Suspended: suspended}
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

// recordServes reports whether the directory holds a record under key that is the identity id.
func (a *Admin) recordServes(ctx context.Context, kv jetstream.KeyValue, key string, id sap.EndpointIdentity) (bool, error) {
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
	rid, err := rec.Identity()
	if err != nil {
		return false, fmt.Errorf("mash: directory record %s: %v", config.Clean(key), err)
	}
	return rid == id, nil
}
