package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
)

// The deadline wake of an answer=now ask (peer-status design 6.5, option U3). When an ask's deadline passes with the ask
// still open, mash tells this session, through the same local-notice path its other notices use (the serve's channel
// notice: a "mash:" line with no sender and no inbox sequence, never mail of a peer), what the recipient reports about
// itself, and closes the ask. The notice carries only state words and mash's own numbers: nothing a peer sent.

// AskWatchEvery is how often the table is scanned for expired asks. The scan reads the stored deadlines, so a restart
// loses nothing.
const AskWatchEvery = 5 * time.Second

// inboxLook is how many of the newest inbox messages the deadline check reads when the watcher has no Since search.
const inboxLook = 50

// inboxSlack is how far before an ask's send time the inbox search reaches, for the difference between this clock and the
// one that stamped the stored mail.
const inboxSlack = time.Minute

// errInboxIncomplete is the read of an inbox that did not reach back to the ask's send time (the walk bound ended it).
var errInboxIncomplete = errors.New("mash: the inbox walk ended before the ask's send time")

// CatchUpWait is how long the watcher waits at its start for the mail loop to account for the inbox backlog it started
// with before the away scan runs. After it the scan runs anyway, and the inbox check below still keeps an ask open while
// a verified reply to it is stored and the loop has not caught up.
const CatchUpWait = 30 * time.Second

// AskWatch is what the deadline watcher needs; AskWatchOf fills all but Now and CatchUpWait from an endpoint.
type AskWatch struct {
	Table    *AskTable
	Activity func(ctx context.Context, name string) PeerState
	Inbox    func(ctx context.Context, limit int) ([]render.Mail, error)
	// Since, when set, is used in place of Inbox: the mail stored at or after since, and whether the search reached back
	// that far (false when its bound ended it first).
	Since func(ctx context.Context, since time.Time) ([]render.Mail, bool, error)
	// Notify writes one notice; a non-nil error means it was not delivered, and the ask stays open for the next scan.
	Notify func(text string) error
	// Logf, when set, receives the one line per ask that says its notice could not be written.
	Logf func(format string, args ...any)
	// CaughtUp reports whether the mail loop has been through the inbox backlog that was stored when it started (so a
	// stored verified reply has had its chance to close an ask); true when nil.
	CaughtUp    func() bool
	CatchUpWait time.Duration    // CatchUpWait when zero
	Now         func() time.Time // time.Now when nil

	// warned holds the ids of the asks whose failed notice has been logged (once each, not every scan). It is shared by
	// the copies of the watcher, and only the one goroutine running Run/Scan/ScanAway touches it.
	warned map[string]bool
}

// AskWatchOf is the watcher of table t on this endpoint, delivering notices through notify.
func (e *Endpoint) AskWatchOf(t *AskTable, notify func(string) error) AskWatch {
	return AskWatch{Table: t, warned: map[string]bool{}, Activity: e.PeerActivity, Inbox: e.Inbox, Since: e.InboxSince, Notify: notify,
		CaughtUp: func() bool { _, ok := e.waking.snapshot(); return ok }}
}

// Run waits (at most CatchUpWait) until the mail loop has caught up with the backlog stored at start, delivers the asks
// that expired while no serve was running as one combined notice, then scans every `every` until ctx ends. It returns
// only after ctx has ended, and touches the table no more once it has returned.
func (w AskWatch) Run(ctx context.Context, every time.Duration) {
	if w.warned == nil {
		w.warned = map[string]bool{}
	}
	wait := w.CatchUpWait
	if wait <= 0 {
		wait = CatchUpWait
	}
	poll := min(every, 250*time.Millisecond)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	pt := time.NewTicker(poll)
	defer pt.Stop()
wait:
	for w.CaughtUp != nil && !w.CaughtUp() {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			break wait
		case <-pt.C:
		}
	}
	if ctx.Err() != nil {
		return
	}
	w.ScanAway(ctx)
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			w.Scan(ctx)
		}
	}
}

func (w AskWatch) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w AskWatch) caughtUp() bool { return w.CaughtUp == nil || w.CaughtUp() }

// hasReply reports whether mail holds the message the mail loop's answer wake matches for ask a: verified trust, a signed
// in-reply-to that verified (render.Mail.ReplyTo is 0 otherwise) equal to the receipt sequence, from the ask's recipient.
// It reads the stored mail as classified by the endpoint, never an unverified header.
func hasReply(a OpenAsk, mail []render.Mail) bool {
	for _, m := range mail {
		if m.Trust == sap.TrustVerified && m.From == a.Recipient && m.ReplyTo != 0 && m.ReplyTo == a.ReceiptSeq {
			return true
		}
	}
	return false
}

// settle decides what happens to an expired ask given the inbox read: true means the caller may close it with the
// unanswered notice. A stored verified reply to the ask means the answer is not missing. While the mail loop has not
// caught up, the ask is left open for the loop's answer wake. Once it has, the loop already took the reply as ordinary
// mail (it came before the ask could match it), so the ask is closed here with a notice that it was answered, the one
// wake that reply can still give. An inbox that could not be read, or searched back to the ask's send time, leaves the
// ask open until the next scan.
func (w AskWatch) settle(a OpenAsk, mail []render.Mail, mailErr error) bool {
	if w.Inbox == nil && w.Since == nil {
		return true
	}
	if !hasReply(a, mail) {
		return mailErr == nil // an unreadable or not fully searched inbox leaves the ask open
	}
	if w.caughtUp() && w.deliver(a.ID, fmt.Sprintf("mash: %s answered #%d. The ask is closed.", sap.Display(a.Recipient), a.ReceiptSeq)) {
		w.Table.Close(a.ID)
	}
	return false
}

