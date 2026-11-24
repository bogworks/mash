package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
)

var statusSpec = epSpec{name: "status", verb: "status", min: 0, max: 0}

// statusRow is the state of one server: the line of `mashctl status` and one object of its JSON.
type statusRow struct {
	Server    string  `json:"server"`
	OK        bool    `json:"ready"`
	Endpoints *int    `json:"endpoint_count"`
	Online    *int    `json:"online_count"`
	Reason    *string `json:"reason"`

	unset bool // the reason is a missing setting: nothing was tried, so doctor cannot help
}

// statusDeadline bounds the connection and the questions to one server.
const statusDeadline = 3 * time.Second

const serverSilent = "the server did not answer"

// serverStatus asks one server: the directory records it holds and how many of their endpoints hold a live lease. A
// server it cannot ask is a row that is not ok, with the reason; it is never an error of the command.
func serverStatus(cfg config.Config, server string) statusRow {
	row := statusRow{Server: maskURL(server)}
	notOK := func(why string) statusRow {
		why = config.Clean(strings.TrimPrefix(strings.TrimPrefix(strings.SplitN(why, "\n", 2)[0], "mashctl: "), "mash: "))
		row.Reason = &why
		return row
	}
	cfg.URL = server
	cfg.AdminTimeout = statusDeadline // one silent server must not hold the others' rows
	if cfg.AdminCreds == "" { // OpenAdminWith checks the owner first; a missing credential is reported either way
		row.unset = true
		return notOK("admin credentials are not set: set MASH_ADMIN_CREDS, or \"admin_creds\" in config.json")
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdminWith("mashctl", cfg, nats.NoReconnect(), nats.IgnoreDiscoveredServers(), nats.Timeout(statusDeadline))
	if err != nil {
		var noCreds *ctl.NoAdminCredsError
		if errors.As(err, &noCreds) {
			row.unset = true
			return notOK("admin credentials are not set: set MASH_ADMIN_CREDS, or \"admin_creds\" in config.json")
		}
		switch msg := err.Error(); {
		case isUnreachable(err), strings.Contains(msg, "no servers available"):
			return notOK(serverSilent)
		case strings.Contains(msg, "Authorization Violation"), strings.Contains(msg, "authorization violation"):
			return notOK("the server refused the admin credentials")
		default:
			return notOK(msg)
		}
	}
	defer closeAdmin()
	recs, _, err := adm.List(ctx)
	if err != nil {
		return notOK(admin.RewordUnavailable(err.Error()))
	}
	online := 0
	for _, r := range recs {
		st, err := adm.EndpointState(ctx, recordAddress(r), false)
		if err != nil {
			return notOK(admin.RewordUnavailable(err.Error()))
		}
		if st.Online {
			online++
		}
	}
	n := len(recs)
	row.OK, row.Endpoints, row.Online = true, &n, &online
	return row
}

// count is "1 endpoint" or "3 endpoints".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "x") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// statusRows asks every server at the same time and returns the rows in the order of servers.
func statusRows(cfg config.Config, servers []string, ask func(config.Config, string) statusRow) []statusRow {
	rows := make([]statusRow, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = ask(cfg, s)
		}()
	}
	wg.Wait()
	return rows
}

// runStatus is `mashctl status`: one line per configured server. It reports and does not fail: the lines go to
// stdout whatever it finds, and it exits 1 when a server is not ok.
func runStatus(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	in, err := parseEpInput(statusSpec, args)
	jsonOut := in.json || g.JSON
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args))
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return cliframe.Report(stdout, stderr, failErr(err), jsonOut)
	}
	var servers []string
	for _, s := range strings.Split(cfg.URL, ",") {
		if s = strings.TrimSpace(s); s != "" {
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		return cliframe.Report(stdout, stderr, cliframe.Failf("no server is configured").WithHint(initFix), jsonOut)
	}
	for _, s := range servers { // a plain URL without the override is a refusal, not a state of the server
		if _, _, err := natsauth.RequireTLS(s, cfg.SkipEncryption); err != nil {
			return cliframe.Report(stdout, stderr, withPlainCode(cliframe.Failf("%s", strings.TrimPrefix(err.Error(), "mash: ")), err), jsonOut)
		}
	}
	rows := statusRows(cfg, servers, serverStatus)
	bad, hint := false, false
	for _, r := range rows {
		bad = bad || !r.OK
		hint = hint || (!r.OK && !r.unset)
	}
	switch {
	case jsonOut && len(rows) == 1:
		writeJSON(stdout, rows[0])
	case jsonOut:
		writeJSON(stdout, struct {
			Servers []statusRow `json:"servers"`
		}{rows})
	default:
		for _, r := range rows {
			if r.OK {
				fmt.Fprintf(stdout, "%s: ok, %s, %d online\n", r.Server, count(*r.Endpoints, "endpoint"), *r.Online)
			} else if r.unset {
				fmt.Fprintf(stdout, "%s: %s\n", r.Server, *r.Reason)
			} else {
				fmt.Fprintf(stdout, "%s: not ok: %s\n", r.Server, *r.Reason)
			}
		}
	}
	if bad {
		if !jsonOut && hint {
			fmt.Fprintln(stderr, "hint: mashctl doctor")
		}
		return 1
	}
	return 0
}
