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
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// Refusing unverified mail. Only mail whose trust is verified reaches the session, the inbox listing and mash read.
// Mail that is unsigned (absent), signed by an unknown key, claimed but unproven, or stale is dropped and counted, and
// the sender is not told. accept_unverified (MASH_ACCEPT_UNVERIFIED, mash serve --accept-unverified) delivers it, with
// its label, as before. Forged and malformed mail was never shown and still is not (classify).

// refusedSeqsFile lists, one decimal stream sequence per line, the inbox messages serve refused. The inbox stream keeps
// everything the server stored, so the counts mash inbox reports leave these out: the file says which they are. It is a
// help for the counts only; a missing or damaged file means the counts include the refused mail still stored.
//
// The count file (RefusedFile) is the running count, which includes the sequences pruned from this file. The file
// therefore also records how many it dropped (refusedSeqsPruned), so that count == pruned + recorded sequences holds
// after every write and a count left low by a kill can be repaired from the file alone.
const refusedSeqsFile = "refused-unverified.seqs"

// refusedSeqsHeader opens the sequences file: the identity of the inbox stream the sequences belong to (its name and
// creation time, as the spool binds to). Stream sequences restart when a stream is recreated, so a file of another
// stream's identity is stale: its sequences and the count beside it are discarded, never applied to the new stream.
const refusedSeqsHeader = "stream "

// refusedSeqsKeep is the size at which the file is pruned: past twice that, the sequences below the inbox stream's
// first sequence (gone from the stream) are dropped, and the number dropped is added to the file's pruned marker. A
// sequence the stream still holds is never dropped.
var refusedSeqsKeep = 4096

// refusedSeqsPruned opens the marker line "pruned N": N is the decimal count of sequences dropped from this file so far,
// for the stream identity in its header. A reader that does not know the marker ignores it, as it ignores any line that
// is not a decimal sequence (ParseUint fails and the line is skipped), so an older mash reads the same sequences.
const refusedSeqsPruned = "pruned "

// refusedMu serialises the writers of the count and the sequence list, so concurrent arrivals lose no count.
var refusedMu sync.Mutex

// refusedFileReads counts the reads of the sequences file (tests check a reader does not parse it per message).
var refusedFileReads atomic.Int64

// refuses reports whether mail of this trust is refused: accept_unverified is off and the trust is not verified.
func (e *Endpoint) refuses(t sap.Trust) bool {
	return !e.cfg.AcceptUnverified && t != sap.TrustVerified
}

// refuse records one refused message: the count (RefusedFile, replaced whole), the sequence and one line on the log
// without the body. A failure to write is logged and reported: the message is then not acknowledged. It reports whether the
// refusal is recorded and whether this call added it.
func (e *Endpoint) refuse(seq uint64, claimed string, trust sap.Trust) (recorded, added bool) {
	line := fmt.Sprintf("refused message %d from %s: not verified (%s)", seq, refusedSender(claimed), trust)
	_, added, err := e.recordRefusalNew(seq)
	if err != nil {
		line += fmt.Sprintf("; the refusal could not be recorded: %v", err)
	}
	e.log.Print(line)
	return err == nil, added
}

// unknownSender stands in the log line for a message that claims no sender at all.
const unknownSender = "(unknown sender)"

// refusedSender is the address a refused message claims, for the log line: control characters (newline and tab too)
// shown as visible \xNN escapes, so a sender cannot forge a log line. A message that claims nothing is unknownSender.
func refusedSender(claimed string) string {
	if claimed == "" || claimed == "?" {
		return unknownSender
	}
	return strings.NewReplacer("\n", `\x0a`, "\t", `\x09`).Replace(config.EscapeText(claimed))
}

// claimedAddress is the address a message claims to come from: the name its sender header gives, else what classify
// resolved, which is empty when nothing was claimed.
func claimedAddress(h sap.SenderHeader, from string) string {
	if h.Name != "" {
		return h.Name
	}
	return from
}

// refusedSeq reports whether serve refused the inbox message with this sequence (it is in the sidecar). The record
// holds whatever accept_unverified is now: mail refused earlier is not delivered afterwards, and a later directory
// update does not make it verified.
func (e *Endpoint) refusedSeq(seq uint64, stream string) bool {
	return e.refusedSnapshot().has(seq, stream)
}

// refusedSnap is the sequences file as read once: a reader that judges many messages reads the file once and asks the
// snapshot for each, instead of opening and parsing it per message. A missing or unreadable file, or none to find, is
// an empty snapshot: nothing is refused.
type refusedSnap struct {
	id   string
	seqs map[uint64]bool
}

