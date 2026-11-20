package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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

// TestMain stands in for the download of the nats command: the pinned asset of this architecture is replaced by a
// small zip made here, so no unit test touches the network.
func TestMain(m *testing.M) {
	zb := fakeNatsZip()
	sum := sha256.Sum256(zb)
	natsCLIAssets[runtime.GOARCH] = natsCLIAsset{URL: "https://example.invalid/nats.zip", SHA256: hex.EncodeToString(sum[:])}
	httpFetchDefault = func(url string) ([]byte, error) { return zb, nil }
	os.Exit(m.Run())
}

func fakeNatsZip() []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("nats-0.0.0-linux/nats")
	_, _ = w.Write([]byte("#!/bin/sh\n"))
	_ = zw.Close()
	return b.Bytes()
}

func newPodRun(t *testing.T) *Run {
	t.Helper()
	d := t.TempDir()
	script := filepath.Join(d, "setup.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(d, "repo")
	for f, m := range map[string]os.FileMode{"plugin/.mcp.json": 0o644, "plugin/hooks/hooks.json": 0o644, "plugin/scripts/mash": 0o755} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x "+f), m); err != nil {
			t.Fatal(err)
		}
	}
	return &Run{Dir: d, Repo: repo, ID: testRunID, NatsBin: staticElf(t), SetupScript: script, Runner: &Runner{Out: d, Cwd: d}, pids: map[int]string{}}
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
	linuxOnly(t)
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
		"podman run -d --name mash-at005-" + id + "-m1 --network mash-at005-" + id + " --ip 10.89.10.11 --user 1000:1000 --label " + lbl + " --read-only=false mash-at005:" + id + " /usr/local/bin/nodeinit serve",
		"podman run -d --name mash-at005-" + id + "-m2 --network mash-at005-" + id + " --ip 10.89.10.12 --user 1000:1000 --label " + lbl,
		"podman run -d --name mash-at005-" + id + "-m3 --network mash-at005-" + id + " --ip 10.89.10.13 --user 1000:1000 --label " + lbl,
		"podman exec mash-at005-" + id + "-m3 /usr/local/bin/nodeinit mkdir /node/tls",
		"podman exec mash-at005-" + id + "-m3 /usr/local/bin/nodeinit mkdir /node/run",
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
			if c.Dir != r.Repo || !strings.Contains(strings.Join(c.Env, " "), "CGO_ENABLED=0") {
				t.Errorf("go build %v: dir %q env", c.Argv, c.Dir)
			}
		}
	}
	if builds != 3 {
		t.Errorf("%d go builds", builds)
	}
	if b, err := os.ReadFile(filepath.Join(ctx, "Containerfile")); err != nil || string(b) != containerfile ||
		!strings.Contains(containerfile, "\nFROM "+baseImage+"\n") || !strings.Contains(containerfile, "\nUSER mash\n") || !strings.Contains(containerfile, "ENTRYPOINT []") {
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
		if h.Role() != "M"+n || h.Addr() != "10.89.10.1"+n || h.Home().Dir != "/node" || h.Bin("mash") != "/usr/local/bin/mash" ||
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
	linuxOnly(t)
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
		"podman ps -a --filter " + lbl + " --format {{.ID}}",
		"podman network ls --filter " + lbl + " --format {{.Name}}",
		"podman images --filter " + lbl + " --format {{.ID}}",
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
	linuxOnly(t)
	removed := false
	f := &fakeRT{reply: func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		if l == "podman rm -f x1" {
			removed = true
		}
		if strings.HasPrefix(l, "podman ps -a --filter label=mash-at005="+testRunID+" ") && !removed {
			return "x1\n", "", 0, true
		}
		return "", "", 0, false
	}}
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	var so, se strings.Builder
	if code := at005Cleanup([]string{"--run", testRunID}, &so, &se, f, look); code != 0 {
		t.Fatal(code, se.String())
	}
	if !strings.Contains(so.String(), "removed container x1") {
		t.Errorf("it does not say what it removed: %q", so.String())
	}
	if !f.has("podman ps -a --filter label=mash-at005="+testRunID+" --format {{.ID}}") || !f.has("podman rm -f x1") {
		t.Errorf("commands %v", f.lines())
	}
	so.Reset()
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	g := &fakeRT{}
	if at005Cleanup(nil, &so, &se, g, none) != 0 || len(g.calls) != 0 || !strings.Contains(so.String(), "podman is not installed") {
		t.Error("with no podman it must run nothing and succeed")
	}
	// a failure to remove is exit 1
	bad := &fakeRT{reply: func(Cmd) (string, string, int, bool) { return "", "boom", 125, true }}
	if at005Cleanup([]string{"--run", testRunID}, &so, &se, bad, look) != 1 {
		t.Error("a failed removal is not exit 1")
	}
}

