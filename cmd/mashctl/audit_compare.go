package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// auditCompareStream is one legacy inbox stream across two audits. A stream "moved" when it is present now and its
// last sequence differs from the earlier one (a new message was stored) or its `deliver` consumer's delivered stream
// sequence differs (a message was read). The ack floor is reported but does not by itself fail --strict.
type auditCompareStream struct {
	Stream        string `json:"stream"`
	PresentBefore bool   `json:"present_before"`
	PresentNow    bool   `json:"present_now"`

	LastSeqBefore   uint64 `json:"last_seq_before"`
	LastSeqNow      uint64 `json:"last_seq_now"`
	LastSeqMoved    bool   `json:"last_seq_moved"`
	DeliveredBefore uint64 `json:"delivered_before"`
	DeliveredNow    uint64 `json:"delivered_now"`
	DeliveredMoved  bool   `json:"delivered_moved"`
	AckFloorBefore  uint64 `json:"ack_floor_before"`
	AckFloorNow     uint64 `json:"ack_floor_now"`
	AckFloorMoved   bool   `json:"ack_floor_moved"`

	// Vanished: present in the earlier audit and absent now, and not retired. The mail in it may have been lost.
	// Retired: present and drained in the earlier audit, and its directory record is gone now too (deprovisioned).
	// The *_before fields keep what the earlier audit showed.
	Vanished bool `json:"vanished"`
	Retired  bool `json:"retired"`

	// Creation times of the stream and of its `deliver` consumer, as the earlier and the current audit show them.
	// Recreated: present in both and a creation time differs, so the stream (or its consumer) was deleted and made
	// again and equal sequences prove nothing. CreationUnknown: present in both and a creation time is missing in
	// either audit (an earlier audit made before `created` existed), so recreation can be neither shown nor ruled out.
	CreatedBefore        string `json:"created_before,omitempty"`
	CreatedNow           string `json:"created_now,omitempty"`
	DeliverCreatedBefore string `json:"deliver_created_before,omitempty"`
	DeliverCreatedNow    string `json:"deliver_created_now,omitempty"`
	Recreated            bool   `json:"recreated"`
	CreationUnknown      bool   `json:"creation_unknown"`
}

// auditCompare is the current audit set against an earlier one (--compare).
type auditCompare struct {
	AgainstGenerated string               `json:"against_generated"`
	Streams          []auditCompareStream `json:"streams"`
	Removed          []string             `json:"identities_removed"` // record keys present before and absent now
	Added            []string             `json:"identities_added"`   // record keys absent before and present now
	Moved            int                  `json:"moved"`              // legacy streams whose last_seq or delivered sequence moved
	Vanished         int                  `json:"vanished"`           // legacy inboxes present before, absent now, not retired: fails --strict
	Recreated        int                  `json:"recreated"`          // legacy streams or deliver consumers present in both audits with a different creation time: fails --strict
	CreationUnknown  int                  `json:"creation_unknown"`   // present in both and a creation time is missing (an earlier audit without `created`): fails --strict
	Retired          int                  `json:"retired"`            // legacy inboxes drained before whose record and stream are both gone: shown only
	// AccountUnverified: the earlier audit has no `account` (made before it existed), so it cannot be tied to this
	// deployment. Shown always; fails --strict, like missing creation times.
	AccountUnverified bool `json:"account_unverified"`
}

// earlierWhen says when an earlier inbox must carry a field.
type earlierWhen int

const (
	whenAlways         earlierWhen = iota // every inbox the report lists
	whenInboxPresent                      // the inbox is present (`present` is true)
	whenDeliverPresent                    // the inbox is present and its `deliver` consumer is present
)

