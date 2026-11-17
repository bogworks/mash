package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The open-ask table (peer-status design 6.3, option U2). A send marked answer=now is an instruction to this session's
// own mash and nothing on the wire: the table remembers the ask by the recipient's user key and the sequence the send
// acknowledged in the recipient's inbox, which is exactly what a verified reply names in its signed Mash-In-Reply-To
// (ADR 0063). The mail loop asks the table about each verified mail (ReplyWaker); the first match rings the session like
// an urgent mail whatever the reply's kind (ADR 0027); the ask closes once that reply is spooled or delivered.

const (
	// MaxOpenAsks is how many answer=now asks may be open at once; one more is refused before it is sent.
	MaxOpenAsks = 20
	// DefaultAnswerWithin and MaxAnswerWithin bound answer_within.
	DefaultAnswerWithin = 10 * time.Minute
	MaxAnswerWithin     = 24 * time.Hour
	// AskSettleGrace is how long past its deadline an ask stays in the table at the most: the deadline watcher settles an
	// expired ask within seconds of a serve running, and one still unsettled after this long (no serve ran, or its inbox
	// could not be read) is one it can no longer act on. A table never drops an expired ask before then.
	AskSettleGrace = 7 * 24 * time.Hour
)

// ReplyWaker is what the mail loop asks about a verified message whose signed in-reply-to verified. AnswerMatches
// reports, without changing anything, whether the message from the verified user key answers an open ask; CloseAnswered
// closes that ask, and the mail loop calls it only once the reply is durably spooled or delivered, so a reply that is
// nak'd and redelivered still matches (and keeps its wake) and the ask is never closed by a message nobody saw.
type ReplyWaker interface {
	AnswerMatches(userKey string, replyTo uint64) bool
	CloseAnswered(userKey string, replyTo uint64) bool
}

// SetReplyWaker installs the table of a serve (nil removes it).
func (e *Endpoint) SetReplyWaker(w ReplyWaker) {
	if w == nil {
		e.replyWaker.Store(nil)
		return
	}
	e.replyWaker.Store(&w)
}

// OpenAsk is one entry of the table.
type OpenAsk struct {
	ID           string    `json:"id"`
	RecipientKey string    `json:"recipient_key"` // the recipient's user key: what its verified reply's sender header names
	Recipient    string    `json:"recipient"`
	ReceiptSeq   uint64    `json:"receipt_seq"` // the sequence in the recipient's inbox
	SentAt       time.Time `json:"sent_at"`
	Deadline     time.Time `json:"deadline"` // sent_at + answer_within; stored here, acted on by the deadline wake
}

// askTableTemp is the suffix of the unique temp file a write makes beside the table file, and askTableLock is appended
// to the table file's path for the lock file that serialises writers (created, never removed).
const (
	askTableTemp = ".tmp"
	askTableLock = ".lock"
)

// AskTablePath is the table file of endpoint name of owner under stateDir; "" keeps the table in memory only.
func AskTablePath(stateDir, owner, name string) string {
	if stateDir == "" || owner == "" || name == "" {
		return ""
	}
	return filepath.Join(stateDir, "asks", owner, name+".json")
}

// AskTable is the open-ask table: a small JSON file ({"asks":[...]}) rewritten atomically (a unique temp file beside it,
// renamed over it, all under an flock on its ".lock" file), loaded when the table is opened so it survives a restart.
type AskTable struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	asks []OpenAsk
	seq  uint64
	// pending counts slots reserved by sends in flight (Reserve), so concurrent sends cannot all pass the limit.
	pending int
	// onDisk is the ids the file held when this table last read or wrote it: save compares the file against it to tell
	// what another writer added or closed since (see merge).
	onDisk map[string]bool
	// seen is the file's stat at that read or write: refresh skips the locked re-read while the file is unchanged.
	seen os.FileInfo
}

type askFile struct {
	Asks []OpenAsk `json:"asks"`
}

// OpenAskTable loads the table at path (missing is empty; unreadable or malformed starts empty, and the next write
// replaces it). Entries AskSettleGrace past their deadline are dropped.
func OpenAskTable(path string) *AskTable {
	t := &AskTable{path: path, now: time.Now}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var f askFile
			if json.Unmarshal(b, &f) == nil {
				t.asks = f.Asks
			}
		}
	}
	t.prune()
	t.onDisk = idsOf(t.asks)
	return t
}

// Reload re-reads the table file now (one locked refresh, even when the file looks unchanged), so a serve that waited
// for the endpoint lease sees the asks the previous holder recorded meanwhile. A missing, unreadable or malformed file
// keeps the current asks. Reserved slots (pending) are untouched.
func (t *AskTable) Reload() {
	if t == nil || t.path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen = nil
	t.refresh()
	t.prune()
}

func idsOf(asks []OpenAsk) map[string]bool {
	m := make(map[string]bool, len(asks))
	for _, a := range asks {
		m[a.ID] = true
	}
	return m
}

