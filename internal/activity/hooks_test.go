package activity

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// clock is an injected time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var envLaunch = map[string]string{"MASH_LAUNCH_TOKEN": "tok-1", "CLAUDE_CODE_SESSION_ID": "sess-1"}

// hooksTracker is a tracker with a hooks source in a temp dir and no goroutines: tests call pollHooks.
type hooksTracker struct {
	*Tracker
	dir string
	clk *clock
	seq int
}

func newHooksTracker(t *testing.T, env map[string]string) *hooksTracker {
	t.Helper()
	clk := newClock()
	dir := t.TempDir()
	return &hooksTracker{Tracker: New(Options{Env: mapEnv(env), Dir: dir, Now: clk.Now}), dir: dir, clk: clk}
}

// write stores r and polls. The file's mod time is set to a new value each time so a coarse file system clock
// cannot hide a rewrite.
func (h *hooksTracker) write(t *testing.T, r Record) {
	t.Helper()
	if err := WriteRecord(h.dir, r); err != nil {
		t.Fatal(err)
	}
	h.seq++
	mt := time.Unix(1_000_000+int64(h.seq), 0)
	if err := os.Chtimes(filepath.Join(h.dir, RecordFile), mt, mt); err != nil {
		t.Fatal(err)
	}
	h.pollHooks()
}

func (h *hooksTracker) rec(event string) Record {
	return Record{Event: event, At: h.clk.Now(), Launch: "tok-1", Session: "sess-1"}
}

func TestRecordFromHook(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.FixedZone("x", 3600))
	env := mapEnv(map[string]string{"MASH_LAUNCH_TOKEN": "tok-1", "CLAUDE_CODE_SESSION_ID": "env-sess"})
	cases := []struct {
		name  string
		in    string
		env   func(string) string
		want  Record
		wantK bool
	}{}
	for _, ev := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "PreCompact", "PostCompact"} {
		cases = append(cases, struct {
			name  string
			in    string
			env   func(string) string
			want  Record
			wantK bool
		}{ev, `{"hook_event_name":"` + ev + `","session_id":"s1","tool_name":"Bash"}`, env,
			Record{Event: ev, At: now.UTC(), Session: "s1", Launch: "tok-1"}, true})
	}
	type c = struct {
		name  string
		in    string
		env   func(string) string
		want  Record
		wantK bool
	}
	cases = append(cases,
		c{"permission keeps the tool", `{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"AskUserQuestion"}`, env,
			Record{Event: "PermissionRequest", Tool: "AskUserQuestion", At: now.UTC(), Session: "s1", Launch: "tok-1"}, true},
		c{"subagent ignored", `{"hook_event_name":"PreToolUse","session_id":"s1","agent_id":"a7"}`, env, Record{}, false},
		c{"untracked event", `{"hook_event_name":"SessionStart","session_id":"s1"}`, env, Record{}, false},
		c{"no event", `{}`, env, Record{}, false},
		c{"bad json", `{"hook_event_name":`, env, Record{}, false},
		c{"empty input", ``, env, Record{}, false},
		c{"session from env", `{"hook_event_name":"Stop"}`, env,
			Record{Event: "Stop", At: now.UTC(), Session: "env-sess", Launch: "tok-1"}, true},
		c{"no env", `{"hook_event_name":"Stop","session_id":"s1"}`, mapEnv(nil),
			Record{Event: "Stop", At: now.UTC(), Session: "s1"}, true},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RecordFromHook([]byte(tc.in), tc.env, now)
			if ok != tc.wantK || (ok && got != tc.want) {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, tc.want, tc.wantK)
			}
		})
	}
}

func TestWriteRecordAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	r := Record{Event: "Stop", At: time.Now().UTC(), Session: "s"}
	if err := WriteRecord(dir, r); err != nil {
		t.Fatal(err)
	}
	if err := WriteRecord(dir, Record{Event: "PreToolUse", At: time.Now().UTC(), Session: "s"}); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != RecordFile {
		t.Fatalf("dir holds %v, want only %s", ents, RecordFile)
	}
	fi, _ := os.Stat(filepath.Join(dir, RecordFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestWriteRecordMissingDirCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	if err := WriteRecord(dir, Record{Event: "Stop"}); err == nil {
		t.Fatal("want an error")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("dir was created")
	}
}

func TestHooksFileTolerant(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	path := filepath.Join(h.dir, RecordFile)
	h.pollHooks() // absent
	if _, ok := h.Current(); ok {
		t.Fatal("absent file gave activity")
	}
	for _, junk := range []string{"", "not json", `{"event":"Pre`, "\x00\x01"} {
		os.WriteFile(path, []byte(junk), 0o600)
		h.pollHooks()
		if _, ok := h.Current(); ok {
			t.Fatalf("garbage %q gave activity", junk)
		}
	}
	h.write(t, h.rec("PreToolUse"))
	if a, ok := h.Current(); !ok || a.State != StateWorking {
		t.Fatalf("after a good record: %+v %v", a, ok)
	}
	// A later half-written file keeps the last good record.
	os.WriteFile(path, []byte(`{"event":`), 0o600)
	os.Chtimes(path, time.Unix(2_000_000, 0), time.Unix(2_000_000, 0))
	h.pollHooks()
	if a, ok := h.Current(); !ok || a.State != StateWorking {
		t.Fatalf("after a partial file: %+v %v", a, ok)
	}
	// The file going away clears it.
	os.Remove(path)
	h.pollHooks()
	if _, ok := h.Current(); ok {
		t.Fatal("removed file still gave activity")
	}
}

func TestHooksRecordMatching(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		rec  Record
		want bool
	}{
		{"launch matches", envLaunch, Record{Event: "Stop", Launch: "tok-1"}, true},
		{"session matches", envLaunch, Record{Event: "Stop", Session: "sess-1"}, true},
		{"both match", envLaunch, Record{Event: "Stop", Launch: "tok-1", Session: "sess-1"}, true},
		{"other launch and other session", envLaunch, Record{Event: "Stop", Launch: "tok-2", Session: "sess-2"}, false},
		{"no ids in record", envLaunch, Record{Event: "Stop"}, false},
		{"no ids in env", nil, Record{Event: "Stop", Launch: "tok-1", Session: "sess-1"}, false},
		{"session only env", map[string]string{"CLAUDE_CODE_SESSION_ID": "sess-1"}, Record{Event: "Stop", Session: "sess-1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHooksTracker(t, tc.env)
			tc.rec.At = h.clk.Now()
			h.write(t, tc.rec)
			_, ok := h.Current()
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestHooksEventStates(t *testing.T) {
	cases := []struct {
		event, tool string
		state       State
		kind        Kind
	}{
		{"UserPromptSubmit", "", StateWorking, ""},
		{"PreToolUse", "", StateWorking, ""},
		{"PostToolUse", "", StateWorking, ""},
		{"PreCompact", "", StateWorking, ""},
		{"Stop", "", StateDone, ""},
		{"PostCompact", "", StateDone, ""},
		{"PermissionRequest", "Bash", StateBlocked, KindPermission},
		{"PermissionRequest", "", StateBlocked, KindPermission},
		{"PermissionRequest", "AskUserQuestion", StateBlocked, KindQuestion},
	}
	for _, tc := range cases {
		t.Run(tc.event+"/"+tc.tool, func(t *testing.T) {
			h := newHooksTracker(t, envLaunch)
			r := h.rec(tc.event)
			r.Tool = tc.tool
			h.write(t, r)
			a, ok := h.Current()
			if !ok || a.State != tc.state || a.Kind != tc.kind || a.Source != "hooks" || !a.Since.Equal(r.At) {
				t.Fatalf("got %+v %v", a, ok)
			}
			if a.State == StateIdle {
				t.Fatal("hooks said idle")
			}
		})
	}
}

func TestHooksUnknownEventInFileIsAbsent(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	h.write(t, h.rec("SessionStart"))
	if _, ok := h.Current(); ok {
		t.Fatal("an untracked event in the file gave activity")
	}
}

func TestHooksStaleNonBlockedIsAbsent(t *testing.T) {
	for _, ev := range []string{"UserPromptSubmit", "Stop", "PostCompact"} {
		h := newHooksTracker(t, envLaunch)
		h.write(t, h.rec(ev))
		h.clk.Advance(HooksStale)
		if _, ok := h.Current(); !ok {
			t.Fatalf("%s: exactly at the limit is still current", ev)
		}
		h.clk.Advance(time.Second)
		if _, ok := h.Current(); ok {
			t.Fatalf("%s: a stale record gave activity", ev)
		}
	}
}

func TestHooksStaleBlockedStillReportedWithTrueSince(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	r := h.rec("PermissionRequest")
	r.Tool = "Bash"
	h.write(t, r)
	first, _ := h.Current()
	h.clk.Advance(3*time.Hour + 30*time.Minute)
	h.pollHooks()
	a, ok := h.Current()
	if !ok || a.State != StateBlocked || a.Kind != KindPermission || !a.Since.Equal(r.At) || !a.Since.Equal(first.Since) {
		t.Fatalf("got %+v %v, want blocked since %v", a, ok, r.At)
	}
}

func TestHooksStartedWithOldBlockedRecordKeepsItsSince(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	r := h.rec("PermissionRequest")
	r.At = h.clk.Now().Add(-3 * time.Hour) // written before this process started
	h.write(t, r)
	a, ok := h.Current()
	if !ok || !a.Since.Equal(r.At) {
		t.Fatalf("got %+v %v", a, ok)
	}
}

func TestHooksNeverIdleWhateverTheEvent(t *testing.T) {
	for ev := range trackedEvents {
		r := Record{Event: ev}
		if st, _, ok := r.stateOf(); !ok || st == StateIdle {
			t.Fatalf("%s maps to %q", ev, st)
		}
	}
}

func TestHooksPollGoroutineReadsFile(t *testing.T) {
	dir := t.TempDir()
	tr := New(Options{Env: mapEnv(envLaunch), Dir: dir, HooksPoll: 2 * time.Millisecond})
	tr.Start(t.Context())
	t.Cleanup(func() { waitGroup(t, tr) })
	if err := WriteRecord(dir, Record{Event: "UserPromptSubmit", At: time.Now(), Launch: "tok-1"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, tr, StateWorking)
	if err := WriteRecord(dir, Record{Event: "Stop", At: time.Now(), Launch: "tok-1"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, tr, StateDone)
}
