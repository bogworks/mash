package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

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
}

// auditCompare is the current audit set against an earlier one (--compare).
type auditCompare struct {
	AgainstGenerated string               `json:"against_generated"`
	Streams          []auditCompareStream `json:"streams"`
	Removed          []string             `json:"identities_removed"` // record keys present before and absent now
	Added            []string             `json:"identities_added"`   // record keys absent before and present now
	Moved            int                  `json:"moved"`              // legacy streams whose last_seq or delivered sequence moved
	Vanished         int                  `json:"vanished"`           // legacy inboxes present before, absent now, not retired: fails --strict
	Retired          int                  `json:"retired"`            // legacy inboxes drained before whose record and stream are both gone: shown only
}

// readEarlierAudit reads an earlier `audit --json` report. A file that is not JSON, or is another version, is refused.
func readEarlierAudit(path string) (*auditReport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mashctl: audit: --compare: %s", config.Clean(errors.Unwrap(err).Error()))
	}
	var r auditReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, errors.New("mashctl: audit: --compare: the file is not an audit JSON report")
	}
	if r.Version != auditVersion {
		return nil, fmt.Errorf("mashctl: audit: --compare: the file is audit report version %d, this mashctl reads version %d", r.Version, auditVersion)
	}
	return &r, nil
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
	fmt.Fprintf(w, "compare against the audit generated %s: %d legacy stream(s), %d moved, %d vanished, %d retired\n", cl(c.AgainstGenerated), len(c.Streams), c.Moved, c.Vanished, c.Retired)
	for _, s := range c.Streams {
		state := "unchanged"
		if s.LastSeqMoved || s.DeliveredMoved {
			state = "MOVED"
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
