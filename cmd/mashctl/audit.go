package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// This file sorts the server snapshot into what `mashctl doctor` judges: the directory records against the inbox
// streams, the mail an inbox lost unread, and the retired-name bucket. It reads and changes nothing.

type auditDeliver struct {
	Present    bool   `json:"present"`
	Pending    uint64 `json:"pending"`
	AckPending int    `json:"ack_pending"`

	DeliverPolicy  string   `json:"deliver_policy,omitempty"`
	StartSeq       uint64   `json:"start_seq"`
	StartTime      string   `json:"start_time,omitempty"`
	FilterSubjects []string `json:"filter_subjects"`
	AckFloor       uint64   `json:"ack_floor"` // stream sequence
	Delivered      uint64   `json:"delivered"` // stream sequence

	DeliveredConsumerSeq uint64 `json:"delivered_consumer_seq"`
	AckFloorConsumerSeq  uint64 `json:"ack_floor_consumer_seq"`
	DeliveredLast        string `json:"delivered_last,omitempty"` // RFC 3339, UTC; absent when the server reports none
	AckFloorLast         string `json:"ack_floor_last,omitempty"`
	NumRedelivered       int    `json:"num_redelivered"`
	Created              string `json:"created,omitempty"` // when the consumer was created, RFC 3339 UTC; absent when the server reports none
}

type auditInbox struct {
	Stream    string       `json:"stream"`
	Present   bool         `json:"present"`
	Msgs      uint64       `json:"msgs"`
	FirstSeq  uint64       `json:"first_seq"`
	LastSeq   uint64       `json:"last_seq"`
	LastTime  string       `json:"last_time,omitempty"` // when the stream last stored a message, RFC 3339 UTC
	Created   string       `json:"created,omitempty"`   // when the stream was created, RFC 3339 UTC; absent when the server reports none
	Subjects  []string     `json:"subjects"`
	Suspended bool         `json:"suspended"`
	Deliver   auditDeliver `json:"deliver"`

	Bytes       uint64  `json:"bytes"`
	MaxBytes    int64   `json:"max_bytes"`
	FillPercent float64 `json:"fill_percent"`
	Evicted     uint64  `json:"evicted"` // messages the stream took and no longer holds (a full inbox or the age limit)
	// EvictedUnread is a lower bound: of those, at least this many were never delivered to the endpoint. It is derived
	// from the consumer's sequences, so mail that was fetched but never shown to the user, or that was redelivered, can
	// make it read low; 0 does not prove nothing was lost unread.
	EvictedUnread uint64 `json:"evicted_unread"`
}

type auditRecord struct {
	Key      string     `json:"key"`     // the directory key: the address's flat key
	Address  string     `json:"address"` // owner.group.instance
	Owner    string     `json:"owner"`
	Group    string     `json:"group"`
	Instance string     `json:"instance"`
	User     string     `json:"user"`
	Running  bool       `json:"running"`
	Inbox    auditInbox `json:"inbox"`
}

type auditOrphanStream struct {
	Stream    string   `json:"stream"`
	Msgs      uint64   `json:"msgs"`
	FirstSeq  uint64   `json:"first_seq"`
	LastSeq   uint64   `json:"last_seq"`
	Subjects  []string `json:"subjects"`
	Suspended bool     `json:"suspended"`
}

type auditOrphanLease struct {
	Key string `json:"key"`
}

type auditOrphans struct {
	Streams []auditOrphanStream `json:"streams"`
	Leases  []auditOrphanLease  `json:"leases"`
}

// auditRetiredMarker is one retired-identity marker. It carries the retired user public key (the audit shows the other
// users' public keys too) and never a seed.
type auditRetiredMarker struct {
	Key       string `json:"key"` // the flat key
	Address   string `json:"address"`
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Instance  string `json:"instance"`
	User      string `json:"user,omitempty"`
	State     string `json:"state"`      // pending or final (a marker with no state counts as final)
	RetiredAt string `json:"retired_at"` // as the marker holds it, RFC 3339 UTC
}

