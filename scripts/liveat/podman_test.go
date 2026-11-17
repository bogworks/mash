package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRT records every command and answers from a table, so the Podman host is tested without podman.
type fakeRT struct {
	mu    sync.Mutex
	calls []Cmd
	// reply may return handled=false to get the default answer (success, no output).
	reply func(c Cmd) (out, errOut string, code int, handled bool)
}

func (f *fakeRT) Run(c Cmd) (string, string, int) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.reply != nil {
		if o, e, code, ok := f.reply(c); ok {
			return o, e, code
		}
	}
	return "", "", 0
}

func (f *fakeRT) lines() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c.Argv, " "))
	}
	return out
}

func (f *fakeRT) has(sub string) bool {
	for _, l := range f.lines() {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

var (
	staticOnce sync.Once
	staticBin  string
)

// staticElf is a statically linked ELF file made by CGO_ENABLED=0 go build, standing in for nats-server.
func staticElf(t *testing.T) string {
	t.Helper()
	staticOnce.Do(func() {
		dir, err := os.MkdirTemp("", "liveat-static-")
		if err != nil {
			return
		}
		src := filepath.Join(dir, "main.go")
		_ = os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o600)
		out := filepath.Join(dir, "nats-server")
		c := exec.Command("go", "build", "-o", out, src)
		c.Dir = dir
		c.Env = append(goEnv(), "CGO_ENABLED=0", "GOFLAGS=")
		if b, err := c.CombinedOutput(); err != nil {
			t.Logf("go build: %v %s", err, b)
			return
		}
		staticBin = out
	})
	if staticBin == "" {
		t.Skip("cannot build a static test binary here")
	}
	return staticBin
}

const testRunID = "20261117T010203Z-0a1b2c3d"

func newPodRun(t *testing.T) *Run {
	t.Helper()
	d := t.TempDir()
	return &Run{Dir: d, Repo: "/repo", ID: testRunID, NatsBin: staticElf(t), Runner: &Runner{Out: d, Cwd: d}, pids: map[int]string{}}
}

func podmanFake(reply func(Cmd) (string, string, int, bool)) *fakeRT {
	base := func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(l, "podman info"):
			return "true\n", "", 0, true
		case strings.HasPrefix(l, "podman network ls --format"):
			return "podman\nother\n", "", 0, true
		case strings.HasPrefix(l, "podman network inspect") && strings.HasSuffix(l, " podman"):
			return "10.88.0.0/16 \n", "", 0, true
		case strings.HasPrefix(l, "podman network inspect"):
			return "10.89.5.0/24 \n", "", 0, true
		}
		if reply != nil {
			return reply(c)
		}
		return "", "", 0, false
	}
	return &fakeRT{reply: base}
}

