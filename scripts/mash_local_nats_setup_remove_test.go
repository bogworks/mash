package scripts

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #995: `mash-local-nats-setup.sh --remove` keeps refusing while anything listens on the stopped folder's port,
// and the refusal names the pids that hold it (no folder or path), with the retry as the hint. Every case ends
// with exit 1, the folder unchanged and the exact two lines. The script cannot
// be sourced without running main, so these run the real script under a pseudo-terminal (util-linux `script`, for
// --remove's /dev/tty prompt) with ss and lsof stubs on PATH that report listeners with pids the test chooses. A
// listener is either a "nats" stand-in (a copy of this test binary named nats-server, which only sleeps when
// fakeServerEnv=1 (see TestMain in mash_local_nats_setup_start_test.go), started with the argv the case needs) or a "plain" sleep whose argv[0] is a chosen string.

// typedReader types ans after the prompt is up, then holds the terminal open while the script answers.
type typedReader struct {
	ans  string
	done bool
}

func (r *typedReader) Read(p []byte) (int, error) {
	if r.done {
		time.Sleep(3 * time.Second)
		return 0, io.EOF
	}
	time.Sleep(1 * time.Second)
	r.done = true
	return copy(p, r.ans), nil
}

// env is what a case's listener definitions can name: the folder being removed (dir), the other folder (other,
// with a mash-setup.json), a folder without one (bare), and the home the script runs in.
type env struct{ root, home, dir, other, bare string }

// listener is one process holding the port. nats: a nats-server stand-in run with args (after argv[0]); otherwise a
// plain sleep whose argv[0] is the args joined by spaces.
type listener struct {
	nats bool
	args func(e env) []string
	cwd  func(e env) string // default: /
}

type scenario struct {
	listeners []listener
	noPid     bool // ss and lsof omit the pid
	shared    bool // ss shows every pid on one line
	custom    bool // the folder is not the default $HOME/mash-nats, so the retry hint carries --dir
}

func plain(args func(e env) []string) listener    { return listener{args: args} }
func natsWith(args func(e env) []string) listener { return listener{nats: true, args: args} }

const testPort = 4291

