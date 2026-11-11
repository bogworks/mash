package endpoint

import (
	"sync"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// Sessions whose hooks never run (ADR 0041). A session spooled from the start holds mail until a hook shows it, and a
// hook is the only thing that drains. If mash's hooks never run there (disableAllHooks, allowManagedHooksOnly, the
// plugin's hooks not loaded, every hook refused as another session's), re-waking cannot help: each wake costs a model
// turn that shows a summary line and no mail. Two signals, both only while no main-session hook has run since this
// serve bound the spool:
//
//   - suspected: HooklessWakes wakes have been sent with no hook run. The re-wake backoff stops; a new waking message
//     still rings once. Nothing is acked.
//   - proven: two mash tool calls, ended at least HookGrace apart, after the bind, and still no hook. A PostToolUse
//     hook runs after every successful tool call, so the first call's hook would have run by the second. As suspected,
//     plus the wake line says to read each message with the read tool, and that tool then marks the message drained
//     (MarkRead), so serve acks it: the model has seen the body.
//
// A hook of this session's own subagent (the spool's .alive) or of the main session (.hooked) ends both states. A hook
// refused as another session's writes neither, so for delivery that session is hookless.
var (
	// HookGrace is how far apart two tool calls must end before no hook run between them means the hooks do not run:
	// above the plugin's 10 s hook timeout. A knob for tests.
	HookGrace = 12 * time.Second
	// HooklessWakes is how many wakes with no hook run make a session suspected hookless. A knob for tests.
	HooklessWakes = 3
)

// HooklessReadHint ends the wake line of a session proven hookless.
const HooklessReadHint = "mash's hooks are not running in this session; read each message with the read tool (#seq)."

// Hookless is how sure serve is that no mash hook runs in this session.
type Hookless int

const (
	HooklessNone      Hookless = iota // a hook has run, or too little is known
	HooklessSuspected                 // HooklessWakes wakes and no hook run
	HooklessProven                    // two tool calls HookGrace apart and no hook run
)

// hookless is the detector's state, shared by Run's goroutine (settleHeld), the tool calls (ToolCalled, MarkRead) and
// the status tool. All of it is guarded by mu.
type hookless struct {
	mu        sync.Mutex
	sp        spool.Spool
	since     time.Time
	bound     bool // SetSpool has run
	armed     bool // SpoolFromStart has run
	first     time.Time
	last      time.Time
	wakes     int
	announced bool // the proven notice has gone out
}

func (h *hookless) bind(sp spool.Spool, since time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sp, h.since, h.bound = sp, since, true
}

func (h *hookless) fromStart() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.armed = true
}

// state is the current verdict. extra, when not zero, is a tool call that is ending now and not yet recorded.
// A hook run since the bind ends the spell: its counts start again.
func (h *hookless) state(extra time.Time) Hookless {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.bound || !h.armed {
		return HooklessNone
	}
	if h.sp.HookedSince(h.since) || h.sp.AliveSince(h.since) {
		h.first, h.last, h.wakes, h.announced = time.Time{}, time.Time{}, 0, false
		return HooklessNone
	}
	first, last := h.first, h.last
	if !extra.IsZero() && !extra.Before(h.since) {
		if first.IsZero() {
			first = extra
		}
		if extra.After(last) {
			last = extra
		}
	}
	switch {
	case !first.IsZero() && last.Sub(first) >= HookGrace:
		return HooklessProven
	case h.wakes >= HooklessWakes:
		return HooklessSuspected
	}
	return HooklessNone
}

func (h *hookless) noticed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.announced
}

// woke counts a wake sent. proven: it was the proven state's, so the notice has gone out.
func (h *hookless) woke(proven bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.wakes++
	if proven {
		h.announced = true
	}
}

// ToolCalled records that one of this session's mash tool calls ended at at. Call it when the call returns: a hook
// runs after the call, so only a call that has ended can show that its hook did not run. Calls before the spool was
// bound are not the evidence of this serve.
func (e *Endpoint) ToolCalled(at time.Time) {
	h := &e.hl
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.bound || !h.armed || at.Before(h.since) {
		return
	}
	if h.first.IsZero() {
		h.first = at
	}
	if at.After(h.last) {
		h.last = at
	}
}

// Hookless reports whether no hook runs in this session, counting the call being made now as one that ends now, and
// how many wakes have been sent with no hook run. Used by the status tool.
func (e *Endpoint) Hookless() (Hookless, int) {
	st := e.hl.state(time.Now())
	e.hl.mu.Lock()
	defer e.hl.mu.Unlock()
	return st, e.hl.wakes
}

// MarkRead marks a held message drained, so serve acks it, once the read tool has shown the model its body, in two
// cases. (1) The message was listed in a hook's digest and not yet read (ADR 0046): in every mode, since a digest
// never drains what it lists, the read is what makes it seen. (2) The session is proven hookless and the message is
// waiting in the spool: no hook will show it. In every other state it does nothing, and the message stays held until
// a hook shows it. It reports whether it drained. Call it only after a successful Read; the inbox tool never calls it.
func (e *Endpoint) MarkRead(seq MsgRef) bool {
	e.hl.mu.Lock()
	sp, bound := e.hl.sp, e.hl.bound
	e.hl.mu.Unlock()
	if !bound {
		return false
	}
	// A cheap look before the lock: most reads are of a message that is neither listed nor held.
	listed := sp.IsListed(seq)
	if !listed && e.hl.state(time.Now()) != HooklessProven {
		return false
	}
	// Under the spool lock, as a hook's drain is: the message is either waiting or already drained.
	unlock, err := sp.Lock()
	if err != nil {
		e.log.Printf("read #%s: %v", FormatMsgRef(seq), err)
		return false
	}
	defer unlock()
	if !sp.Has(seq) || sp.IsDrained(seq) {
		return false
	}
	if err := sp.Drained(seq); err != nil {
		e.log.Printf("read #%s: marking it drained: %v", FormatMsgRef(seq), err)
		return false
	}
	return true
}
