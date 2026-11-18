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
	for _, s := range []string{"MASH_INSECURE_PLAINTEXT", "tls://"} {
		if !strings.Contains(text, s) {
			miss = append(miss, s)
		}
	}
	if !strings.Contains(text, "insecure_plaintext") && !strings.Contains(text, "MASH_INSECURE_PLAINTEXT") {
		miss = append(miss, "insecure_plaintext")
	}
	ok := r.Code == want && len(miss) == 0 && connects == 0
	return ok, fmt.Sprintf("exit %d (want %d); message lacks: %s; %d connections reached the server; %s", r.Code, want, orNone(strings.Join(miss, ", ")), connects, exitDetail(r))
}

// plainWarnedOK judges one command run with the override: a connection was made (the listener counted one) and the
// plain-text warning is on standard error, never on standard output.
func plainWarnedOK(r Result, connects int64) (bool, string) {
	warned := strings.Contains(strings.ToLower(r.Stderr), "plain-text") || strings.Contains(strings.ToLower(r.Stderr), "plain text")
	onStdout := strings.Contains(strings.ToLower(r.Stdout), "warning: plain")
	ok := connects > 0 && warned && !onStdout
	return ok, fmt.Sprintf("%d connections reached the server; warning on stderr %v, on stdout %v; %s", connects, warned, onStdout, exitDetail(r))
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
		return append(append(a.Conn(), "MASH_URL="+url, "MASH_INSECURE_PLAINTEXT="+over, "MASH_OPEN_TIMEOUT=2s"), more...)
	}
	session := []string{"MASH_GROUP=dev", "MASH_NAME=design", "MASH_CREDS=" + design.creds, "MASH_STATE_DIR=" + filepath.Join(he.State, "plain")}
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
		a.C.Check("REQ-014: "+r.name+" with insecure_plaintext set connects and prints the plain-text warning on standard error", ok, detail)
	}
	st := a.Mash("plain-status", he, append(base("1"), session...), "status")
	a.C.Check("REQ-014: mash status with the override says tls: WARN", strings.Contains(st.Text(), "tls: WARN"), snip(grepLine(st.Text(), "tls:")))
	st = a.Mash("plain-status-refused", he, append(base("0"), session...), "status")
	a.C.Check("REQ-014: mash status without it says tls: FAILED", strings.Contains(st.Text(), "tls: FAILED"), snip(grepLine(st.Text(), "tls:")))
}
