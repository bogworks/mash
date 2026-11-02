package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// SuspendState is whether an endpoint's inbound mail is parked (ADR 0057), when, and where its inbox stands. Msgs and
// LastSeq are the stream's, read fresh; while the state holds, LastSeq is final.
type SuspendState struct {
	Suspended bool
	Since     string // RFC 3339 UTC; empty when not suspended
	ID        string // names this one suspension; empty when not suspended or when parked by hand without one
	Msgs      uint64
	LastSeq   uint64
}

// suspendedSubjects returns the subjects a suspended inbox stream had before they were parked. A stream is suspended
// when its metadata holds the saved subjects and its own subjects are exactly the parked one; the parked subject
// without the metadata, or the metadata without the parked subject, is not suspended.
func suspendedSubjects(name string, cfg jetstream.StreamConfig) ([]string, bool) {
	raw, ok := cfg.Metadata[store.MetaSuspendedSubjects]
	if !ok || !slices.Equal(cfg.Subjects, []string{store.SuspendedSubject(name)}) {
		return nil, false
	}
	var saved []string
	if err := json.Unmarshal([]byte(raw), &saved); err != nil || len(saved) == 0 {
		return nil, false
	}
	return saved, true
}

// inboxForSuspend reads name's inbox stream.
func (a *Admin) inboxForSuspend(ctx context.Context, name string) (jetstream.Stream, error) {
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("mash: endpoint %s has no inbox stream", name)
	} else if err != nil {
		return nil, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return s, nil
}

// suspendState reads the stream's state fresh from the server, never from a cached copy.
func suspendState(ctx context.Context, name string, s jetstream.Stream) (SuspendState, error) {
	info, err := s.Info(ctx)
	if err != nil {
		return SuspendState{}, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	st := SuspendState{Msgs: info.State.Msgs, LastSeq: info.State.LastSeq}
	if _, ok := suspendedSubjects(name, info.Config); ok {
		st.Suspended, st.Since, st.ID = true, info.Config.Metadata[store.MetaSuspendedAt], info.Config.Metadata[store.MetaSuspendedID]
	}
	return st, nil
}

// Suspend parks name's inbox stream: it saves the stream's subjects in its metadata and replaces them with the one
// subject mash.suspended.<name>, so a sender's publish finds no stream. Only the subjects and metadata change and
// nothing is purged. It also stores a fresh random suspension id. It is idempotent: a suspended inbox is reported as it is, with its existing id. The state's LastSeq is read after the
// update.
func (a *Admin) Suspend(ctx context.Context, name string) (_ SuspendState, err error) {
	if !config.ValidName(name) {
		return SuspendState{}, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return SuspendState{}, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "suspend", &err)
	defer end()
	s, err := a.inboxForSuspend(ctx, name)
	if err != nil {
		return SuspendState{}, err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return SuspendState{}, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := info.Config
	if _, ok := suspendedSubjects(name, cfg); ok {
		return suspendState(ctx, name, s)
	}
	if slices.Equal(cfg.Subjects, []string{store.SuspendedSubject(name)}) {
		return SuspendState{}, fmt.Errorf("mash: inbox of %s is parked but its saved subjects are missing, so suspend cannot tell what to restore; restore them by hand or deprovision", name)
	}
	// The record is read only to remember whose it was; a missing directory or record leaves the user empty.
	savedUser := ""
	if kv, err := a.dir(ctx); err == nil {
		if e, err := kv.Get(ctx, name); err == nil {
			if rec, err := store.ParseRecord(name, e.Value()); err == nil {
				savedUser = rec.User
			}
		}
	}
	saved, _ := json.Marshal(cfg.Subjects)
	meta := maps.Clone(cfg.Metadata)
	if meta == nil {
		meta = map[string]string{}
	}
	meta[store.MetaSuspendedSubjects] = string(saved)
	meta[store.MetaSuspendedAt] = time.Now().UTC().Format(time.RFC3339)
	var idb [16]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return SuspendState{}, fmt.Errorf("mash: suspend %s: suspension id: %w", name, err)
	}
	meta[store.MetaSuspendedID] = hex.EncodeToString(idb[:])
	meta[store.MetaSuspendedUser] = savedUser
	cfg.Subjects, cfg.Metadata = []string{store.SuspendedSubject(name)}, meta
	if err := lk.check(ctx); err != nil {
		return SuspendState{}, err
	}
	if s, err = a.js.UpdateStream(ctx, cfg); err != nil {
		return SuspendState{}, fmt.Errorf("mash: suspend %s: %w", name, err)
	}
	return suspendState(ctx, name, s)
}

// Resume restores the subjects Suspend saved and clears its metadata. It is idempotent: an inbox that is not
// suspended is left as it is. Stale metadata on live subjects is cleared and the subjects kept; parked subjects with
// no usable saved list are an error, never a guess.
func (a *Admin) Resume(ctx context.Context, name string) (err error) {
	if !config.ValidName(name) {
		return fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "resume", &err)
	defer end()
	s, err := a.inboxForSuspend(ctx, name)
	if err != nil {
		return err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := info.Config
	saved, ok := suspendedSubjects(name, cfg)
	_, hasMeta := cfg.Metadata[store.MetaSuspendedSubjects]
	if _, hasID := cfg.Metadata[store.MetaSuspendedID]; hasID {
		hasMeta = true
	}
	parked := slices.Equal(cfg.Subjects, []string{store.SuspendedSubject(name)})
	switch {
	case !ok && parked:
		// Parked with no usable saved list: the original subjects are unknown, and guessing could misroute mail.
		return fmt.Errorf("mash: resume %s: inbox subjects are parked but the saved subjects are missing or unusable; "+
			"the original subjects are unknown, so restore them by hand or deprovision the endpoint", name)
	case !ok && !hasMeta:
		return nil
	}
	meta := maps.Clone(cfg.Metadata)
	delete(meta, store.MetaSuspendedSubjects)
	delete(meta, store.MetaSuspendedAt)
	delete(meta, store.MetaSuspendedID)
	delete(meta, store.MetaSuspendedUser)
	if len(meta) == 0 {
		meta = nil
	}
	if ok {
		cfg.Subjects = saved
	} // else metadata is stale on live subjects: clear it and leave the subjects as they are
	cfg.Metadata = meta
	if err := lk.check(ctx); err != nil {
		return err
	}
	if _, err := a.js.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("mash: resume %s: %w", name, err)
	}
	return nil
}

// Suspended reads whether name's inbox is suspended, since when, and the stream's message count and last sequence.
func (a *Admin) Suspended(ctx context.Context, name string) (SuspendState, error) {
	if !config.ValidName(name) {
		return SuspendState{}, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	s, err := a.inboxForSuspend(ctx, name)
	if err != nil {
		return SuspendState{}, err
	}
	return suspendState(ctx, name, s)
}
