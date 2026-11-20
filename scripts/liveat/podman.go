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
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The Podman hosts: M1 to M3 are three rootless podman containers that the test builds from this checkout and
// destroys. Nothing is installed or left on the host: every container, the network and the image carry the label
// mash-at005=<run id>, and removal is by that label, with this run's id as the value: a run never touches another run's resources.
//
//   - The image is a pinned minimal Debian (Containerfile.at005) with bash, openssl and iproute2, the static mash,
//     mashctl, nats-server and nodeinit, the pinned nats command and the setup script, all in /usr/local/bin. The
//     base is pulled by tag when the image is built and its digest is recorded in the run record. Nodes run as the
//     non-root image user mash (uid 1000); the setup script runs inside a node, never on the host.
//   - One private network with a /24 inside 10.89.0.0/16 by default, or the /16 of --subnet-base (a third octet that no
//     podman network, host route or host address overlaps); the
//     nodes have fixed addresses .11, .12 and .13, so certificates carry the node address before the node starts.
//     No port is published: the ports in Ports are private to each node's network namespace.
//   - Each node has its own filesystem (the container's own layer, under /node). Nothing is shared and no host
//     directory is mounted. Files go in and out by `podman cp` through a temporary file in the run directory that is
//     removed by its exact path right after; a seed or a JWT is never on a command line.
//   - Exec is `podman exec`. Spawn starts `podman exec -i ... nodeinit run --pidfile /node/run/NAME.pid -- CMD` as a local
//     child of the harness: nodeinit starts CMD inside the node and writes CMD's PID to that file. Stop first runs
//     `nodeinit stop --pidfile ...` in the node (SIGTERM, then SIGKILL, to exactly the PID in the file), then ends the
//     local client by its recorded PID. Nothing inside is found or killed by pattern; removing the whole node (podman
//     rm -f, at Close) is the last resort.

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
	// SubnetBase is the private /16 the test network is taken from, "A.B" (default 10.89).
	SubnetBase string
	// HostAddrs lists the host's own interface addresses (default net.InterfaceAddrs).
	HostAddrs func() ([]net.Addr, error)
	// Fetch downloads a URL for the build context (the pinned nats command); nil: an HTTP GET.
	Fetch func(url string) ([]byte, error)
}

// defaultSubnetBase is the /16 of the test network when --subnet-base is not given.
const defaultSubnetBase = "10.89"

// validSubnetBase accepts "A.B" naming a private IPv4 /16: 10.x, 172.16 to 172.31 or 192.168.
func validSubnetBase(b string) bool {
	parts := strings.Split(b, ".")
	if len(parts) != 2 {
		return false
	}
	n := [2]int{}
	for i, s := range parts {
		v, err := strconv.Atoi(s)
		if err != nil || v < 0 || v > 255 || strconv.Itoa(v) != s {
			return false
		}
		n[i] = v
	}
	return n[0] == 10 || (n[0] == 172 && n[1] >= 16 && n[1] <= 31) || (n[0] == 192 && n[1] == 168)
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

// Version is the podman client version, or "unknown" when podman does not say.
func (p *Podman) Version() string {
	rt := p.Runner
	if rt == nil {
		rt = execRunner{}
	}
	out, _, code := rt.Run(Cmd{Argv: []string{"podman", "version", "--format", "{{.Client.Version}}"}})
	if v := strings.TrimSpace(out); code == 0 && v != "" && !strings.ContainsAny(v, "\n \t") {
		return v
	}
	return "unknown"
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
	base    string // the /16 of the test network, "A.B"
	addrs   func() ([]net.Addr, error)
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

// prepare checks podman, registers the destruction of this run's resources and builds the image: the part of Open and
// of OpenSandbox that comes before any container.
func (p *Podman) prepare(r *Run) (*podCtx, string, error) {
	if err := p.Preflight(); err != nil {
		return nil, "", err
	}
	rt := p.Runner
	if rt == nil {
		rt = execRunner{}
	}
	c := &podCtx{r: r, rt: rt, id: r.ID, network: "mash-at005-" + r.ID, image: labelKey + ":" + r.ID, base: p.SubnetBase, addrs: p.HostAddrs}
	if c.base == "" {
		c.base = defaultSubnetBase
	}
	if !validSubnetBase(c.base) {
		return nil, "", fmt.Errorf("subnet base %q is not a private IPv4 /16 (10.x, 172.16-31.x or 192.168)", c.base)
	}
	c.execFn = func(name string, argv []string) Result { return r.Runner.RunIn(r.Dir, name, argv, podmanEnv(), "") }
	out, err := c.do("info", "--format", "{{.Host.Security.Rootless}}")
	if err != nil {
		return nil, "", fmt.Errorf("podman is installed but does not work (podman info failed): %w", err)
	}
	if strings.TrimSpace(out) != "true" {
		return nil, "", fmt.Errorf("podman is not rootless (podman info says Host.Security.Rootless is %q): AT-005 runs only rootless", strings.TrimSpace(out))
	}
	// From here on something may exist: Close removes it by label, whatever happens next.
	r.OnClose(func() error { return p.destroy(rt, r) })
	ctx := filepath.Join(r.Dir, "podman-ctx")
	c.tmp = filepath.Join(r.Dir, "podman-tmp")
	for _, d := range []string{ctx, c.tmp} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return nil, "", err
		}
	}
	if err := p.buildImage(c, ctx); err != nil {
		return nil, "", err
	}
	return c, ctx, nil
}

