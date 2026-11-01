package testenv

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// RTTProxy is a TCP proxy between a NATS client and the test server that counts the client's dependent round trips.
//
// A wave is a group of client requests that could all have been sent without waiting for each other: a client
// request (CONNECT, PUB, HPUB) that follows a server response (INFO, PONG, MSG, HMSG) delivered since the previous
// wave began starts a new one. Requests pipelined in one go are one wave however many there are, and one that waits
// for an answer is the next. The count is the number of times the client waited on the network, so it does not depend
// on how fast the machine is, only on the order of the protocol.
//
// Each direction is delayed by the one-way latency so a server response reaches the proxy's log at the moment the
// client would see it. That makes the order of the log the order of causality as long as the client's burst of
// pipelined sends is shorter than one round trip.
//
// Two kinds of client traffic are not requests and never start a wave: heartbeats, and replies to a request someone
// else made (a PUB to an _INBOX subject). A reply is the client answering, not waiting, and nothing it sends next
// depends on it. An endpoint's own $SRV.INFO probe, for one, is answered by its own service, and that reply lands
// beside the dependent chain at whatever moment the loopback MSG arrives, which under load can split a wave in two.
type RTTProxy struct {
	ln     net.Listener
	target string
	oneWay time.Duration

	mu        sync.Mutex
	waves     []rttWave
	sinceWave bool      // a response was delivered since the current wave began
	start     time.Time // Reset, or the proxy's creation: the origin of a wave's logged time
}

type rttWave struct {
	at       time.Time // when the wave's first request was sent
	subjects []string
}

// connectOverhead is what a command pays before its first wave besides the CONNECT wave itself: the TCP handshake
// (one round trip) and the server's INFO (half of one).
const connectOverhead = 1.5

// NewRTTProxy listens on a loopback port and forwards to the server at natsURL, adding oneWay latency to each
// direction. It stops when the test ends.
func NewRTTProxy(t *testing.T, natsURL string, oneWay time.Duration) *RTTProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &RTTProxy{ln: ln, target: strings.TrimPrefix(natsURL, "nats://"), oneWay: oneWay, sinceWave: true, start: time.Now()}
	go p.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return p
}

// URL is the address a client connects to in place of the server's.
func (p *RTTProxy) URL() string { return "nats://" + p.ln.Addr().String() }

// Reset forgets the waves counted so far.
func (p *RTTProxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.waves, p.sinceWave = nil, true
	p.start = time.Now()
}

// Waves is the number of dependent waves since Reset, the CONNECT handshake's among them.
func (p *RTTProxy) Waves() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.waves)
}

// WavesThrough is the number of waves up to and including the first that sent a request whose subject starts with
// prefix, and whether one has been sent. It marks a point in a long-running process's startup as the end of the count.
func (p *RTTProxy) WavesThrough(prefix string) (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.waves {
		for _, s := range w.subjects {
			if strings.HasPrefix(s, prefix) {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// RTT is Waves plus the connection's TCP handshake and INFO: the round trips a latency of oneWay*2 would cost.
func (p *RTTProxy) RTT() float64 { return float64(p.Waves()) + connectOverhead }

// Describe lists the waves and the subjects each sent, for a failure message.
func (p *RTTProxy) Describe() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	for i, w := range p.waves {
		count := map[string]int{}
		for _, s := range w.subjects {
			if i := strings.Index(s, ".$KV."); i >= 0 { // a KV key: count the gets, not each name
				s = s[:i] + ".$KV.*"
			}
			count[s]++
		}
		keys := make([]string, 0, len(count))
		for s := range count {
			keys = append(keys, s)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "wave %d (+%dms):", i+1, w.at.Sub(p.start).Milliseconds())
		for _, s := range keys {
			fmt.Fprintf(&b, " %s", s)
			if count[s] > 1 {
				fmt.Fprintf(&b, " x%d", count[s])
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (p *RTTProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(c)
	}
}

func (p *RTTProxy) handle(cl net.Conn) {
	time.Sleep(2 * p.oneWay) // the TCP handshake: one round trip before the first byte
	sv, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = cl.Close()
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go p.pump(cl, sv, true, &wg)
	go p.pump(sv, cl, false, &wg)
	wg.Wait()
	_ = cl.Close()
	_ = sv.Close()
}

type delayed struct {
	at time.Time
	b  []byte
}

// pump copies src to dst, each chunk delayed by oneWay. A client chunk is logged as it is read; a server chunk as it
// is written, when the client would receive it.
func (p *RTTProxy) pump(src, dst net.Conn, fromClient bool, wg *sync.WaitGroup) {
	defer wg.Done()
	q := make(chan delayed, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f := &framer{}
		for ch := range q {
			if d := time.Until(ch.at.Add(p.oneWay)); d > 0 {
				time.Sleep(d)
			}
			if !fromClient {
				p.feed(f, ch.b, false)
			}
			_, _ = dst.Write(ch.b)
		}
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	cf := &framer{}
	buf := make([]byte, 64<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			b := append([]byte(nil), buf[:n]...)
			if fromClient {
				p.feed(cf, b, true)
			}
			q <- delayed{time.Now(), b}
		}
		if err != nil {
			break
		}
	}
	close(q)
	<-done
}

func (p *RTTProxy) feed(f *framer, b []byte, fromClient bool) {
	for _, fr := range f.feed(b) {
		p.note(fr, fromClient)
	}
}

func (p *RTTProxy) note(f frame, fromClient bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fromClient {
		switch f.verb {
		case "CONNECT", "PUB", "HPUB":
		default:
			return
		}
		if strings.HasPrefix(f.subject, "agents.hb.") || strings.HasPrefix(f.subject, "_INBOX.") {
			return // not a request the client waits on: see RTTProxy
		}
		if p.sinceWave || len(p.waves) == 0 {
			p.waves = append(p.waves, rttWave{at: time.Now()})
			p.sinceWave = false
		}
		s := f.verb
		if f.subject != "" {
			s = f.subject
		}
		w := &p.waves[len(p.waves)-1]
		w.subjects = append(w.subjects, s)
		return
	}
	switch f.verb {
	case "INFO", "PONG":
		p.sinceWave = true
	case "MSG", "HMSG":
		if !strings.HasPrefix(f.subject, "agents.hb.") {
			p.sinceWave = true
		}
	}
}

// frame is one NATS protocol operation, without its payload.
type frame struct{ verb, subject string }

// framer splits a NATS protocol stream into operations, skipping message payloads.
type framer struct{ buf []byte }

func (f *framer) feed(b []byte) []frame {
	f.buf = append(f.buf, b...)
	var out []frame
	for {
		i := bytes.IndexByte(f.buf, '\n')
		if i < 0 {
			return out
		}
		fields := strings.Fields(string(f.buf[:i]))
		if len(fields) == 0 {
			f.buf = f.buf[i+1:]
			continue
		}
		fr := frame{verb: strings.ToUpper(fields[0])}
		end := i + 1
		switch fr.verb {
		case "PUB", "HPUB", "MSG", "HMSG":
			if len(fields) >= 3 {
				fr.subject = fields[1]
				size, _ := strconv.Atoi(fields[len(fields)-1])
				end += size + 2 // the payload and its CRLF
			}
		}
		if len(f.buf) < end {
			return out // the payload has not all arrived
		}
		f.buf = f.buf[end:]
		out = append(out, fr)
	}
}
