// Command nodeinit is the only helper in an AT-005 node image. The image is FROM scratch: no shell, no sleep, no curl.
// `serve` keeps the container up until SIGTERM; the others are the file and network operations the harness needs
// inside a node (each acts on exactly what it is given, nothing is matched by pattern).
//
//	nodeinit serve
//	nodeinit mkdir DIR           make DIR (and parents), mode 0700
//	nodeinit chmod OCTAL PATH
//	nodeinit rm PATH             remove one file or one empty directory
//	nodeinit stat PATH           exit 0 if it exists
//	nodeinit dial HOST:PORT      exit 0 if a TCP connection is accepted
//	nodeinit get URL             print the body of an HTTP GET
//	nodeinit run --pidfile PATH -- CMD ARGS...
//	                             run CMD as a child (same env and stdio), write the child's pid to PATH (0600),
//	                             forward SIGTERM and SIGINT to it, exit with its status, remove PATH
//	nodeinit forge --url tls://ADDR:PORT --ca FILE --creds FILE --seconds N
//	                             as a second owner (the given creds): answer each discovery request of owner alice
//	                             with a forged and a replay-shaped payload, count the servers' refusals, print
//	                             {"seen_requests":N,"violations":M} (counts only)
//	nodeinit stop --pidfile PATH [--grace 5s]
//	                             SIGTERM the pid in PATH, wait up to the grace, then SIGKILL that pid only; exit 0
//	                             once it is gone (also when PATH is absent)
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(a []string) int {
	need := func(n int) bool {
		if len(a) != n+1 {
			fmt.Fprintln(os.Stderr, "usage: nodeinit serve|mkdir DIR|chmod MODE PATH|rm PATH|stat PATH|dial HOST:PORT|get URL")
			return false
		}
		return true
	}
	var err error
	switch {
	case len(a) > 0 && a[0] == "run":
		return cmdRun(a[1:])
	case len(a) > 0 && a[0] == "stop":
		return cmdStop(a[1:])
	case len(a) > 0 && a[0] == "forge":
		return cmdForge(a[1:], os.Stdout, os.Stderr)
	case len(a) == 0:
		need(1)
		return 2
	case a[0] == "serve":
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
		<-c
	case a[0] == "mkdir" && need(1):
		err = os.MkdirAll(a[1], 0o700)
	case a[0] == "chmod" && need(2):
		var m uint64
		if m, err = strconv.ParseUint(a[1], 8, 32); err == nil {
			err = os.Chmod(a[2], os.FileMode(m))
		}
	case a[0] == "rm" && need(1):
		err = os.Remove(a[1])
	case a[0] == "stat" && need(1):
		_, err = os.Stat(a[1])
	case a[0] == "dial" && need(1):
		var c net.Conn
		if c, err = net.DialTimeout("tcp", a[1], 2*time.Second); err == nil {
			c.Close()
		}
	case a[0] == "get" && need(1):
		var resp *http.Response
		if resp, err = (&http.Client{Timeout: 3 * time.Second}).Get(a[1]); err == nil {
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
		}
	default:
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nodeinit:", err)
		return 1
	}
	return 0
}

func cmdRun(a []string) int {
	if len(a) < 4 || a[0] != "--pidfile" || a[2] != "--" || a[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: nodeinit run --pidfile PATH -- CMD ARGS...")
		return 2
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	return superviseCmd(a[1], a[3:], sigs, os.Stdin, os.Stdout, os.Stderr)
}

// superviseCmd runs argv as a child, records the child's pid in pidfile, forwards the signals it receives to the child
// and returns the child's exit status. The pidfile is removed when the child is gone.
func superviseCmd(pidfile string, argv []string, sigs <-chan os.Signal, in io.Reader, out, errw io.Writer) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	if in != nil {
		cmd.Stdin = in
	}
	if out != nil {
		cmd.Stdout = out
	}
	if errw != nil {
		cmd.Stderr = errw
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "nodeinit:", err)
		return 127
	}
	defer os.Remove(pidfile)
	if err := writePidfile(pidfile, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		fmt.Fprintln(os.Stderr, "nodeinit:", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
		case err := <-done:
			if err == nil {
				return 0
			}
			if ee, ok := err.(*exec.ExitError); ok {
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					return 128 + int(ws.Signal())
				}
				return ee.ExitCode()
			}
			fmt.Fprintln(os.Stderr, "nodeinit:", err)
			return 1
		}
	}
}

// writePidfile writes the pid atomically: a private temporary file in the same directory, then a rename.
func writePidfile(path string, pid int) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pid-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.WriteString(strconv.Itoa(pid) + "\n")
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

func cmdStop(a []string) int {
	grace := 5 * time.Second
	switch {
	case len(a) == 2 && a[0] == "--pidfile":
	case len(a) == 4 && a[0] == "--pidfile" && a[2] == "--grace":
		d, err := time.ParseDuration(a[3])
		if err != nil || d < 0 {
			fmt.Fprintln(os.Stderr, "nodeinit: bad --grace")
			return 2
		}
		grace = d
	default:
		fmt.Fprintln(os.Stderr, "usage: nodeinit stop --pidfile PATH [--grace 5s]")
		return 2
	}
	if err := stopPidfile(a[1], grace); err != nil {
		fmt.Fprintln(os.Stderr, "nodeinit:", err)
		return 1
	}
	return 0
}

// stopPidfile sends SIGTERM to the pid in the file and, if it is still there after grace, SIGKILL to that pid only.
// An absent file means it is already stopped.
func stopPidfile(path string, grace time.Duration) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return fmt.Errorf("%s does not hold a pid", path)
	}
	gone := func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }
	if err := syscall.Kill(pid, syscall.SIGTERM); err == syscall.ESRCH {
		return nil
	}
	waitGone := func(d time.Duration) bool {
		end := time.Now().Add(d)
		for !gone() {
			if time.Now().After(end) {
				return false
			}
			time.Sleep(20 * time.Millisecond)
		}
		return true
	}
	if !waitGone(grace) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		if !waitGone(5 * time.Second) {
			return fmt.Errorf("pid %d did not exit after SIGKILL", pid)
		}
	}
	_ = os.Remove(path)
	return nil
}