func (c *podCtx) newHost(role, name, addr string, ports Ports) *podHost {
	return &podHost{c: c, role: role, name: name, addr: addr, ports: ports,
		home: Home{Dir: nodeHome, Config: nodeHome + "/config", Keys: nodeHome + "/keys", State: nodeHome + "/state", Tmp: nodeHome + "/tmp"}}
}

// startContainer runs one container of the image as the non-root image user and makes the directories of its home.
func (c *podCtx) startContainer(h *podHost, netArgs []string) error {
	args := append([]string{"run", "-d", "--name", h.name}, netArgs...)
	args = append(args, "--user", nodeUID, "--label", labelKey+"="+c.id, "--read-only=false", c.image, nodeBin+"/nodeinit", "serve")
	if _, err := c.do(args...); err != nil {
		return err
	}
	for _, d := range []string{h.home.Dir, h.home.State, h.home.Tmp, h.ServerDir(), h.TLSDir(), h.runDir(), "/tmp"} {
		if _, err := c.do("exec", h.name, nodeBin+"/nodeinit", "mkdir", d); err != nil {
			return err
		}
	}
	return nil
}

// sandboxName is the name of the one container of a sandboxed AT-001 to AT-003 run.
func sandboxName(id string) string { return "mash-at005-" + id + "-sbx" }

// OpenSandbox builds the image and starts ONE throwaway rootless container of it, with no network but loopback and no
// published port, as the non-root image user. The sandboxed AT-001 to AT-003 run inside it (sandbox.go).
func (p *Podman) OpenSandbox(r *Run) (*podHost, error) {
	c, _, err := p.prepare(r)
	if err != nil {
		return nil, err
	}
	h := c.newHost("SBX", sandboxName(r.ID), "127.0.0.1", Ports{})
	if err := c.startContainer(h, []string{"--network", "none"}); err != nil {
		return nil, err
	}
	return h, nil
}