func TestPodmanNoRuntimeFails(t *testing.T) {
	linuxOnly(t)
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
	linuxOnly(t)
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
	linuxOnly(t)
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
	if err == nil || !strings.Contains(err.Error(), "dynamically linked") || !strings.Contains(err.Error(), "static binary") {
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
	linuxOnly(t)
	f := podmanFake(nil)
	c := &podCtx{rt: f, addrs: noAddrs}
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
	if _, err := freeOctet(&podCtx{rt: f, addrs: noAddrs}, func(int) int { return 0 }); err == nil {
		t.Fatal("a /8 network was not seen as a clash")
	}
}

func TestPodmanExecAndSpawnArgs(t *testing.T) {
	linuxOnly(t)
	a := execArgs("c1", []string{"/usr/local/bin/mash", "status"}, []string{"MASH_SERVER=tls://10.89.1.11:14222", "HOME=/node"}, false)
	want := "podman exec --workdir /node -e PATH=/usr/local/bin:/usr/bin:/bin -e MASH_SERVER=tls://10.89.1.11:14222 -e HOME=/node c1 /usr/local/bin/mash status"
	if strings.Join(a, " ") != want {
		t.Fatalf("%v", a)
	}
	if b := execArgs("c1", []string{"/usr/local/bin/mash", "serve"}, nil, true); b[2] != "-i" {
		t.Fatalf("spawn keeps stdin open: %v", b)
	}
	r := newPodRun(t)
	f := podmanFake(nil)
	hs := openPodman(t, f, r)
	var got []string
	hs[0].(*podHost).c.execFn = func(name string, argv []string) Result { got = append([]string{name}, argv...); return Result{Code: 7} }
	res := hs[0].Exec("x", []string{"/usr/local/bin/mashctl", "endpoint", "list"}, []string{"K=V"})
	if res.Code != 7 || got[0] != "M1-x" || !strings.Contains(strings.Join(got, " "), "-e K=V mash-at005-"+testRunID+"-m1 /usr/local/bin/mashctl endpoint list") {
		t.Fatalf("%v %+v", got, res)
	}
}

func TestPodmanSpawnRunsUnderNodeinitAndStopsByPidfile(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	f := podmanFake(nil)
	hs := openPodman(t, f, r)
	h := hs[0].(*podHost)
	pf1, pf2 := h.pidfile("serve a/b"), h.pidfile("serve a/b")
	if pf1 == pf2 || !strings.HasPrefix(pf1, "/node/run/serve_a_b-") || !strings.HasSuffix(pf1, ".pid") {
		t.Fatalf("%q %q", pf1, pf2)
	}
	got := strings.Join(spawnArgs("c1", pf1, []string{"/usr/local/bin/mash", "serve"}, []string{"K=V"}), " ")
	want := "podman exec -i --workdir /node -e PATH=/usr/local/bin:/usr/bin:/bin -e K=V c1 /usr/local/bin/nodeinit run --pidfile " + pf1 + " -- /usr/local/bin/mash serve"
	if got != want {
		t.Fatalf("%s", got)
	}
	f.calls = nil
	if err := h.stopInNode(pf1); err != nil {
		t.Fatal(err)
	}
	if !f.has("podman exec mash-at005-" + testRunID + "-m1 /usr/local/bin/nodeinit stop --pidfile " + pf1 + " --grace 5s") {
		t.Fatalf("%s", strings.Join(f.lines(), "\n"))
	}
}

func TestPodmanCopyInAndOut(t *testing.T) {
	linuxOnly(t)
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
	for _, want := range []string{"podman exec " + c2 + " /usr/local/bin/nodeinit mkdir /node/keys", "podman cp ", c2 + ":/node/keys/s.seed", "/usr/local/bin/nodeinit chmod 600 /node/keys/s.seed", "podman cp " + c2 + ":/node/x "} {
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
	if err := m2.Remove("/node/adopt-in/sys.creds"); err != nil || !f.has("/usr/local/bin/nodeinit rm /node/adopt-in/sys.creds") {
		t.Errorf("remove: %v %v", err, f.lines())
	}
	if !m2.Exists("/x") || !f.has("/usr/local/bin/nodeinit stat /x") {
		t.Error("exists")
	}
	if _, err := m2.HTTPGet("http://10.89.10.11:18222/routez"); err != nil || !f.has("/usr/local/bin/nodeinit get http://10.89.10.11:18222/routez") {
		t.Error("http get is done from inside the node")
	}
}

func TestPodmanSnapshotAndPortFree(t *testing.T) {
	linuxOnly(t)
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
	if err := hs[2].WaitPortFree(14224, 0); err != nil || !f.has("/usr/local/bin/nodeinit dial 10.89.10.13:14224") {
		t.Errorf("port free: %v", err)
	}
}

func TestPodmanIsAHostsValue(t *testing.T) {
	linuxOnly(t)
	var _ Hosts = &Podman{}
	var _ Preflighter = &Podman{}
	var _ Host = &podHost{}
	var _ Host = &localHost{}
}

func noAddrs() ([]net.Addr, error) { return nil, nil }

func routeFake(routes string) *fakeRT {
	return podmanFake(func(c Cmd) (string, string, int, bool) {
		if strings.HasPrefix(strings.Join(c.Argv, " "), "ip -4 route show") {
			return routes, "", 0, true
		}
		return "", "", 0, false
	})
}

func TestPodmanFreeOctetAvoidsHostRoutes(t *testing.T) {
	linuxOnly(t)
	// a route for the whole default /16 leaves nothing under it, with a message that names the flag
	f := routeFake("default via 192.0.2.1 dev eth0\n10.89.0.0/16 dev tun0 scope link\n")
	_, err := freeOctet(&podCtx{rt: f, addrs: noAddrs}, func(int) int { return 0 })
	if err == nil || !strings.Contains(err.Error(), "--subnet-base") || !strings.Contains(err.Error(), "10.89.x.0/24") {
		t.Fatalf("%v", err)
	}
	// the same table leaves another base free
	r, err := freeOctet(&podCtx{rt: f, addrs: noAddrs, base: "172.20"}, func(int) int { return 6 })
	if err != nil || r != 7 {
		t.Fatal(r, err)
	}
	// a single /24 route and a host route are skipped (a fallback scan finds the first free octet)
	f = routeFake("10.89.1.0/24 dev x\n10.89.2.9 dev y\nblackhole 10.89.3.0/24\n")
	r, err = freeOctet(&podCtx{rt: f, addrs: noAddrs}, func(int) int { return 0 })
	if err != nil || r != 4 {
		t.Fatal(r, err)
	}
	// no ip command: ignored
	f = podmanFake(func(c Cmd) (string, string, int, bool) {
		if c.Argv[0] == "ip" {
			return "", "", -1, true
		}
		return "", "", 0, false
	})
	if _, err := freeOctet(&podCtx{rt: f, addrs: noAddrs}, func(int) int { return 0 }); err != nil {
		t.Fatal(err)
	}
}

func TestPodmanFreeOctetAvoidsHostAddresses(t *testing.T) {
	linuxOnly(t)
	f := podmanFake(nil)
	addrs := func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("10.89.1.77"), Mask: net.CIDRMask(24, 32)}, &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}, nil
	}
	picks := []int{0, 1}
	r, err := freeOctet(&podCtx{rt: f, addrs: addrs}, func(int) int { v := picks[0]; picks = picks[1:]; return v })
	if err != nil || r != 2 {
		t.Fatal(r, err)
	}
}

