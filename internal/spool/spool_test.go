package spool

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newSpool(t *testing.T) Spool {
	t.Helper()
	s := Spool{Dir: filepath.Join(t.TempDir(), "mash", "spool", "o", "a")}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return s
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// Spooled mail is the session's private mail: 0700 directories, 0600 files (lead's condition 2), also when the
// directory existed with looser modes.
func TestPermissions(t *testing.T) {
	s := Spool{Dir: filepath.Join(t.TempDir(), "a")}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{Seq: 7, From: "b", Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{s.Dir: 0o700, s.drained(): 0o700, s.file(Ref{Seq: 7}): 0o600, filepath.Join(s.Dir, marker): 0o600} {
		if got := mode(t, p); got != want {
			t.Errorf("%s: %o, want %o", p, got, want)
		}
	}
	if err := s.Drained(Ref{Seq: 7}); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, filepath.Join(s.drained(), "7")); got != 0o600 {
		t.Errorf("tombstone: %o", got)
	}
}

func TestWaitingIsOldestFirstAndSkipsWhatItCannotRead(t *testing.T) {
	s := newSpool(t)
	for _, seq := range []uint64{12, 3, 9} {
		if err := s.Put(Entry{Seq: seq, From: "b", Trust: "verified", At: time.Unix(0, 0).UTC(), Body: []byte("m")}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"5.json": "{not json", "6.json": `{"seq":99}`, "notes.txt": "x", ".put-123": "partial"} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.Waiting()
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 3 || w[0].Seq != 3 || w[1].Seq != 9 || w[2].Seq != 12 || string(w[0].Body) != "m" {
		t.Fatalf("waiting %+v", w)
	}
}

// Drained writes the tombstone before it removes the message, and Acked forgets both.
func TestDrainedThenAcked(t *testing.T) {
	s := newSpool(t)
	if err := s.Put(Entry{Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if s.IsDrained(Ref{Seq: 4}) {
		t.Fatal("drained before the hook")
	}
	if err := s.Drained(Ref{Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 0 || !s.IsDrained(Ref{Seq: 4}) {
		t.Fatalf("after drain: waiting %v drained %v", w, s.IsDrained(Ref{Seq: 4}))
	}
	if err := s.Put(Entry{Seq: 4}); err != nil { // a redelivery spooled before serve noticed
		t.Fatal(err)
	}
	if err := s.Acked(Ref{Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 0 || s.IsDrained(Ref{Seq: 4}) {
		t.Fatalf("after ack: waiting %v drained %v", w, s.IsDrained(Ref{Seq: 4}))
	}
	if err := s.Acked(Ref{Seq: 4}); err != nil {
		t.Fatalf("acking twice: %v", err)
	}
}

func TestHookedSince(t *testing.T) {
	s := newSpool(t)
	start := time.Now()
	if s.HookedSince(start) {
		t.Fatal("hooked with no hook run")
	}
	if err := s.Touch(); err != nil {
		t.Fatal(err)
	}
	if !s.HookedSince(start) {
		t.Fatal("a hook run after the start is not seen")
	}
	if s.HookedSince(time.Now().Add(time.Second)) {
		t.Fatal("a hook run before the start counts")
	}
}

// A hook for an endpoint whose serve has made no spool does not create one.
func TestTouchNeedsTheSpool(t *testing.T) {
	s := Spool{Dir: filepath.Join(t.TempDir(), "none")}
	if err := s.Touch(); err == nil {
		t.Fatal("touched a spool that does not exist")
	}
}

func TestClearWaitingKeepsTombstones(t *testing.T) {
	s := Spool{Dir: t.TempDir()}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{Seq: 1, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drained(Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, ".put-123"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearWaiting(); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 0 {
		t.Fatalf("waiting %+v", w)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".put-123")); err == nil {
		t.Fatal("temp file kept")
	}
	if !s.IsDrained(Ref{Seq: 2}) {
		t.Fatal("tombstone lost")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".hooked")); err != nil {
		t.Fatal("marker lost")
	}
}

// codex H1 on #21 (round 2): tombstones and waiting files belong to one inbox stream; a different stream drops them.
func TestBindClearsTombstonesWhenTheStreamChanges(t *testing.T) {
	s := newSpool(t)
	if err := s.Bind("s1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Entry{Seq: 1, Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drained(Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("s1"); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 1 || !s.IsDrained(Ref{Seq: 2}) {
		t.Fatalf("same stream changed the spool: %+v drained=%v", w, s.IsDrained(Ref{Seq: 2}))
	}
	if err := s.Bind("s2"); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 0 || s.IsDrained(Ref{Seq: 2}) {
		t.Fatalf("new stream kept: %+v drained=%v", w, s.IsDrained(Ref{Seq: 2}))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".hooked")); err != nil {
		t.Fatalf("marker lost: %v", err)
	}
	p := filepath.Join(s.Dir, ".stream")
	if b, _ := os.ReadFile(p); string(b) != "s2" || mode(t, p) != 0o600 {
		t.Fatalf(".stream %q %v", b, mode(t, p))
	}
}

// The owner file round-trips, is 0600, and Own("") removes it.
func TestOwn(t *testing.T) {
	s := newSpool(t)
	if s.Owner() != "" {
		t.Fatalf("owner %q before Own", s.Owner())
	}
	if err := s.Own("S1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Owner(); got != "S1" {
		t.Fatalf("owner %q", got)
	}
	if m := mode(t, filepath.Join(s.Dir, ".session")); m != 0o600 {
		t.Fatalf("mode %v", m)
	}
	if err := s.Own("S2"); err != nil || s.Owner() != "S2" {
		t.Fatalf("re-own: %v %q", err, s.Owner())
	}
	if err := s.Own(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".session")); !os.IsNotExist(err) {
		t.Fatalf("file remains: %v", err)
	}
	if err := s.Own(""); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// Lock is exclusive: a second holder waits for the first to release (codex H1 on #21, round 7).
func TestLockIsExclusive(t *testing.T) {
	s := newSpool(t)
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, err := s.Lock()
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("second Lock acquired while the first was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("second Lock not acquired after unlock")
	}
}

// A hook records the seqs it is about to show; Waiting then flags those entries Redelivered (at read time, never in
// the entry's json). Drained and Acked forget the mark, a serve restart (ClearWaiting) keeps it as it keeps
// tombstones (the message is redelivered and may have been seen), and a new stream (Bind) drops it.
func TestShownMarksRedelivery(t *testing.T) {
	s := newSpool(t)
	for i := uint64(1); i <= 3; i++ {
		if err := s.Put(Entry{Seq: i, From: "b", Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := s.Waiting()
	for _, e := range w {
		if e.Redelivered {
			t.Fatalf("#%d redelivered before it was shown", e.Seq)
		}
	}
	if err := s.MarkShown(Ref{Seq: 1}, Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, filepath.Join(s.shown(), "1")); got != 0o600 {
		t.Errorf("shown marker: %o", got)
	}
	flags := func() [3]bool {
		w, err := s.Waiting()
		if err != nil || len(w) != 3 {
			t.Fatalf("%+v %v", w, err)
		}
		return [3]bool{w[0].Redelivered, w[1].Redelivered, w[2].Redelivered}
	}
	if got := flags(); got != [3]bool{true, true, false} {
		t.Fatalf("flags %v", got)
	}
	b, _ := os.ReadFile(s.file(Ref{Seq: 1}))
	if strings.Contains(string(b), "edelivered") {
		t.Fatalf("persisted in the entry: %s", b)
	}
	if err := s.Drained(Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.shown(), "1")); err == nil {
		t.Fatal("Drained left the shown marker")
	}
	if err := s.ClearWaiting(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.shown(), "2")); err != nil {
		t.Fatalf("ClearWaiting dropped the shown marker: %v", err)
	}
	if err := s.Put(Entry{Seq: 2, From: "b", Body: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if w, _ := s.Waiting(); len(w) != 1 || !w[0].Redelivered {
		t.Fatalf("a redelivery after a restart is not flagged: %+v", w)
	}
	if err := s.Acked(Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.shown(), "2")); err == nil {
		t.Fatal("Acked left the shown marker")
	}
	if err := s.MarkShown(Ref{Seq: 5}); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("stream-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.shown(), "5")); err == nil {
		t.Fatal("Bind left the shown marker")
	}
}

// A spool made before the shown/ directory existed still takes a mark.
func TestMarkShownCreatesItsDirectory(t *testing.T) {
	s := newSpool(t)
	os.Remove(s.shown())
	if err := s.MarkShown(Ref{Seq: 4}); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, s.shown()); got != 0o700 {
		t.Errorf("shown dir: %o", got)
	}
}

// .refused is written when absent or older than 60 s, read back, and cleared by Own.
func TestRefusedIsThrottledAndClearedByOwn(t *testing.T) {
	s := newSpool(t)
	if _, _, ok := s.Refused(); ok {
		t.Fatal("refusal before any")
	}
	if err := s.NoteRefused(Refusal{Time: time.Now(), Why: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteRefused(Refusal{Time: time.Now(), Why: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, why, ok := s.Refused(); !ok || why != "first" {
		t.Fatalf("within 60 s the file is kept: %v %q", ok, why)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(s.Dir, ".refused"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteRefused(Refusal{Time: time.Now(), Why: "third"}); err != nil {
		t.Fatal(err)
	}
	if _, why, _ := s.Refused(); why != "third" {
		t.Fatalf("an old file is rewritten: %q", why)
	}
	if err := s.Own("S1"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Refused(); ok {
		t.Fatal("Own did not clear .refused")
	}
}

// Own writes the legacy pid text to .session (what an older hook reads) and the JSON record to .owner; an empty
// record removes .owner; Owner prefers .owner; a damaged .owner is unreadable, never "no owner".
func TestOwnRecordKeepsLegacySessionText(t *testing.T) {
	s := newSpool(t)
	rec := `{"pid":42,"session":"S","launch":"L"}`
	if err := s.OwnRecord(LegacyToken(rec), rec); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(s.Dir, ".session")) // an old-style reader looks here only
	if err != nil || string(old) != "pid:42" {
		t.Fatalf(".session = %q, %v", old, err)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, ".owner"))
	if err != nil || string(b) != rec {
		t.Fatalf(".owner = %q, %v", b, err)
	}
	if m := mode(t, filepath.Join(s.Dir, ".owner")); m != 0o600 {
		t.Fatalf(".owner mode %o", m)
	}
	if s.Owner() != rec {
		t.Fatalf("Owner = %q", s.Owner())
	}
	if err := s.OwnRecord("pid:7", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, ".owner")); !os.IsNotExist(err) {
		t.Fatalf(".owner left after an empty record: %v", err)
	}
	if s.Owner() != "pid:7" {
		t.Fatalf("Owner = %q", s.Owner())
	}
	if err := os.WriteFile(filepath.Join(s.Dir, ".owner"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Owner() != OwnerUnreadable {
		t.Fatalf("empty .owner read as %q", s.Owner())
	}
	if err := os.Remove(filepath.Join(s.Dir, ".session")); err != nil { // a missing .session plus a present .owner works
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, ".owner"), []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Owner() != rec {
		t.Fatalf("Owner without .session = %q", s.Owner())
	}
}

func TestLegacyToken(t *testing.T) {
	if got := LegacyToken(`{"pid":9}`); got != "pid:9" {
		t.Fatal(got)
	}
	if got := LegacyToken(`{"session":"x"}`); got != "pid:"+strconv.Itoa(os.Getppid()) {
		t.Fatal(got)
	}
}

// .alive is a subagent's hook run: it is not .hooked, and AliveSince sees only a run at or after t.
func TestAliveIsNotHooked(t *testing.T) {
	s := newSpool(t)
	before := time.Now()
	if s.AliveSince(before) {
		t.Fatal("alive before any")
	}
	if err := s.Alive(); err != nil {
		t.Fatal(err)
	}
	if !s.AliveSince(before) {
		t.Fatal("AliveSince misses a run after t")
	}
	if s.AliveSince(time.Now().Add(time.Minute)) {
		t.Fatal("AliveSince sees a run before t as after")
	}
	if s.HookedSince(before) {
		t.Fatal("Alive marked the spool hooked")
	}
	if _, ok := s.LastHook(); ok {
		t.Fatal("Alive set the last hook run")
	}
	if err := s.Alive(); err != nil { // again: the marker exists
		t.Fatal(err)
	}
}

func TestWaiters(t *testing.T) {
	s := newSpool(t)
	self := os.Getpid()
	if got := s.Waiters(); len(got) != 0 {
		t.Fatalf("waiters before any: %v", got)
	}
	if err := s.NoteWaiter(WaiterRecord{PID: self, Session: "S1"}); err != nil {
		t.Fatal(err)
	}
	// junk, a dead pid and an unrelated pid file are skipped
	_ = os.WriteFile(filepath.Join(s.Dir, ".waiting.junk"), []byte("not json"), 0o600)
	_ = os.WriteFile(filepath.Join(s.Dir, ".waiting.999999999"), []byte(`{"pid":999999999}`), 0o600)
	got := s.Waiters()
	if len(got) != 1 || got[0].PID != self || got[0].Session != "S1" {
		t.Fatalf("Waiters = %+v", got)
	}
	if err := s.ClearWaiter(self); err != nil {
		t.Fatal(err)
	}
	if got := s.Waiters(); len(got) != 0 {
		t.Fatalf("after ClearWaiter: %v", got)
	}
	// OwnRecord deletes every .waiting.* naming the new owner's pid, and keeps the others
	if err := s.NoteWaiter(WaiterRecord{PID: self}); err != nil {
		t.Fatal(err)
	}
	other := os.Getppid()
	if err := s.NoteWaiter(WaiterRecord{PID: other}); err != nil {
		t.Fatal(err)
	}
	if err := s.OwnRecord("pid:"+strconv.Itoa(self), `{"pid":`+strconv.Itoa(self)+`}`); err != nil {
		t.Fatal(err)
	}
	got = s.Waiters()
	if len(got) != 1 || got[0].PID != other {
		t.Fatalf("after OwnRecord: %+v", got)
	}
	// a waiting message spool is not touched by waiter files, and vice versa
	if err := s.ClearWaiting(); err != nil {
		t.Fatal(err)
	}
	if len(s.Waiters()) != 1 {
		t.Fatal("ClearWaiting removed a waiter record")
	}
}

// A listed message stays waiting and undrained, flagged by Waiting; a restart's ClearWaiting and a stream rebind
// forget the mark (the redelivery is shown again), and a drain or an ack removes it (ADR 0046).
func TestListedMarkLifecycle(t *testing.T) {
	s := newSpool(t)
	put := func(seq uint64) {
		t.Helper()
		if err := s.Put(Entry{Seq: seq, From: "b", At: time.Now(), Body: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	put(1)
	put(2)
	if err := s.MarkShown(Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Listed(Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	w, _ := s.Waiting()
	if len(w) != 2 || !w[0].Listed || w[1].Listed || w[0].Redelivered || s.IsDrained(Ref{Seq: 1}) || !s.IsListed(Ref{Seq: 1}) || s.IsListed(Ref{Seq: 2}) {
		t.Fatalf("after Listed(1): %+v drained %v", w, s.IsDrained(Ref{Seq: 1}))
	}
	if mode(t, filepath.Join(s.Dir, "listed", "1")) != 0o600 {
		t.Fatal("listed mark is not 0600")
	}
	if err := s.Listed(Ref{Seq: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drained(Ref{Seq: 2}); err != nil || s.IsListed(Ref{Seq: 2}) || !s.IsDrained(Ref{Seq: 2}) {
		t.Fatalf("a drain keeps the listed mark: %v", err)
	}
	if err := s.Acked(Ref{Seq: 2}); err != nil || s.IsDrained(Ref{Seq: 2}) {
		t.Fatal(err)
	}
	if err := s.ClearWaiting(); err != nil || s.IsListed(Ref{Seq: 1}) {
		t.Fatalf("ClearWaiting kept the listed mark: %v", err)
	}
	put(1)
	if err := s.Listed(Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("stream-a"); err != nil || s.IsListed(Ref{Seq: 1}) {
		t.Fatalf("Bind to a new stream kept the listed mark: %v", err)
	}
	put(3)
	if err := s.Listed(Ref{Seq: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Acked(Ref{Seq: 3}); err != nil || s.IsListed(Ref{Seq: 3}) {
		t.Fatalf("Acked kept the listed mark: %v", err)
	}
}
