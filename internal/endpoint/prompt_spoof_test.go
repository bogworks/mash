package endpoint

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

const nuidDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// nextNUIDs lists the NUID tokens that can follow tok (12-char prefix + 10-char base62 sequence, step 33..333).
func nextNUIDs(tok string, from, to int) []string {
	if len(tok) != 22 {
		return nil
	}
	var seq int64
	for _, c := range tok[12:] {
		seq = seq*62 + int64(strings.IndexRune(nuidDigits, c))
	}
	var out []string
	for k := from; k <= to; k++ {
		n, b := seq+int64(k), make([]byte, 10)
		for i := 9; i >= 0; i-- {
			b[i], n = nuidDigits[n%62], n/62
		}
		out = append(out, tok[:12]+string(b))
	}
	return out
}

// TestPeerCannotSpoofPromptAnswer: endpoint c, same owner, sees b's discovery request and its reply subject on $SRV.>,
// and may publish to _INBOX.>; with NUID inboxes it guesses b's prompt reply inbox and answers in a's place.
func TestPeerCannotSpoofPromptAnswer(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	release := make(chan struct{})
	f.serve(t, "a", func(ep *Endpoint, p PromptEvent) {
		<-release
		_ = ep.Reply(p.RequestID, "genuine", true)
	})
	b, c := f.client(t, "b"), f.client(t, "c")

	spy, err := c.Conn().SubscribeSync("$SRV.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Conn().Flush(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			m, err := spy.NextMsg(100 * time.Millisecond)
			select {
			case <-stop:
				return
			default:
			}
			if err != nil || !strings.HasPrefix(m.Subject, "$SRV.INFO") || m.Reply == "" {
				continue
			}
			dot := strings.LastIndex(m.Reply, ".")
			prefix, tok := m.Reply[:dot], m.Reply[dot+1:]
			evil := []byte(`{"type":"response","data":"EVIL"}`)
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
				select {
				case <-stop:
					return
				default:
				}
				for _, n := range nextNUIDs(tok, 1, 700) {
					_ = c.Conn().Publish(prefix+"."+n, evil)
					_ = c.Conn().Publish(prefix+"."+n, nil)
				}
				_ = c.Conn().Flush()
			}
		}
	}()

	subj, err := Discover(context.Background(), b.Conn(), "o", "a")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go func() { time.Sleep(3500 * time.Millisecond); close(release) }()
	_ = Prompt(ctx, b.Conn(), b.Identity(), "b", subj, "ping", &out)
	if strings.Contains(out.String(), "EVIL") {
		t.Fatalf("a peer spoofed the prompt answer: %q", out.String())
	}
}

// TestPromptInboxesUnguessable: reply inboxes are random, not NUIDs: consecutive ones share only the endpoint prefix.
func TestPromptInboxesUnguessable(t *testing.T) {
	f := newFixture(t, "a")
	nc := f.client(t, "a").Conn()
	x, y := secretInbox(nc), secretInbox(nc)
	pre := nc.NewInbox()
	pre = pre[:strings.LastIndex(pre, ".")+1]
	if !strings.HasPrefix(x, pre) || !strings.HasPrefix(y, pre) {
		t.Fatalf("inbox outside the endpoint prefix %q: %q %q", pre, x, y)
	}
	tx, ty := x[len(pre):], y[len(pre):]
	if len(tx) < 32 || tx[:8] == ty[:8] {
		t.Fatalf("inboxes look sequential: %q %q", tx, ty)
	}
}
