//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// consumerKillGrace is how long the script has to clean up (remove its containers) after the shell is told to stop,
// before the shell's whole process group is killed.
var consumerKillGrace = 10 * time.Minute

// runConsumer starts the shell in its own process group. On cancellation TERM goes to the shell alone (the script
// forwards it to the script, which cleans up its containers; a second TERM straight to the script could cut that
// short); once the grace has passed, whatever is left in the group is killed, including children of a shell that
// already exited.
func runConsumer(ctx context.Context, dir, script string, args, env []string, out io.Writer) (int, error) {
	c := exec.Command("sh", append([]string{script}, args...)...)
	c.Dir, c.Env = dir, env
	c.Stdout, c.Stderr = out, out
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = 5 * time.Second
	if err := c.Start(); err != nil {
		return 0, fmt.Errorf("starting the consumer runner: %w", err)
	}
	pgid := c.Process.Pid // Setpgid: the shell leads a group of its own
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()

	select {
	case err := <-done:
		if ctx.Err() == nil {
			return exitCode0(err)
		}
	case <-ctx.Done():
	}
	// Cancelled (or finished just as the deadline passed).
	grace := time.NewTimer(consumerKillGrace)
	defer grace.Stop()
	c.Process.Signal(syscall.SIGTERM)
	exited := false
	for tick := time.NewTicker(50 * time.Millisecond); ; <-tick.C {
		if !exited {
			select {
			case <-done:
				exited = true
			default:
			}
		}
		if exited && syscall.Kill(-pgid, 0) != nil {
			tick.Stop()
			break // the shell and everything it started are gone
		}
		select {
		case <-grace.C:
			syscall.Kill(-pgid, syscall.SIGKILL)
			tick.Stop()
			if !exited {
				<-done
			}
			return 0, fmt.Errorf("the consumer run did not finish: %w", ctx.Err())
		default:
		}
	}
	return 0, fmt.Errorf("the consumer run did not finish: %w", ctx.Err())
}
