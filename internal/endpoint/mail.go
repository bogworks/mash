package endpoint

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// Deliverer writes one channel event to the Claude session. A nil error
// means it was written; only then is the mail acked.
type Deliverer func(text string, meta map[string]string) error

// UrgentHeader marks a message urgent: a signature by the sender's key over the message (sap.SignUrgent). Only a
// verified sender's mark that verifies counts; urgent mail wakes a hooked session at once.
const UrgentHeader = "Mash-Urgent"

// KindHeader carries the sender's kind (kind.Kind). Missing or unknown reads as task on receipt (kind.Of): an old sender's mail wakes.
const KindHeader = "Mash-Kind"

// InReplyToHeader carries the seq a message answers and the sender's signature over it, "<seq>.<sig>"
// (sap.SignInReplyTo, ADR 0063). Only a value that verifies is ever shown; an older sender's bare decimal is ignored.
const InReplyToHeader = "Mash-In-Reply-To"

// ViaHeader is the sender's signed declaration that the message relays or quotes message #seq of its own inbox, whose
// sender was origin: "<origin>#<seq>.<sig>" (sap.SignVia, ADR 0063). Only a value that verifies is ever shown.
const ViaHeader = "Mash-Via"

// InstanceHeader carries the sender's instance id and its signature over it
// (sap.Identity.SignInstance). Only a value that verifies is ever shown.
const InstanceHeader = "Mash-Sender-Instance"

// maxPending flushes a batch early so a flood can't grow it without bound.
const maxPending = 200

// MaxSignedAge bounds how long before its storage a message may have been
// signed. Older (or future) signatures are labelled stale: a replayed capture.
var MaxSignedAge = 5 * time.Minute

// shownName is the sender as displayed when it is not proven: a short form of its key, never a self-declared name (an
// endpoint's address is shown only from the directory, see classify).
func shownName(h sap.SenderHeader) string {
	if len(h.User) > 8 {
		return h.User[:4] + "…" + h.User[len(h.User)-4:]
	}
	return "?"
}

// senderInstance is the sender's instance id, only when the sender's own key
// signed it onto this very message and the message itself verified.
func senderInstance(hdr nats.Header, trust sap.Trust, h sap.SenderHeader) string {
	if trust != sap.TrustVerified && trust != sap.TrustStale {
		return ""
	}
	inst, _ := sap.VerifyInstance(hdr.Get(InstanceHeader), h)
	return inst
}

// senderMarks are the sender's signed in-reply-to and via declarations, only when they verify for this very message and
// the message itself verified (or is stale, like the instance). ReplyTo is 0 and via empty otherwise: the old bare
// decimal, a forged or moved signature and a malformed value are ignored, never shown and never an error. via reads
// "<origin> #<seq>".
func senderMarks(hdr nats.Header, trust sap.Trust, h sap.SenderHeader) (replyTo uint64, via string) {
	if trust != sap.TrustVerified && trust != sap.TrustStale {
		return 0, ""
	}
	replyTo, _ = sap.VerifyInReplyTo(h, hdr.Get(InReplyToHeader))
	if origin, seq, ok := sap.VerifyVia(h, hdr.Get(ViaHeader)); ok {
		via = origin + " #" + strconv.FormatUint(seq, 10)
	}
	return replyTo, via
}

// classify verifies a stored message and names its sender (spec section 4).
// storedAt is when the stream stored it. ok is false for forged or malformed
// mail, which is never shown.
func (e *Endpoint) classify(subject string, hdr nats.Header, data []byte, storedAt time.Time) (from string, trust sap.Trust, h sap.SenderHeader, ok bool) {
	h, trust = sap.VerifyStored(hdr.Get(sap.HeaderName), subject, data)
	switch trust {
	case sap.TrustForged, sap.TrustMalformed:
		return "", trust, h, false
	case sap.TrustAbsent:
		return "?", trust, h, true
	case sap.TrustClaimed:
		return shownName(h), trust, h, true
	}
	e.mu.RLock()
	name := e.byUser[h.User]
	e.mu.RUnlock()
	if name == "" || (h.Name != "" && h.Name != name) {
		// The directory cache may predate the sender (it is re-read every DirectoryPoll): read that one record now,
		// once, before the message is called unknown-signer and, by default, dropped for good.
		if e.reloadSender(h) {
			e.mu.RLock()
			name = e.byUser[h.User]
			e.mu.RUnlock()
		}
	}
	if name == "" || (h.Name != "" && h.Name != name) {
		return shownName(h), sap.TrustUnknownSigner, h, true
	}
	if signed, err := time.Parse(time.RFC3339Nano, h.TS); err == nil {
		if age := storedAt.Sub(signed); age > MaxSignedAge || age < -sap.LiveSkew {
			return name, sap.TrustStale, h, true
		}
	}
	return name, sap.TrustVerified, h, true
}

// seenSet is the recipient's memory of signed mail already shown, keyed by (user, nonce) with the stream sequence and
// store time it was shown at. It serves two purposes. A redelivery (a lost ack, a nak) has the same stream sequence as
// the delivery shown: it is dropped. A second message with the same (user, nonce) and a different sequence is either a
// replay or a re-send of a deterministic "send --id" nonce: inside the stream's duplicate window it is dropped (the
// stream would have dropped a genuine re-send itself, so this is a replay). Stored more than a window after the one
// shown, it is a new send and is shown, unless it carries the same signed timestamp: a genuine re-send is signed
// afresh with a new timestamp, so the same one means the same signed bytes copied. Such a copy is dropped for as long
// as its signature could still be shown as verified, so the two windows line up. Every signed timestamp shown for a
// (user, nonce) is remembered, not just the latest, so a copy of the original after a re-send is still dropped. Each
// timestamp is kept for the longer of the duplicate window and MaxSignedAge plus sap.LiveSkew after its store time,
// then forgotten (and the entry with its last one). The shown
// stream sequences are kept separately for the whole session, so a redelivery whose nonce entry was pruned is still
// dropped (codex on #122). A shown sequence is forgotten only once the consumer's confirmed ack floor has passed it: a
// sequence at or below the floor is acknowledged and is never redelivered, while one above it (a lost ack, a nak) may
// be, however many later messages have been shown. The floor is asked for only when the set has reached seqPruneAt,
// at most once per seqPruneEvery. If it cannot be read or has not moved, nothing is pruned and the set grows; there is
// no cap, as an unbounded set of 8-byte keys grows slowly and forgetting an unacknowledged sequence would show mail twice.
type seenSet struct {
	window time.Duration
	m      map[string]seenEntry
	seqs   map[spool.Ref]struct{} // (stream, sequence) of signed mail shown

	// ackFloors is each stream's consumer's ack floor (stream sequence); false when unknown. A stream with none is never
	// pruned. Each stream's shown sequences are pruned by its own floor only; the (user, nonce) keys are global.
	ackFloors  map[string]func() (uint64, bool)
	pruneAt    int           // set size from which the ack floors are consulted
	pruneEvery time.Duration // minimum time between ack floor reads
	lastCheck  time.Time
	lastFloor  map[string]uint64
}

