package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// cli runs the one-shot commands that are not on the command table (prompt) as the endpoint (MASH_ENDPOINT, owner.group.name), without taking its lease.
func cli(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if args[0] == "prompt" && len(args) >= 3 { // an address is owner.group.name; the flat form is refused, nothing is attempted
		if _, err := cmdcore.ParseAddress(args[1], "mash prompt --help"); err != nil {
			return cliframe.Report(stdout, stderr, err, false)
		}
	}
	if cfg.Inert() {
		return cliframe.Report(stdout, stderr, cmdcore.NoEndpoint(cmdcore.NoneInstalled, args[0], nil), false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliDeadline(cfg))
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	ep, err := endpoint.OpenClientWith(openCtx, cfg, endpoint.ClientOptions{})
	openCancel()
	if err != nil {
		return fail(err)
	}
	defer ep.Close()
	short := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, cfg.CallTimeout) }

	switch args[0] {
	case "prompt":
		if len(args) < 3 {
			return fail(fmt.Errorf("usage: mash prompt <to> <text...>"))
		}
		c, cc := short()
		subj, err := ep.DiscoverRecipient(c, args[1])
		cc()
		if err != nil {
			return fail(err)
		}
		if err := endpoint.Prompt(ctx, ep.Conn(), ep.Identity(), ep.Key(), subj, strings.Join(args[2:], " "), stdout); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

// cliDeadline is the total deadline of one command: cli_timeout, else 15 minutes.
func cliDeadline(cfg config.Config) time.Duration {
	if cfg.CLITimeout > 0 {
		return cfg.CLITimeout
	}
	return config.DefaultCLITimeout
}