// auditRetired is the retired-identity bucket `mash-retired` (ADR 0088).
type auditRetired struct {
	Present     bool                 `json:"present"` // false: no bucket, so no retirement ever happened
	Final       int                  `json:"final"`
	Pending     int                  `json:"pending"`
	Bytes       uint64               `json:"bytes"`        // bytes the bucket stores
	MaxBytes    int64                `json:"max_bytes"`    // the bucket's cap (store.RetiredMaxBytes for a bucket mash made)
	PercentUsed float64              `json:"percent_used"` // bytes / max_bytes, 0 to 100, two decimals
	Alarm       bool                 `json:"alarm"`        // bytes are at or over retiredAlarmPercent of the cap
	Markers     []auditRetiredMarker `json:"markers"`      // sorted by key
}

// retiredAlarmPercent is the share of the bucket's cap at which the audit alarms (ADR 0088).
const retiredAlarmPercent = 50

// retiredAlarm reports whether used bytes are at or over the alarm share of max. A bucket with no cap never alarms.
func retiredAlarm(used uint64, max int64) bool {
	return max > 0 && used*100 >= uint64(max)*retiredAlarmPercent
}

// buildRetired is the retired section and the findings it carries: a bucket that fails its configuration check, a
// marker that cannot be read, a pending marker (an unfinished retirement) and the alarm.
func buildRetired(snap admin.AuditRetired) (auditRetired, []auditProblem) {
	ar := auditRetired{Present: snap.Present, Markers: []auditRetiredMarker{}}
	var probs []auditProblem
	if !snap.Present {
		return ar, probs
	}
	ar.Bytes, ar.MaxBytes = snap.Bytes, snap.MaxBytes
	if ar.MaxBytes <= 0 {
		// No size cap (0 or -1 on the stream). Do not invent one: the percent is 0 and it never alarms.
		ar.MaxBytes = 0
		probs = append(probs, auditProblem{What: "retired", Item: store.RetiredBucket,
			Error: "the bucket has no size cap: ADR 0088 gives it 64 MiB so a full bucket refuses new markers, and an uncapped one never alarms; recreate it with the cap, copying its markers"})
	} else {
		ar.PercentUsed = math.Round(float64(ar.Bytes)*10000/float64(ar.MaxBytes)) / 100
		ar.Alarm = retiredAlarm(ar.Bytes, ar.MaxBytes)
	}
	if snap.ConfigError != "" {
		probs = append(probs, auditProblem{What: "retired", Item: store.RetiredBucket, Error: snap.ConfigError})
	}
	for _, m := range snap.Markers {
		if m.Error != "" {
			probs = append(probs, auditProblem{What: "retired", Item: m.Key, Error: m.Error})
			continue
		}
		state := store.RetiredFinal
		if m.Marker.IsPending() {
			state = store.RetiredPending
			ar.Pending++
			probs = append(probs, auditProblem{What: "retired", Item: m.Key, Error: "pending retired marker: a retirement was started and not finished (the name stays refused); rerun mashctl endpoint delete for it"})
		} else {
			ar.Final++
			if m.Marker.State != "" && m.Marker.State != store.RetiredFinal {
				// Not a state mash writes: a tampered or future-format marker. The name stays refused, so it counts as
				// final and the report keeps "final" (self-comparable), but it is not a clean completed retirement.
				probs = append(probs, auditProblem{What: "retired", Item: m.Key,
					Error: fmt.Sprintf("retired marker has an unrecognized state %q (treated as final: the name stays refused); investigate", capState(m.Marker.State))})
			}
		}
		ar.Markers = append(ar.Markers, auditRetiredMarker{Key: m.Key, Address: m.Marker.Owner + "." + m.Marker.Group + "." + m.Marker.Instance,
			Owner: m.Marker.Owner, Group: m.Marker.Group, Instance: m.Marker.Instance, User: m.Marker.User, State: state, RetiredAt: m.Marker.RetiredAt})
	}
	return ar, probs
}