const (
	seqPruneAt    = 1 << 17
	seqPruneEvery = 30 * time.Second
)

type seenEntry struct {
	ref spool.Ref
	at  time.Time            // the store time of the latest delivery shown
	tss map[string]time.Time // every signed timestamp shown for the key -> the store time it was shown at
}

func newSeenSet(window time.Duration) *seenSet {
	return &seenSet{window: window, m: map[string]seenEntry{}, seqs: map[spool.Ref]struct{}{}, pruneAt: seqPruneAt, pruneEvery: seqPruneEvery}
}

// dup reports whether p repeats mail already recorded.
func (s *seenSet) dup(p pending) bool {
	if p.key == "" {
		return false
	}
	if _, ok := s.seqs[refOf(p.mail)]; ok {
		return true
	}
	en, ok := s.m[p.key]
	if !ok {
		return false
	}
	if en.ref == refOf(p.mail) || p.mail.At.Sub(en.at) < s.window {
		return true
	}
	// Past the window: the same signed timestamp is a copy of the signed bytes, a new one a genuine re-send.
	_, copied := en.tss[p.ts]
	return p.ts != "" && copied
}

// forget undoes mark for p, a message that was recorded but not shown (a held message dropped unacked): its
// redelivery must be shown, not taken for a repeat.
func (s *seenSet) forget(p pending) {
	if s == nil || p.key == "" {
		return
	}
	ref := refOf(p.mail)
	delete(s.seqs, ref)
	en, ok := s.m[p.key]
	if !ok || en.ref != ref {
		return
	}
	delete(en.tss, p.ts)
	if len(en.tss) == 0 {
		delete(s.m, p.key)
		return
	}
	en.ref, en.at = spool.Ref{}, time.Time{} // timestamps of other shown messages stay remembered
	s.m[p.key] = en
}

// retention is how long after its store time an entry is kept: until it is past the duplicate window and its
// signature could no longer be shown as verified.
func (s *seenSet) retention() time.Duration {
	return max(s.window, MaxSignedAge+sap.LiveSkew)
}

// mark records p as shown and forgets entries more than a window older than it.
func (s *seenSet) mark(p pending) {
	if p.key == "" {
		return
	}
	if len(s.m) >= 256 {
		for k, en := range s.m {
			if p.mail.At.Sub(en.at) >= s.retention() {
				delete(s.m, k)
			}
		}
	}
	en := s.m[p.key]
	tss := map[string]time.Time{}
	for ts, at := range en.tss {
		if p.mail.At.Sub(at) < s.retention() {
			tss[ts] = at
		}
	}
	if p.ts != "" {
		tss[p.ts] = p.mail.At
	}
	s.m[p.key] = seenEntry{ref: refOf(p.mail), at: p.mail.At, tss: tss}
	s.seqs[refOf(p.mail)] = struct{}{}
	s.pruneSeqs()
}

// pruneSeqs forgets the shown sequences the consumer has acknowledged, when the set is large enough to be worth it
// and the floor has not been read too recently.
func (s *seenSet) pruneSeqs() {
	if len(s.ackFloors) == 0 || len(s.seqs) < s.pruneAt {
		return
	}
	now := time.Now()
	if !s.lastCheck.IsZero() && now.Sub(s.lastCheck) < s.pruneEvery {
		return
	}
	s.lastCheck = now
	if s.lastFloor == nil {
		s.lastFloor = map[string]uint64{}
	}
	floors := map[string]uint64{}
	for stream, read := range s.ackFloors {
		if floor, ok := read(); ok && floor > s.lastFloor[stream] {
			floors[stream] = floor
			s.lastFloor[stream] = floor
		}
	}
	if len(floors) == 0 {
		return
	}
	for q := range s.seqs {
		// A stream's floor prunes that stream's refs only.
		if floor, ok := floors[q.Stream]; ok && q.Seq <= floor {
			delete(s.seqs, q)
		}
	}
}

// consumerAckFloor reads the consumer's ack floor (the stream sequence of the highest contiguously acked message). It
// logs the first failure only.
func (e *Endpoint) consumerAckFloor(cons jetstream.Consumer) func() (uint64, bool) {
	warned := false
	return func() (uint64, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		info, err := cons.Info(ctx)
		if err != nil {
			if !warned {
				warned = true
				e.log.Printf("consumer info (ack floor) unavailable, shown sequences are not pruned: %v", err)
			}
			return 0, false
		}
		return info.AckFloor.Stream, true
	}
}

// wakingProbe reads the consumer's and the inbox stream's state (the stream named stream) from the server for the
// waking tracker.
func (e *Endpoint) wakingProbe(cons jetstream.Consumer, stream string) wakingProbe {
	return func(ctx context.Context) (uint64, int, uint64, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		t0 := time.Now()
		ci, err := cons.Info(ctx)
		if err != nil {
			return 0, 0, 0, err
		}
		e.noteServerTime(ci.TimeStamp, t0, time.Now())
		s, err := e.js.Stream(ctx, stream)
		if err != nil {
			return 0, 0, 0, err
		}
		return ci.NumPending, ci.NumAckPending, s.CachedInfo().State.FirstSeq, nil // Stream() read the info just now
	}
}

// dupWindow is how long the recipient treats a repeated nonce as a replay: the inbox stream's duplicate window.
func (e *Endpoint) dupWindow() time.Duration {
	if e.duplicateWindow > 0 {
		return e.duplicateWindow
	}
	return store.InboxDuplicateWindow
}

type pending struct {
	mail render.Mail
	msg  jetstream.Msg
	key  string // (user, nonce) of signed mail, for de-duplication
	ts   string // the signed timestamp of signed mail, to tell a copy from a re-send
}

