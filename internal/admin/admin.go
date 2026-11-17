package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Admin provisions endpoints with the account's admin credentials.
type Admin struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	owner  string
	limits store.Limits
	revoke RevokeHook     // nil: the removal methods revoke nothing (see WithRevoker)
	rule   *namerule.Rule // nil: no name rule (see WithNameRule)

	accountWait time.Duration // 0: AccountLockWait (see WithAccountLockWait)

	clock func() time.Time // nil: time.Now (see UseClock)
}

// UseClock sets the clock this Admin's locks read. For tests: lets a test give two Admins clocks that disagree.
// Copies made from a (the With methods) keep it.
func (a *Admin) UseClock(f func() time.Time) { a.clock = f }

func (a *Admin) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// WithAccountLockWait returns a copy of a that waits d, not AccountLockWait, for the account lock. a itself is
// unchanged.
func (a *Admin) WithAccountLockWait(d time.Duration) *Admin {
	c := *a
	c.accountWait = d
	return &c
}

// WithNameRule returns a copy of a whose Provision applies rule to the address it provisions, before the directory is
// read (docs/name-rule.md). A nil rule accepts everything. a itself is unchanged.
func (a *Admin) WithNameRule(rule *namerule.Rule) *Admin {
	c := *a
	c.rule = rule
	return &c
}

// CheckProvision applies rule to the address addr, as provisioning does. A refusal is an error that names the rule and
// carries its message. An address that is not valid passes here: the code that follows refuses it on its own terms.
func CheckProvision(rule *namerule.Rule, addr sap.Address) error {
	if rule == nil || addr.Validate() != nil {
		return nil
	}
	if err := rule.Check(addr); err != nil {
		return fmt.Errorf("mash: provision: %w", err)
	}
	return nil
}

// RevokeHook revokes the NATS user key user (a public key) of an endpoint that is about to be removed. It runs under the
// endpoint's name lock, after every guard of the removal has passed and before anything is removed; an error aborts the
// removal with nothing removed.
type RevokeHook func(ctx context.Context, user string) error

// WithRevoker returns a copy of a whose Deprovision, DeprovisionIfUser and DeprovisionDrained call h, when it is not
// nil, with the user key of the directory record read under the lock (see RevokeHook). Plain Deprovision calls it only
// when the name has a readable record. a itself is unchanged.
func (a *Admin) WithRevoker(h RevokeHook) *Admin {
	c := *a
	c.revoke = h
	return &c
}

// revokeLocked runs the revoke hook for user, if both are set, and re-checks the lock afterwards (the hook may take
// seconds). A hook error is a refusal (nothing was removed), though the revocation may be in force on some servers
// (apply.ErrNotConfirmed). done reports that the hook returned nil: from then on the user is revoked, and a failure of
// the lock check, like every later failure of the call, is a partialError (see partialIf).
func (a *Admin) revokeLocked(ctx context.Context, lk *streamLock, name, user string) (done bool, err error) {
	if a.revoke == nil || user == "" {
		return false, nil
	}
	if err := a.revoke(ctx, user); err != nil {
		return false, &refusedError{name: name, why: err, text: "could not confirm the revocation of " + shortKey(user) + ": " + err.Error() + "; it may already be in force on some servers; nothing was removed; run the same command again (revoking again is safe)"}
	}
	return true, partialIf(true, name, user, lk.check(ctx))
}

// shortKey is the first 8 characters of a key for messages (ctl.ShortKey; admin cannot import ctl).
func shortKey(k string) string { return fmt.Sprintf("%.8s", k) }

// partialError: the endpoint's NATS user was revoked, so its credentials are dead, but the removal did not complete.
// It is never a refusal, whatever it wraps.
type partialError struct {
	name, user string
	err        error
}

func (p *partialError) Error() string {
	return "mash: PARTIAL: " + p.name + "'s user key " + shortKey(p.user) + " was revoked in the account JWT, so its credentials no longer work, but the removal did not complete: " + p.err.Error() + "; run the same command again (revoking again is safe)"
}
func (p *partialError) Unwrap() error { return p.err }

// partialIf words err as a partialError when the user was revoked (done) and err is not nil.
func partialIf(done bool, name, user string, err error) error {
	if !done || err == nil {
		return err
	}
	var p *partialError
	if errors.As(err, &p) {
		return err
	}
	return &partialError{name: name, user: user, err: err}
}

// SetLimits sets the caps Init, Provision and EnsureInbox use (default: store.DefaultLimits). It validates them.
func (a *Admin) SetLimits(l store.Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	a.limits = l
	return nil
}

// New returns an Admin for owner, the owner token its issue records carry when they name none.
func New(nc *nats.Conn, owner string) (*Admin, error) {
	if _, err := sap.NewAddress(owner, "g", "i"); err != nil {
		return nil, fmt.Errorf("mash: owner %q is not a valid owner token (lower-case letters, digits and single dashes, 1-32 characters; the value is not shown)", owner)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &Admin{nc: nc, js: js, owner: owner, limits: store.DefaultLimits()}, nil
}

// Conn is the connection the Admin was made with. The caller owns it.
func (a *Admin) Conn() *nats.Conn { return a.nc }

// Init creates the directory, lease, retired-name and issue-record buckets. It is idempotent. A bucket from before buckets had a size cap (no
// MaxBytes) is bounded in place with a stream update, keeping its keys; a bucket whose MaxBytes is smaller than the
// configured limit is raised to it; a larger one is kept (a limit is never lowered), and nothing is purged. The lease
// bucket does not allow rollup: Init creates it hardened and hardens an existing one (see store.HardenLeaseBucket),
// and fails if it cannot.
func (a *Admin) Init(ctx context.Context) error {
	return JetStreamNotEnabledHint(a.initBuckets(ctx), 0)
}

func (a *Admin) initBuckets(ctx context.Context) error {
	for _, cfg := range []jetstream.KeyValueConfig{store.DirectoryConfigFor(a.limits), store.LeaseConfigFor(a.limits), store.RetiredConfig()} {
		if cfg.Bucket == store.LeaseBucket {
			if err := a.initLease(ctx, cfg); err != nil {
				return err
			}
			continue
		}
		if kv, err := a.js.KeyValue(ctx, cfg.Bucket); err == nil {
			st, err := kv.Status(ctx)
			if err != nil {
				return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
			}
			if cfg.Bucket == store.RetiredBucket {
				if err := store.VerifyRetiredStatus(st); err != nil { // fail closed; never modified here
					return err
				}
				continue // an existing retired bucket is never altered (ADR 0088)
			}
			if bs, ok := st.(*jetstream.KeyValueBucketStatus); ok && bs.StreamInfo().Config.MaxBytes > cfg.MaxBytes {
				cfg.MaxBytes = bs.StreamInfo().Config.MaxBytes
			}
		}
		if _, err := a.js.CreateOrUpdateKeyValue(ctx, cfg); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
		}
	}
	if err := a.initIssued(ctx); err != nil {
		return err
	}
	return a.initAdoption(ctx)
}

// initLease creates the lease bucket, or brings an existing one to cfg's limits, and leaves it refusing rollup. An
// existing bucket is updated through its stream (never CreateOrUpdateKeyValue, which would set rollup back on), so
// there is no moment at which an updated bucket allows rollup. A new bucket is created with rollup off from the start
// (store.CreateLeaseBucket).
func (a *Admin) initLease(ctx context.Context, cfg jetstream.KeyValueConfig) error {
	if _, err := a.js.KeyValue(ctx, cfg.Bucket); errors.Is(err, jetstream.ErrBucketNotFound) {
		if _, err := store.CreateLeaseBucket(ctx, a.js, cfg); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
		}
	} else if err != nil {
		return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
	} else {
		sc, err := store.LeaseStreamConfig(ctx, a.js)
		if err != nil {
			return err
		}
		if sc.MaxBytes > cfg.MaxBytes {
			cfg.MaxBytes = sc.MaxBytes
		}
		sc.MaxBytes = cfg.MaxBytes
		sc.AllowRollup = false
		if _, err := a.js.UpdateStream(ctx, sc); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
		}
	}
	if err := store.HardenLeaseBucket(ctx, a.js); err != nil {
		return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
	}
	return nil
}