func openPodman(t *testing.T, f *fakeRT, r *Run) []Host {
	t.Helper()
	picks := []int{4, 9} // third octet 5 clashes with an existing network, 10 is free
	p := &Podman{Runner: f, LookPath: func(string) (string, error) { return "/usr/bin/podman", nil },
		Pick: func(int) int { v := picks[0]; picks = picks[1:]; return v }}
	hs, err := p.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestPodmanOpenCommandLines(t *testing.T) {
	r := newPodRun(t)
	f := podmanFake(nil)
	hs := openPodman(t, f, r)
	id := testRunID
	lbl := "mash-at005=" + id
	ctx := filepath.Join(r.Dir, "podman-ctx")
	for _, want := range []string{
		"podman info --format {{.Host.Security.Rootless}}",
		"podman build --label " + lbl + " -t mash-at005:" + id + " " + ctx,
		"podman network create --subnet 10.89.10.0/24 --label " + lbl + " mash-at005-" + id,
		"podman run -d --name mash-at005-" + id + "-m1 --network mash-at005-" + id + " --ip 10.89.10.11 --label " + lbl + " --read-only=false mash-at005:" + id + " /bin/nodeinit serve",
		"podman run -d --name mash-at005-" + id + "-m2 --network mash-at005-" + id + " --ip 10.89.10.12 --label " + lbl,
		"podman run -d --name mash-at005-" + id + "-m3 --network mash-at005-" + id + " --ip 10.89.10.13 --label " + lbl,
		"podman exec mash-at005-" + id + "-m3 /bin/nodeinit mkdir /node/tls",
	} {
		if !f.has(want) {
			t.Errorf("missing command %q in:\n%s", want, strings.Join(f.lines(), "\n"))
		}
	}
	for _, l := range f.lines() {
		for _, bad := range []string{" -v ", "--volume", "--mount", " -p ", "--publish", "--privileged", "--pull"} {
			if strings.Contains(l+" ", bad) {
				t.Errorf("%q has %q", l, bad)
			}
		}
	}
	// the three binaries are built static from the checkout; nats-server is copied; the Containerfile is the template.
	builds := 0
	for _, c := range f.calls {
		if len(c.Argv) > 1 && c.Argv[0] == "go" && c.Argv[1] == "build" {
			builds++
			if c.Dir != "/repo" || !strings.Contains(strings.Join(c.Env, " "), "CGO_ENABLED=0") {
				t.Errorf("go build %v: dir %q env", c.Argv, c.Dir)
			}
		}
	}
	if builds != 3 {
		t.Errorf("%d go builds", builds)
	}
	if b, err := os.ReadFile(filepath.Join(ctx, "Containerfile")); err != nil || string(b) != containerfile ||
		!strings.Contains(containerfile, "FROM scratch") || !strings.Contains(containerfile, "ENTRYPOINT []") {
		t.Errorf("Containerfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ctx, "nats-server")); err != nil {
		t.Error("nats-server is not in the build context")
	}
	// the nodes
	if len(hs) != 3 {
		t.Fatal(len(hs))
	}
	seen := map[string]bool{}
	for i, h := range hs {
		n := string(rune('1' + i))
		if h.Role() != "M"+n || h.Addr() != "10.89.10.1"+n || h.Home().Dir != "/node" || h.Bin("mash") != "/bin/mash" ||
			h.ServerDir() != "/node/server" || h.TLSDir() != "/node/tls" || h.Ports() != DefaultPorts[i] {
			t.Errorf("node %d: %s %s %+v", i, h.Role(), h.Addr(), h.Ports())
		}
		seen[h.Addr()] = true
	}
	if len(seen) != 3 {
		t.Error("addresses are not distinct")
	}
	// SANs of the certificates come from the node address.
	if got := SANsFor(hs[1].Addr()); len(got) != 1 || got[0] != "10.89.10.12" {
		t.Errorf("SANs %v", got)
	}
}

func TestPodmanCleanupByLabel(t *testing.T) {
	r := newPodRun(t)
	f := podmanFake(func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(l, "podman ps"):
			return "aaa111\nbbb222\nccc333\n", "", 0, true
		case strings.HasPrefix(l, "podman network ls --filter"):
			return "mash-at005-" + testRunID + "\n", "", 0, true
		case strings.HasPrefix(l, "podman images"):
			return "dd44 localhost/mash-at005:" + testRunID + "\n", "", 0, true
		}
		return "", "", 0, false
	})
	openPodman(t, f, r)
	f.calls = nil
	r.Close() // runs the registered cleanup
	lbl := "label=mash-at005=" + testRunID
	want := []string{
		"podman ps -a --filter " + lbl + " --format {{.ID}}",
		"podman rm -f aaa111 bbb222 ccc333",
		"podman network ls --filter " + lbl + " --format {{.Name}}",
		"podman network rm -f mash-at005-" + testRunID,
		"podman images --filter " + lbl + " --format {{.ID}} {{.Repository}}:{{.Tag}}",
		"podman rmi localhost/mash-at005:" + testRunID,
	}
	got := f.lines()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cleanup commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if strings.Contains(l, "kill") || strings.Contains(l, "prune") || strings.Contains(l, "*") || strings.Contains(l, "rm -af") {
			t.Errorf("unsafe cleanup command %q", l)
		}
	}
	if _, err := os.Stat(r.Dir); !os.IsNotExist(err) {
		t.Error("the run directory is left")
	}
}

func TestAT005CleanupSubcommand(t *testing.T) {
	f := &fakeRT{reply: func(c Cmd) (string, string, int, bool) {
		if strings.HasPrefix(strings.Join(c.Argv, " "), "podman ps") {
			return "x1\n", "", 0, true
		}
		return "", "", 0, false
	}}
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	var so, se strings.Builder
	if code := at005Cleanup([]string{"--label", "mash-at005"}, &so, &se, f, look); code != 0 {
		t.Fatal(code, se.String())
	}
	if !strings.Contains(so.String(), "removed container x1") {
		t.Errorf("it does not say what it removed: %q", so.String())
	}
	if !f.has("podman ps -a --filter label=mash-at005 --format {{.ID}}") || !f.has("podman rm -f x1") {
		t.Errorf("commands %v", f.lines())
	}
	for _, bad := range [][]string{{"--label", "a=b"}, {"--label", "x*"}, {"--label"}, {"stray"}} {
		if at005Cleanup(bad, &so, &se, f, look) != 2 {
			t.Errorf("%v accepted", bad)
		}
	}
	so.Reset()
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	g := &fakeRT{}
	if at005Cleanup(nil, &so, &se, g, none) != 0 || len(g.calls) != 0 {
		t.Error("with no podman it must run nothing and succeed")
	}
	// a failure to remove is exit 1
	bad := &fakeRT{reply: func(Cmd) (string, string, int, bool) { return "", "boom", 125, true }}
	if at005Cleanup(nil, &so, &se, bad, look) != 1 {
		t.Error("a failed removal is not exit 1")
	}
}