// consume starts the inbox consumer and its delivery worker. The consumer's
// callback only classifies and queues; one worker goroutine gathers messages
// until SettleMS pass with no new one, pushes them as one event, then acks
// them (a failed write naks them for redelivery). Being the only writer, the
// worker keeps stream order and makes de-duplication exact. The returned
// stop waits for the consumer to close and the worker to finish.
func (e *Endpoint) consume(ctx context.Context, deliver Deliverer) (func(), error) {
	if !e.graceWait(ctx) { // a takeover from a live serve of this session: let it stop first
		return func() {}, nil
	}
	stream := e.ident.InboxStream()
	// The refusal state is tied to the stream's identity before the first message is handled, whatever SetSpool did: a
	// redelivery of mail already refused is found refused. A failure is a startup error, nothing is consumed.
	if _, err := e.bindRefusals(ctx); err != nil {
		return nil, fmt.Errorf("mash: refused-mail state not tied to the inbox stream: %w", err)
	}
	cons, err := e.js.Consumer(ctx, stream, store.Consumer)
	if err != nil {
		return nil, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	queue := make(chan pending, maxPending)
	// The startup messages are those the consumer held unacked or pending, all at or below Delivered + NumPending, read in
	// one consumer info (no extra round trip; startup is budgeted). A gap among the pending (a deleted message) only
	// lowers this bound, which delays readiness to the server's rule and never makes it early; a later arrival is above it.
	ci := cons.CachedInfo()
	e.waking.start(int(ci.NumPending)+ci.NumAckPending, ci.Delivered.Stream+ci.NumPending, true, e.wakingProbe(cons, stream))
	quit := make(chan struct{})
	workerDone := make(chan struct{})
	go e.deliverLoop(deliver, queue, quit, workerDone, map[string]func() (uint64, bool){"": e.consumerAckFloor(cons)})

	handler := func(m jetstream.Msg) {
		meta, err := m.Metadata()
		if err != nil {
			e.log.Printf("message without metadata: %v", err)
			_ = m.Term()
			return
		}
		ref := MsgRef{Seq: meta.Sequence.Stream}
		if meta.NumDelivered > 1 {
			// A redelivery of mail already refused (its ack was lost): still refused, whatever the directory or the
			// setting says now. Acknowledged without a second count.
			if id := e.refusedStream.Load(); id != nil && e.refusedSeq(ref.Seq, *id) {
				if e.mayAck(pending{}) {
					_ = m.Ack()
				} else {
					_ = m.Nak()
				}
				return
			}
		}
		if e.leaseGone.Load() {
			_ = m.NakWithDelay(time.Second) // the lease is gone: not queued, not acked; redelivered after a second, not in a loop
			return
		}
		defer e.waking.received(ref)
		// The sender's signature binds the subject the message was stored under, so classify checks it against the
		// stored subject.
		from, trust, h, ok := e.classify(m.Subject(), m.Headers(), m.Data(), meta.Timestamp)
		if !ok {
			e.dropped.Add(1)
			e.log.Printf("dropped #%s: %s sender header (claimed user %s)", FormatMsgRef(ref), trust, h.User)
			if e.verdict() == verdictHeld {
				_ = m.Ack()
			}
			return
		}
		if e.refuses(trust) {
			// Dropped and counted, never delivered: acked so it is not redelivered for ever. The sender is told nothing.
			// Without the lease the new holder gets it (and counts it), so it is nak'd, not counted.
			if e.mayAck(pending{}) {
				recorded, _ := e.refuse(ref.Seq, claimedAddress(h, from), trust)
				if e.refusedHook != nil {
					e.refusedHook()
				}
				// Recording can take seconds: the lease is checked again right before the ack, and on a loss the
				// record stays (never taken back) and the message is nak'd: the new holder finds it refused on the
				// redelivery and acks it without counting again. A handover can overcount by one.
				if !recorded {
					// Not recorded: acking would let the message read as mail later. Retried.
					_ = m.NakWithDelay(time.Second)
				} else if e.mayAck(pending{}) {
					_ = m.Ack()
				} else {
					_ = m.Nak()
				}
			} else {
				_ = m.Nak()
			}
			return
		}
		var key string
		if trust == sap.TrustVerified || trust == sap.TrustUnknownSigner || trust == sap.TrustStale {
			key = h.User + "\x00" + h.Nonce
		}
		replyTo, via := senderMarks(m.Headers(), trust, h)
		if v, err := e.viaAllowed(via); err != nil {
			e.log.Printf("#%s: relay declaration not honoured: %v", FormatMsgRef(ref), err)
			via = v
		}
		p := pending{msg: m, key: key, ts: h.TS, mail: render.Mail{ReplyTo: replyTo, Via: via, Seq: ref.Seq, From: from, Trust: trust,
			At: meta.Timestamp, Body: m.Data(), Instance: senderInstance(m.Headers(), trust, h), Owner: e.ownerOf(from),
			Urgent: trust == sap.TrustVerified && sap.VerifyUrgent(m.Headers().Get(UrgentHeader), h),
			Kind:   kind.Of(m.Headers().Get(KindHeader))}}
		e.relayInboundRef(ref, from, trust, via)
		e.waking.add(refOf(p.mail), p.mail.Urgent || p.mail.Kind.Wakes())
		select {
		case queue <- p:
		case <-quit:
		}
	}
	cc, err := cons.Consume(handler, jetstream.PullMaxMessages(100))
	if err != nil {
		close(quit)
		<-workerDone
		return nil, fmt.Errorf("mash: consume: %w", err)
	}
	return func() {
		// A serve that has lost its lease drains instead of stopping: Stop discards what the client already pulled into its
		// buffer, and mail discarded unacked is not redelivered to the new holder until the consumer's AckWait (30 s). Drain
		// hands each buffered message to the handler, which nak's it (the lease is gone) so the new holder gets it at once.
		if e.leaseGone.Load() {
			cc.Drain()
		} else {
			cc.Stop()
		}
		select {
		case <-cc.Closed():
		case <-time.After(5 * time.Second):
		}
		close(quit)
		<-workerDone
	}, nil
}

// deliverLoop is the single writer of channel events (see consume).
func (e *Endpoint) deliverLoop(deliver Deliverer, queue <-chan pending, quit <-chan struct{}, done chan<- struct{}, ackFloors map[string]func() (uint64, bool)) {
	defer close(done)
	settle := time.Duration(e.cfg.SettleMS) * time.Millisecond
	seen := newSeenSet(e.dupWindow()) // signed mail already shown, see seenSet
	seen.ackFloors = ackFloors
	h := &held{msgs: map[spool.Ref]pending{}, at: map[spool.Ref]time.Time{}, rung: map[spool.Ref]bool{}, fails: map[spool.Ref]int{}, seen: seen}
	tick := time.NewTicker(SpoolCheck)
	defer tick.Stop()
	for {
		var batch []pending
		select {
		case <-quit:
			e.releaseOnQuit(h, nil, queue) // held messages stay unacked: redelivered to the next session
			return
		case <-tick.C:
			e.settleHeld(deliver, h)
			continue
		case req := <-e.spoolReq:
			e.bindSpool(req)
			continue
		case p := <-queue:
			batch = append(batch, p)
		}
	gather:
		for len(batch) < maxPending {
			select {
			case <-quit:
				e.releaseOnQuit(h, batch, queue) // unacked: redelivered to the next session
				return
			case p := <-queue:
				batch = append(batch, p)
			case <-time.After(settle):
				break gather
			}
		}
		e.dropHeld(h)
		if batch = e.dropUnleased(batch); len(batch) == 0 {
			continue
		}
		if e.noChannel && e.spool == nil {
			hold(batch) // no spool and no channel: mail stays in the inbox, unacked
			continue
		}
		show := e.dedupe(batch, seen, h)
		// A tombstone that survived a restart marks mail a hook already showed: ack it, never show it again, even
		// before a hook has run in this session (codex H1 on #21, round 5).
		if e.spool != nil {
			kept := show[:0]
			for _, p := range show {
				if e.spool.IsDrained(refOf(p.mail)) {
					e.ack(p)
				} else {
					kept = append(kept, p)
				}
			}
			show = kept
		}
		if len(show) == 0 {
			continue
		}
		if e.hooked() {
			e.spoolBatch(deliver, show, h, seen)
			continue
		}
		e.deliverBatch(deliver, show, seen)
	}
}

// releaseOnQuit is the end of the deliver loop. When the lease is gone, what the loop still holds (the gathered batch,
// the queue, mail held for a hook) is nak'd at once, so the new holder is redelivered it now rather than after the
// ack wait; none of it is acked. With the lease held it does nothing: that mail waits out the ack wait, as it always did.
func (e *Endpoint) releaseOnQuit(h *held, batch []pending, queue <-chan pending) {
	if !e.leaseGone.Load() {
		return
	}
	e.dropUnleased(batch)
	for {
		select {
		case p := <-queue:
			e.dropUnleased([]pending{p})
		default:
			e.dropHeld(h)
			return
		}
	}
}

// dedupe drops signed mail already shown (acking it) or repeated within the batch. A delivery of a message still held
// in the spool is never acked or shown (codex H1 on #21): no hook has shown it, so acking would break "acked only
// once drained". It replaces the held handle, so the eventual ack and keep-alives use the newest delivery.
func (e *Endpoint) dedupe(batch []pending, seen *seenSet, h *held) []pending {
	var show []pending
	inBatch := newSeenSet(e.dupWindow())
	for _, p := range batch {
		if hp, ok := h.msgs[refOf(p.mail)]; ok {
			hp.msg = p.msg
			h.msgs[refOf(p.mail)] = hp
			continue
		}
		if seen.dup(p) || inBatch.dup(p) {
			if e.mayAck(p) {
				_ = p.msg.Ack()
			} else {
				_ = p.msg.Nak() // the lease is gone: the holder's consumer is redelivered it, nothing is acked here
			}
			e.waking.done(refOf(p.mail))
			continue
		}
		inBatch.mark(p)
		show = append(show, p)
	}
	return show
}

// Send mails text to endpoint to, signed by this endpoint, and returns the
// recipient-inbox sequence from the stream's acknowledgement.
func (e *Endpoint) Send(ctx context.Context, to, text string, inReplyTo uint64) (uint64, error) {
	return e.SendWith(ctx, to, text, SendOptions{InReplyTo: inReplyTo})
}

// SendOptions are a send's optional parts.
type SendOptions struct {
	InReplyTo uint64 // the #seq this answers; sent signed, so a recipient can show it
	// Via is a message of the sender's own inbox (a stream sequence) this one relays or quotes. SendDetailed reads it,
	// names its sender as the origin and sends the declaration signed (Mash-Via); the send fails when there is no such
	// message. Zero is none.
	Via    uint64
	Urgent bool      // wake the recipient's session at once, not after its idle wait
	Kind   kind.Kind // what the sender wants of the recipient; the zero value is task
	// ID is an idempotency key: sends from one endpoint to one recipient with the same ID are one message within the
	// stream's duplicate window. Empty means a fresh random key, which a timed-out send reports so it can be retried.
	ID string
}

// SendResult is what the stream acknowledged.
type SendResult struct {
	Seq       uint64 // the recipient-inbox sequence; for a duplicate, the original message's
	Duplicate bool   // the stream already held a message with this key and stored nothing new
}

// MaxSendIDLen is the longest idempotency key.
const MaxSendIDLen = 128

// ValidateSendID checks an idempotency key: 1 to MaxSendIDLen printable ASCII characters, no spaces.
func ValidateSendID(id string) error {
	if id == "" || len(id) > MaxSendIDLen {
		return fmt.Errorf("mash: send id must be 1 to %d characters, got %d", MaxSendIDLen, len(id))
	}
	for _, r := range id {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("mash: send id %q must be printable ASCII with no spaces", id)
		}
	}
	return nil
}

