package endpoint

import (
	"fmt"
	"github.com/bogworks/mash/internal/sap"
	"os"
	"strconv"
	"sync"
)

// SessionIdent is who this serve process is, as far as a later serve of the same Claude Code session can tell: its
// parent pid, that parent's start time (so a reused pid is told from the parent) and CLAUDE_CODE_SESSION_ID. Each part
// is "" when unknown. It goes into the lease (leaseValue.PPID, PStart, Session) so a new serve of the same session can
// take the lease over from the earlier one (ADR 0082).
type SessionIdent struct {
	PPID, PStart, Session string
}

// OurSession is this process's SessionIdent; a variable so tests can stand in for another session or none.
var OurSession = func() SessionIdent { return ourSessionOnce() }

var ourSessionOnce = sync.OnceValue(func() SessionIdent {
	id := SessionIdent{Session: os.Getenv("CLAUDE_CODE_SESSION_ID")}
	if pp := os.Getppid(); pp > 1 { // a parent of 1 or less is no parent: never an identity
		id.PPID = strconv.Itoa(pp)
		id.PStart, _ = procStart(pp)
	}
	return id
})

// sameSession reports whether the holder recorded in lv is an earlier serve of this process's own session: same machine
// (boot and pid namespace) and the same non-empty session id (two different non-empty ids always refuse), or, when a side has no session id, the
// same parent pid with the same parent start time. Anything absent on either side, a parent pid <= 1, another machine, or two different launch tokens (another launch
// of a running session, ADR 0043) is not the same session.
func (e *Endpoint) sameSession(lv leaseValue) bool {
	machine := ourMachine()
	if machine == "" || lv.Machine != machine {
		return false
	}
	me := OurSession()
	if pp, err := strconv.Atoi(me.PPID); err != nil || pp <= 1 {
		return false
	}
	if e.cfg.LaunchToken != "" && lv.LaunchToken != "" && e.cfg.LaunchToken != lv.LaunchToken {
		return false
	}
	// Two different non-empty session ids are two sessions, whatever parent they share: never the same. The parent
	// identity decides only when a side has no session id.
	if lv.Session != "" && me.Session != "" {
		return lv.Session == me.Session
	}
	return lv.PPID != "" && lv.PPID == me.PPID && lv.PStart != "" && lv.PStart == me.PStart
}

// takeoverReason says why this process may take the lease from the holder lv, or "" when it may not: the holder is on
// this machine and its process is gone (G270), or it is an earlier serve of this same session.
func (e *Endpoint) takeoverReason(lv leaseValue) string {
	me := ourMachine()
	if me == "" || lv.Machine != me {
		return ""
	}
	if lv.PID != fmt.Sprint(os.Getpid()) && holderGone(lv.PID, lv.Start) {
		return "dead"
	}
	if e.sameSession(lv) {
		return "same"
	}
	return ""
}

// tookOverLine is the log line of a takeover; key is the lease's key, shown only for the legacy identity's.
func (e *Endpoint) tookOverLine(key, reason string, lv leaseValue) string {
	what := "lease: took over"
	if key != e.key {
		what += " " + key
	}
	if reason == "same" {
		return fmt.Sprintf("%s from the same session's earlier serve %s (pid %s)", what, lv.Instance, lv.PID)
	}
	return fmt.Sprintf("%s from dead holder %s (pid %s)", what, lv.Instance, lv.PID)
}

// LeaseLostError is a lease refresh that found another session holding the name: this serve no longer holds it.
// SameSession says the new holder is a later serve of this process's own session (it superseded this one).
type LeaseLostError struct {
	Key              string
	Host, PID, Since string // the new holder, "" when it could not be read
	SameSession      bool
}

func (e *LeaseLostError) Error() string {
	s := fmt.Sprintf("mash: lost the lease on %s to another session", sap.Display(e.Key))
	if e.PID != "" {
		s += fmt.Sprintf(" (host %s, pid %s)", e.Host, e.PID)
	}
	return s
}
