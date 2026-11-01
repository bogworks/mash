package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/spool"
)

// hook is `mash hook`, the plugin's hook (hook delivery, mash v0.2.0), run for any host adapter. It shows the session
// the mail serve has spooled, as the adapter presents it, and marks what was shown drained, which serve then acks. It
// makes no NATS connection: an empty spool costs a directory read, on every tool call. It always exits 0: a hook that
// fails must not block the session, and the mail stays spooled for the next run.
//
// Delivery is at-least-once, by choice: the batch is printed, then each entry marked drained, so a hook killed
// between the two (Claude Code's timeout, a crash) shows the batch again on its next run. Mail is never lost, and may
// be shown twice. To make the repeat visible the hook first marks the seqs it is about to show (spool.MarkShown); an
// entry still marked on a later run is presented as re-delivered, so the model does not act on it twice. A mark that
// cannot be written does not stop the print: at-least-once wins, and stderr says so.
func hook(cfg config.Config, cfgErr error, adapter host.Adapter, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if cfgErr != nil || cfg.Inert() {
		return 0
	}
	// The whole input is read, uncapped: a PostToolUse input carries the tool's full response and may pass 1 MiB.
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return 0
	}
	trig, ok := adapter.Trigger(raw, env)
	if !ok {
		return 0
	}
	sp := spool.Spool{Dir: spool.Dir(cfg.StateDir, cfg.Owner, cfg.Name)}
	// The lock is held from the owner check to the drained marks, so a new lease holder cannot rebind and re-own the
	// spool in between and have its new mail drained into this old session (codex H1 on #21, round 7).
	unlock, err := sp.Lock()
	if err != nil {
		return 0 // no spool: serve is not spooling for this endpoint
	}
	defer unlock()
	id := host.HookIdentity{SessionID: trig.SessionID, LaunchToken: getenv(env, "MASH_LAUNCH_TOKEN")}
	if id.SessionID == "" { // the stdin session_id wins; the environment only fills in when stdin has none
		id.SessionID = getenv(env, "CLAUDE_CODE_SESSION_ID")
	}
	for _, w := range sp.Waiters() {
		id.Waiters = append(id.Waiters, w.PID)
	}
	ok, why := adapter.Match(sp.Owner(), id)
	if getenv(env, "MASH_HOOK_TRACE") == "1" {
		fmt.Fprintf(stderr, "mash hook: trace pid=%d ancestors=%v hook-session=%q match=%v (%s)\n",
			os.Getpid(), ancestorPIDs(), id.SessionID, ok, why)
	}
	if !ok {
		// Refused, and recorded: a hook that runs but is not recognised must leave a trace, or the mail stays spooled and
		// serve keeps waking the session with nothing said (ADR 0040). Nothing goes to stdout.
		if err := sp.NoteRefused(spool.Refusal{Time: time.Now().UTC(), Why: why, Session: id.SessionID, Ancestors: ancestorPIDs()}); err != nil {
			fmt.Fprintf(stderr, "mash hook: recording the refusal: %v\n", err)
		}
		fmt.Fprintf(stderr, "mash hook: not this session's spool (%s); mail left for its owner\n", why)
		if host.IsOtherLaunch(why) {
			// Once per (session id, launch token): tell the model in-session, since status is pull-only (ADR 0043).
			who := id.LaunchToken
			if who == "" { // no token: the waiting serve's pid tells the copies apart
				who = strconv.Itoa(host.WaiterAncestor(host.OwnerPID(sp.Owner()), id.Waiters))
			}
			sum := sha256.Sum256([]byte(id.SessionID + "\x00" + who))
			key := hex.EncodeToString(sum[:])[:16]
			if !sp.Told(key) {
				text := fmt.Sprintf("mash: this copy of the session is not receiving mail. Another Claude process of this same session (pid %d) holds the endpoint %s; mail goes to that process. End one of the two, or run this one under a different MASH_NAME.",
					host.OwnerPID(sp.Owner()), cfg.Name)
				if _, err := stdout.Write(host.Notice(trig.Event, text)); err == nil {
					if err := sp.NoteTold(key); err != nil {
						fmt.Fprintf(stderr, "mash hook: recording the notice: %v\n", err)
					}
				}
			}
		}
		return 0
	}
	if trig.Subagent {
		// A subagent's hook of this session: hooks work here, so serve must not call the session hookless, but the
		// main session's mail is not this subagent's to read. Never .hooked, never a drain.
		if err := sp.Alive(); err != nil {
			fmt.Fprintf(stderr, "mash hook: recording a subagent's hook run: %v\n", err)
		}
		return 0
	}
	// Every run marks the session hooked, so serve spools from now on; before the first run it delivered by channel.
	if err := sp.Touch(); err != nil {
		return 0 // no spool: serve is not spooling for this endpoint
	}
	waiting, err := sp.Waiting()
	if err != nil || len(waiting) == 0 {
		return 0
	}
	out, shown := adapter.Present(host.Presentation{Name: cfg.Name, Owner: cfg.Owner, Event: trig.Event,
		BatchMsgs: cfg.BatchMsgs, BatchBytes: cfg.BatchBytes}, waiting)
	seqs := make([]uint64, len(shown))
	for i, e := range shown {
		seqs[i] = e.Seq
	}
	if err := sp.MarkShown(seqs...); err != nil {
		fmt.Fprintf(stderr, "mash hook: recording what is being shown: %v; showing it anyway (a repeat after an interruption would not be flagged)\n", err)
	}
	failpoint(env, stderr, "before-print")
	if _, err := stdout.Write(out); err != nil {
		return 0 // not shown: left spooled
	}
	failpoint(env, stderr, "after-print")
	for i, e := range shown {
		if err := sp.Drained(e.Seq); err != nil {
			fmt.Fprintf(stderr, "mash hook: marking #%d drained: %v\n", e.Seq, err)
		}
		if i == 0 {
			failpoint(env, stderr, "after-first-drained")
		}
	}
	return 0
}

func getenv(env func(string) string, k string) string {
	if env == nil {
		return ""
	}
	return env(k)
}

// ancestorPIDs is the first 10 process ids above the hook.
func ancestorPIDs() []int {
	var out []int
	for pid := range host.Ancestors {
		if out = append(out, pid); len(out) == 10 {
			break
		}
	}
	return out
}

// failpoint is a test-only hook: when MASH_HOOK_FAILPOINT names this point, the hook says so on stderr and blocks, so
// a test can SIGKILL it there. Never set outside the crash tests.
func failpoint(env func(string) string, stderr io.Writer, name string) {
	if env != nil && env("MASH_HOOK_FAILPOINT") == name {
		fmt.Fprintf(stderr, "mash hook: failpoint %s\n", name)
		time.Sleep(time.Hour)
	}
}
