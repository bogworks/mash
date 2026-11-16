// Package herdr is the runtime source for package activity that talks to herdr, a terminal workspace manager, over
// its unix socket. Everything specific to herdr is here: the socket protocol, the HERDR_* environment variables and
// the version text. Package activity knows none of it; the caller passes the source in as Options.Runtime.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/activity"
)

// Options configures the source. The zero value is the production setting.
type Options struct {
	Poll    time.Duration                       // backstop pane.get interval; 0 = 5s
	Dial    func(path string) (net.Conn, error) // nil = unix socket dial; tests inject
	Backoff func(attempt int) time.Duration     // reconnect delay; nil = 1s doubling to 30s
	Log     io.Writer                           // diagnostics lines (connected, unavailable, version); nil = discard
}

// FromEnv returns the source for the pane the process runs in. ok is false when HERDR_SOCKET_PATH or HERDR_PANE_ID
// is unset or empty: the process is not under herdr and there is nothing to read.
func FromEnv(env func(string) string, o Options) (activity.RuntimeSource, bool) {
	sock, pane := env("HERDR_SOCKET_PATH"), env("HERDR_PANE_ID")
	if sock == "" || pane == "" {
		return nil, false
	}
	return &source{o: o, sock: sock, pane: pane}, true
}

// The source reads the pane's agent state from herdr over its unix socket: JSON lines, one request per line.
// It sends ping, pane.get and events.subscribe and nothing else; it never writes to the pane.

// herdrTimeout bounds one request and its answer.
const herdrTimeout = 5 * time.Second

// herdrMaxLine bounds one line from the server.
const herdrMaxLine = 1 << 20

type herdrMsg struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func (m herdrMsg) err() error {
	if m.Error != nil {
		return fmt.Errorf("herdr error %s: %s", m.Error.Code, m.Error.Message)
	}
	return nil
}

// connSet closes every connection of one session when the session ends.
type connSet struct {
	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

func (s *connSet) add(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		c.Close()
		return
	}
	s.conns = append(s.conns, c)
}

func (s *connSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, c := range s.conns {
		c.Close()
	}
}

type herdrConn struct {
	c  net.Conn
	sc *bufio.Scanner
}

func newHerdrConn(c net.Conn) *herdrConn {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 0, 4096), herdrMaxLine)
	return &herdrConn{c: c, sc: sc}
}

// readMsg reads one line; with a deadline of zero it blocks.
func (h *herdrConn) readMsg(deadline time.Duration) (herdrMsg, error) {
	if deadline > 0 {
		h.c.SetReadDeadline(time.Now().Add(deadline))
	} else {
		h.c.SetReadDeadline(time.Time{})
	}
	if !h.sc.Scan() {
		if err := h.sc.Err(); err != nil {
			return herdrMsg{}, err
		}
		return herdrMsg{}, errors.New("herdr closed the connection")
	}
	var m herdrMsg
	if err := json.Unmarshal(h.sc.Bytes(), &m); err != nil {
		return herdrMsg{}, fmt.Errorf("herdr sent a line that is not JSON: %w", err)
	}
	return m, nil
}

func (h *herdrConn) send(id, method string, params any) error {
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	h.c.SetWriteDeadline(time.Now().Add(herdrTimeout))
	_, err = h.c.Write(append(b, '\n'))
	return err
}

// call sends one request and reads its one answer line.
func (h *herdrConn) call(id, method string, params any) (herdrMsg, error) {
	if err := h.send(id, method, params); err != nil {
		return herdrMsg{}, err
	}
	m, err := h.readMsg(herdrTimeout)
	if err != nil {
		return herdrMsg{}, err
	}
	if err := m.err(); err != nil {
		return herdrMsg{}, err
	}
	if m.ID != id {
		return herdrMsg{}, fmt.Errorf("herdr answered id %q to request %q", m.ID, id)
	}
	return m, nil
}

// herdrState maps herdr's agent_status to a state; "" means not an agent (unknown, or any word we do not know).
func herdrState(s string) activity.State {
	switch st := activity.State(s); st {
	case activity.StateIdle, activity.StateWorking, activity.StateBlocked, activity.StateDone:
		return st
	}
	return ""
}

// source is the herdr runtime source for one pane.
type source struct {
	o    Options
	sock string
	pane string

	wg     sync.WaitGroup // every goroutine Run starts; Run waits for it
	report func(activity.RuntimeReport)
}

func (s *source) Name() string { return "herdr" }

// Describe is the status text for an available report.
func (s *source) Describe(r activity.RuntimeReport) string {
	if r.Version != "" {
		return "herdr " + r.Version + " ok"
	}
	return "herdr ok"
}

func (s *source) spawn(f func()) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		f()
	}()
}

func (s *source) logf(format string, a ...any) {
	if s.o.Log != nil {
		fmt.Fprintf(s.o.Log, format+"\n", a...)
	}
}

func (s *source) dial(path string) (net.Conn, error) {
	if s.o.Dial != nil {
		return s.o.Dial(path)
	}
	return net.DialTimeout("unix", path, herdrTimeout)
}

func (s *source) backoff(attempt int) time.Duration {
	if s.o.Backoff != nil {
		return s.o.Backoff(attempt)
	}
	if attempt > 5 {
		attempt = 5
	}
	return min(time.Second<<attempt, 30*time.Second)
}