// earlierInboxFields is the ONE table of what an earlier report must carry for each legacy inbox it lists (a record
// of form legacy: `inbox`; a rename: `legacy_inbox`). Each is a field the comparison or the drain rule reads, so a
// report without it could make a vanished inbox read as drained (a missing `msgs` is 0) or hide a recreation. A field
// counts as present when its key is there and is not null, so `msgs: 0` is present and a missing `msgs` is not. The
// validator, docs/audit.md and the tests all iterate this table. Fields the writer omits when empty or unknown and
// that nothing reads (`last_time`, `subjects`, `start_time`, `delivered_last`, ...) are not listed.
var earlierInboxFields = []struct {
	Path string // relative to the inbox, with dots for the nested `deliver` object
	When earlierWhen
	Why  string
}{
	{"stream", whenAlways, "names the stream the inbox is compared by"},
	{"present", whenAlways, "whether the stream existed"},
	{"msgs", whenInboxPresent, "stored message count: 0 makes the inbox drained"},
	{"first_seq", whenInboxPresent, "drain rule: a start sequence at or below it"},
	{"last_seq", whenInboxPresent, "movement; the ack floor must reach it"},
	{"created", whenInboxPresent, "recreation check for the stream"},
	{"suspended", whenInboxPresent, "state of the stream the earlier audit recorded"},
	{"deliver", whenInboxPresent, "the `deliver` consumer object"},
	{"deliver.present", whenInboxPresent, "whether the consumer existed"},
	{"deliver.pending", whenDeliverPresent, "drain rule: nothing pending"},
	{"deliver.ack_pending", whenDeliverPresent, "drain rule: nothing unacknowledged"},
	{"deliver.delivered", whenDeliverPresent, "movement of the delivered stream sequence"},
	{"deliver.ack_floor", whenDeliverPresent, "drain rule: reaches last_seq"},
	{"deliver.created", whenDeliverPresent, "recreation check for the consumer"},
	{"deliver.deliver_policy", whenDeliverPresent, "drain rule: all or a start sequence"},
	{"deliver.start_seq", whenDeliverPresent, "drain rule: with by_start_sequence"},
	{"deliver.filter_subjects", whenDeliverPresent, "drain rule: any filter disqualifies"},
}

// earlierLookup finds a dotted path in a decoded JSON object. ok is false when a key is missing or null.
func earlierLookup(m map[string]json.RawMessage, path string) (raw json.RawMessage, ok bool) {
	head, rest, nested := strings.Cut(path, ".")
	v, found := m[head]
	if !found || string(bytes.TrimSpace(v)) == "null" {
		return nil, false
	}
	if !nested {
		return v, true
	}
	var sub map[string]json.RawMessage
	if json.Unmarshal(v, &sub) != nil || sub == nil {
		return nil, false
	}
	return earlierLookup(sub, rest)
}

// earlierTrue is whether a boolean field is present and true.
func earlierTrue(m map[string]json.RawMessage, path string) bool {
	raw, ok := earlierLookup(m, path)
	var b bool
	return ok && json.Unmarshal(raw, &b) == nil && b
}

// checkEarlierInbox names the first table field the inbox lacks, as a JSON path under where.
func checkEarlierInbox(where string, in map[string]json.RawMessage) error {
	for _, f := range earlierInboxFields {
		switch f.When {
		case whenInboxPresent:
			if !earlierTrue(in, "present") {
				continue
			}
		case whenDeliverPresent:
			if !earlierTrue(in, "present") || !earlierTrue(in, "deliver.present") {
				continue
			}
		}
		raw, ok := earlierLookup(in, f.Path)
		if f.Path == "stream" {
			var name string
			ok = ok && json.Unmarshal(raw, &name) == nil && name != ""
		}
		if !ok {
			note := ""
			if f.Path == "created" || f.Path == "deliver.created" {
				note = " (an audit made before creation times existed: regenerate it with this mashctl)"
			}
			return fmt.Errorf("mashctl: audit: --compare: the file is not a complete audit report: it lacks %s.%s%s", where, f.Path, note)
		}
	}
	return nil
}

type earlierReport struct {
	Version   *int             `json:"version"`
	Generated *string          `json:"generated"`
	Owner     *string          `json:"owner"`
	Records   *json.RawMessage `json:"records"`
	Renames   *json.RawMessage `json:"renames"`
}

