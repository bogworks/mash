package endpoint

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/spool"
)

// Pure tests of the malformed list (REQ-006 items 1, 2 and 6): no server, no stream.

// listEndpoint is an endpoint with a state directory, bound to a stream identity, whose log lines go to the buffer. When
// atLog is set it runs at each log write, so a test can look at the list at the moment the line is written.
func listEndpoint(t *testing.T, atLog func()) (*Endpoint, *bytes.Buffer, string) {
	t.Helper()
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	buf := &bytes.Buffer{}
	e := &Endpoint{cfg: cfg, log: log.New(writerFunc(func(p []byte) (int, error) {
		if atLog != nil {
			atLog()
		}
		return buf.Write(p)
	}), "", 0)}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	dir, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, buf, filepath.Join(dir, malformedSeqsFile)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// The list has the unverified list's header and one "<seq> <class>" line each; a sequence is recorded once.
func TestRecordMalformedNewIsIdempotentAndWritesTheClass(t *testing.T) {
	e, _, p := listEndpoint(t, nil)
	for i := 0; i < 3; i++ {
		added, err := e.recordMalformedNew(7, "name too long")
		if err != nil || added != (i == 0) {
			t.Fatalf("record %d: added %v err %v", i, added, err)
		}
	}
	if added, err := e.recordMalformedNew(9, "two sender headers"); err != nil || !added {
		t.Fatalf("second: %v %v", added, err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "stream inbox@now\n7 name too long\n9 two sender headers\n" {
		t.Fatalf("file %q %v", b, err)
	}
	if !e.malformedListed(7) || e.malformedListed(8) {
		t.Fatal("malformedListed")
	}
}

// Log first, then record. At the moment the line is written the sequence is not on the list yet; a sequence on the list
// logs nothing; every recorded sequence has a line, and an uninterrupted run has exactly one.
func TestMalformedLogsFirstThenRecords(t *testing.T) {
	var e *Endpoint
	var listedAtLog []bool
	e, buf, _ := listEndpoint(t, func() { listedAtLog = append(listedAtLog, e.malformedListed(3)) })
	for i := 0; i < 3; i++ {
		if _, err := e.refuseMalformed(3, "control character in name"); err != nil {
			t.Fatal(err)
		}
	}
	if got := buf.String(); got != "refused message 3: control character in name\n" {
		t.Fatalf("log %q, want one line in the TO-7 wording", got)
	}
	if len(listedAtLog) != 1 || listedAtLog[0] {
		t.Fatalf("the sequence was already on the list when the line was written: %v", listedAtLog)
	}
	if !e.malformedListed(3) {
		t.Fatal("not recorded")
	}
}

// A crash between the log write and the record leaves a line and no record, so the restart logs it again (one line
// repeated), and it never leaves a record with no line.
func TestMalformedCrashBetweenLogAndRecord(t *testing.T) {
	var e *Endpoint
	e, buf, p := listEndpoint(t, func() { panic("crash at the log write") })
	func() {
		defer func() { _ = recover() }()
		_, _ = e.refuseMalformed(5, "name too long")
	}()
	if _, err := os.Stat(p); err == nil {
		t.Fatalf("a record exists with the crash at the log write")
	}
	// The restart: the same sequence, a log that works.
	e.log = log.New(buf, "", 0)
	if _, err := e.refuseMalformed(5, "name too long"); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "refused message 5: name too long\n"); n != 1 {
		t.Fatalf("%d lines after the restart: %q", n, buf.String())
	}
	if !e.malformedListed(5) {
		t.Fatal("not recorded after the restart")
	}
}

// The log line carries the class and nothing of the header (REQ-017).
func TestMalformedLogLineHasNoHeaderContent(t *testing.T) {
	e, buf, _ := listEndpoint(t, nil)
	if _, err := e.refuseMalformed(12, "signature unreadable"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "refused message 12: signature unreadable" {
		t.Fatalf("%q", buf.String())
	}
}

// Same pruning as the unverified list: past twice the keep, entries below the stream's first sequence go; one the stream
// holds never does; the scanned line is kept.
func TestMalformedListPrunesLikeTheUnverifiedOne(t *testing.T) {
	old := refusedSeqsKeep
	refusedSeqsKeep = 10
	t.Cleanup(func() { refusedSeqsKeep = old })
	e, _, p := listEndpoint(t, nil)
	first := uint64(1)
	e.firstSeqFn = func(context.Context) (uint64, error) { return first, nil }
	refusedMu.Lock()
	l := readSeqList(p)
	l.ID, l.Scanned, l.Done = "inbox@now", 99, true
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	err := writeSeqList(p, l)
	refusedMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	const n = 30
	for i := uint64(1); i <= n; i++ {
		if _, err := e.recordMalformedNew(i, "name too long"); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(1); i <= n; i++ {
		if !e.malformedListed(i) {
			t.Fatalf("sequence %d lost while the stream holds it", i)
		}
	}
	first = 25
	for i := uint64(n + 1); i <= n+2; i++ {
		if _, err := e.recordMalformedNew(i, "name too long"); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(1); i <= n+2; i++ {
		if want := i >= 25; e.malformedListed(i) != want {
			t.Fatalf("sequence %d listed=%v, want %v", i, !want, want)
		}
	}
	if l := readSeqList(p); !l.Done || l.Scanned != 99 || l.ID != "inbox@now" {
		t.Fatalf("header lost in pruning: %+v", l)
	}
}

// The scanned line is part of the header, survives reads and appends, and a list of another stream identity is replaced
// by a header alone when the stream is bound (so the new stream is scanned).
func TestScannedLineAndStreamIdentity(t *testing.T) {
	e, _, p := listEndpoint(t, nil)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSeqList(p, seqList{ID: "inbox@now", Scanned: 41, Done: true, Entries: []seqEntry{{Seq: 3, Class: "name too long"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.recordMalformedNew(4, "space in subject"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "stream inbox@now\nscanned 41\n3 name too long\n4 space in subject\n" {
		t.Fatalf("file %q", b)
	}
	// Another stream.
	e2 := &Endpoint{cfg: e.cfg, log: e.log}
	if _, err := e2.bindRefusalsTo("inbox@later"); err != nil {
		t.Fatal(err)
	}
	if l := readSeqList(p); l.ID != "inbox@later" || l.Done || len(l.Entries) != 0 {
		t.Fatalf("the old stream's list is applied to the new stream: %+v", l)
	}
}

// Precedence (REQ-006 item 6): an unverified entry whose sequence is on the malformed list is skipped by the views'
// helper; the unverified file is not edited; a list of another stream reads as empty.
func TestMalformedTakesPrecedenceOverUnverified(t *testing.T) {
	e, _, mp := listEndpoint(t, nil)
	if err := os.MkdirAll(filepath.Dir(mp), 0o700); err != nil {
		t.Fatal(err)
	}
	up := filepath.Join(filepath.Dir(mp), refusedSeqsFile)
	if err := writeRefusedSeqs(up, "inbox@now", 0, []uint64{2, 5, 8}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(up)
	if _, err := e.recordMalformedNew(5, "empty sender header"); err != nil { // shape 9, an old unverified record
		t.Fatal(err)
	}
	if _, err := e.recordMalformedNew(6, "name too long"); err != nil {
		t.Fatal(err)
	}
	mal, unv := e.refusedLists("inbox@now")
	if !slices.Equal(unv, []uint64{2, 8}) {
		t.Fatalf("unverified %v, want 2 and 8 (5 is malformed)", unv)
	}
	if len(mal) != 2 || mal[0] != (seqEntry{5, "empty sender header"}) || mal[1] != (seqEntry{6, "name too long"}) {
		t.Fatalf("malformed %v", mal)
	}
	if after, _ := os.ReadFile(up); !bytes.Equal(before, after) {
		t.Fatalf("the unverified record was edited:\n%s\n%s", before, after)
	}
	if m, u := e.refusedLists("inbox@other"); len(m) != 0 || len(u) != 0 {
		t.Fatalf("another stream's lists apply: %v %v", m, u)
	}
}

// A shell reads "checking stored mail" from the serve's record, only while that serve exists.
func TestScanOnDiskReadsTheServesRecord(t *testing.T) {
	sp := spool.Spool{Dir: t.TempDir()}
	if ScanOnDisk(sp) {
		t.Fatal("no record, yet scanning")
	}
	host, _ := os.Hostname()
	rec := HookRecord{Since: time.Now(), Host: host, PID: strconv.Itoa(os.Getpid()), Scanning: true}
	rec.Start, _ = procStart(os.Getpid())
	recordTo(t, sp, rec)
	if !ScanOnDisk(sp) {
		t.Fatal("the record says scanning")
	}
	rec.Scanning = false
	recordTo(t, sp, rec)
	if ScanOnDisk(sp) {
		t.Fatal("the record says the scan is over")
	}
	rec.Scanning, rec.PID = true, "999999999"
	recordTo(t, sp, rec)
	if ScanOnDisk(sp) {
		t.Fatal("a serve that is gone is not scanning")
	}
}

// A shell derives the skipped scan from the directory, with no record: the scan is not done and the directory is not
// writable. The access check is replaced, as a chmod does not block a root test.
func TestScanSkipOnDiskIsDerivedFromTheDirectory(t *testing.T) {
	old := DirAccessible
	t.Cleanup(func() { DirAccessible = old })
	sp := spool.Spool{Dir: t.TempDir()}
	list := filepath.Join(sp.Dir, malformedSeqsFile)
	writable := true
	DirAccessible = func(string) bool { return writable }
	if got := ScanSkipOnDisk(sp); got != "" {
		t.Fatalf("writable, yet skipped: %q", got)
	}
	writable = false
	if got := ScanSkipOnDisk(sp); got != sp.Dir {
		t.Fatalf("not done and unwritable: %q", got)
	}
	if err := writeSeqList(list, seqList{ID: "s", Scanned: 3, Done: true}); err != nil {
		t.Fatal(err)
	}
	if got := ScanSkipOnDisk(sp); got != "" {
		t.Fatalf("scan done, yet skipped: %q", got)
	}
	// A directory not made yet is as writable as its nearest existing ancestor.
	var asked string
	DirAccessible = func(d string) bool { asked = d; return false }
	if got := ScanSkipOnDisk(spool.Spool{Dir: filepath.Join(sp.Dir, "new", "deeper")}); got == "" || asked != sp.Dir {
		t.Fatalf("missing dir: %q asked %q", got, asked)
	}
}