// clearLease removes a deprovisioned name's lease tombstone, or a lease left in the key. The lease bucket does not
// allow rollup, so it is a revision-aware delete, never a purge. A key that is absent or already deleted needs
// nothing. Otherwise it deletes the key's current value by its revision. If the key changed between the read and the
// delete (a revision conflict), it reads and deletes again. It returns an error only if the key changed on all
// clearLeaseTries attempts, so Provision stops rather than proceed over a lease that is being refreshed.
func clearLease(ctx context.Context, lease jetstream.KeyValue, name string) error {
	for attempt := 0; attempt < clearLeaseTries; attempt++ {
		e, err := lease.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return nil
		}
		if err != nil {
			return err
		}
		err = lease.Delete(ctx, name, jetstream.LastRevision(e.Revision()))
		switch {
		case err == nil, errors.Is(err, jetstream.ErrKeyNotFound):
			return nil
		case !isRevisionConflict(err):
			return err
		}
	}
	return fmt.Errorf("lease key %q keeps changing; it is held by a live endpoint", name)
}

// clearLeaseTries bounds clearLease's read-and-delete attempts on a revision conflict.
const clearLeaseTries = 3

func (a *Admin) dir(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		if down := bucketDown(store.DirectoryBucket, err); down != nil {
			return nil, down
		}
		return nil, fmt.Errorf("mash: directory bucket (run `mash admin init`): %w", err)
	}
	return kv, nil
}

// Meta is a record's optional directory metadata.
type Meta struct {
	Role, Host  string
	Labels      map[string]string
	Description string
	Caps        []string
}

func (m Meta) validate() error {
	if err := store.ValidDescription(m.Description); err != nil {
		return err
	}
	if err := store.ValidCaps(m.Caps); err != nil {
		return err
	}
	for _, f := range []struct{ name, v string }{{"role", m.Role}, {"host", m.Host}} {
		if err := store.ValidMeta(f.name, f.v); err != nil {
			return err
		}
	}
	return store.ValidLabels(m.Labels)
}

// createInboxLocked creates addr's inbox stream, or updates an existing one to the configured limits unless that
// would lower a cap. The caller holds addr's lock.
func (a *Admin) createInboxLocked(ctx context.Context, lk *streamLock, addr sap.Address) (jetstream.Stream, error) {
	return a.createStreamLocked(ctx, lk, addr.Flat(), func(existing jetstream.Stream) jetstream.StreamConfig {
		return store.InboxConfigFor(addr, a.limits)
	})
}

// createStreamLocked is the create-or-update of the inbox stream inbox-<key> that both provisioning paths share: it
// reads any existing stream, refuses a suspended one or an update that would lower a cap, then writes the
// configuration build returns (build gets the existing stream, nil when there is none). The caller holds key's lock.
func (a *Admin) createStreamLocked(ctx context.Context, lk *streamLock, key string, build func(existing jetstream.Stream) jetstream.StreamConfig, alsoCheck ...func() error) (jetstream.Stream, error) {
	existing, err := a.js.Stream(ctx, sap.InboxStream(key))
	if err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("mash: inbox stream: %w", err)
	}
	if afterStreamRead != nil {
		afterStreamRead(a.limits.InboxMaxBytes)
	}
	if err != nil {
		existing = nil
	} else {
		if _, ok := suspendedSubjects(key, existing.CachedInfo().Config); ok {
			return nil, fmt.Errorf("mash: inbox of %s is suspended; run `mashctl resume %s` first", key, key)
		}
		if err := refuseLowering(key, existing.CachedInfo().Config, a.limits); err != nil {
			return nil, err
		}
	}
	cfg := build(existing)
	if err := lk.check(ctx); err != nil {
		return nil, err
	}
	for _, chk := range alsoCheck { // other locks the caller holds
		if err := chk(); err != nil {
			return nil, err
		}
	}
	s, err := a.js.CreateOrUpdateStream(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mash: inbox stream: %w", err)
	}
	return s, nil
}

// afterStreamRead is a test seam between reading an inbox stream's caps and writing them; it gets the writer's
// configured inbox max_bytes so a test can tell admins apart. Nil in production.
var afterStreamRead func(inboxMaxBytes int64)

// EnsureInbox makes sure name's inbox stream and its durable consumer exist, creating whichever is missing with the
// same configuration Provision uses. It is for an endpoint whose directory record already exists (Provision refuses
// those) and reports what it had to create: "stream", "consumer", both, or neither. An existing stream or consumer is
// only checked, never updated: one that differs from that configuration in any field mail depends on is an error
// naming the field, because fixing it in place could discard stored mail (a lower MaxBytes or MaxAge would). The
// user decides to deprovision it, which removes whatever the inbox holds, and provisions again.
func (a *Admin) EnsureInbox(ctx context.Context, addr sap.Address) ([]string, error) {
	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("mash: ensure inbox: %w", err)
	}
	name := addr.Flat()
	var made []string
	s, created, err := a.ensureInboxStream(ctx, addr)
	if err != nil {
		return nil, err
	}
	if created {
		made = append(made, "stream")
	}
	c, err := s.Consumer(ctx, store.Consumer)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		if _, err := s.CreateConsumer(ctx, store.ConsumerConfig()); err != nil {
			return nil, fmt.Errorf("mash: inbox consumer: %w", err)
		}
		made = append(made, "consumer")
	} else if errors.Is(err, jetstream.ErrNotPullConsumer) {
		// The client will not hand out a push consumer, so its other fields cannot be read; the delivery subject is the field.
		return nil, firstInboxDiff("consumer", name, []inboxField{{"deliver_subject", "(set)", "(empty: a pull consumer)"}})
	} else if err != nil {
		return nil, fmt.Errorf("mash: inbox consumer: %w", err)
	} else if err := checkInboxConsumer(name, c.CachedInfo().Config, store.ConsumerConfig()); err != nil {
		return nil, err
	}
	return made, nil
}

// ensureInboxStream is ensureInboxStreamLocked under name's lock.
func (a *Admin) ensureInboxStream(ctx context.Context, addr sap.Address) (s jetstream.Stream, created bool, err error) {
	lk, err := a.lockStream(ctx, addr.Flat())
	if err != nil {
		return nil, false, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "ensure-inbox", &err)
	defer end()
	return a.ensureInboxStreamLocked(ctx, lk, addr)
}

// ensureInboxStreamLocked finds name's inbox stream, creating it if missing, or checks an existing one against the
// configuration. The caller holds name's lock.
func (a *Admin) ensureInboxStreamLocked(ctx context.Context, lk *streamLock, addr sap.Address) (s jetstream.Stream, created bool, err error) {
	name := addr.Flat()
	s, err = a.js.Stream(ctx, sap.InboxStream(name))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		if err := lk.check(ctx); err != nil {
			return nil, false, err
		}
		if s, err = a.js.CreateStream(ctx, store.InboxConfigFor(addr, a.limits)); err != nil {
			return nil, false, fmt.Errorf("mash: inbox stream: %w", err)
		}
		return s, true, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("mash: inbox stream: %w", err)
	}
	want := store.InboxConfigFor(addr, a.limits)
	got := s.CachedInfo().Config
	// A suspended stream is the endpoint's own: its saved subjects stand in for the parked one (ADR 0057).
	if saved, ok := suspendedSubjects(name, got); ok {
		got.Subjects = saved
	}
	if err := checkInboxStream(name, got, want); err != nil {
		return nil, false, err
	}
	return s, false, nil
}