// SendNonce is the nonce (and Nats-Msg-Id) of a send with an idempotency key: 32 hex characters of a SHA-256 over
// the sender, recipient and key, each length-prefixed so the fields cannot run together. It satisfies the SAP nonce
// syntax ([A-Za-z0-9_-]{1,64}). Equal inputs give the same value; different senders or recipients never share one.
func SendNonce(sender, to, id string) string {
	h := sha256.New()
	h.Write([]byte("mash-send-id-v1\n"))
	for _, f := range []string{sender, to, id} {
		fmt.Fprintf(h, "%d:%s\n", len(f), f)
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// SendTimeoutError is a send whose acknowledgement did not arrive: the message may or may not have been stored.
// Sending again with Key as the ID within the stream's duplicate window stores it at most once; after the window a
// retry may store a second copy.
type SendTimeoutError struct {
	To  string
	Key string
	Err error
	// Ambiguous is set when a permission violation on the subject was seen while several sends were in flight, so
	// it could not be attributed to this one.
	Ambiguous bool
}

func (e *SendTimeoutError) Error() string {
	return fmt.Sprintf("mash: send to %s timed out; the message may or may not have been delivered. "+
		"Retry with --id %s (the send tool's id) within %s to send it at most once; after that a retry may store a second copy. (%v)",
		sap.Display(e.To), e.Key, DuplicateWindowText(), e.Err) + e.ambiguousClause()
}

func (e *SendTimeoutError) ambiguousClause() string {
	if !e.Ambiguous {
		return ""
	}
	return "; a permission violation on this subject was seen while several sends were in flight, so it could not be attributed (mashctl endpoint permissions ADDRESS shows what an endpoint may publish)"
}

// DuplicateWindowText is the inbox stream's duplicate window in words ("2 minutes"), for messages and docs.
func DuplicateWindowText() string {
	d := store.InboxDuplicateWindow
	if d%time.Minute == 0 {
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", n)
	}
	return d.String()
}

func (e *SendTimeoutError) Unwrap() error { return e.Err }

func newSendKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// SendWith is Send with options.
func (e *Endpoint) SendWith(ctx context.Context, to, text string, o SendOptions) (uint64, error) {
	r, err := e.SendDetailed(ctx, to, text, o)
	return r.Seq, err
}

// SendDetailed is SendWith that also reports a duplicate. Every send carries an idempotency key, the caller's (o.ID)
// or a random one; the message's nonce and Nats-Msg-Id derive from it, so a retry with the key is dropped by the
// stream. When the acknowledgement times out the error is a *SendTimeoutError naming the key.
func (e *Endpoint) SendDetailed(ctx context.Context, to, text string, o SendOptions) (SendResult, error) {
	to = sap.KeyOf(to) // the dotted owner.group.instance names the same recipient as its flat key
	// The name rule comes first: before the inbox or the directory is read, and nothing is dropped silently.
	if err := e.checkSelf(pointSendSender); err != nil {
		return SendResult{}, err
	}
	if err := e.checkKey(pointSendRecipient, to); err != nil {
		return SendResult{}, err
	}
	inReplyTo := o.InReplyTo
	var viaOrigin string
	if o.Via > 0 {
		var err error
		if viaOrigin, err = e.viaOrigin(ctx, o.Via); err != nil {
			return SendResult{}, err
		}
	}
	key := o.ID
	if key != "" {
		if err := ValidateSendID(key); err != nil {
			return SendResult{}, err
		}
	}
	rec, err := e.lookup(ctx, to)
	if err != nil {
		return SendResult{}, err
	}
	k, err := kind.Parse(string(o.Kind))
	if err != nil {
		return SendResult{}, fmt.Errorf("mash: %w", err)
	}
	data := []byte(text)
	if len(data) == 0 {
		return SendResult{}, errors.New("mash: empty message")
	}
	if key == "" {
		if key, err = newSendKey(); err != nil {
			return SendResult{}, err
		}
	}
	h, err := e.id.Sign(rec.MailSubject, data, e.key, time.Now(), SendNonce(e.key, to, key))
	if err != nil {
		return SendResult{}, err
	}
	msg := nats.NewMsg(rec.MailSubject)
	msg.Data = data
	msg.Header.Set(sap.HeaderName, h.Encode())
	msg.Header.Set(jetstream.MsgIDHeader, h.Nonce)
	if iv, err := e.id.SignInstance(h, e.instance); err == nil {
		msg.Header.Set(InstanceHeader, iv)
	}
	if inReplyTo > 0 {
		v, err := e.id.SignInReplyTo(h, inReplyTo)
		if err != nil {
			return SendResult{}, err
		}
		msg.Header.Set(InReplyToHeader, v)
	}
	if o.Via > 0 {
		v, err := e.id.SignVia(h, viaOrigin, o.Via)
		if err != nil {
			return SendResult{}, err
		}
		msg.Header.Set(ViaHeader, v)
	}
	msg.Header.Set(KindHeader, string(k))
	if o.Urgent {
		u, err := e.id.SignUrgent(h)
		if err != nil {
			return SendResult{}, err
		}
		msg.Header.Set(UrgentHeader, u)
	}
	if size := len(data) + headerSize(msg.Header); size > sendLimit(e.nc) {
		if limit := int(e.nc.MaxPayload()); limit > 0 && limit < store.MaxMsgSize {
			return SendResult{}, fmt.Errorf("mash: message is %d bytes with headers; this server accepts at most %d (its max_payload). "+
				"Send a smaller message or put the content in a file and send the path.", size, limit)
		}
		return SendResult{}, fmt.Errorf("mash: message is %d bytes with headers; the limit is %d", size, store.MaxMsgSize)
	}
	// A publish the server refuses by permissions gets no reply, only a violation on the connection's error handler
	// (refusal.go). Register for the subject first, publish in a goroutine, and take whichever comes first: the ack,
	// a refusal, or the caller's deadline (which PublishMsg itself honours through pctx).
	// After a send on this subject ended unsettled, first order any violation it left in flight ahead of this send;
	// when that cannot be done this send cannot be attributed a definite refusal.
	refused, release, err := e.refusals.begin(ctx, msg.Subject)
	if err != nil {
		// Nothing was published: the caller's deadline ended while another send's barrier ran.
		return SendResult{}, &SendTimeoutError{To: to, Key: key, Err: err}
	}
	settled := true
	defer func() { release(settled) }()
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type published struct {
		ack *jetstream.PubAck
		err error
	}
	done := make(chan published, 1)
	go func() {
		a, err := e.js.PublishMsg(pctx, msg)
		done <- published{a, err}
	}()
	var ack *jetstream.PubAck
	select {
	case p := <-done:
		ack, err = p.ack, p.err
	case <-refused:
		// Matching is by subject only, so a violation raised for another send to this subject could land while
		// ours was acked. Prefer an ack that is already here.
		select {
		case p := <-done:
			ack, err = p.ack, p.err
			if err != nil {
				return SendResult{}, &RefusedError{To: to, Subject: msg.Subject, Key: key}
			}
		default:
			return SendResult{}, &RefusedError{To: to, Subject: msg.Subject, Key: key}
		}
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
			settled = false
			e.refusals.markDirty(msg.Subject)
			return SendResult{}, &SendTimeoutError{To: to, Key: key, Err: err, Ambiguous: e.refusals.sawAmbiguous(msg.Subject)}
		}
		return SendResult{}, fmt.Errorf("mash: send to %s: %w", sap.Display(to), err)
	}
	if !ack.Duplicate {
		e.relaySent(to, o.Via, viaOrigin)
	}
	return SendResult{Seq: ack.Sequence, Duplicate: ack.Duplicate}, nil
}

// viaOrigin names the sender of message seq of this endpoint's own inbox, for a Mash-Via declaration. The original need
// not be verified (relaying unverified mail is still worth declaring), but it must exist and its sender name must be a
// valid endpoint name: a claimed name that is not one is never put into a signed declaration.
func (e *Endpoint) viaOrigin(ctx context.Context, seq uint64) (string, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return "", err
	}
	raw, err := s.GetMsg(ctx, seq)
	if err != nil {
		return "", fmt.Errorf("mash: no message #%d in your inbox to relay", seq)
	}
	h, trust := sap.VerifyStored(raw.Header.Get(sap.HeaderName), raw.Subject, raw.Data)
	if trust == sap.TrustForged || trust == sap.TrustMalformed || trust == sap.TrustAbsent || !validDirectoryKey(h.Name) {
		return "", fmt.Errorf("mash: message #%d names no sender that can be declared as the origin of a relay", seq)
	}
	return h.Name, nil
}

