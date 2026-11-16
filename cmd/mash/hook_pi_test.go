package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// piOffer is the JSON a Pi hook prints for a batch.
type piOffer struct {
	Text string `json:"text"`
	Ack  string `json:"ack"`
}

func piHook(t *testing.T, env map[string]string, in string, args ...string) (piOffer, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(append([]string{"hook"}, args...), envFrom(env), strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var o piOffer
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &o); err != nil {
			t.Fatalf("not JSON: %q: %v", out.String(), err)
		}
	}
	return o, errb.String()
}

func piSpool(t *testing.T, env map[string]string, seqs ...uint64) spool.Spool {
	t.Helper()
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	for _, q := range seqs {
		if err := sp.Put(spool.Entry{Seq: q, From: "b", Trust: "claimed", At: time.Now(), Body: []byte(fmt.Sprintf("hello pi %d", q))}); err != nil {
			t.Fatal(err)
		}
	}
	return sp
}

const piIn = `{"event":"tool_result","session":"s1"}`

// Under MASH_HOST=pi the hook offers the batch as JSON {text, ack} and does not drain it; nothing waiting prints nothing.
func TestPiHookOffersWithoutDraining(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env)
	if o, _ := piHook(t, env, piIn); o.Text != "" || o.Ack != "" {
		t.Fatalf("empty spool printed %+v", o)
	}
	for _, q := range []uint64{1, 2} {
		if err := sp.Put(spool.Entry{Seq: q, From: "b", Trust: "claimed", At: time.Now(), Body: []byte(fmt.Sprintf("hello pi %d", q))}); err != nil {
			t.Fatal(err)
		}
	}
	o, _ := piHook(t, env, piIn)
	if o.Ack == "" || !strings.Contains(o.Text, "> hello pi 1") || !strings.Contains(o.Text, "2 messages from b") {
		t.Fatalf("offer %+v", o)
	}
	if w, _ := sp.Waiting(); len(w) != 2 {
		t.Fatalf("the hook drained: %+v", w)
	}
	// the same session is not offered the same batch again at once
	if o2, _ := piHook(t, env, piIn); o2.Text != "" {
		t.Fatalf("re-offered inside the window: %+v", o2)
	}
	// a new session is offered it again, flagged as a repeat
	o3, _ := piHook(t, env, `{"event":"tool_result","session":"s2"}`)
	if o3.Ack == "" || !strings.Contains(o3.Text, "hello pi 1") {
		t.Fatalf("not re-offered to a new session: %+v", o3)
	}
}

// The ack drains exactly the entries the token names; a second ack is a no-op; an entry that arrived after the offer stays.
func TestPiAckDrainsExactlyTheOfferedEntries(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env, 1, 2)
	o, _ := piHook(t, env, piIn)
	if err := sp.Put(spool.Entry{Seq: 3, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("late")}); err != nil {
		t.Fatal(err)
	}
	if out, _ := piHook(t, env, piIn, "--ack", o.Ack); out.Text != "" {
		t.Fatalf("an ack printed %+v", out)
	}
	w, _ := sp.Waiting()
	if len(w) != 1 || w[0].Seq != 3 || !sp.IsDrained(spool.Ref{Seq: 1}) || !sp.IsDrained(spool.Ref{Seq: 2}) || sp.IsDrained(spool.Ref{Seq: 3}) {
		t.Fatalf("after the ack: waiting %+v", w)
	}
	// double ack: nothing changes, no error
	if _, e := piHook(t, env, piIn, "--ack="+o.Ack); e != "" {
		t.Fatalf("second ack said %q", e)
	}
	if w, _ := sp.Waiting(); len(w) != 1 || w[0].Seq != 3 {
		t.Fatalf("after the second ack: %+v", w)
	}
	// an ack of an entry serve already acked (tombstone and file gone) does not write a tombstone
	if err := sp.Acked(spool.Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	piHook(t, env, piIn, "--ack", o.Ack)
	if sp.IsDrained(spool.Ref{Seq: 1}) {
		t.Fatal("an ack of an unknown entry wrote a tombstone")
	}
	// a junk token is a no-op
	if _, e := piHook(t, env, piIn, "--ack", "!!"); !strings.Contains(e, "unreadable token") {
		t.Fatalf("stderr %q", e)
	}
}

