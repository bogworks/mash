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

// The storage words of a session: decided by whoever asks, by trying the folder (spool.Spool.Writable); nothing about
// storage is recorded.
const (
	StorageOK      = "ok"
	StorageFailing = "failing"
)

// HookRecord is what serve records in the spool (spool.NoteHookState) when its detectors change, so that mash status at
// the session's shell reads what the tool says. It holds no mail and no secret: the detector's verdict, when the serve
// bound the spool, and which serve wrote it. It exists only while that serve holds the endpoint: serve removes it when it
// stops holding it. At is when it was written.
type HookRecord struct {
	Hooks string `json:"hooks"` // "", HookSuspected or HookDegraded: what only serve knows
	// Scanning is true while serve checks the stored mail before it delivers any (malformed.go): status at a shell says not ready.
	Scanning bool      `json:"scanning,omitempty"`
	Since    time.Time `json:"since"`
	Host     string    `json:"host"`
	PID      string    `json:"pid"`
	Start    string    `json:"start,omitempty"`
	At       time.Time `json:"at"`
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
	return hookStateOf(sp, since, e.hl.state(time.Time{}))
}

// StorageState is StorageOK or StorageFailing for the folder this session spools to: failing when serve reports the
// folder it requires as failing, or when a try at writing to it fails now. Before the spool is bound there is no folder.
func (e *Endpoint) StorageState() string {
	e.hl.mu.Lock()
	sp, bound := e.hl.sp, e.hl.bound
	e.hl.mu.Unlock()
	if _, failing := e.SpoolFaultLine(); failing || (bound && !sp.Writable()) {
		return StorageFailing
	}
	return StorageOK
}

// RecordHookState writes the detectors' state to the spool when it differs from what was last written (the mail
// folder's state with it). It does nothing before the spool is bound. A folder that cannot be written cannot take the
// record either, so a failed write is not an error.
func (e *Endpoint) RecordHookState() { e.recordHookState(e.serveCtx()) }

func (e *Endpoint) recordHookState(ctx context.Context) {
	e.hl.mu.Lock()
	sp, since, bound := e.hl.sp, e.hl.since, e.hl.bound
	e.hl.mu.Unlock()
	if !bound {
		return
	}
	// Only the serve that holds the endpoint writes, and under the spool's lock, so a serve losing the lease cannot
	// overwrite the record of the one that took it over. A serve that does not hold it forgets what it wrote: if it
	// regains the lease, the record is rewritten.
	if !e.holdsLease() {
		e.hookRecorded.Store(nil)
		return
	}
	unlock, err := sp.LockContext(ctx)
	if err != nil {
		return
	}
	defer unlock()
	if !e.holdsLease() { // the lease may have been lost while waiting for the lock
		e.hookRecorded.Store(nil)
		return
	}
	// What is on disk must be this serve's own record, or the cache is stale (another serve's record, or none: removed
	// by a clear).
	own := false
	if b, found := sp.HookState(); found {
		var cur HookRecord
		own = json.Unmarshal(b, &cur) == nil && cur.PID == strconv.Itoa(os.Getpid())
	}
	if !own {
		e.hookRecorded.Store(nil)
	}
	rec := HookRecord{Since: since, PID: strconv.Itoa(os.Getpid()), Scanning: e.scanning.Load()}
	rec.Host, _ = os.Hostname()
	rec.Start, _ = procStart(os.Getpid())
	// The detector's state as it stands: no call is ending now, so none is supplied (a synthetic call time would make one
	// real call followed by HookGrace look like a second call that never happened).
	switch e.hl.state(time.Time{}) {
	case HooklessProven:
		rec.Hooks = HookDegraded
	case HooklessSuspected:
		rec.Hooks = HookSuspected
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

// holdsLease is true while this serve holds the endpoint: always without a lease (tests, no-lease mode), otherwise until
// the lease is reported lost (a flag, no round trip).
func (e *Endpoint) holdsLease() bool { return !e.leaseGone.Load() }

// cleanupLockWait is the longest serve's shutdown cleanup waits for the spool lock.
var cleanupLockWait = time.Second

// serveCtx is the context of the Run in progress (Background before one): the spool lock waits that happen while serve
// runs follow it.
func (e *Endpoint) serveCtx() context.Context {
	if p := e.runCtx.Load(); p != nil {
		return *p
	}
	return context.Background()
}

// lockCtx is the context a held-mail spool lock wait follows.
func (h *held) lockCtx() context.Context {
	if h.ctx == nil {
		return context.Background()
	}
	return h.ctx
}

// recordEvery is how often serve looks for a change in what it records (a hook run is read from the spool itself).
var recordEvery = 2 * time.Second

func (e *Endpoint) recordHookStates(ctx context.Context) {
	t := time.NewTicker(recordEvery)
	defer t.Stop()
	defer e.clearHookState() // serve stops here when it no longer holds the endpoint (lease lost) or is shutting down
	for {
		e.recordHookState(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// clearHookState removes the record when it is this serve's own: a newer serve of the session may already have written its
// own, which stays. The spool's lock is held across the check and the removal.
func (e *Endpoint) clearHookState() {
	e.hl.mu.Lock()
	sp, bound := e.hl.sp, e.hl.bound
	e.hl.mu.Unlock()
	if !bound {
		return
	}
	// Runs once serve is stopping, so the wait is bounded and not the run's: a hook stalled holding the lock must not keep
	// serve from exiting. A wait that times out leaves the record as a crash would, and a reader already tolerates it
	// (SessionOnDisk checks that the recorded pid still exists).
	ctx, cancel := context.WithTimeout(context.Background(), cleanupLockWait)
	defer cancel()
	unlock, err := sp.LockContext(ctx)
	if err != nil {
		return
	}
	defer unlock()
	if b, found := sp.HookState(); found {
		var rec HookRecord
		if json.Unmarshal(b, &rec) == nil && rec.PID != strconv.Itoa(os.Getpid()) {
			return
		}
		_ = sp.NoteHookState(nil)
	}
	e.hookRecorded.Store(nil)
}

// SessionOnDisk is what mash status at a shell says about the session on this machine whose serve recorded its state in
// the spool: the hook state and the storage word, which is decided here, by trying the folder. ok is false when no live
// serve of this machine recorded one (no session, or one that no longer holds the endpoint). The serve's process must
// still exist: a killed serve leaves its file behind.
func SessionOnDisk(sp spool.Spool) (hooks, storage string, ok bool) {
	rec, ok := liveRecord(sp)
	if !ok {
		return "", "", false
	}
	det := HooklessNone
	switch rec.Hooks {
	case HookDegraded:
		det = HooklessProven
	case HookSuspected:
		det = HooklessSuspected
	}
	st := StorageOK
	if !sp.Writable() {
		st = StorageFailing
	}
	return hookStateOf(sp, rec.Since, det), st, true
}

// ScanOnDisk is true at a shell when the live serve of this machine's session says it is checking the stored mail
// (StoredScanRunning), from what it recorded in the spool.
func ScanOnDisk(sp spool.Spool) bool {
	rec, ok := liveRecord(sp)
	return ok && rec.Scanning
}

// liveRecord is the record of a serve of this machine that still exists.
func liveRecord(sp spool.Spool) (HookRecord, bool) {
	b, found := sp.HookState()
	if !found {
		return HookRecord{}, false
	}
	var rec HookRecord
	if json.Unmarshal(b, &rec) != nil || rec.PID == "" {
		return HookRecord{}, false
	}
	if host, _ := os.Hostname(); rec.Host != host || holderGone(rec.PID, rec.Start) {
		return HookRecord{}, false
	}
	return rec, true
}