// refusedSnapshot reads the sequences file once.
func (e *Endpoint) refusedSnapshot() refusedSnap {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return refusedSnap{}
	}
	id, seqs := readRefusedSeqs(filepath.Join(dir, refusedSeqsFile))
	set := make(map[uint64]bool, len(seqs))
	for _, n := range seqs {
		set[n] = true
	}
	return refusedSnap{id: id, seqs: set}
}

// has reports whether the snapshot lists seq as refused for the inbox stream with this identity.
func (s refusedSnap) has(seq uint64, stream string) bool {
	return s.id == stream && s.seqs[seq]
}

// streamIdentity is the identity of the inbox stream described by info.
func streamIdentity(info *jetstream.StreamInfo) string {
	return info.Config.Name + "@" + info.Created.UTC().Format(time.RFC3339Nano)
}

// bindRefusals ties the count and the sequences to the inbox stream's identity: when the files belong to another
// stream (it was recreated) they are discarded, the count with them. It returns the identity.
func (e *Endpoint) bindRefusals(ctx context.Context) (string, error) {
	if id := e.refusedStream.Load(); id != nil {
		return *id, nil
	}
	st, err := e.js.Stream(ctx, e.ident.InboxStream())
	if err != nil {
		return "", err
	}
	return e.bindRefusalsTo(streamIdentity(st.CachedInfo()))
}

// bindRefusalsTo is bindRefusals for the stream identity id, already read.
func (e *Endpoint) bindRefusalsTo(id string) (string, error) {
	if e.cfg.StateDir != "" {
		dir, err := SpoolDir(e.cfg)
		if err != nil {
			return "", err
		}
		refusedMu.Lock()
		defer refusedMu.Unlock()
		p := filepath.Join(dir, refusedSeqsFile)
		if have, _ := readRefusedSeqs(p); have != id {
			_, seenFile := os.Stat(p)
			if seenFile == nil || RefusedCount(e.cfg) > 0 {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					return "", err
				}
				if err := replaceFile(filepath.Join(dir, RefusedFile), []byte("0\n")); err != nil {
					return "", err
				}
				if err := replaceFile(p, []byte(refusedSeqsHeader+id+"\n")); err != nil {
					return "", err
				}
			}
		}
	}
	e.refusedStream.Store(&id)
	return id, nil
}

// RefusedMessageError is the error of a read of a message serve refused because it was not verified.
type RefusedMessageError struct{ Seq uint64 }

func (e *RefusedMessageError) Error() string {
	return fmt.Sprintf("mash: message #%d was refused: it was not verified", e.Seq)
}

// recordRefusal records the refusal of seq. An error means nothing was counted. The sequence is written first, the
// count after: a count that fails after the sequence was recorded is retried by the redelivery, which finds the
// sequence and does not count it twice.
func (e *Endpoint) recordRefusal(seq uint64) (int, error) {
	n, _, err := e.recordRefusalNew(seq)
	return n, err
}

// refusedFirstSeq is the first sequence the inbox stream still holds.
func (e *Endpoint) refusedFirstSeq(ctx context.Context) (uint64, error) {
	if e.firstSeqFn != nil {
		return e.firstSeqFn(ctx)
	}
	if e.js == nil {
		return 0, errors.New("no stream")
	}
	st, err := e.js.Stream(ctx, e.ident.InboxStream())
	if err != nil {
		return 0, err
	}
	info, err := st.Info(ctx)
	if err != nil {
		return 0, err
	}
	return info.State.FirstSeq, nil
}

// writeRefusedSeqs replaces the sequences file with the stream identity id, the pruned marker (only when pruned is
// above 0) and seqs.
func writeRefusedSeqs(p, id string, pruned int, seqs []uint64) error {
	var b strings.Builder
	b.WriteString(refusedSeqsHeader + id + "\n")
	if pruned > 0 {
		b.WriteString(refusedSeqsPruned + strconv.Itoa(pruned) + "\n")
	}
	for _, s := range seqs {
		b.WriteString(strconv.FormatUint(s, 10) + "\n")
	}
	return replaceFile(p, []byte(b.String()))
}

// repairRefusedCountLocked raises the count file to pruned plus the number of recorded sequences when it is lower
// (every pruned and every recorded sequence was counted; a kill between the two writes leaves it low) and returns the
// count as it stands. The caller holds refusedMu.
func (e *Endpoint) repairRefusedCountLocked(dir string, pruned int, seqs []uint64) (int, error) {
	cur := RefusedCount(e.cfg)
	have := max(cur, pruned+len(seqs))
	if have > cur {
		if err := replaceFile(filepath.Join(dir, RefusedFile), []byte(strconv.Itoa(have)+"\n")); err != nil {
			return cur, err
		}
	}
	return have, nil
}