func TestSubnetBaseValidation(t *testing.T) {
	linuxOnly(t)
	for _, ok := range []string{"10.89", "10.0", "172.16", "172.31", "192.168"} {
		if !validSubnetBase(ok) {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"", "10", "10.89.1", "11.1", "172.15", "172.32", "192.169", "10.256", "10.x", "10.08", "8.8"} {
		if validSubnetBase(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
	if _, err := parseAt005([]string{"--hosts", "podman", "--run-by", "manual", "--host-label", "x", "--subnet-base", "8.8"}); err == nil {
		t.Error("a public base is accepted")
	}
	if o, err := parseAt005([]string{"--hosts", "podman", "--run-by", "manual", "--host-label", "x", "--subnet-base", "172.20"}); err != nil || o.subnetBase != "172.20" {
		t.Error(o, err)
	}
	if _, err := parseAt005([]string{"--hosts", "local", "--subnet-base", "10.1"}); err == nil {
		t.Error("--subnet-base with local")
	}
	var so, se strings.Builder
	if code := at005Main([]string{"--hosts", "podman", "--run-by", "manual", "--host-label", "x", "--subnet-base", "1.2"}, &so, &se); code != 2 {
		t.Errorf("exit %d", code)
	}
	if _, err := (&Podman{Runner: podmanFake(nil), LookPath: func(string) (string, error) { return "/usr/bin/podman", nil }, SubnetBase: "8.8"}).Open(newPodRun(t)); err == nil || !strings.Contains(err.Error(), "private") {
		t.Errorf("%v", err)
	}
}

func TestAT005CleanupBadRunID(t *testing.T) {
	linuxOnly(t)
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	f := &fakeRT{}
	var so, se strings.Builder
	for _, bad := range [][]string{{"--run", "mash-at005"}, {"--run", "mash-at005=" + testRunID}, {"--run", testRunID + " x"}, {"--run", "2026*"},
		{"--run", ""}, {"--run", "a=b"}, {"--run", testRunID + "\n"}, {"--run", "[a-z]"}, {"--run"}, {"--label", "mash-at005"}, {"stray"}, {"--run", testRunID, "x"}} {
		if at005Cleanup(bad, &so, &se, f, look) != 2 {
			t.Errorf("%q accepted", bad)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("it ran %v", f.lines())
	}
}

const otherRunID = "20261117T999999Z-ffffffff"

func TestAT005CleanupRunNeverTouchesOtherRun(t *testing.T) {
	linuxOnly(t)
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	f := &fakeRT{}
	var so, se strings.Builder
	if code := at005Cleanup([]string{"--run", testRunID}, &so, &se, f, look); code != 0 {
		t.Fatal(code, se.String())
	}
	if len(f.calls) == 0 {
		t.Fatal("it ran nothing")
	}
	for _, l := range f.lines() {
		if strings.Contains(l, otherRunID) {
			t.Errorf("touches another run: %s", l)
		}
		if strings.Contains(l, "--filter") && !strings.Contains(l, "--filter label=mash-at005="+testRunID+" ") {
			t.Errorf("a filter that is not this run's: %s", l)
		}
	}
}

func TestAT005CleanupNoArgListsOnly(t *testing.T) {
	linuxOnly(t)
	look := func(string) (string, error) { return "/usr/bin/podman", nil }
	f := &fakeRT{reply: func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(l, "podman ps"):
			return testRunID + " c1\n" + otherRunID + " c2\n", "", 0, true
		case strings.HasPrefix(l, "podman network ls"):
			return otherRunID + " mash-at005-" + otherRunID + "\n", "", 0, true
		}
		return "", "", 0, false
	}}
	var so, se strings.Builder
	if code := at005Cleanup(nil, &so, &se, f, look); code != 0 {
		t.Fatal(code, se.String())
	}
	for _, l := range f.lines() {
		if strings.HasPrefix(l, "podman rm") || strings.HasPrefix(l, "podman rmi") || strings.HasPrefix(l, "podman network rm") ||
			!(strings.HasPrefix(l, "podman ps") || strings.HasPrefix(l, "podman network ls") || strings.HasPrefix(l, "podman images")) {
			t.Errorf("no-arg run issued %q", l)
		}
		if !strings.Contains(l, "--filter label=mash-at005 ") {
			t.Errorf("listing is not by the key: %q", l)
		}
	}
	out := so.String()
	for _, want := range []string{"run " + testRunID + ": container c1", "run " + otherRunID + ": container c2, network mash-at005-" + otherRunID, "--run"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "removed") && !strings.Contains(out, "nothing was removed") {
		t.Errorf("output claims a removal: %s", out)
	}
}

func TestPodmanDestroyOnlyOwnRunFilters(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	f := podmanFake(nil)
	openPodman(t, f, r)
	f.calls = nil
	if err := r.Destroy(); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range f.lines() {
		if i := strings.Index(l, "--filter "); i >= 0 {
			n++
			if !strings.Contains(l[i:], "--filter label=mash-at005="+testRunID+" ") {
				t.Errorf("filter without this run's id: %s", l)
			}
		}
	}
	if n != 6 {
		t.Errorf("%d filtered listings, want 6 (3 to remove, 3 to verify): %v", n, f.lines())
	}
}

// destroyFake answers like podman after a removal: the listings are empty once "rm" succeeded, unless rmFails or
// leftover says otherwise.
func destroyFake(rmFails bool, leftover string) *fakeRT {
	removed := false
	return podmanFake(func(c Cmd) (string, string, int, bool) {
		l := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(l, "podman rm -f"):
			if rmFails {
				return "", "cannot remove container", 125, true
			}
			removed = true
			return "", "", 0, true
		case strings.HasPrefix(l, "podman ps -a --filter label=mash-at005="+testRunID+" --format {{.ID}}"):
			if !removed {
				return "aaa111\n", "", 0, true
			}
			return leftover, "", 0, true
		case strings.HasPrefix(l, "podman network ls --filter"), strings.HasPrefix(l, "podman images"):
			return "", "", 0, true
		}
		return "", "", 0, false
	})
}

func TestAT5DestroyCheck(t *testing.T) {
	linuxOnly(t)
	for _, tc := range []struct {
		name     string
		rmFails  bool
		leftover string
		pass     bool
		want     string
	}{
		{"success", false, "", true, ""},
		{"rm fails", true, "", false, "cannot remove container"},
		{"leftover container", false, "zzz999\n", false, "1 container(s) labelled mash-at005=" + testRunID + " remain: zzz999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newPodRun(t)
			hs := openPodman(t, destroyFake(tc.rmFails, tc.leftover), r)
			s := &At5{R: r, Hosts: hs, C: &Checks{}, secrets: map[string][]byte{}}
			s.destroyCheck()
			var got *Assertion
			for _, a := range s.C.All() {
				if strings.Contains(a.Name, "nothing labelled mash-at005="+testRunID+" remains") {
					a := a
					got = &a
				}
			}
			if got == nil || got.Pass != tc.pass || !strings.Contains(got.Detail, tc.want) {
				t.Fatalf("check %+v", got)
			}
			rec := Record{RunID: "r", Revision: "v", AT: "at005", Scenario: "s", Observed: s.observed()}
			md := rec.Markdown()
			if !strings.Contains(md, "every container, network and image of this run is destroyed and nothing labelled mash-at005="+testRunID+" remains") {
				t.Errorf("record lacks the check line:\n%s", md)
			}
			if rec.Passed() != tc.pass {
				t.Errorf("record passed = %v", rec.Passed())
			}
			// a failed destruction is retried by Close and never panics
			r.Close()
		})
	}
}

