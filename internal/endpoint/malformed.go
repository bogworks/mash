package endpoint

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

// The malformed list (REQ-006): the inbox messages whose Agent-Sender header the SDK parser refuses as malformed. It is
// the unverified list's twin, in its own file beside it, with the same header, the same pruning and one more thing on
// each line, the class that says why (REQ-017: a class name, never the sender's content).

// malformedSeqsFile is the list: a header line "stream <id>", an optional "scanned <last>" line, and one "<seq> <class>"
// line for each malformed message the inbox stream may still hold.
const malformedSeqsFile = "refused-malformed.seqs"

// scannedPrefix opens the header line that says the one-time scan of the stream finished, with the last sequence it read.
const scannedPrefix = "scanned "

// seqEntry is one line of a list: Class is empty on the unverified list.
type seqEntry struct {
	Seq   uint64
	Class string
}

// seqList is a list file as read: the stream identity of its header ("" when there is none), whether and where the scan
// finished, and the entries in file order.
type seqList struct {
	ID      string
	Pruned  int // the "pruned" marker line: how many entries earlier prunes dropped (0 when none)
	Scanned uint64
	Done    bool // a "scanned" line is present
	Entries []seqEntry
}

func entriesOf(seqs []uint64) []seqEntry {
	out := make([]seqEntry, len(seqs))
	for i, s := range seqs {
		out[i] = seqEntry{Seq: s}
	}
	return out
}

// readSeqList reads a list file. A missing or damaged file reads as empty; a damaged line is skipped.
func readSeqList(path string) seqList {
	var l seqList
	f, err := os.Open(path)
	if err != nil {
		return l
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, refusedSeqsHeader); ok {
			l.ID = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(line, refusedSeqsPruned); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 0 {
				n = 0
			}
			l.Pruned = n // the last marker wins
			continue
		}
		if rest, ok := strings.CutPrefix(line, scannedPrefix); ok {
			if n, err := strconv.ParseUint(strings.TrimSpace(rest), 10, 64); err == nil {
				l.Scanned, l.Done = n, true
			}
			continue
		}
		num, class, _ := strings.Cut(strings.TrimSpace(line), " ")
		if n, err := strconv.ParseUint(num, 10, 64); err == nil {
			l.Entries = append(l.Entries, seqEntry{Seq: n, Class: strings.TrimSpace(class)})
		}
	}
	return l
}

// writeSeqList replaces the list file whole: the header lines, then the entries.
func writeSeqList(p string, l seqList) error {
	var b strings.Builder
	b.WriteString(refusedSeqsHeader + l.ID + "\n")
	if l.Pruned > 0 {
		b.WriteString(refusedSeqsPruned + strconv.Itoa(l.Pruned) + "\n")
	}
	if l.Done {
		b.WriteString(scannedPrefix + strconv.FormatUint(l.Scanned, 10) + "\n")
	}
	for _, en := range l.Entries {
		b.WriteString(entryLine(en))
	}
	return replaceFile(p, []byte(b.String()))
}

func entryLine(en seqEntry) string {
	if en.Class == "" {
		return strconv.FormatUint(en.Seq, 10) + "\n"
	}
	return strconv.FormatUint(en.Seq, 10) + " " + en.Class + "\n"
}

