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
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
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

// shownName is the sender name as displayed: a self-declared name only when it
// is a valid endpoint name, else a short form of the key (never raw text).
func shownName(h sap.SenderHeader) string {
	// Only a legacy-valid name is shown for a sender that is not proven: a flat key is shown only from the directory
	// (classify), never as a self-declared name.
	if config.ValidName(h.Name) {
		return h.Name
	}
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

// inboxSource is one stream an endpoint's consumer reads: the primary inbox stream (ref stream "") and, for an
// endpoint with an alias, the legacy inbox stream (LegacyStream) through its existing durable consumer.
type inboxSource struct {
	ref    string // the MsgRef stream: "" or LegacyStream
	stream string // the JetStream stream name
	cons   jetstream.Consumer
}

// sources are the streams this endpoint reads, primary first.
func (e *Endpoint) sources() []inboxSource {
	out := []inboxSource{{ref: "", stream: sap.InboxStream(e.key)}}
	if e.hasLegacyStream() {
		out = append(out, inboxSource{ref: LegacyStream, stream: sap.InboxStream(e.legacyKey)})
	}
	return out
}

// consume starts the inbox consumer and its delivery worker. The consumer's
// callback only classifies and queues; one worker goroutine gathers messages
// until SettleMS pass with no new one, pushes them as one event, then acks
// them (a failed write naks them for redelivery). Being the only writer, the
// worker keeps stream order and makes de-duplication exact. The returned
// stop waits for the consumer to close and the worker to finish.
//
// An endpoint with an alias consumes a second stream, the legacy inbox stream, through the durable consumer it already
// has there (never a new one: a fresh consumer would deliver mail the old one acknowledged). Both feed the one queue,
// so the worker is still the only writer. A message's ack goes to its own delivery, whichever stream it came from.
//
// The legacy stream is consumed only while this endpoint holds the legacy identity's lease (legacylease.go): without
// it the stream is left to its holder (the consumer is not even created here), and when the lease is lost mid-session
// its consume loop is stopped while the own stream's keeps running.
func (e *Endpoint) consume(ctx context.Context, deliver Deliverer) (func(), error) {
	srcs := e.sources()
	if !e.legacyReading() {
		kept := srcs[:0]
		for _, src := range srcs {
			if src.ref != LegacyStream {
				kept = append(kept, src)
			}
		}
		srcs = kept
	}
	for i := range srcs {
		cons, err := e.js.Consumer(ctx, srcs[i].stream, store.Consumer)
		if err != nil {
			if srcs[i].ref == LegacyStream {
				return nil, fmt.Errorf("mash: legacy inbox consumer: %w", err)
			}
			return nil, fmt.Errorf("mash: inbox consumer: %w", err)
		}
		srcs[i].cons = cons
	}
	queue := make(chan pending, maxPending)
	// The startup messages are those a consumer held unacked or pending, all at or below Delivered + NumPending, read in
	// one consumer info (no extra round trip; startup is budgeted). A gap among the pending (a deleted message) only
	// lowers this bound, which delays readiness to the server's rule and never makes it early; a later arrival is above it.
	starts := map[string]streamStart{}
	probes := map[string]wakingProbe{}
	floors := map[string]func() (uint64, bool){}
	for _, src := range srcs {
		ci := src.cons.CachedInfo()
		starts[src.ref] = streamStart{Backlog: int(ci.NumPending) + ci.NumAckPending, Last: ci.Delivered.Stream + ci.NumPending}
		probes[src.ref] = e.wakingProbe(src.cons, src.stream)
		floors[src.ref] = e.consumerAckFloor(src.cons)
	}
	e.waking.startStreams(starts, true, probes)
	quit := make(chan struct{})
	workerDone := make(chan struct{})
	go e.deliverLoop(deliver, queue, quit, workerDone, floors)

	handler := func(stream string) func(m jetstream.Msg) {
		return func(m jetstream.Msg) {
			meta, err := m.Metadata()
			if err != nil {
				e.log.Printf("message without metadata: %v", err)
				_ = m.Term()
				return
			}
			ref := MsgRef{Stream: stream, Seq: meta.Sequence.Stream}
			if stream == LegacyStream && !e.legacyReading() {
				return // the lease is gone: not queued, not acked; the holder's consumer is redelivered it
			}
			defer e.waking.received(ref)
			// The sender's signature binds the subject the message was stored under, so classify checks it against the
			// stored subject: a legacy-stream message keeps its legacy subject and verifies as it always did.
			from, trust, h, ok := e.classify(m.Subject(), m.Headers(), m.Data(), meta.Timestamp)
			if !ok {
				e.dropped.Add(1)
				e.log.Printf("dropped #%s: %s sender header (claimed user %s)", FormatMsgRef(ref), trust, h.User)
				_ = m.Ack()
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
			p := pending{msg: m, key: key, ts: h.TS, mail: render.Mail{ReplyTo: replyTo, Via: via, Seq: ref.Seq, Stream: stream, From: from, Trust: trust,
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
	}
	var ccs []jetstream.ConsumeContext
	var watched sync.WaitGroup
	stopAll := func() {
		for _, cc := range ccs {
			cc.Stop()
		}
		for _, cc := range ccs {
			select {
			case <-cc.Closed():
			case <-time.After(5 * time.Second):
			}
		}
	}
	for _, src := range srcs {
		cc, err := src.cons.Consume(handler(src.ref), jetstream.PullMaxMessages(100))
		if err != nil {
			stopAll()
			close(quit)
			<-workerDone
			return nil, fmt.Errorf("mash: consume: %w", err)
		}
		ccs = append(ccs, cc)
		if src.ref == LegacyStream {
			// A lease lost mid-session stops this consumer only.
			watched.Add(1)
			go func() {
				defer watched.Done()
				select {
				case <-e.legacy.lost:
					cc.Stop()
					e.log.Printf("legacy inbox: stopped reading %s (the lease was lost); the own inbox is still read", e.legacyKey)
				case <-quit:
				}
			}()
		}
	}
	return func() {
		stopAll()
		close(quit)
		<-workerDone
		watched.Wait()
	}, nil
}

// deliverLoop is the single writer of channel events (see consume).
func (e *Endpoint) deliverLoop(deliver Deliverer, queue <-chan pending, quit <-chan struct{}, done chan<- struct{}, ackFloors map[string]func() (uint64, bool)) {
	defer close(done)
	settle := time.Duration(e.cfg.SettleMS) * time.Millisecond
	seen := newSeenSet(e.dupWindow()) // signed mail already shown, see seenSet
	seen.ackFloors = ackFloors
	h := &held{msgs: map[spool.Ref]pending{}, at: map[spool.Ref]time.Time{}, rung: map[spool.Ref]bool{}, fails: map[spool.Ref]int{}}
	tick := time.NewTicker(SpoolCheck)
	defer tick.Stop()
	for {
		var batch []pending
		select {
		case <-quit:
			return // held messages stay unacked: redelivered to the next session
		case <-tick.C:
			e.settleHeld(deliver, h)
			continue
		case p := <-queue:
			batch = append(batch, p)
		}
	gather:
		for len(batch) < maxPending {
			select {
			case <-quit:
				return // unacked: redelivered to the next session
			case p := <-queue:
				batch = append(batch, p)
			case <-time.After(settle):
				break gather
			}
		}
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
			_ = p.msg.Ack()
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
		e.To, e.Key, DuplicateWindowText(), e.Err) + e.ambiguousClause()
}

func (e *SendTimeoutError) ambiguousClause() string {
	if !e.Ambiguous {
		return ""
	}
	return "; a permission violation on this subject was seen while several sends were in flight, so it could not be attributed (mashctl policy explain shows whether the pair is allowed)"
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
		return SendResult{}, fmt.Errorf("mash: send to %s: %w", to, err)
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
	if err := e.checkKey(pointLookup, name); err != nil { // before the directory is read: a gone record gets the rule, not "not found"
		return store.Record{}, err
	}
	if !validDirectoryKey(name) {
		return store.Record{}, fmt.Errorf("mash: %q is not an endpoint key: give a legacy endpoint name or the key as who shows it (agent_owner_name)", name)
	}
	e.mu.RLock()
	rec, ok := e.dir[name]
	e.mu.RUnlock()
	if ok {
		return rec, nil
	}
	rec, err := store.GetRecord(ctx, e.dirKV, name)
	if err != nil {
		return store.Record{}, fmt.Errorf("mash: no endpoint named %q (see who)", name)
	}
	return rec, nil
}

// inbox is this endpoint's inbox stream with its info as of now: the one the first wave of a one-shot client read, else
// one read now. The stream's CachedInfo is that info.
func (e *Endpoint) inbox(ctx context.Context) (jetstream.Stream, error) {
	s, err, ok := e.pre.takeInbox()
	if !ok {
		s, err = e.js.Stream(ctx, sap.InboxStream(e.key))
	}
	if err != nil {
		return nil, fmt.Errorf("mash: inbox: %w", err)
	}
	return s, nil
}

func (e *Endpoint) toMail(raw *jetstream.RawStreamMsg) (render.Mail, bool) {
	from, trust, h, ok := e.classify(raw.Subject, raw.Header, raw.Data, raw.Time)
	if !ok {
		return render.Mail{Seq: raw.Sequence, From: "?", Trust: trust, At: raw.Time}, false
	}
	replyTo, via := senderMarks(raw.Header, trust, h)
	via, _ = e.viaAllowed(via) // dropped here too; the serve path logs it
	return render.Mail{Seq: raw.Sequence, From: from, Trust: trust, At: raw.Time, Body: raw.Data, ReplyTo: replyTo, Via: via,
		Instance: senderInstance(raw.Header, trust, h), Kind: kind.Of(raw.Header.Get(KindHeader))}, true
}

// streamOf is the stream of this endpoint's inbox that refs of the given ref-stream live in, with its info as of now: the
// primary one is what the first wave of a one-shot client read (e.inbox), the legacy one is read now. A legacy ref on
// an endpoint without that stream is ErrNoLegacyStream.
func (e *Endpoint) streamOf(ctx context.Context, ref string) (jetstream.Stream, error) {
	switch ref {
	case "":
		return e.inbox(ctx)
	case LegacyStream:
		if !e.hasLegacyStream() {
			return nil, fmt.Errorf("mash: %w", ErrNoLegacyStream)
		}
		s, err := e.js.Stream(ctx, sap.InboxStream(e.legacyKey))
		if err != nil {
			return nil, fmt.Errorf("mash: legacy inbox: %w", err)
		}
		return s, nil
	}
	return nil, fmt.Errorf("mash: no stream %q on this endpoint", ref)
}

// Read returns message seq from this endpoint's own (primary) inbox.
func (e *Endpoint) Read(ctx context.Context, seq uint64) (render.Mail, error) {
	return e.ReadRef(ctx, MsgRef{Seq: seq})
}

// ReadRef returns the message r names: of the endpoint's own inbox, or (r.Stream == LegacyStream) of the legacy inbox
// stream it reads during the transition. The mail carries its stream, so it is shown with its qualified number.
func (e *Endpoint) ReadRef(ctx context.Context, r MsgRef) (render.Mail, error) {
	s, err := e.streamOf(ctx, r.Stream)
	if err != nil {
		return render.Mail{}, err
	}
	raw, err := s.GetMsg(ctx, r.Seq)
	if err != nil {
		if r.Stream == LegacyStream {
			return render.Mail{}, fmt.Errorf("mash: no message #%s in %s's legacy inbox", FormatMsgRef(r), e.legacyKey)
		}
		return render.Mail{}, fmt.Errorf("mash: no message #%d in %s's inbox", r.Seq, e.key)
	}
	if err := e.loadSenders(ctx, []*jetstream.RawStreamMsg{raw}); err != nil {
		return render.Mail{}, err
	}
	m, ok := e.toMail(raw)
	m.Stream = r.Stream
	if !ok {
		return m, fmt.Errorf("mash: message #%s has a %s sender header and is not shown", FormatMsgRef(r), m.Trust)
	}
	return m, nil
}

// Inbox returns up to limit of the most recent messages in this endpoint's
// inbox, oldest first. Forged mail is skipped. An endpoint that also reads a legacy stream lists both, merged by
// store time (ties by stream, then sequence), each with its own number.
func (e *Endpoint) Inbox(ctx context.Context, limit int) ([]render.Mail, error) {
	var out []render.Mail
	for _, src := range e.sources() {
		s, err := e.streamOf(ctx, src.ref)
		if err != nil {
			return nil, err
		}
		info := s.CachedInfo()
		ms, err := e.collectStream(ctx, src.ref, s.GetMsg, seqsDown(info.State.LastSeq, info.State.FirstSeq), limit)
		out = append(out, ms...)
		if err != nil {
			sortMail(out)
			return out, err
		}
	}
	sortMail(out)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
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

// InboxAfterRef is InboxAfter for a message of either stream: it lists the messages after after in after's own
// stream (a page forward is per stream, because two streams' numbers are not one sequence).
func (e *Endpoint) InboxAfterRef(ctx context.Context, after MsgRef, limit int) ([]render.Mail, error) {
	s, err := e.streamOf(ctx, after.Stream)
	if err != nil {
		return nil, err
	}
	info := s.CachedInfo()
	return e.collectStream(ctx, after.Stream, s.GetMsg, seqsUp(max(after.Seq+1, info.State.FirstSeq), info.State.LastSeq), limit)
}

// getMsg is jetstream.Stream.GetMsg, replaceable in tests.
type getMsg func(ctx context.Context, seq uint64, opts ...jetstream.GetMsgOpt) (*jetstream.RawStreamMsg, error)

// collect reads seqs in the order given until it has limit messages. A seq
// that is gone (expired or dropped by the stream's limits) is skipped, as is
// forged mail; any other read error ends the listing and is returned, so a
// broken connection is not mistaken for an empty inbox.
func (e *Endpoint) collect(ctx context.Context, get getMsg, seqs []uint64, limit int) ([]render.Mail, error) {
	return e.collectStream(ctx, "", get, seqs, limit)
}

// collectStream is collect for the stream of the given ref-stream: the mail it returns carries that stream.
func (e *Endpoint) collectStream(ctx context.Context, stream string, get getMsg, seqs []uint64, limit int) ([]render.Mail, error) {
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
				return out, fmt.Errorf("mash: inbox: read #%s: %w", FormatMsgRef(MsgRef{Stream: stream, Seq: seq}), errs[i])
			}
			if m, ok := e.toMail(raws[i]); ok {
				m.Stream = stream
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
		if err := p.msg.Ack(); err != nil {
			e.log.Printf("ack #%s: %v", p.mail.Num(), err)
		}
		e.waking.done(refOf(p.mail))
	}
}