// Open builds the image, makes the network and starts the three nodes.
func (p *Podman) Open(r *Run) ([]Host, error) {
	c, _, err := p.prepare(r)
	if err != nil {
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
	if _, err := c.do("network", "create", "--subnet", fmt.Sprintf("%s.%d.0/24", c.base, octet), "--label", labelKey+"="+r.ID, c.network); err != nil {
		return nil, err
	}
	var hs []Host
	for i, ports := range DefaultPorts {
		n := i + 1
		h := c.newHost("M"+strconv.Itoa(n), containerName(r.ID, n), fmt.Sprintf("%s.%d.%d", c.base, octet, 10+n), ports)
		if err := c.startContainer(h, []string{"--network", c.network, "--ip", h.addr}); err != nil {
			return nil, err
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
	if err := writeNatsCLI(ctx, p.Fetch); err != nil {
		return err
	}
	sb, err := os.ReadFile(r.scriptPath())
	if err != nil {
		return fmt.Errorf("the setup script for the node image: %w", err)
	}
	if err := os.WriteFile(filepath.Join(ctx, "mash-local-nats-setup.sh"), sb, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ctx, "Containerfile"), []byte(containerfile), 0o644); err != nil {
		return err
	}
	if _, err = c.do("build", "--label", labelKey+"="+r.ID, "-t", c.image, ctx); err != nil {
		return err
	}
	dg, _ := c.do("image", "inspect", "--format", "{{.Digest}}", baseImage)
	r.ImageLines = []string{baseImageLine(dg), natsCLILine()}
	return nil
}

// checkStatic fails unless the binary can run in any image: an ELF file for this machine's architecture
// with no program interpreter and no needed shared library. (A dynamic section as such is allowed: a static PIE has one.)
func checkStatic(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("nats-server (%s) is not an ELF binary: %v", path, err)
	}
	defer f.Close()
	for _, pr := range f.Progs {
		if pr.Type == elf.PT_INTERP {
			return fmt.Errorf("nats-server (%s) is dynamically linked (it names a program interpreter); the node image takes a static binary only: use a release build or CGO_ENABLED=0 go build", path)
		}
	}
	if libs, _ := f.ImportedLibraries(); len(libs) > 0 {
		return fmt.Errorf("nats-server (%s) is dynamically linked (needs %s); the node image takes a static binary only: use a release build or CGO_ENABLED=0 go build", path, strings.Join(libs, ", "))
	}
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if want != 0 && f.Machine != want {
		return fmt.Errorf("nats-server (%s) is built for %s, not for %s", path, f.Machine, runtime.GOARCH)
	}
	return nil
}

// hostPrefixes is every IPv4 prefix the host itself knows: its interface addresses and, when `ip` exists, its routes.
// A missing or failing `ip` is ignored. The default route is not a prefix to avoid.
func hostPrefixes(c *podCtx) []*net.IPNet {
	var out []*net.IPNet
	addrs := c.addrs
	if addrs == nil {
		addrs = net.InterfaceAddrs
	}
	if as, err := addrs(); err == nil {
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, &net.IPNet{IP: n.IP.To4().Mask(n.Mask), Mask: n.Mask})
			}
		}
	}
	so, _, code := c.rt.Run(Cmd{Argv: []string{"ip", "-4", "route", "show"}})
	if code != 0 {
		return out
	}
	for _, line := range strings.Split(so, "\n") {
		for _, f := range strings.Fields(line) {
			if f == "default" {
				break
			}
			if _, n, err := net.ParseCIDR(f); err == nil {
				out = append(out, n)
				break
			}
			if ip := net.ParseIP(f); ip != nil && ip.To4() != nil {
				out = append(out, &net.IPNet{IP: ip.To4(), Mask: net.CIDRMask(32, 32)})
				break
			}
		}
	}
	return out
}

// freeOctet picks a third octet r in 1..254 such that <base>.r.0/24 overlaps no podman network, no host route and no
// host interface address.
func freeOctet(c *podCtx, pick func(int) int) (int, error) {
	base := c.base
	if base == "" {
		base = defaultSubnetBase
	}
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
	used = append(used, hostPrefixes(c)...)
	free := func(r int) bool {
		_, cand, _ := net.ParseCIDR(fmt.Sprintf("%s.%d.0/24", base, r))
		for _, u := range used {
			if u.Contains(cand.IP) || cand.Contains(u.IP) {
				return false
			}
		}
		return true
	}
	for try := 0; try < 200; try++ {
		if r := 1 + pick(254); free(r) {
			return r, nil
		}
	}
	for r := 1; r <= 254; r++ {
		if free(r) {
			return r, nil
		}
	}
	return 0, fmt.Errorf("no free %s.x.0/24 network is left to use: every one overlaps a podman network, a host route or a host address; pass --subnet-base A.B with another private /16", base)
}

// removeLabelled removes every container, network and image that carries the label (filter is "key" for any value or
// "key=value" for one run; a run's own destroy and `at005-cleanup --run` always pass one run), in that order, and prints what it removed. It removes by the ids and names that podman
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