func TestAT5DestroyLocalIsTrivial(t *testing.T) {
	linuxOnly(t)
	r := &Run{Dir: t.TempDir(), pids: map[int]string{}}
	if err := r.Destroy(); err != nil {
		t.Error(err)
	}
	r.OnClose(func() error { panic("boom") })
	if err := r.Destroy(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("panic not turned into an error: %v", err)
	}
}

func natsRepo(t *testing.T, ver string) *Run {
	t.Helper()
	d := t.TempDir()
	gm := "module x\n\ngo 1.27.4\n\nrequire (\n\tgithub.com/other/thing v1.0.0\n\tgithub.com/nats-io/nats-server/v2 " + ver + "\n)\n"
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte(gm), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Run{Dir: d, Repo: d, Runner: &Runner{Out: d, Cwd: d}, pids: map[int]string{}}
}

func TestBuildNatsFromPin(t *testing.T) {
	var calls [][]string
	var envs [][]string
	fake := func(reported string) buildFn {
		return func(dir string, env []string, argv ...string) (string, error) {
			calls = append(calls, argv)
			envs = append(envs, env)
			if len(argv) > 1 && argv[1] == "build" {
				return "", nil
			}
			return reported + "\n", nil
		}
	}
	r := natsRepo(t, "v2.15.1")
	bin := filepath.Join(r.Dir, "bin")
	if err := r.buildNats(bin, fake("nats-server: v2.15.1")); err != nil {
		t.Fatal(err)
	}
	if r.NatsBin != filepath.Join(bin, "nats-server") {
		t.Errorf("NatsBin %q", r.NatsBin)
	}
	if got := strings.Join(calls[0], " "); got != "go build -o "+r.NatsBin+" github.com/nats-io/nats-server/v2" {
		t.Errorf("build command %q", got)
	}
	if !slices.Contains(envs[0], "CGO_ENABLED=0") {
		t.Error("the build is not static")
	}
	if got := "nats-server: " + r.NatsVer; got != "nats-server: v2.15.1 (built from the go.mod pin v2.15.1)" {
		t.Errorf("record line %q", got)
	}
	// a mismatch is refused and records nothing
	r2 := natsRepo(t, "v2.15.1")
	err := r2.buildNats(filepath.Join(r2.Dir, "bin"), fake("nats-server: v2.14.0"))
	if err == nil || !strings.Contains(err.Error(), "refusing") || r2.NatsBin != "" || r2.NatsVer != "" {
		t.Errorf("mismatch not refused: %v (%q %q)", err, r2.NatsBin, r2.NatsVer)
	}
	// a failed build is an error
	r3 := natsRepo(t, "v2.15.1")
	if err := r3.buildNats("b", func(string, []string, ...string) (string, error) { return "", errors.New("boom") }); err == nil {
		t.Error("a failed build is not an error")
	}
	// no pin in go.mod
	r4 := natsRepo(t, "v2.15.1")
	_ = os.WriteFile(filepath.Join(r4.Dir, "go.mod"), []byte("module x\n"), 0o600)
	if err := r4.buildNats("b", fake("")); err == nil {
		t.Error("no pin is not an error")
	}
}