// inboxField is one configuration field of an existing inbox next to the value mash wants.
type inboxField struct {
	name      string
	got, want any
}

// sortedCopy returns subjects sorted, so the order the server reports them in does not matter.
func sortedCopy(subjects []string) []string {
	c := slices.Clone(subjects)
	slices.Sort(c)
	return c
}

// firstInboxDiff returns an error for the first field whose value differs, naming it, both values and the way out.
func firstInboxDiff(what, name string, fields []inboxField) error {
	for _, f := range fields {
		if fmt.Sprint(f.got) != fmt.Sprint(f.want) {
			return fmt.Errorf("mash: inbox %s of %s has %s %v where mash needs %v; mash does not change an existing inbox, because that could discard stored mail; "+
				"if the mail in it can go, run `mash admin deprovision %s` and provision it again", what, name, f.name, f.got, f.want, name)
		}
	}
	return nil
}

// checkInboxStream compares an existing inbox stream with want, field by field. want carries the one mail filter.
func checkInboxStream(name string, got, want jetstream.StreamConfig) error {
	wantSubjects := sortedCopy(want.Subjects)
	return firstInboxDiff("stream", name, []inboxField{
		{"subjects", sortedCopy(got.Subjects), wantSubjects},
		{"storage", got.Storage, want.Storage},
		{"retention", got.Retention, want.Retention},
		{"replicas", got.Replicas, want.Replicas},
		{"max_age", got.MaxAge, want.MaxAge},
		{"max_bytes", got.MaxBytes, want.MaxBytes},
		{"max_msgs", got.MaxMsgs, want.MaxMsgs},
		{"max_msgs_per_subject", got.MaxMsgsPerSubject, want.MaxMsgsPerSubject},
		{"max_msg_size", got.MaxMsgSize, want.MaxMsgSize},
		{"discard", got.Discard, want.Discard},
		{"discard_new_per_subject", got.DiscardNewPerSubject, want.DiscardNewPerSubject},
		{"duplicate_window", got.Duplicates, want.Duplicates},
		{"deny_delete", got.DenyDelete, want.DenyDelete},
		{"deny_purge", got.DenyPurge, want.DenyPurge},
		{"allow_direct", got.AllowDirect, want.AllowDirect},
		{"mirror_direct", got.MirrorDirect, want.MirrorDirect},
		{"max_consumers", got.MaxConsumers, want.MaxConsumers},
		{"allow_rollup", got.AllowRollup, want.AllowRollup},
		{"compression", got.Compression, want.Compression},
		{"no_ack", got.NoAck, want.NoAck},
		{"sealed", got.Sealed, want.Sealed},
		{"mirror", got.Mirror != nil, want.Mirror != nil},
		{"sources", len(got.Sources), len(want.Sources)},
		{"subject_transform", got.SubjectTransform != nil, want.SubjectTransform != nil},
		{"republish", got.RePublish != nil, want.RePublish != nil},
	})
}

// checkInboxConsumer is checkInboxStream for the durable consumer: everything ConsumerConfig sets, plus what would
// turn it into something the pull reader cannot use (a delivery subject, headers only, a start point, a replay rate).
func checkInboxConsumer(name string, got, want jetstream.ConsumerConfig) error {
	return firstInboxDiff("consumer", name, []inboxField{
		{"ack_policy", got.AckPolicy, want.AckPolicy},
		{"deliver_policy", got.DeliverPolicy, want.DeliverPolicy},
		{"ack_wait", got.AckWait, want.AckWait},
		{"max_ack_pending", got.MaxAckPending, want.MaxAckPending},
		{"max_deliver", got.MaxDeliver, want.MaxDeliver},
		{"filter_subject", got.FilterSubject, want.FilterSubject},
		{"filter_subjects", sortedCopy(got.FilterSubjects), sortedCopy(want.FilterSubjects)},
		{"deliver_subject", got.DeliverSubject, want.DeliverSubject},
		{"deliver_group", got.DeliverGroup, want.DeliverGroup},
		{"headers_only", got.HeadersOnly, want.HeadersOnly},
		{"replay_policy", got.ReplayPolicy, want.ReplayPolicy},
		{"inactive_threshold", got.InactiveThreshold, want.InactiveThreshold},
		{"opt_start_seq", got.OptStartSeq, want.OptStartSeq},
		{"opt_start_time", got.OptStartTime != nil, want.OptStartTime != nil},
		{"backoff", got.BackOff, want.BackOff},
		{"rate_limit", got.RateLimit, want.RateLimit},
		{"flow_control", got.FlowControl, want.FlowControl},
		{"idle_heartbeat", got.IdleHeartbeat, want.IdleHeartbeat},
		// max_request_* are limits on pull requests that a hosted server fills in itself (Synadia sets max_request_batch
		// 4096); they do not change which mail arrives, so they are not compared.
		{"memory_storage", got.MemoryStorage, want.MemoryStorage},
	})
}

// ApplyLimits raises the configured limits on existing inboxes in place with a stream update: max_bytes and max_age
// where they are below the configured value, and max_consumers where it differs. With no addresses it covers every
// directory endpoint. Lowering max_bytes or max_age is refused with an error naming the endpoint and field (nothing
// changes for that endpoint), because it could discard stored mail; the other endpoints are still processed and the
// errors are joined. It returns one line per endpoint saying what changed.
func (a *Admin) ApplyLimits(ctx context.Context, addrs ...sap.Address) ([]string, error) {
	var skipped []string
	if len(addrs) == 0 {
		recs, bad, err := a.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range bad {
			skipped = append(skipped, "warning: skipping directory record "+config.Clean(b.Key)+": "+config.Clean(b.Error))
		}
		for _, r := range recs {
			addr, err := r.Address()
			if err != nil {
				return nil, err
			}
			addrs = append(addrs, addr)
		}
	}
	lines := skipped
	var errs []error
	for _, addr := range addrs {
		line, err := a.applyInboxLimits(ctx, addr)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		lines = append(lines, line)
	}
	return lines, errors.Join(errs...)
}

// unlimited reports whether a NATS cap value means no cap: MaxBytes is -1 (or 0), MaxAge is 0.
func unlimited(n int64) bool { return n <= 0 }

// lowers reports whether moving a cap from existing to configured would lower it. An unlimited cap is larger than
// any finite one, so a finite configured value lowers an unlimited existing one.
func lowers(existing, configured int64) bool {
	return !unlimited(configured) && (unlimited(existing) || existing > configured)
}

// raises reports whether moving a cap from existing to configured would raise it. Only a finite existing cap can
// be raised; an unlimited one is already as large as it gets.
func raises(existing, configured int64) bool {
	return !unlimited(existing) && (unlimited(configured) || existing < configured)
}

// capText renders an existing cap (n, finite text) for an error message, saying so when the stream is unlimited.
func capText(n int64, finite string) string {
	if unlimited(n) {
		return "unlimited"
	}
	return finite
}

// refuseLowering returns the "mash never lowers a limit" error when configuring limits l on an inbox stream with
// config cfg would lower its MaxBytes or MaxAge (unlimited counts as larger than any finite value), else nil.
func refuseLowering(name string, cfg jetstream.StreamConfig, l store.Limits) error {
	if lowers(cfg.MaxBytes, l.InboxMaxBytes) {
		return fmt.Errorf("mash: inbox of %s has max_bytes %s, above the configured %d; mash never lowers a limit, because that could discard stored mail; "+
			"to lower it, run `mash admin deprovision %s` and provision it again", name, capText(cfg.MaxBytes, strconv.FormatInt(cfg.MaxBytes, 10)), l.InboxMaxBytes, name)
	}
	if lowers(int64(cfg.MaxAge), int64(l.InboxMaxAge)) {
		return fmt.Errorf("mash: inbox of %s has max_age %s, above the configured %s; mash never lowers a limit, because that could discard stored mail; "+
			"to lower it, run `mash admin deprovision %s` and provision it again", name, capText(int64(cfg.MaxAge), cfg.MaxAge.String()), l.InboxMaxAge, name)
	}
	return nil
}

