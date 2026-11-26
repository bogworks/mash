package endpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// AT-018 (REQ-018): stored-mail duplicates and replays behave as they do on main. This test is the oracle: it uses only
// API present both on main and on a branch that changes the stored-mail path, records what each case does in a JSON file
// (MASH_AT018_OUT), and passes on its own. scripts/storedmail/at006.sh runs it on a copy of main first and on the
// branch second and fails unless the two files are identical. The cases are:
//   a1-a4  a copy of a signed message republished to the inbox subject (with and without the sender's Nats-Msg-Id, after
//          the stream's duplicate window, and stored before the endpoint starts);
//   b1-b2  a redelivery: the acknowledgement lost on the wire, and a failed write that naks;
//   c1-c2  a restart of the endpoint with mail already shown, the mail acked and the ack lost.
// These start an in-process NATS server: run them in a container (scripts/storedmail/at006.sh).

// dupObs is one case's observable outcome. Nothing in it depends on timing, a key or a clock.
type dupObs struct {
	Delivered         map[string]int `json:"delivered"`          // body text -> how many times it was shown
	Shown             []string       `json:"shown"`              // every message shown, as text, the time removed, sorted
	RefusedUnverified []string       `json:"refused_unverified"` // entry lines of the unverified list
	RefusedMalformed  []string       `json:"refused_malformed"`  // entry lines of the malformed list (absent before it exists)
	StreamMessages    uint64         `json:"stream_messages"`
	AckFloor          uint64         `json:"ack_floor"`
	Detail            map[string]any `json:"detail,omitempty"`
}

type dupRec struct {
	mu    sync.Mutex
	texts []string
	fail  atomic.Int32 // fail this many deliveries first
}

func (r *dupRec) deliver(text string, _ map[string]string) error {
	if r.fail.Load() > 0 {
		r.fail.Add(-1)
		return os.ErrClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	return nil
}

var (
	dupHead = regexp.MustCompile(`(?m)^--- #`)
	dupTime = regexp.MustCompile(`(?m)^(--- #\d+ from .* \[[^\]]+\]) at \S+$`)
	dupInst = regexp.MustCompile(`(?m)^instance: \S+$`)
)

// blocks are the messages shown, one string each, with the time and the sender's instance id taken out.
func (r *dupRec) blocks() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, t := range r.texts {
		idx := dupHead.FindAllStringIndex(t, -1)
		for i, ix := range idx {
			end := len(t)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			b := dupTime.ReplaceAllString(t[ix[0]:end], "$1 at T")
			out = append(out, strings.TrimRight(dupInst.ReplaceAllString(b, "instance: I"), "\n"))
		}
	}
	return out
}

// bodyOf is the first quoted line of a block: every body here is one line.
func dupBodyOf(block string) string {
	for _, l := range strings.Split(block, "\n") {
		if rest, ok := strings.CutPrefix(l, "> "); ok {
			return rest
		}
	}
	return ""
}

// dupAckProxy forwards to a NATS server and, while armed, drops a client's JetStream acknowledgements (PUBs to
// $JS.ACK.>): the server never learns the message was handled, as with a lost ack. It parses only the client's PUB and
// HPUB framing and forwards every other byte untouched.
type dupAckProxy struct {
	ln      net.Listener
	target  string
	drop    atomic.Int32 // acknowledgements still to drop
	dropped atomic.Int32
}

func newDupAckProxy(t *testing.T, natsURL string) *dupAckProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &dupAckProxy{ln: ln, target: strings.TrimPrefix(natsURL, "nats://")}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return p
}

func (p *dupAckProxy) URL() string { return "nats://" + p.ln.Addr().String() }