// Run keeps a session to herdr going: on any error the source is unavailable at once, then it retries after
// a growing delay. The delay restarts from the beginning after a session that connected. It returns when ctx is
// done and every goroutine it started has stopped.
func (s *source) Run(ctx context.Context, report func(activity.RuntimeReport)) {
	s.report = report
	defer s.wg.Wait()
	attempt := 0
	for ctx.Err() == nil {
		connected, err := s.session(ctx)
		report(activity.RuntimeReport{})
		if ctx.Err() != nil {
			return
		}
		s.logf("herdr unavailable: %v", err)
		if connected {
			attempt = 0
		}
		d := s.backoff(attempt)
		attempt++
		timer := time.NewTimer(d)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type subMsg struct {
	gen int
	msg herdrMsg
	err error
}

// session runs one connection's life and returns why it ended. connected is true once the source went live.
func (s *source) session(ctx context.Context) (connected bool, err error) {
	sock, pane := s.sock, s.pane
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var cs connSet
	s.spawn(func() {
		<-sctx.Done()
		cs.closeAll()
	})

	nextID := 0
	id := func() string { nextID++; return "a" + strconv.Itoa(nextID) }

	c, err := s.dial(sock)
	if err != nil {
		return false, err
	}
	cs.add(c)
	ctrl := newHerdrConn(c)

	m, err := ctrl.call(id(), "ping", map[string]any{})
	if err != nil {
		return false, err
	}
	var pong struct {
		Type     string `json:"type"`
		Version  string `json:"version"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(m.Result, &pong); err != nil || pong.Type != "pong" || pong.Version == "" || pong.Protocol == 0 {
		return false, errors.New("herdr ping answer has no version or protocol")
	}

	getPane := func() (term string, st activity.State, err error) {
		m, err := ctrl.call(id(), "pane.get", map[string]any{"pane_id": pane})
		if err != nil {
			return "", "", err
		}
		var r struct {
			Type string `json:"type"`
			Pane struct {
				PaneID      string `json:"pane_id"`
				TerminalID  string `json:"terminal_id"`
				AgentStatus string `json:"agent_status"`
			} `json:"pane"`
		}
		if err := json.Unmarshal(m.Result, &r); err != nil || r.Type != "pane_info" {
			return "", "", errors.New("herdr pane.get answer is not pane_info")
		}
		return r.Pane.TerminalID, herdrState(r.Pane.AgentStatus), nil
	}

	term, _, err := getPane()
	if err != nil {
		return false, err
	}

	events := make(chan subMsg, 16)
	gen := 0
	var sub net.Conn
	subscribe := func() error {
		if sub != nil {
			sub.Close()
		}
		gen++
		sc, err := s.dial(sock)
		if err != nil {
			return err
		}
		cs.add(sc)
		sub = sc
		h := newHerdrConn(sc)
		sid := id()
		err = h.send(sid, "events.subscribe", map[string]any{"subscriptions": []map[string]any{
			{"type": "pane.agent_status_changed", "pane_id": pane},
		}})
		if err != nil {
			return err
		}
		first, err := h.readMsg(herdrTimeout)
		if err != nil {
			return err
		}
		if err := first.err(); err != nil {
			return err
		}
		var r struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(first.Result, &r) != nil || r.Type != "subscription_started" {
			return errors.New("herdr did not start the subscription")
		}
		g := gen
		s.spawn(func() {
			for {
				m, err := h.readMsg(0)
				select {
				case events <- subMsg{gen: g, msg: m, err: err}:
				case <-sctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		})
		return nil
	}
	// refresh reads the pane and fails the session if the pane id now belongs to another terminal.
	var version string
	refresh := func() error {
		tid, st, err := getPane()
		if err != nil {
			return err
		}
		if tid != term {
			return errors.New("herdr pane id now names a different terminal")
		}
		s.report(activity.RuntimeReport{Available: true, State: st, Version: version})
		return nil
	}

	version = pong.Version
	if err := subscribe(); err != nil {
		return false, err
	}
	// Events from now on are covered; read the state once more to close the gap before the subscription began.
	if err := refresh(); err != nil {
		return false, err
	}
	connected = true
	s.logf("herdr %s ok", pong.Version)

	poll := s.o.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case <-sctx.Done():
			return connected, sctx.Err()
		case <-tick.C:
			if err := refresh(); err != nil {
				return connected, err
			}
		case sm := <-events:
			if sm.gen != gen {
				continue // the connection we replaced
			}
			if sm.err != nil {
				return connected, sm.err
			}
			if sm.msg.Error != nil {
				if sm.msg.Error.Code != "events_lost" {
					return connected, sm.msg.err()
				}
				// The server fell behind and ended the subscription: subscribe again and read the pane.
				if err := subscribe(); err != nil {
					return connected, err
				}
				if err := refresh(); err != nil {
					return connected, err
				}
				continue
			}
			if sm.msg.Event != "pane.agent_status_changed" {
				continue
			}
			var d struct {
				PaneID      string `json:"pane_id"`
				AgentStatus string `json:"agent_status"`
			}
			if json.Unmarshal(sm.msg.Data, &d) != nil || d.PaneID != pane {
				continue
			}
			s.report(activity.RuntimeReport{Available: true, State: herdrState(d.AgentStatus), Version: version})
		}
	}
}
