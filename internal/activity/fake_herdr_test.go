package activity

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeHerdr speaks the part of herdr's socket protocol the source uses.
type fakeHerdr struct {
	t    *testing.T
	path string
	ln   net.Listener

	mu        sync.Mutex
	status    string
	termID    string
	version   string // empty: the pong has none
	protocol  int
	paneErr   bool
	methods   map[string]int
	conns     []net.Conn
	subs      []net.Conn
	subscribe int // events.subscribe requests seen
	paneGets  int
	accepted  int
}

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	dir, err := os.MkdirTemp("", "ah")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeHerdr{t: t, path: filepath.Join(dir, "h.sock"), status: "idle", termID: "term_1",
		version: "0.9.5", protocol: 22, methods: map[string]int{}}
	ln, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { ln.Close(); f.dropAll() })
	go f.accept()
	return f
}

func (f *fakeHerdr) accept() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.accepted++
		f.mu.Unlock()
		go f.serve(c)
	}
}

func (f *fakeHerdr) write(c net.Conn, v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	defer f.mu.Unlock()
	c.Write(append(b, '\n'))
}

func (f *fakeHerdr) serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			return
		}
		f.mu.Lock()
		f.methods[req.Method]++
		status, term, version, protocol, paneErr := f.status, f.termID, f.version, f.protocol, f.paneErr
		switch req.Method {
		case "pane.get":
			f.paneGets++
		case "events.subscribe":
			f.subscribe++
			f.subs = append(f.subs, c)
		}
		f.mu.Unlock()
		switch req.Method {
		case "ping":
			res := map[string]any{"type": "pong", "capabilities": map[string]any{}}
			if version != "" {
				res["version"] = version
			}
			if protocol != 0 {
				res["protocol"] = protocol
			}
			f.write(c, map[string]any{"id": req.ID, "result": res})
		case "pane.get":
			if paneErr {
				f.write(c, map[string]any{"id": req.ID, "error": map[string]any{"code": "pane_not_found", "message": "no such pane"}})
				continue
			}
			f.write(c, map[string]any{"id": req.ID, "result": map[string]any{"type": "pane_info", "pane": map[string]any{
				"pane_id": "w1:p1", "terminal_id": term, "workspace_id": "w1", "agent_status": status}}})
		case "events.subscribe":
			f.write(c, map[string]any{"id": req.ID, "result": map[string]any{"type": "subscription_started"}})
		default:
			f.write(c, map[string]any{"id": req.ID, "error": map[string]any{"code": "unknown_method", "message": req.Method}})
		}
	}
}

func (f *fakeHerdr) set(status string) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

func (f *fakeHerdr) setTerm(id string) {
	f.mu.Lock()
	f.termID = id
	f.mu.Unlock()
}

func (f *fakeHerdr) setPaneErr(v bool) {
	f.mu.Lock()
	f.paneErr = v
	f.mu.Unlock()
}

// push sends a status event to every subscribed connection.
func (f *fakeHerdr) push(pane, status string) {
	f.mu.Lock()
	subs := append([]net.Conn(nil), f.subs...)
	f.mu.Unlock()
	for _, c := range subs {
		f.write(c, map[string]any{"event": "pane.agent_status_changed", "data": map[string]any{
			"agent": "claude", "agent_status": status, "pane_id": pane, "workspace_id": "w1"}})
	}
}

func (f *fakeHerdr) eventsLost() {
	f.mu.Lock()
	subs := append([]net.Conn(nil), f.subs...)
	f.subs = nil
	f.mu.Unlock()
	for _, c := range subs {
		f.write(c, map[string]any{"id": "s1", "error": map[string]any{"code": "events_lost", "message": "behind"}})
	}
}

func (f *fakeHerdr) dropAll() {
	f.mu.Lock()
	conns := f.conns
	f.conns, f.subs = nil, nil
	f.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}

func (f *fakeHerdr) seenMethods() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]int{}
	for k, v := range f.methods {
		m[k] = v
	}
	return m
}

func (f *fakeHerdr) count(get func(*fakeHerdr) int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return get(f)
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func herdrEnv(f *fakeHerdr) map[string]string {
	return map[string]string{"HERDR_SOCKET_PATH": f.path, "HERDR_PANE_ID": "w1:p1"}
}