func (p *dupAckProxy) handle(cl net.Conn) {
	sv, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = cl.Close()
		return
	}
	go func() { _, _ = io.Copy(cl, sv); _ = cl.Close() }()
	defer sv.Close()
	br := bufio.NewReader(cl)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var payload []byte
		keep := true
		if strings.HasPrefix(string(line), "PUB ") || strings.HasPrefix(string(line), "HPUB ") {
			f := strings.Fields(strings.TrimSpace(string(line)))
			n, err := strconv.Atoi(f[len(f)-1])
			if err != nil {
				return
			}
			payload = make([]byte, n+2)
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			if strings.HasPrefix(f[1], "$JS.ACK.") && p.drop.Load() > 0 {
				p.drop.Add(-1)
				p.dropped.Add(1)
				keep = false
			}
		}
		if keep {
			if _, err := sv.Write(append(line, payload...)); err != nil {
				return
			}
		}
	}
}

// dupWorld is a fixture with a and b, a state dir for a, and the consumer's ack wait shortened so a lost ack is
// redelivered inside the test.
type dupWorld struct {
	f     *fixture
	state string
	js    jetstream.JetStream
}

func newDupWorld(t *testing.T, ackWait time.Duration) *dupWorld {
	t.Helper()
	f := newFixture(t, "a", "b")
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	if ackWait > 0 {
		c, err := js.Consumer(context.Background(), taddr("a").InboxStream(), store.Consumer)
		if err != nil {
			t.Fatal(err)
		}
		cfg := c.CachedInfo().Config
		cfg.AckWait = ackWait
		if _, err := js.UpdateConsumer(context.Background(), taddr("a").InboxStream(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	return &dupWorld{f: f, state: t.TempDir(), js: js}
}

func (w *dupWorld) cfg(url string) config.Config {
	c := w.f.cfg("a")
	c.StateDir, c.AcceptUnverified = w.state, false
	if url != "" {
		c.URL = url
	}
	return c
}

type dupServe struct {
	ep   *Endpoint
	rec  *dupRec
	stop func()
}

func (w *dupWorld) serve(t *testing.T, cfg config.Config, window time.Duration) *dupServe {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ep.duplicateWindow = window
	rec := &dupRec{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, rec.deliver); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; ep.Close() }) }
	t.Cleanup(stop)
	return &dupServe{ep: ep, rec: rec, stop: stop}
}

