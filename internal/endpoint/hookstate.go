package endpoint

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// The hook states of a session (mash status --detail, mash doctor): whether the agent host's hooks have delivered mail in
// it. They have the meanings 0.5.5's delivery line had.
const (
	HookRan       = "ran"       // a hook has run in this session
	HookNotRun    = "not_run"   // none has yet: normal until the first prompt or tool call
	HookSuspected = "suspected" // wakes were sent and no hook ran (Hookless suspected)
	HookDegraded  = "degraded"  // tool calls ended well apart and no hook ran (Hookless proven)
	HookRefused   = "refused"   // a hook ran, but was refused as another session's
)

// The storage words of a session's record.
const (
	StorageOK      = "ok"
	StorageFailing = "failing"
)

// HookRecord is what serve records in the spool (spool.NoteHookState) when its detectors change, so that mash status at
// the session's shell reads what the tool says. It holds no mail and no secret: the detector's verdict, when the serve
// bound the spool, which serve wrote it, and whether the mail folder worked. At is when it was written.
type HookRecord struct {
	Hooks   string    `json:"hooks"` // "", HookSuspected or HookDegraded: what only serve knows
	Storage string    `json:"storage"`
	Since   time.Time `json:"since"`
	Host    string    `json:"host"`
	PID     string    `json:"pid"`
	Start   string    `json:"start,omitempty"`
	At      time.Time `json:"at"`
}

// hookStateOf is the hook state from the spool's own files and the detector's verdict. A hook run since the bind (the
// main session's or its subagent's) wins; then the detector's proven verdict, a refusal since the bind, and the
// suspected verdict, in the order 0.5.5's delivery line used.
func hookStateOf(sp spool.Spool, since time.Time, det Hookless) string {
	if at, ok := sp.LastHook(); (ok && !at.Before(since)) || sp.AliveSince(since) {
		return HookRan
	}
	if det == HooklessProven {
		return HookDegraded
	}
	if at, _, ok := sp.Refused(); ok && at.After(since) {
		return HookRefused
	}
	if det == HooklessSuspected {
		return HookSuspected
	}
	return HookNotRun
}

// HookState is this session's hook state now: HookRan, HookNotRun, HookSuspected, HookDegraded or HookRefused. Before the
// spool is bound no hook can have run.
func (e *Endpoint) HookState() string {
	e.hl.mu.Lock()
	sp, since, bound := e.hl.sp, e.hl.since, e.hl.bound
	e.hl.mu.Unlock()
	if !bound {
		return HookNotRun
	}
	return hookStateOf(sp, since, e.hl.state(time.Now()))
}

// RecordHookState writes the detectors' state to the spool when it differs from what was last written (the mail
// folder's state with it). It does nothing before the spool is bound. A folder that cannot be written cannot take the
// record either, so a failed write is not an error.
func (e *Endpoint) RecordHookState() {
	e.hl.mu.Lock()
	sp, since, bound := e.hl.sp, e.hl.since, e.hl.bound
	e.hl.mu.Unlock()
	if !bound {
		return
	}
	rec := HookRecord{Storage: StorageOK, Since: since, PID: strconv.Itoa(os.Getpid())}
	rec.Host, _ = os.Hostname()
	rec.Start, _ = procStart(os.Getpid())
	switch e.hl.state(time.Now()) {
	case HooklessProven:
		rec.Hooks = HookDegraded
	case HooklessSuspected:
		rec.Hooks = HookSuspected
	}
	if _, failing := e.SpoolFaultLine(); failing {
		rec.Storage = StorageFailing
	}
	kb, _ := json.Marshal(rec) // without At: the key of what was last written
	if last := e.hookRecorded.Load(); last != nil && *last == string(kb) {
		return
	}
	rec.At = time.Now().UTC()
	b, err := json.Marshal(rec)
	if err == nil && sp.NoteHookState(b) == nil {
		key := string(kb)
		e.hookRecorded.Store(&key)
	}
}

// recordEvery is how often serve looks for a change in what it records (a hook run is read from the spool itself).
var recordEvery = 2 * time.Second

func (e *Endpoint) recordHookStates(ctx context.Context) {
	t := time.NewTicker(recordEvery)
	defer t.Stop()
	for {
		e.RecordHookState()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SessionOnDisk is what mash status at a shell says about the session on this machine whose serve recorded its state in
// the spool: the hook state and the storage word. ok is false when no live serve of this machine recorded one (no
// session). The serve's process must still exist: a killed serve leaves its file behind.
func SessionOnDisk(sp spool.Spool) (hooks, storage string, ok bool) {
	b, found := sp.HookState()
	if !found {
		return "", "", false
	}
	var rec HookRecord
	if json.Unmarshal(b, &rec) != nil || rec.PID == "" {
		return "", "", false
	}
	if host, _ := os.Hostname(); rec.Host != host || holderGone(rec.PID, rec.Start) {
		return "", "", false
	}
	det := HooklessNone
	switch rec.Hooks {
	case HookDegraded:
		det = HooklessProven
	case HookSuspected:
		det = HooklessSuspected
	}
	st := rec.Storage
	if st != StorageFailing {
		st = StorageOK
	}
	return hookStateOf(sp, rec.Since, det), st, true
}