// reconcileRefusedCount repairs a count left low by a kill, for the redelivery of a recorded refusal that is
// acknowledged without going through recordRefusalNew. Errors are returned for logging; they never block the ack.
func (e *Endpoint) reconcileRefusedCount(stream string) error {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	id, seqs, pruned := readRefusedFile(filepath.Join(dir, refusedSeqsFile))
	if id != stream {
		return nil
	}
	_, err = e.repairRefusedCountLocked(dir, pruned, seqs)
	return err
}

// recordRefusalNew is recordRefusal; added reports whether seq was new (counted now), false when it was already
// recorded and the count is returned as it stands.
func (e *Endpoint) recordRefusalNew(seq uint64) (n int, added bool, err error) {
	if e.cfg.StateDir == "" {
		return 0, false, errors.New("no state directory")
	}
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return 0, false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := e.bindRefusals(ctx)
	if err != nil {
		return 0, false, err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, false, err
	}
	p := filepath.Join(dir, refusedSeqsFile)
	fid, seqs, pruned := readRefusedFile(p)
	if fid != id {
		seqs, pruned = nil, 0 // another stream's sequences are not this stream's
	}
	// Every pruned and every recorded sequence was counted, so the count is never below their number. A kill between
	// the sequence write and the count write leaves it below: the redelivery repairs it here, before it returns.
	have, rerr := e.repairRefusedCountLocked(dir, pruned, seqs)
	// A legacy file (written by a mash before the marker) may already have been pruned: its count is above its recorded
	// sequences and it has no marker, so pruned reads 0. Persist the difference as the marker before this call changes
	// the file, so a kill after the append or prune and before the count write leaves pruned+recorded at or above the
	// old count, and the repair above is exact. A file whose marker is current has base == pruned and is not rewritten.
	if fid == id {
		if base := have - len(seqs); base > pruned {
			if err := writeRefusedSeqs(p, id, base, seqs); err != nil {
				return 0, false, err
			}
			pruned = base
		}
	}
	if slices.Contains(seqs, seq) {
		return have, false, rerr
	}
	if _, err := os.Stat(p); err != nil { // a new file starts with its stream's identity
		if err := replaceFile(p, []byte(refusedSeqsHeader+id+"\n")); err != nil {
			return 0, false, err
		}
	}
	wrote := false
	if len(seqs) >= 2*refusedSeqsKeep {
		// Only sequences the stream no longer holds are dropped: every one it still holds stays refused.
		if first, err := e.refusedFirstSeq(ctx); err == nil {
			kept := make([]uint64, 0, len(seqs)+1)
			for _, s := range seqs {
				if s >= first {
					kept = append(kept, s)
				}
			}
			if len(kept) < len(seqs) {
				if err := writeRefusedSeqs(p, id, pruned+len(seqs)-len(kept), append(kept, seq)); err != nil {
					return 0, false, err
				}
				wrote = true // written whole, seq included
			}
		}
	}
	if !wrote {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, false, err
		}
		_, werr := fmt.Fprintf(f, "%d\n", seq)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return 0, false, werr
		}
	}
	n = have + 1
	if err := replaceFile(filepath.Join(dir, RefusedFile), []byte(strconv.Itoa(n)+"\n")); err != nil {
		return 0, false, err
	}
	return n, true, nil
}

// replaceFile writes data to a temporary file beside path and renames it over path, so a reader never sees half of it.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o600)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name) // the temporary file this call made
	}
	return werr
}

// readRefusedSeqs reads the sequences file: the stream identity of its header line ("" when there is none) and the
// distinct sequences, in file order. A sequence written twice (two processes appending across a handover) is one
// refusal, so every count derived from the list is of distinct sequences.
func readRefusedSeqs(path string) (string, []uint64) {
	id, seqs, _ := readRefusedFile(path)
	return id, seqs
}

// readRefusedFile is readRefusedSeqs with the pruned total of the marker line too (0 when there is none or its number
// is malformed; the last marker wins).
func readRefusedFile(path string) (id string, seqs []uint64, pruned int) {
	refusedFileReads.Add(1)
	f, err := os.Open(path)
	if err != nil {
		return "", nil, 0
	}
	defer f.Close()
	seen := map[uint64]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), refusedSeqsPruned); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil || n < 0 {
				n = 0
			}
			pruned = n
			continue
		}
		if rest, ok := strings.CutPrefix(sc.Text(), refusedSeqsHeader); ok {
			id = strings.TrimSpace(rest)
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(sc.Text()), 10, 64); err == nil && !seen[n] {
			seen[n] = true
			seqs = append(seqs, n)
		}
	}
	return id, seqs, pruned
}

