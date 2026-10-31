package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	Role, Host string
	Labels     map[string]string
}

func (m Meta) validate() error {
	for _, f := range []struct{ name, v string }{{"role", m.Role}, {"host", m.Host}} {
		if err := store.ValidMeta(f.name, f.v); err != nil {
			return err
		}
	}
	return store.ValidLabels(m.Labels)
}

// Provision creates name's inbox stream, its consumer and, last, its
// directory record, so a half-done provision can simply be re-run.
func (a *Admin) Provision(ctx context.Context, name, user, account, note string, meta Meta) (store.Record, error) {
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
	s, err := a.js.CreateOrUpdateStream(ctx, store.InboxConfigFor(a.owner, name, a.limits))
	if err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox stream: %w", err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		return store.Record{}, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	rec := store.Record{
		Name: name, Owner: a.owner, Agent: sap.AgentName, AgentToken: sap.AgentToken,
		Account: account, User: user, MailSubject: sap.For(a.owner, name).Mail,
		Created: time.Now().UTC().Format(time.RFC3339), Note: note,
		Role: meta.Role, Host: meta.Host, Labels: meta.Labels,
	}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, name, val); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return store.Record{}, fmt.Errorf("mash: endpoint %s is already provisioned", name)
		}
		return store.Record{}, fmt.Errorf("mash: directory record: %w", err)
	}
	return rec, nil
}

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
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		if s, err = a.js.CreateStream(ctx, store.InboxConfigFor(a.owner, name, a.limits)); err != nil {
			return nil, fmt.Errorf("mash: inbox stream: %w", err)
		}
		made = append(made, "stream")
	} else if err != nil {
		return nil, fmt.Errorf("mash: inbox stream: %w", err)
	} else if err := checkInboxStream(name, s.CachedInfo().Config, store.InboxConfigFor(a.owner, name, a.limits)); err != nil {
		return nil, err
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

// checkInboxStream compares an existing inbox stream with want, field by field.
func checkInboxStream(name string, got, want jetstream.StreamConfig) error {
	return firstInboxDiff("stream", name, []inboxField{
		{"subjects", sortedCopy(got.Subjects), sortedCopy(want.Subjects)},
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

func (a *Admin) applyInboxLimits(ctx context.Context, name string) (string, error) {
	if !config.ValidName(name) {
		return "", fmt.Errorf("mash: endpoint name %q: 1-48 lower-case letters, digits and single hyphens", name)
	}
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		return "", fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	cfg := s.CachedInfo().Config
	l := a.limits
	if cfg.MaxBytes > l.InboxMaxBytes {
		return "", fmt.Errorf("mash: inbox of %s has max_bytes %d, above the configured %d; mash never lowers a limit, because that could discard stored mail; "+
			"to lower it, run `mash admin deprovision %s` and provision it again", name, cfg.MaxBytes, l.InboxMaxBytes, name)
	}
	if cfg.MaxAge > l.InboxMaxAge {
		return "", fmt.Errorf("mash: inbox of %s has max_age %s, above the configured %s; mash never lowers a limit, because that could discard stored mail; "+
			"to lower it, run `mash admin deprovision %s` and provision it again", name, cfg.MaxAge, l.InboxMaxAge, name)
	}
	var changed []string
	if cfg.MaxBytes < l.InboxMaxBytes {
		changed = append(changed, fmt.Sprintf("max_bytes %d -> %d", cfg.MaxBytes, l.InboxMaxBytes))
		cfg.MaxBytes = l.InboxMaxBytes
	}
	if cfg.MaxAge < l.InboxMaxAge {
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
	if _, err := a.js.UpdateStream(ctx, cfg); err != nil {
		return "", fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return name + ": " + strings.Join(changed, ", "), nil
}

// MetaUpdate names the metadata fields SetMeta changes; a nil field is left alone and a pointer to "" clears it.
// Labels is applied per key: a value sets that label, an empty value clears it, other labels are left alone.
type MetaUpdate struct {
	Role, Host *string
	Labels     map[string]string
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

// isRevisionConflict reports a failed compare-and-set: the key's revision moved (or it was deleted) since the read.
func isRevisionConflict(err error) bool {
	var apiErr *jetstream.APIError
	return errors.Is(err, jetstream.ErrKeyExists) ||
		(errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence)
}

// Deprovision fences name's lease with a tombstone, then removes its directory record and inbox stream. The fence
// comes first: a session that is live or starting cannot take the lease back once its endpoint is gone (#10).
func (a *Admin) Deprovision(ctx context.Context, name string) error {
	kv, err := a.dir(ctx)
	if err != nil {
		return err
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		return fmt.Errorf("mash: lease bucket: %w", err)
	}
	if _, err := lease.Put(ctx, name, store.LeaseTombstone()); err != nil {
		return fmt.Errorf("mash: lease: %w", err)
	}
	if err := kv.Purge(ctx, name); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return fmt.Errorf("mash: directory record: %w", err)
	}
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