func (a *Admin) applyInboxLimits(ctx context.Context, addr sap.Address) (msg string, err error) {
	if err := addr.Validate(); err != nil {
		return "", fmt.Errorf("mash: limits: %w", err)
	}
	// The read, the check and the write run under the stream's limits lock.
	lk, err := a.lockStream(ctx, addr.Flat())
	if err != nil {
		return "", err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "limits change", &err)
	defer end()
	return a.applyInboxLimitsLocked(ctx, lk, addr.Flat())
}

// applyInboxLimitsLocked is applyInboxLimits for a caller that holds name's lock.
func (a *Admin) applyInboxLimitsLocked(ctx context.Context, lk *streamLock, name string) (string, error) {
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		return "", fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := s.CachedInfo().Config
	l := a.limits
	if err := refuseLowering(name, cfg, l); err != nil {
		return "", err
	}
	var changed []string
	if raises(cfg.MaxBytes, l.InboxMaxBytes) {
		changed = append(changed, fmt.Sprintf("max_bytes %d -> %d", cfg.MaxBytes, l.InboxMaxBytes))
		cfg.MaxBytes = l.InboxMaxBytes
	}
	if raises(int64(cfg.MaxAge), int64(l.InboxMaxAge)) {
		changed = append(changed, fmt.Sprintf("max_age %s -> %s", cfg.MaxAge, l.InboxMaxAge))
		cfg.MaxAge = l.InboxMaxAge
	}
	if cfg.MaxConsumers != l.InboxMaxConsumers {
		changed = append(changed, fmt.Sprintf("max_consumers %d -> %d", cfg.MaxConsumers, l.InboxMaxConsumers))
		cfg.MaxConsumers = l.InboxMaxConsumers
	}
	if len(changed) == 0 {
		return name + ": unchanged", nil
	}
	if afterStreamRead != nil {
		afterStreamRead(a.limits.InboxMaxBytes)
	}
	if err := lk.check(ctx); err != nil {
		return "", err
	}
	if _, err := a.js.UpdateStream(ctx, cfg); err != nil {
		return "", fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return name + ": " + strings.Join(changed, ", "), nil
}

// MetaUpdate names the metadata fields SetMeta changes; a nil field is left alone and a pointer to "" clears it.
// Labels is applied per key: a value sets that label, an empty value clears it, other labels are left alone.
// Description is set like Role. Caps are applied as a merge: AddCaps are added, DropCaps removed, and ClearCaps
// removes every capability first (then AddCaps still apply).
type MetaUpdate struct {
	Role, Host  *string
	Labels      map[string]string
	Description *string
	AddCaps     []string
	DropCaps    []string
	ClearCaps   bool
}

func sortedCaps(c []string) []string {
	if len(c) == 0 {
		return nil
	}
	out := append([]string(nil), c...)
	sort.Strings(out)
	return out
}

// setMetaTries is how many times SetMeta re-reads and retries after losing a compare-and-set.
const setMetaTries = 5

// afterMetaGet is a test seam between SetMeta's read and its write; nil in production.
var afterMetaGet func()

// SetMeta changes addr's role, host and labels, only those given. It merges into the stored JSON object, so every
// other field, including ones a newer mash wrote, survives, and writes with a compare-and-set against the revision
// it read, re-reading and retrying if another write got in between.
func (a *Admin) SetMeta(ctx context.Context, addr sap.Address, u MetaUpdate) (store.Record, error) {
	if err := addr.Validate(); err != nil {
		return store.Record{}, fmt.Errorf("mash: set meta: %w", err)
	}
	name := addr.Flat()
	fields := map[string]*string{"role": u.Role, "host": u.Host}
	for f, v := range fields {
		if v != nil {
			if err := store.ValidMeta(f, *v); err != nil {
				return store.Record{}, err
			}
		}
	}
	if u.Description != nil {
		if err := store.ValidDescription(*u.Description); err != nil {
			return store.Record{}, err
		}
		fields["description"] = u.Description
	}
	for _, c := range append(append([]string(nil), u.AddCaps...), u.DropCaps...) {
		if !store.ValidCap(c) {
			return store.Record{}, fmt.Errorf("mash: capability: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)")
		}
	}
	changeCaps := u.ClearCaps || len(u.AddCaps) > 0 || len(u.DropCaps) > 0
	for k, v := range u.Labels {
		if !store.ValidLabelKey(k) {
			return store.Record{}, fmt.Errorf("mash: label key: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)")
		}
		if err := store.ValidMeta("label "+k, v); err != nil {
			return store.Record{}, err
		}
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	for try := 0; try < setMetaTries; try++ {
		e, err := kv.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is not provisioned", name)
		}
		if err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		if _, err := store.ParseRecord(name, e.Value()); err != nil {
			return store.Record{}, err
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(e.Value(), &obj); err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		for f, v := range fields {
			if v == nil {
				continue
			}
			if *v == "" {
				delete(obj, f)
				continue
			}
			b, _ := json.Marshal(*v)
			obj[f] = b
		}
		if len(u.Labels) > 0 {
			// Merge into the stored labels.
			labels := map[string]string{}
			if raw, ok := obj["labels"]; ok {
				if err := json.Unmarshal(raw, &labels); err != nil {
					return store.Record{}, fmt.Errorf("mash: directory %s: labels: %w", name, err)
				}
			}
			for k, v := range u.Labels {
				if v == "" {
					delete(labels, k)
				} else {
					labels[k] = v
				}
			}
			if err := store.ValidLabels(labels); err != nil {
				return store.Record{}, err
			}
			if len(labels) == 0 {
				delete(obj, "labels")
			} else {
				b, _ := json.Marshal(labels)
				obj["labels"] = b
			}
		}
		if changeCaps {
			var caps []string
			if raw, ok := obj["caps"]; ok && !u.ClearCaps {
				if err := json.Unmarshal(raw, &caps); err != nil {
					return store.Record{}, fmt.Errorf("mash: directory %s: caps: %w", name, err)
				}
			}
			set := map[string]bool{}
			for _, c := range caps {
				set[c] = true
			}
			for _, c := range u.DropCaps {
				delete(set, c)
			}
			for _, c := range u.AddCaps {
				set[c] = true
			}
			merged := make([]string, 0, len(set))
			for c := range set {
				merged = append(merged, c)
			}
			sort.Strings(merged)
			if err := store.ValidCaps(merged); err != nil {
				return store.Record{}, err
			}
			if len(merged) == 0 {
				delete(obj, "caps")
			} else {
				b, _ := json.Marshal(merged)
				obj["caps"] = b
			}
		}
		val, err := json.Marshal(obj)
		if err != nil {
			return store.Record{}, err
		}
		if afterMetaGet != nil {
			afterMetaGet()
		}
		if _, err := kv.Update(ctx, name, val, e.Revision()); err != nil {
			if isRevisionConflict(err) {
				continue
			}
			return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
		}
		return store.ParseRecord(name, val)
	}
	return store.Record{}, fmt.Errorf("mash: directory %s kept changing under set-meta; try again", name)
}

// NameLock is a held name lock (ADR 0048, ADR 0058), handed to the func WithNameLock runs. It is valid only inside that
// func.
type NameLock struct {
	l *streamLock
}

// Check refuses unless the lock is still held and not about to expire (ADR 0058). Call it immediately before a write
// the lock protects that Admin does not make itself, such as a file.
func (n NameLock) Check(ctx context.Context) error { return n.l.check(ctx) }

// WithNameLock takes name's lock, runs fn with a context that ends before the lock expires and releases the lock. fn
// does its reads and writes through UserOfLocked and SetUserLocked (the lock is not reentrant: SetUser or UserOf inside
// fn would wait for it) and re-checks the lock with Check before any write of its own. An error from fn that is the
// context ending because the lock was about to expire is worded to say so.
func (a *Admin) WithNameLock(ctx context.Context, addr sap.Address, fn func(ctx context.Context, l NameLock) error) (err error) {
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("mash: name lock: %w", err)
	}
	lk, err := a.lockStream(ctx, addr.Flat())
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	bctx, end := lk.guard(ctx, "operation", &err)
	defer end()
	return fn(bctx, NameLock{l: lk})
}

// UserOf reads name's directory record and returns its user key and account. It returns ErrNoRecord when there is none.
// It does not take the name's lock; inside WithNameLock use UserOfLocked (the read is the same).
func (a *Admin) UserOf(ctx context.Context, addr sap.Address) (user, account string, err error) {
	if err := addr.Validate(); err != nil {
		return "", "", fmt.Errorf("mash: user of: %w", err)
	}
	name := addr.Flat()
	kv, err := a.dir(ctx)
	if err != nil {
		return "", "", err
	}
	e, err := kv.Get(ctx, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return "", "", ErrNoRecord
	}
	if err != nil {
		return "", "", fmt.Errorf("mash: directory %s: %w", name, err)
	}
	rec, err := store.ParseRecord(name, e.Value())
	if err != nil {
		return "", "", err
	}
	return rec.User, rec.Account, nil
}

// UserOfLocked is UserOf for a caller that holds name's lock l (from WithNameLock); it refuses a lock that is not
// held or not name's.
func (a *Admin) UserOfLocked(ctx context.Context, l NameLock, addr sap.Address) (user, account string, err error) {
	if l.l == nil || l.l.name != addr.Flat() {
		return "", "", fmt.Errorf("mash: the lock held is not %s's", addr.Flat())
	}
	if err := l.l.check(ctx); err != nil {
		return "", "", err
	}
	return a.UserOf(ctx, addr)
}

// SetUser is a compare-and-set of name's directory record's user key from oldUser to newUser (public user keys), for a
// key rotation. It takes the name's lock like SetMeta, reads the record and writes it back against the revision it
// read, re-reading and retrying if another write got in between, and keeps every other field. It refuses a name with
// no record, a record whose account is not account, and a record whose user is neither oldUser nor newUser. It is
// idempotent: a record that already carries newUser is returned as it is.
func (a *Admin) SetUser(ctx context.Context, addr sap.Address, account, oldUser, newUser string) (store.Record, error) {
	if err := validSetUser(addr, oldUser, newUser); err != nil {
		return store.Record{}, err
	}
	lk, err := a.lockStream(ctx, addr.Flat())
	if err != nil {
		return store.Record{}, err
	}
	defer lk.release(ctx)
	return a.setUser(ctx, lk, addr.Flat(), account, oldUser, newUser)
}

// SetUserLocked is SetUser for a caller that already holds name's lock l (from WithNameLock): it does not take the lock
// again, and re-checks it before the write.
func (a *Admin) SetUserLocked(ctx context.Context, l NameLock, addr sap.Address, account, oldUser, newUser string) (store.Record, error) {
	if err := validSetUser(addr, oldUser, newUser); err != nil {
		return store.Record{}, err
	}
	if l.l == nil || l.l.name != addr.Flat() {
		return store.Record{}, fmt.Errorf("mash: the lock held is not %s's", addr.Flat())
	}
	return a.setUser(ctx, l.l, addr.Flat(), account, oldUser, newUser)
}

func validSetUser(addr sap.Address, oldUser, user string) error {
	if !nkeys.IsValidPublicUserKey(oldUser) {
		return fmt.Errorf("mash: %q is not a user public key (U...)", oldUser)
	}
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("mash: set user: %w", err)
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return fmt.Errorf("mash: %q is not a user public key (U...)", user)
	}
	return nil
}