// capState shortens a marker's state for a message, so a hostile value cannot flood the output.
func capState(s string) string {
	const max = 40
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "..."
	}
	return s
}

type auditProblem struct {
	What  string `json:"what"` // record, creds, retired, lease or inbox
	Item  string `json:"item"` // the directory key or the file
	Error string `json:"error"`
}

func toAuditInbox(name string, st *admin.AuditStream) auditInbox {
	in := auditInbox{Stream: sap.InboxStream(name), Subjects: []string{}}
	if st == nil {
		return in
	}
	in.Present, in.Msgs, in.FirstSeq, in.LastSeq, in.Suspended = true, st.Msgs, st.FirstSeq, st.LastSeq, st.Suspended
	in.LastTime = auditTime(&st.LastTime)
	in.Created = auditTime(&st.Created)
	in.Subjects = append(in.Subjects, st.Subjects...)
	in.Bytes, in.MaxBytes = st.Bytes, st.MaxBytes
	is := store.InboxState{FirstSeq: st.FirstSeq, LastSeq: st.LastSeq, Bytes: st.Bytes, MaxBytes: st.MaxBytes}
	if d := st.Deliver; d != nil && d.DeliverPolicy == "all" {
		is.HaveConsumer, is.DeliveredStream, is.DeliveredConsumer = true, d.Delivered, d.DeliveredConsumer
	}
	ev := store.Eviction(is)
	in.FillPercent, in.Evicted, in.EvictedUnread = math.Round(ev.Fill*100)/100, ev.Dropped, ev.Unread
	if st.Deliver != nil {
		d := st.Deliver
		in.Deliver = auditDeliver{Present: true, Pending: d.Pending, AckPending: d.AckPending, DeliverPolicy: d.DeliverPolicy,
			StartSeq: d.StartSeq, StartTime: d.StartTime, FilterSubjects: append([]string{}, d.FilterSubjects...),
			AckFloor: d.AckFloor, Delivered: d.Delivered, DeliveredConsumerSeq: d.DeliveredConsumer, AckFloorConsumerSeq: d.AckFloorConsumer,
			DeliveredLast: auditTime(d.DeliveredLast), AckFloorLast: auditTime(d.AckFloorLast), NumRedelivered: d.NumRedelivered, Created: auditTime(&d.Created)}
	}
	return in
}

// auditTime is t as RFC 3339 UTC with fractional seconds, or "" for nil or the zero time.
func auditTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// inboxProblems is the problems of one endpoint's inbox: mail lost unread. A message dropped after it was delivered is
// ordinary turnover (the oldest mail goes when the cap or the age limit is reached), so it is not a problem. An inbox
// near its byte cap is operational state, not a misconfiguration: inboxNote reports it, and --strict does not count it.
// An inbox with unread loss that is also near full says so in the same problem. The count is a lower bound (the
// consumer's sequences cannot see mail that was fetched and never shown, or redelivered), so it is worded "at least".
func inboxProblems(key string, in auditInbox) []auditProblem {
	if !in.Present || in.EvictedUnread == 0 {
		return nil
	}
	msg := fmt.Sprintf(
		"at least %d unread message(s) lost: evicted from this inbox (%d evicted in all; it was full or its mail aged out); the endpoint should read mail sooner, or a sender is flooding it (see docs/operations.md)",
		in.EvictedUnread, in.Evicted)
	if in.FillPercent >= store.InboxNearFullPercent {
		msg += fmt.Sprintf("; the inbox is also %.0f%% full (%d of %d bytes)", in.FillPercent, in.Bytes, in.MaxBytes)
	}
	return []auditProblem{{What: "inbox", Item: key, Error: msg}}
}

