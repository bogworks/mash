package discover

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

// MaxPromptBytes is the most bytes of text a live prompt may carry, as the endpoint's constant of the same name.
const MaxPromptBytes = 32 << 10

// PromptTooLargeCode is the stable code of a refused oversized prompt, as the endpoint's constant of the same name.
const PromptTooLargeCode = "413"

// PromptTooLargeText is the description that follows the code, word for word as the endpoint's.
var PromptTooLargeText = fmt.Sprintf("prompt_too_large: a live prompt is at most %d bytes; send the longer text as mail", MaxPromptBytes)

// DefaultWindow is the longest a discovery waits when no window is given (main's 2 s).
const DefaultWindow = 2 * time.Second

// SettleGap is how long a discovery that has already found the target waits for a further answer before it ends. It
// keeps counting invalid answers that arrive a little after the valid one (TO-12), and is cut short by the window.
const SettleGap = 500 * time.Millisecond

// idleLimit is the longest a prompt waits for a reply chunk, as main's.
const idleLimit = 60 * time.Second

// Config is everything a Caller needs.
type Config struct {
	NC        *nats.Conn  // the asker's own connection
	Asker     sap.Address // the caller's own endpoint address; its InboxPrefix is the reply prefix
	Creds     []byte      // contents of the asker's creds file (the key that signs)
	Name      string      // display name in Agent-Sender: the asker's flat key, as main passes it
	Directory Directory   // every endpoint of the mesh
	Window    time.Duration

	// Now and Nonce fix the signed header's clock and nonce (REQ-015 deterministic inputs). Nil Now means time.Now;
	// an empty Nonce means a fresh one for each prompt.
	Now   func() time.Time
	Nonce string
}

// Caller discovers endpoints and prompts them. It is safe for concurrent use.
type Caller struct {
	cfg      Config
	id       sapsdk.Identity
	clientID string // the one token shared by all of this caller's reply subjects

	notVerified atomic.Int64

	mu      sync.Mutex
	replies []string
}

var tokenEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// randToken is n lowercase base32 characters from crypto/rand. It is read at the time of the call, never at init.
func randToken(n int) string {
	b := make([]byte, (n*5+7)/8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return strings.ToLower(tokenEnc.EncodeToString(b))[:n]
}

const (
	clientIDLen = 16 // 80 bits; shared, so it is not what makes a reply subject unguessable
	routeLen    = 26 // 130 bits, fresh for each request
)

// New builds a Caller. The client-id token is drawn here, from crypto/rand.
func New(cfg Config) (*Caller, error) {
	if cfg.NC == nil {
		return nil, errors.New("discover: no connection")
	}
	id, err := sapsdk.IdentityFromCredsAt("creds", cfg.Creds)
	if err != nil {
		return nil, err
	}
	return &Caller{cfg: cfg, id: id, clientID: randToken(clientIDLen)}, nil
}

// replySubject is _INBOX.<owner>.<flat>.<client-id>.<route> with a fresh route token (REQ-015).
func (c *Caller) replySubject() string {
	s := c.cfg.Asker.InboxPrefix() + "." + c.clientID + "." + randToken(routeLen)
	c.mu.Lock()
	c.replies = append(c.replies, s)
	c.mu.Unlock()
	return s
}

// ReplySubjects lists the reply subjects used so far, in order of use.
func (c *Caller) ReplySubjects() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.replies...)
}

// NotVerified is the cumulative count of answers rejected as not verified (TO-12's total).
func (c *Caller) NotVerified() int { return int(c.notVerified.Load()) }

// Close ends the caller. It does not close the connection.
func (c *Caller) Close() error { return nil }

// Discovery is the result of one discovery.
type Discovery struct {
	Accepted    []Registration // verified registrations of the target, nothing else
	NotVerified int            // answers rejected in this discovery as not verified, each counted once
}