// sendLimit is the largest message, headers included, a send may be: mash's own cap, or the connected server's
// max_payload (from its INFO) when that is smaller. Hosted servers cap it below 1 MiB (Synadia's free tier: 512 KiB),
// and the client would otherwise fail a send between the two with a bare "maximum payload exceeded".
func sendLimit(nc *nats.Conn) int {
	if mp := int(nc.MaxPayload()); mp > 0 && mp < store.MaxMsgSize {
		return mp
	}
	return store.MaxMsgSize
}

func headerSize(h nats.Header) int {
	n := len("NATS/1.0\r\n\r\n")
	for k, vs := range h {
		for _, v := range vs {
			n += len(k) + 2 + len(v) + 2
		}
	}
	return n
}

// ownerOf is a sender's owner from the live directory copy, "" when it is not there: the channel line shows such a
// sender whole (render.Sender). It never reads the store: delivery does not wait on it.
func (e *Endpoint) ownerOf(name string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.dir[name].Owner
}

// lookup resolves a name through the directory (the live copy, else a read).
func (e *Endpoint) lookup(ctx context.Context, name string) (store.Record, error) {
	// A recipient may be written dotted, owner.group.instance.
	name = sap.KeyOf(name)
	if err := e.checkKey(pointLookup, name); err != nil { // before the directory is read: a gone record gets the rule, not "not found"
		return store.Record{}, err
	}
	if _, err := parseRecipient(name); err != nil {
		return store.Record{}, err
	}
	e.mu.RLock()
	rec, ok := e.dir[name]
	e.mu.RUnlock()
	if ok {
		return rec, nil
	}
	rec, err := store.GetRecord(ctx, e.dirKV, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return store.Record{}, &NoEndpointError{Name: sap.Display(name)}
		}
		return store.Record{}, fmt.Errorf("mash: looking up %q: %w", sap.Display(name), err)
	}
	return rec, nil
}

