package main

import (
	"context"
	"errors"
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
	if args[0] == "prompt" && len(args) < 3 { // before anything is opened, as send's usage errors are
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash prompt needs <to> and the message text").WithHint("mash prompt --help"), false)
	}
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
	fail := func(err error) int {
		if f := framedIn(cfg, cfg.DisplayName(), err); f != err {
			return cliframe.Report(stdout, stderr, f, false)
		}
		return cliframe.Report(stdout, stderr, promptFailure(err), false)
	}
	if err != nil {
		return fail(err)
	}
	defer ep.Close()
	short := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, cfg.CallTimeout) }

	switch args[0] {
	case "prompt":
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

// promptFailure is err in style A (CHG-17): the "mash: " prefix of the endpoint's text is dropped and the words after it
// stay. A hint is added only where it names a command that shows more: mash who for an endpoint that is not online,
// mash doctor for a failed connect or credentials. None follows a refusal by the peer or a signing refusal.
func promptFailure(err error) *cliframe.Error {
	msg := strings.TrimPrefix(err.Error(), "mash: ")
	e := cliframe.Failf("%s", msg)
	var xc interface{ ExitCode() int } // an error that carries its own code (a refused plain NATS URL is 2)
	if errors.As(err, &xc) && xc.ExitCode() == 2 {
		e.Code = 2
	}
	switch {
	case strings.HasSuffix(msg, " is not online (no SAP registration answered)"):
		return e.WithHint("mash who")
	case strings.HasPrefix(msg, "connect "), strings.HasPrefix(msg, "credentials: "):
		return e.WithHint("mash doctor")
	}
	return e
}

// cliDeadline is the total deadline of one command: cli_timeout, else 15 minutes.
func cliDeadline(cfg config.Config) time.Duration {
	if cfg.CLITimeout > 0 {
		return cfg.CLITimeout
	}
	return config.DefaultCLITimeout
}
