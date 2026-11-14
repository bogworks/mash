package endpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// Hook delivery (mash v0.2.0). Once a Claude Code hook (`mash hook`) has run in this session, mail goes to the spool instead
// of the channel. A message is held unacked while spooled and acked once a hook has shown it, so the inbox consumer's
// unread count keeps meaning "not yet seen". A hook runs after every tool call and with every prompt, so a busy session
// sees its mail with no turn of its own; only mail left unshown for the idle wake (45 s by default) wakes the session,
// with one channel event that is the summary line alone (its UserPromptSubmit hook then shows the mail). Only mail
// whose kind wakes (blocker, question, task; or urgent) counts for that: result and fyi mail waits for the next hook
// run and never wakes the session, but is covered by the summary when a waking message is held; unless a backlog of it
// reaches half the consumer's limit (SpoolWakeCount) or a quarter of the inbox's byte limit (SpoolWakeBytes), when it wakes the session to drain. A wake that lands no
// turn is repeated while a waking message is still held, with backoff (2x the idle wake after the first wake, then 4x,
// doubling, to 30 minutes); a drain resets it, and with no hook run in the session at all it stops after HooklessWakes
// wakes (hookless.go, ADR 0041). Before any hook has run, delivery is the channel's, as before, and
// nothing is spooled, so no message is ever shown twice across the switch-over. A session marked SpoolFromStart
// is hooked from the start: mail is spooled from the first message and acked only once a hook has drained it, never on a
// channel write; a wake is a hint, not a receipt. Claude opts in (MASH_SPOOL_FROM_START) because it drops channel events
// that arrive before its first user message, so an early channel ack loses the mail. A host that also takes no channel
// delivery at all (NoChannel: Pi), or whose operator set MASH_SPOOL_REQUIRED (spoolrequired.go), never falls back to the channel: mail the spool cannot take is held unacked.

var (
	// SpoolCheck is how often serve looks for messages the hook has drained.
	SpoolCheck = 250 * time.Millisecond
	// SpoolKeepAlive is how often a held message's ack deadline (the consumer's 30 s AckWait) is pushed back.
	SpoolKeepAlive = 10 * time.Second
	// SpoolMaxBytes bounds the held bodies. Past it, new mail is not spooled but left in NATS (nak with a delay),
	// where the unread count shows it: never dropped. The consumer's MaxAckPending bounds the count.
	SpoolMaxBytes = 32 << 20
	// SpoolWakeCount is the held count at which mail that never wakes (result, fyi) wakes the session anyway, to drain
	// before the backlog fills the consumer's MaxAckPending (1000, internal/store) and waking mail behind it cannot be
	// delivered: half of it.
	SpoolWakeCount = 500
	// SpoolWakeBytes does the same for held bodies, well below the inbox stream's InboxMaxBytes (16 MiB, headers
	// included), past which NATS discards the oldest messages, unacked ones too (codex H1 on #75): a quarter of it.
	SpoolWakeBytes = store.InboxMaxBytes / 4
	// SpoolWakeHardCount is the held count, as a multiple of SpoolWakeCount (3/2: 750 at the default), past which the
	// drain no longer holds back a crowded wake. Growth detection after a drain is best effort (arrivals can land in
	// the same pass as the drain), so this line does not depend on it: it stays well under the consumer's 1,000
	// pending-message limit (ADR 0069).
	spoolWakeHardNum, spoolWakeHardDen = 3, 2
	// IdleWake is how long spooled mail waits for a hook before serve wakes the session: an idle session runs none. It is
	// the default when Config.IdleWake is unset (production sets it: MASH_IDLE_WAKE / "idle_wake"), and the tests' knob.
	IdleWake       = 45 * time.Second
	spoolFullDelay = 30 * time.Second
)