// destroy removes this run's containers, network and image and returns every failure joined, then checks that nothing
// labelled with this run's label (mash-at005=<run id>) remains. It never selects by the bare key, so another live
// run's resources are neither removed nor counted. It is idempotent.
func (p *Podman) destroy(rt CmdRunner, r *Run) error {
	var errs []error
	own := labelKey + "=" + r.ID
	if err := removeLabelled(rt, own, r.Runner.Log); err != nil {
		errs = append(errs, fmt.Errorf("removing what run %s made: %w", r.ID, err))
	}
	if err := verifyNoneLabelled(rt, own); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// verifyNoneLabelled lists containers, networks and images that carry the label (filter is "key=value", one run's) and
// fails if any is left (or a listing fails). The text names ids and names podman lists, never file contents or credentials.
func verifyNoneLabelled(rt CmdRunner, filter string) error {
	lbl := "label=" + filter
	var errs []error
	for _, l := range []struct {
		what string
		args []string
	}{
		{"container", []string{"ps", "-a", "--filter", lbl, "--format", "{{.ID}}"}},
		{"network", []string{"network", "ls", "--filter", lbl, "--format", "{{.Name}}"}},
		{"image", []string{"images", "--filter", lbl, "--format", "{{.ID}}"}},
	} {
		o, e, code := rt.Run(Cmd{Argv: append([]string{"podman"}, l.args...)})
		if code != 0 {
			errs = append(errs, fmt.Errorf("verifying: podman %s: exit %d: %s", l.args[0], code, strings.TrimSpace(e)))
			continue
		}
		if f := strings.Fields(o); len(f) > 0 {
			errs = append(errs, fmt.Errorf("%d %s(s) labelled %s remain: %s", len(f), l.what, filter, strings.Join(f, " ")))
		}
	}
	return errors.Join(errs...)
}

// runIDRE is the shape of Run.ID (newRunID): a UTC timestamp, a dash and eight hex digits.
var runIDRE = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

const at005CleanupUsage = "usage: liveat at005-cleanup [--run RUNID]"

// listLabelled prints what carries the label key, grouped by run id (the label's value). It removes nothing.
func listLabelled(rt CmdRunner, w io.Writer) error {
	lbl := "label=" + labelKey
	idx := `{{index .Labels "` + labelKey + `"}}`
	groups := map[string][]string{}
	var errs []error
	for _, l := range []struct {
		what string
		args []string
	}{
		{"container", []string{"ps", "-a", "--filter", lbl, "--format", idx + " {{.ID}}"}},
		{"network", []string{"network", "ls", "--filter", lbl, "--format", idx + " {{.Name}}"}},
		{"image", []string{"images", "--filter", lbl, "--format", idx + " {{.ID}}"}},
	} {
		o, e, code := rt.Run(Cmd{Argv: append([]string{"podman"}, l.args...)})
		if code != 0 {
			errs = append(errs, fmt.Errorf("podman %s: exit %d: %s", l.args[0], code, strings.TrimSpace(e)))
			continue
		}
		for _, ln := range strings.Split(o, "\n") {
			f := strings.Fields(ln)
			switch len(f) {
			case 0:
			case 1: // an empty label value
				groups["(none)"] = append(groups["(none)"], l.what+" "+f[0])
			default:
				groups[f[0]] = append(groups[f[0]], l.what+" "+f[1])
			}
		}
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(w, "run %s: %s\n", id, strings.Join(groups[id], ", "))
	}
	if len(ids) == 0 {
		fmt.Fprintf(w, "nothing labelled %s exists\n", labelKey)
	} else {
		fmt.Fprintln(w, "nothing was removed: pass --run RUNID to remove one run's resources (only if that run is dead)")
	}
	return errors.Join(errs...)
}

// at005Cleanup is `liveat at005-cleanup [--run RUNID]`. With --run it removes the resources labelled
// mash-at005=RUNID (that run's only) and verifies none of that run remain; it is for the always-run step of the CI job,
// which knows its own run id. With no argument it lists what carries the label key, any run, grouped by run id, and
// removes nothing. Exit 0: done (or listed); 1: something could not be removed or listed; 2: usage.
func at005Cleanup(args []string, stdout, stderr io.Writer, rt CmdRunner, look func(string) (string, error)) int {
	runID := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--run":
		runID = args[1]
		if !runIDRE.MatchString(runID) {
			fmt.Fprintf(stderr, "liveat at005-cleanup: %q is not a run id (like 20261117T010203Z-0a1b2c3d: no '=', spaces or patterns)\n", runID)
			return 2
		}
	default:
		fmt.Fprintln(stderr, at005CleanupUsage)
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
	if runID == "" {
		if err := listLabelled(rt, stdout); err != nil {
			fmt.Fprintln(stderr, "liveat at005-cleanup:", err)
			return 1
		}
		return 0
	}
	own := labelKey + "=" + runID
	if err := removeLabelled(rt, own, stdout); err != nil {
		fmt.Fprintln(stderr, "liveat at005-cleanup:", err)
		return 1
	}
	if err := verifyNoneLabelled(rt, own); err != nil {
		fmt.Fprintln(stderr, "liveat at005-cleanup:", err)
		return 1
	}
	fmt.Fprintf(stdout, "nothing labelled %s is left\n", own)
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
	return nodeBin + "/" + name
}

// execArgs is the command line of one command in the node: the working directory and the environment are set on the
// podman command line, and the environment is scratch values (paths and flags), never a seed or a JWT.
func execArgs(container string, args, env []string, stdin bool) []string {
	a := []string{"podman", "exec"}
	if stdin {
		a = append(a, "-i")
	}
	a = append(a, "--workdir", nodeHome, "-e", "PATH="+nodePath) // the node's PATH, never the host's
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			a = append(a, "-e", kv)
		}
	}
	a = append(a, container)
	return append(a, args...)
}