// validateEarlier refuses a file that is not a complete audit report: the fields the comparison reads must be there,
// or a stub such as {"version":1} would pass for a baseline with no legacy streams.
func validateEarlier(raw []byte) error {
	var e earlierReport
	if err := json.Unmarshal(raw, &e); err != nil {
		return errors.New("mashctl: audit: --compare: the file is not an audit JSON report")
	}
	if e.Version == nil || *e.Version != auditVersion {
		v := 0
		if e.Version != nil {
			v = *e.Version
		}
		return fmt.Errorf("mashctl: audit: --compare: the file is audit report version %d, this mashctl reads version %d", v, auditVersion)
	}
	miss := func(f string) error {
		return fmt.Errorf("mashctl: audit: --compare: the file is not a complete audit report: it lacks %s", f)
	}
	if e.Generated == nil || *e.Generated == "" {
		return miss("generated")
	}
	if e.Owner == nil || *e.Owner == "" {
		return miss("owner")
	}
	if e.Records == nil || e.Renames == nil {
		return miss("records and renames")
	}
	var recs []struct {
		Key   string                     `json:"key"`
		Form  string                     `json:"form"`
		Inbox map[string]json.RawMessage `json:"inbox"`
	}
	var rens []struct {
		Flat        string                     `json:"flat"`
		LegacyInbox map[string]json.RawMessage `json:"legacy_inbox"`
	}
	if err := json.Unmarshal(*e.Records, &recs); err != nil || recs == nil {
		return miss("records (an array)")
	}
	if err := json.Unmarshal(*e.Renames, &rens); err != nil || rens == nil {
		return miss("renames (an array)")
	}
	for i, r := range recs {
		if r.Form == "legacy" {
			if err := checkEarlierInbox(fmt.Sprintf("records[%d].inbox", i), r.Inbox); err != nil {
				return err
			}
		}
	}
	for i, r := range rens {
		if err := checkEarlierInbox(fmt.Sprintf("renames[%d].legacy_inbox", i), r.LegacyInbox); err != nil {
			return err
		}
	}
	return nil
}

// readEarlierAudit reads an earlier `audit --json` report. A file that is not JSON, is another version or lacks a
// field the comparison reads is refused.
func readEarlierAudit(path string) (*auditReport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mashctl: audit: --compare: %s", config.Clean(errors.Unwrap(err).Error()))
	}
	if err := validateEarlier(raw); err != nil {
		return nil, err
	}
	var r auditReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, errors.New("mashctl: audit: --compare: the file is not an audit JSON report")
	}
	return &r, nil
}

// bindEarlierAudit refuses an earlier report from another owner or another account. One without an account (made
// before the field existed) passes here and is flagged unverified in the comparison.
func bindEarlierAudit(before *auditReport, now auditReport) error {
	if before.Owner != now.Owner {
		return fmt.Errorf("mashctl: audit: --compare: the earlier audit is for owner %s, this audit is for owner %s", config.Clean(before.Owner), config.Clean(now.Owner))
	}
	if before.Account != "" && before.Account != now.Account {
		return fmt.Errorf("mashctl: audit: --compare: the earlier audit is for account %s, this audit is for account %s", config.Clean(before.Account), config.Clean(now.Account))
	}
	return nil
}

// legacyStreams is every legacy inbox the report shows: the inbox of each old-form record and the legacy inbox of
// each renamed identity, by stream name.
func (r auditReport) legacyStreams() map[string]auditInbox {
	m, _ := r.legacyStreamsWithKeys()
	return m
}

// legacyStreamsWithKeys also returns, for each stream, the directory key of the record it belongs to.
func (r auditReport) legacyStreamsWithKeys() (map[string]auditInbox, map[string]string) {
	m, keys := map[string]auditInbox{}, map[string]string{}
	for _, x := range r.Records {
		if x.Form == "legacy" {
			m[x.Inbox.Stream], keys[x.Inbox.Stream] = x.Inbox, x.Key
		}
	}
	for _, x := range r.Renames {
		m[x.LegacyInbox.Stream], keys[x.LegacyInbox.Stream] = x.LegacyInbox, x.Flat
	}
	return m, keys
}