// NoEndpointError is the error for a recipient that is not in the directory.
type NoEndpointError struct{ Name string }

func (e *NoEndpointError) Error() string {
	return fmt.Sprintf("mash: no endpoint named %q (see who)", e.Name)
}

// inbox is this endpoint's inbox stream with its info as of now: the one the first wave of a one-shot client read, else
// one read now. The stream's CachedInfo is that info.
func (e *Endpoint) inbox(ctx context.Context) (jetstream.Stream, error) {
	s, err, ok := e.pre.takeInbox()
	if !ok {
		s, err = e.js.Stream(ctx, e.ident.InboxStream())
	}
	if err != nil {
		return nil, fmt.Errorf("mash: inbox: %w", err)
	}
	id := streamIdentity(s.CachedInfo())
	e.inboxID.Store(&id)
	return s, nil
}

func (e *Endpoint) toMail(raw *jetstream.RawStreamMsg) (render.Mail, bool) {
	from, trust, h, ok := e.classify(raw.Subject, raw.Header, raw.Data, raw.Time)
	if !ok {
		return render.Mail{Seq: raw.Sequence, From: "?", Trust: trust, At: raw.Time}, false
	}
	if id := e.inboxID.Load(); id != nil && e.refusedSeq(raw.Sequence, *id) { // refused earlier: stays refused
		return render.Mail{Seq: raw.Sequence, From: from, Trust: trust, At: raw.Time}, false
	}
	if e.refuses(trust) { // stored, but not shown: serve counted and dropped it (refuse.go)
		return render.Mail{Seq: raw.Sequence, From: from, Trust: trust, At: raw.Time}, false
	}
	replyTo, via := senderMarks(raw.Header, trust, h)
	via, _ = e.viaAllowed(via) // dropped here too; the serve path logs it
	return render.Mail{Seq: raw.Sequence, From: from, Trust: trust, At: raw.Time, Body: raw.Data, ReplyTo: replyTo, Via: via,
		Instance: senderInstance(raw.Header, trust, h), Owner: e.ownerOf(from),
		Urgent: trust == sap.TrustVerified && sap.VerifyUrgent(raw.Header.Get(UrgentHeader), h),
		Kind:   kind.Of(raw.Header.Get(KindHeader))}, true
}

// InboxState is what the server holds for this endpoint's inbox as of now: how many messages, how many of them have
// not reached the session, and how long the oldest of those has waited.
type InboxState struct {
	// Total is every message the inbox holds.
	Total int
	// Waiting is the messages the session has not been told of: the inbox consumer's pending and unacknowledged.
	Waiting int
	// AckFloor is the stream sequence up to which every message is acknowledged. A message above it is new (a message
	// acknowledged out of order above it, such as one read by the tool while an earlier one is still held, shows as new
	// until the floor passes it: the server does not list which messages it has acknowledged).
	AckFloor uint64
	// OldestWaiting is when the oldest waiting message was stored; zero when none is waiting.
	OldestWaiting time.Time
}

// InboxQuery selects the messages ReadInbox lists: those above After (the first Limit of them, oldest first) or, with
// After zero, the newest Limit; with OnlyNew only those the session has not been told of.
type InboxQuery struct {
	OnlyNew bool
	After   uint64
	Limit   int
}

// InboxRead is one reading of the inbox: the server's counts, the messages the query lists (oldest first) and how many
// of the waiting messages are urgent.
type InboxRead struct {
	State InboxState
	Mail  []render.Mail
	// NewUrgent is how many waiting messages are urgent, among the newest UrgentLook of them (the whole count when
	// fewer are waiting).
	NewUrgent int
}