// shownAtLeast waits until n messages are shown or d passes.
func (s *dupServe) shownAtLeast(n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(s.rec.blocks()) >= n {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func dupEntries(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && l[0] >= '0' && l[0] <= '9' {
			out = append(out, l)
		}
	}
	return out
}

// observe stops the serves, then records what the case did. extra serves are included in Delivered and Shown.
func (w *dupWorld) observe(t *testing.T, detail map[string]any, serves ...*dupServe) dupObs {
	t.Helper()
	for _, s := range serves {
		s.stop()
	}
	o := dupObs{Delivered: map[string]int{}, Shown: []string{}, Detail: detail}
	for _, s := range serves {
		for _, b := range s.rec.blocks() {
			o.Delivered[dupBodyOf(b)]++
			o.Shown = append(o.Shown, b)
		}
	}
	sort.Strings(o.Shown)
	dir, err := SpoolDir(w.cfg(""))
	if err != nil {
		t.Fatal(err)
	}
	o.RefusedUnverified = dupEntries(filepath.Join(dir, "refused-unverified.seqs"))
	o.RefusedMalformed = dupEntries(filepath.Join(dir, "refused-malformed.seqs"))
	ctx := context.Background()
	st, err := w.js.Stream(ctx, taddr("a").InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o.StreamMessages = info.State.Msgs
	c, err := st.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o.AckFloor = ci.AckFloor.Stream
	return o
}

// capture is the exact subject, headers and body of stored message seq, as a copy would publish them.
func (w *dupWorld) capture(t *testing.T, seq uint64, withID bool) *nats.Msg {
	t.Helper()
	st, err := w.js.Stream(context.Background(), taddr("a").InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := st.GetMsg(context.Background(), seq)
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(raw.Subject)
	m.Data = append([]byte(nil), raw.Data...)
	for k, v := range raw.Header {
		if strings.HasPrefix(k, "Nats-") && k != nats.MsgIdHdr { // the server's own stamps, not the publisher's
			continue
		}
		m.Header[k] = append([]string(nil), v...)
	}
	if !withID {
		m.Header.Del(nats.MsgIdHdr)
	}
	return m
}

// copyIn republishes m as the sender b would and returns whether the stream dropped it as a duplicate.
func (w *dupWorld) copyIn(t *testing.T, m *nats.Msg) bool {
	t.Helper()
	pub, err := jetstream.New(w.f.e.ConnAs(t, w.f.creds["b"], tkey("b")))
	if err != nil {
		t.Fatal(err)
	}
	ack, err := pub.PublishMsg(context.Background(), m)
	if err != nil {
		t.Fatalf("copy publish: %v", err)
	}
	return ack.Duplicate
}

func (w *dupWorld) send(t *testing.T, body string) uint64 {
	t.Helper()
	r, err := w.f.client(t, "b").SendDetailed(context.Background(), tdot("a"), body, SendOptions{})
	if err != nil || r.Duplicate {
		t.Fatalf("send %q: %+v %v", body, r, err)
	}
	return r.Seq
}

func (o dupObs) require(t *testing.T, body string, n int) {
	t.Helper()
	if o.Delivered[body] != n {
		t.Errorf("%q shown %d times, want %d (all: %v)", body, o.Delivered[body], n, o.Delivered)
	}
}

func TestStoredDuplicateOracle(t *testing.T) {
	results := map[string]dupObs{}
	var mu sync.Mutex
	record := func(name string, o dupObs) { mu.Lock(); results[name] = o; mu.Unlock() }

	// A copy of a signed message, with and without the sender's Nats-Msg-Id, while the endpoint serves.
	for _, withID := range []bool{true, false} {
		name := "a2-copy-without-msg-id-while-serving"
		if withID {
			name = "a1-copy-with-msg-id-while-serving"
		}
		t.Run(name, func(t *testing.T) {
			w := newDupWorld(t, 0)
			seq := w.send(t, "orig")
			s := w.serve(t, w.cfg(""), 0)
			if !s.shownAtLeast(1, 15*time.Second) {
				t.Fatal("the original was not shown")
			}
			dup := w.copyIn(t, w.capture(t, seq, withID))
			time.Sleep(2500 * time.Millisecond)
			o := w.observe(t, map[string]any{"copy_dropped_by_stream": dup}, s)
			o.require(t, "orig", 1)
			record(name, o)
		})
	}

	// A copy stored after the stream's duplicate window (shortened), inside the signature age.
	t.Run("a3-copy-after-the-duplicate-window", func(t *testing.T) {
		w := newDupWorld(t, 0)
		w.f.dup = time.Second
		w.f.shortenWindow(t, "a")
		seq := w.send(t, "orig")
		s := w.serve(t, w.cfg(""), w.f.dup)
		if !s.shownAtLeast(1, 15*time.Second) {
			t.Fatal("the original was not shown")
		}
		time.Sleep(2500 * time.Millisecond)
		dup := w.copyIn(t, w.capture(t, seq, true))
		time.Sleep(2500 * time.Millisecond)
		o := w.observe(t, map[string]any{"copy_dropped_by_stream": dup}, s)
		o.require(t, "orig", 1)
		record("a3-copy-after-the-duplicate-window", o)
	})

	// The original and its copy are both stored before the endpoint starts.
	t.Run("a4-copy-stored-before-the-start", func(t *testing.T) {
		w := newDupWorld(t, 0)
		seq := w.send(t, "orig")
		dup := w.copyIn(t, w.capture(t, seq, false))
		s := w.serve(t, w.cfg(""), 0)
		s.shownAtLeast(1, 15*time.Second)
		time.Sleep(2500 * time.Millisecond)
		o := w.observe(t, map[string]any{"copy_dropped_by_stream": dup}, s)
		if o.Delivered["orig"] < 1 {
			t.Errorf("the original was not shown: %v", o.Delivered)
		}
		record("a4-copy-stored-before-the-start", o)
	})

	// A redelivery after a lost ack: the serve shows the message, its ack never reaches the server, the server
	// redelivers after the (shortened) ack wait, and the serve's memory of what it showed drops it.
	t.Run("b1-redelivery-after-a-lost-ack", func(t *testing.T) {
		w := newDupWorld(t, 2*time.Second)
		px := newDupAckProxy(t, w.f.e.URL)
		px.drop.Store(1)
		w.send(t, "orig")
		s := w.serve(t, w.cfg(px.URL()), 0)
		if !s.shownAtLeast(1, 15*time.Second) {
			t.Fatal("the original was not shown")
		}
		time.Sleep(7 * time.Second) // past the ack wait, so the server redelivers
		o := w.observe(t, map[string]any{"acks_dropped": int(px.dropped.Load())}, s)
		if px.dropped.Load() != 1 {
			t.Errorf("the proxy dropped %d acks, want 1", px.dropped.Load())
		}
		o.require(t, "orig", 1)
		record("b1-redelivery-after-a-lost-ack", o)
	})

	// A redelivery after a failed write: the session did not get the message, so it is naked and shown again.
	t.Run("b2-redelivery-after-a-failed-write", func(t *testing.T) {
		w := newDupWorld(t, 0)
		w.send(t, "orig")
		ctx, cancel := context.WithCancel(context.Background())
		ep, err := Open(ctx, w.cfg(""))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		rec := &dupRec{}
		rec.fail.Store(1)
		done := make(chan struct{})
		go func() { _ = ep.Run(ctx, rec.deliver); close(done) }()
		s := &dupServe{ep: ep, rec: rec, stop: func() { cancel(); <-done; ep.Close() }}
		if !s.shownAtLeast(1, 15*time.Second) {
			s.stop()
			t.Fatal("the original was not shown after a failed write")
		}
		time.Sleep(2 * time.Second)
		o := w.observe(t, nil, s)
		o.require(t, "orig", 1)
		record("b2-redelivery-after-a-failed-write", o)
	})

	// A restart with the mail already shown and acked: nothing is shown again.
	t.Run("c1-restart-after-the-mail-was-shown", func(t *testing.T) {
		w := newDupWorld(t, 0)
		w.send(t, "orig")
		s1 := w.serve(t, w.cfg(""), 0)
		if !s1.shownAtLeast(1, 15*time.Second) {
			t.Fatal("the original was not shown")
		}
		time.Sleep(time.Second)
		s1.stop()
		w.send(t, "later")
		s2 := w.serve(t, w.cfg(""), 0)
		if !s2.shownAtLeast(1, 15*time.Second) {
			t.Fatal("the later message was not shown after the restart")
		}
		time.Sleep(2500 * time.Millisecond)
		o := w.observe(t, nil, s1, s2)
		o.require(t, "orig", 1)
		o.require(t, "later", 1)
		record("c1-restart-after-the-mail-was-shown", o)
	})

	// A restart with the mail shown but its ack lost: the new serve has no memory of it.
	t.Run("c2-restart-after-a-lost-ack", func(t *testing.T) {
		w := newDupWorld(t, 2*time.Second)
		px := newDupAckProxy(t, w.f.e.URL)
		px.drop.Store(1)
		w.send(t, "orig")
		s1 := w.serve(t, w.cfg(px.URL()), 0)
		if !s1.shownAtLeast(1, 15*time.Second) {
			t.Fatal("the original was not shown")
		}
		time.Sleep(time.Second)
		s1.stop()
		s2 := w.serve(t, w.cfg(""), 0)
		s2.shownAtLeast(1, 10*time.Second)
		time.Sleep(3 * time.Second)
		o := w.observe(t, map[string]any{"acks_dropped": int(px.dropped.Load())}, s1, s2)
		if px.dropped.Load() != 1 {
			t.Errorf("the proxy dropped %d acks, want 1", px.dropped.Load())
		}
		if o.Delivered["orig"] < 1 {
			t.Errorf("the original was not shown: %v", o.Delivered)
		}
		record("c2-restart-after-a-lost-ack", o)
	})

	if out := os.Getenv("MASH_AT018_OUT"); out != "" {
		b, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
