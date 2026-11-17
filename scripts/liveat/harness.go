package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Run is one invocation of the harness: one private run directory, the binaries built into it, and the processes
// started under it. Everything it creates is under Dir, and Close removes only Dir (unless Keep).
type Run struct {
	Dir      string // 0700, made with os.MkdirTemp
	Repo     string // the checkout the binaries are built from
	Port     int
	Keep     bool
	ID       string
	Revision string
	Mash     string // built binaries
	Mashctl  string
	NatsBin  string // nats-server, resolved from the caller's PATH
	NatsVer  string
	Runner   *Runner
	Out      string
	LogFile  *os.File

	mu    sync.Mutex
	procs []*Proc
	pids  map[int]string // recorded PIDs of processes this run started and has not yet stopped
}

// NewRun makes the run directory and its output directory, and opens the run log.
func NewRun(repo string, port int, keep bool) (*Run, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("liveat: --port %d is out of range", port)
	}
	dir, err := os.MkdirTemp("", "liveat-")
	if err != nil {
		return nil, err
	}
	r := &Run{Dir: dir, Repo: repo, Port: port, Keep: keep, ID: newRunID(time.Now()), pids: map[int]string{}}
	fail := func(err error) (*Run, error) { _ = os.RemoveAll(dir); return nil, err }
	if err := os.Chmod(dir, 0o700); err != nil {
		return fail(err)
	}
	r.Out = filepath.Join(dir, "out")
	if err := os.Mkdir(r.Out, 0o700); err != nil {
		return fail(err)
	}
	lf, err := os.OpenFile(filepath.Join(dir, "run.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return fail(err)
	}
	r.LogFile = lf
	r.Runner = &Runner{Out: r.Out, Cwd: dir, Log: lf}
	return r, nil
}

// Close stops anything still running by recorded PID, closes the log and, unless Keep, removes the run directory.
func (r *Run) Close() {
	r.mu.Lock()
	procs := r.procs
	pids := r.pids
	r.mu.Unlock()
	for _, p := range procs {
		p.Stop()
	}
	for pid, name := range pids {
		stopPID(pid, name, r.Runner.logf)
	}
	if r.LogFile != nil {
		r.LogFile.Close()
	}
	if !r.Keep {
		_ = os.RemoveAll(r.Dir)
	}
}

func (r *Run) record(pid int, what string) {
	r.mu.Lock()
	r.pids[pid] = what
	r.mu.Unlock()
}

func (r *Run) forget(pid int) {
	r.mu.Lock()
	delete(r.pids, pid)
	r.mu.Unlock()
}

// StartProc starts a process the run will stop by PID: at Stop, or at Close.
func (r *Run) StartProc(name string, args, env []string) (*Proc, error) {
	p, err := r.Runner.Start(name, args, env)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
	return p, nil
}

// goEnv is the environment of `go build` and git: an explicit list of the variables the toolchain needs, taken from the
// caller by name. The mash and mashctl children never get it.
func goEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "GOFLAGS", "GOTOOLCHAIN", "GOPROXY", "GONOSUMDB", "GONOSUMCHECK", "GOSUMDB"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
}

// Revision is the commit of the checkout and whether its tree is clean.
func gitRevision(repo string) (string, error) {
	run := func(args ...string) (string, error) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = goEnv()
		b, err := c.Output()
		return strings.TrimSpace(string(b)), err
	}
	sha, err := run("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	st, err := run("status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if st == "" {
		return sha + " (clean)", nil
	}
	return fmt.Sprintf("%s (dirty: %d paths changed)", sha, len(strings.Split(st, "\n"))), nil
}

// Prepare builds mash and mashctl from the checkout into <run>/bin, records the revision, and finds nats-server.
func (r *Run) Prepare() error {
	rev, err := gitRevision(r.Repo)
	if err != nil {
		return err
	}
	r.Revision = rev
	bin := filepath.Join(r.Dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		return err
	}
	for _, name := range []string{"mash", "mashctl"} {
		out := filepath.Join(bin, name)
		c := exec.Command("go", "build", "-o", out, "./cmd/"+name)
		c.Dir = r.Repo
		c.Env = goEnv()
		if b, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("go build ./cmd/%s: %v\n%s", name, err, b)
		}
		r.Runner.logf("built %s from %s", name, r.Revision)
	}
	r.Mash, r.Mashctl = filepath.Join(bin, "mash"), filepath.Join(bin, "mashctl")
	nb, err := exec.LookPath("nats-server")
	if err != nil {
		return errors.New("liveat: nats-server is not on PATH")
	}
	r.NatsBin = nb
	vb, err := exec.Command(nb, "--version").Output()
	if err != nil {
		return fmt.Errorf("nats-server --version: %w", err)
	}
	r.NatsVer = strings.TrimSpace(string(vb))
	return nil
}

// EnvLines is the environment section of a record. Scratch names are relative to the run directory.
func (r *Run) EnvLines(scratch []string) []string {
	return []string{
		runtime.GOOS + "/" + runtime.GOARCH,
		"go: " + runtime.Version(),
		"nats-server: " + r.NatsVer,
		"scratch (relative to the run directory): " + strings.Join(scratch, ", "),
	}
}

// stopPID stops a process this run recorded: SIGTERM, then SIGKILL after 10 seconds, to that PID only. It first checks
// that the PID still belongs to a process whose command line holds want, so a reused PID is never signalled.
func stopPID(pid int, want string, logf func(string, ...any)) {
	if pid <= 1 || !alive(pid) {
		return
	}
	cl := cmdlineOf(pid)
	if want != "" && !strings.Contains(cl, want) {
		logf("not stopping pid %d: its command line does not name %s", pid, want)
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			logf("stopped pid %d", pid)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	logf("killed pid %d", pid)
}

// alive reports whether the process exists and is not a zombie.
func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 && len(b) > i+2 && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}

// cmdlineOf is the command line of pid, from /proc, else from ps.
func cmdlineOf(pid int) string {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		return strings.ReplaceAll(string(b), "\x00", " ")
	}
	b, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return string(b)
}

// waitPortFree waits until nothing listens on 127.0.0.1:port.
func waitPortFree(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
		if err != nil {
			return nil
		}
		c.Close()
		if time.Now().After(deadline) {
			return fmt.Errorf("liveat: port %d is still in use", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// treeHash is a digest of the file tree under root: each entry's relative path, kind, mode, size and content. Top-level
// entries named in skip are left out.
func treeHash(root string, skip ...string) (string, error) {
	h := sha256.New()
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		for _, s := range skip {
			if rel == s {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		entries = append(entries, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	for _, rel := range entries {
		p := filepath.Join(root, rel)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", rel, st.Mode(), st.Size())
		if st.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			s := sha256.Sum256(b)
			h.Write(s[:])
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
