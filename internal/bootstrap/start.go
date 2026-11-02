package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
)

// startWait is how long Start waits for the server it launched to accept a connection.
const startWait = 15 * time.Second

// ErrNoServerBinary is wrapped by Start when nats-server is not on PATH; nothing was started.
var ErrNoServerBinary = errors.New("mashctl: nats-server is not on PATH: nothing was started (the files remain). Install it from nats.io or your package manager, then run: nats-server -c <dir>/server.conf")

// checkPortFree fails if anything accepts a TCP connection on 127.0.0.1:port.
func checkPortFree(port int) error {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return nil
	}
	c.Close()
	return fmt.Errorf("mashctl: port %d is already in use: something already listens on 127.0.0.1:%d, so nothing was started (pick another with --port, or stop that server)", port, port)
}

// checkServerName fails unless the server that answered is the one our server.conf names.
func checkServerName(got string) error {
	if got != ServerName {
		return fmt.Errorf("mashctl: the server answering is %q, not the %q this run configured: another server holds the port", got, ServerName)
	}
	return nil
}

// logTail is the last lines of the server log, for an error message.
func logTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	return strings.Join(lines, "\n")
}

// Start launches nats-server -c <Dir>/server.conf detached (its own process group; output to server.log, pid in
// server.pid), and waits until it accepts a connection with the mashadmin credentials. It returns the pid. On a
// failure after the launch it stops only the process it started.
func Start(p Params) (int, error) {
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		return 0, ErrNoServerBinary
	}
	if err := checkPortFree(p.Port); err != nil {
		return 0, err
	}
	log, err := os.OpenFile(p.LogPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("mashctl: create %s: %w", p.LogPath(), err)
	}
	defer log.Close()
	cmd := exec.Command(bin, "-c", p.ConfPath())
	cmd.Dir = p.Dir
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("mashctl: start %s: %w", bin, err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	fail := func(err error) (int, error) {
		_ = cmd.Process.Kill() // only the process this run started
		<-exited
		_ = os.Remove(p.PIDPath())
		return 0, err
	}
	if err := os.WriteFile(p.PIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fail(fmt.Errorf("mashctl: write %s: %w", p.PIDPath(), err))
	}
	url := "nats://127.0.0.1:" + strconv.Itoa(p.Port)
	deadline := time.Now().Add(startWait)
	for time.Now().Before(deadline) {
		select {
		case werr := <-exited:
			_ = os.Remove(p.PIDPath())
			return 0, fmt.Errorf("mashctl: nats-server exited at once (%v): see %s\n%s", werr, p.LogPath(), logTail(p.LogPath()))
		default:
		}
		nc, cerr := nats.Connect(url, natsauth.UserCredentials(p.AdminCredsPath()), nats.Timeout(time.Second), nats.MaxReconnects(0))
		if cerr == nil {
			nc.Close()
			return pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fail(fmt.Errorf("mashctl: nats-server did not accept a connection within %s: stopped it; see %s", startWait, p.LogPath()))
}