// refusedStored is how many of the refused messages the inbox stream, whose sequences run from first to last, may still
// hold.
func (e *Endpoint) refusedStored(first, last uint64, stream string) (stored int) {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return 0
	}
	seen := map[uint64]bool{}
	id, seqs := readRefusedSeqs(filepath.Join(dir, refusedSeqsFile))
	if id != stream {
		return 0
	}
	for _, s := range seqs {
		if s >= first && s <= last && !seen[s] {
			seen[s] = true
		}
	}
	return len(seen)
}

// deliveredSeqsFile lists, in the format of refusedSeqsFile, the inbox messages serve delivered although they were not
// verified, because accept_unverified was on for serve. A shell command (mash read, mash inbox) shows them with their
// trust label whatever its own setting says. Mail no serve has judged is judged by the shell's own setting. A missing or
// damaged file means nothing is recorded: the shell uses its own setting for all of it.
const deliveredSeqsFile = "delivered-unverified.seqs"

// deliveredSeq reports whether serve delivered the unverified inbox message with this sequence (it is in the sidecar
// and the sidecar belongs to this inbox stream).
func (e *Endpoint) deliveredSeq(seq uint64, stream string) bool {
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return false
	}
	id, seqs := readRefusedSeqs(filepath.Join(dir, deliveredSeqsFile))
	return id == stream && slices.Contains(seqs, seq)
}

// shownAtShell reports whether a shell shows an unverified message that its own setting would refuse: yes when serve
// delivered it (the delivered sidecar). The caller has already checked the refused sidecar, which wins.
func (e *Endpoint) shownAtShell(seq uint64) bool {
	id := e.inboxID.Load()
	return id != nil && e.deliveredSeq(seq, *id)
}

// recordDelivered records that serve delivers the unverified message seq, before it is queued or acknowledged. An error
// means nothing is recorded (the shell then falls back to its own setting).
func (e *Endpoint) recordDelivered(seq uint64) error {
	if e.cfg.StateDir == "" {
		return errors.New("no state directory")
	}
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := e.bindRefusals(ctx)
	if err != nil {
		return err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(dir, deliveredSeqsFile)
	fid, seqs := readRefusedSeqs(p)
	if fid != id {
		seqs = nil // another stream's sequences are not this stream's
		if err := writeRefusedSeqs(p, id, 0, nil); err != nil {
			return err
		}
	}
	if slices.Contains(seqs, seq) {
		return nil
	}
	if len(seqs) >= 2*refusedSeqsKeep {
		// Only sequences the stream no longer holds are dropped.
		if first, err := e.refusedFirstSeq(ctx); err == nil {
			kept := make([]uint64, 0, len(seqs)+1)
			for _, s := range seqs {
				if s >= first {
					kept = append(kept, s)
				}
			}
			if len(kept) < len(seqs) {
				return writeRefusedSeqs(p, id, 0, append(kept, seq))
			}
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintf(f, "%d\n", seq)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// deliveredEarlier reports whether serve recorded this unverified message as delivered, under accept_unverified, before:
// a redelivery (its ack was lost, or serve restarted with the setting off) keeps that verdict and is not refused now,
// the way a recorded refusal stays refused.
func (e *Endpoint) deliveredEarlier(seq uint64) bool {
	if e.cfg.StateDir == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := e.bindRefusals(ctx)
	return err == nil && e.deliveredSeq(seq, id)
}

// recordDeliveredBatch records the unverified messages serve is about to show or spool under accept_unverified, so a
// shell shows them with their label whatever its own setting. It runs after the lease check, the no-channel hold and
// the duplicate check: a message the worker naks unseen or holds unacknowledged is never recorded. A failure to record is logged and leaves the shell on its own setting.
func (e *Endpoint) recordDeliveredBatch(batch []pending) {
	if !e.cfg.AcceptUnverified {
		return
	}
	for _, p := range batch {
		if p.mail.Trust == sap.TrustVerified {
			continue
		}
		if err := e.recordDelivered(p.mail.Seq); err != nil {
			e.log.Printf("#%d: delivery of unverified mail not recorded: %v", p.mail.Seq, err)
		}
	}
}