// Discover publishes one $SRV.INFO.agents request and returns the verified registrations of target that answered
// within the window, and how many answers were rejected as not verified. An invalid answer never ends the discovery
// early or fails it. Nothing found is an empty Accepted and a nil error.
func (c *Caller) Discover(ctx context.Context, target sap.Address) (Discovery, error) {
	window := c.cfg.Window
	if window <= 0 {
		window = DefaultWindow
	}
	sub, err := c.cfg.NC.SubscribeSync(c.replySubject())
	if err != nil {
		return Discovery{}, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	// the subject of the subscription is the last reply subject used
	reply := sub.Subject
	if err := c.cfg.NC.PublishRequest("$SRV.INFO."+sap.ServiceName, reply, nil); err != nil {
		return Discovery{}, err
	}
	col := NewCollector(c.cfg.Directory, target)
	deadline := time.Now().Add(window)
	for {
		wait := time.Until(deadline)
		if len(col.Accepted) > 0 && wait > SettleGap {
			wait = SettleGap
		}
		if wait <= 0 {
			break
		}
		wctx, cancel := context.WithTimeout(ctx, wait)
		m, err := sub.NextMsgWithContext(wctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return Discovery{}, ctx.Err()
			}
			break // the window, or the quiet gap after the target was found, is over
		}
		col.Add(m.Data)
	}
	c.notVerified.Add(int64(col.NotVerified))
	return Discovery{Accepted: col.Accepted, NotVerified: col.NotVerified}, nil
}

// Prompt discovers target, then prompts the verified registration's prompt subject with text and returns the text of
// each response chunk in order. A text over MaxPromptBytes is refused before anything is published. An unverified or
// absent target is an error and nothing is published to any prompt subject.
func (c *Caller) Prompt(ctx context.Context, target sap.Address, text string) ([]string, error) {
	if len(text) > MaxPromptBytes {
		return nil, fmt.Errorf("mash: prompt refused: %s %s (this one is %d)", PromptTooLargeCode, PromptTooLargeText, len(text))
	}
	found, err := c.Discover(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(found.Accepted) == 0 {
		return nil, fmt.Errorf("mash: %s is not online (no SAP registration answered)", sap.Display(target.Flat()))
	}
	subject := found.Accepted[0].PromptSubject

	sub, err := c.cfg.NC.SubscribeSync(c.replySubject())
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	m, err := c.promptMsg(subject, sub.Subject, text)
	if err != nil {
		return nil, err
	}
	if err := c.cfg.NC.PublishMsg(m); err != nil {
		return nil, err
	}
	var chunks []string
	for {
		wait := idleLimit
		if d, ok := ctx.Deadline(); ok && time.Until(d) < wait {
			wait = time.Until(d)
		}
		r, err := sub.NextMsg(wait)
		if err != nil {
			return chunks, fmt.Errorf("mash: prompt: no reply chunk within %s: %w", wait, err)
		}
		if code := r.Header.Get("Nats-Service-Error-Code"); code != "" {
			return chunks, fmt.Errorf("mash: prompt refused: %s %s", code, r.Header.Get("Nats-Service-Error"))
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			return chunks, nil
		}
		var ch struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(r.Data, &ch) != nil || ch.Type != "response" {
			continue // status chunks and unknown types are ignored (SAP 6.6)
		}
		var s string
		if json.Unmarshal(ch.Data, &s) != nil {
			var obj struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(ch.Data, &obj)
			s = obj.Text
		}
		chunks = append(chunks, s)
	}
}

// promptMsg is the signed request a prompt sends: the text as the bare payload (SAP 0.3 section 5.1) and the
// Agent-Sender header, as the endpoint's promptMsg builds it.
func (c *Caller) promptMsg(subject, reply, text string) (*nats.Msg, error) {
	payload := []byte(text)
	now := time.Now()
	if c.cfg.Now != nil {
		now = c.cfg.Now()
	}
	h, err := c.id.Sign(subject, payload, c.cfg.Name, now, c.cfg.Nonce)
	if err != nil {
		return nil, err
	}
	m := nats.NewMsg(subject)
	m.Reply = reply
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	return m, nil
}
