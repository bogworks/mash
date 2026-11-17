package main

import (
	"bytes"
	"debug/elf"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The Podman hosts: M1 to M3 are three rootless podman containers that the test builds from this checkout and
// destroys. Nothing is installed or left on the host: every container, the network and the image carry the label
// mash-at005=<run id>, and removal is by that label.
//
//   - The image is FROM scratch (Containerfile.at005): mash, mashctl, nats-server and nodeinit as static binaries in
//     /bin. Nothing is pulled at run time.
//   - One private network with a /24 inside 10.89.0.0/16 (a third octet that no existing podman network uses); the
//     nodes have fixed addresses .11, .12 and .13, so certificates carry the node address before the node starts.
//     No port is published: the ports in Ports are private to each node's network namespace.
//   - Each node has its own filesystem (the container's own layer, under /node). Nothing is shared and no host
//     directory is mounted. Files go in and out by `podman cp` through a temporary file in the run directory that is
//     removed by its exact path right after; a seed or a JWT is never on a command line.
//   - Exec is `podman exec`. Spawn starts `podman exec -i` as a local child process of the harness; the harness records
//     that client's PID and stops it with SIGTERM to that PID only. Stopping the whole node (podman rm -f, at Close)
//     ends everything inside it, so no process inside is ever found or killed by pattern.

//go:embed Containerfile.at005
var containerfile string

const (
	labelKey = "mash-at005" // the label key on every container, the network and the image
	nodeHome = "/node"
)

// ErrNoPodman is the failure of a run on a machine without podman. It is a failure: the run tested nothing.
var ErrNoPodman = errors.New("podman is not installed: AT-005 needs rootless podman (the CI job provides it); this run did not test anything")

// Cmd is one command the Podman host runs on the machine. The runner is injectable so that tests see the command lines.
type Cmd struct {
	Argv []string
	Dir  string
	Env  []string // nil: podmanEnv
}

// CmdRunner runs a command to completion. code is -1 when the command did not start.
type CmdRunner interface {
	Run(c Cmd) (stdout, stderr string, code int)
}

type execRunner struct{}

func (execRunner) Run(c Cmd) (string, string, int) {
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = podmanEnv()
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return so.String(), se.String(), 0
	case errors.As(err, &ee):
		return so.String(), se.String(), ee.ExitCode()
	}
	return so.String(), err.Error(), -1
}

// podmanEnv is what the podman client needs from the caller, taken by name. No MASH_* variable reaches it.
func podmanEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "DBUS_SESSION_BUS_ADDRESS", "CONTAINERS_CONF", "CONTAINERS_STORAGE_CONF", "CONTAINERS_REGISTRIES_CONF", "TMPDIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// Podman is the Hosts value of `--hosts podman`. The zero value runs the real podman.
type Podman struct {
	Runner   CmdRunner
	LookPath func(string) (string, error)
	Pick     func(n int) int // a random number in [0, n): the third octet of the network
}

func (*Podman) Name() string { return "podman" }

// Preflight fails, with ErrNoPodman, when there is no podman on this machine. It runs before anything is built.
func (p *Podman) Preflight() error {
	look := p.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if _, err := look("podman"); err != nil {
		return ErrNoPodman
	}
	return nil
}

// podCtx is what the three nodes of one run share.
type podCtx struct {
	r       *Run
	rt      CmdRunner
	id      string
	network string
	image   string
	tmp     string // run directory for the temporary files of Put and Get
	n       atomic.Int64
	execFn  func(name string, argv []string) Result
}

func containerName(id string, node int) string { return "mash-at005-" + id + "-m" + strconv.Itoa(node) }

// do runs `podman args...` and returns its stdout; a non-zero exit is an error that carries podman's own words.
func (c *podCtx) do(args ...string) (string, error) {
	out, errOut, code := c.rt.Run(Cmd{Argv: append([]string{"podman"}, args...)})
	if code != 0 {
		return out, fmt.Errorf("podman %s: exit %d: %s", strings.Join(args[:min(len(args), 2)], " "), code, strings.TrimSpace(errOut))
	}
	return out, nil
}

