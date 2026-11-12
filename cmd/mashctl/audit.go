package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// exitAuditIncomplete is the exit code of an audit that could not complete: no server, unreadable required input, a
// bad --compare file. 1 is --strict findings and 2 is usage.
const exitAuditIncomplete = 3

const auditUsage = "usage: mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]\n"

// auditVersion is the version field of the JSON report. It changes only when a field is removed or changes meaning.
const auditVersion = 1

// trustLabelNA is the trust_label of every renamed identity: the server holds no trust label (the receiving endpoint
// evaluates one from the signed sender header when it reads a message).
const (
	trustLabelNA   = "not-available"
	trustLabelNote = "the server holds no trust label: the receiving endpoint evaluates it when it reads a message, so compare the message counts and last sequences here and check labels at the endpoint"
)

// auditNow is the clock for the report's generated field; a test replaces it.
var auditNow = time.Now

type auditIdentity struct {
	Agent string `json:"agent"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type auditAlias struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

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

	Bytes         uint64  `json:"bytes"`
	MaxBytes      int64   `json:"max_bytes"`
	FillPercent   float64 `json:"fill_percent"`
	Evicted       uint64  `json:"evicted"`        // messages the stream took and no longer holds (a full inbox or the age limit)
	EvictedUnread uint64  `json:"evicted_unread"` // of those, at least this many were never delivered to the endpoint
}

type auditRecord struct {
	Key      string        `json:"key"`
	Identity auditIdentity `json:"identity"`
	Flat     string        `json:"flat"`
	Form     string        `json:"form"` // legacy or new
	User     string        `json:"user"`
	Zone     string        `json:"zone,omitempty"`
	Alias    *auditAlias   `json:"alias,omitempty"`
	Running  bool          `json:"running"`
	Inbox    auditInbox    `json:"inbox"`
}

type auditRename struct {
	Identity       string     `json:"identity"` // agent.owner.name
	Flat           string     `json:"flat"`
	Alias          auditAlias `json:"alias"`
	LegacyInbox    auditInbox `json:"legacy_inbox"`
	NewInbox       auditInbox `json:"new_inbox"`
	Drained        bool       `json:"drained"`
	TrustLabel     string     `json:"trust_label"`
	TrustLabelNote string     `json:"trust_label_note"`
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

type auditOrphanCreds struct {
	File   string `json:"file"`
	User   string `json:"user"`
	Staged bool   `json:"staged"` // a *.creds.next file: credentials staged by an interrupted rotation
}

type auditOrphans struct {
	Streams []auditOrphanStream `json:"streams"`
	Leases  []auditOrphanLease  `json:"leases"`
	Creds   []auditOrphanCreds  `json:"creds"`
}

// auditRetiredMarker is one retired-identity marker. It carries the retired user public key (the audit shows the other
// users' public keys too) and never a seed.
type auditRetiredMarker struct {
	Key       string `json:"key"` // the flat identity
	Agent     string `json:"agent"`
	Owner     string `json:"owner"`
	Name      string `json:"name"`
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
	if ar.Alarm {
		probs = append(probs, auditProblem{What: "retired", Item: store.RetiredBucket,
			Error: fmt.Sprintf("ALARM: the bucket stores %d of %d bytes (%.2f%%), at or over %d%% of its cap: a full bucket makes provisioning refuse new identities for good, and mash never changes an existing bucket, so recreate it with a larger cap and its markers copied before it fills (ADR 0088)", ar.Bytes, ar.MaxBytes, ar.PercentUsed, retiredAlarmPercent)})
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
			probs = append(probs, auditProblem{What: "retired", Item: m.Key, Error: "pending retired marker: a retirement was started and not finished (the name stays refused); rerun deprovision for it"})
		} else {
			ar.Final++
			if m.Marker.State != "" && m.Marker.State != store.RetiredFinal {
				// Not a state mash writes: a tampered or future-format marker. The name stays refused, so it counts as
				// final and the report keeps "final" (self-comparable), but it is not a clean completed retirement.
				probs = append(probs, auditProblem{What: "retired", Item: m.Key,
					Error: fmt.Sprintf("retired marker has an unrecognized state %q (treated as final: the name stays refused); investigate", capState(m.Marker.State))})
			}
		}
		ar.Markers = append(ar.Markers, auditRetiredMarker{Key: m.Key, Agent: m.Marker.Agent, Owner: m.Marker.Owner, Name: m.Marker.Name,
			User: m.Marker.User, State: state, RetiredAt: m.Marker.RetiredAt})
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

type auditSummary struct {
	Records       int `json:"records"`
	Legacy        int `json:"legacy"`
	New           int `json:"new"`
	Running       int `json:"running"`
	Renamed       int `json:"renamed"`
	Undrained     int `json:"undrained"`
	OrphanStreams int `json:"orphan_streams"`
	OrphanLeases  int `json:"orphan_leases"`
	OrphanCreds   int `json:"orphan_creds"`
	Problems      int `json:"problems"`
	// RetiredFinal and RetiredPending count the retired-identity markers by state. Both are in `problems` only as far as
	// a pending marker, the alarm or an unreadable bucket is a finding there.
	RetiredFinal   int `json:"retired_final"`
	RetiredPending int `json:"retired_pending"`
}

// auditReport is what `mashctl audit --json` prints.
type auditReport struct {
	Version     int            `json:"version"`
	Generated   string         `json:"generated"`
	Owner       string         `json:"owner"`
	Account     string         `json:"account"` // the account's public key (never a seed or a token); binds --compare to one deployment
	CredsDirs   []string       `json:"creds_dirs"`
	Records     []auditRecord  `json:"records"`
	Renames     []auditRename  `json:"renames"`
	Orphans     auditOrphans   `json:"orphans"`
	FencedLease []string       `json:"fenced_leases"` // lease keys holding a deprovision tombstone and no record: expected, they expire
	Retired     *auditRetired  `json:"retired"`       // always set in a report this mashctl writes; nil in an earlier report from before it
	Problems    []auditProblem `json:"problems"`
	Notes       []string       `json:"notes"` // sorted; things skipped or absent that are not findings
	Summary     auditSummary   `json:"summary"`
	Compare     *auditCompare  `json:"compare,omitempty"` // only with --compare
}

// findings is how many things --strict counts: every orphan, every undrained alias, every item the audit could not
// read (it cannot call what it did not read clean) and, with --compare, every legacy stream that moved.
func (r auditReport) findings() int {
	s := r.Summary
	n := s.OrphanStreams + s.OrphanLeases + s.OrphanCreds + s.Undrained + s.Problems
	if r.Compare != nil {
		n += r.Compare.Moved + r.Compare.Vanished + r.Compare.Recreated + r.Compare.CreationUnknown + len(r.Compare.RetiredFinalLost)
		if r.Compare.AccountUnverified {
			n++
		}
	}
	return n
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

// drained reports whether a legacy inbox can be cut off. An inbox that is gone, or one that holds no message, is
// drained. Otherwise its `deliver` consumer must have nothing pending and nothing unacknowledged, and must be
// configured to have delivered every stored message: deliver policy all (or a start sequence at or below the first
// stored sequence), no filter at all, and an ack floor that reaches the last stored sequence. The counts alone are
// not enough: a consumer recreated to skip stored messages shows zero for both. Any filter disqualifies it, even one
// equal to the stream's current subjects: the stream may have captured other subjects earlier, whose stored messages
// the filter skips. mash creates `deliver` unfiltered, so a filter means it was recreated by hand.
func (in auditInbox) drained() bool {
	if !in.Present || in.Msgs == 0 {
		return true
	}
	d := in.Deliver
	if !d.Present || d.Pending != 0 || d.AckPending != 0 {
		return false
	}
	switch d.DeliverPolicy {
	case "all":
	case "by_start_sequence":
		if d.StartSeq > in.FirstSeq {
			return false
		}
	default: // new, last, last_per_subject, by_start_time (a time cannot be compared with the stored messages), unknown
		return false
	}
	if len(d.FilterSubjects) > 0 {
		return false
	}
	return d.AckFloor >= in.LastSeq
}

// inboxProblems is the problems of one endpoint's inbox: mail lost unread. A message dropped after it was delivered is
// ordinary turnover (the oldest mail goes when the cap or the age limit is reached), so it is not a problem. An inbox
// near its byte cap is operational state, not a misconfiguration: inboxNote reports it, and --strict does not count it.
// An inbox with unread loss that is also near full says so in the same problem.
func inboxProblems(key string, in auditInbox) []auditProblem {
	if !in.Present || in.EvictedUnread == 0 {
		return nil
	}
	msg := fmt.Sprintf(
		"%d unread message(s) were evicted from this inbox (%d evicted in all; it was full or its mail aged out); the endpoint should read mail sooner, or a sender is flooding it (see docs/operations.md)",
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

// buildAudit combines the server snapshot with the creds files' public user keys. credsUsers maps a creds file to its
// user public key (or to an error); skipUsers are keys that are not endpoints' (the admin's).
func buildAudit(owner string, snap admin.AuditSnapshot, dirs []string, creds []credsFile, skipUsers map[string]bool) auditReport {
	rep := auditReport{Version: auditVersion, Owner: owner, CredsDirs: dirs,
		Records: []auditRecord{}, Renames: []auditRename{}, FencedLease: []string{}, Problems: []auditProblem{}, Notes: []string{},
		Orphans: auditOrphans{Streams: []auditOrphanStream{}, Leases: []auditOrphanLease{}, Creds: []auditOrphanCreds{}}}
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
	claimedStream, claimedLease, users := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for _, b := range snap.BadRecords {
		rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: b.Key, Error: b.Error})
		// the entry still exists: what it names is not an orphan, though the entry itself is not trusted
		claimedStream[sap.InboxStream(b.Key)], claimedLease[b.Key] = true, true
		if b.User != "" {
			users[b.User] = true
		}
	}
	for _, r := range snap.Records {
		id, err := r.Identity()
		if err != nil {
			rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: r.KeyOrName(), Error: err.Error()})
			k := r.KeyOrName()
			claimedStream[sap.InboxStream(k)], claimedLease[k] = true, true
			if r.User != "" {
				users[r.User] = true
			}
			continue
		}
		key := id.Flat()
		claimedStream[sap.InboxStream(key)], claimedLease[key], users[r.User] = true, true, true
		form := "new"
		if id.IsLegacy() {
			form = "legacy"
		}
		ar := auditRecord{Key: key, Identity: auditIdentity{Agent: id.Agent, Owner: id.Owner, Name: id.Name}, Flat: key,
			Form: form, User: r.User, Zone: r.Zone, Running: live[key], Inbox: toAuditInbox(key, streams[sap.InboxStream(key)])}
		if r.Alias != nil {
			ar.Alias = &auditAlias{Owner: r.Alias.Owner, Name: r.Alias.Name}
			aid, ok, aerr := r.AliasIdentity()
			if aerr != nil {
				// an alias that names no legacy identity: the audit cannot say which inbox is the old one, so it reports the
				// record as a problem (which fails --strict) and claims the inbox the alias's name would give, so mail
				// stored there is not reported as an orphan instead.
				rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: key, Error: "invalid alias: " + aerr.Error()})
				if r.Alias.Name != "" {
					claimedStream[sap.InboxStream(r.Alias.Name)] = true
				}
			}
			if aerr == nil && ok {
				legacy := aid.Flat()
				claimedStream[sap.InboxStream(legacy)] = true
				rn := auditRename{Identity: id.String(), Flat: key, Alias: *ar.Alias,
					LegacyInbox: toAuditInbox(legacy, streams[sap.InboxStream(legacy)]), NewInbox: ar.Inbox,
					TrustLabel: trustLabelNA, TrustLabelNote: trustLabelNote}
				rn.Drained = rn.LegacyInbox.drained()
				rep.Renames = append(rep.Renames, rn)
			}
		}
		rep.Records = append(rep.Records, ar)
		rep.Problems = append(rep.Problems, inboxProblems(key, ar.Inbox)...)
		if n := inboxNote(key, ar.Inbox); n != "" {
			rep.Notes = append(rep.Notes, n)
		}
	}
	slices.SortFunc(rep.Records, func(a, b auditRecord) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(rep.Renames, func(a, b auditRename) int { return strings.Compare(a.Flat, b.Flat) })

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
	for _, c := range creds {
		switch {
		case c.err != nil:
			rep.Problems = append(rep.Problems, auditProblem{What: "creds", Item: c.path, Error: c.err.Error()})
		case users[c.user] || skipUsers[c.user]:
		default:
			rep.Orphans.Creds = append(rep.Orphans.Creds, auditOrphanCreds{File: c.path, User: c.user, Staged: c.staged})
		}
	}
	slices.SortFunc(rep.Orphans.Creds, func(a, b auditOrphanCreds) int { return strings.Compare(a.File, b.File) })
	retired, retiredProbs := buildRetired(snap.Retired)
	rep.Retired = &retired
	rep.Problems = append(rep.Problems, retiredProbs...)
	if snap.LeaseRollup {
		rep.Problems = append(rep.Problems, auditProblem{What: "lease", Item: store.LeaseBucket,
			Error: `lease bucket allows rollup writes; run "mash admin init" to harden it`})
	}
	if snap.LeaseError != "" {
		rep.Problems = append(rep.Problems, auditProblem{What: "lease", Item: store.LeaseBucket,
			Error: "the lease bucket's configuration could not be read: " + snap.LeaseError})
	}
	slices.SortFunc(rep.Problems, func(a, b auditProblem) int {
		return strings.Compare(a.What+"\x00"+a.Item+"\x00"+a.Error, b.What+"\x00"+b.Item+"\x00"+b.Error)
	})

	s := &rep.Summary
	s.Records, s.Renamed = len(rep.Records), len(rep.Renames)
	for _, r := range rep.Records {
		if r.Form == "legacy" {
			s.Legacy++
		} else {
			s.New++
		}
		if r.Running {
			s.Running++
		}
	}
	for _, r := range rep.Renames {
		if !r.Drained {
			s.Undrained++
		}
	}
	s.RetiredFinal, s.RetiredPending = retired.Final, retired.Pending
	s.OrphanStreams, s.OrphanLeases, s.OrphanCreds, s.Problems = len(rep.Orphans.Streams), len(rep.Orphans.Leases), len(rep.Orphans.Creds), len(rep.Problems)
	return rep
}

// credsFile is one creds file found in a managed directory: its public user key, or why it could not be read.
// staged is a *.creds.next file.
type credsFile struct {
	path   string
	user   string
	err    error
	staged bool
}

// scanCreds reads the user public key of every regular *.creds and *.creds.next (staged) file in dirs. Only the JWT's
// subject is read: the seed and the token are never kept or shown, so a staged file is as safe to read as a creds
// file. A symbolic link is skipped, and so is a directory that does not exist when optional (else an error); each
// skip is returned as a note.
func scanCreds(dirs []string, optional bool) ([]credsFile, []string, error) {
	var out []credsFile
	var notes []string
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if errors.Is(err, fs.ErrNotExist) && optional {
			notes = append(notes, fmt.Sprintf("creds directory %s does not exist: nothing scanned there", config.Clean(d)))
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("mashctl: audit: creds directory: %w", err)
		}
		for _, e := range ents {
			staged := strings.HasSuffix(e.Name(), ".creds.next")
			if !staged && !strings.HasSuffix(e.Name(), ".creds") {
				continue
			}
			p := filepath.Join(d, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				notes = append(notes, fmt.Sprintf("creds file %s is a symbolic link: skipped, so its user key is not matched", config.Clean(p)))
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			u, err := credsUserKey(p)
			if err != nil {
				// the error may name a path and a parse failure, never the file's content
				out = append(out, credsFile{path: p, staged: staged, err: errors.New(config.Clean(strings.TrimPrefix(err.Error(), p+": ")))})
				continue
			}
			out = append(out, credsFile{path: p, user: u, staged: staged})
		}
	}
	slices.Sort(notes)
	return out, notes, nil
}

type auditOpts struct {
	json, strict bool
	credsDir     string
	compare      string
}

func parseAuditArgs(args []string) (auditOpts, error) {
	var o auditOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch name {
		case "--json", "--strict":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			if name == "--json" {
				o.json = true
			} else {
				o.strict = true
			}
		case "--creds-dir", "--compare":
			var v string
			switch {
			case hasInline && inline != "":
				v = inline
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				v = args[i]
			default:
				return o, fmt.Errorf("%s needs a value", name)
			}
			if name == "--creds-dir" {
				o.credsDir = v
			} else {
				o.compare = v
			}
		default:
			return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
		}
	}
	return o, nil
}

// runAudit is `mashctl audit`: a read-only listing of mash's side of the population. It never writes to the server or
// to disk.
func runAudit(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	o, err := parseAuditArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: audit: %v\n%s", err, auditUsage)
		return 2
	}
	var earlier *auditReport
	if o.compare != "" {
		earlier, err = readEarlierAudit(o.compare)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitAuditIncomplete
		}
	}
	// audit changes nothing and acts as no endpoint, so a set agent_token (which makes the writing verbs refuse) is no
	// reason to refuse it.
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, errAgentTokenSet) {
		fmt.Fprintln(stderr, err)
		return exitAuditIncomplete
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitAuditIncomplete
	}
	defer closeAdmin()
	snap, err := adm.Audit(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitAuditIncomplete
	}

	dirs, optional := []string{}, true
	if o.credsDir != "" {
		dirs, optional = []string{filepath.Clean(o.credsDir)}, false
	} else {
		for _, d := range []string{cfg.Dir, cfg.Legacy} {
			if d != "" && !slices.Contains(dirs, filepath.Clean(d)) {
				dirs = append(dirs, filepath.Clean(d))
			}
		}
	}
	creds, notes, err := scanCreds(dirs, optional)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitAuditIncomplete
	}
	skip := map[string]bool{} // the admin's and the system account's creds are in the same directory but are no endpoint's
	for _, p := range []string{cfg.AdminCreds, env("MASH_SYS_CREDS")} {
		if p == "" {
			continue
		}
		if u, err := credsUserKey(p); err == nil {
			skip[u] = true
		}
	}
	rep := buildAudit(cfg.Owner, snap, dirs, creds, skip)
	if rep.Account, err = credsAccountKey(cfg.AdminCreds); err != nil {
		fmt.Fprintln(stderr, err)
		return exitAuditIncomplete
	}
	if earlier != nil {
		if err := bindEarlierAudit(earlier, rep); err != nil {
			fmt.Fprintln(stderr, err)
			return exitAuditIncomplete
		}
	}
	rep.Generated = auditNow().UTC().Format(time.RFC3339)
	rep.Notes = append(rep.Notes, notes...)
	slices.Sort(rep.Notes)
	if earlier != nil {
		rep.Compare = compareAudits(*earlier, rep)
		rep.Compare.AccountUnverified = earlier.Account == ""
		if o.strict && rep.Compare.RetiredUnverified {
			// Like a report that lacks a required field: it cannot prove no final marker was deleted. Nothing on stdout.
			fmt.Fprintf(stderr, "mashctl: audit: --compare: %s: regenerate it with this mashctl and compare again\n", retiredUnverifiedMsg)
			return exitAuditIncomplete
		}
	}

	if o.json {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitAuditIncomplete
		}
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		writeAuditText(stdout, rep)
	}
	if o.strict && rep.findings() > 0 {
		s := rep.Summary
		moved, vanished, recreated, unknown, lost := 0, 0, 0, 0, 0
		if rep.Compare != nil {
			moved, vanished, recreated, unknown, lost = rep.Compare.Moved, rep.Compare.Vanished, rep.Compare.Recreated, rep.Compare.CreationUnknown, len(rep.Compare.RetiredFinalLost)
		}
		fmt.Fprintf(stderr, "mashctl: audit --strict: %d finding(s): %d orphan stream(s), %d orphan lease(s), %d orphan creds file(s), %d alias inbox(es) not drained, %d item(s) not readable or retired-marker finding(s), %d legacy stream(s) moved, %d vanished and %d recreated since the earlier audit, %d final retired marker(s) lost since the earlier audit\n",
			rep.findings(), s.OrphanStreams, s.OrphanLeases, s.OrphanCreds, s.Undrained, s.Problems, moved, vanished, recreated, lost)
		if unknown > 0 {
			fmt.Fprintf(stderr, "mashctl: audit --strict: the earlier audit lacks creation times for %d legacy stream(s), so it cannot show they were not deleted and recreated: regenerate it with this mashctl and compare again\n", unknown)
		}
		if rep.Compare != nil && rep.Compare.AccountUnverified {
			fmt.Fprintln(stderr, "mashctl: audit --strict: the earlier audit lacks the account field, so it cannot be tied to this deployment: regenerate it with this mashctl and compare again")
		}
		return 1
	}
	return 0
}

func inboxText(in auditInbox) string {
	if !in.Present {
		return fmt.Sprintf("stream %s\tabsent", config.Clean(in.Stream))
	}
	d := in.Deliver
	consumer := "deliver false"
	if d.Present {
		filter := "none"
		if len(d.FilterSubjects) > 0 {
			filter = strings.Join(d.FilterSubjects, ",")
		}
		consumer = fmt.Sprintf("deliver true\tpolicy %s\tstart_seq %d\tfilter %s\tack_floor %d", config.Clean(d.DeliverPolicy), d.StartSeq, config.Clean(filter), d.AckFloor)
		if d.StartTime != "" {
			consumer += "\tstart_time " + config.Clean(d.StartTime)
		}
		consumer += fmt.Sprintf("\tdelivered %d\tnum_redelivered %d", d.Delivered, d.NumRedelivered)
	}
	return fmt.Sprintf("stream %s\tmsgs %d\tfirst %d\tlast %d\tsuspended %t\tpending %d\tack_pending %d\t%s\tsubjects %s", config.Clean(in.Stream), in.Msgs, in.FirstSeq, in.LastSeq,
		in.Suspended, d.Pending, d.AckPending, consumer, config.Clean(strings.Join(in.Subjects, ",")))
}

// writeAuditText is the human form: the same facts as the JSON, one tab-separated line each.
func writeAuditText(w io.Writer, r auditReport) {
	c := config.Clean
	fmt.Fprintf(w, "mashctl audit version %d generated %s owner %s\n", r.Version, r.Generated, c(r.Owner))
	fmt.Fprintf(w, "creds directories: %s\n", c(strings.Join(r.CredsDirs, ", ")))
	fmt.Fprintf(w, "records %d (legacy %d, new %d), running %d\n", r.Summary.Records, r.Summary.Legacy, r.Summary.New, r.Summary.Running)
	for _, x := range r.Records {
		state := "stopped"
		if x.Running {
			state = "running"
		}
		alias := ""
		if x.Alias != nil {
			alias = "\talias " + c(x.Alias.Owner) + "/" + c(x.Alias.Name)
		}
		fmt.Fprintf(w, "record %s\t%s\t%s\tidentity %s.%s.%s\tuser %s%s\t%s\n", c(x.Key), x.Form, state, c(x.Identity.Agent), c(x.Identity.Owner), c(x.Identity.Name), c(x.User), alias, inboxText(x.Inbox))
	}
	fmt.Fprintf(w, "renamed identities %d, not drained %d\n", r.Summary.Renamed, r.Summary.Undrained)
	for _, x := range r.Renames {
		drained := "drained"
		if !x.Drained {
			drained = "NOT drained"
		}
		fmt.Fprintf(w, "rename %s\t%s\ttrust_label %s\n", c(x.Identity), drained, x.TrustLabel)
		fmt.Fprintf(w, "  before (legacy %s/%s)\t%s\n", c(x.Alias.Owner), c(x.Alias.Name), inboxText(x.LegacyInbox))
		fmt.Fprintf(w, "  after\t%s\n", inboxText(x.NewInbox))
	}
	if r.Summary.Renamed > 0 {
		fmt.Fprintf(w, "trust_label: %s\n", trustLabelNote)
	}
	fmt.Fprintf(w, "orphans: %d stream(s), %d lease(s), %d creds file(s)\n", r.Summary.OrphanStreams, r.Summary.OrphanLeases, r.Summary.OrphanCreds)
	for _, x := range r.Orphans.Streams {
		fmt.Fprintf(w, "orphan stream %s\tmsgs %d\tfirst %d\tlast %d\tsuspended %t\tsubjects %s\n", c(x.Stream), x.Msgs, x.FirstSeq, x.LastSeq, x.Suspended, c(strings.Join(x.Subjects, ",")))
	}
	for _, x := range r.Orphans.Leases {
		fmt.Fprintf(w, "orphan lease %s\n", c(x.Key))
	}
	for _, x := range r.Orphans.Creds {
		staged := ""
		if x.Staged {
			staged = "\tstaged"
		}
		fmt.Fprintf(w, "orphan creds %s\tuser %s%s\n", c(x.File), c(x.User), staged)
	}
	for _, k := range r.FencedLease {
		fmt.Fprintf(w, "fenced lease %s (a deprovision tombstone; it expires)\n", c(k))
	}
	for _, p := range r.Problems {
		if p.What == "inbox" { // a report about readable data, not a read failure
			fmt.Fprintf(w, "inbox %s: %s\n", c(p.Item), c(p.Error))
			continue
		}
		fmt.Fprintf(w, "not readable %s %s: %s\n", p.What, c(p.Item), c(p.Error))
	}
	if rt := r.Retired; rt != nil {
		if !rt.Present {
			fmt.Fprintf(w, "no retired bucket (%s): no retirement has been recorded\n", store.RetiredBucket)
		} else {
			if rt.MaxBytes <= 0 {
				fmt.Fprintf(w, "retired markers %d (final %d, pending %d)\tbucket %s\tbytes %d, no cap\n", rt.Final+rt.Pending, rt.Final, rt.Pending, store.RetiredBucket, rt.Bytes)
			} else {
				fmt.Fprintf(w, "retired markers %d (final %d, pending %d)\tbucket %s\tbytes %d of %d\tused %.2f%%\n", rt.Final+rt.Pending, rt.Final, rt.Pending, store.RetiredBucket, rt.Bytes, rt.MaxBytes, rt.PercentUsed)
			}
			if rt.Alarm {
				fmt.Fprintf(w, "retired bucket ALARM: at or over %d%% of its cap\n", retiredAlarmPercent)
			}
			for _, m := range rt.Markers {
				state := m.State
				if m.State == store.RetiredPending {
					state = "PENDING"
				}
				user := ""
				if m.User != "" {
					user = "\tuser " + c(m.User)
				}
				fmt.Fprintf(w, "retired marker %s\t%s\tretired_at %s%s\n", c(m.Key), state, c(m.RetiredAt), user)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "note: %s\n", c(n))
	}
	if r.Compare != nil {
		writeCompareText(w, r.Compare)
	}
}