// prune drops the asks the watcher can no longer act on (AskSettleGrace past their deadline); an expired ask the watcher
// has not settled is kept.
func (t *AskTable) prune() {
	now := t.now()
	kept := t.asks[:0]
	for _, a := range t.asks {
		if a.Deadline.Add(AskSettleGrace).After(now) {
			kept = append(kept, a)
		}
	}
	t.asks = kept
}

// Len is the number of open asks.
func (t *AskTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	t.prune()
	return len(t.asks)
}

// Asks is a copy of the open asks, oldest first.
func (t *AskTable) Asks() []OpenAsk {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	t.prune()
	out := append([]OpenAsk(nil), t.asks...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt.Before(out[j].SentAt) })
	return out
}

// Check refuses a new ask when the open asks and the reserved slots fill the table.
func (t *AskTable) Check() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	t.prune()
	return t.fullLocked()
}

func (t *AskTable) fullLocked() error {
	if len(t.asks)+t.pending >= MaxOpenAsks {
		return fmt.Errorf("mash: %d answer=now asks are already open (the limit); wait for a reply or for one to be settled at its deadline, or send without answer=now", MaxOpenAsks)
	}
	return nil
}

// Reserve takes one slot atomically before a send, refusing when open asks plus reserved slots fill the table. The
// caller runs release once the send has failed, or after Add (release is idempotent).
func (t *AskTable) Reserve() (release func(), err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	t.prune()
	if err := t.fullLocked(); err != nil {
		return nil, err
	}
	t.pending++
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			t.pending--
			t.mu.Unlock()
		})
	}, nil
}

// ParseAnswerWithin reads the send tool's answer and answer_within: ok is false when the send is not an answer=now one.
func ParseAnswerWithin(answer, within string) (d time.Duration, ok bool, err error) {
	switch answer {
	case "":
		if within != "" {
			return 0, false, errors.New("mash: answer_within needs answer=now")
		}
		return 0, false, nil
	case "now":
	default:
		return 0, false, fmt.Errorf("mash: answer must be \"now\" or absent, got %q", answer)
	}
	d = DefaultAnswerWithin
	if within != "" {
		var err error
		if d, err = time.ParseDuration(within); err != nil {
			return 0, false, fmt.Errorf("mash: answer_within %q is not a duration (for example 10m): %v", within, err)
		}
		if d <= 0 || d > MaxAnswerWithin {
			return 0, false, fmt.Errorf("mash: answer_within %q is out of range: it must be above 0 and at most %s", within, MaxAnswerWithin)
		}
	}
	return d, true, nil
}

// Add records an ask for the message the send's receipt acknowledged and persists the table. It fails when the table is
// full (the send is already made; callers run Check first). It is idempotent on (recipientKey, receiptSeq): an open
// ask with that pair is returned as it is (not duplicated, deadline not extended, no slot taken), so a retried send
// whose receipt was a duplicate can record its ask safely.
func (t *AskTable) Add(recipient, recipientKey string, receiptSeq uint64, within time.Duration) (OpenAsk, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var a OpenAsk
	var full error
	err := t.update(func() bool {
		t.prune()
		for _, e := range t.asks {
			if e.RecipientKey == recipientKey && e.ReceiptSeq == receiptSeq {
				a = e
				return false
			}
		}
		if len(t.asks) >= MaxOpenAsks {
			full = fmt.Errorf("mash: %d answer=now asks are already open", MaxOpenAsks)
			return false
		}
		now := t.now()
		t.seq++
		a = OpenAsk{ID: fmt.Sprintf("%d-%d", now.UnixNano(), t.seq), RecipientKey: recipientKey, Recipient: recipient, ReceiptSeq: receiptSeq,
			SentAt: now.UTC(), Deadline: now.Add(within).UTC()}
		t.asks = append(t.asks, a)
		return true
	})
	if full != nil {
		return OpenAsk{}, full
	}
	return a, err
}

// AnswerMatches implements ReplyWaker: whether an open ask is the one a message from userKey answers (its verified
// in-reply-to is the ask's receipt sequence). It changes nothing; the table is refreshed from the file first, so an ask
// another serve of the session added since this one loaded it is matched too.
func (t *AskTable) AnswerMatches(userKey string, replyTo uint64) bool {
	if userKey == "" || replyTo == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refresh()
	t.prune()
	for _, a := range t.asks {
		if a.RecipientKey == userKey && a.ReceiptSeq == replyTo {
			return true
		}
	}
	return false
}

// CloseAnswered implements ReplyWaker: it closes the ask a message from userKey answers and reports whether there was
// one. Later replies find no ask and follow their own kind. The close is one locked refresh, close and save.
func (t *AskTable) CloseAnswered(userKey string, replyTo uint64) bool {
	if userKey == "" || replyTo == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeWhere(func(a OpenAsk) bool { return a.RecipientKey == userKey && a.ReceiptSeq == replyTo })
}