// setUser is SetUser's body, run with name's lock lk held.
func (a *Admin) setUser(ctx context.Context, lk *streamLock, name, account, oldUser, user string) (store.Record, error) {
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	for try := 0; try < setMetaTries; try++ {
		e, err := kv.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is not provisioned", name)
		}
		if err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		rec, err := store.ParseRecord(name, e.Value())
		if err != nil {
			return store.Record{}, err
		}
		if rec.Account != account {
			return store.Record{}, fmt.Errorf("mash: the record of %s belongs to account %s, not %s; left in place", name, rec.Account, account)
		}
		if rec.User == user {
			return rec, nil
		}
		if rec.User != oldUser {
			return store.Record{}, fmt.Errorf("mash: the record of %s names user key %s..., not %s...; left in place", name, shortKey(rec.User), shortKey(oldUser))
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(e.Value(), &obj); err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		ub, _ := json.Marshal(user)
		obj["user"] = ub
		val, err := json.Marshal(obj)
		if err != nil {
			return store.Record{}, err
		}
		if err := lk.check(ctx); err != nil {
			return store.Record{}, err
		}
		if _, err := kv.Update(ctx, name, val, e.Revision()); err != nil {
			if isRevisionConflict(err) {
				continue
			}
			return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
		}
		return store.ParseRecord(name, val)
	}
	return store.Record{}, fmt.Errorf("mash: directory %s kept changing under reissue; try again", name)
}

// isRevisionConflict reports a failed compare-and-set: the key's revision moved (or it was deleted) since the read.
func isRevisionConflict(err error) bool {
	var apiErr *jetstream.APIError
	return errors.Is(err, jetstream.ErrKeyExists) ||
		(errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence)
}

// Deprovision removes addr's directory record and inbox stream and fences its lease with a tombstone. The fence
// comes first: a session that is live or starting cannot take the lease back once its endpoint is gone (#10). The
// address's retired marker goes pending -> purge -> final (see beginRetire): the marker is written pending
// before the user is revoked or anything is removed, and made final once the record is purged.
func (a *Admin) Deprovision(ctx context.Context, addr sap.Address) (err error) {
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("mash: deprovision: %w", err)
	}
	lk, err := a.lockStream(ctx, addr.Flat())
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "deprovision", &err)
	defer end()
	return a.deprovisionLocked(ctx, lk, addr)
}