// SetSpool hands delivery to s once a hook has run in this session (after this call). Call it before Run, and only
// once this serve holds the lease: owner is its owner record (host.Adapter.OwnerToken), which then owns the spool.
func (e *Endpoint) SetSpool(s spool.Spool, owner string) error {
	if err := s.Init(); err != nil {
		return err
	}
	// Hooks are excluded while the spool changes hands: one that passed its owner check before this rebind must not
	// drain the new session's mail (codex H1 on #21, round 7).
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	// Tombstones are keyed by stream sequence, which restarts when the stream is recreated: bind the spool to this
	// stream's identity so an old stream's tombstone never acks new mail (codex H1 on #21, round 2).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Asked for here, never taken from a read made earlier (e.inbox may hand back one a one-shot client prefetched):
	// a stream recreated since, while this session waited for the lease, would bind the spool to the old identity,
	// keep its tombstones, and ack new mail unshown (codex H1 on #121). It is the one stream-info request serve's
	// startup makes, so it adds no wave.
	st, err := e.js.Stream(ctx, sap.InboxStream(e.key))
	if err != nil {
		return fmt.Errorf("mash: inbox: %w", err)
	}
	info := st.CachedInfo() // the response to the request just made
	if err := s.BindStream("", info.Config.Name+"@"+info.Created.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if e.alias != nil {
		// The legacy stream is bound on its own: its sequences are not the primary stream's, and it too can be recreated.
		lst, err := e.js.Stream(ctx, sap.InboxStream(e.legacyKey))
		if err != nil {
			return fmt.Errorf("mash: legacy inbox: %w", err)
		}
		linfo := lst.CachedInfo()
		if err := s.BindStream(LegacyStream, linfo.Config.Name+"@"+linfo.Created.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	// The previous session's waiting files are stale: the hook timestamp resets with this serve, so a later hook
	// would print them again while NATS redelivers the unacked mail. Clear them; the redelivery is spooled again or
	// channel-delivered, never both (codex M1 on #21).
	if err := s.ClearWaiting(); err != nil {
		return err
	}
	// Only the lease holder gets here: bind the spool to its session, so a waiting session's hooks (same MASH_NAME)
	// cannot drain this one's mail (codex H1 on #21, round 3).
	legacy := ""
	if owner != "" {
		legacy = spool.LegacyToken(owner) // an older hook reads only this, and so still enforces ancestry
	}
	if err := s.OwnRecord(legacy, owner); err != nil {
		return err
	}
	e.spool, e.since = &s, time.Now()
	e.epochSpool.Store(&s)
	if err := e.noteLegacyEpochLocked(); err != nil && e.legacyReading() { // under the lock SetSpool holds: the file names the hold in force, or none
		e.failLegacyHold(err, true) // the hold's entries would be hidden and never drained: give it up
	}
	e.hl.bind(s, e.since)
	return nil
}

// SpoolFromStart counts the session as hooked from the start: with a spool set, mail is spooled from the first message,
// as if a hook had already run, and nothing is acked unless a hook drained it. Call it only once SetSpool has succeeded
// (without a spool the channel still delivers, as before), and before Run. A spool that will not take a message still
// falls back to the channel unless NoChannel is set.
func (e *Endpoint) SpoolFromStart() { e.fromStart = true; e.hl.fromStart() }

// NoChannel marks the host as taking no channel delivery (host.Adapter.SpoolsFromStart): mail is held unacked in the
// inbox instead of delivered, when there is no spool or the spool will not take it, because the channel's ack would
// mean "seen", which this host never sees. Call it before SetSpool and Run.
func (e *Endpoint) NoChannel() { e.noChannel = true }

func (e *Endpoint) hooked() bool {
	return e.spool != nil && (e.fromStart || e.spool.HookedSince(e.since))
}

// hold leaves a batch unacked in the inbox, redelivered after spoolFullDelay: the delivery of a no-channel endpoint that
// has no spool, or whose spool will not take a message.
func hold(batch []pending) {
	for _, p := range batch {
		_ = p.msg.NakWithDelay(spoolFullDelay)
	}
}

// held is what the spool holds unacked, by (stream, sequence).
type held struct {
	msgs       map[spool.Ref]pending
	at         map[spool.Ref]time.Time // when each was spooled
	bytes      int
	stored     int                  // the held messages' stored size, headers included, as the inbox stream counts it
	alive      time.Time            // the last keep-alive
	wakeAt     time.Time            // when the session was last woken; zero once a hook has drained since
	wakes      int                  // wakes since a hook last drained: the backoff exponent
	hookAt     time.Time            // when a hook last drained: the idle spell is measured from here, and a crowded backlog stops waking at once
	hookHeld   int                  // the held count right after the last drain: a backlog above it has grown since
	hookStored int                  // the held stored size right after the last drain
	added      int                  // messages spooled since the last settle: arrivals of the current pass, not yet seen by a drain check
	listed     map[spool.Ref]bool   // messages a hook listed in a digest and the model has not read: announced, so they never wake (ADR 0046)
	rung       map[spool.Ref]bool   // urgent messages a wake has already announced
	fails      map[spool.Ref]int    // consecutive spool failures per message
	failed     bool                 // a spool failure is logged for the current spell
	full       bool                 // the byte cap turned a message away since a hook last drained
	epochs     map[spool.Ref]uint64 // the legacy epoch each held legacy message was spooled under
	seen       *seenSet             // the deliver loop's seen set: a dropped legacy message is forgotten there, so its redelivery is shown
}

// spoolTries is how many times a message is spooled before the channel delivers it instead.
const spoolTries = 3

// spoolBatch spools show for the next hook run. A message a hook already showed (a redelivery after a restart) is
// acked, not spooled again.
func (e *Endpoint) spoolBatch(deliver Deliverer, show []pending, h *held, seen *seenSet) {
	var fallback []pending // messages the spool would not take: the channel's, as before any hook
	for _, p := range show {
		seq := refOf(p.mail)
		switch {
		case e.spool.IsDrained(seq):
			e.ackDrained(p)
		case h.msgs[seq].msg != nil: // already held: the consumer redelivered it; keep the newest handle (codex H1 on #21)
			hp := h.msgs[seq]
			hp.msg = p.msg
			h.msgs[seq] = hp
			continue
		case h.bytes+len(p.mail.Body) > SpoolMaxBytes:
			h.full = true
			_ = p.msg.NakWithDelay(spoolFullDelay)
			continue
		default:
			wrote, ep, err := e.putEntry(p)
			if !wrote {
				// The lease is not held now: nothing is written, nothing is seen or acked. Nak'd as dropUnleased does,
				// so the new holder is redelivered it at once.
				_ = p.msg.NakWithDelay(time.Second) // a second, not at once: an unconfirmed lease would otherwise redeliver in a loop
				e.waking.done(seq)
				continue
			}
			if err != nil {
				e.NoteSpoolFault(err)
				if !h.failed && !e.faultOn {
					h.failed = true
					e.log.Printf("spool #%s: %v (retried, then delivered by the channel)", FormatMsgRef(seq), err)
				}
				if h.fails[seq]++; h.fails[seq] >= spoolTries {
					delete(h.fails, seq)
					fallback = append(fallback, p)
				} else {
					_ = p.msg.NakWithDelay(500 * time.Millisecond)
				}
				continue
			}
			h.failed = false
			if e.faultOn {
				e.ClearSpoolFault()
			}
			delete(h.fails, seq)
			h.msgs[seq] = p
			if ep != 0 {
				if h.epochs == nil {
					h.epochs = map[spool.Ref]uint64{}
				}
				h.epochs[seq] = ep
			}
			h.added++
			h.at[seq] = time.Now()
			h.bytes += len(p.mail.Body)
			h.stored += storedSize(p)
		}
		seen.mark(p)
	}
	if len(fallback) > 0 && (e.noChannel || (e.fromStart && !e.spool.HookedSince(e.since)) || e.hl.state(time.Time{}) != HooklessNone) {
		// The channel would ack what no hook has shown; before a hook, Claude drops channel events too. And when the
		// last HooklessWakes wakes started no turn, a wake is not landing, so a channel write is not proof of delivery
		// (issue #40): hold it, unacked, until a hook or the read tool shows it.
		hold(fallback)
	} else if len(fallback) > 0 {
		e.deliverBatch(deliver, fallback, seen)
	}
}

// putEntry writes p's entry to the spool. An own-stream entry is written as it always was. A legacy-stream entry is
// written in one critical section of the spool lock, after a check of the legacy lease made inside it, and stamped with
// the epoch of the hold (legacyEpochNow). A lease lost after the check but known by the end of the write has its epoch
// "none" recorded in the same critical section as the entry, so no hook read shows the entry. A lease lost later still
// has its "none" written under the lock right after, but a hook read that takes the lock first in between can show the
// entry once: a duplicate at worst, since the old holder cannot ack it and the next holder is redelivered it. Delivery
// is at least once: a duplicate is accepted, a lost message never. held is false when nothing was written because the
// legacy lease is not held.
func (e *Endpoint) putEntry(p pending) (held bool, epoch uint64, err error) {
	ent := EntryOf(p.mail)
	if p.mail.Stream != LegacyStream || !e.hasLegacyStream() {
		if !e.leaseHeldFor(p.mail.Stream) { // the own lease is gone (flag only): nothing is written
			return false, 0, nil
		}
		return true, 0, e.spool.Put(ent)
	}
	unlock, err := e.spool.Lock()
	if err != nil {
		return true, 0, err
	}
	defer unlock()
	return e.putLegacyLocked(ent)
}

// putLegacyLocked is the legacy-stream half of putEntry, the caller holding the spool lock: the lease check, then the
// stamped write, then a recheck of the hold: a loss known by then is recorded before the lock is released. held is false
// when nothing was written because the legacy lease is not held; it stays true when the entry was written and the lease
// was lost during the write (the entry is dropped and the message nak'd later, so the next holder is redelivered it).
func (e *Endpoint) putLegacyLocked(ent spool.Entry) (held bool, epoch uint64, err error) {
	if testLegacyCheckSeam != nil {
		testLegacyCheckSeam()
	}
	// The live lease read (lease_fence.go), not just the watch's state: a takeover the watch has not yet noticed is noticed
	// here, before anything is written. Not held, lost or unconfirmed alike, writes nothing.
	if e.verdictFor(LegacyStream) != verdictHeld {
		e.noteLegacyEpochLocked() // the loss is recorded here too, should its own writer not have yet
		return false, 0, nil
	}
	ent.Epoch = e.legacy.epoch.Load()
	if testLegacyPutSeam != nil {
		testLegacyPutSeam()
	}
	if err := e.spool.Put(ent); err != nil {
		return true, ent.Epoch, err
	}
	if !e.legacyReading() {
		e.noteLegacyEpochLocked() // lost during the write: record "none" in this critical section, not after it
	}
	return true, ent.Epoch, nil
}

// testSettleSeam is a test seam: called in settleHeld after the per-entry loop, before the drain is acted on. Nil in
// production.
var testSettleSeam func()

// settleHeld acks what a hook has drained, keeps the rest from being redelivered, and wakes an idle session.
func (e *Endpoint) settleHeld(deliver Deliverer, h *held) {
	defer func() { h.added = 0 }()
	e.dropHeld(h)
	if e.spool == nil {
		return
	}
	hl := e.hl.state(time.Time{}) // also ends a spell when a hook has run, even with nothing held
	if len(h.msgs) == 0 {
		return
	}
	ran := false
	for seq, p := range h.msgs {
		if !h.listed[seq] && e.spool.IsListed(seq) {
			// Listed in a digest: the model was told of it, so it neither rings nor wakes again and is not a waking
			// message for orchestrators, but it stays unacked until read (ADR 0046). The hook ran: the idle spell restarts.
			if h.listed == nil {
				h.listed = map[spool.Ref]bool{}
			}
			h.listed[seq] = true
			e.waking.listed(seq)
			h.wakeAt, h.wakes = time.Time{}, 0
		}
		if e.spool.IsDrained(seq) {
			ran = true
			e.ackDrained(p)
			h.bytes -= len(p.mail.Body)
			h.stored -= storedSize(p)
			delete(h.msgs, seq)
			delete(h.at, seq)
			delete(h.rung, seq)
			delete(h.listed, seq)
			delete(h.epochs, seq)
			h.full = false
			h.wakeAt, h.wakes = time.Time{}, 0 // a hook ran: the session is not idle, and the next idle spell may wake it again
			h.hookAt = time.Now()
		}
	}
	if testSettleSeam != nil {
		testSettleSeam()
	}
	if ran {
		// A drain shows a bounded batch: an urgent message it left held was not shown, so it may ring again
		// (codex H1 on #23). Judged live: an entry
		// the hook drained after the loop above read it was shown.
		for seq, p := range h.msgs {
			if p.mail.Urgent && !e.spool.IsDrained(seq) {
				delete(h.rung, seq)
			}
		}
		// The count before this pass's arrivals: mail that landed in the same pass as the drain counts as growth.
		h.hookHeld, h.hookStored = max(len(h.msgs)-h.added, 0), h.stored
	}
	e.healHeld(h)
	e.wakeIfIdle(deliver, h, hl)
	if time.Since(h.alive) >= SpoolKeepAlive {
		for _, p := range h.msgs {
			_ = p.msg.InProgress()
		}
		h.alive = time.Now()
	}
}

// wakeIfIdle sends the summary line of the held mail once some of it has waited the idle wake for a hook, and again while
// it is still held and no hook has drained: wakeBackoff after the previous wake, doubling each time. A drain resets
// it. Only mail that wakes (kind.Wakes) counts, and the summary covers all held mail. Urgent mail wakes at once, any
// kind, each urgent message once. A backlog near capacity (SpoolWakeCount held, SpoolWakeBytes held, or the byte cap
// having turned mail away) counts as waking and wakes at once, without the idle wait, so it is drained before waking
// mail behind it cannot be delivered; later wakes keep the backoff. Under SpoolFromStart, until the first hook has run
// in this serve, mail that wakes (kind.Wakes) rings at once like urgent mail, each message once, with no idle wait: a fresh session has run no hook to drain it, and
// waiting out the idle wake would delay its first mail. That ring does not start the backoff, so the idle wake still
// follows if it starts no turn (Claude Code may drop a channel event before its first prompt). Result and fyi mail still waits. Once a hook has run, the rules above apply unchanged.
func (e *Endpoint) wakeIfIdle(deliver Deliverer, h *held, hl Hookless) {
	if len(h.msgs) == 0 {
		return
	}
	var ring []spool.Ref
	// Listed mail was announced by the digest and waits for the model to read it: it never wakes (ADR 0046).
	if len(h.msgs) == len(h.listed) {
		return
	}
	// Before the first hook, waking mail rings at once (see above).
	prehook := e.fromStart && !e.spool.HookedSince(e.since)
	oldest := time.Time{} // the longest-held waking message
	crowded := len(h.msgs) >= SpoolWakeCount || h.stored >= SpoolWakeBytes || h.full
	for seq, p := range h.msgs {
		if h.listed[seq] {
			continue
		}
		if (p.mail.Urgent || (prehook && p.mail.Kind.Wakes())) && !h.rung[seq] && !e.spool.IsDrained(seq) {
			ring = append(ring, seq)
		}
		if t := h.at[seq]; (crowded || p.mail.Urgent || p.mail.Kind.Wakes()) && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	// The one notice of the proven state goes out for held mail of any kind: with no hook to drain it, result and fyi mail
	// would stay unread unless the model checks.
	notice := hl == HooklessProven && !e.hl.noticed()
	if oldest.IsZero() && !notice {
		return // only result and fyi mail: it waits for the next hook run
	}
	// due: the normal idle/backoff rule says wake now. urgent: an urgent message rings. A wake that is neither is a
	// pre-hook ring alone, and it must not start the backoff.
	var due bool
	if !h.wakeAt.IsZero() {
		due = !oldest.IsZero() && time.Since(h.wakeAt) >= wakeBackoff(e.idleWake(), h.wakes)
	} else {
		// A crowded backlog wakes at once: waiting could cost mail behind it (codex H2 on #75). Not while a hook is
		// draining it: a drain shows a bounded batch, the held count stays high for many runs, and the idle spell
		// restarts at each (ADR-0028: a busy session is never woken).
		since := oldest
		if h.hookAt.After(since) {
			since = h.hookAt
		}
		// The drain suppresses the wake only while it makes headroom: the backlog has not grown since it, the stream
		// is not full, and the held bytes and count are under hard lines (half the inbox limit, 3/2 of SpoolWakeCount)
		// past which a crowded backlog always wakes at once (ADR 0069).
		active := !h.hookAt.IsZero() && time.Since(h.hookAt) < e.idleWake() && !h.full &&
			len(h.msgs) <= h.hookHeld && h.stored <= h.hookStored && h.stored < SpoolWakeBytes*2 &&
			len(h.msgs) < SpoolWakeCount*spoolWakeHardNum/spoolWakeHardDen
		// The stream's own headroom is read only when everything else already holds (a crowded backlog just after a
		// drain), and an unreadable stream counts as full.
		if active && crowded && !e.streamHasHeadroom(context.Background()) {
			active = false
		}
		due = !oldest.IsZero() && ((crowded && !active) || time.Since(since) >= e.idleWake())
	}
	urgent := false
	for _, seq := range ring {
		if h.msgs[seq].mail.Urgent {
			urgent = true
			break
		}
	}
	// No hook is running in this session (hookless.go): re-waking cannot get the mail shown, so nothing repeats. A new
	// waking message still rings once (above), and the entry into the proven state sends one wake that says how to
	// read the mail.
	if hl != HooklessNone {
		due = false
	}
	if len(ring) == 0 && !due && !notice {
		return
	}
	seqs := make([]spool.Ref, 0, len(h.msgs))
	for seq := range h.msgs {
		if !h.listed[seq] { // the summary does not announce listed mail again
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i].Less(seqs[j]) })
	mails := make([]render.Mail, len(seqs))
	ids := make([]string, len(seqs))
	for i, seq := range seqs {
		mails[i] = h.msgs[seq].mail
		ids[i] = FormatMsgRef(seq)
	}
	line, _, _ := strings.Cut(render.Batch(e.cfg.Name, e.cfg.Owner, mails, e.cfg.BatchMsgs, e.cfg.BatchBytes), "\n")
	if hl == HooklessProven {
		line += " " + HooklessReadHint
	}
	meta := map[string]string{"to": e.cfg.Name, "count": strconv.Itoa(len(mails)), "seqs": strings.Join(ids, ","), "wake": "true"}
	if err := deliver(line, meta); err != nil {
		e.log.Printf("wake failed, retried next check: %v", err)
		return
	}
	e.hl.woke(hl == HooklessProven)
	if due || urgent {
		h.wakeAt = time.Now()
		h.wakes++
	}
	for _, seq := range ring {
		h.rung[seq] = true
	}
}

// headroomTTL is how long a stream-info reading stands; headroomTimeout bounds one read.
const (
	headroomTTL     = 5 * time.Second
	headroomTimeout = 2 * time.Second
)

// streamBytes reads the inbox stream's stored bytes and its byte limit. A package var so tests can fake it.
var streamBytes = func(ctx context.Context, e *Endpoint) (bytes, max uint64, err error) {
	if e.js == nil {
		return 0, 0, errors.New("no stream handle")
	}
	st, err := e.js.Stream(ctx, sap.InboxStream(e.key))
	if err != nil {
		return 0, 0, err
	}
	info, err := st.Info(ctx)
	if err != nil {
		return 0, 0, err
	}
	max = uint64(store.InboxMaxBytes)
	if info.Config.MaxBytes > 0 {
		max = uint64(info.Config.MaxBytes)
	}
	return info.State.Bytes, max, nil
}

// streamHasHeadroom reports whether the inbox stream holds under half its byte limit, retained history included
// (ADR 0069). The reading is cached for headroomTTL; any error or timeout counts as no headroom.
func (e *Endpoint) streamHasHeadroom(ctx context.Context) bool {
	e.headMu.Lock()
	defer e.headMu.Unlock()
	if !e.headAt.IsZero() && time.Since(e.headAt) < headroomTTL {
		return e.headOK
	}
	ctx, cancel := context.WithTimeout(ctx, headroomTimeout)
	defer cancel()
	b, m, err := streamBytes(ctx, e)
	e.headOK = err == nil && m > 0 && b < m/2
	e.headAt = time.Now()
	return e.headOK
}

// ackDrained acks a message a hook has shown. Under the before-ack failpoint it does not: see failpointBeforeAck.
func (e *Endpoint) ackDrained(p pending) {
	if e.failBeforeAck != nil {
		e.failBeforeAck(p.mail.Seq)
	}
	e.ack(p)
}

// failpointBeforeAck is a test-only hook: when MASH_SERVE_FAILPOINT is "before-ack", serve says so on stderr as it
// is about to ack a message a hook has drained, and blocks there for good, so a test can SIGKILL it with the message
// shown but unacked. It is read once, when the endpoint opens; nil (the production path) costs one nil check. Never
// set outside the crash tests.
func failpointBeforeAck(env string) func(seq uint64) {
	if env != "before-ack" {
		return nil
	}
	return func(seq uint64) {
		fmt.Fprintf(os.Stderr, "mash serve: failpoint before-ack #%d\n", seq)
		select {}
	}
}

// ackTimeout bounds the wait for the server to confirm an ack.
const ackTimeout = 2 * time.Second

// ack acks a shown message and forgets it in the spool, but only once the server has confirmed the ack: a lost
// fire-and-forget ack would leave the message unacked with no tombstone, to be shown again.
func (e *Endpoint) ack(p pending) {
	defer e.waking.done(refOf(p.mail)) // shown, whether or not the server confirms the ack
	if !e.mayAck(p) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ackTimeout)
	defer cancel()
	if err := p.msg.DoubleAck(ctx); err != nil {
		e.log.Printf("ack #%s: %v", p.mail.Num(), err)
		return // the tombstone stays: a redelivery is acked then
	}
	if err := e.spool.Acked(refOf(p.mail)); err != nil {
		e.log.Printf("%v", err)
	}
}

// healHeld rewrites the spool file of a held message that is missing or unreadable (damaged on disk) from memory. A
// hook holds the spool lock from its read to its drained marks, so under the lock a message is either drained
// (its tombstone is there: left alone) or still waiting; the file is never re-created for one a hook just showed.
func (e *Endpoint) healHeld(h *held) {
	var bad []spool.Ref
	for seq := range h.msgs {
		if !e.spool.Has(seq) && !e.spool.IsDrained(seq) {
			bad = append(bad, seq)
		}
	}
	if len(bad) == 0 {
		return
	}
	unlock, err := e.spool.Lock()
	if err != nil {
		e.log.Printf("spool: %v", err)
		return
	}
	defer unlock()
	for _, seq := range bad {
		if e.spool.IsDrained(seq) || e.spool.Has(seq) {
			continue
		}
		ent := EntryOf(h.msgs[seq].mail)
		var err error
		if ent.Stream == LegacyStream && e.hasLegacyStream() {
			// Through the lease-checked, epoch-stamped path of putEntry: a lost lease writes nothing.
			_, _, err = e.putLegacyLocked(ent)
		} else {
			err = e.spool.Put(ent)
		}
		if err != nil {
			e.log.Printf("spool #%s: rewrite: %v", FormatMsgRef(seq), err)
		}
	}
}

// EntryOf is a message as the spool keeps it.
func EntryOf(m render.Mail) spool.Entry {
	return spool.Entry{Seq: m.Seq, Stream: m.Stream, From: m.From, Trust: string(m.Trust), Instance: m.Instance, Owner: m.Owner, At: m.At, Body: m.Body,
		Urgent: m.Urgent, Kind: string(m.Kind.Norm()), ReplyTo: m.ReplyTo, Via: m.Via}
}

// MailOf is a spooled message as render shows it.
func MailOf(e spool.Entry) render.Mail {
	return render.Mail{Seq: e.Seq, Stream: e.Stream, From: e.From, Trust: sap.Trust(e.Trust), Instance: e.Instance, Owner: e.Owner, At: e.At, Body: e.Body,
		Urgent: e.Urgent, Kind: kind.Of(e.Kind), ReplyTo: e.ReplyTo, Via: e.Via, Redelivered: e.Redelivered}
}

// wakeMax caps the re-wake backoff.
const wakeMax = 30 * time.Minute

// idleWake is how long held mail waits for a hook before the first wake: cfg.IdleWake, else the IdleWake default.
func (e *Endpoint) idleWake() time.Duration {
	if e.cfg.IdleWake > 0 {
		return e.cfg.IdleWake
	}
	return IdleWake
}

// wakeBackoff is how long after a wake the session is woken again if no hook has drained: base (the idle wake)
// doubled for each wake since, up to wakeMax.
func wakeBackoff(base time.Duration, wakes int) time.Duration {
	d := base
	for i := 0; i < wakes && d < wakeMax; i++ {
		d *= 2
	}
	return min(d, wakeMax)
}

// storedSize is about what the inbox stream counts for a message: its subject, headers and body. The byte backlog wake
// uses it, not the body alone: a sender's large headers count against the stream's limit too (codex H1 on #75).
func storedSize(p pending) int {
	n := len(p.mail.Body)
	if p.msg != nil {
		n += len(p.msg.Subject())
		for k, vs := range p.msg.Headers() {
			for _, v := range vs {
				n += len(k) + len(v) + 4 // "k: v\r\n"
			}
		}
	}
	return n
}