// stillOpen reports whether ask id is still in the table (read fresh from the file).
func (w AskWatch) stillOpen(id string) bool {
	for _, a := range w.Table.Asks() {
		if a.ID == id {
			return true
		}
	}
	return false
}

// deliver writes the notice for ask id and reports whether it was written. A failed write is logged once per ask and
// leaves the ask open, so the next scan tries again; the ask's own expiry (AskSettleGrace) ends the retries of a channel
// that never recovers. Callers close the ask only after a true: delivering first means a notice is never lost, and a
// reply that closes the ask in between only makes the Close a no-op (the notice already says no answer by the deadline).
// Scan, ScanAway and settle run on the one watcher goroutine (Run), one ask at a time, so a notice is not sent twice.
func (w AskWatch) deliver(id, text string) bool {
	err := w.Notify(text)
	if err == nil {
		delete(w.warned, id)
		return true
	}
	if w.Logf != nil && w.warned != nil && !w.warned[id] {
		w.warned[id] = true
		w.Logf("mash: the deadline notice for ask %s could not be written (%v); retrying every scan until the ask expires\n", id, err)
	}
	return false
}

// readInbox reads the mail stored since the earliest send time given (less inboxSlack). An incomplete search returns
// what it found with errInboxIncomplete.
func (w AskWatch) readInbox(ctx context.Context, sent time.Time) ([]render.Mail, error) {
	if w.Since != nil {
		mail, complete, err := w.Since(ctx, sent.Add(-inboxSlack))
		if err == nil && !complete {
			err = errInboxIncomplete
		}
		return mail, err
	}
	if w.Inbox == nil {
		return nil, nil
	}
	return w.Inbox(ctx, inboxLook)
}

// ScanAway closes every expired ask and delivers one notice for all of them. An ask with a stored verified reply is
// not one of them (see settle).
func (w AskWatch) ScanAway(ctx context.Context) {
	expired := w.Table.Expired(w.now())
	if len(expired) == 0 {
		return
	}
	sent := expired[0].SentAt
	for _, a := range expired {
		if a.SentAt.Before(sent) {
			sent = a.SentAt
		}
	}
	mail, err := w.readInbox(ctx, sent)
	var parts []string
	var ids []string
	for _, a := range expired {
		if ctx.Err() != nil {
			return
		}
		if w.settle(a, mail, err) {
			parts = append(parts, fmt.Sprintf("#%d to %s", a.ReceiptSeq, sap.Display(a.Recipient)))
			ids = append(ids, a.ID)
		}
	}
	var text string
	switch len(parts) {
	case 0:
		return
	case 1:
		text = "mash: 1 ask went unanswered while you were away: " + parts[0] + "."
	default:
		text = fmt.Sprintf("mash: %d asks went unanswered while you were away: %s.", len(parts), strings.Join(parts, ", "))
	}
	// Deliver, then close. On a failed write the asks stay open; the scans that follow retry them one by one.
	if !w.deliver(strings.Join(ids, ","), text) {
		return
	}
	for _, id := range ids {
		w.Table.Close(id)
	}
}

// Scan delivers one notice for each expired ask, then closes it (a failed write leaves it open for the next scan). An ask a reply closed meanwhile gets none, and neither
// does one with a stored verified reply the mail loop has yet to take (see settle).
func (w AskWatch) Scan(ctx context.Context) {
	for _, a := range w.Table.Expired(w.now()) {
		if ctx.Err() != nil {
			return
		}
		mail, err := w.readInbox(ctx, a.SentAt)
		if !w.settle(a, mail, err) {
			continue
		}
		text := w.notice(ctx, a, mail) // may take a while (a status request): a reply can close the ask meanwhile
		if !w.stillOpen(a.ID) {
			continue
		}
		if w.deliver(a.ID, text) {
			w.Table.Close(a.ID)
		}
	}
}

// notice is the text for one expired ask: mail from the recipient since the send if the inbox holds any (the reply of a
// sender that does not mark its answers), else what the recipient reports about itself.
func (w AskWatch) notice(ctx context.Context, a OpenAsk, ms []render.Mail) string {
	who := sap.Display(a.Recipient)
	var at time.Time
	for _, m := range ms {
		if m.Trust == sap.TrustVerified && m.From == a.Recipient && m.At.After(a.SentAt) && m.At.After(at) {
			at = m.At
		}
	}
	if !at.IsZero() {
		return fmt.Sprintf("mash: %s sent mail at %s (not marked as an answer to #%d). The ask is closed.", who, at.UTC().Format("15:04Z"), a.ReceiptSeq)
	}
	st := PeerState{Outcome: PeerNoAnswer}
	if w.Activity != nil {
		st = w.Activity(ctx, a.Recipient)
	}
	return fmt.Sprintf("mash: No answer to #%d to %s after %s. %s", a.ReceiptSeq, who, shortDuration(a.Deadline.Sub(a.SentAt)), stateSentence(st))
}

func stateSentence(st PeerState) string {
	switch st.Outcome {
	case PeerReported:
		switch {
		case st.State == "working":
			return "It is working."
		case st.Kind != "":
			return "It reports: " + st.State + " (" + st.Kind + ")."
		}
		return "It reports: " + st.State + "."
	case PeerNotReported:
		return "It is online and does not report what it is doing."
	case PeerRefused:
		return "Its state is not available to this endpoint."
	}
	return "It gave no answer to a status request (offline, or unreachable)."
}

// shortDuration writes d as "10m", "1h30m" or "45s".
func shortDuration(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// DeadlineText is the receipt wording of an ask due at deadline.
func DeadlineText(deadline time.Time) string {
	return "will wake you on the reply, or at " + deadline.UTC().Format("15:04Z") + " with its state if none came"
}