// inboxNote is the note for an inbox near its byte cap that has lost no mail unread ("" otherwise). It is a note, not
// a problem: it does not count toward --strict.
func inboxNote(key string, in auditInbox) string {
	if !in.Present || in.EvictedUnread > 0 || in.FillPercent < store.InboxNearFullPercent {
		return ""
	}
	return fmt.Sprintf("inbox %s is %.0f%% full (%d of %d bytes); the oldest mail is evicted at 100%%", key, in.FillPercent, in.Bytes, in.MaxBytes)
}

// auditReport is the snapshot sorted into what doctor judges.
type auditReport struct {
	Records     []auditRecord
	Orphans     auditOrphans
	FencedLease []string // lease keys holding a deprovision tombstone and no record: expected, they expire
	Retired     *auditRetired
	Problems    []auditProblem
	Notes       []string // sorted; inboxes near full
}

// buildAudit sorts the server snapshot into records with their inboxes, orphans, retired-name findings and problems.
func buildAudit(snap admin.AuditSnapshot) auditReport {
	rep := auditReport{Records: []auditRecord{}, FencedLease: []string{}, Problems: []auditProblem{}, Notes: []string{},
		Orphans: auditOrphans{Streams: []auditOrphanStream{}, Leases: []auditOrphanLease{}}}
	streams := map[string]*admin.AuditStream{}
	for i := range snap.Streams {
		streams[snap.Streams[i].Name] = &snap.Streams[i]
	}
	live := map[string]bool{}
	for _, l := range snap.Leases {
		if !l.Tombstone {
			live[l.Key] = true
		}
	}
	claimedStream, claimedLease := map[string]bool{}, map[string]bool{}

	for _, b := range snap.BadRecords {
		rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: b.Key, Error: b.Error})
		// the entry still exists: what it names is not an orphan, though the entry itself is not trusted
		claimedStream[sap.InboxStream(b.Key)], claimedLease[b.Key] = true, true
	}
	for _, r := range snap.Records {
		addr, err := r.Address()
		if err != nil {
			// a record that fails ParseRecord is in snap.BadRecords; this is a record whose tokens are not an address
			rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: r.Owner + "_" + r.Group + "_" + r.Instance, Error: err.Error()})
			continue
		}
		key := addr.Flat()
		claimedStream[sap.InboxStream(key)], claimedLease[key] = true, true
		ar := auditRecord{Key: key, Address: addr.String(), Owner: addr.Owner, Group: addr.Group, Instance: addr.Instance,
			User: r.User, Running: live[key], Inbox: toAuditInbox(key, streams[sap.InboxStream(key)])}
		rep.Records = append(rep.Records, ar)
		rep.Problems = append(rep.Problems, inboxProblems(key, ar.Inbox)...)
		if n := inboxNote(key, ar.Inbox); n != "" {
			rep.Notes = append(rep.Notes, n)
		}
	}
	slices.SortFunc(rep.Records, func(a, b auditRecord) int { return strings.Compare(a.Key, b.Key) })

	for _, st := range snap.Streams {
		if !claimedStream[st.Name] {
			rep.Orphans.Streams = append(rep.Orphans.Streams, auditOrphanStream{Stream: st.Name, Msgs: st.Msgs, FirstSeq: st.FirstSeq,
				LastSeq: st.LastSeq, Subjects: append([]string{}, st.Subjects...), Suspended: st.Suspended})
		}
	}
	for _, l := range snap.Leases {
		switch {
		case claimedLease[l.Key]:
		case l.Tombstone:
			rep.FencedLease = append(rep.FencedLease, l.Key)
		default:
			rep.Orphans.Leases = append(rep.Orphans.Leases, auditOrphanLease{Key: l.Key})
		}
	}
	retired, retiredProbs := buildRetired(snap.Retired)
	rep.Retired = &retired
	rep.Problems = append(rep.Problems, retiredProbs...)
	slices.SortFunc(rep.Problems, func(a, b auditProblem) int {
		return strings.Compare(a.What+"\x00"+a.Item+"\x00"+a.Error, b.What+"\x00"+b.Item+"\x00"+b.Error)
	})
	slices.Sort(rep.Notes)
	return rep
}