// UrgentLook is how many of the newest waiting messages ReadInbox reads to count the urgent ones; a body can be a
// mebibyte, and a long backlog is not read whole for a count.
const UrgentLook = 200

// oldestLookups bounds how many sequences are tried for the oldest waiting message: a message that is gone (expired or
// deleted) is skipped, and a long run of them is not worth reading for.
const oldestLookups = 20

// ReadInbox reads the inbox consumer and stream, and the messages the query lists, the newest waiting ones and the
// oldest waiting one, all in one wave of requests. An endpoint with no consumer yet has acknowledged nothing: every
// message is waiting.
func (e *Endpoint) ReadInbox(ctx context.Context, q InboxQuery) (InboxRead, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return InboxRead{}, err
	}
	info := s.CachedInfo()
	first, last := info.State.FirstSeq, info.State.LastSeq
	// The stream keeps the refused mail it stored; the counts are of what the inbox shows.
	shown := max(int(info.State.Msgs)-e.refusedStored(first, last, streamIdentity(info)), 0)
	st := InboxState{Total: shown, Waiting: shown}
	cons, err, ok := e.pre.takeConsumer()
	if !ok {
		cons, err = e.js.Consumer(ctx, e.ident.InboxStream(), store.Consumer)
	}
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
	case err != nil:
		return InboxRead{}, fmt.Errorf("mash: inbox consumer: %w", err)
	default:
		ci := cons.CachedInfo()
		st.Waiting = int(ci.NumPending) + ci.NumAckPending
		st.AckFloor = ci.AckFloor.Stream
	}
	if st.Waiting < 0 {
		st.Waiting = 0
	}
	out := InboxRead{State: st}

	// The sequences to read: what the query lists (in the order it takes them), the newest waiting ones, and the oldest.
	var listSeqs []uint64
	floor := uint64(0)
	if q.OnlyNew {
		floor = st.AckFloor
	}
	if q.After > 0 {
		listSeqs = seqsUp(max(q.After+1, floor+1, first), last)
	} else {
		listSeqs = seqsDown(last, max(first, floor+1))
	}
	lower := max(st.AckFloor+1, first) // the oldest sequence that can be waiting
	var waitSeqs []uint64
	if st.Waiting > 0 && lower <= last {
		waitSeqs = seqsUp(max(lower, last+1-min(last+1-lower, UrgentLook)), last)
	}
	want := map[uint64]bool{}
	var seqs []uint64
	add := func(n uint64) {
		if !want[n] {
			want[n] = true
			seqs = append(seqs, n)
		}
	}
	firstBatch := listSeqs[:min(len(listSeqs), q.Limit)]
	for _, n := range firstBatch {
		add(n)
	}
	for _, n := range waitSeqs {
		add(n)
	}
	if st.Waiting > 0 && lower <= last {
		add(lower)
	}
	raws := make([]*jetstream.RawStreamMsg, len(seqs))
	errs := make([]error, len(seqs))
	parallel(len(seqs), fetchConcurrency, func(i int) { raws[i], errs[i] = s.GetMsg(ctx, seqs[i]) })
	got := map[uint64]*jetstream.RawStreamMsg{}
	var read []*jetstream.RawStreamMsg
	for i, n := range seqs {
		switch {
		case errs[i] == nil:
			got[n] = raws[i]
			read = append(read, raws[i])
		case errors.Is(errs[i], jetstream.ErrMsgNotFound):
		default:
			return InboxRead{}, fmt.Errorf("mash: inbox: read #%s: %w", FormatMsgRef(MsgRef{Seq: n}), errs[i])
		}
	}
	if err := e.loadSenders(ctx, read); err != nil {
		return InboxRead{}, fmt.Errorf("mash: inbox: %w", err)
	}
	mails := map[uint64]render.Mail{}
	for n, raw := range got {
		if m, ok := e.toMail(raw); ok {
			mails[n] = m
		}
	}
	for _, n := range firstBatch {
		if m, ok := mails[n]; ok {
			out.Mail = append(out.Mail, m)
		}
	}
	if len(out.Mail) < q.Limit && len(listSeqs) > len(firstBatch) { // gone or forged ones in the way: read on
		more, err := e.collect(ctx, s.GetMsg, listSeqs[len(firstBatch):], q.Limit-len(out.Mail))
		if err != nil {
			return InboxRead{}, err
		}
		out.Mail = append(out.Mail, more...)
	}
	if q.After == 0 {
		sortMail(out.Mail)
	}
	if st.Waiting > 0 {
		for n, m := range mails {
			if n > st.AckFloor && m.Urgent {
				out.NewUrgent++
			}
		}
		oldest := time.Time{}
		for n, tries := lower, 0; n <= last && tries < oldestLookups; n, tries = n+1, tries+1 {
			raw := got[n]
			if raw == nil && !want[n] { // the first was gone: look at the next
				r, err := s.GetMsg(ctx, n)
				if errors.Is(err, jetstream.ErrMsgNotFound) {
					continue
				}
				if err != nil {
					return InboxRead{}, fmt.Errorf("mash: inbox: read #%d: %w", n, err)
				}
				raw = r
			}
			if raw != nil {
				oldest = raw.Time
				break
			}
		}
		out.State.OldestWaiting = oldest
	}
	return out, nil
}

// Read returns message seq from this endpoint's inbox.
func (e *Endpoint) Read(ctx context.Context, seq uint64) (render.Mail, error) {
	return e.ReadRef(ctx, MsgRef{Seq: seq})
}

// NoMessageError is the error of a read of a message the inbox does not hold (never stored, expired or dropped).
type NoMessageError struct {
	Seq      uint64
	Endpoint string
}

func (e *NoMessageError) Error() string {
	return fmt.Sprintf("mash: no message #%d in %s's inbox", e.Seq, e.Endpoint)
}