// deprovisionLocked is Deprovision's body; the caller holds addr's lock.
func (a *Admin) deprovisionLocked(ctx context.Context, lk *streamLock, addr sap.Address) error {
	name := addr.Flat()
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		if down := bucketDown(store.LeaseBucket, err); down != nil {
			return down
		}
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	// The record's user key, read under the lock. No record leaves nothing to revoke; a record that cannot be
	// parsed hides its user key, so a removal that must revoke fails closed.
	var ruser string
	have := false
	e, err := kv.Get(ctx, name)
	switch {
	case err == nil:
		have = true
		rec, perr := store.ParseRecord(name, e.Value())
		switch {
		case perr == nil:
			ruser = rec.User
		case a.revoke != nil:
			return &refusedError{name: name, why: perr, text: "its record cannot be read, so its user key is unknown; --no-revoke removes it without revoking (nothing was removed): " + perr.Error()}
		}
	case !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted):
		return fmt.Errorf("mash: directory %s: %w; nothing was removed", name, err)
	}
	// First phase, before anything is revoked or purged: the address's marker is written pending (a full or
	// failing bucket stops the call here). An address with no record is never burned, but a pending marker left by an
	// earlier run is picked up and finished.
	var pm *retireMark
	if have {
		pm, err = a.beginRetire(ctx, lk, addr, ruser)
	} else {
		pm, err = a.loadRetire(ctx, addr)
	}
	if err != nil {
		return err
	}
	var revoked, purgeTried bool
	err = func() error {
		if have && a.revoke != nil {
			done, err := a.revokeLocked(ctx, lk, name, ruser)
			revoked = done
			if err != nil {
				return err
			}
		}
		if err := fence(ctx, lease, name); err != nil {
			return err
		}
		if err := lk.check(ctx); err != nil {
			return err
		}
		purgeTried = true
		if err := purgeDirectory(ctx, kv, name); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
			return unfinishedPurge(name, pm, err)
		}
		return nil
	}()
	if err != nil {
		err = partialIf(revoked, name, ruser, err)
		// Only a definitive refusal made before the purge was tried deletes the marker; once the user is revoked the
		// retirement is under way, and a purge that failed unknown may have succeeded: both stay pending.
		if !revoked && !purgeTried {
			err = a.abortRetire(ctx, lk, pm, err, false)
		}
		return err
	}
	if afterDeprovisionPurge != nil {
		afterDeprovisionPurge()
	}
	if err := a.finalizeRetire(ctx, lk, pm, ruser); err != nil {
		return partialIf(revoked, name, ruser, err)
	}
	if err := lk.check(ctx); err != nil {
		return partialIf(revoked, name, ruser, err)
	}
	return partialIf(revoked, name, ruser, a.deleteInbox(ctx, name))
}

// directoryPurgeFault is a test seam: it runs after a directory purge the server accepted and may return an error
// that replaces the result (a timeout after the work was done); nil in production.
var directoryPurgeFault func() error

// purgeDirectory purges name's directory record.
func purgeDirectory(ctx context.Context, kv jetstream.KeyValue, name string, opts ...jetstream.KVDeleteOpt) error {
	err := kv.Purge(ctx, name, opts...)
	if err == nil && directoryPurgeFault != nil {
		err = directoryPurgeFault()
	}
	return err
}

// unfinishedPurge words a directory purge that failed with an answer that does not say whether the server did it (a
// timeout, a lost connection). When the name has a retired marker, the retirement is unfinished and the marker is
// left pending: it blocks the name until the deprovision is run again, which resolves it.
func unfinishedPurge(name string, pm *retireMark, err error) error {
	if pm == nil {
		return fmt.Errorf("mash: directory record: %w", err)
	}
	return fmt.Errorf("mash: the retirement of %s is unfinished: the directory purge did not report success (%w); its retired marker stays pending and blocks the name; run the same command again to finish it", name, err)
}

