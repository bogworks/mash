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
const refusedSeqsFile = "refused-unverified.seqs"

// refusedSeqsHeader opens the sequences file: the identity of the inbox stream the sequences belong to (its name and
// creation time, as the spool binds to). Stream sequences restart when a stream is recreated, so a file of another
// stream's identity is stale: its sequences and the count beside it are discarded, never applied to the new stream.
const refusedSeqsHeader = "stream "

// refusedSeqsKeep is the size at which the file is pruned: past twice that, the sequences below the inbox stream's
// first sequence (gone from the stream) are dropped. A sequence the stream still holds is never dropped.
var refusedSeqsKeep = 4096

// refusedMu serialises the writers of the count and the sequence list, so concurrent arrivals lose no count.
var refusedMu sync.Mutex

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
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return false
	}
	id, seqs := readRefusedSeqs(filepath.Join(dir, refusedSeqsFile))
	return id == stream && slices.Contains(seqs, seq)
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

// writeRefusedSeqs replaces the sequences file with the stream identity id and seqs.
func writeRefusedSeqs(p, id string, seqs []uint64) error {
	var b strings.Builder
	b.WriteString(refusedSeqsHeader + id + "\n")
	for _, s := range seqs {
		b.WriteString(strconv.FormatUint(s, 10) + "\n")
	}
	return replaceFile(p, []byte(b.String()))
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
	fid, seqs := readRefusedSeqs(p)
	if fid == id && slices.Contains(seqs, seq) {
		return RefusedCount(e.cfg), false, nil
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
				if err := writeRefusedSeqs(p, id, append(kept, seq)); err != nil {
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
	n = RefusedCount(e.cfg) + 1
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
// sequences.
func readRefusedSeqs(path string) (string, []uint64) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	var out []uint64
	id := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), refusedSeqsHeader); ok {
			id = strings.TrimSpace(rest)
			continue
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(sc.Text()), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return id, out
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
