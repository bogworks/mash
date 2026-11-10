package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// VerifyAlias is the one check of a new-form record's legacy alias (ADR 0076, "Validation at provisioning"). The
// permission set WindowBridge builds trusts its alias, so nothing else guards the grants that name another endpoint's
// exact prompt, status and heartbeat subjects: provisioning and any permission assembly must call this first.
//
// existing is the directory's records. It returns the alias as a legacy identity, or an error when
//   - id is not a valid new-form identity;
//   - the alias owner or name fails the legacy name rule (sap.Legacy);
//   - the alias owner equals id's owner (the legacy wildcard filter would reach id), or the alias overlaps id;
//   - no unzoned legacy record has exactly that owner and name (a zoned one is named in the refusal);
//   - another new-form record, one not keyed by id's own flat key, already claims the alias.
//
// The record under id's own flat key is id itself, so verifying an already provisioned record passes.
func VerifyAlias(id sap.EndpointIdentity, alias store.Alias, existing []store.Record) (sap.EndpointIdentity, error) {
	if err := id.Validate(); err != nil {
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias: identity: %w", err)
	}
	if id.IsLegacy() {
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias: %s must be a new-form identity: a legacy identity cannot carry an alias", id)
	}
	leg, err := sap.Legacy(alias.Owner, alias.Name)
	if err != nil {
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: %w", err)
	}
	if alias.Owner == id.Owner {
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: alias owner %q equals the owner of %s: the legacy wildcard filter would reach it", alias.Owner, id)
	}
	if sap.Overlaps(id, leg) {
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: %s overlaps the alias %s/%s", id, alias.Owner, alias.Name)
	}
	named, zoned := false, false
	for _, r := range existing {
		if r.Owner == alias.Owner && r.Name == alias.Name && r.Key() == r.Name { // a legacy-keyed record
			if r.Zone == "" {
				named = true
			} else {
				zoned = true
			}
		}
		if r.Alias != nil && *r.Alias == alias && r.KeyOrName() != id.Flat() {
			return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: %s/%s is already claimed by %s", alias.Owner, alias.Name, r.KeyOrName())
		}
	}
	switch {
	case named:
		return leg, nil
	case zoned:
		return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: the legacy endpoint %s/%s is zoned (zone-bound subjects cannot be proved disjoint)", alias.Owner, alias.Name)
	}
	return sap.EndpointIdentity{}, fmt.Errorf("mash: alias refused: no legacy endpoint %s/%s in the directory", alias.Owner, alias.Name)
}

// directoryRecords reads every directory record. Fail closed: any key that cannot be read or modelled is an error,
// because an unmodellable record could hide an overlap. The caller holds the admission lock.
func (a *Admin) directoryRecords(ctx context.Context, kv jetstream.KeyValue) ([]store.Record, error) {
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}
	slices.Sort(keys)
	var recs []store.Record
	for _, k := range keys {
		if afterDirectoryGet != nil {
			afterDirectoryGet(k)
		}
		r, err := store.GetRecord(ctx, kv, k)
		if err != nil {
			return nil, fmt.Errorf("unmodellable record %s in the directory: %w", k, err)
		}
		recs = append(recs, r)
	}
	return recs, nil
}

