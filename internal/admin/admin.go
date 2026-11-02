package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Admin provisions endpoints with the account's admin credentials.
type Admin struct {
	js     jetstream.JetStream
	owner  string
	limits store.Limits
}

// SetLimits sets the caps Init, Provision and EnsureInbox use (default: store.DefaultLimits). It validates them.
func (a *Admin) SetLimits(l store.Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	a.limits = l
	return nil
}

// New returns an Admin for owner.
func New(nc *nats.Conn, owner string) (*Admin, error) {
	if !config.ValidName(owner) {
		return nil, fmt.Errorf("mash: owner %q is not a valid name", owner)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return &Admin{js: js, owner: owner, limits: store.DefaultLimits()}, nil
}

// Init creates the directory and lease buckets. It is idempotent. A bucket from before buckets had a size cap (no
// MaxBytes) is bounded in place with a stream update, keeping its keys; a bucket whose MaxBytes is smaller than the
// configured limit is raised to it; a larger one is kept (a limit is never lowered), and nothing is purged.
func (a *Admin) Init(ctx context.Context) error {
	for _, cfg := range []jetstream.KeyValueConfig{store.DirectoryConfigFor(a.limits), store.LeaseConfigFor(a.limits)} {
		if kv, err := a.js.KeyValue(ctx, cfg.Bucket); err == nil {
			st, err := kv.Status(ctx)
			if err != nil {
				return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
			}
			if bs, ok := st.(*jetstream.KeyValueBucketStatus); ok && bs.StreamInfo().Config.MaxBytes > cfg.MaxBytes {
				cfg.MaxBytes = bs.StreamInfo().Config.MaxBytes
			}
		}
		if _, err := a.js.CreateOrUpdateKeyValue(ctx, cfg); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", cfg.Bucket, err)
		}
	}
	return nil
}

func (a *Admin) dir(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
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
	// Zone, when set, provisions the endpoint into that zone: a 6-token mail subject, and an inbox that takes both
	// the zone and the legacy subject (ADR 0054). Empty is the legacy 5-token subject.
	Zone string
}

func (m Meta) validate() error {
	if m.Zone != "" && !sap.ValidZone(m.Zone) {
		return errBadZone(m.Zone)
	}
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

// Provision creates name's inbox stream, its consumer and, last, its
// directory record, so a half-done provision can simply be re-run.
func (a *Admin) Provision(ctx context.Context, name, user, account, note string, meta Meta) (rec store.Record, err error) {
	if !config.ValidName(name) {
		return store.Record{}, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	if !nkeys.IsValidPublicUserKey(user) {
		return store.Record{}, fmt.Errorf("mash: %q is not a user public key (U...)", user)
	}
	if !nkeys.IsValidPublicAccountKey(account) {
		return store.Record{}, fmt.Errorf("mash: %q is not an account public key (A...)", account)
	}
	if err := meta.validate(); err != nil {
		return store.Record{}, err
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	// The name's lock is held for the whole provision, record and stream alike, so a deprovision of the same name
	// cannot interleave with it (ADR-0048, ADR-0047). The stream helpers below are the Locked variants.
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return store.Record{}, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "provision", &err)
	defer end()
	if _, err := kv.Get(ctx, name); err == nil {
		return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", name)
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Record{}, err
	}
	// A deprovisioned name's lease key holds a tombstone until it expires: clear it, so the name works at once.
	if lease, err := a.js.KeyValue(ctx, store.LeaseBucket); err == nil {
		if err := lease.Purge(ctx, name); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
			return store.Record{}, fmt.Errorf("mash: lease: %w", err)
		}
	}
	// A retried provision finds its inbox stream already there: never let the update lower its caps. The read, the
	// check and the write run under the stream's limits lock, so a concurrent limits change cannot slip between them.
	s, err := a.createInboxLocked(ctx, lk, name, meta.Zone)
	if err != nil {
		return store.Record{}, err
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	rec = store.Record{
		Name: name, Owner: a.owner, Agent: sap.AgentName, AgentToken: sap.AgentToken,
		Account: account, User: user, MailSubject: store.MailSubjectFor(a.owner, meta.Zone, name), Zone: meta.Zone,
		Created: time.Now().UTC().Format(time.RFC3339), Note: note,
		Role: meta.Role, Host: meta.Host, Labels: meta.Labels, Description: meta.Description, Caps: sortedCaps(meta.Caps),
	}
	val, _ := json.Marshal(rec)
	if err := lk.check(ctx); err != nil {
		return store.Record{}, err
	}
	if _, err := kv.Create(ctx, name, val); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", name)
		}
		return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
	}
	return rec, nil
}

