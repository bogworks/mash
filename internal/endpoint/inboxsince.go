package endpoint

import (
	"context"
	"time"

	"github.com/bogworks/mash/internal/render"
)

const (
	// inboxPage is how many sequences one step of InboxSince reads.
	inboxPage = 200
	// InboxWalkMax is how many sequences InboxSince examines in one stream before it gives up on reaching its time.
	InboxWalkMax = 20000
)

// inboxWalkMax is InboxWalkMax, replaceable in tests.
var inboxWalkMax = InboxWalkMax

// InboxSince returns the mail stored at or after since, oldest first, found by walking each inbox stream backwards by
// sequence a page at a time. complete is true when every stream was walked back to a message stored before since, or to
// its first sequence; it is false when the walk bound (InboxWalkMax sequences per stream) ended it first, so the mail
// returned may not be all that was stored. Forged mail is skipped, as in Inbox. On a read error it returns what it has,
// with complete false.
func (e *Endpoint) InboxSince(ctx context.Context, since time.Time) (out []render.Mail, complete bool, err error) {
	complete = true
	for _, src := range e.sources() {
		s, serr := e.streamOf(ctx, src.ref)
		if serr != nil {
			sortMail(out)
			return out, false, serr
		}
		info := s.CachedInfo()
		first := max(info.State.FirstSeq, 1)
		reached := false
		scanned := 0
		for seq := info.State.LastSeq; seq >= first && seq > 0; {
			if scanned >= inboxWalkMax {
				break
			}
			lo := first
			page := uint64(min(inboxPage, inboxWalkMax-scanned))
			if seq-first+1 > page {
				lo = seq - page + 1
			}
			seqs := seqsDown(seq, lo)
			scanned += len(seqs)
			ms, cerr := e.collectStream(ctx, src.ref, s.GetMsg, seqs, len(seqs))
			for _, m := range ms {
				if m.At.Before(since) {
					reached = true
				} else {
					out = append(out, m)
				}
			}
			if cerr != nil {
				sortMail(out)
				return out, false, cerr
			}
			if reached || lo <= first {
				reached = true
				break
			}
			seq = lo - 1
		}
		if info.State.LastSeq < first {
			reached = true // an empty stream
		}
		if !reached {
			complete = false
		}
	}
	sortMail(out)
	return out, complete, nil
}