// ProvisionIdentity provisions the new-form endpoint id (ADR 0074): the directory record keyed by id's flat identity,
// the inbox stream inbox-<flat> filtered on id's exact mail subject, and the durable consumer. alias, when set, is
// the legacy identity the endpoint answers to during the transition window (ADR 0076); it is verified by VerifyAlias
// before anything is written. A new-form identity is never zoned, so meta.Zone must be empty.
//
// Discipline is Provision's: the flat key's lock, then the directory-wide admission lock (a leaf), held while the
// directory is listed, admitted against and written, with both re-checked before the record. The record is written
// last, so a provision that stopped part-way is finished by running the same call again; a finished one is refused as
// already provisioned. A retired new-form identity is refused (INV-002): the new-form deprovision writes a durable
// marker into the no-TTL bucket store.RetiredBucket (key = flat identity; value: store.Retired, its tokens, the retired
// user public key and the time, never a seed) before it removes the record, stream or lease, and this call checks it
// under the admission lock. Nothing in mash deletes a marker and there is no release path: releasing one would need a
// contract ruling and an audit record. A legacy name is not marked (Provision clears its tombstone on purpose), so it
// stays reusable.
func (a *Admin) ProvisionIdentity(ctx context.Context, id sap.EndpointIdentity, alias *store.Alias, user, account, note string, meta Meta) (rec store.Record, err error) {
	if err := id.Validate(); err != nil {
		return store.Record{}, fmt.Errorf("mash: provision identity: %w", err)
	}
	if id.IsLegacy() {
		return store.Record{}, fmt.Errorf("mash: provision identity: %s is a legacy identity: use Provision", id)
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return store.Record{}, errors.New("mash: the user is not a user public key (U, then 55 base32 characters; the value is not shown)")
	}
	if !nkeys.IsValidPublicAccountKey(account) {
		return store.Record{}, errors.New("mash: the account is not an account public key (A, then 55 base32 characters; the value is not shown)")
	}
	if meta.Zone != "" {
		return store.Record{}, fmt.Errorf("mash: provision identity: %s is a new-form identity and takes no zone", id)
	}
	if err := meta.validate(); err != nil {
		return store.Record{}, err
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	flat := id.Flat()
	lk, err := a.lockStream(ctx, flat)
	if err != nil {
		return store.Record{}, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "provision", &err)
	defer end()
	var aliasLock *streamLock
	// Lock order: the new identity's flat-key lock, then the alias legacy name's lock, then the admission lock (a
	// leaf). The alias name's lock is the one legacy Deprovision and Provision take for that name, so while it is held
	// the alias's legacy record cannot be removed or replaced between verification and the record's creation.
	if alias != nil {
		if _, err := sap.Legacy(alias.Owner, alias.Name); err != nil {
			return store.Record{}, fmt.Errorf("mash: alias refused: %w", err)
		}
		alk, err := a.lockStream(ctx, alias.Name)
		if err != nil {
			return store.Record{}, err
		}
		defer alk.release(ctx)
		var cancel context.CancelFunc
		ctx, cancel = alk.bound(ctx)
		defer cancel()
		aliasLock = alk
	}
	adm, err := a.lockStream(ctx, admissionLockName)
	if err != nil {
		return store.Record{}, err
	}
	defer adm.release(ctx)
	if afterAdmissionLock != nil {
		afterAdmissionLock()
	}
	// INV-002: a retired new-form identity is never reassigned. The marker is durable (no TTL) and mash never deletes
	// it. Checked with the flat-key and admission locks held, before anything is written; an unreadable marker
	// refuses (fail closed).
	retired, err := a.retiredKV(ctx)
	if err != nil {
		return store.Record{}, err
	}
	switch e, err := retired.Get(ctx, flat); {
	case err == nil:
		var m store.Retired
		_ = json.Unmarshal(e.Value(), &m)
		return store.Record{}, fmt.Errorf("mash: endpoint %s is retired (since %s) and its name is never reassigned (INV-002)", id, m.RetiredAt)
	case !errors.Is(err, jetstream.ErrKeyNotFound):
		return store.Record{}, fmt.Errorf("mash: retired marker %s: %w", flat, err)
	}
	// The alias is verified below against a directory read made now, with all three locks held.
	if _, err := kv.Get(ctx, flat); err == nil {
		return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", id)
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Record{}, err
	}
	lease, err := a.leaseKV(ctx)
	if err != nil {
		return store.Record{}, err
	}
	switch e, err := lease.Get(ctx, flat); {
	case err == nil:
		// Belt and braces: the durable fence is the retired marker above. A tombstone written before markers existed
		// still refuses here while it lives.
		if isTombstone(e.Value()) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s was deprovisioned and its name is retired: a retired name is not reassigned", id)
		}
	case !errors.Is(err, jetstream.ErrKeyNotFound):
		return store.Record{}, fmt.Errorf("mash: lease %s: %w", flat, err)
	}
	recs, err := a.directoryRecords(ctx, kv)
	if err != nil {
		return store.Record{}, fmt.Errorf("mash: admit %s refused: %w", id, err)
	}
	var aliasID *sap.EndpointIdentity
	if alias != nil {
		leg, err := VerifyAlias(id, *alias, recs)
		if err != nil {
			return store.Record{}, err
		}
		aliasID = &leg
	}
	if err := store.AdmitWithAlias(id, aliasID, "", recs); err != nil {
		return store.Record{}, err
	}
	s, err := a.createStreamLocked(ctx, lk, flat, func(jetstream.Stream) jetstream.StreamConfig {
		return store.InboxConfigIdentity(id, a.limits)
	})
	if err != nil {
		return store.Record{}, err
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	rec = store.Record{
		Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent,
		Account: account, User: user, MailSubject: id.Subjects().Mail,
		Created: time.Now().UTC().Format(time.RFC3339), Note: note,
		Role: meta.Role, Host: meta.Host, Labels: meta.Labels, Description: meta.Description, Caps: sortedCaps(meta.Caps),
		Alias: alias,
	}
	val, _ := json.Marshal(rec)
	if err := lk.check(ctx); err != nil {
		return store.Record{}, err
	}
	if aliasLock != nil {
		if err := aliasLock.check(ctx); err != nil {
			return store.Record{}, err
		}
	}
	if err := adm.check(ctx); err != nil {
		return store.Record{}, err
	}
	if _, err := kv.Create(ctx, flat, val); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", id)
		}
		return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
	}
	return rec, nil
}