// appendListEntry adds ent to the list file p, whose current content is l (read under refusedMu by the caller, with the
// stream's identity in l.ID). A new file starts with its stream's identity. Past twice refusedSeqsKeep entries the ones
// below the stream's first sequence are dropped first, as the unverified list always did; an entry the stream still
// holds is never dropped. The caller holds refusedMu.
func (e *Endpoint) appendListEntry(ctx context.Context, p string, l seqList, ent seqEntry) error {
	if _, err := os.Stat(p); err != nil {
		if err := replaceFile(p, []byte(refusedSeqsHeader+l.ID+"\n")); err != nil {
			return err
		}
	}
	if len(l.Entries) >= 2*refusedSeqsKeep {
		if first, err := e.refusedFirstSeq(ctx); err == nil {
			kept := make([]seqEntry, 0, len(l.Entries)+1)
			for _, en := range l.Entries {
				if en.Seq >= first {
					kept = append(kept, en)
				}
			}
			if len(kept) < len(l.Entries) {
				l.Pruned += len(l.Entries) - len(kept) // the marker keeps the count exact (#1302)
				l.Entries = append(kept, ent)
				return writeSeqList(p, l) // written whole, ent included, the scanned line kept
			}
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(entryLine(ent))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// malformedPath is the list file of this endpoint.
func (e *Endpoint) malformedPath() (string, error) {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, malformedSeqsFile), nil
}

// malformedListed reports whether seq is on the malformed list of the stream bound now.
func (e *Endpoint) malformedListed(seq uint64) bool {
	id := e.refusedStream.Load()
	if id == nil || e.cfg.StateDir == "" {
		return false
	}
	p, err := e.malformedPath()
	if err != nil {
		return false
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	l := readSeqList(p)
	return l.ID == *id && slices.ContainsFunc(l.Entries, func(en seqEntry) bool { return en.Seq == seq })
}

// recordMalformedNew records seq as malformed with its class; added reports whether it was new, false when it was
// already on the list (nothing is written). An error means nothing was recorded.
func (e *Endpoint) recordMalformedNew(seq uint64, class string) (added bool, err error) {
	if e.cfg.StateDir == "" {
		return false, errors.New("no state directory")
	}
	p, err := e.malformedPath()
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := e.bindRefusals(ctx)
	if err != nil {
		return false, err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return false, err
	}
	l := readSeqList(p)
	if l.ID != id {
		l = seqList{ID: id} // another stream's entries are not this stream's
	}
	if slices.ContainsFunc(l.Entries, func(en seqEntry) bool { return en.Seq == seq }) {
		return false, nil
	}
	if err := e.appendListEntry(ctx, p, l, seqEntry{Seq: seq, Class: class}); err != nil {
		return false, err
	}
	return true, nil
}

// refuseMalformed is the one function both entry points call: the serve path on first sight of a message and the scan.
// Log first, then record: a sequence not on the list gets its log line (the class only, TO-7), then is recorded. A
// sequence already on the list logs nothing. A crash between the two can repeat one line after the restart and can
// never leave a record with no line. added reports whether this call recorded the sequence.
func (e *Endpoint) refuseMalformed(seq uint64, class string) (added bool, err error) {
	if class == "" {
		class = sapsdk.ClassUnreadable
	}
	if !e.malformedListed(seq) {
		e.log.Printf("refused message %d: %s", seq, class)
	}
	return e.recordMalformedNew(seq, class)
}

// refusedLists reads both lists of the stream and applies REQ-006 item 6: one refusal for each message. An unverified
// entry whose sequence is on the malformed list is left out (it counts and shows once, as malformed); the unverified
// file itself is never edited. A list of another stream's identity reads as empty. The views use this.
func (e *Endpoint) refusedLists(stream string) (malformed []seqEntry, unverified []uint64) {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return nil, nil
	}
	return mergeRefusedLists(stream, readSeqList(filepath.Join(dir, malformedSeqsFile)), readSeqList(filepath.Join(dir, refusedSeqsFile)))
}

func mergeRefusedLists(stream string, mal, unv seqList) (malformed []seqEntry, unverified []uint64) {
	if mal.ID == stream {
		malformed = mal.Entries
	}
	if unv.ID != stream {
		return malformed, nil
	}
	for _, en := range unv.Entries {
		if !slices.ContainsFunc(malformed, func(m seqEntry) bool { return m.Seq == en.Seq }) {
			unverified = append(unverified, en.Seq)
		}
	}
	return malformed, unverified
}

// StoredScanRunning is true while the one-time scan of the stored mail runs (the first start of the switched-on build):
// nothing is delivered and status says not ready, with ReasonStoredScan.
func (e *Endpoint) StoredScanRunning() bool { return e.scanning.Load() }

// scanStored is the one-time scan (REQ-006 item 3): once per stream identity it reads the stream's [first, last],
// classifies each message once through sapsdk.Classify and records the malformed ones, then writes the "scanned <last>"
// line. It runs before consumption begins, so nothing is delivered and mail that arrives meanwhile waits in the stream.
// A scan killed half-way is done again from the start: recording is idempotent and logs only the sequences not yet on
// the list. It reads the stream and writes nothing else.
func (e *Endpoint) scanStored(ctx context.Context) error {
	if e.cfg.StateDir == "" { // nowhere to keep the list or the scanned line
		return nil
	}
	p, err := e.malformedPath()
	if err != nil {
		return err
	}
	id, err := e.bindRefusals(ctx)
	if err != nil {
		return err
	}
	refusedMu.Lock()
	l := readSeqList(p)
	refusedMu.Unlock()
	if l.ID == id && l.Done {
		return nil
	}
	// The stream's range is the startup's own read (no round trip of its own); a stream handle, which costs a request,
	// is taken only when there is stored mail to read.
	info := e.bindInfo.Load()
	if info == nil {
		s, err := e.js.Stream(ctx, e.ident.InboxStream())
		if err != nil {
			return err
		}
		if info, err = s.Info(ctx); err != nil {
			return err
		}
	}
	// A state directory that cannot be made holds no list and no scanned line: nothing can be recorded, so there is
	// nothing to scan for. Every message is still classified on its way through serve (a malformed one is never
	// delivered), and a start with a writable directory scans.
	// An existing directory that is read-only passes MkdirAll, so a file is made and removed to prove it takes writes.
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		e.log.Printf("mash: stored mail not checked: the state directory is not writable: %v", err)
		return nil
	}
	if probe, err := os.CreateTemp(filepath.Dir(p), ".scanprobe-*"); err != nil {
		e.log.Printf("mash: stored mail not checked: the state directory is not writable: %v", err)
		return nil
	} else {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
	}
	first, last := info.State.FirstSeq, info.State.LastSeq
	e.scanning.Store(true)
	e.RecordHookState()
	defer func() {
		e.scanning.Store(false)
		e.RecordHookState()
	}()
	var s jetstream.Stream
	if info.State.Msgs > 0 {
		var err error
		if s, err = e.js.Stream(ctx, e.ident.InboxStream()); err != nil {
			return err
		}
	}
	for from := first; from <= last && info.State.Msgs > 0; {
		to := min(from+uint64(countBatch)-1, last)
		n := int(to - from + 1)
		raws := make([]*jetstream.RawStreamMsg, n)
		errs := make([]error, n)
		parallel(n, fetchConcurrency, func(i int) { raws[i], errs[i] = s.GetMsg(ctx, from+uint64(i)) })
		for i := range raws {
			seq := from + uint64(i)
			if e.leaseGone.Load() { // a replacement serve may scan now: this one writes nothing more
				return nil // stopped, not finished: nothing is marked done, and a replacement serve scans
			}
			if e.scanStep != nil {
				if err := e.scanStep(seq); err != nil {
					return err
				}
			}
			switch {
			case errs[i] == nil:
			case errors.Is(errs[i], jetstream.ErrMsgNotFound):
				continue
			default:
				return fmt.Errorf("mash: stored mail check: read #%d: %w", seq, errs[i])
			}
			trust, class := sapsdk.Classify(raws[i].Subject, raws[i].Header.Values(sap.HeaderName), raws[i].Data)
			if trust != sap.TrustMalformed {
				continue
			}
			if _, err := e.refuseMalformed(seq, class); err != nil {
				return fmt.Errorf("mash: stored mail check: #%d not recorded: %w", seq, err)
			}
		}
		from = to + 1
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	if e.leaseGone.Load() {
		return nil // stopped, not finished: nothing is marked done, and a replacement serve scans
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	l = readSeqList(p)
	if l.ID != id {
		l = seqList{ID: id}
	}
	l.Scanned, l.Done = last, true
	return writeSeqList(p, l)
}