func compareAudits(before, now auditReport) *auditCompare {
	c := &auditCompare{AgainstGenerated: before.Generated, Streams: []auditCompareStream{}, Removed: []string{}, Added: []string{}}
	b, bkeys := before.legacyStreamsWithKeys()
	n := now.legacyStreams()
	recordNow := map[string]bool{}
	for _, r := range now.Records {
		recordNow[r.Key] = true
	}
	names := map[string]bool{}
	for k := range b {
		names[k] = true
	}
	for k := range n {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	slices.Sort(sorted)
	for _, name := range sorted {
		bi, ni := b[name], n[name]
		s := auditCompareStream{Stream: name, PresentBefore: bi.Present, PresentNow: ni.Present,
			LastSeqBefore: bi.LastSeq, LastSeqNow: ni.LastSeq, DeliveredBefore: bi.Deliver.Delivered, DeliveredNow: ni.Deliver.Delivered,
			AckFloorBefore: bi.Deliver.AckFloor, AckFloorNow: ni.Deliver.AckFloor}
		if ni.Present {
			s.LastSeqMoved = s.LastSeqNow != s.LastSeqBefore
			s.DeliveredMoved = s.DeliveredNow != s.DeliveredBefore
			s.AckFloorMoved = s.AckFloorNow != s.AckFloorBefore
		}
		if s.LastSeqMoved || s.DeliveredMoved {
			c.Moved++
		}
		if bi.Present && ni.Present {
			s.CreatedBefore, s.CreatedNow = bi.Created, ni.Created
			s.DeliverCreatedBefore, s.DeliverCreatedNow = bi.Deliver.Created, ni.Deliver.Created
			both := bi.Deliver.Present && ni.Deliver.Present
			streamDiffers := bi.Created != "" && ni.Created != "" && bi.Created != ni.Created
			deliverDiffers := both && bi.Deliver.Created != "" && ni.Deliver.Created != "" && bi.Deliver.Created != ni.Deliver.Created
			missing := bi.Created == "" || ni.Created == "" || (both && (bi.Deliver.Created == "" || ni.Deliver.Created == ""))
			switch {
			case streamDiffers || deliverDiffers:
				s.Recreated = true
				c.Recreated++
			case missing:
				s.CreationUnknown = true
				c.CreationUnknown++
			}
		}
		if bi.Present && !ni.Present {
			// A drained inbox whose record is gone too was deprovisioned: retired. Anything else may have lost mail.
			if bi.drained() && !recordNow[bkeys[name]] {
				s.Retired = true
				c.Retired++
			} else {
				s.Vanished = true
				c.Vanished++
			}
		}
		c.Streams = append(c.Streams, s)
	}
	was, is := map[string]bool{}, map[string]bool{}
	for _, r := range before.Records {
		was[r.Key] = true
	}
	for _, r := range now.Records {
		is[r.Key] = true
		if !was[r.Key] {
			c.Added = append(c.Added, r.Key)
		}
	}
	for _, r := range before.Records {
		if !is[r.Key] {
			c.Removed = append(c.Removed, r.Key)
		}
	}
	slices.Sort(c.Added)
	slices.Sort(c.Removed)
	return c
}

func writeCompareText(w io.Writer, c *auditCompare) {
	cl := config.Clean
	if c.AccountUnverified {
		fmt.Fprintln(w, "compare account not verified: the earlier audit has no account field, so it cannot be tied to this deployment")
	}
	fmt.Fprintf(w, "compare against the audit generated %s: %d legacy stream(s), %d moved, %d vanished, %d retired, %d recreated, %d creation unknown\n", cl(c.AgainstGenerated), len(c.Streams), c.Moved, c.Vanished, c.Retired, c.Recreated, c.CreationUnknown)
	for _, s := range c.Streams {
		state := "unchanged"
		if s.LastSeqMoved || s.DeliveredMoved {
			state = "MOVED"
		}
		if s.Recreated {
			state = "RECREATED"
		} else if s.CreationUnknown {
			state = "UNKNOWN"
		}
		if s.Vanished {
			state = "VANISHED"
		} else if s.Retired {
			state = "RETIRED"
		}
		fmt.Fprintf(w, "compare stream %s\t%s\tlast_seq %d->%d\tdelivered %d->%d\tack_floor %d->%d\tpresent %t->%t\n", cl(s.Stream), state,
			s.LastSeqBefore, s.LastSeqNow, s.DeliveredBefore, s.DeliveredNow, s.AckFloorBefore, s.AckFloorNow, s.PresentBefore, s.PresentNow)
	}
	for _, k := range c.Removed {
		fmt.Fprintf(w, "compare identity removed %s\n", cl(k))
	}
	for _, k := range c.Added {
		fmt.Fprintf(w, "compare identity added %s\n", cl(k))
	}
}