// runRemove drives --remove and returns its exit code, its output, the folders, the listener pids in order and a
// snapshot of the folder taken just before the run.
func runRemove(t *testing.T, sc scenario) (rc int, out string, e env, pids []int, before string) {
	t.Helper()
	for _, tool := range []string{"bash", "script", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not found")
		}
	}
	if _, err := os.Stat("/proc/self/exe"); err != nil {
		t.Skip("needs /proc (Linux)")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// under umask 002 TempDir is 0775, and the script refuses a --dir under a group-writable folder
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	natsBin := filepath.Join(root, "natsbin")
	home := filepath.Join(root, "home")
	dir := filepath.Join(home, "mash-nats")
	if sc.custom {
		dir = filepath.Join(home, "custom-nats")
	}
	other := filepath.Join(root, "other")
	bare := filepath.Join(root, "bare")
	for _, d := range []string{bin, natsBin, home, dir, other, bare} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string, mode os.FileMode) {
		if err := os.WriteFile(p, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	setup := func(d string) {
		write(filepath.Join(d, "mash-setup.json"), fmt.Sprintf(
			"{\n  \"schema\": 1,\n  \"owner\": \"acme\",\n  \"port\": %d,\n  \"dir\": %q\n}\n", testPort, d), 0o600)
		write(filepath.Join(d, "server.conf"), fmt.Sprintf("listen: 127.0.0.1:%d\nserver_name: mash-local\n", testPort), 0o600)
	}
	setup(dir)
	setup(other)
	write(filepath.Join(bare, "server.conf"), fmt.Sprintf("listen: 127.0.0.1:%d\n", testPort), 0o600)
	// the stand-in nats-server: /proc/PID/exe ends in nats-server
	natsExe := filepath.Join(natsBin, "nats-server")
	if b, err := os.ReadFile(self); err != nil {
		t.Fatal(err)
	} else {
		write(natsExe, string(b), 0o755)
	}

	e = env{root: root, home: home, dir: dir, other: other, bare: bare}
	for _, l := range sc.listeners {
		var c *exec.Cmd
		if l.nats {
			c = exec.Command(natsExe, l.args(e)...)
			c.Args[0] = "nats-server"
			c.Env = []string{fakeServerEnv + "=1"} // TestMain (start test) sleeps in this mode
		} else {
			c = exec.Command("bash", "-c", `exec -a "$1" sleep 300`, "bash", strings.Join(l.args(e), " "))
		}
		c.Dir = "/"
		if l.cwd != nil {
			c.Dir = l.cwd(e)
		}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		pid := c.Process.Pid
		t.Cleanup(func() { // only the process this test started, by its recorded pid
			_ = c.Process.Kill()
			_ = c.Wait()
		})
		for i := 0; i < 100; i++ { // wait for exec -a to replace bash
			b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			if strings.HasPrefix(string(b), "nats-server") {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		pids = append(pids, pid)
	}

	var ssLines, lsofOut string
	switch {
	case sc.noPid:
		ssLines = fmt.Sprintf("echo \"LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:*\"\n", testPort)
	case sc.shared:
		var users []string
		for _, pid := range pids {
			users = append(users, fmt.Sprintf(`(\"nats-server\",pid=%d,fd=7)`, pid))
		}
		ssLines = fmt.Sprintf("echo \"LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:* users:(%s)\"\n", testPort, strings.Join(users, ","))
	default:
		for _, pid := range pids {
			ssLines += fmt.Sprintf("echo \"LISTEN 0 4096 127.0.0.1:%d 0.0.0.0:* users:((\\\"nats-server\\\",pid=%d,fd=7))\"\n", testPort, pid)
		}
	}
	if !sc.noPid {
		for _, pid := range pids {
			lsofOut += fmt.Sprintf("p%d\\nn127.0.0.1:%d\\n", pid, testPort) // lsof -Fn form; unused on Linux
		}
	}
	write(filepath.Join(bin, "ss"), "#!/bin/sh\necho 'State Recv-Q Send-Q Local-Address:Port Peer-Address:Port Process'\n"+ssLines, 0o755)
	write(filepath.Join(bin, "lsof"), fmt.Sprintf("#!/bin/sh\nprintf '%s'\n", lsofOut), 0o755)

	before = snapshot(t, dir)
	script, err := filepath.Abs("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("script", "-qec", "bash "+script+" --remove --dir "+dir, "/dev/null")
	cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + home, "TERM=dumb"}
	cmd.Stdin = &typedReader{ans: dir + "\n"}
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err != nil {
			t.Fatalf("a listener process was killed: %v", err)
		}
	}
	return rc, string(b), e, pids, before
}

// snapshot lists every path under d with its content, to prove the folder is unchanged.
func snapshot(t *testing.T, d string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(d, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		sb.WriteString(p + "\n")
		if !de.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sb.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// wantRefusal: exit 1, the folder unchanged, and the exact stderr lines: the error line then the hint line.
func wantRefusal(t *testing.T, rc int, out string, e env, before, wantErr, wantHint string) {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "error:") || strings.HasPrefix(l, "hint:") {
			lines = append(lines, l)
		}
	}
	if rc != 1 || len(lines) != 2 || lines[0] != wantErr || lines[1] != wantHint || strings.Contains(out, "removed "+e.dir) {
		t.Fatalf("rc=%d out=%q; want rc 1 and the lines %q, %q", rc, out, wantErr, wantHint)
	}
	if strings.Contains(out, e.other) || strings.Contains(out, "local server") {
		t.Fatalf("the refusal names a folder: %q", out)
	}
	if after := snapshot(t, e.dir); after != before {
		t.Fatalf("the folder was changed:\nbefore=%q\nafter=%q", before, after)
	}
	t.Logf("refusal: %s | %s", wantErr, wantHint)
}

const inUseHint = "hint: mash-local-nats-setup.sh --remove"

func inUse(who string) string {
	return fmt.Sprintf("error: port %d is in use (%s); nothing was removed", testPort, who)
}

// one pid: the pid alone, whatever the holder's argv (no folder is named)
func TestRemoveNamesTheOnePid(t *testing.T) {
	rc, out, e, p, b := runRemove(t, scenario{listeners: []listener{
		natsWith(func(e env) []string { return []string{"-c", e.other + "/server.conf"} })}})
	wantRefusal(t, rc, out, e, b, inUse(fmt.Sprintf("pid %d", p[0])), inUseHint)
}

// not a nats-server: the pid alone as well
func TestRemoveNamesTheOnePidWhenNotANatsServer(t *testing.T) {
	rc, out, e, p, b := runRemove(t, scenario{listeners: []listener{
		plain(func(e env) []string { return []string{"nats-server", "-c", e.other + "/server.conf"} })}})
	wantRefusal(t, rc, out, e, b, inUse(fmt.Sprintf("pid %d", p[0])), inUseHint)
}

// a shared socket: ss shows two pids on one line, both named, in order
func TestRemoveNamesEverySharedSocketPid(t *testing.T) {
	rc, out, e, p, b := runRemove(t, scenario{shared: true, listeners: []listener{
		natsWith(func(e env) []string { return []string{"-c", e.other + "/server.conf"} }),
		natsWith(func(e env) []string { return []string{"--store_dir", e.dir} })}})
	wantRefusal(t, rc, out, e, b, inUse(fmt.Sprintf("pids %d, %d", p[0], p[1])), inUseHint)
}

// a custom folder: the retry hint keeps --dir, the default folder's bare hint does not
func TestRemoveHintKeepsACustomDir(t *testing.T) {
	rc, out, e, p, b := runRemove(t, scenario{custom: true, listeners: []listener{
		natsWith(func(e env) []string { return []string{"-c", e.other + "/server.conf"} })}})
	wantRefusal(t, rc, out, e, b, inUse(fmt.Sprintf("pid %d", p[0])), inUseHint+" --dir "+e.dir)
}

// two listener lines, two pids, in tool order
func TestRemoveNamesSeveralPidsFromSeveralLines(t *testing.T) {
	rc, out, e, p, b := runRemove(t, scenario{listeners: []listener{
		natsWith(func(e env) []string { return []string{"--store_dir", e.dir} }),
		natsWith(func(e env) []string { return []string{"--store_dir", e.dir} })}})
	wantRefusal(t, rc, out, e, b, inUse(fmt.Sprintf("pids %d, %d", p[0], p[1])), inUseHint)
}

// no pid visible (another user's process): main's two lines exactly
func TestRemoveKeepsTheOldLinesWhenNoPidIsShown(t *testing.T) {
	rc, out, e, _, b := runRemove(t, scenario{noPid: true, listeners: []listener{
		natsWith(func(e env) []string { return []string{"-c", e.other + "/server.conf"} })}})
	wantRefusal(t, rc, out, e, b,
		fmt.Sprintf("error: something still listens on 127.0.0.1:%d; nothing was removed", testPort),
		"hint: stop the program that holds the port (a wrapper script that runs nats-server?), then run mash-local-nats-setup.sh --remove again")
}
