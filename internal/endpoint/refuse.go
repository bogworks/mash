package endpoint

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

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

// refusedSeqsKeep is how many sequences the file keeps: past twice that it is rewritten to the newest.
const refusedSeqsKeep = 4096

// refusedMu serialises the writers of the count and the sequence list, so concurrent arrivals lose no count.
var refusedMu sync.Mutex

// refuses reports whether mail of this trust is refused: accept_unverified is off and the trust is not verified.
func (e *Endpoint) refuses(t sap.Trust) bool {
	return !e.cfg.AcceptUnverified && t != sap.TrustVerified
}

// refuse records one refused message: the count (RefusedFile, replaced whole), the sequence and one line on the log
// without the body. A failure to write is logged; it never stops the mail from being dropped.
func (e *Endpoint) refuse(seq uint64, from string, trust sap.Trust) {
	n, err := e.recordRefusal(seq)
	if err != nil {
		e.log.Printf("refused unverified mail #%d from %s (%s); the count could not be kept: %v", seq, from, trust, err)
		return
	}
	e.log.Printf("refused unverified mail #%d from %s (%s); %d refused in all", seq, from, trust, n)
}

func (e *Endpoint) recordRefusal(seq uint64) (int, error) {
	if e.cfg.StateDir == "" {
		return 0, errors.New("no state directory")
	}
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return 0, err
	}
	refusedMu.Lock()
	defer refusedMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	n := RefusedCount(e.cfg) + 1
	if err := replaceFile(filepath.Join(dir, RefusedFile), []byte(strconv.Itoa(n)+"\n")); err != nil {
		return 0, err
	}
	// The sequence list is best effort: the count is the record.
	p := filepath.Join(dir, refusedSeqsFile)
	seqs := readRefusedSeqs(p)
	seqs = append(seqs, seq)
	if len(seqs) > 2*refusedSeqsKeep {
		seqs = seqs[len(seqs)-refusedSeqsKeep:]
		var b strings.Builder
		for _, s := range seqs {
			b.WriteString(strconv.FormatUint(s, 10) + "\n")
		}
		_ = replaceFile(p, []byte(b.String()))
	} else if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = fmt.Fprintf(f, "%d\n", seq)
		_ = f.Close()
	}
	return n, nil
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

func readRefusedSeqs(path string) []uint64 {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n, err := strconv.ParseUint(strings.TrimSpace(sc.Text()), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// refusedStored is how many of the refused messages the inbox stream, whose sequences run from first to last, may still
// hold. Zero when accept_unverified is set.
func (e *Endpoint) refusedStored(first, last uint64) (stored int) {
	if e.cfg.AcceptUnverified {
		return 0
	}
	dir, err := SpoolDir(e.cfg)
	if err != nil {
		return 0
	}
	seen := map[uint64]bool{}
	for _, s := range readRefusedSeqs(filepath.Join(dir, refusedSeqsFile)) {
		if s >= first && s <= last && !seen[s] {
			seen[s] = true
		}
	}
	return len(seen)
}