// Open builds the image, makes the network and starts the three nodes.
func (p *Podman) Open(r *Run) ([]Host, error) {
	if err := p.Preflight(); err != nil {
		return nil, err
	}
	rt := p.Runner
	if rt == nil {
		rt = execRunner{}
	}
	c := &podCtx{r: r, rt: rt, id: r.ID, network: "mash-at005-" + r.ID, image: labelKey + ":" + r.ID}
	c.execFn = func(name string, argv []string) Result { return r.Runner.RunIn(r.Dir, name, argv, podmanEnv(), "") }
	out, err := c.do("info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		return nil, fmt.Errorf("podman is installed but does not work (podman info failed): %w", err)
	}
	if strings.TrimSpace(out) != "true" {
		return nil, fmt.Errorf("podman is not rootless (podman info says Host.Security.Rootless is %q): AT-005 runs only rootless", strings.TrimSpace(out))
	}
	// From here on something may exist: Close removes it by label, whatever happens next.
	r.OnClose(func() {
		if err := removeLabelled(rt, labelKey+"="+r.ID, r.Runner.Log); err != nil {
			r.Runner.logf("cleanup by label %s=%s: %v", labelKey, r.ID, err)
		}
	})
	ctx := filepath.Join(r.Dir, "podman-ctx")
	c.tmp = filepath.Join(r.Dir, "podman-tmp")
	for _, d := range []string{ctx, c.tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return nil, err
		}
	}
	if err := p.buildImage(c, ctx); err != nil {
		return nil, err
	}
	pick := p.Pick
	if pick == nil {
		pick = rand.IntN
	}
	octet, err := freeOctet(c, pick)
	if err != nil {
		return nil, err
	}
	if _, err := c.do("network", "create", "--subnet", fmt.Sprintf("10.89.%d.0/24", octet), "--label", labelKey+"="+r.ID, c.network); err != nil {
		return nil, err
	}
	var hs []Host
	for i, ports := range DefaultPorts {
		n := i + 1
		h := &podHost{c: c, role: "M" + strconv.Itoa(n), name: containerName(r.ID, n), addr: fmt.Sprintf("10.89.%d.%d", octet, 10+n), ports: ports,
			home: Home{Dir: nodeHome, Config: nodeHome + "/config", Keys: nodeHome + "/keys", State: nodeHome + "/state", Tmp: nodeHome + "/tmp"}}
		if _, err := c.do("run", "-d", "--name", h.name, "--network", c.network, "--ip", h.addr, "--label", labelKey+"="+r.ID, "--read-only=false", c.image, "/bin/nodeinit", "serve"); err != nil {
			return nil, err
		}
		for _, d := range []string{h.home.Dir, h.home.State, h.home.Tmp, h.ServerDir(), h.TLSDir(), "/tmp"} {
			if _, err := c.do("exec", h.name, "/bin/nodeinit", "mkdir", d); err != nil {
				return nil, err
			}
		}
		hs = append(hs, h)
	}
	return hs, nil
}

// buildImage makes the static binaries and the Containerfile in ctx and builds the image.
func (p *Podman) buildImage(c *podCtx, ctx string) error {
	r := c.r
	if err := checkStatic(r.NatsBin); err != nil {
		return err
	}
	env := append(goEnv(), "CGO_ENABLED=0")
	for _, b := range []struct{ out, pkg string }{{"mash", "./cmd/mash"}, {"mashctl", "./cmd/mashctl"}, {"nodeinit", "./scripts/liveat/nodeinit"}} {
		if _, se, code := c.rt.Run(Cmd{Argv: []string{"go", "build", "-o", filepath.Join(ctx, b.out), b.pkg}, Dir: r.Repo, Env: env}); code != 0 {
			return fmt.Errorf("CGO_ENABLED=0 go build %s: exit %d: %s", b.pkg, code, strings.TrimSpace(se))
		}
	}
	nb, err := os.ReadFile(r.NatsBin)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctx, "nats-server"), nb, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctx, "Containerfile"), []byte(containerfile), 0o644); err != nil {
		return err
	}
	_, err = c.do("build", "--label", labelKey+"="+r.ID, "-t", c.image, ctx)
	return err
}

// checkStatic fails unless the binary can run in a FROM scratch image: an ELF file for this machine's architecture
// with no program interpreter and no needed shared library. (A dynamic section as such is allowed: a static PIE has one.)
func checkStatic(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("nats-server (%s) is not an ELF binary: %v", path, err)
	}
	defer f.Close()
	for _, pr := range f.Progs {
		if pr.Type == elf.PT_INTERP {
			return fmt.Errorf("nats-server (%s) is dynamically linked (it names a program interpreter); the AT-005 image is FROM scratch and needs a static binary: use a release build or CGO_ENABLED=0 go build", path)
		}
	}
	if libs, _ := f.ImportedLibraries(); len(libs) > 0 {
		return fmt.Errorf("nats-server (%s) is dynamically linked (needs %s); the AT-005 image is FROM scratch and needs a static binary: use a release build or CGO_ENABLED=0 go build", path, strings.Join(libs, ", "))
	}
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if want != 0 && f.Machine != want {
		return fmt.Errorf("nats-server (%s) is built for %s, not for %s", path, f.Machine, runtime.GOARCH)
	}
	return nil
}

