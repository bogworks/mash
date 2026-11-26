package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"
)

// A session is one `mash serve` process driven the way Claude Code drives it: an MCP client on its standard input and
// output. It answers the server's own requests (ping), keeps channel events, and matches replies to calls by id.
type session struct {
	name   string
	cmd    *exec.Cmd
	in     io.WriteCloser
	pid    int
	stderr *os.File
	done   chan struct{}
	exit   int

	mu      sync.Mutex
	nextID  int
	pending map[int]chan json.RawMessage
	events  chan map[string]any
	initVer string // the version the initialize reply reported
}

// startSession starts bin serve under env, writing its standard error to errPath, and completes the MCP handshake.
func startSession(name, bin string, env []string, errPath string) (*session, error) {
	cmd := exec.Command(bin, "serve")
	cmd.Env = env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ef, err := os.Create(errPath)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = ef
	if err := cmd.Start(); err != nil {
		ef.Close()
		return nil, err
	}
	s := &session{name: name, cmd: cmd, in: in, pid: cmd.Process.Pid, stderr: ef, done: make(chan struct{}),
		nextID: 10, pending: map[int]chan json.RawMessage{}, events: make(chan map[string]any, 64)}
	go s.read(out)
	go func() {
		err := cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok {
			s.exit = ee.ExitCode()
		}
		close(s.done)
	}()
	if err := s.handshake(); err != nil {
		s.kill()
		return nil, err
	}
	return s, nil
}

func (s *session) read(out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		var id *int
		if raw, ok := m["id"]; ok {
			var n int
			if json.Unmarshal(raw, &n) == nil {
				id = &n
			}
		}
		_, hasMethod := m["method"]
		switch {
		case hasMethod && id != nil: // a request from the server: answer it (ping)
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *id, "result": map[string]any{}})
			_, _ = s.in.Write(append(b, '\n'))
		case hasMethod:
			var ev map[string]any
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				select {
				case s.events <- ev:
				default:
				}
			}
		case id != nil:
			s.mu.Lock()
			ch := s.pending[*id]
			delete(s.pending, *id)
			s.mu.Unlock()
			if ch != nil {
				// The scanner reuses its buffer on the next scan: the receiver gets its own copy.
				ch <- append(json.RawMessage(nil), sc.Bytes()...)
			}
		}
	}
}

// request sends one JSON-RPC request and returns the whole reply line.
func (s *session) request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan json.RawMessage, 1)
	s.pending[id] = ch
	s.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		return m, nil
	case <-s.done:
		return nil, errors.New("the session ended")
	case <-time.After(timeout):
		return nil, fmt.Errorf("no reply to %s within %s", method, timeout)
	}
}

func (s *session) handshake() error {
	m, err := s.request("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{"experimental": map[string]any{"claude/channel": map[string]any{}}},
		"clientInfo":      map[string]any{"name": "at011", "version": "1"},
	}, 30*time.Second)
	if err != nil {
		return err
	}
	var rep struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	_ = json.Unmarshal(m, &rep)
	s.initVer = rep.Result.ServerInfo.Version
	if _, err := s.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		return err
	}
	if _, err := s.request("tools/list", map[string]any{}, 30*time.Second); err != nil {
		return err
	}
	return nil
}

// call runs a tool and returns the text it answered.
func (s *session) call(tool string, args map[string]any, timeout time.Duration) (string, error) {
	m, err := s.request("tools/call", map[string]any{"name": tool, "arguments": args}, timeout)
	if err != nil {
		return "", err
	}
	var rep struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(m, &rep); err != nil {
		return "", err
	}
	if rep.Error != nil {
		return "", errors.New(rep.Error.Message)
	}
	text := ""
	for _, c := range rep.Result.Content {
		text += c.Text
	}
	if rep.Result.IsError {
		return text, errors.New("the tool reported an error: " + text)
	}
	return text, nil
}

var versionRE = regexp.MustCompile(`\bv?(\d+\.\d+\.\d+)\b`)

// reportedVersion is the version the running session reports through its version tool.
func (s *session) reportedVersion() (string, error) {
	text, err := s.call("version", map[string]any{}, 30*time.Second)
	if err != nil {
		return "", err
	}
	if m := versionRE.FindStringSubmatch(text); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("no version in %q", text)
}

// promptEvent waits for a live prompt and returns its request id.
func (s *session) promptEvent(timeout time.Duration) (string, bool) {
	t := time.After(timeout)
	for {
		select {
		case ev := <-s.events:
			if ev["method"] != "notifications/claude/channel" {
				continue
			}
			p, _ := ev["params"].(map[string]any)
			meta, _ := p["meta"].(map[string]any)
			if id, _ := meta["request_id"].(string); id != "" {
				return id, true
			}
		case <-t:
			return "", false
		case <-s.done:
			return "", false
		}
	}
}

func (s *session) alive() bool {
	select {
	case <-s.done:
		return false
	default:
	}
	return syscall.Kill(s.pid, 0) == nil
}

// stop ends the session the way Claude Code does at exit: its standard input is closed, and it must exit by itself.
// It reports whether it did and with what status.
func (s *session) stop(wait time.Duration) (exited bool, status int) {
	_ = s.in.Close()
	select {
	case <-s.done:
		s.stderr.Close()
		return true, s.exit
	case <-time.After(wait):
		s.kill()
		return false, -1
	}
}

// kill ends the process by its recorded PID, with SIGTERM and then SIGKILL, each to that PID only.
func (s *session) kill() {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		_ = syscall.Kill(s.pid, sig)
		select {
		case <-s.done:
			s.stderr.Close()
			return
		case <-time.After(5 * time.Second):
		}
	}
	s.stderr.Close()
}

// exeInfo is the executable a process runs: its path as the OS gives it and the sha256 of the file the process has open
// (on Linux the very inode it started from, even after the path was replaced). Where the OS does not show it, ok is false.
func exeInfo(pid int) (path, sum string, ok bool) {
	link := filepath.Join("/proc", fmt.Sprint(pid), "exe")
	p, err := os.Readlink(link)
	if err != nil {
		return "", "", false
	}
	s, err := fileSHA(link)
	if err != nil {
		return p, "", false
	}
	return p, s, true
}