func (h *podHost) Exec(label string, args, env []string) Result {
	return h.c.execFn(h.role+"-"+label, execArgs(h.name, args, env, false))
}

func (h *podHost) runDir() string { return nodeHome + "/run" }

var pidLabelRE = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// pidfile is a unique file name for one spawned process in the node's run directory.
func (h *podHost) pidfile(label string) string {
	return h.runDir() + "/" + pidLabelRE.ReplaceAllString(label, "_") + "-" + strconv.FormatInt(h.c.n.Add(1), 10) + ".pid"
}

// spawnArgs is the command line of a spawned process: nodeinit runs it in the node and records its PID in pidfile.
func spawnArgs(container, pidfile string, args, env []string) []string {
	return execArgs(container, append([]string{nodeBin + "/nodeinit", "run", "--pidfile", pidfile, "--"}, args...), env, true)
}

// Spawn starts the process in the node under `nodeinit run` and records the PID of the local `podman exec -i` client.
// Stop first stops the process inside the node by the PID in its pidfile, then ends the client by its recorded PID.
func (h *podHost) Spawn(label string, args, env []string) (*Proc, error) {
	pf := h.pidfile(label)
	p, err := h.c.r.StartProc(h.role+"-"+label, spawnArgs(h.name, pf, args, env), podmanEnv())
	if err != nil {
		return nil, err
	}
	p.Before = func() {
		if err := h.stopInNode(pf); err != nil {
			h.c.r.Runner.logf("%s: stop in the node: %v", h.role+"-"+label, err)
		}
	}
	return p, nil
}

// stopInNode ends the process whose PID is in pidfile, inside the node (nodeinit stop: SIGTERM, then SIGKILL, to that PID).
func (h *podHost) stopInNode(pidfile string) error {
	_, err := h.c.do("exec", h.name, nodeBin+"/nodeinit", "stop", "--pidfile", pidfile, "--grace", "5s")
	return err
}

func (h *podHost) tool(args ...string) (string, error) {
	return h.c.do(append([]string{"exec", h.name, nodeBin + "/nodeinit"}, args...)...)
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

// Cleanup removes the podman resources of one run, labelled mash-at005=<runID> (see at005Cleanup).
func (p *Podman) Cleanup(runID string, stdout, stderr io.Writer) int {
	return at005Cleanup([]string{"--run", runID}, stdout, stderr, p.Runner, p.LookPath)
}

var _ Cleaner = (*Podman)(nil)