// freeOctet picks a third octet r in 1..254 such that 10.89.r.0/24 overlaps no existing podman network.
func freeOctet(c *podCtx, pick func(int) int) (int, error) {
	out, err := c.do("network", "ls", "--format", "{{.Name}}")
	if err != nil {
		return 0, err
	}
	var used []*net.IPNet
	for _, name := range strings.Fields(out) {
		o, err := c.do("network", "inspect", "--format", "{{range .Subnets}}{{.Subnet}} {{end}}", name)
		if err != nil {
			return 0, err
		}
		for _, s := range strings.Fields(o) {
			if _, n, err := net.ParseCIDR(s); err == nil {
				used = append(used, n)
			}
		}
	}
	for try := 0; try < 200; try++ {
		r := 1 + pick(254)
		_, cand, _ := net.ParseCIDR(fmt.Sprintf("10.89.%d.0/24", r))
		clash := false
		for _, u := range used {
			if u.Contains(cand.IP) || cand.Contains(u.IP) {
				clash = true
			}
		}
		if !clash {
			return r, nil
		}
	}
	return 0, errors.New("no free 10.89.x.0/24 network is left to use")
}

// removeLabelled removes every container, network and image that carries the label (filter is "key" for any value or
// "key=value" for one run), in that order, and prints what it removed. It removes by the ids and names that podman
// lists for the label: no shell, no glob, no pattern.
func removeLabelled(rt CmdRunner, filter string, w io.Writer) error {
	if w == nil {
		w = io.Discard
	}
	run := func(args ...string) (string, error) {
		o, e, code := rt.Run(Cmd{Argv: append([]string{"podman"}, args...)})
		if code != 0 {
			return o, fmt.Errorf("podman %s: exit %d: %s", args[0], code, strings.TrimSpace(e))
		}
		return o, nil
	}
	var errs []error
	lbl := "label=" + filter
	if o, err := run("ps", "-a", "--filter", lbl, "--format", "{{.ID}}"); err != nil {
		errs = append(errs, err)
	} else if ids := strings.Fields(o); len(ids) > 0 {
		if _, err := run(append([]string{"rm", "-f"}, ids...)...); err != nil {
			errs = append(errs, err)
		} else {
			for _, id := range ids {
				fmt.Fprintf(w, "removed container %s\n", id)
			}
		}
	}
	if o, err := run("network", "ls", "--filter", lbl, "--format", "{{.Name}}"); err != nil {
		errs = append(errs, err)
	} else {
		for _, n := range strings.Fields(o) {
			if _, err := run("network", "rm", "-f", n); err != nil {
				errs = append(errs, err)
			} else {
				fmt.Fprintf(w, "removed network %s\n", n)
			}
		}
	}
	if o, err := run("images", "--filter", lbl, "--format", "{{.ID}} {{.Repository}}:{{.Tag}}"); err != nil {
		errs = append(errs, err)
	} else {
		for _, l := range strings.Split(strings.TrimSpace(o), "\n") {
			f := strings.Fields(l)
			if len(f) != 2 {
				continue
			}
			ref := f[1]
			if strings.HasPrefix(ref, "<none>") {
				ref = f[0]
			}
			if _, err := run("rmi", ref); err != nil {
				errs = append(errs, err)
			} else {
				fmt.Fprintf(w, "removed image %s\n", ref)
			}
		}
	}
	return errors.Join(errs...)
}

// at005Cleanup is `liveat at005-cleanup [--label mash-at005]`: it removes the leftovers of any AT-005 run, whatever its
// run id, by the label key. It is for the always-run step of the CI job. Exit 0: nothing is left; 1: something could
// not be removed; 2: usage.
func at005Cleanup(args []string, stdout, stderr io.Writer, rt CmdRunner, look func(string) (string, error)) int {
	label := labelKey
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--label":
		label = args[1]
	default:
		fmt.Fprintln(stderr, "usage: liveat at005-cleanup [--label mash-at005]")
		return 2
	}
	if label == "" || strings.ContainsAny(label, "= \t\n*?[") {
		fmt.Fprintln(stderr, "liveat at005-cleanup: --label is a label key (no value, no pattern)")
		return 2
	}
	if look == nil {
		look = exec.LookPath
	}
	if rt == nil {
		rt = execRunner{}
	}
	if _, err := look("podman"); err != nil {
		fmt.Fprintln(stdout, "podman is not installed: nothing of AT-005 can exist here, nothing removed")
		return 0
	}
	if err := removeLabelled(rt, label, stdout); err != nil {
		fmt.Fprintln(stderr, "liveat at005-cleanup:", err)
		return 1
	}
	fmt.Fprintf(stdout, "nothing labelled %s is left\n", label)
	return 0
}

