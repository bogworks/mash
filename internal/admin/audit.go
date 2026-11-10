package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// AuditConsumer is the drain state of an inbox stream's durable consumer (store.Consumer).
type AuditConsumer struct {
	Pending    uint64 // messages not yet delivered
	AckPending int    // delivered and not yet acknowledged

	DeliverPolicy  string   // all, last, new, by_start_sequence, by_start_time or last_per_subject
	StartSeq       uint64   // the configured start sequence (0 unless by_start_sequence)
	StartTime      string   // the configured start time, RFC 3339 UTC (empty unless by_start_time)
	FilterSubjects []string // the configured filter subject(s), sorted; empty when unfiltered
	AckFloor       uint64   // the stream sequence at or below which every message is acknowledged
	Delivered      uint64   // the highest stream sequence delivered

	DeliveredConsumer uint64     // the consumer sequence of the last delivery
	AckFloorConsumer  uint64     // the consumer sequence of the ack floor
	DeliveredLast     *time.Time // when the last delivery was made; nil when the server reports none
	AckFloorLast      *time.Time // when the ack floor last moved; nil when the server reports none
	NumRedelivered    int        // messages redelivered and not yet acknowledged
	Created           time.Time  // when the consumer was created; zero when the server reports none
}

// AuditStream is what the server shows of one inbox stream.
type AuditStream struct {
	Name      string // the stream name, inbox-<key>
	Msgs      uint64
	FirstSeq  uint64
	LastSeq   uint64
	Created   time.Time // when the stream was created; zero when the server reports none
	LastTime  time.Time // when the stream last stored a message; zero for a stream that never held one
	Subjects  []string  // as configured; for a suspended stream the parked subject
	Suspended bool
	Deliver   *AuditConsumer // nil when the stream has no deliver consumer
}

// AuditBadRecord is a directory key whose value does not parse as a record.
type AuditBadRecord struct {
	Key   string
	Error string
	User  string // the raw value's "user" field if it still yields one; used only to claim the creds, never trusted
}

// AuditLease is one lease key. Tombstone is the fence a deprovision leaves until it expires.
type AuditLease struct {
	Key       string
	Tombstone bool
}

// AuditSnapshot is mash's side of the population as the server holds it, each list sorted. It is read only.
type AuditSnapshot struct {
	Records    []store.Record // sorted by directory key
	BadRecords []AuditBadRecord
	Streams    []AuditStream // every inbox-* stream, sorted by name
	Leases     []AuditLease  // every lease key except the admin locks, sorted by key
}

