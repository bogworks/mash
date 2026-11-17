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
	// AskTTL is how long an ask stays in the table at the most.
	AskTTL = 24 * time.Hour
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

// askTableTemp is appended to the table file's path for its fixed temp file.
const askTableTemp = ".tmp"

// AskTablePath is the table file of endpoint name of owner under stateDir; "" keeps the table in memory only.
func AskTablePath(stateDir, owner, name string) string {
	if stateDir == "" || owner == "" || name == "" {
		return ""
	}
	return filepath.Join(stateDir, "asks", owner, name+".json")
}

// AskTable is the open-ask table: a small JSON file ({"asks":[...]}) rewritten atomically (the fixed temp name is the
// file's own name plus ".tmp", renamed over it), loaded when the table is opened so it survives a restart.
type AskTable struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	asks []OpenAsk
	seq  uint64
	// pending counts slots reserved by sends in flight (Reserve), so concurrent sends cannot all pass the limit.
	pending int
}

type askFile struct {
	Asks []OpenAsk `json:"asks"`
}

// OpenAskTable loads the table at path (missing is empty; unreadable or malformed starts empty, and the next write
// replaces it). Entries older than AskTTL are dropped.
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
	return t
}

func (t *AskTable) prune() {
	cut := t.now().Add(-AskTTL)
	kept := t.asks[:0]
	for _, a := range t.asks {
		if a.SentAt.After(cut) {
			kept = append(kept, a)
		}
	}
	t.asks = kept
}

// Len is the number of open asks.
func (t *AskTable) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	return len(t.asks)
}

// Asks is a copy of the open asks, oldest first.
func (t *AskTable) Asks() []OpenAsk {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	out := append([]OpenAsk(nil), t.asks...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt.Before(out[j].SentAt) })
	return out
}

// Check refuses a new ask when the open asks and the reserved slots fill the table.
func (t *AskTable) Check() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	return t.fullLocked()
}

func (t *AskTable) fullLocked() error {
	if len(t.asks)+t.pending >= MaxOpenAsks {
		return fmt.Errorf("mash: %d answer=now asks are already open (the limit); wait for a reply or for one to expire (24 hours), or send without answer=now", MaxOpenAsks)
	}
	return nil
}

// Reserve takes one slot atomically before a send, refusing when open asks plus reserved slots fill the table. The
// caller runs release once the send has failed, or after Add (release is idempotent).
func (t *AskTable) Reserve() (release func(), err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
// full (the send is already made; callers run Check first).
func (t *AskTable) Add(recipient, recipientKey string, receiptSeq uint64, within time.Duration) (OpenAsk, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	if len(t.asks) >= MaxOpenAsks {
		return OpenAsk{}, fmt.Errorf("mash: %d answer=now asks are already open", MaxOpenAsks)
	}
	now := t.now()
	t.seq++
	a := OpenAsk{ID: fmt.Sprintf("%d-%d", now.UnixNano(), t.seq), RecipientKey: recipientKey, Recipient: recipient, ReceiptSeq: receiptSeq,
		SentAt: now.UTC(), Deadline: now.Add(within).UTC()}
	t.asks = append(t.asks, a)
	return a, t.save()
}

// AnswerMatches implements ReplyWaker: whether an open ask is the one a message from userKey answers (its verified
// in-reply-to is the ask's receipt sequence). It changes nothing.
func (t *AskTable) AnswerMatches(userKey string, replyTo uint64) bool {
	if userKey == "" || replyTo == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	for _, a := range t.asks {
		if a.RecipientKey == userKey && a.ReceiptSeq == replyTo {
			return true
		}
	}
	return false
}

// CloseAnswered implements ReplyWaker: it closes the ask a message from userKey answers and reports whether there was
// one. Later replies find no ask and follow their own kind.
func (t *AskTable) CloseAnswered(userKey string, replyTo uint64) bool {
	if userKey == "" || replyTo == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune()
	for i, a := range t.asks {
		if a.RecipientKey == userKey && a.ReceiptSeq == replyTo {
			t.asks = append(t.asks[:i:i], t.asks[i+1:]...)
			_ = t.save() // the ask is closed in memory whatever the disk says
			return true
		}
	}
	return false
}

// closeAnswered closes the ask p answered, if it answered one: called when p is settled (durably spooled, or delivered
// by the channel), never before.
func (e *Endpoint) closeAnswered(p pending) {
	if p.askSeq == 0 {
		return
	}
	if w := e.replyWaker.Load(); w != nil {
		(*w).CloseAnswered(p.askUser, p.askSeq)
	}
}

// save writes the table: the fixed temp file, then a rename over the table file, both under its directory.
func (t *AskTable) save() error {
	if t.path == "" {
		return nil
	}
	b, err := json.Marshal(askFile{Asks: append([]OpenAsk{}, t.asks...)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	tmp := t.path + askTableTemp
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, t.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