// An ack from a session that does not own the spool is refused: nothing is drained.
func TestPiAckAfterOwnerChangeIsRefused(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env, 1)
	o, _ := piHook(t, env, piIn)
	if o.Ack == "" {
		t.Fatal("no offer")
	}
	// the spool now belongs to another launch
	if err := sp.OwnRecord("pid:1", `{"pid":1,"launch":"someone-else"}`); err != nil {
		t.Fatal(err)
	}
	env2 := map[string]string{}
	for k, v := range env {
		env2[k] = v
	}
	env2["MASH_LAUNCH_TOKEN"] = "mine"
	if out, e := piHook(t, env2, piIn, "--ack", o.Ack); out.Text != "" || !strings.Contains(e, "not this session's spool") {
		t.Fatalf("out %+v stderr %q", out, e)
	}
	if w, _ := sp.Waiting(); len(w) != 1 || sp.IsDrained(spool.Ref{Seq: 1}) {
		t.Fatalf("a refused ack drained: %+v", w)
	}
}

// A Claude hook has no ack phase: `--ack` is a no-op there (nothing printed, nothing drained), and without it the hook
// drains in one step as before.
func TestClaudeHookHasNoAckPhase(t *testing.T) {
	env := hookEnv(t, nil)
	sp := piSpool(t, env, 1)
	var out, errb bytes.Buffer
	run([]string{"hook", "--ack", "x"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
	if w, _ := sp.Waiting(); out.Len() != 0 || len(w) != 1 {
		t.Fatalf("out %q waiting %+v", out.String(), w)
	}
	run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
	if w, _ := sp.Waiting(); !strings.Contains(out.String(), "additionalContext") || len(w) != 0 {
		t.Fatalf("out %q waiting %+v", out.String(), w)
	}
}

// Claude's input is not Pi's: with MASH_HOST=pi nothing is printed and nothing is drained or offered.
func TestPiInputOfAnotherHostIsStillIgnored(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env, 1)
	var out, errb bytes.Buffer
	run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
	if w, _ := sp.Waiting(); out.Len() != 0 || len(w) != 1 || w[0].Redelivered {
		t.Fatalf("out %q waiting %+v", out.String(), w)
	}
}

func TestSpoolDirPrintsTheSpoolDirectory(t *testing.T) {
	state := t.TempDir()
	env := hookEnv(t, map[string]string{"MASH_STATE_DIR": state})
	var out, errb bytes.Buffer
	if code := run([]string{"spool-dir"}, envFrom(env), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if want := filepath.Join(state, "spool", "o", "a") + "\n"; out.String() != want || errb.Len() != 0 {
		t.Fatalf("out %q (want %q), stderr %q", out.String(), want, errb.String())
	}
	if got := spoolOf(map[string]string{"HOME": env["HOME"], "MASH_OWNER": "o", "MASH_NAME": "a"}).Dir; got != filepath.Join(env["HOME"], ".local", "state", "mash", "spool", "o", "a") {
		t.Fatalf("spoolOf %q", got)
	}
}

func TestSpoolDirWithoutAName(t *testing.T) {
	env := hookEnv(t, nil)
	delete(env, "MASH_NAME")
	var out, errb bytes.Buffer
	if code := run([]string{"spool-dir"}, envFrom(env), strings.NewReader(""), &out, &errb); code == 0 || out.Len() != 0 || !strings.Contains(errb.String(), "MASH_NAME") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

func TestSpoolDirIsInTheUsage(t *testing.T) {
	var out bytes.Buffer
	run([]string{"help"}, envFrom(nil), strings.NewReader(""), &out, &out)
	if !strings.Contains(out.String(), "mash spool-dir") {
		t.Fatal("usage does not list spool-dir")
	}
	out.Reset()
	if code := run([]string{"spool-dir", "--help"}, envFrom(nil), strings.NewReader(""), &out, &out); code != 0 || !strings.Contains(out.String(), "usage: mash spool-dir") {
		t.Fatalf("exit %d: %q", code, out.String())
	}
}

// A token is only base64 JSON: an ack drains only entries offered to the acking session. A forged token naming a
// spooled entry that was never offered drains nothing; neither does the ack of another session's offer.
func TestPiAckOnlyDrainsWhatWasOfferedToThisSession(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env, 1, 2)
	forged := ackToken([]spool.Entry{{Seq: 1}, {Seq: 2}})
	if _, e := piHook(t, env, piIn, "--ack", forged); !strings.Contains(e, "not offered to this session") {
		t.Fatalf("stderr %q", e)
	}
	if w, _ := sp.Waiting(); len(w) != 2 || sp.IsDrained(spool.Ref{Seq: 1}) {
		t.Fatalf("a forged token drained: %+v", w)
	}
	o, _ := piHook(t, env, piIn)
	if o.Ack == "" {
		t.Fatal("no offer")
	}
	if _, e := piHook(t, env, `{"event":"tool_result","session":"s2"}`, "--ack", o.Ack); !strings.Contains(e, "not offered to this session") {
		t.Fatalf("stderr %q", e)
	}
	if w, _ := sp.Waiting(); len(w) != 2 {
		t.Fatalf("another session's ack drained: %+v", w)
	}
	piHook(t, env, piIn, "--ack", o.Ack)
	if w, _ := sp.Waiting(); len(w) != 0 {
		t.Fatalf("the offered session's ack did not drain: %+v", w)
	}
}

// An ack with no session id is refused, even for an entry offered with an empty session.
func TestPiAckWithoutASessionIsRefused(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := piSpool(t, env, 1)
	if err := sp.MarkOffered("", spool.Entry{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	_, e := piHook(t, env, `{"event":"tool_result","session":""}`, "--ack", ackToken([]spool.Entry{{Seq: 1}}))
	if sp.IsDrained(spool.Ref{Seq: 1}) {
		t.Fatalf("drained with no session (stderr %q)", e)
	}
}

// A digest lists, it does not ack (ADR 0046): the ack reads whether an entry was offered in full or as a digest line from
// the spool, so a token edited to say a digest entry was shown in full (the old "l" flag false) does not drain it.
func TestPiAckUsesTheStoredListedStateNotTheToken(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi", "MASH_BATCH_BYTES": "100"})
	sp := piSpool(t, env)
	for q := uint64(1); q <= 4; q++ {
		if err := sp.Put(spool.Entry{Seq: q, From: "b", Trust: "claimed", At: time.Now(), Body: []byte(strings.Repeat("x", 300))}); err != nil {
			t.Fatal(err)
		}
	}
	o, _ := piHook(t, env, piIn)
	if o.Ack == "" {
		t.Fatal("nothing offered")
	}
	var listed, full []spool.Ref
	for _, q := range []uint64{1, 2, 3, 4} {
		r := spool.Ref{Seq: q}
		if _, l, _, ok := sp.Offered(r); ok && l {
			listed = append(listed, r)
		} else if ok {
			full = append(full, r)
		}
	}
	if len(listed) == 0 {
		t.Fatalf("no entry was offered as a digest line: %q", o.Text)
	}
	// A forged token claims every entry was shown in full (l false), as an edited token would.
	items := make([]map[string]any, 0, 4)
	for _, r := range append(slicesClone(listed), full...) {
		items = append(items, map[string]any{"q": r.Seq, "l": false})
	}
	b, _ := json.Marshal(items)
	piHook(t, env, piIn, "--ack", base64.RawURLEncoding.EncodeToString(b))
	for _, r := range listed {
		if sp.IsDrained(r) || !sp.IsListed(r) {
			t.Fatalf("digest-only #%d: drained=%v listed=%v after a token claiming it was shown in full", r.Seq, sp.IsDrained(r), sp.IsListed(r))
		}
	}
	// the full case (a batch that fits) drains: TestPiAckDrainsExactlyTheOfferedEntries
}

func slicesClone(r []spool.Ref) []spool.Ref { return append([]spool.Ref(nil), r...) }
