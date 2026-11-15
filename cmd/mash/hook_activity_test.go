package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/activity"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/spool"
)

// activityEnv is an endpoint's environment with its spool directory made, as serve makes it. It needs no server: the
// hook connects to nothing.
func activityEnv(t *testing.T) (map[string]string, string) {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_URL": "tls://x:4222", "MASH_OWNER": "o", "MASH_CREDS": "/none"}
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	return env, sp.Dir
}

// runHookRaw runs `mash hook` on stdin and returns its stdout and stderr; the exit code must be 0.
func runHookRaw(t *testing.T, env map[string]string, stdin string) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(stdin), &out, &errb); code != 0 {
		t.Fatalf("hook exited %d: %s", code, errb.String())
	}
	return out.String(), errb.String()
}

func readActivityRecord(t *testing.T, dir string) (activity.Record, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, activity.RecordFile))
	if os.IsNotExist(err) {
		return activity.Record{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var r activity.Record
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r, true
}

func TestHookRecordsEachTrackedEvent(t *testing.T) {
	for _, ev := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "PreCompact", "PostCompact"} {
		t.Run(ev, func(t *testing.T) {
			env, dir := activityEnv(t)
			env["MASH_LAUNCH_TOKEN"] = "tok"
			out, _ := runHookRaw(t, env, `{"hook_event_name":"`+ev+`","session_id":"s1","tool_name":"Bash"}`)
			if out != "" {
				t.Fatalf("stdout %q, want none", out)
			}
			r, ok := readActivityRecord(t, dir)
			if !ok || r.Event != ev || r.Session != "s1" || r.Launch != "tok" || r.At.IsZero() {
				t.Fatalf("record %+v, %v", r, ok)
			}
			if (ev == "PermissionRequest") != (r.Tool == "Bash") {
				t.Fatalf("tool %q for %s", r.Tool, ev)
			}
		})
	}
}

// Recording the activity leaves the mail delivery as it was: the same context is printed and the mail drained.
func TestHookRecordsActivityAndStillDeliversMail(t *testing.T) {
	for _, ev := range []string{"PostToolUse", "UserPromptSubmit"} {
		t.Run(ev, func(t *testing.T) {
			env, dir := activityEnv(t)
			sp := spoolOf(env)
			if err := sp.Put(spool.Entry{Seq: 1, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("hello")}); err != nil {
				t.Fatal(err)
			}
			got := runHookAs(t, env, ev, "s1")
			if !strings.Contains(got, "mash mail for a, delivered by mash's hook") || !strings.Contains(got, "> hello") {
				t.Fatalf("delivery changed:\n%s", got)
			}
			if w, _ := sp.Waiting(); len(w) != 0 {
				t.Fatalf("left %+v", w)
			}
			if r, ok := readActivityRecord(t, dir); !ok || r.Event != ev {
				t.Fatalf("record %+v, %v", r, ok)
			}
		})
	}
}

func TestHookRecordsNothing(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env map[string]string, dir string)
		in    string
	}{
		{"subagent", nil, `{"hook_event_name":"PostToolUse","agent_id":"x"}`},
		{"untracked event", nil, `{"hook_event_name":"Notification"}`},
		{"bad json", nil, `{not json`},
		{"empty stdin", nil, ``},
		{"off", func(env map[string]string, _ string) { env["MASH_ACTIVITY"] = "off" }, `{"hook_event_name":"Stop"}`},
		{"bad setting", func(env map[string]string, _ string) { env["MASH_ACTIVITY"] = "maybe" }, `{"hook_event_name":"Stop"}`},
		{"inert", func(env map[string]string, _ string) { delete(env, "MASH_NAME") }, `{"hook_event_name":"Stop"}`},
		{"missing spool dir", func(_ map[string]string, dir string) { os.RemoveAll(dir) }, `{"hook_event_name":"Stop"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, dir := activityEnv(t)
			if tc.setup != nil {
				tc.setup(env, dir)
			}
			out, _ := runHookRaw(t, env, tc.in)
			if out != "" {
				t.Fatalf("stdout %q, want none", out)
			}
			if _, ok := readActivityRecord(t, dir); ok {
				t.Fatal("an activity record was written")
			}
			if tc.name == "missing spool dir" {
				if _, err := os.Stat(dir); err == nil {
					t.Fatal("the hook created the spool directory")
				}
			}
		})
	}
}

// A configuration the hook refuses for TLS still has its activity recorded: the record needs no TLS, and the refusal
// is said on stderr with nothing on stdout.
func TestHookRecordsActivityUnderATLSRefusal(t *testing.T) {
	env := requireTLSEnv(t)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	out, errb := runHookRaw(t, env, `{"hook_event_name":"Stop","session_id":"s1"}`)
	if out != "" {
		t.Fatalf("stdout %q", out)
	}
	if !strings.Contains(errb, refusalHost) {
		t.Fatalf("stderr %q lacks the refusal", errb)
	}
	if r, ok := readActivityRecord(t, sp.Dir); !ok || r.Event != "Stop" {
		t.Fatalf("record %+v, %v", r, ok)
	}
}

func TestStatusActivityLine(t *testing.T) {
	line := func(src *statusSource) string {
		for _, l := range strings.Split(statusText(src), "\n") {
			if strings.HasPrefix(l, "activity:") {
				return l
			}
		}
		return ""
	}
	none := func(string) string { return "" }
	off := activity.New(activity.Options{Off: true, Env: none})
	if got := line(&statusSource{cfg: config.Config{Name: "a"}, tracker: off, srv: &atomic.Pointer[mcp.Server]{}, getenv: none}); got != "activity: off" {
		t.Fatalf("off: %q", got)
	}
	absent := activity.New(activity.Options{Env: none})
	if got := line(&statusSource{cfg: config.Config{Name: "a"}, tracker: absent, srv: &atomic.Pointer[mcp.Server]{}, getenv: none}); !strings.HasPrefix(got, "activity: absent (") {
		t.Fatalf("absent: %q", got)
	}
	if got := line(&statusSource{cfg: config.Config{Name: "a"}, cli: true, getenv: none}); got != "activity: reported by a running session" {
		t.Fatalf("cli: %q", got)
	}
	if got := line(&statusSource{cfg: config.Config{Name: "a"}, srv: &atomic.Pointer[mcp.Server]{}, getenv: none}); got != "" {
		t.Fatalf("no tracker: %q", got)
	}
}