// NewFormUserPermissions is the permission set of a new-form endpoint's user: NewFormPermissions(id, extra), plus
// WindowBridge for the alias when there is one, allow lists merged and deduplicated. It takes no policy input, so a
// policy-compiled set cannot reach it (RefuseCompiledForNewForm is for the caller that holds that flag).
//
// existing is the directory's records. A non-nil alias is verified here with VerifyAlias against existing, and refused
// on error before WindowBridge is called, so verification cannot be skipped. The caller passes a directory read made
// while it holds the locks that keep the alias's legacy record in place.
func NewFormUserPermissions(id sap.EndpointIdentity, alias *store.Alias, existing []store.Record, extra []string) (jwt.Permissions, error) {
	base, err := NewFormPermissions(id, extra)
	if err != nil || alias == nil {
		return base, err
	}
	leg, err := VerifyAlias(id, *alias, existing)
	if err != nil {
		return jwt.Permissions{}, fmt.Errorf("mash: new-form permissions: %w", err)
	}
	bridge, err := WindowBridge(id, leg)
	if err != nil {
		return jwt.Permissions{}, err
	}
	var p jwt.Permissions
	p.Pub.Allow.Add(dedupe(slices.Concat(base.Pub.Allow, bridge.Pub.Allow))...)
	p.Sub.Allow.Add(dedupe(slices.Concat(base.Sub.Allow, bridge.Sub.Allow))...)
	return p, nil
}

// retiredKV returns the retired-name bucket, creating it if a deployment from before it existed has not been
// re-initialised (`mash admin init` creates it).
func (a *Admin) retiredKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.RetiredBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = a.js.CreateKeyValue(ctx, store.RetiredConfig()); err != nil {
			// Lost a race with another admin creating it, or no right to create: read it again.
			if kv2, err2 := a.js.KeyValue(ctx, store.RetiredBucket); err2 == nil {
				return kv2, nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: retired-name bucket (run `mash admin init`): %w", err)
	}
	return kv, nil
}

// retireLocked burns key's retired marker when key is a new-form flat identity (it holds "_"; a legacy name never
// does) that has a directory record, and does nothing otherwise. user is the record's user key when the caller
// already read it; when empty the record is read from dir. A key with no record is not burned, so a mistyped name
// retires nothing, since the marker has no release. The caller holds key's lock.
func (a *Admin) retireLocked(ctx context.Context, lk *streamLock, dir jetstream.KeyValue, key, user string) error {
	if !strings.Contains(key, "_") {
		return nil
	}
	if user == "" {
		e, err := dir.Get(ctx, key)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted):
			return nil
		case err != nil:
			return fmt.Errorf("mash: directory %s: %w; nothing was removed", key, err)
		}
		if rec, perr := store.ParseRecord(key, e.Value()); perr == nil {
			user = rec.User
		}
	}
	return a.retireMarker(ctx, lk, key, user)
}

// retireMarker writes key's marker with Create, so an existing one (a re-run, or a crash after the marker) keeps its
// first retired-at and the call is idempotent. A key that is not a valid new-form flat identity is not marked. The
// caller holds key's lock.
func (a *Admin) retireMarker(ctx context.Context, lk *streamLock, key, user string) error {
	if !strings.Contains(key, "_") {
		return nil
	}
	id, err := sap.FromFlat(key)
	if err != nil {
		return nil
	}
	kv, err := a.retiredKV(ctx)
	if err != nil {
		return err
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	val, _ := json.Marshal(store.Retired{Agent: id.Agent, Owner: id.Owner, Name: id.Name, User: user,
		RetiredAt: time.Now().UTC().Format(time.RFC3339)})
	if _, err := kv.Create(ctx, key, val); err != nil && !errors.Is(err, jetstream.ErrKeyExists) {
		return fmt.Errorf("mash: retired marker %s: %w", key, err)
	}
	return nil
}
