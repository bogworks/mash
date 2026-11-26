package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// AT-005 runs on three machines M1, M2 and M3. A Hosts value says where they are; the scenario talks to a Host and
// knows nothing else. Local puts all three on this machine (loopback, a directory tree each, three NATS processes).
// Podman puts them in three rootless containers the test builds and destroys (podman.go). SSH is a stub that must not
// be implemented: no test runs on a production host.

// Ports are the ports of one node. The operator reserved 14222-14224 (clients), 16222-16224 (routes) and 18222 (the
// monitor of node 1 only); nothing else may be used.
type Ports struct{ Client, Route, Monitor int }

// DefaultPorts is the reserved port set, indexed by node (0 is M1).
var DefaultPorts = []Ports{{14222, 16222, 18222}, {14223, 16223, 0}, {14224, 16224, 0}}

// Host is one machine as the scenario sees it. Every path is a path on that machine.
type Host interface {
	Role() string                                          // "M1", "M2" or "M3"
	Facts() string                                         // operating system and architecture
	Addr() string                                          // the address the other machines reach it on
	Ports() Ports                                          // its NATS ports
	Home() Home                                            // its HOME, config, keys and state directories
	ServerDir() string                                     // the NATS server's directory
	TLSDir() string                                        // where its certificate files are
	Bin(name string) string                                // path of "mash", "mashctl" or "nats-server" on this machine
	Exec(label string, args, env []string) Result          // run to completion
	Spawn(label string, args, env []string) (*Proc, error) // a long-running process, stopped by its recorded PID
	Put(path string, data []byte, mode os.FileMode) error
	Get(path string) ([]byte, error)
	Remove(path string) error // one exact path (a file or an empty directory)
	Exists(path string) bool
	// WaitPortFree waits until nothing accepts connections on this machine's port (on its own address).
	WaitPortFree(port int, d time.Duration) error
	// HTTPGet fetches a URL from this machine (the monitor is reached from the node's own network).
	HTTPGet(url string) ([]byte, error)
	// Snapshot makes the machine's whole file tree (home, config, keys, server, tls) readable on the test's own
	// filesystem for the secret search. dir is its root; done removes any copy the call made.
	Snapshot() (dir string, done func(), err error)
}

// Preflighter is a Hosts value that can tell, before anything is built, that it cannot run here.
type Preflighter interface{ Preflight() error }

// Cleaner is a Hosts value whose substrate can leave things behind (containers, networks, images, machines) and can
// remove one run's by its label (mash-at005=<run id>). It backs `liveat at005-cleanup`.
type Cleaner interface {
	Cleanup(runID string, stdout, stderr io.Writer) int
}

// Hosts is the one seam between the AT-005 scenario and the substrate that makes its nodes: Open creates three nodes
// (each a Host: its address, exec, copy in and out, a way to read its file tree) and registers their destruction on
// the Run (Run.OnClose); a Cleaner removes one run's leftovers by label. The scenario knows nothing else, so the substrate can
// change (Local loopback, rootless podman, a future VM or container service) by adding one more Hosts value.
type Hosts interface {
	Name() string
	Open(r *Run) ([]Host, error)
}

var errSSHNotConfigured = errors.New("ssh hosts: not configured")

// SSH is the three real hosts of the contract, each with a directory ~/mash-at005 and no sudo, the same
// ports as above, binaries copied over.
//
// TODO: not implemented. A later change fills in Open (ssh to each host, copy the binaries, return Hosts whose Exec,
// Spawn, Put, Get and Remove run over ssh) and nothing else in the harness changes. It must not touch a remote host
// until then.
type SSH struct{ Names []string }

func (SSH) Name() string { return "ssh" }

// Open reports that ssh hosts are not configured.
func (SSH) Open(*Run) ([]Host, error) { return nil, errSSHNotConfigured }

// Local is three machines on this host, under <run>/at005/M1..M3.
type Local struct{}

func (Local) Name() string { return "local" }

// Open makes each machine's directory (0700) and its home.
func (Local) Open(r *Run) ([]Host, error) {
	root := filepath.Join(r.Dir, "at005")
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	var hs []Host
	for i, p := range DefaultPorts {
		role := "M" + strconv.Itoa(i+1)
		dir := filepath.Join(root, role)
		home, err := NewHome(filepath.Join(dir, "home"))
		if err != nil {
			return nil, err
		}
		for _, d := range []string{filepath.Join(dir, "server"), filepath.Join(dir, "tls")} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return nil, err
			}
		}
		hs = append(hs, &localHost{r: r, role: role, dir: dir, home: home, ports: p})
	}
	return hs, nil
}

type localHost struct {
	r     *Run
	role  string
	dir   string
	home  Home
	ports Ports
}

func (h *localHost) Role() string      { return h.role }
func (h *localHost) Facts() string     { return runtime.GOOS + "/" + runtime.GOARCH }
func (h *localHost) Addr() string      { return "127.0.0.1" }
func (h *localHost) Ports() Ports      { return h.ports }
func (h *localHost) Home() Home        { return h.home }
func (h *localHost) ServerDir() string { return filepath.Join(h.dir, "server") }
func (h *localHost) TLSDir() string    { return filepath.Join(h.dir, "tls") }
func (h *localHost) Bin(name string) string {
	switch name {
	case "mash":
		return h.r.Mash
	case "mashctl":
		return h.r.Mashctl
	}
	return h.r.NatsBin
}
func (h *localHost) Exec(label string, args, env []string) Result {
	return h.r.Runner.RunIn(h.home.Dir, h.role+"-"+label, args, env, "")
}
func (h *localHost) Spawn(label string, args, env []string) (*Proc, error) {
	return h.r.StartProc(h.role+"-"+label, args, env)
}
func (h *localHost) Put(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
func (h *localHost) Get(path string) ([]byte, error) { return os.ReadFile(path) }
func (h *localHost) Remove(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("remove: %q is not an absolute path", path)
	}
	return os.Remove(path)
}
func (h *localHost) Exists(path string) bool                      { _, err := os.Stat(path); return err == nil }
func (h *localHost) WaitPortFree(port int, d time.Duration) error { return waitPortFree(port, d) }
func (h *localHost) HTTPGet(url string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Snapshot of a local machine is its directory itself: home, server and tls.
func (h *localHost) Snapshot() (string, func(), error) { return h.dir, func() {}, nil }
