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
	target, legacy, found, err := a.ServingIdentity(ctx, id)
	if err != nil || !found {
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

// ServingIdentity resolves id to the directory record that serves its mail, and reads nothing else: the identity's own
// record (the directory key id.Flat() holding id), or, for a new-form id with none, the legacy record of the same owner
// and name (sap.Legacy), whose identity is target with legacy true. found is false when no record serves id. A record
// that cannot be read is an error. A legacy name is not unique across owners, so a caller that compares the result with
// a record must compare the owner as well as the name.
func (a *Admin) ServingIdentity(ctx context.Context, id sap.EndpointIdentity) (target sap.EndpointIdentity, legacy, found bool, err error) {
	if err := id.Validate(); err != nil {
		return sap.EndpointIdentity{}, false, false, fmt.Errorf("mash: serving identity: %w", err)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return sap.EndpointIdentity{}, false, false, err
	}
	ok, err := a.recordServes(ctx, kv, id.Flat(), id)
	if err != nil {
		return sap.EndpointIdentity{}, false, false, err
	}
	if ok {
		return id, false, true, nil
	}
	if id.IsLegacy() {
		return sap.EndpointIdentity{}, false, false, nil
	}
	// No record of its own: the legacy endpoint of the same owner and name receives its mail.
	lid, err := sap.Legacy(id.Owner, id.Name)
	if err != nil {
		return sap.EndpointIdentity{}, false, false, nil // the address has no legacy form, so no legacy endpoint
	}
	ok, err = a.recordServes(ctx, kv, lid.Flat(), lid)
	if err != nil || !ok {
		return sap.EndpointIdentity{}, false, false, err
	}
	return lid, true, true, nil
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

// LegacyOwner reads the directory record keyed name and reports its owner, and whether it is a legacy record (a record
// that names the legacy identity of its owner and name, not a new-form one). found is false when there is no such
// record. A record that cannot be read or parsed is an error. It is the directory read behind a legacy endpoint tag
// (ADR 0138): the issuer issues the tag of a legacy name only for the owner this returns.
func (a *Admin) LegacyOwner(ctx context.Context, name string) (owner string, legacy, found bool, err error) {
	kv, err := a.dir(ctx)
	if err != nil {
		return "", false, false, err
	}
	e, err := kv.Get(ctx, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("mash: directory %s: %w", config.Clean(name), err)
	}
	rec, err := store.ParseRecord(name, e.Value())
	if err != nil {
		return "", false, false, fmt.Errorf("mash: directory record %s is not readable: %v", config.Clean(name), err)
	}
	id, err := rec.Identity()
	if err != nil {
		return "", false, false, fmt.Errorf("mash: directory record %s: %v", config.Clean(name), err)
	}
	return rec.Owner, id.IsLegacy(), true, nil
}