// ReadRef returns the message r names, of the endpoint's inbox.
func (e *Endpoint) ReadRef(ctx context.Context, r MsgRef) (render.Mail, error) {
	if r.Stream != "" {
		return render.Mail{}, fmt.Errorf("mash: no stream %q on this endpoint", r.Stream)
	}
	s, err := e.inbox(ctx)
	if err != nil {
		return render.Mail{}, err
	}
	if e.refusedSeq(r.Seq, streamIdentity(s.CachedInfo())) {
		return render.Mail{}, &RefusedMessageError{Seq: r.Seq} // refused mail is not there to read
	}
	raw, err := s.GetMsg(ctx, r.Seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return render.Mail{}, &NoMessageError{Seq: r.Seq, Endpoint: e.Display()}
	}
	if err != nil {
		return render.Mail{}, fmt.Errorf("mash: read #%d: %w", r.Seq, err)
	}
	if err := e.loadSenders(ctx, []*jetstream.RawStreamMsg{raw}); err != nil {
		return render.Mail{}, err
	}
	m, ok := e.toMail(raw)
	if !ok && e.refuses(m.Trust) && m.Trust != sap.TrustForged && m.Trust != sap.TrustMalformed {
		return render.Mail{}, &RefusedMessageError{Seq: r.Seq}
	}
	if !ok {
		return m, fmt.Errorf("mash: message #%s has a %s sender header and is not shown", FormatMsgRef(r), m.Trust)
	}
	return m, nil
}

// Inbox returns up to limit of the most recent messages in this endpoint's
// inbox, oldest first. Forged mail is skipped.
func (e *Endpoint) Inbox(ctx context.Context, limit int) ([]render.Mail, error) {
	return e.InboxAbove(ctx, 0, limit)
}

// InboxAbove is Inbox for the messages above stream sequence floor only: up to limit of the most recent, oldest first.
func (e *Endpoint) InboxAbove(ctx context.Context, floor uint64, limit int) ([]render.Mail, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return nil, err
	}
	info := s.CachedInfo()
	out, err := e.collect(ctx, s.GetMsg, seqsDown(info.State.LastSeq, max(info.State.FirstSeq, floor+1)), limit)
	sortMail(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, err
}

// sortMail orders mail by store time, then stream (primary first), then sequence.
func sortMail(ms []render.Mail) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return refOf(a).Less(refOf(b))
	})
}

// InboxAfter returns up to limit of the messages after seq in this endpoint's
// own (primary) inbox, oldest first: a reader pages forward by passing the last seq it got,
// which the newest-first window of Inbox cannot do once it has fallen behind.
// Forged mail is skipped.
func (e *Endpoint) InboxAfter(ctx context.Context, after uint64, limit int) ([]render.Mail, error) {
	return e.InboxAfterRef(ctx, MsgRef{Seq: after}, limit)
}

// InboxAfterRef is InboxAfter for a message reference.
func (e *Endpoint) InboxAfterRef(ctx context.Context, after MsgRef, limit int) ([]render.Mail, error) {
	if after.Stream != "" {
		return nil, fmt.Errorf("mash: no stream %q on this endpoint", after.Stream)
	}
	s, err := e.inbox(ctx)
	if err != nil {
		return nil, err
	}
	info := s.CachedInfo()
	return e.collect(ctx, s.GetMsg, seqsUp(max(after.Seq+1, info.State.FirstSeq), info.State.LastSeq), limit)
}

// getMsg is jetstream.Stream.GetMsg, replaceable in tests.
type getMsg func(ctx context.Context, seq uint64, opts ...jetstream.GetMsgOpt) (*jetstream.RawStreamMsg, error)

// collect reads seqs in the order given until it has limit messages. A seq
// that is gone (expired or dropped by the stream's limits) is skipped, as is
// forged mail; any other read error ends the listing and is returned, so a
// broken connection is not mistaken for an empty inbox.
func (e *Endpoint) collect(ctx context.Context, get getMsg, seqs []uint64, limit int) ([]render.Mail, error) {
	var out []render.Mail
	// Seqs are read a batch at a time, many requests in flight at once. A batch is no larger than what is still
	// wanted, so the limit is never passed; seqs that turn out to be gone or forged leave the next batch to top up.
	// A batch is judged in the order given, so the result is what a read one seq at a time would have been.
	for len(seqs) > 0 && len(out) < limit {
		n := min(limit-len(out), len(seqs))
		batch := seqs[:n]
		seqs = seqs[n:]
		raws := make([]*jetstream.RawStreamMsg, n)
		errs := make([]error, n)
		parallel(n, fetchConcurrency, func(i int) { raws[i], errs[i] = get(ctx, batch[i]) })
		var read []*jetstream.RawStreamMsg
		for i, raw := range raws {
			if errs[i] == nil {
				read = append(read, raw)
			}
		}
		if err := e.loadSenders(ctx, read); err != nil {
			return out, fmt.Errorf("mash: inbox: %w", err)
		}
		for i, seq := range batch {
			if errors.Is(errs[i], jetstream.ErrMsgNotFound) {
				continue
			}
			if errs[i] != nil {
				return out, fmt.Errorf("mash: inbox: read #%s: %w", FormatMsgRef(MsgRef{Seq: seq}), errs[i])
			}
			if m, ok := e.toMail(raws[i]); ok {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// seqsDown is last..first, newest first; seqsUp is from..to. Both are empty
// for an empty stream (seq 0) or an empty range.
func seqsDown(last, first uint64) []uint64 {
	var out []uint64
	for seq := last; seq >= max(first, 1); seq-- {
		out = append(out, seq)
	}
	return out
}

func seqsUp(from, to uint64) []uint64 {
	var out []uint64
	for seq := max(from, 1); seq <= to; seq++ {
		out = append(out, seq)
	}
	return out
}

// deliverBatch is the channel path: show goes out as one event, then is acked (a failed write naks it for redelivery).
func (e *Endpoint) deliverBatch(deliver Deliverer, show []pending, seen *seenSet) {
	if show = e.dropUnleased(show); len(show) == 0 {
		return // the lease is gone: nothing is delivered or acked
	}
	mails := make([]render.Mail, len(show))
	seqs := make([]string, len(show))
	for i, p := range show {
		mails[i] = p.mail
		seqs[i] = FormatMsgRef(refOf(p.mail))
	}
	text := render.Batch(e.cfg.Name, e.cfg.Owner, mails, e.cfg.BatchMsgs, e.cfg.BatchBytes)
	meta := map[string]string{"to": e.cfg.Name, "count": strconv.Itoa(len(show)), "seqs": strings.Join(seqs, ",")}
	if err := deliver(text, meta); err != nil {
		e.log.Printf("delivery failed, will redeliver: %v", err)
		for _, p := range show {
			_ = p.msg.NakWithDelay(500 * time.Millisecond)
		}
		return
	}
	for _, p := range show {
		seen.mark(p)
		if e.mayAck(p) {
			if err := p.msg.Ack(); err != nil {
				e.log.Printf("ack #%s: %v", p.mail.Num(), err)
			}
		}
		e.waking.done(refOf(p.mail))
	}
}
