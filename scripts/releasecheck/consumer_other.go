//go:build !unix

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// consumerKillGrace has no effect here: without process groups only the shell is killed, at the deadline.
var consumerKillGrace = 10 * time.Minute

func runConsumer(ctx context.Context, dir, script string, args, env []string, out io.Writer) (int, error) {
	c := exec.CommandContext(ctx, "sh", append([]string{script}, args...)...)
	c.Dir, c.Env = dir, env
	c.Stdout, c.Stderr = out, out
	c.WaitDelay = 5 * time.Second
	err := c.Run()
	if err != nil && ctx.Err() != nil {
		return 0, fmt.Errorf("the consumer run did not finish: %w", ctx.Err())
	}
	return exitCode0(err)
}
