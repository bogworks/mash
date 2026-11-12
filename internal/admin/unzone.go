package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Unzone makes the zoned endpoint name five-token again (ADR 0091) without losing stored mail, and reports whether
// anything changed. Each step converges, so a run that stopped at any step is finished by running it again:
//
//  1. the directory record is rewritten to the five-token form (no zone, the five-token mail subject), keeping every
//     other field. The inbox stream already accepts the five-token subject (a zoned inbox takes both), so mail from a
//     sender that now follows the record lands at once;
//  2. the inbox stream's subjects are updated in place (never recreated, nothing purged) to the five-token subject
//     only. Stored mail stays readable: the consumer has no subject filter.
//
// An already five-token endpoint with an already five-token stream is changed=false. The name's lock, then the
// directory-wide admission lock, are held throughout (the order of Provision), and the record is
// written against the revision it was read at. An endpoint with no record is an error; a suspended inbox is refused.
func (a *Admin) Unzone(ctx context.Context, name string) (changed bool, err error) {
	if !config.ValidName(name) {
		return false, fmt.Errorf("mash: endpoint name: 1-48 lower-case letters, digits and single hyphens (the value is not shown)")
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return false, err
	}
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return false, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "unzone", &err)
	defer end()
	adm, err := a.lockStream(ctx, admissionLockName)
	if err != nil {
		return false, err
	}
	defer adm.release(ctx)

	for try := 0; try < setMetaTries; try++ {
		e, err := kv.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return false, fmt.Errorf("mash: endpoint %s is not provisioned", name)
		}
		if err != nil {
			return false, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		rec, err := store.ParseRecord(name, e.Value())
		if err != nil {
			return false, err
		}
		if rec.Zone != "" {
			// The inbox is validated before the record changes: a failing check leaves the record as it was.
			if err := a.checkInboxForUnzoneLocked(ctx, name); err != nil {
				return false, err
			}
			// Relaxing to the five-token identity is an admission decision like any other: refuse before
			// anything changes if it could not coexist with the directory's records.
			if err := a.admit(ctx, kv, name); err != nil {
				return false, err
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(e.Value(), &obj); err != nil {
				return false, fmt.Errorf("mash: directory %s: %w", name, err)
			}
			mb, _ := json.Marshal(store.MailSubjectFor(rec.Owner, name))
			delete(obj, "zone")
			obj["mail_subject"] = mb
			val, err := json.Marshal(obj)
			if err != nil {
				return false, err
			}
			if err := lk.check(ctx); err != nil {
				return false, err
			}
			if err := adm.check(ctx); err != nil {
				return false, err
			}
			if _, err := kv.Update(ctx, name, val, e.Revision()); err != nil {
				if isRevisionConflict(err) {
					continue
				}
				return false, fmt.Errorf("mash: directory record: %w", err)
			}
			changed = true
		}
		moved, err := a.dropZoneFilterLocked(ctx, lk, name)
		if err != nil {
			return changed, err
		}
		return changed || moved, nil
	}
	return false, fmt.Errorf("mash: directory %s kept changing under unzone; try again", name)
}

// inboxForUnzoneLocked reads name's inbox stream and validates it for unzone without changing anything. It returns the
// stream's config and whether its subjects are already the five-token mail filter alone. A missing stream, a suspended
// inbox, or subjects other than the five-token filter plus zone filters of this endpoint are refused.
func (a *Admin) inboxForUnzoneLocked(ctx context.Context, name string) (jetstream.StreamConfig, bool, error) {
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		return jetstream.StreamConfig{}, false, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := s.CachedInfo().Config
	if _, ok := suspendedSubjects(name, cfg); ok {
		return cfg, false, fmt.Errorf("mash: inbox of %s is suspended; run `mashctl resume %s` before unzone", name, name)
	}
	five := sap.MailFilter(a.owner, name)
	if slices.Equal(cfg.Subjects, []string{five}) {
		return cfg, true, nil
	}
	if !slices.Contains(cfg.Subjects, five) {
		return cfg, false, firstInboxDiff("stream", name, []inboxField{{"subjects", sortedCopy(cfg.Subjects), []string{five}}})
	}
	for _, sub := range cfg.Subjects {
		if sub == five {
			continue
		}
		if _, ok := sap.LegacyZoneOfMailFilter(sub, a.owner, name); !ok {
			return cfg, false, firstInboxDiff("stream", name, []inboxField{{"subjects", sortedCopy(cfg.Subjects), []string{five}}})
		}
	}
	return cfg, false, nil
}

// checkInboxForUnzoneLocked is the read-only half of dropZoneFilterLocked: Unzone runs it before it rewrites the
// directory record, so an inbox that cannot be moved never leaves a published address that cannot receive mail.
func (a *Admin) checkInboxForUnzoneLocked(ctx context.Context, name string) error {
	_, _, err := a.inboxForUnzoneLocked(ctx, name)
	return err
}

// dropZoneFilterLocked updates name's inbox stream so its subjects are the five-token mail filter alone, changing
// nothing else. A stream with exactly that is left as it is (false); a stream whose subjects are the five-token filter
// plus zone filters of this endpoint is updated (true); a missing stream and anything else is refused.
func (a *Admin) dropZoneFilterLocked(ctx context.Context, lk *streamLock, name string) (bool, error) {
	cfg, already, err := a.inboxForUnzoneLocked(ctx, name)
	if err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	cfg.Subjects = []string{sap.MailFilter(a.owner, name)}
	if err := lk.check(ctx); err != nil {
		return false, err
	}
	if _, err := a.js.UpdateStream(ctx, cfg); err != nil {
		return false, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return true, nil
}
