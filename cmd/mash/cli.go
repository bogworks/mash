package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
)

// cli runs the one-shot commands as MASH_NAME, without taking its lease.
func cli(cfg config.Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if cfg.Inert() {
		return fail(fmt.Errorf("mash: set MASH_NAME to the endpoint you are acting as"))
	}
	var whoF endpoint.WhoFilter
	var whoO whoOutput
	if args[0] == "who" {
		var err error
		if whoF, whoO, err = ParseWhoFlags(args[1:]); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliDeadline(cfg))
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	ep, err := endpoint.OpenClientWith(openCtx, cfg, clientNeeds(cfg, args))
	openCancel()
	if err != nil {
		return fail(err)
	}
	defer ep.Close()
	short := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, cfg.CallTimeout) }

	switch args[0] {
	case "send":
		urgent, k, sendID := false, kind.Task, ""
		for len(args) > 2 {
			if args[2] == "--urgent" {
				urgent = true
				args = append(args[:2:2], args[3:]...)
				continue
			}
			if v, isID := strings.CutPrefix(args[2], "--id="); isID || args[2] == "--id" {
				n := 3
				if !isID {
					if len(args) < 4 {
						fmt.Fprintln(stderr, sendUsage)
						return 2
					}
					v, n = args[3], 4
				}
				if err := endpoint.ValidateSendID(v); err != nil {
					fmt.Fprintf(stderr, "mash send: %v\n", err)
					return 2
				}
				sendID = v
				args = append(args[:2:2], args[n:]...)
				continue
			}
			v, isKind := strings.CutPrefix(args[2], "--kind=")
			n := 3
			if args[2] == "--kind" {
				isKind = true
				if len(args) < 4 {
					fmt.Fprintln(stderr, sendUsage)
					return 2
				}
				v, n = args[3], 4
			}
			if !isKind {
				break
			}
			var err error
			if k, err = kind.Parse(v); err != nil {
				fmt.Fprintf(stderr, "mash send: %v\n", err)
				return 2
			}
			args = append(args[:2:2], args[n:]...)
		}
		if len(args) < 3 {
			return fail(fmt.Errorf(sendUsage))
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
		ac, acc := context.WithTimeout(ctx, cfg.CallTimeout)
		// A session's shell outlives its serve's lease in two windows: right after a (re)start, before the new serve
		// takes the lease, and after a crash, while the dead serve's lease (another launch's token) has not yet
		// expired. Refusing there lost a resumed session's replies. So the mail goes out
		// under the client's own instance, and the warning says it cannot be bound to the session; a reader that
		// binds acks to the session's instance then rejects it, exactly as before this adoption existed.
		if err := ep.AdoptLaunchInstance(ac); err != nil {
			fmt.Fprintf(stderr, "%v; sending under this command's own instance\n", err)
		}
		acc()
		c, cc := short()
		defer cc()
		res, err := ep.SendDetailed(c, args[1], body, endpoint.SendOptions{Urgent: urgent, Kind: k, ID: sendID})
		if err != nil {
			return fail(err)
		}
		if res.Duplicate {
			fmt.Fprintf(stdout, "sent #%d from %s to %s (duplicate of --id %s: already delivered, nothing new stored)\n", res.Seq, ep.Key(), args[1], sendID)
			return 0
		}
		fmt.Fprintf(stdout, "sent #%d from %s to %s\n", res.Seq, ep.Key(), args[1])
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
		limit, after, err := inboxArgs(args[1:])
		if err != nil {
			return fail(err)
		}
		c, cc := short()
		defer cc()
		var ms []render.Mail
		if after != nil {
			ms, err = ep.InboxAfter(c, *after, limit)
		} else {
			ms, err = ep.Inbox(c, limit)
		}
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
		var live map[string]endpoint.LeaseInfo
		if whoO.Long {
			live = liveLeases(c, ep)
		}
		if out := whoRender(whoF.Apply(ep.Who()), whoO, !whoF.IsZero(), live); out != "" {
			fmt.Fprintln(stdout, out)
		}
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

// clientNeeds is what a command reads besides the endpoint's own record, so the open can ask for it all at once: over a
// slow link every dependent request is a round trip, and reading the whole directory one key at a time cost a session 148 ms
// from NATS ten seconds per command. who shows the directory, so it reads it; the rest read the records they use: the
// recipient of a send, the senders of the mail shown.
func clientNeeds(cfg config.Config, args []string) endpoint.ClientOptions {
	switch args[0] {
	case "send":
		o := endpoint.ClientOptions{Lease: cfg.LaunchToken != ""}
		if len(args) > 1 {
			o.Records = []string{args[1]}
		}
		return o
	case "read", "inbox":
		return endpoint.ClientOptions{Inbox: true}
	case "who":
		_, o, _ := ParseWhoFlags(args[1:])
		return endpoint.ClientOptions{FullDirectory: true, Presence: true, Lease: o.Long}
	}
	return endpoint.ClientOptions{} // prompt finds its peer by service discovery
}

// cliMaxLimit bounds `mash inbox --limit` from a shell. It is above the MCP tool's 100 because a poller that fell
// behind widens its look (a launcher asks ten times its window); a page past this takes --after.
const cliMaxLimit = 1000

func cliLimit(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > cliMaxLimit:
		return cliMaxLimit
	}
	return n
}

// inboxArgs reads `mash inbox [--limit N] [--after SEQ]`, in either order. after is nil without --after.
func inboxArgs(args []string) (limit int, after *uint64, err error) {
	const use = "usage: mash inbox [--limit N] [--after SEQ]"
	limit = 20
	for len(args) > 0 {
		if len(args) < 2 {
			return 0, nil, fmt.Errorf("%s", use)
		}
		switch args[0] {
		case "--limit":
			n, perr := strconv.Atoi(args[1])
			if perr != nil {
				return 0, nil, fmt.Errorf("%s: --limit %q is not a number", use, args[1])
			}
			limit = cliLimit(n)
		case "--after":
			n, perr := strconv.ParseUint(strings.TrimPrefix(args[1], "#"), 10, 64)
			if perr != nil {
				return 0, nil, fmt.Errorf("%s: --after %q is not a #seq", use, args[1])
			}
			after = &n
		default:
			return 0, nil, fmt.Errorf("%s: unknown argument %q", use, args[0])
		}
		args = args[2:]
	}
	return limit, after, nil
}

// cliDeadline is the total deadline of one command: cli_timeout, else 15 minutes.
func cliDeadline(cfg config.Config) time.Duration {
	if cfg.CLITimeout > 0 {
		return cfg.CLITimeout
	}
	return config.DefaultCLITimeout
}