// podHost is one node.
type podHost struct {
	c     *podCtx
	role  string
	name  string // the container's name
	addr  string
	ports Ports
	home  Home
}

func (h *podHost) Role() string      { return h.role }
func (h *podHost) Facts() string     { return "linux/" + runtime.GOARCH + " (rootless podman container)" }
func (h *podHost) Addr() string      { return h.addr }
func (h *podHost) Ports() Ports      { return h.ports }
func (h *podHost) Home() Home        { return h.home }
func (h *podHost) ServerDir() string { return nodeHome + "/server" }
func (h *podHost) TLSDir() string    { return nodeHome + "/tls" }
func (h *podHost) Bin(name string) string {
	return "/bin/" + name
}

// execArgs is the command line of one command in the node: the working directory and the environment are set on the
// podman command line, and the environment is scratch values (paths and flags), never a seed or a JWT.
func execArgs(container string, args, env []string, stdin bool) []string {
	a := []string{"podman", "exec"}
	if stdin {
		a = append(a, "-i")
	}
	a = append(a, "--workdir", nodeHome)
	for _, kv := range env {
		a = append(a, "-e", kv)
	}
	a = append(a, container)
	return append(a, args...)
}

func (h *podHost) Exec(label string, args, env []string) Result {
	return h.c.execFn(h.role+"-"+label, execArgs(h.name, args, env, false))
}

// Spawn starts `podman exec -i` as a child of the harness and records its PID: that client is what Stop signals.
func (h *podHost) Spawn(label string, args, env []string) (*Proc, error) {
	return h.c.r.StartProc(h.role+"-"+label, execArgs(h.name, args, env, true), podmanEnv())
}

func (h *podHost) tool(args ...string) (string, error) {
	return h.c.do(append([]string{"exec", h.name, "/bin/nodeinit"}, args...)...)
}

func (h *podHost) tmpFile(kind string) string {
	return filepath.Join(h.c.tmp, kind+"-"+strconv.FormatInt(h.c.n.Add(1), 10))
}

// Put writes the file through a private temporary file in the run directory and `podman cp`; the temporary file is
// removed by its exact path as soon as the copy is done.
func (h *podHost) Put(path string, data []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("put: %q is not an absolute path", path)
	}
	tmp := h.tmpFile("put")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Chmod(tmp, mode.Perm()); err != nil {
		return err
	}
	if _, err := h.tool("mkdir", filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := h.c.do("cp", tmp, h.name+":"+path); err != nil {
		return err
	}
	_, err := h.tool("chmod", strconv.FormatUint(uint64(mode.Perm()), 8), path)
	return err
}

func (h *podHost) Get(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("get: %q is not an absolute path", path)
	}
	tmp := h.tmpFile("get")
	defer os.Remove(tmp)
	if _, err := h.c.do("cp", h.name+":"+path, tmp); err != nil {
		return nil, err
	}
	return os.ReadFile(tmp)
}

func (h *podHost) Remove(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("remove: %q is not an absolute path", path)
	}
	_, err := h.tool("rm", path)
	return err
}

func (h *podHost) Exists(path string) bool {
	_, err := h.tool("stat", path)
	return err == nil
}

func (h *podHost) WaitPortFree(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		if _, err := h.tool("dial", net.JoinHostPort(h.addr, strconv.Itoa(port))); err != nil {
			return nil // nothing accepted the connection
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("liveat: %s port %d is still in use", h.role, port)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (h *podHost) HTTPGet(url string) ([]byte, error) {
	o, err := h.tool("get", url)
	return []byte(o), err
}

// Snapshot copies the node's home tree (which holds config, keys, server and tls) into the run directory.
func (h *podHost) Snapshot() (string, func(), error) {
	dir, err := os.MkdirTemp(h.c.tmp, "snap-")
	if err != nil {
		return "", nil, err
	}
	done := func() { _ = os.RemoveAll(dir) } // the directory this call made
	if _, err := h.c.do("cp", h.name+":"+nodeHome, dir); err != nil {
		done()
		return "", nil, err
	}
	return filepath.Join(dir, filepath.Base(nodeHome)), done, nil
}

// Cleanup removes every podman resource labelled with the label key, whatever run made it (see at005Cleanup).
func (p *Podman) Cleanup(label string, stdout, stderr io.Writer) int {
	return at005Cleanup([]string{"--label", label}, stdout, stderr, p.Runner, p.LookPath)
}

var _ Cleaner = (*Podman)(nil)