// fenceAndDeleteInbox is the part of a deprovision that follows a guarded record delete (DeprovisionIfUser,
// DeprovisionDrained; Deprovision fences before it purges): the lease tombstone, then the inbox stream.
func (a *Admin) fenceAndDeleteInbox(ctx context.Context, lk *streamLock, lease jetstream.KeyValue, name string) error {
	if err := lk.check(ctx); err != nil {
		return err
	}
	if err := fence(ctx, lease, name); err != nil {
		return err
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	return a.deleteInbox(ctx, name)
}

// DeprovisionDrained is Deprovision for an inbox that was suspended and read through sequence seq (the last_seq that
// Suspend reported); id is the suspension id Suspend reported. Under the name's lock it reads the stream fresh and
// refuses, touching nothing, unless the inbox is still suspended, carries a suspension id equal to id (an empty id never
// matches, so a re-provisioned endpoint of the same name is safe from an old command) and its last sequence is exactly seq: another admin's Resume, and any mail stored after it, make the
// check fail instead of losing mail. A name with no inbox stream is refused: nothing proves it is the one drained.
//
// The address's retired marker goes pending -> revoke and purge -> final exactly as in Deprovision (see beginRetire),
// and a re-run after a stop that followed the purge finishes it.
func (a *Admin) DeprovisionDrained(ctx context.Context, addr sap.Address, id string, seq uint64) (err error) {
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("mash: deprovision: %w", err)
	}
	name := addr.Flat()
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "deprovision", &err)
	defer end()
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	switch {
	case errors.Is(err, jetstream.ErrStreamNotFound):
		return fmt.Errorf("mash: endpoint %s has no inbox stream, so nothing proves it is the suspension you drained; run plain `deprovision` if it is a leftover", name)
	case err != nil:
		return fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	if _, ok := suspendedSubjects(name, info.Config); !ok {
		return fmt.Errorf("mash: inbox of %s is not suspended; run suspend first", name)
	}
	if cur := info.Config.Metadata[store.MetaSuspendedID]; id == "" || cur != id {
		return fmt.Errorf("mash: inbox of %s: this is not the suspension you drained (id %s, you hold %s); suspend state changed, read it again", name, cur, id)
	}
	if info.State.LastSeq != seq {
		return fmt.Errorf("mash: inbox of %s is at last_seq %d, not the drained %d; drain the rest and try again", name, info.State.LastSeq, seq)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	savedUser := info.Config.Metadata[store.MetaSuspendedUser]
	e, err := kv.Get(ctx, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		if savedUser != "" {
			// A removal that stopped after the purge: its retired marker, if any, is finished (see
			// finishAfterPurge); with none this refuses and changes nothing.
			if err := a.finishAfterPurge(ctx, lk, lease, addr, savedUser); err != nil {
				return fmt.Errorf("mash: endpoint %s has no directory record, so nothing proves it is the endpoint that was drained: %w", name, err)
			}
			return nil
		}
		return fmt.Errorf("mash: endpoint %s has no directory record, so nothing proves it is the endpoint that was drained; run plain `deprovision` if it is a leftover", name)
	} else if err != nil {
		return fmt.Errorf("mash: directory %s: %w", name, err)
	}
	rec, err := store.ParseRecord(name, e.Value())
	if err != nil {
		return fmt.Errorf("mash: refusing to deprovision %s: its record cannot be read, so nothing proves it is the endpoint that was drained (left in place): %w", name, err)
	}
	switch {
	case savedUser == "":
		return fmt.Errorf("mash: endpoint %s was suspended without a record; use plain `deprovision`", name)
	case rec.User != savedUser:
		return fmt.Errorf("mash: endpoint %s: its record belongs to a different user key than when it was suspended (left in place)", name)
	}
	if afterDeprovisionGet != nil {
		afterDeprovisionGet()
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	// First phase: the address's marker is written pending before the user is revoked or the record purged.
	pm, err := a.beginRetire(ctx, lk, addr, rec.User)
	if err != nil {
		return err
	}
	revoked, err := a.revokeLocked(ctx, lk, name, rec.User)
	if err != nil {
		// Nothing was purged: the marker this call wrote goes.
		return a.abortRetire(ctx, lk, pm, partialIf(revoked, name, rec.User, err), false)
	}
	if err := purgeDirectory(ctx, kv, name, jetstream.LastRevision(e.Revision())); err != nil {
		if isRevisionConflict(err) {
			// The server refused the guarded purge: the retirement did not happen, so the pending marker goes.
			err = partialIf(revoked, name, rec.User, fmt.Errorf("mash: refusing to deprovision %s: the record changed; left in place", name))
			return a.abortRetire(ctx, lk, pm, err, true)
		}
		// The answer does not say whether the server purged it: the marker stays pending.
		return partialIf(revoked, name, rec.User, unfinishedPurge(name, pm, err))
	}
	if afterDeprovisionPurge != nil {
		afterDeprovisionPurge()
	}
	// Second phase: the marker becomes final after the purge, never before it.
	if err := a.finalizeRetire(ctx, lk, pm, rec.User); err != nil {
		return partialIf(revoked, name, rec.User, err)
	}
	return partialIf(revoked, name, rec.User, a.fenceAndDeleteInbox(ctx, lk, lease, name))
}

// The refusals of DeprovisionIfUser: each leaves the record, the lease and the inbox as they were.
var (
	// ErrRecordOtherUser: the directory record's user is not the key given.
	ErrRecordOtherUser = errors.New("its record belongs to another user key (left in place)")
	// ErrRecordChanged: the record was replaced or changed between the read and the delete.
	ErrRecordChanged = errors.New("the record changed; left in place")
	// ErrNoRecord: there is no directory record, so nothing proves who owns the name (or its inbox stream).
	ErrNoRecord = errors.New("it has no directory record, so nothing proves the key owns it (left in place)")
)

// afterDeprovisionGet is a test seam between DeprovisionIfUser's read and its delete; nil in production.
var afterDeprovisionGet func()

// afterDeprovisionPurge is a test seam between a deprovision's record delete (Deprovision's or DeprovisionIfUser's) and
// the finalizing of its retired marker; nil in production.
var afterDeprovisionPurge func()

// DeprovisionIfUser is Deprovision for a record that must still belong to user (a public user key): it reads the
// record's value and revision, refuses unless its user equals user, and deletes the record with a revision guard
// (Purge with LastRevision), so a record replaced between the read and the delete is never removed. A revision
// conflict re-reads the record (up to setMetaTries times): a metadata-only change by another admin leaves the user
// unchanged, so the delete is retried on the new revision; a changed user, a vanished record or exhausted retries refuse.
//
// Order: guard and guarded record delete first, then the lease fence and the inbox delete. A refused call therefore
// touches nothing: no tombstone, no stream delete. The whole call runs under the name's lock (ADR-0048), so another
// admin's Provision of the name waits until the fence and the inbox delete are done and is never fenced and never has its
// inbox deleted. For sessions the gap between the record delete and the fence is safe because
// a session cannot hold a record-less name: Open re-reads the directory record after taking the lease and gives the
// lease back when it is gone (see Open), and a session that already held the lease is stopped by the tombstone at
// its next refresh, exactly as in Deprovision. The cost of this order against Deprovision's is that a failure
// between the two steps leaves a record-less name with an inbox and no fence; re-running `deprovision` (without
// --user) finishes it.
//
// With no record nothing proves ownership, so it refuses even when the inbox stream exists (a provision that failed
// before writing its record): remove that stream with a plain deprovision. The one exception is a key with a
// retired marker, the remains of an interrupted removal, which it finishes; with no marker it changes nothing.
func (a *Admin) DeprovisionIfUser(ctx context.Context, addr sap.Address, user string) (err error) {
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("mash: deprovision: %w", err)
	}
	name := addr.Flat()
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	// The name's lock covers the read, the record delete, the fence and the inbox delete: a Provision of the same
	// name waits for all of it, so it can never be fenced or have its inbox deleted by this call (ADR-0047).
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "deprovision", &err)
	defer end()
	changed := &refusedError{name: name, why: ErrRecordChanged, text: "the record for " + name + " changed; left in place"}
	purged, revoked := false, false
	var pm *retireMark
	// keep: the marker must stay pending (the purge's outcome is unknown, or the record vanished and nothing proves
	// the retirement did not happen). refused: the server definitively refused the guarded purge, or the record
	// turned out to be another user's, so the retirement did not happen and the pending marker goes.
	var keep, refused bool
	err = func() error {
		for try := 0; try < setMetaTries && !purged; try++ {
			e, err := kv.Get(ctx, name)
			if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
				if try > 0 {
					keep = true
					return partialIf(revoked, name, user, changed) // it vanished after we read it: nothing proves ownership now
				}
				// A record already gone: finished only if a retired marker shows a removal began (else
				// ErrNoRecord, nothing changed).
				return a.finishAfterPurge(ctx, lk, lease, addr, user)
			}
			if err != nil {
				return partialIf(revoked, name, user, fmt.Errorf("mash: directory %s: %w", name, err))
			}
			rec, err := store.ParseRecord(name, e.Value())
			if err != nil {
				return partialIf(revoked, name, user, fmt.Errorf("mash: refusing to deprovision %s: its record cannot be read, so nothing proves the key owns it (left in place): %w", name, err))
			}
			if rec.User != user {
				if try > 0 {
					refused = true
					return partialIf(revoked, name, user, changed)
				}
				return refusal(name, ErrRecordOtherUser)
			}
			if afterDeprovisionGet != nil {
				afterDeprovisionGet()
			}
			if err := lk.check(ctx); err != nil {
				return partialIf(revoked, name, user, err)
			}
			if pm == nil {
				// First phase: the marker is pending before the user is revoked or the record purged.
				if pm, err = a.beginRetire(ctx, lk, addr, user); err != nil {
					return err
				}
			}
			if !revoked {
				// rec.User == user here; once per call, since a retry only repeats for the same user.
				done, err := a.revokeLocked(ctx, lk, name, rec.User)
				revoked = done
				if err != nil {
					return err
				}
			}
			err = purgeDirectory(ctx, kv, name, jetstream.LastRevision(e.Revision()))
			switch {
			case err == nil:
				purged = true
			case isRevisionConflict(err):
				// Another admin changed the record (SetMeta bumps the revision). Re-read: if it is still this user's,
				// retry on the new revision; a different user or a vanished record refuses on the next pass.
			default:
				keep = true // the answer does not say whether the server purged it
				return partialIf(revoked, name, user, unfinishedPurge(name, pm, err))
			}
		}
		if !purged {
			refused = true // the guarded purge was refused every time: the record kept changing
			return partialIf(revoked, name, user, changed)
		}
		return nil
	}()
	if err != nil {
		// A definitive refusal deletes the pending marker even after the revoke (the old user stays revoked); a
		// refusal before the purge deletes the one this call wrote; an unknown outcome leaves it pending.
		if !purged && !keep && (refused || !revoked) {
			err = a.abortRetire(ctx, lk, pm, err, refused)
		}
		return err
	}
	if !purged { // finishAfterPurge did everything
		return nil
	}
	if afterDeprovisionPurge != nil {
		afterDeprovisionPurge()
	}
	// Second phase: the marker becomes final after the guarded purge, never before it.
	if err := a.finalizeRetire(ctx, lk, pm, user); err != nil {
		return partialIf(revoked, name, user, err)
	}
	return partialIf(revoked, name, user, a.fenceAndDeleteInbox(ctx, lk, lease, name))
}

// finishAfterPurge completes a DeprovisionIfUser whose record is already gone: the remains of a
// removal that stopped after the purge. Only an existing retired marker proves such a removal began (the marker is
// written pending before anything is revoked or purged), so with no marker it refuses with ErrNoRecord and changes
// nothing: a never-provisioned name, or a partial provision's inbox stream, is neither burned nor deleted. A pending
// marker becomes final (it keeps its first time); a final one is left as it is; a marker for another user than the
// caller's refuses. Then the lease is fenced and the inbox deleted. The caller holds name's lock.
func (a *Admin) finishAfterPurge(ctx context.Context, lk *streamLock, lease jetstream.KeyValue, addr sap.Address, user string) error {
	name := addr.Flat()
	pm, err := a.loadRetire(ctx, addr)
	if err != nil {
		return err
	}
	if pm == nil {
		return refusal(name, ErrNoRecord)
	}
	if pm.m.User != "" && pm.m.User != user {
		return refusal(name, ErrRecordOtherUser)
	}
	if err := a.finalizeRetire(ctx, lk, pm, user); err != nil {
		return err
	}
	return a.fenceAndDeleteInbox(ctx, lk, lease, name)
}