// Audit reads the directory, the inbox streams and the lease keys and changes nothing. A value that does not parse
// is reported in BadRecords, not as an error, so one damaged record does not hide the rest. A key or stream that
// disappears while it is read is skipped.
func (a *Admin) Audit(ctx context.Context) (AuditSnapshot, error) {
	var snap AuditSnapshot
	kv, err := a.dir(ctx)
	if err != nil {
		return snap, err
	}
	keys, err := kv.Keys(ctx)
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		return snap, fmt.Errorf("mash: directory: %w", err)
	}
	slices.Sort(keys)
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return snap, fmt.Errorf("mash: directory %s: %w", k, err)
		}
		r, err := store.ParseRecord(k, e.Value())
		if err != nil {
			var raw struct {
				User string `json:"user"`
			}
			_ = json.Unmarshal(e.Value(), &raw)
			snap.BadRecords = append(snap.BadRecords, AuditBadRecord{Key: k, Error: err.Error(), User: raw.User})
			continue
		}
		snap.Records = append(snap.Records, r)
	}

	names := a.js.StreamNames(ctx)
	var streams []string
	for n := range names.Name() {
		if strings.HasPrefix(n, sap.InboxStream("")) {
			streams = append(streams, n)
		}
	}
	if err := names.Err(); err != nil {
		return snap, fmt.Errorf("mash: list streams: %w", err)
	}
	slices.Sort(streams)
	for _, n := range streams {
		s, err := a.js.Stream(ctx, n)
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			continue
		}
		if err != nil {
			return snap, fmt.Errorf("mash: stream %s: %w", n, err)
		}
		info, err := s.Info(ctx)
		if err != nil {
			return snap, fmt.Errorf("mash: stream %s: %w", n, err)
		}
		st := AuditStream{Name: n, Msgs: info.State.Msgs, FirstSeq: info.State.FirstSeq, LastSeq: info.State.LastSeq, LastTime: info.State.LastTime, Created: info.Created,
			Subjects: slices.Clone(info.Config.Subjects)}
		slices.Sort(st.Subjects)
		_, st.Suspended = suspendedSubjects(strings.TrimPrefix(n, sap.InboxStream("")), info.Config)
		c, err := s.Consumer(ctx, store.Consumer)
		switch {
		case errors.Is(err, jetstream.ErrConsumerNotFound):
		case err != nil:
			return snap, fmt.Errorf("mash: stream %s consumer: %w", n, err)
		default:
			ci, err := c.Info(ctx)
			if err != nil {
				return snap, fmt.Errorf("mash: stream %s consumer: %w", n, err)
			}
			cc := ci.Config
			ac := &AuditConsumer{Pending: ci.NumPending, AckPending: ci.NumAckPending, DeliverPolicy: deliverPolicyName(cc.DeliverPolicy),
				StartSeq: cc.OptStartSeq, AckFloor: ci.AckFloor.Stream, Delivered: ci.Delivered.Stream,
				DeliveredConsumer: ci.Delivered.Consumer, AckFloorConsumer: ci.AckFloor.Consumer,
				DeliveredLast: ci.Delivered.Last, AckFloorLast: ci.AckFloor.Last, NumRedelivered: ci.NumRedelivered, Created: ci.Created}
			if cc.OptStartTime != nil {
				ac.StartTime = cc.OptStartTime.UTC().Format(time.RFC3339)
			}
			if cc.FilterSubject != "" {
				ac.FilterSubjects = append(ac.FilterSubjects, cc.FilterSubject)
			}
			ac.FilterSubjects = append(ac.FilterSubjects, cc.FilterSubjects...)
			slices.Sort(ac.FilterSubjects)
			st.Deliver = ac
		}
		snap.Streams = append(snap.Streams, st)
	}

	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return snap, nil
	}
	if err != nil {
		return snap, fmt.Errorf("mash: lease bucket: %w", err)
	}
	lkeys, err := lease.Keys(ctx)
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		return snap, fmt.Errorf("mash: lease bucket: %w", err)
	}
	slices.Sort(lkeys)
	for _, k := range lkeys {
		if strings.HasPrefix(k, lockKeyPrefix) { // an admin operation's lock, not an endpoint's lease
			continue
		}
		e, err := lease.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return snap, fmt.Errorf("mash: lease %s: %w", k, err)
		}
		var lv struct {
			Deprovisioned bool `json:"deprovisioned"`
		}
		_ = json.Unmarshal(e.Value(), &lv)
		snap.Leases = append(snap.Leases, AuditLease{Key: k, Tombstone: lv.Deprovisioned})
	}
	return snap, nil
}

func deliverPolicyName(p jetstream.DeliverPolicy) string {
	switch p {
	case jetstream.DeliverAllPolicy:
		return "all"
	case jetstream.DeliverLastPolicy:
		return "last"
	case jetstream.DeliverNewPolicy:
		return "new"
	case jetstream.DeliverByStartSequencePolicy:
		return "by_start_sequence"
	case jetstream.DeliverByStartTimePolicy:
		return "by_start_time"
	case jetstream.DeliverLastPerSubjectPolicy:
		return "last_per_subject"
	}
	return fmt.Sprintf("unknown(%d)", int(p))
}
