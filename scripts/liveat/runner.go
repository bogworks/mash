package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Home is one scratch home: where a mash or mashctl command thinks the user lives. Everything is under Dir.
type Home struct {
	Dir    string // HOME
	Config string // MASH_CONFIG_DIR
	Keys   string // MASH_KEYS_DIR
	State  string // MASH_STATE_DIR
	Tmp    string // TMPDIR
}

// NewHome makes the directories of a scratch home under dir (mode 0700). The config and key directories are left for
// the commands to create, as a fresh machine has none.
func NewHome(dir string) (Home, error) {
	h := Home{Dir: dir, Config: filepath.Join(dir, "config"), Keys: filepath.Join(dir, "keys"), State: filepath.Join(dir, "state"), Tmp: filepath.Join(dir, "tmp")}
	for _, d := range []string{h.Dir, h.State, h.Tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return h, err
		}
	}
	return h, nil
}

// BuildEnv is the whole environment of a child process, built from scratch values: nothing is read from the caller's
// environment, so no MASH_*, NATS_* or HOME of the caller reaches a child. The one exception is PATH, which names where the
// tools live on this machine (the caller's PATH, or /usr/bin:/bin when it has none); pathDirs are added after it.
// extra is a list of KEY=VALUE pairs that are added last and win over the base.
// toolPath is the caller's PATH: where the tools the harness and the commands need live on this machine.
func toolPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/bin:/bin"
}

func BuildEnv(h Home, pathDirs []string, extra ...string) []string {
	base := []string{
		"HOME=" + h.Dir,
		"MASH_CONFIG_DIR=" + h.Config,
		"MASH_KEYS_DIR=" + h.Keys,
		"MASH_STATE_DIR=" + h.State,
		"TMPDIR=" + h.Tmp,
		"PATH=" + strings.Join(append([]string{toolPath()}, pathDirs...), ":"),
		"LC_ALL=C",
	}
	idx := map[string]int{}
	var out []string
	for _, kv := range append(base, extra...) {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}

// Result is what one command did.
type Result struct {
	Code   int // the exit code; -1 when it did not start or was killed by the timeout
	Stdout string
	Stderr string
	Seq    int
}

// Text is stdout and stderr together.
func (r Result) Text() string { return r.Stdout + r.Stderr }

// Runner runs commands, keeps the output of every one under Out as NN-name.out and NN-name.err (mode 0600), logs the
// command line and exit code, and scans the output for a seed or a JWT.
type Runner struct {
	Out     string // the captured output directory
	Cwd     string // the working directory of Run
	Timeout time.Duration
	Log     io.Writer

	mu    sync.Mutex
	seq   int
	leaks []Leak
}

var labelRE = regexp.MustCompile(`[^a-z0-9-]+`)

func (r *Runner) next(name string) (int, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq, fmt.Sprintf("%02d-%s", r.seq, strings.Trim(labelRE.ReplaceAllString(strings.ToLower(name), "-"), "-"))
}

func (r *Runner) logf(format string, a ...any) {
	if r.Log != nil {
		r.mu.Lock()
		fmt.Fprintf(r.Log, format+"\n", a...)
		r.mu.Unlock()
	}
}

// Leaks is every secret the runner found in output so far.
func (r *Runner) Leaks() []Leak {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Leak(nil), r.leaks...)
}

// save writes the output files of one command and scans them.
func (r *Runner) save(base string, stdout, stderr []byte) error {
	for ext, b := range map[string][]byte{".out": stdout, ".err": stderr} {
		p := filepath.Join(r.Out, base+ext)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return err
		}
		for _, k := range scanBytes(b) {
			r.mu.Lock()
			r.leaks = append(r.leaks, Leak{File: base + ext, Kind: k})
			r.mu.Unlock()
			r.logf("LEAK: a %s in %s", k, base+ext)
		}
	}
	return nil
}

// Run runs args[0] with args[1:] in the runner's working directory. name labels its output files.
func (r *Runner) Run(name string, args []string, env []string, stdin string) Result {
	return r.RunIn(r.Cwd, name, args, env, stdin)
}

// RunIn is Run in directory dir.
func (r *Runner) RunIn(dir, name string, args []string, env []string, stdin string) Result {
	seq, base := r.next(name)
	res := Result{Code: -1, Seq: seq}
	if len(args) == 0 {
		res.Stderr = "liveat: no command"
		_ = r.save(base, nil, []byte(res.Stderr))
		return res
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	start := time.Now()
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.Code = 0
	case errors.As(err, &ee):
		res.Code = ee.ExitCode()
	default:
		fmt.Fprintf(&se, "liveat: %v\n", err)
	}
	if ctx.Err() != nil {
		res.Code = -1
		fmt.Fprintf(&se, "liveat: killed after %s\n", timeout)
	}
	res.Stdout, res.Stderr = so.String(), se.String()
	if err := r.save(base, so.Bytes(), se.Bytes()); err != nil {
		r.logf("%s: saving output: %v", base, err)
	}
	r.logf("%s: %s exit=%d (%s)", base, cmdline(args), res.Code, time.Since(start).Round(time.Millisecond))
	return res
}

