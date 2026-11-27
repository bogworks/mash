package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

func recordTo(t *testing.T, sp spool.Spool, rec HookRecord) {
	t.Helper()
	b, _ := json.Marshal(rec)
	if err := sp.NoteHookState(b); err != nil {
		t.Fatal(err)
	}
}

// readOnly makes dir refuse new files for the rest of the test, and says false when the test runs where that is not
// possible (as root, whom a mode does not stop).
func readOnly(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	return !(spool.Spool{Dir: dir}).Writable()
}

// A shell reads the session from the record only while the serve that wrote it exists, on this machine; a hook run and a
// refusal are read from the spool itself, and the storage by trying the folder.
func TestSessionOnDisk(t *testing.T) {
	sp := spool.Spool{Dir: t.TempDir()}
	host, _ := os.Hostname()
	since := time.Now().Add(-time.Minute)
	if _, _, ok := SessionOnDisk(sp); ok {
		t.Fatal("a session with no record")
	}
	self := HookRecord{Hooks: HookSuspected, Since: since, Host: host, PID: strconv.Itoa(os.Getpid())}
	self.Start, _ = procStart(os.Getpid())
	recordTo(t, sp, self)
	if h, st, ok := SessionOnDisk(sp); !ok || h != HookSuspected || st != StorageOK {
		t.Errorf("suspected: %q %q %v", h, st, ok)
	}
	self.Hooks = ""
	recordTo(t, sp, self)
	if h, _, ok := SessionOnDisk(sp); !ok || h != HookNotRun {
		t.Errorf("not run: %q %v", h, ok)
	}
	self.Hooks = HookDegraded
	recordTo(t, sp, self)
	if h, _, _ := SessionOnDisk(sp); h != HookDegraded {
		t.Errorf("degraded: %q", h)
	}
	if err := sp.NoteRefused(spool.Refusal{Time: time.Now(), Why: "x"}); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := SessionOnDisk(sp); h != HookDegraded {
		t.Errorf("degraded outranks a refusal: %q", h)
	}
	self.Hooks = ""
	recordTo(t, sp, self)
	if h, _, _ := SessionOnDisk(sp); h != HookRefused {
		t.Errorf("refused: %q", h)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := SessionOnDisk(sp); h != HookRan {
		t.Errorf("ran: %q", h)
	}
	other := self
	other.Host = host + "-elsewhere"
	recordTo(t, sp, other)
	if _, _, ok := SessionOnDisk(sp); ok {
		t.Error("a record of another machine")
	}
	gone := self
	gone.PID = "2147483646" // no such process
	gone.Start = ""
	recordTo(t, sp, gone)
	if _, _, ok := SessionOnDisk(sp); ok {
		t.Error("a record of a serve that is gone")
	}
	// Own starts the next holder clean
	if err := sp.Own(`{"pid":1}`); err != nil {
		t.Fatal(err)
	}
	if _, found := sp.HookState(); found {
		t.Error("the record outlived Own")
	}
}

// The storage is the reader's own try at the folder; the record says nothing about it.
func TestSessionOnDiskStorageIsTriedByTheReader(t *testing.T) {
	dir := t.TempDir()
	sp := spool.Spool{Dir: dir}
	host, _ := os.Hostname()
	self := HookRecord{Since: time.Now().Add(-time.Minute), Host: host, PID: strconv.Itoa(os.Getpid())}
	self.Start, _ = procStart(os.Getpid())
	recordTo(t, sp, self)
	if b, _ := sp.HookState(); strings.Contains(string(b), "storage") {
		t.Errorf("the record holds a storage field: %s", b)
	}
	if !readOnly(t, dir) {
		t.Skip("a read-only folder is still writable here")
	}
	if _, st, ok := SessionOnDisk(sp); !ok || st != StorageFailing {
		t.Errorf("read-only folder: %q %v", st, ok)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the try left files behind: %v", entries)
	}
}

func boundEndpoint(t *testing.T, since time.Time) (*Endpoint, spool.Spool) {
	t.Helper()
	sp := spool.Spool{Dir: t.TempDir()}
	e := &Endpoint{}
	e.hl.bind(sp, since)
	e.hl.fromStart()
	return e, sp
}

// The periodic recorder reads the detector as it stands: one real tool call followed by HookGrace is not two calls.
func TestRecordHookStateSuppliesNoSyntheticCall(t *testing.T) {
	e, sp := boundEndpoint(t, time.Now().Add(-time.Minute))
	e.ToolCalled(time.Now().Add(-HookGrace - time.Second))
	e.RecordHookState()
	b, found := sp.HookState()
	if !found {
		t.Fatal("nothing recorded")
	}
	var rec HookRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.Hooks != "" {
		t.Errorf("one real call recorded as %q (%v)", rec.Hooks, err)
	}
	// two real calls HookGrace apart are proof
	e.ToolCalled(time.Now())
	e.RecordHookState()
	b, _ = sp.HookState()
	_ = json.Unmarshal(b, &rec)
	if rec.Hooks != HookDegraded {
		t.Errorf("two real calls recorded as %q", rec.Hooks)
	}
}

// A serve that stops holding the endpoint takes its own record away, and only its own.
func TestClearHookStateRemovesOnlyItsOwn(t *testing.T) {
	e, sp := boundEndpoint(t, time.Now().Add(-time.Minute))
	e.RecordHookState()
	if _, found := sp.HookState(); !found {
		t.Fatal("nothing recorded")
	}
	e.clearHookState()
	if _, found := sp.HookState(); found {
		t.Error("the record outlived the serve's hold")
	}
	// a newer serve's record is left alone
	newer := HookRecord{Since: time.Now(), PID: "1", Host: "h"}
	recordTo(t, sp, newer)
	e.clearHookState()
	if _, found := sp.HookState(); !found {
		t.Error("another serve's record was removed")
	}
}

// A record call that comes after serve's shutdown clear, on the cancelled context of the Run, does not write the record
// back: the lock can be taken on a cancelled context when it is free (#1872).
func TestRecordHookStateAfterClearWritesNothing(t *testing.T) {
	e, sp := boundEndpoint(t, time.Now().Add(-time.Minute))
	e.RecordHookState()
	if _, found := sp.HookState(); !found {
		t.Fatal("nothing recorded")
	}
	e.clearHookState()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.recordHookState(ctx)
	if b, found := sp.HookState(); found {
		t.Errorf("the record was written back after the clear: %s", b)
	}
}

// During a handoff the serve that lost the lease must not overwrite the record of the one that took over, forgets what it
// wrote, and rewrites when it holds the lease again.
func TestRecordHookStateOnlyWhileHoldingTheLease(t *testing.T) {
	a, sp := boundEndpoint(t, time.Now().Add(-time.Minute))
	a.RecordHookState()
	if _, found := sp.HookState(); !found {
		t.Fatal("A recorded nothing")
	}
	// B (another live process: the parent) takes over and records.
	ppid := os.Getppid()
	host, _ := os.Hostname()
	b := HookRecord{Hooks: HookSuspected, Since: time.Now(), Host: host, PID: strconv.Itoa(ppid)}
	b.Start, _ = procStart(ppid)
	recordTo(t, sp, b)
	want, _ := sp.HookState()
	// A's lease is lost: it must not write, and drops its cache.
	a.leaseGone.Store(true)
	a.RecordHookState()
	if got, _ := sp.HookState(); string(got) != string(want) {
		t.Errorf("A overwrote B's record:\n%s\n%s", got, want)
	}
	if a.hookRecorded.Load() != nil {
		t.Error("A kept its cache after losing the lease")
	}
	// A regains the lease: it rewrites, though its detector state is unchanged.
	a.leaseGone.Store(false)
	a.RecordHookState()
	got, _ := sp.HookState()
	var rec HookRecord
	if err := json.Unmarshal(got, &rec); err != nil || rec.PID != strconv.Itoa(os.Getpid()) {
		t.Errorf("A did not rewrite after regaining: %s", got)
	}
	// A foreign record that appears while A holds a cache is not hidden by it.
	recordTo(t, sp, b)
	a.RecordHookState()
	got, _ = sp.HookState()
	if err := json.Unmarshal(got, &rec); err != nil || rec.PID != strconv.Itoa(os.Getpid()) {
		t.Errorf("a stale cache suppressed the rewrite: %s", got)
	}
}

// The state shown (HookState) and the state recorded come from the detector read the same way.
func TestHookStateAgreesWithTheRecord(t *testing.T) {
	e, sp := boundEndpoint(t, time.Now().Add(-time.Minute))
	e.ToolCalled(time.Now().Add(-HookGrace - time.Second))
	if h := e.HookState(); h != HookNotRun {
		t.Errorf("one real call shown as %q", h)
	}
	e.RecordHookState()
	if h, _, ok := SessionOnDisk(sp); !ok || h != e.HookState() {
		t.Errorf("recorded %q, shown %q (%v)", h, e.HookState(), ok)
	}
}

// A spool serve could not bind leaves no folder to try, yet storage is failing: mail cannot be stored. A later bind ends it.
func TestStorageFailsWhileSpoolUnbound(t *testing.T) {
	e := &Endpoint{}
	if got := e.StorageState(); got != StorageOK {
		t.Fatalf("before any try: %s", got)
	}
	e.NoteSpoolUnbound(errors.New("cannot create the state folder"))
	if got := e.StorageState(); got != StorageFailing {
		t.Fatalf("after NoteSpoolUnbound: %s, want %s", got, StorageFailing)
	}
	e.hl.bind(spool.Spool{Dir: t.TempDir()}, time.Now())
	if got := e.StorageState(); got != StorageOK {
		t.Fatalf("after a bind: %s, want %s", got, StorageOK)
	}
}