// createInboxLocked creates name's inbox stream, or updates an existing one to the configured limits unless that
// would lower a cap. The caller holds name's lock.
func (a *Admin) createInboxLocked(ctx context.Context, lk *streamLock, name, zone string) (jetstream.Stream, error) {
	existing, err := a.js.Stream(ctx, sap.InboxStream(name))
	if err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, fmt.Errorf("mash: inbox stream: %w", err)
	}
	if afterStreamRead != nil {
		afterStreamRead(a.limits.InboxMaxBytes)
	}
	if err == nil {
		if _, ok := suspendedSubjects(name, existing.CachedInfo().Config); ok {
			return nil, fmt.Errorf("mash: inbox of %s is suspended; run `mashctl resume %s` first", name, name)
		}
		if err := refuseLowering(name, existing.CachedInfo().Config, a.limits); err != nil {
			return nil, err
		}
	}
	cfg := store.InboxConfigZoned(a.owner, zone, name, a.limits)
	if err == nil && zone == "" {
		// A retried legacy provision must not drop the exact zone filter a stream already has.
		for _, sub := range existing.CachedInfo().Config.Subjects {
			if z, ok := sap.ZoneOfMailFilter(sub, a.owner, name); ok {
				cfg.Subjects = append(cfg.Subjects, sap.MailFilterZone(a.owner, z, name))
				break
			}
		}
	}
	if err := lk.check(ctx); err != nil {
		return nil, err
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
func (a *Admin) EnsureInbox(ctx context.Context, name string) ([]string, error) {
	if !config.ValidName(name) {
		return nil, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	var made []string
	s, created, err := a.ensureInboxStream(ctx, name)
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
func (a *Admin) ensureInboxStream(ctx context.Context, name string) (s jetstream.Stream, created bool, err error) {
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return nil, false, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "ensure-inbox", &err)
	defer end()
	// The zone is read under the lock, and a failed read is an error, never an empty zone: that would repair a zoned
	// endpoint's missing stream with the legacy subject only.
	zone, err := a.recordZone(ctx, name)
	if err != nil {
		return nil, false, err
	}
	return a.ensureInboxStreamLocked(ctx, lk, name, zone)
}

// ensureInboxStreamLocked finds name's inbox stream, creating it if missing, or checks an existing one against the
// configuration. The caller holds name's lock.
func (a *Admin) ensureInboxStreamLocked(ctx context.Context, lk *streamLock, name, zone string) (s jetstream.Stream, created bool, err error) {
	s, err = a.js.Stream(ctx, sap.InboxStream(name))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		if err := lk.check(ctx); err != nil {
			return nil, false, err
		}
		if s, err = a.js.CreateStream(ctx, store.InboxConfigZoned(a.owner, zone, name, a.limits)); err != nil {
			return nil, false, fmt.Errorf("mash: inbox stream: %w", err)
		}
		return s, true, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("mash: inbox stream: %w", err)
	}
	// A zoned record needs exactly the legacy filter and the exact filter of its own zone: mail sent to its recorded
	// subject must enter, and mail addressed to another zone must not. A legacy record accepts [legacy] or the legacy
	// filter plus one exact zone filter, as set-zone adds that filter before it rewrites the record.
	want := store.InboxConfigZoned(a.owner, zone, name, a.limits)
	got := s.CachedInfo().Config
	// A suspended stream is the endpoint's own: its saved subjects stand in for the parked one (ADR 0057).
	if saved, ok := suspendedSubjects(name, got); ok {
		got.Subjects = saved
	}
	var alt []string
	if zone == "" {
		for _, sub := range got.Subjects {
			if z, ok := sap.ZoneOfMailFilter(sub, a.owner, name); ok {
				alt = []string{sap.MailFilter(a.owner, name), sap.MailFilterZone(a.owner, z, name)}
				break
			}
		}
	}
	if err := checkInboxStream(name, got, want, alt); err != nil {
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

// checkInboxStream compares an existing inbox stream with want, field by field. want carries the legacy mail filter;
// a stream with the legacy filter and zoneFilter together is a zoned endpoint's inbox and is accepted too (ADR 0054).
func checkInboxStream(name string, got, want jetstream.StreamConfig, alt []string) error {
	wantSubjects := sortedCopy(want.Subjects)
	if alt != nil && slices.Equal(sortedCopy(got.Subjects), sortedCopy(alt)) {
		wantSubjects = sortedCopy(got.Subjects)
	}
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
// where they are below the configured value, and max_consumers where it differs. With no names it covers every
// directory endpoint. Lowering max_bytes or max_age is refused with an error naming the endpoint and field (nothing
// changes for that endpoint), because it could discard stored mail; the other endpoints are still processed and the
// errors are joined. It returns one line per endpoint saying what changed.
func (a *Admin) ApplyLimits(ctx context.Context, names ...string) ([]string, error) {
	if len(names) == 0 {
		recs, err := a.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			names = append(names, r.Name)
		}
	}
	var lines []string
	var errs []error
	for _, name := range names {
		line, err := a.applyInboxLimits(ctx, name)
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

func (a *Admin) applyInboxLimits(ctx context.Context, name string) (msg string, err error) {
	if !config.ValidName(name) {
		return "", fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	// The read, the check and the write run under the stream's limits lock.
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return "", err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "limits change", &err)
	defer end()
	return a.applyInboxLimitsLocked(ctx, lk, name)
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

// SetMeta changes name's role, host and labels, only those given. It merges into the stored JSON object, so every
// other field, including ones a newer mash wrote, survives, and writes with a compare-and-set against the revision
// it read, re-reading and retrying if another write got in between.
func (a *Admin) SetMeta(ctx context.Context, name string, u MetaUpdate) (store.Record, error) {
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
			return store.Record{}, fmt.Errorf("mash: capability %q: a letter, then up to 31 of a-z 0-9 _ . -", c)
		}
	}
	changeCaps := u.ClearCaps || len(u.AddCaps) > 0 || len(u.DropCaps) > 0
	for k, v := range u.Labels {
		if !store.ValidLabelKey(k) {
			return store.Record{}, fmt.Errorf("mash: label key %q: a letter, then up to 31 of a-z 0-9 _ . -", k)
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

func errBadZone(z string) error {
	return fmt.Errorf("mash: zone %q: 1-%d lower-case letters, digits and hyphens", z, sap.MaxZoneLen)
}

// recordZone is name's zone on record: empty when it has none, or when there is no record (or no directory yet).
// Any other failure to read the directory is an error.
func (a *Admin) recordZone(ctx context.Context, name string) (string, error) {
	kv, err := a.js.KeyValue(ctx, store.DirectoryBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mash: directory: %w", err)
	}
	rec, err := store.GetRecord(ctx, kv, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mash: directory %s: %w", name, err)
	}
	return rec.Zone, nil
}

// SetZone moves the existing endpoint name into zone without touching stored mail. It first appends the zone's mail
// filter to the inbox stream's subjects (an update that changes nothing else and purges nothing, so mail that a
// sender not yet updated publishes to the legacy subject still lands), then rewrites the directory record's zone and
// mail subject against the revision it read, keeping every other field. It is idempotent: an endpoint already in zone
// is returned as it is. An endpoint in another zone is refused, as no endpoint returns to legacy or changes zone in
// this version.
func (a *Admin) SetZone(ctx context.Context, name, zone string) (_ store.Record, err error) {
	if !config.ValidName(name) {
		return store.Record{}, fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	if !sap.ValidZone(zone) {
		return store.Record{}, errBadZone(zone)
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return store.Record{}, err
	}
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return store.Record{}, err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "set-zone", &err)
	defer end()
	for try := 0; try < setMetaTries; try++ {
		e, err := kv.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is not provisioned", name)
		}
		if err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		rec, err := store.ParseRecord(name, e.Value())
		if err != nil {
			return store.Record{}, err
		}
		if rec.Zone != "" && rec.Zone != zone {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is in zone %q; moving it to another zone, or back to legacy, is not supported", name, rec.Zone)
		}
		if err := a.addZoneFilterLocked(ctx, lk, name, zone); err != nil {
			return store.Record{}, err
		}
		if rec.Zone == zone {
			return rec, nil
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(e.Value(), &obj); err != nil {
			return store.Record{}, fmt.Errorf("mash: directory %s: %w", name, err)
		}
		zb, _ := json.Marshal(zone)
		mb, _ := json.Marshal(store.MailSubjectFor(rec.Owner, zone, name))
		obj["zone"], obj["mail_subject"] = zb, mb
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
	return store.Record{}, fmt.Errorf("mash: directory %s kept changing under set-zone; try again", name)
}

// addZoneFilterLocked appends the exact mail filter of zone to name's inbox stream subjects, changing nothing else.
// The stream must have exactly the legacy filter, or the legacy filter and the exact filter of zone (a resumed
// set-zone); anything else, another zone's filter or a wildcard included, is refused. The caller holds name's lock.
func (a *Admin) addZoneFilterLocked(ctx context.Context, lk *streamLock, name, zone string) error {
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		return fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := s.CachedInfo().Config
	if _, ok := suspendedSubjects(name, cfg); ok {
		return fmt.Errorf("mash: inbox of %s is suspended; run `mashctl resume %s` before set-zone", name, name)
	}
	legacy, zoned := sap.MailFilter(a.owner, name), sap.MailFilterZone(a.owner, zone, name)
	switch got := sortedCopy(cfg.Subjects); {
	case slices.Equal(got, sortedCopy([]string{legacy, zoned})):
		return nil
	case slices.Equal(got, []string{legacy}):
	default:
		return firstInboxDiff("stream", name, []inboxField{{"subjects", got, sortedCopy([]string{legacy})}})
	}
	cfg.Subjects = append(slices.Clone(cfg.Subjects), zoned)
	if err := lk.check(ctx); err != nil {
		return err
	}
	if _, err := a.js.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return nil
}

// isRevisionConflict reports a failed compare-and-set: the key's revision moved (or it was deleted) since the read.
func isRevisionConflict(err error) bool {
	var apiErr *jetstream.APIError
	return errors.Is(err, jetstream.ErrKeyExists) ||
		(errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence)
}

// Deprovision fences name's lease with a tombstone, then removes its directory record and inbox stream. The fence
// comes first: a session that is live or starting cannot take the lease back once its endpoint is gone (#10).
func (a *Admin) Deprovision(ctx context.Context, name string) (err error) {
	lk, err := a.lockStream(ctx, name)
	if err != nil {
		return err
	}
	defer lk.release(ctx)
	ctx, end := lk.guard(ctx, "deprovision", &err)
	defer end()
	return a.deprovisionLocked(ctx, lk, name)
}

// deprovisionLocked is Deprovision's body; the caller holds name's lock.
func (a *Admin) deprovisionLocked(ctx context.Context, lk *streamLock, name string) error {
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	if err := fence(ctx, lease, name); err != nil {
		return err
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	if err := kv.Purge(ctx, name); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf("mash: directory record: %w", err)
	}
	if err := lk.check(ctx); err != nil {
		return err
	}
	return a.deleteInbox(ctx, name)
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
func (a *Admin) DeprovisionDrained(ctx context.Context, name, id string, seq uint64) (err error) {
	if !config.ValidName(name) {
		return fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
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
	e, err := kv.Get(ctx, name)
	if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
		return fmt.Errorf("mash: endpoint %s has no directory record, so nothing proves it is the endpoint that was drained; run plain `deprovision` if it is a leftover", name)
	} else if err != nil {
		return fmt.Errorf("mash: directory %s: %w", name, err)
	}
	rec, err := store.ParseRecord(name, e.Value())
	if err != nil {
		return fmt.Errorf("mash: refusing to deprovision %s: its record cannot be read, so nothing proves it is the endpoint that was drained (left in place): %w", name, err)
	}
	savedUser := info.Config.Metadata[store.MetaSuspendedUser]
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
	if err := kv.Purge(ctx, name, jetstream.LastRevision(e.Revision())); err != nil {
		if isRevisionConflict(err) {
			return fmt.Errorf("mash: refusing to deprovision %s: the record changed; left in place", name)
		}
		return fmt.Errorf("mash: directory record: %w", err)
	}
	return a.fenceAndDeleteInbox(ctx, lk, lease, name)
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

// afterDeprovisionPurge is a test seam between DeprovisionIfUser's record delete and its lease fence; nil in production.
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
// before writing its record): remove that stream with a plain deprovision.
func (a *Admin) DeprovisionIfUser(ctx context.Context, name, user string) (err error) {
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
	purged := false
	for try := 0; try < setMetaTries && !purged; try++ {
		e, err := kv.Get(ctx, name)
		if errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted) {
			if try > 0 {
				return changed // it vanished after we read it: nothing proves ownership now
			}
			return refusal(name, ErrNoRecord)
		}
		if err != nil {
			return fmt.Errorf("mash: directory %s: %w", name, err)
		}
		rec, err := store.ParseRecord(name, e.Value())
		if err != nil {
			return fmt.Errorf("mash: refusing to deprovision %s: its record cannot be read, so nothing proves the key owns it (left in place): %w", name, err)
		}
		if rec.User != user {
			if try > 0 {
				return changed
			}
			return refusal(name, ErrRecordOtherUser)
		}
		if afterDeprovisionGet != nil {
			afterDeprovisionGet()
		}
		if err := lk.check(ctx); err != nil {
			return err
		}
		err = kv.Purge(ctx, name, jetstream.LastRevision(e.Revision()))
		switch {
		case err == nil:
			purged = true
		case isRevisionConflict(err):
			// Another admin changed the record (SetMeta bumps the revision). Re-read: if it is still this user's,
			// retry on the new revision; a different user or a vanished record refuses on the next pass.
		default:
			return fmt.Errorf("mash: directory record: %w", err)
		}
	}
	if !purged {
		return changed
	}
	if afterDeprovisionPurge != nil {
		afterDeprovisionPurge()
	}
	return a.fenceAndDeleteInbox(ctx, lk, lease, name)
}

// IsRefusal reports whether err is a refusal of DeprovisionIfUser: the call touched nothing. Any other error from it
// may come after the record was deleted (the lease fence or the inbox delete failed, or the connection broke).
func IsRefusal(err error) bool {
	var r *refusedError
	return errors.As(err, &r)
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

// List returns every directory record, sorted by name.
func (a *Admin) List(ctx context.Context) ([]store.Record, error) {
	kv, err := a.dir(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(keys)
	var out []store.Record
	for _, k := range keys {
		r, err := store.GetRecord(ctx, kv, k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