// IsRefusal reports whether err is a refusal of DeprovisionIfUser: the call touched nothing. Any other error from it
// may come after the record was deleted (the lease fence or the inbox delete failed, or the connection broke).
func IsRefusal(err error) bool {
	var p *partialError
	if errors.As(err, &p) {
		return false
	}
	var m *pendingMarkerError // a refusal whose pending marker could not be deleted did leave something behind
	if errors.As(err, &m) {
		return false
	}
	var r *refusedError
	return errors.As(err, &r)
}

// IsPartial reports whether err says the endpoint's NATS user was revoked but the removal did not complete: the
// credentials are dead and the same command run again finishes the removal.
func IsPartial(err error) bool {
	var p *partialError
	return errors.As(err, &p)
}

func refusal(name string, why error) error {
	return &refusedError{name: name, why: why}
}

type refusedError struct {
	name string
	why  error
	text string // overrides why's text when set
}

func (r *refusedError) Error() string {
	if r.text != "" {
		return "mash: refusing to deprovision " + r.name + ": " + r.text
	}
	return "mash: refusing to deprovision " + r.name + ": " + r.why.Error()
}
func (r *refusedError) Unwrap() error { return r.why }

// isTombstone reports whether a lease value is the tombstone Deprovision writes.
func isTombstone(v []byte) bool { return bytes.Contains(v, []byte(`"deprovisioned":true`)) }

// fence writes the lease tombstone for name.
func fence(ctx context.Context, lease jetstream.KeyValue, name string) error {
	if _, err := lease.Put(ctx, name, store.LeaseTombstone()); err != nil {
		return fmt.Errorf("mash: lease: %w", err)
	}
	return nil
}

func (a *Admin) deleteInbox(ctx context.Context, name string) error {
	if err := a.js.DeleteStream(ctx, sap.InboxStream(name)); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("mash: inbox stream: %w", err)
	}
	return nil
}

// BadRecord is a directory key whose value fails store.ParseRecord: the readers skip it, with a warning that names it.
type BadRecord struct {
	Key   string `json:"key"`
	Error string `json:"error"`
}

// Warn writes the warning a reader owes for a skipped record: its key and why, on one line.
func (b BadRecord) Warn(w io.Writer, prog string) {
	fmt.Fprintf(w, "%s: warning: skipping directory record %s: %s\n", prog, config.Clean(b.Key), config.Clean(b.Error))
}

// List returns every directory record that parses, sorted by name, and the records that do not. A record that fails
// store.ParseRecord is skipped, never fatal: the caller must report each BadRecord (the key and the error), never
// silently.
func (a *Admin) List(ctx context.Context) ([]store.Record, []BadRecord, error) {
	kv, err := a.dir(ctx)
	if err != nil {
		return nil, nil, err
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, wordUnavailable(err)
	}
	slices.Sort(keys)
	var out []store.Record
	var bad []BadRecord
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue // deleted since the listing
		}
		if err != nil {
			return nil, nil, wordUnavailable(fmt.Errorf("directory %s: %w", k, err))
		}
		r, err := store.ParseRecord(k, e.Value())
		if err != nil {
			bad = append(bad, BadRecord{Key: k, Error: err.Error()})
			continue
		}
		out = append(out, r)
	}
	return out, bad, nil
}

// Exists reports whether addr has a directory record, an inbox stream or a live lease (not just the tombstone a
// deprovision leaves): any of them is something a deprovision removes or fences.
// Deprovision itself stays idempotent (rollbacks rely on it); a CLI verb asks this first to refuse a name that was
// never there.
func (a *Admin) Exists(ctx context.Context, addr sap.Address) (bool, error) {
	if err := addr.Validate(); err != nil {
		return false, fmt.Errorf("mash: exists: %w", err)
	}
	name := addr.Flat()
	kv, err := a.js.KeyValue(ctx, store.DirectoryBucket)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
	case err != nil:
		return false, wordUnavailable(fmt.Errorf("mash: directory: %w", err))
	default:
		if _, err := kv.Get(ctx, name); err == nil {
			return true, nil
		} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
			return false, wordUnavailable(fmt.Errorf("mash: directory %s: %w", name, err))
		}
	}
	if _, err := a.js.Stream(ctx, sap.InboxStream(name)); err == nil {
		return true, nil
	} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return false, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	if lease, err := a.js.KeyValue(ctx, store.LeaseBucket); err == nil {
		switch e, err := lease.Get(ctx, name); {
		case err == nil:
			return !isTombstone(e.Value()), nil
		case !errors.Is(err, jetstream.ErrKeyNotFound):
			return false, fmt.Errorf("mash: lease %s: %w", name, err)
		}
	}
	return false, nil
}

// jsErrUnavailable is the JetStream API error code for "JetStream system temporarily unavailable" (no meta quorum,
// typically too few servers up to keep quorum).
const jsErrUnavailable = 10008

// unavailableText is the phrase NATS gives for jsErrUnavailable.
const unavailableText = "JetStream system temporarily unavailable"

const unavailableHint = "mash: JetStream is unavailable (a server is down, or too few servers are up to keep quorum): bring the servers back and run the command again"

func isUnavailable(err error) bool {
	var api *jetstream.APIError
	return err != nil && ((errors.As(err, &api) && api.ErrorCode == jsErrUnavailable) || strings.Contains(err.Error(), unavailableText))
}

// wordUnavailable rewords a lost-quorum error; it returns err unchanged for any other.
func wordUnavailable(err error) error {
	if isUnavailable(err) {
		return fmt.Errorf("%s: %w", unavailableHint, err)
	}
	return err
}

// streamNameInError finds the stream name in a NATS error text such as "stream inbox-a: nats: ...".
var streamNameInError = regexp.MustCompile(`\bstream ([A-Za-z0-9_.-]+):`)

const serverDownText = "is down: bring it back and run the command again"

// RewordUnavailable puts a hint in front of the raw NATS text in an already formatted message: the lost-quorum hint
// for "JetStream system temporarily unavailable", and, for "stream is offline" (err_code 10118), a line saying the
// server that holds the stream or bucket is down. It leaves a message that has a hint, or carries neither text, as it
// is.
func RewordUnavailable(msg string) string {
	if strings.Contains(msg, unavailableText) && !strings.Contains(msg, unavailableHint) {
		return unavailableHint + ": " + msg
	}
	if (strings.Contains(msg, "stream is offline") || strings.Contains(msg, "err_code="+strconv.Itoa(jsErrStreamOffline))) && !strings.Contains(msg, serverDownText) {
		what := "a stream or bucket"
		if m := streamNameInError.FindStringSubmatch(msg); m != nil {
			what = "stream " + m[1]
		}
		return "mash: the server holding " + what + " " + serverDownText + ": " + msg
	}
	return msg
}

// jsErrStreamOffline is the JetStream API error code for a stream whose server is down or not yet recovered.
const jsErrStreamOffline = 10118

// bucketDown words a NATS "stream is offline" error (err_code 10118) on a bucket: the server that holds the bucket's
// stream is down, so the bucket exists and `mash admin init` would not help. It words err_code 10008 (JetStream
// unavailable, no quorum) the same way. It returns nil for any other error.
func bucketDown(bucket string, err error) error {
	if err == nil {
		return nil
	}
	var api *jetstream.APIError
	if (errors.As(err, &api) && api.ErrorCode == jsErrStreamOffline) || strings.Contains(err.Error(), "stream is offline") {
		return fmt.Errorf("mash: the server holding bucket %s is down: bring it back and run the command again: %w", bucket, err)
	}
	if isUnavailable(err) {
		return wordUnavailable(err)
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "context deadline exceeded") {
		return fmt.Errorf("mash: bucket %s did not answer in time: check that every server is up, then run the command again: %w", bucket, err)
	}
	return nil
}
