package spool

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The legacy-lease epoch. An endpoint that reads a legacy stream beside its own does so only while it holds the legacy
// identity's lease. The serve records the epoch of that hold (a number that differs for every hold) in .legacy-epoch,
// or "none" while it does not hold it, and stamps each legacy entry it spools with the epoch current when it wrote it.
// A hook shows a stamped entry only while the file still names that epoch, so mail spooled just before the lease was
// lost is never shown by the old session while the new holder is redelivered it.
const epochFile = ".legacy-epoch"

// NoteEpoch records the current epoch; 0 records "none" (the legacy lease is not held). Call it under Lock.
func (s Spool) NoteEpoch(epoch uint64) error {
	v := "none"
	if epoch != 0 {
		v = strconv.FormatUint(epoch, 10)
	}
	return s.writeOrRemove(epochFile, v)
}

// Epoch is the recorded epoch. ok is false when there is no readable record; epoch is 0 for "none".
func (s Spool) Epoch() (epoch uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(s.Dir, epochFile))
	if err != nil {
		return 0, false
	}
	v := strings.TrimSpace(string(b))
	if v == "none" {
		return 0, true
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	return n, true
}

// visible reports whether a hook may show e given the current epoch: an entry with no epoch (an own-stream entry, or one
// of an older spool) always; a stamped entry only while the epoch file names its epoch (a missing file fails closed).
func (e Entry) visible(cur uint64, ok bool) bool {
	return e.Epoch == 0 || (ok && cur == e.Epoch)
}
