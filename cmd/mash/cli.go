package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/endpoint"
	"github.com/randybias/mash/internal/render"
)

// cli runs the one-shot commands as MASH_NAME, without taking its lease.
func cli(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if cfg.Inert() {
		return fail(fmt.Errorf("mash: set MASH_NAME to the endpoint you are acting as"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, 20*time.Second)
	ep, err := endpoint.OpenClient(openCtx, cfg)
	openCancel()
	if err != nil {
		return fail(err)
	}
	defer ep.Close()
	short := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 15*time.Second) }

	switch args[0] {
	case "send":
		if len(args) < 3 {
			return fail(fmt.Errorf("usage: mash send <to> [text... | --file F | -]"))
		}
		var body string
		switch {
		case args[2] == "--file" && len(args) == 4:
			b, err := os.ReadFile(args[3])
			if err != nil {
				return fail(err)
			}
			body = string(b)
		case args[2] == "-" && len(args) == 3:
			b, err := io.ReadAll(stdin)
			if err != nil {
				return fail(err)
			}
			body = string(b)
		default:
			body = strings.Join(args[2:], " ")
		}
		// A send from a launched session's shell names the session's instance; adopted after the body is read, so
		// a slow stdin can't leave it signing for a serve that has since gone.
		ac, acc := context.WithTimeout(ctx, 5*time.Second)
		err := ep.AdoptLaunchInstance(ac)
		acc()
		if err != nil {
			return fail(err)
		}
		c, cc := short()
		defer cc()
		seq, err := ep.Send(c, args[1], body, 0)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "sent #%d from %s to %s\n", seq, cfg.Name, args[1])
	case "read":
		if len(args) != 2 {
			return fail(fmt.Errorf("usage: mash read <seq>"))
		}
		seq, err := strconv.ParseUint(strings.TrimPrefix(args[1], "#"), 10, 64)
		if err != nil {
			return fail(fmt.Errorf("mash: %q is not a #seq", args[1]))
		}
		c, cc := short()
		defer cc()
		m, err := ep.Read(c, seq)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, render.One(m))
	case "inbox":
		limit := 20
		if len(args) == 3 && args[1] == "--limit" {
			if n, err := strconv.Atoi(args[2]); err == nil {
				limit = clampLimit(n)
			}
		}
		c, cc := short()
		defer cc()
		ms, err := ep.Inbox(c, limit)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, inboxText(ms))
	case "who":
		c, cc := short()
		defer cc()
		if err := ep.Refresh(c); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, whoText(ep.Who()))
	case "prompt":
		if len(args) < 3 {
			return fail(fmt.Errorf("usage: mash prompt <to> <text...>"))
		}
		c, cc := short()
		subj, err := endpoint.Discover(c, ep.Conn(), cfg.Owner, args[1])
		cc()
		if err != nil {
			return fail(err)
		}
		if err := endpoint.Prompt(ctx, ep.Conn(), ep.Identity(), cfg.Name, subj, strings.Join(args[2:], " "), stdout); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}
