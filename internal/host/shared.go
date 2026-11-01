package host

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// ownerRecord is the owner serve writes into the spool's .owner (ADR 0040; .session keeps the legacy "pid:N" text,
// see spool.LegacyToken): the host process (serve's parent), the session id and the launch token serve was started
// with. Empty fields are omitted.
type ownerRecord struct {
	PID     int    `json:"pid,omitempty"`
	Session string `json:"session,omitempty"`
	Launch  string `json:"launch,omitempty"`
}

// ownerToken is the JSON owner record of the serve that holds the lease: its parent pid (the host process), the
// session id read from sessionEnv and its MASH_LAUNCH_TOKEN.
func ownerToken(env func(string) string, sessionEnv string) string {
	r := ownerRecord{PID: os.Getppid()}
	if env != nil {
		r.Session, r.Launch = env(sessionEnv), env("MASH_LAUNCH_TOKEN")
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// matchOwner decides whether the hook belongs to the session whose serve holds the lease. Any one of three rules, cheapest
// first, is enough; a hook that matches none is refused, and no rule adopts an unknown session:
//  1. session: the hook's session id equals serve's (both non-empty);
//  2. launch: both launch tokens are non-empty and equal (stable across /clear);
//  3. process: serve's parent pid is among the hook's ancestors (covers /clear on the plain CLI).
//
// A legacy owner ("pid:N" text) is checked by rule 3 alone; an empty or other older owner is accepted.
func matchOwner(owner string, h HookIdentity) (bool, string) {
	var r ownerRecord
	switch {
	case strings.HasPrefix(owner, "{"):
		if json.Unmarshal([]byte(owner), &r) != nil {
			return false, "owner record unreadable (is serve a different mash version than the hook?)"
		}
	case strings.HasPrefix(owner, "pid:"):
		n, err := strconv.Atoi(strings.TrimPrefix(owner, "pid:"))
		if err != nil {
			return false, "owner record unreadable"
		}
		r.PID = n
	default:
		return true, "no owner recorded"
	}
	if h.SessionID != "" && r.Session != "" && h.SessionID == r.Session {
		return true, "session id"
	}
	if h.LaunchToken != "" && r.Launch != "" && h.LaunchToken == r.Launch {
		return true, "launch token"
	}
	if r.PID > 0 {
		for pid := range Ancestors { // lazy: stops at the owner
			if pid == r.PID {
				return true, "process ancestry"
			}
		}
	}
	return false, "session id " + differs(h.SessionID, r.Session) + ", launch token " + differs(h.LaunchToken, r.Launch) +
		", process " + strconv.Itoa(r.PID) + " is not an ancestor of the hook"
}

// differs words why two ids did not match, without printing a launch token.
func differs(hook, owner string) string {
	switch {
	case owner == "":
		return "not recorded by serve"
	case hook == "":
		return "not seen by the hook"
	}
	return "differs"
}

// batchEntries picks what one drain may show: urgent entries first, each group in seq order, at most BatchMsgs of
// them. The rest wait for the next run: never dropped.
func batchEntries(p Presentation, waiting []spool.Entry) []spool.Entry {
	// BatchMsgs 0 is allowed and means "digest" to render, so it must not also empty the drain (codex M2 on #21).
	n := p.BatchMsgs
	if n < 1 {
		n = 10
	}
	// Urgent first, so a drain never shows only normal mail while an urgent message waits behind it, and fitting
	// drops non-urgent entries first (codex H1 on #23).
	slices.SortStableFunc(waiting, func(a, b spool.Entry) int {
		switch {
		case a.Urgent == b.Urgent:
			return 0
		case a.Urgent:
			return -1
		}
		return 1
	})
	if len(waiting) > n {
		waiting = waiting[:n]
	}
	return waiting
}

// frameBatch is the framed text a host shows: the hook frame, then the batch in full or, past maxBytes, a digest.
func frameBatch(p Presentation, w []spool.Entry, maxBytes int) string {
	ms := make([]render.Mail, len(w))
	ts := make([]sap.Trust, len(w))
	for i, e := range w {
		ms[i] = endpoint.MailOf(e)
		ts[i] = ms[i].Trust
	}
	return endpoint.HookFrame(p.Name, ts) + "\n" + render.Batch(p.Name, p.Owner, ms, p.BatchMsgs, maxBytes)
}