// cmdline is the command as logged: the binary's base name and the arguments. Arguments are paths and flags, never
// a seed or a JWT (those go by file).
func cmdline(args []string) string {
	a := append([]string{filepath.Base(args[0])}, args[1:]...)
	return redact(strings.Join(a, " "))
}

// syncBuf is a bytes.Buffer safe for a writer goroutine and a reader.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Proc is a long-running process (a mash serve session) started with a recorded PID.
type Proc struct {
	Name   string
	PID    int
	r      *Runner
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *syncBuf
	errb   *syncBuf
	files  []*os.File
	done   chan struct{}
	exited bool
	mu     sync.Mutex
	base   string
}

// Start starts args[0] with args[1:] and stdin held open. Its output is kept in memory and in files under Out.
func (r *Runner) Start(name string, args []string, env []string) (*Proc, error) {
	_, base := r.next(name)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Dir = r.Cwd
	p := &Proc{Name: name, r: r, cmd: cmd, out: &syncBuf{}, errb: &syncBuf{}, done: make(chan struct{}), base: base}
	for ext, w := range map[string]*syncBuf{".out": p.out, ".err": p.errb} {
		f, err := os.OpenFile(filepath.Join(r.Out, base+ext), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			p.closeFiles()
			return nil, err
		}
		p.files = append(p.files, f)
		if ext == ".out" {
			cmd.Stdout = io.MultiWriter(f, w)
		} else {
			cmd.Stderr = io.MultiWriter(f, w)
		}
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		p.closeFiles()
		return nil, err
	}
	p.stdin = in
	if err := cmd.Start(); err != nil {
		p.closeFiles()
		return nil, err
	}
	p.PID = cmd.Process.Pid
	r.logf("%s: started %s pid=%d", base, cmdline(args), p.PID)
	go func() {
		_ = cmd.Wait()
		p.mu.Lock()
		p.exited = true
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *Proc) closeFiles() {
	for _, f := range p.files {
		f.Close()
	}
}

// Exited reports whether the process has ended.
func (p *Proc) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// Stdout and Stderr are what the process has written so far.
func (p *Proc) Stdout() string { return p.out.String() }
func (p *Proc) Stderr() string { return p.errb.String() }

// Send writes one line to the process.
func (p *Proc) Send(line string) error {
	_, err := io.WriteString(p.stdin, line+"\n")
	return err
}

// WaitFor polls until pred holds over stdout and stderr, the process exits, or d passes.
func (p *Proc) WaitFor(d time.Duration, pred func(stdout, stderr string) bool) bool {
	deadline := time.Now().Add(d)
	for {
		if pred(p.Stdout(), p.Stderr()) {
			return true
		}
		if p.Exited() || time.Now().After(deadline) {
			return pred(p.Stdout(), p.Stderr())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Handshake takes an MCP server through initialize and tools/list.
func (p *Proc) Handshake(d time.Duration) error {
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"experimental":{"claude/channel":{}}},"clientInfo":{"name":"liveat","version":"1"}}}`
	if err := p.Send(init); err != nil {
		return err
	}
	if !p.WaitFor(d, func(o, _ string) bool { return strings.Contains(o, `"id":1`) }) {
		return fmt.Errorf("no initialize reply (stderr %q)", redact(strings.TrimSpace(p.Stderr())))
	}
	if err := p.Send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return err
	}
	if err := p.Send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); err != nil {
		return err
	}
	if !p.WaitFor(d, func(o, _ string) bool { return strings.Contains(o, `"id":2`) }) {
		return errors.New("no tools/list reply")
	}
	return nil
}

// Stop ends the process by its recorded PID: stdin is closed first, then SIGTERM, then SIGKILL, each to p.PID only.
// It then scans the files the process wrote.
func (p *Proc) Stop() {
	if p.PID == 0 {
		return
	}
	_ = p.stdin.Close()
	wait := func(d time.Duration) bool {
		select {
		case <-p.done:
			return true
		case <-time.After(d):
			return false
		}
	}
	if !wait(3 * time.Second) {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		if !wait(5 * time.Second) {
			_ = p.cmd.Process.Kill()
			wait(5 * time.Second)
		}
	}
	p.closeFiles()
	for _, ext := range []string{".out", ".err"} {
		b, err := os.ReadFile(filepath.Join(p.r.Out, p.base+ext))
		if err != nil {
			continue
		}
		for _, k := range scanBytes(b) {
			p.r.mu.Lock()
			p.r.leaks = append(p.r.leaks, Leak{File: p.base + ext, Kind: k})
			p.r.mu.Unlock()
			p.r.logf("LEAK: a %s in %s", k, p.base+ext)
		}
	}
	p.r.logf("%s: stopped pid=%d", p.base, p.PID)
	p.PID = 0
}
