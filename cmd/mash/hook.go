package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
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
	// The hook connects to nothing, but a configuration serve and the CLI refuse must not look healthy here: the refusal
	// is said on stderr (hook.log) and the hook still exits 0 (the spool has no mail from a serve that never started).
	if _, err := endpoint.EndpointTLS(cfg); err != nil {
		fmt.Fprintf(stderr, "mash hook: %v\n", err)
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
	dir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		return 0
	}
	sp := spool.Spool{Dir: dir}
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
	// Told at most once per waitNoticeEvery while this session's serve waits for the lease, in a run that would print
	// nothing else: two documents on stdout are not one hook output.
	noteWait := func() { waitNotice(sp, cfg, trig.Event, id, stdout, stderr) }
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
		told := false
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
					host.OwnerPID(sp.Owner()), cfg.DisplayName())
				if _, err := stdout.Write(host.Notice(trig.Event, text)); err == nil {
					told = true
					if err := sp.NoteTold(key); err != nil {
						fmt.Fprintf(stderr, "mash hook: recording the notice: %v\n", err)
					}
				}
			}
		}
		if !told {
			noteWait()
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
		noteWait()
		return 0
	}
	// A message an earlier digest listed is not shown again in a digest (the model was told to read it, and a repeat on
	// every tool call would be noise); it is shown only in full, in a run whose whole batch fits that way, which then
	// drains it (ADR 0046). A run with nothing new shows nothing.
	var fresh, listed []spool.Entry
	for _, e := range waiting {
		if e.Listed {
			listed = append(listed, e)
		} else {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		noteWait()
		return 0
	}
	pres := host.Presentation{Name: cfg.DisplayName(), Owner: cfg.Owner, Event: trig.Event, BatchMsgs: cfg.BatchMsgs, BatchBytes: cfg.BatchBytes}
	out, shown := adapter.Present(pres, slices.Clone(fresh))
	if len(listed) > 0 && !anyListed(shown) {
		// New mail fits in full: see whether the listed mail fits with it. Only a batch with no digest line is taken.
		all := append(slices.Clone(fresh), listed...)
		sort.Slice(all, func(i, j int) bool { return all[i].Ref().Less(all[j].Ref()) })
		if out2, shown2 := adapter.Present(pres, all); !anyListed(shown2) {
			out, shown = out2, shown2
		}
	}
	seqs := make([]spool.Ref, len(shown))
	for i, e := range shown {
		seqs[i] = e.Ref()
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
		if e.Listed {
			// Listed in a digest, not read: waiting and unacked until the model reads it (ADR 0046).
			if err := sp.Listed(e.Ref()); err != nil {
				fmt.Fprintf(stderr, "mash hook: marking #%s listed: %v\n", endpoint.FormatMsgRef(e.Ref()), err)
			}
		} else if err := sp.Drained(e.Ref()); err != nil {
			fmt.Fprintf(stderr, "mash hook: marking #%s drained: %v\n", endpoint.FormatMsgRef(e.Ref()), err)
		}
		if i == 0 {
			failpoint(env, stderr, "after-first-drained")
		}
	}
	return 0
}

// waitNoticeEvery is how often the hook repeats the lease-wait notice while the wait persists.
const waitNoticeEvery = 10 * time.Minute

// waitNotice tells the session its mail is off while its own serve waits for the endpoint's lease (the spool's
// lease-wait state, ADR 0092): once on the first run after the state appears, then at most every waitNoticeEvery. It is
// for the session of the waiting serve only: the hook's session id is the serve's, or the serve's parent is among the
// hook's ancestors; the hook of the session that holds the lease is told nothing. A missing state, or one whose serve is
// gone, prints nothing (and a stale one is removed). Call it under the spool lock.
func waitNotice(sp spool.Spool, cfg config.Config, event string, id host.HookIdentity, stdout, stderr io.Writer) {
	ws, ok := sp.LiveWaitState()
	if !ok {
		if err := sp.ClearStaleWaitState(); err != nil {
			fmt.Fprintf(stderr, "mash hook: clearing a stale lease-wait state: %v\n", err)
		}
		return
	}
	mine := ws.Session != "" && ws.Session == id.SessionID
	if !mine && ws.ParentPID > 1 {
		for pid := range host.Ancestors {
			if pid == ws.ParentPID {
				mine = true
				break
			}
		}
	}
	now := time.Now()
	if !mine || !sp.WaitNoticeDue(now, waitNoticeEvery) {
		return
	}
	text := fmt.Sprintf("mash: this session does not hold endpoint %s (held by pid %s on %s); mail is off until it recovers",
		cfg.Name, ws.HolderPID, ws.HolderHost)
	if _, err := stdout.Write(host.Notice(event, text)); err != nil {
		return
	}
	if err := sp.NoteWaitNotice(now); err != nil {
		fmt.Fprintf(stderr, "mash hook: recording the notice: %v\n", err)
	}
}

// anyListed reports whether any entry was rendered as a digest line.
func anyListed(es []spool.Entry) bool {
	return slices.ContainsFunc(es, func(e spool.Entry) bool { return e.Listed })
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