func TestPodmanNoRuntimeFails(t *testing.T) {
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	f := &fakeRT{}
	_, err := (&Podman{Runner: f, LookPath: none}).Open(newPodRun(t))
	want := "podman is not installed: AT-005 needs rootless podman (the CI job provides it); this run did not test anything"
	if err == nil || err.Error() != want {
		t.Fatalf("%v", err)
	}
	if len(f.calls) != 0 {
		t.Error("it ran commands without podman")
	}
	// the command fails (non-zero) and does not skip; nothing is built first
	t.Setenv("PATH", t.TempDir())
	var so, se strings.Builder
	code := realMain([]string{"at005", "--hosts", "podman", "--run-by", "manual", "--host-label", "t"}, &so, &se)
	if code == 0 || code == 3 || !strings.Contains(se.String(), want) {
		t.Fatalf("exit %d: %q", code, se.String())
	}
}

func TestPodmanNotRootlessFails(t *testing.T) {
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	f := &fakeRT{reply: func(Cmd) (string, string, int, bool) { return "false\n", "", 0, true }}
	if _, err := (&Podman{Runner: f, LookPath: look}).Open(newPodRun(t)); err == nil || !strings.Contains(err.Error(), "not rootless") {
		t.Fatalf("%v", err)
	}
	f = &fakeRT{reply: func(Cmd) (string, string, int, bool) { return "", "cannot connect", 125, true }}
	if _, err := (&Podman{Runner: f, LookPath: look}).Open(newPodRun(t)); err == nil || !strings.Contains(err.Error(), "podman info failed") {
		t.Fatalf("%v", err)
	}
	for _, l := range f.lines() {
		if strings.Contains(l, "build") || strings.Contains(l, " run ") {
			t.Errorf("went on after a failed check: %s", l)
		}
	}
}

func TestPodmanDynamicNatsServerFails(t *testing.T) {
	ls, err := exec.LookPath("ls")
	if err != nil {
		t.Skip("no ls")
	}
	if f, err := os.Open(ls); err == nil {
		defer f.Close()
	}
	if err := checkStatic(ls); err == nil {
		t.Skip("ls is static here")
	}
	r := newPodRun(t)
	r.NatsBin = ls
	f := podmanFake(nil)
	p := &Podman{Runner: f, LookPath: func(string) (string, error) { return "/usr/bin/podman", nil }, Pick: func(int) int { return 0 }}
	_, err = p.Open(r)
	if err == nil || !strings.Contains(err.Error(), "dynamically linked") || !strings.Contains(err.Error(), "FROM scratch") {
		t.Fatalf("%v", err)
	}
	if f.has("podman build") {
		t.Error("it built an image with a dynamic nats-server")
	}
	if err := checkStatic(staticElf(t)); err != nil {
		t.Errorf("a static binary is refused: %v", err)
	}
	if err := checkStatic(filepath.Join(r.Dir, "missing")); err == nil {
		t.Error("a missing file passes")
	}
}

func TestPodmanFreeOctetAvoidsExistingNetworks(t *testing.T) {
	f := podmanFake(nil)
	c := &podCtx{rt: f}
	// 10.88.0.0/16 and 10.89.5.0/24 exist: picks 4 (octet 5) is refused, 0 (octet 1) is free.
	picks := []int{4, 0}
	r, err := freeOctet(c, func(int) int { v := picks[0]; picks = picks[1:]; return v })
	if err != nil || r != 1 {
		t.Fatal(r, err)
	}
	// a wide existing network covering 10.89.0.0/16 leaves nothing
	f = podmanFake(func(c Cmd) (string, string, int, bool) { return "", "", 0, false })
	f.reply = func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		if strings.HasPrefix(l, "podman network ls") {
			return "wide\n", "", 0, true
		}
		return "10.0.0.0/8 \n", "", 0, true
	}
	if _, err := freeOctet(&podCtx{rt: f}, func(int) int { return 0 }); err == nil {
		t.Fatal("a /8 network was not seen as a clash")
	}
}

