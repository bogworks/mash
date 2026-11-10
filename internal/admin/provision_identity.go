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
// user public key, the time and a state, never a seed) as pending before it revokes or removes anything, and makes it
// final once the record is purged. This call checks it under the admission lock; a pending marker blocks exactly like a
// final one. A deprovision deletes only the pending marker its own call wrote, when it refuses before the purge; there
// is no release path for a marker that outlives a call: releasing one would need a contract ruling and an audit record.
// A legacy name is not marked (Provision clears its tombstone on purpose), so it stays reusable. The bucket must hold
// room for one more marker and must have no TTL, or provisioning is refused.
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
	retired, rbs, err := a.openRetired(ctx)
	if err != nil {
		return store.Record{}, err
	}
	switch e, err := retired.Get(ctx, flat); {
	case err == nil:
		m, _ := parseMarker(flat, e.Value()) // an unreadable marker still blocks
		if m.IsPending() {
			return store.Record{}, fmt.Errorf("mash: endpoint %s has a retirement that is pending and unfinished (since %s) and its name is never reassigned (INV-002); run deprovision for it again to finish", id, m.RetiredAt)
		}
		return store.Record{}, fmt.Errorf("mash: endpoint %s is retired (since %s) and its name is never reassigned (INV-002)", id, m.RetiredAt)
	case !errors.Is(err, jetstream.ErrKeyNotFound):
		return store.Record{}, fmt.Errorf("mash: retired marker %s: %w", flat, err)
	}
	if err := retiredRoom(rbs); err != nil {
		return store.Record{}, err
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
	held := []*streamLock{adm}
	if aliasLock != nil {
		held = append(held, aliasLock)
	}
	checkHeld := func() error {
		for _, l := range held {
			if err := l.check(ctx); err != nil {
				return err
			}
		}
		return nil
	}
	s, err := a.createStreamLocked(ctx, lk, flat, func(jetstream.Stream) jetstream.StreamConfig {
		return store.InboxConfigIdentity(id, a.limits)
	}, checkHeld)
	if err != nil {
		return store.Record{}, err
	}
	if err := checkHeld(); err != nil {
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
	if err := checkHeld(); err != nil {
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

// retiredMarkerReserve is the room, in bytes, that provisioning wants free in the retired-name bucket: a marker is
// written when the endpoint is deprovisioned, and a bucket without room would make that fail.
const retiredMarkerReserve = 512

// retiredKV returns the retired-name bucket, creating it if a deployment from before it existed has not been
// re-initialised (`mash admin init` creates it). It fails closed on the bucket's configuration (a TTL would expire
// markers; see store.VerifyRetiredStatus) and never changes an existing bucket.
func (a *Admin) retiredKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, _, err := a.openRetired(ctx)
	return kv, err
}

func (a *Admin) openRetired(ctx context.Context) (jetstream.KeyValue, *jetstream.KeyValueBucketStatus, error) {
	kv, err := a.js.KeyValue(ctx, store.RetiredBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = a.js.CreateKeyValue(ctx, store.RetiredConfig()); err != nil {
			// Lost a race with another admin creating it, or no right to create: read it again.
			kv, err = a.js.KeyValue(ctx, store.RetiredBucket)
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("mash: retired-name bucket (run `mash admin init`): %w", err)
	}
	st, err := kv.Status(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("mash: retired-name bucket %s: %w", store.RetiredBucket, err)
	}
	if err := store.VerifyRetiredStatus(st); err != nil {
		return nil, nil, err
	}
	bs, _ := st.(*jetstream.KeyValueBucketStatus)
	return kv, bs, nil
}

// retiredRoom refuses when the retired-name bucket has no room for one more marker.
func retiredRoom(bs *jetstream.KeyValueBucketStatus) error {
	if bs == nil {
		return nil
	}
	if max := bs.StreamInfo().Config.MaxBytes; max > 0 && bs.Bytes()+retiredMarkerReserve > uint64(max) {
		return fmt.Errorf("mash: bucket %s is full (%d of %d bytes): deprovisioning could not retire a name in it, so nothing is provisioned; raise its max bytes (mash does not change it)", store.RetiredBucket, bs.Bytes(), max)
	}
	return nil
}

// retireMark is a retired marker one call holds: its key, the revision it last saw, its value, and whether this call
// created it (only a marker a call created is deleted by that call when it refuses).
type retireMark struct {
	kv    jetstream.KeyValue
	key   string
	rev   uint64
	m     store.Retired
	wrote bool
}

// pendingMarkerError is a pending marker that could not be deleted after its removal was refused. It stays, and
// blocks the name.
type pendingMarkerError struct {
	key string
	err error
}

func (p *pendingMarkerError) Error() string {
	return "mash: the pending retired marker " + p.key + " could not be removed after the refusal (" + p.err.Error() + "); it still blocks the name until the deprovision is run again"
}
func (p *pendingMarkerError) Unwrap() error { return p.err }

// ErrMarkerFinalOtherUser: the name's retired marker is final (the name is burned, INV-002) yet a live record carries
// another user key. Nothing is removed and the name stays refused.
var ErrMarkerFinalOtherUser = errors.New("its name is already retired (INV-002) by a final retired marker for another user key (left as it is)")

// newFormKey reports whether key is a new-form flat identity and returns it.
func newFormKey(key string) (sap.EndpointIdentity, bool) {
	if !strings.Contains(key, "_") {
		return sap.EndpointIdentity{}, false
	}
	id, err := sap.FromFlat(key)
	return id, err == nil
}

func parseMarker(key string, v []byte) (store.Retired, error) {
	var m store.Retired
	if err := json.Unmarshal(v, &m); err != nil {
		return m, fmt.Errorf("mash: retired marker %s cannot be read: %w", key, err)
	}
	return m, nil
}

// beginRetire is the first phase of retiring a new-form key: it Creates key's marker as pending, before anything is
// revoked or purged, so a full or failing bucket fails the call before it changes anything. A marker already there
// is resumed: pending for the same user (or no user on either side), or final for the same user (older markers have
// no state). A live record exists whenever this is called, so a pending marker for another user key than user is
// stale: the retirement of that old user was superseded (the record was re-keyed). It is deleted, revision-guarded,
// and the call goes on as if there were no marker. A final marker for another user key is a burned name with a live
// record: refused with ErrMarkerFinalOtherUser, nothing changed. A key that is not a new-form identity has no marker
// (nil). The caller holds key's lock.
func (a *Admin) beginRetire(ctx context.Context, lk *streamLock, key, user string) (*retireMark, error) {
	id, ok := newFormKey(key)
	if !ok {
		return nil, nil
	}
	kv, err := a.retiredKV(ctx)
	if err != nil {
		return nil, err
	}
	if err := lk.check(ctx); err != nil {
		return nil, err
	}
	m := store.Retired{Agent: id.Agent, Owner: id.Owner, Name: id.Name, User: user,
		RetiredAt: time.Now().UTC().Format(time.RFC3339), State: store.RetiredPending}
	val, _ := json.Marshal(m)
	for try := 0; try < 3; try++ {
		rev, err := kv.Create(ctx, key, val)
		if err == nil {
			return &retireMark{kv: kv, key: key, rev: rev, m: m, wrote: true}, nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return nil, fmt.Errorf("mash: retired marker %s: %w; nothing was changed", key, err)
		}
		e, gerr := kv.Get(ctx, key)
		if errors.Is(gerr, jetstream.ErrKeyNotFound) {
			continue // deleted between the two calls
		}
		if gerr != nil {
			return nil, fmt.Errorf("mash: retired marker %s: %w", key, gerr)
		}
		cur, perr := parseMarker(key, e.Value())
		if perr != nil {
			return nil, perr
		}
		if cur.User != "" && user != "" && cur.User != user {
			if !cur.IsPending() {
				return nil, &refusedError{name: key, why: ErrMarkerFinalOtherUser,
					text: fmt.Sprintf("endpoint %s is retired (since %s) and its name is never reassigned (INV-002): the final retired marker %s is for user key %s, not the record's %s; nothing was removed", id, cur.RetiredAt, key, shortKey(cur.User), shortKey(user))}
			}
			// Superseded: delete this stale pending marker only if it is still the revision read, then try again.
			if err := lk.check(ctx); err != nil {
				return nil, err
			}
			if err := kv.Purge(ctx, key, jetstream.LastRevision(e.Revision())); err != nil && !isRevisionConflict(err) && !errors.Is(err, jetstream.ErrKeyNotFound) {
				return nil, fmt.Errorf("mash: retired marker %s: the stale pending marker for user key %s could not be removed: %w; nothing was changed", key, shortKey(cur.User), err)
			}
			continue
		}
		return &retireMark{kv: kv, key: key, rev: e.Revision(), m: cur}, nil
	}
	return nil, fmt.Errorf("mash: retired marker %s changed while it was being read; try again", key)
}

// loadRetire reads key's existing marker without creating one: nil when the key is not a new-form identity or has
// none.
func (a *Admin) loadRetire(ctx context.Context, key string) (*retireMark, error) {
	if _, ok := newFormKey(key); !ok {
		return nil, nil
	}
	kv, err := a.retiredKV(ctx)
	if err != nil {
		return nil, err
	}
	e, err := kv.Get(ctx, key)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("mash: retired marker %s: %w", key, err)
	}
	m, err := parseMarker(key, e.Value())
	if err != nil {
		return nil, err
	}
	return &retireMark{kv: kv, key: key, rev: e.Revision(), m: m}, nil
}

// finalize is the second phase: a pending marker becomes final, with a revision check, once the record is purged. A
// final marker, or no marker, is left as it is. The first retired-at is kept; user fills in an empty user. The caller
// holds key's lock.
func (a *Admin) finalizeRetire(ctx context.Context, lk *streamLock, pm *retireMark, user string) error {
	if pm == nil || !pm.m.IsPending() {
		return nil
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	m := pm.m
	m.State = store.RetiredFinal
	if m.User == "" {
		m.User = user
	}
	val, _ := json.Marshal(m)
	rev, err := pm.kv.Update(ctx, pm.key, val, pm.rev)
	if err != nil {
		// The record is already purged, so the name must end up burned. If the marker is gone, write it final; if it
		// is final already, there is nothing to do.
		e, gerr := pm.kv.Get(ctx, pm.key)
		switch {
		case errors.Is(gerr, jetstream.ErrKeyNotFound):
			if err := lk.check(ctx); err != nil {
				return err
			}
			if rev, cerr := pm.kv.Create(ctx, pm.key, val); cerr == nil {
				pm.rev, pm.m = rev, m
				return nil
			}
		case gerr == nil:
			if cur, perr := parseMarker(pm.key, e.Value()); perr == nil && !cur.IsPending() {
				pm.rev, pm.m = e.Revision(), cur
				return nil
			}
		}
		return fmt.Errorf("mash: finalize retired marker %s: %w; it stays pending and blocks the name; run the same command again", pm.key, err)
	}
	pm.rev, pm.m = rev, m
	return nil
}

// retireFinalDirect writes a final marker for a record that is already gone and has no marker (the remains of a
// removal from before markers had two phases). An existing one keeps its first retired-at.
func (a *Admin) retireFinalDirect(ctx context.Context, lk *streamLock, key, user string) error {
	id, ok := newFormKey(key)
	if !ok {
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
		RetiredAt: time.Now().UTC().Format(time.RFC3339), State: store.RetiredFinal})
	if _, err := kv.Create(ctx, key, val); err != nil && !errors.Is(err, jetstream.ErrKeyExists) {
		return fmt.Errorf("mash: retired marker %s: %w", key, err)
	}
	return nil
}

// abortRetire runs when a removal was definitively refused before the record was purged (or the guarded purge was
// refused by the server): it deletes the pending marker, revision-guarded, and returns err. It deletes a marker this
// call wrote, or, when superseded is set, a pending marker it resumed too (the retirement did not happen). It first
// re-checks that this call still holds its name lock (and the admission lock, adm, when held): a call that lost its
// lock must not delete a marker another call may now be resuming, so it leaves it pending and says so. A failure
// to delete is joined to err, never silent. A final marker is never deleted.
func (a *Admin) abortRetire(ctx context.Context, lk *streamLock, adm *aliasGuard, pm *retireMark, err error, superseded bool) error {
	if pm == nil || err == nil || !pm.m.IsPending() || !(pm.wrote || superseded) {
		return err
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	lerr := lk.check(dctx)
	if lerr == nil {
		lerr = adm.check(dctx)
	}
	if lerr != nil {
		return errors.Join(err, &pendingMarkerError{key: pm.key, err: fmt.Errorf("this call no longer holds its lock, so it did not delete it: %w", lerr)})
	}
	if derr := pm.kv.Purge(dctx, pm.key, jetstream.LastRevision(pm.rev)); derr != nil && !isRevisionConflict(derr) {
		return errors.Join(err, &pendingMarkerError{key: pm.key, err: derr})
	}
	return err
}