// closeAnswered closes the ask p answered, if it answered one: called when p is settled (shown by a hook and acked, or
// delivered by the channel), never before.
func (e *Endpoint) closeAnswered(p pending) {
	if p.askSeq == 0 {
		return
	}
	if w := e.replyWaker.Load(); w != nil {
		(*w).CloseAnswered(p.askUser, p.askSeq)
	}
}

// closeWhere removes the first ask match accepts, in one locked read-merge-write of the file; it reports whether there
// was one. The ask is closed in memory whatever the disk says. Hold t.mu.
func (t *AskTable) closeWhere(match func(OpenAsk) bool) bool {
	found := false
	_ = t.update(func() bool {
		t.prune()
		for i, a := range t.asks {
			if match(a) {
				t.asks = append(t.asks[:i:i], t.asks[i+1:]...)
				found = true
				return true
			}
		}
		return false
	})
	return found
}

// lockFile takes the exclusive lock of the table's lock file and returns what releases it. The directory is created.
func (t *AskTable) lockFile() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return nil, err
	}
	lk, err := os.OpenFile(t.path+askTableLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockAskFile(lk); err != nil {
		lk.Close()
		return nil, err
	}
	return func() { unlockAskFile(lk); lk.Close() }, nil
}

// update is one locked read-modify-write of the table: under the file's exclusive lock it merges what the file holds
// (see merge), runs mutate on t.asks, and, when mutate reports a change, writes the table back before the lock is
// released, so no other writer's change falls between the read and the write. When the file cannot be locked mutate
// still runs on the memory table and the error is returned. Hold t.mu.
func (t *AskTable) update(mutate func() bool) error {
	if t.path == "" {
		mutate()
		return nil
	}
	unlock, err := t.lockFile()
	if err != nil {
		mutate()
		return err
	}
	defer unlock()
	t.merge()
	if !mutate() {
		return nil
	}
	return t.write()
}

// refresh folds what another writer did to the file into t.asks before a read, under the file's lock. It does nothing
// while the file is the one this table last read or wrote (same file, size and modification time), when there is no file,
// or when the file cannot be locked. Hold t.mu.
func (t *AskTable) refresh() {
	if t.path == "" {
		return
	}
	fi, err := os.Stat(t.path)
	if err != nil || (t.seen != nil && os.SameFile(t.seen, fi) && t.seen.Size() == fi.Size() && t.seen.ModTime().Equal(fi.ModTime())) {
		return
	}
	unlock, err := t.lockFile()
	if err != nil {
		return
	}
	defer unlock()
	t.merge()
}

// write writes t.asks to a unique temp file in the table's directory and renames it over the table file. The caller
// holds the file's lock. Hold t.mu.
func (t *AskTable) write() error {
	dir := filepath.Dir(t.path)
	b, err := json.Marshal(askFile{Asks: append([]OpenAsk{}, t.asks...)})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(t.path)+".*"+askTableTemp)
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	if err := os.Rename(tmp, t.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	t.onDisk = idsOf(t.asks)
	t.seen, _ = os.Stat(t.path)
	return nil
}

// merge folds what another writer did to the file since this table last read or wrote it into t.asks, so a write never
// overwrites an entry this table did not load: an entry on disk this table has not seen (added by another table of the
// same file, as when an older serve of the session still runs beside a newer one) is adopted; one this table saw on
// disk that is gone now (closed by the other writer) is dropped, not written back. Entries only this table has stay. The
// caller holds the file's lock, so this read and any write after it are one step to every other writer. A missing or
// malformed file changes nothing. Hold t.mu.
func (t *AskTable) merge() {
	fi, _ := os.Stat(t.path)
	b, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	var f askFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	disk := idsOf(f.Asks)
	kept := t.asks[:0:0]
	for _, a := range t.asks {
		if t.onDisk[a.ID] && !disk[a.ID] {
			continue // closed elsewhere
		}
		kept = append(kept, a)
	}
	have := idsOf(kept)
	for _, a := range f.Asks {
		if !t.onDisk[a.ID] && !have[a.ID] {
			kept = append(kept, a)
		}
	}
	t.asks = kept
	t.onDisk = disk
	t.seen = fi
	t.prune()
}

// Expired is a copy of the open asks whose deadline is at or before now, oldest first. It never drops one: an expired ask
// stays until the watcher settles it (Close) or AskSettleGrace has passed. Asks refreshes the table from the file first.
func (t *AskTable) Expired(now time.Time) []OpenAsk {
	var out []OpenAsk
	for _, a := range t.Asks() {
		if !a.Deadline.After(now) {
			out = append(out, a)
		}
	}
	return out
}

// Close removes the ask with id, reporting whether it was still open (a reply that closed it first, here or in another
// serve of the session, makes this false).
func (t *AskTable) Close(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeWhere(func(a OpenAsk) bool { return a.ID == id })
}