func TestPodmanExecAndSpawnArgs(t *testing.T) {
	a := execArgs("c1", []string{"/bin/mash", "status"}, []string{"MASH_URL=tls://10.89.1.11:14222", "HOME=/node"}, false)
	want := "podman exec --workdir /node -e MASH_URL=tls://10.89.1.11:14222 -e HOME=/node c1 /bin/mash status"
	if strings.Join(a, " ") != want {
		t.Fatalf("%v", a)
	}
	if b := execArgs("c1", []string{"/bin/mash", "serve"}, nil, true); b[2] != "-i" {
		t.Fatalf("spawn keeps stdin open: %v", b)
	}
	r := newPodRun(t)
	f := podmanFake(nil)
	hs := openPodman(t, f, r)
	var got []string
	hs[0].(*podHost).c.execFn = func(name string, argv []string) Result { got = append([]string{name}, argv...); return Result{Code: 7} }
	res := hs[0].Exec("x", []string{"/bin/mashctl", "list"}, []string{"K=V"})
	if res.Code != 7 || got[0] != "M1-x" || !strings.Contains(strings.Join(got, " "), "-e K=V mash-at005-"+testRunID+"-m1 /bin/mashctl list") {
		t.Fatalf("%v %+v", got, res)
	}
}

func TestPodmanCopyInAndOut(t *testing.T) {
	r := newPodRun(t)
	secret := "SU" + strings.Repeat("A", 54)
	var atCp []byte
	var cpMode os.FileMode
	f := podmanFake(func(c Cmd) (string, string, int, bool) {
		if len(c.Argv) > 3 && c.Argv[1] == "cp" {
			src, dst := c.Argv[2], c.Argv[3]
			if !strings.Contains(src, ":") { // into the container
				atCp, _ = os.ReadFile(src)
				st, _ := os.Stat(src)
				cpMode = st.Mode().Perm()
				if filepath.Dir(src) != filepath.Join(r.Dir, "podman-tmp") {
					return "", "tmp outside the run directory", 1, true
				}
			} else { // out of the container
				_ = os.WriteFile(dst, []byte("from the node"), 0o600)
			}
			return "", "", 0, true
		}
		return "", "", 0, false
	})
	hs := openPodman(t, f, r)
	f.calls = nil
	m2 := hs[1]
	if err := m2.Put("/node/keys/s.seed", []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	if string(atCp) != secret || cpMode != 0o600 {
		t.Errorf("the temporary file held %q mode %v", atCp, cpMode)
	}
	b, err := m2.Get("/node/x")
	if err != nil || string(b) != "from the node" {
		t.Fatalf("%q %v", b, err)
	}
	c2 := "mash-at005-" + testRunID + "-m2"
	for _, want := range []string{"podman exec " + c2 + " /bin/nodeinit mkdir /node/keys", "podman cp ", c2 + ":/node/keys/s.seed", "/bin/nodeinit chmod 600 /node/keys/s.seed", "podman cp " + c2 + ":/node/x "} {
		if !f.has(want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(f.lines(), "\n"))
		}
	}
	for _, l := range f.lines() {
		if strings.Contains(l, secret) || strings.Contains(l, "SUAAAA") {
			t.Fatalf("a seed is on a command line: %s", l)
		}
	}
	left, _ := os.ReadDir(filepath.Join(r.Dir, "podman-tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if err := m2.Put("relative", nil, 0o600); err == nil {
		t.Error("Put took a relative path")
	}
	if err := m2.Remove("relative"); err == nil {
		t.Error("Remove took a relative path")
	}
	f.calls = nil
	if err := m2.Remove("/node/adopt-in/sys.creds"); err != nil || !f.has("/bin/nodeinit rm /node/adopt-in/sys.creds") {
		t.Errorf("remove: %v %v", err, f.lines())
	}
	if !m2.Exists("/x") || !f.has("/bin/nodeinit stat /x") {
		t.Error("exists")
	}
	if _, err := m2.HTTPGet("http://10.89.10.11:18222/routez"); err != nil || !f.has("/bin/nodeinit get http://10.89.10.11:18222/routez") {
		t.Error("http get is done from inside the node")
	}
}

func TestPodmanSnapshotAndPortFree(t *testing.T) {
	r := newPodRun(t)
	f := podmanFake(func(c Cmd) (string, string, int, bool) {
		if len(c.Argv) > 3 && c.Argv[1] == "cp" {
			_ = os.MkdirAll(filepath.Join(c.Argv[3], "node"), 0o700)
			return "", "", 0, true
		}
		if len(c.Argv) > 5 && c.Argv[len(c.Argv)-2] == "dial" {
			return "", "refused", 1, true
		}
		return "", "", 0, false
	})
	hs := openPodman(t, f, r)
	dir, done, err := hs[0].Snapshot()
	if err != nil || filepath.Base(dir) != "node" {
		t.Fatal(dir, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	done()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the copy is left")
	}
	if err := hs[2].WaitPortFree(14224, 0); err != nil || !f.has("/bin/nodeinit dial 10.89.10.13:14224") {
		t.Errorf("port free: %v", err)
	}
}

func TestPodmanIsAHostsValue(t *testing.T) {
	var _ Hosts = &Podman{}
	var _ Preflighter = &Podman{}
	var _ Host = &podHost{}
	var _ Host = &localHost{}
}