func TestNatsPinMatchesRepoGoMod(t *testing.T) {
	v, err := natsPin("../..")
	if err != nil || !strings.HasPrefix(v, "v2.") {
		t.Fatalf("%q %v", v, err)
	}
}

// linuxOnly skips a test of the podman host on a platform that is not Linux: AT-005's rootless podman nodes exist only there
// (the tests model its command lines and read ELF binaries), so they say so and skip instead of failing on a macOS runner.
func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the podman host of AT-005 is Linux-only (rootless podman, ELF binaries)")
	}
}

func TestBuildContextCopiesThePluginDirectory(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	openPodman(t, podmanFake(nil), r)
	ctx := filepath.Join(r.Dir, "podman-ctx")
	for f, perm := range map[string]os.FileMode{"plugin/.mcp.json": 0o644, "plugin/hooks/hooks.json": 0o644, "plugin/scripts/mash": 0o755} {
		st, err := os.Stat(filepath.Join(ctx, f))
		if err != nil || st.Mode().Perm() != perm {
			t.Errorf("%s in the build context: %v %v", f, st, err)
		}
	}
	for _, want := range []string{"COPY plugin /opt/mash-repo/plugin", "chmod -R a+rX /opt/mash-repo"} {
		if !strings.Contains(containerfile, want) {
			t.Errorf("the Containerfile lacks %q", want)
		}
	}
}

func TestCopyTreeRefusesALinkOutOfTheTree(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "outside"), []byte("o"), 0o644)
	os.WriteFile(filepath.Join(src, "in"), []byte("i"), 0o644)
	os.Symlink("in", filepath.Join(src, "okln"))
	if err := copyTree(src, filepath.Join(d, "dst1")); err != nil {
		t.Fatalf("a link inside the tree: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "dst1", "okln")); string(b) != "i" {
		t.Errorf("link copied as %q", b)
	}
	os.Symlink("../outside", filepath.Join(src, "bad"))
	if err := copyTree(src, filepath.Join(d, "dst2")); err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Errorf("a link out of the tree: %v", err)
	}
}
