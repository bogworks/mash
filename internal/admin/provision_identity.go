package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Provision provisions the endpoint at addr (ADR 0145). The configured name rule is applied first, to all three tokens
// of addr, before the keys are validated and before anything is read or written. It creates the directory record
// keyed by addr's flat key, the inbox stream inbox-<flat> filtered on addr's exact mail subject, and the durable
// consumer. The record's agent is the harness, sap.AgentName.
//
// The flat key's lock is held for the whole call, record and stream alike, so a deprovision of the same address
// cannot interleave with it. The record is written last, so a provision that stopped part-way is finished by running
// the same call again; a finished one is refused as already provisioned. A retired address is refused (INV-002): the
// deprovision writes a durable marker into the no-TTL bucket store.RetiredBucket (key = flat key; value: store.Retired,
// its tokens, the retired user public key, the time and a state, never a seed) as pending before it revokes or
// removes anything, and makes it final once the record is purged. This call checks it under the flat key's lock; a
// pending marker blocks exactly like a final one. A deprovision deletes only the pending marker its own call wrote,
// when it refuses before the purge; there is no release path for a marker that outlives a call: releasing one would
// need a contract ruling and an audit record. The bucket must hold room for one more marker and must have no TTL, or
// provisioning is refused.
func (a *Admin) Provision(ctx context.Context, addr sap.Address, user, account, note string, meta Meta) (rec store.Record, err error) {
	if err := addr.Validate(); err != nil {
		return store.Record{}, fmt.Errorf("mash: provision: %w", err)
	}
	if a.rule != nil {
		if err := a.rule.Check(addr); err != nil {
			return store.Record{}, fmt.Errorf("mash: provision: %w", err)
		}
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return store.Record{}, errors.New("mash: the user is not a user public key (U, then 55 base32 characters; the value is not shown)")
	}
	if !nkeys.IsValidPublicAccountKey(account) {
		return store.Record{}, errors.New("mash: the account is not an account public key (A, then 55 base32 characters; the value is not shown)")
	}
	if err := meta.validate(); err != nil {
		return store.Record{}, err
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	flat := addr.Flat()
	lk, err := a.lockStream(ctx, flat)
	if err != nil {
		return store.Record{}, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "provision", &err)
	defer end()
	// INV-002: a retired address is never reassigned. The marker is durable (no TTL) and mash never deletes it.
	// Checked with the flat key's lock held, before anything is written; an unreadable marker refuses (fail closed).
	retired, rbs, err := a.openRetired(ctx)
	if err != nil {
		return store.Record{}, err
	}
	switch e, err := retired.Get(ctx, flat); {
	case err == nil:
		m, _ := parseMarker(flat, e.Value()) // an unreadable marker still blocks
		if m.IsPending() {
			return store.Record{}, fmt.Errorf("mash: endpoint %s has a retirement that is pending and unfinished (since %s) and its address is never reassigned (INV-002); run deprovision for it again to finish", addr, m.RetiredAt)
		}
		return store.Record{}, fmt.Errorf("mash: endpoint %s is retired (since %s) and its address is never reassigned (INV-002)", addr, m.RetiredAt)
	case !errors.Is(err, jetstream.ErrKeyNotFound):
		return store.Record{}, fmt.Errorf("mash: retired marker %s: %w", flat, err)
	}
	if err := retiredRoom(rbs); err != nil {
		return store.Record{}, err
	}
	if _, err := kv.Get(ctx, flat); err == nil {
		return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", addr)
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
			return store.Record{}, fmt.Errorf("mash: endpoint %s was deprovisioned and its address is retired: a retired address is not reassigned", addr)
		}
	case !errors.Is(err, jetstream.ErrKeyNotFound):
		return store.Record{}, fmt.Errorf("mash: lease %s: %w", flat, err)
	}
	s, err := a.createInboxLocked(ctx, lk, addr)
	if err != nil {
		return store.Record{}, err
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	rec = store.Record{
		Owner: addr.Owner, Group: addr.Group, Instance: addr.Instance, Agent: sap.AgentName,
		Account: account, User: user, MailSubject: store.MailSubjectFor(addr),
		Created: time.Now().UTC().Format(time.RFC3339), Note: note,
		Role: meta.Role, Host: meta.Host, Labels: meta.Labels, Description: meta.Description, Caps: sortedCaps(meta.Caps),
	}
	val, _ := json.Marshal(rec)
	if err := lk.check(ctx); err != nil {
		return store.Record{}, err
	}
	if _, err := kv.Create(ctx, flat, val); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", addr)
		}
		return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
	}
	return rec, nil
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
		if down := bucketDown(store.RetiredBucket, err); down != nil {
			return nil, nil, down
		}
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

func parseMarker(key string, v []byte) (store.Retired, error) {
	var m store.Retired
	if err := json.Unmarshal(v, &m); err != nil {
		return m, fmt.Errorf("mash: retired marker %s cannot be read: %w", key, err)
	}
	return m, nil
}

// beginRetire is the first phase of retiring an address: it Creates the flat key's marker as pending, before anything is
// revoked or purged, so a full or failing bucket fails the call before it changes anything. A marker already there
// is resumed: pending for the same user (or no user on either side), or final for the same user (older markers have
// no state). A live record exists whenever this is called, so a pending marker for another user key than user is
// stale: the retirement of that old user was superseded (the record was re-keyed). It is deleted, revision-guarded,
// and the call goes on as if there were no marker. A final marker for another user key is a burned address with a live
// record: refused with ErrMarkerFinalOtherUser, nothing changed. The caller holds the flat key's lock.
func (a *Admin) beginRetire(ctx context.Context, lk *streamLock, id sap.Address, user string) (*retireMark, error) {
	key := id.Flat()
	kv, err := a.retiredKV(ctx)
	if err != nil {
		return nil, err
	}
	if err := lk.check(ctx); err != nil {
		return nil, err
	}
	m := store.Retired{Owner: id.Owner, Group: id.Group, Instance: id.Instance, User: user,
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

// loadRetire reads the flat key's existing marker without creating one: nil when it has none.
func (a *Admin) loadRetire(ctx context.Context, id sap.Address) (*retireMark, error) {
	key := id.Flat()
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

// abortRetire runs when a removal was definitively refused before the record was purged (or the guarded purge was
// refused by the server): it deletes the pending marker, revision-guarded, and returns err. It deletes a marker this
// call wrote, or, when superseded is set, a pending marker it resumed too (the retirement did not happen). It first
// re-checks that this call still holds its name lock: a call that lost its
// lock must not delete a marker another call may now be resuming, so it leaves it pending and says so. A failure
// to delete is joined to err, never silent. A final marker is never deleted. Deleting after the old user was revoked is
// deliberate: a definitive refusal means the record changed, so no retirement of this name happened and the live record
// keeps its name; the revoked user stays revoked.
func (a *Admin) abortRetire(ctx context.Context, lk *streamLock, pm *retireMark, err error, superseded bool) error {
	if pm == nil || err == nil || !pm.m.IsPending() || !(pm.wrote || superseded) {
		return err
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	if lerr := lk.check(dctx); lerr != nil {
		return errors.Join(err, &pendingMarkerError{key: pm.key, err: fmt.Errorf("this call no longer holds its lock, so it did not delete it: %w", lerr)})
	}
	if derr := pm.kv.Purge(dctx, pm.key, jetstream.LastRevision(pm.rev)); derr != nil && !isRevisionConflict(derr) {
		return errors.Join(err, &pendingMarkerError{key: pm.key, err: derr})
	}
	return err
}
