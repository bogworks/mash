package main

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// connCounter is a TCP listener on a loopback port the kernel picks. It accepts and drops every connection and counts
// them: a plain URL pointed at it shows whether mash or mashctl connected at all. It is not a NATS server.
type connCounter struct {
	ln net.Listener
	n  atomic.Int64
}

func newConnCounter() (*connCounter, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c := &connCounter{ln: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			c.n.Add(1)
			conn.Close()
		}
	}()
	return c, nil
}

func (c *connCounter) Addr() string { return c.ln.Addr().String() }
func (c *connCounter) Count() int64 { return c.n.Load() }
func (c *connCounter) Close()       { _ = c.ln.Close() }

// plainRefusalOK judges one command run with a plain URL and no override (REQ-014): exit code want, the refusal names
// the override and the fix, and no connection reached the listener.
func plainRefusalOK(r Result, want int, connects int64) (bool, string) {
	text := r.Text()
	var miss []string
	for _, s := range []string{"MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", "tls://"} {
		if !strings.Contains(text, s) {
			miss = append(miss, s)
		}
	}
	ok := r.Code == want && len(miss) == 0 && connects == 0
	return ok, fmt.Sprintf("exit %d (want %d); message lacks: %s; %d connections reached the server; %s", r.Code, want, orNone(strings.Join(miss, ", ")), connects, exitDetail(r))
}

// skipWarning is the line every command prints on standard error while MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set.
const skipWarning = "warning: encryption to the server is OFF (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set)"

// plainWarnedOK judges one command run with the override: a connection was made (the listener counted one) and the
// plain-text warning is on standard error, never on standard output.
func plainWarnedOK(r Result, connects int64) (bool, string) {
	warned := strings.Contains(r.Stderr, skipWarning)
	onStdout := strings.Contains(r.Stdout, skipWarning)
	ok := connects > 0 && warned && !onStdout
	return ok, fmt.Sprintf("%d connections reached the server; warning on stderr %v, on stdout %v; %s", connects, warned, onStdout, exitDetail(r))
}

// doctorWarnedOnStderr is the check that `mash doctor` under the override says the plain-text warning on standard
// error. The report itself is on stdout; the standalone warning line must not be. The exit code is not part of it: the
// counting listener the harness stands in for the server does not answer, so the report finds a problem.
func doctorWarnedOnStderr(r Result) (bool, string) {
	warned := strings.Contains(r.Stderr, skipWarning)
	onStdout := strings.Contains(r.Stdout, skipWarning)
	return warned && !onStdout, fmt.Sprintf("warning on stderr %v, standalone on stdout %v; %s", warned, onStdout, exitDetail(r))
}

// plainText is AT-003's plain-URL case (REQ-014). A plain nats:// URL to a loopback address, with no override, is refused
// by mash (session start and a command) and by mashctl, exit 2, naming the override, and no connection reaches the
// address (a counting listener stands in for the server: the refusal comes before any connection). With the override
// the connection is made and the warning is printed on standard error. A failed assertion here is a failure, also when
// the mashctl side does not yet refuse: nothing is skipped.
func (a *AT) plainText(ha, he Home, design ep) {
	cc, err := newConnCounter()
	if !a.C.Check("a counting listener is made on a loopback port", err == nil, fmt.Sprint(err)) {
		return
	}
	defer cc.Close()
	url := "nats://" + cc.Addr()
	base := func(over string, more ...string) []string {
		return append(append(a.Conn(), "MASH_SERVER="+url, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION="+over, // Conn() carries no override: it is set here, per command, and only here
			"MASH_OPEN_TIMEOUT=2s"), more...)
	}
	session := []string{"MASH_ENDPOINT=alice.dev.design", "MASH_CREDS=" + design.creds, "MASH_STATE_DIR=" + filepath.Join(he.State, "plain")}
	type run struct {
		name string
		call func(extra []string) Result
	}
	runs := []run{
		{"mash serve (session start)", func(x []string) Result { return a.Mash("plain-serve", he, append(x, session...), "serve") }},
		{"mash inbox (a command)", func(x []string) Result { return a.Mash("plain-inbox", he, append(x, session...), "inbox") }},
		{"mashctl endpoint list", func(x []string) Result { return a.Ctl("plain-ctl", ha, x, "endpoint", "list") }},
	}
	for _, r := range runs {
		before := cc.Count()
		res := r.call(base("0"))
		ok, detail := plainRefusalOK(res, 2, cc.Count()-before)
		a.C.Check("REQ-014: "+r.name+" refuses a plain nats:// URL to loopback with no override, exit 2, naming the override, and no connection is made", ok, detail)
	}
	for _, r := range runs[1:] { // serve under the override waits on its stdin; a command ends by itself
		before := cc.Count()
		res := r.call(base("1"))
		ok, detail := plainWarnedOK(res, cc.Count()-before)
		a.C.Check("REQ-014: "+r.name+" with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 connects and prints the plain-text warning on standard error", ok, detail)
	}
	st := a.Mash("plain-doctor", he, append(base("1"), session...), "doctor")
	ok, detail := doctorWarnedOnStderr(st)
	a.C.Check("REQ-014: mash doctor with the override prints the plain-text warning on standard error, not standard output", ok, detail)
	a.C.Check("REQ-014: mash doctor with the override does not report the plain URL as refused", !strings.Contains(st.Stdout, "refusing a plain"), snip(grepLine(st.Text(), "server")))
	st = a.Mash("plain-doctor-refused", he, append(base("0"), session...), "doctor")
	a.C.Check("REQ-014: mash doctor without it says the server row FAILs and names MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION", strings.Contains(st.Stdout, "server    FAIL  ") && strings.Contains(st.Stdout, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"), snip(grepLine(st.Text(), "server")))
}
