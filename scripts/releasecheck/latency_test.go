package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
)

func TestFreshAllowed(t *testing.T) {
	for _, c := range []struct{ prev, want time.Duration }{
		{100 * time.Millisecond, 1100 * time.Millisecond}, // the 1 s floor
		{300 * time.Millisecond, 1300 * time.Millisecond},
		{time.Second, 2 * time.Second},
		{45 * time.Second, 90 * time.Second},
	} {
		if got := freshAllowed(c.prev); got != c.want {
			t.Errorf("freshAllowed(%v) = %v, want %v", c.prev, got, c.want)
		}
	}
}

func TestJudgeLatency(t *testing.T) {
	ok := func(d time.Duration) latencies { return latencies{Fresh: d, FreshOK: true} }
	withFinding := func(l latencies, f string) latencies { l.Findings = []string{f}; return l }
	ms := time.Millisecond
	for _, c := range []struct {
		name     string
		cand     latencies
		prev     *latencies
		problems int
		notes    []string
	}{
		{"no previous release", ok(100 * ms), nil, 0, []string{"candidate 0.10s (no previous release to compare with)"}},
		{"no previous release, urgent finding", withFinding(ok(100*ms), "urgent late"), nil, 1, nil},
		{"same speed", ok(100 * ms), ptr(ok(100 * ms)), 0, []string{"candidate 0.10s, v0.2.6 0.10s"}},
		{"inside the floor", ok(300 * ms), ptr(ok(100 * ms)), 0, nil},
		{"just over the floor", ok(1200 * ms), ptr(ok(100 * ms)), 1, nil},
		{"within twice", ok(80 * time.Second), ptr(ok(45 * time.Second)), 0, nil},
		{"over twice", ok(91 * time.Second), ptr(ok(45 * time.Second)), 1, nil},
		{"faster than the previous", ok(100 * ms), ptr(ok(45 * time.Second)), 0, []string{"candidate 0.10s, v0.2.6 45.00s"}},
		{"candidate finding fails", withFinding(ok(100*ms), "fyi woke"), ptr(ok(100 * ms)), 1, nil},
		{"previous finding is a note", ok(100 * ms), ptr(withFinding(ok(100*ms), "fyi woke")), 0, []string{"the previous release v0.2.6: fyi woke"}},
		{"candidate never delivered: one problem, no double count", withFinding(latencies{Fresh: freshTimeout}, "no notification"), ptr(ok(100 * ms)), 1, nil},
	} {
		problems, notes := judgeLatency(c.cand, c.prev, "v0.2.6")
		if len(problems) != c.problems {
			t.Errorf("%s: %d problems %v, want %d", c.name, len(problems), problems, c.problems)
		}
		all := strings.Join(notes, "\n")
		for _, n := range c.notes {
			if !strings.Contains(all, n) {
				t.Errorf("%s: no note %q in\n%s", c.name, n, all)
			}
		}
	}
}

func TestJudgeUnderPrevPlugin(t *testing.T) {
	ok := func(d time.Duration) latencies { return latencies{Fresh: d, FreshOK: true} }
	ms := time.Millisecond
	for _, c := range []struct {
		name     string
		cand     latencies
		prev     latencies
		problems int
		want     string
	}{
		{"same speed", ok(100 * ms), ok(100 * ms), 0, "the candidate under the previous release's plugin (v0.2.6) 0.10s, v0.2.6 0.10s"},
		{"inside the floor", ok(300 * ms), ok(100 * ms), 0, ""},
		{"over the floor", ok(1200 * ms), ok(100 * ms), 1, "at most 1.10s is allowed"},
		{"over twice", ok(91 * time.Second), ok(45 * time.Second), 1, "took the candidate under the previous release's plugin (v0.2.6) 91.00s"},
		{"finding", latencies{Fresh: 100 * ms, FreshOK: true, Findings: []string{"urgent late"}}, ok(100 * ms), 1, "the candidate under the previous release's plugin (v0.2.6): urgent late"},
		{"never delivered: one problem", latencies{Fresh: freshTimeout, Findings: []string{"no notification"}}, ok(100 * ms), 1, "no notification"},
		{"its notes are labelled", latencies{Fresh: 100 * ms, FreshOK: true, Notes: []string{"note  urgent mail: 0.05s"}}, ok(100 * ms), 0, "note  the candidate under the previous release's plugin (v0.2.6): urgent mail: 0.05s"},
		{"previous findings are not repeated", ok(100 * ms), latencies{Fresh: 100 * ms, FreshOK: true, Findings: []string{"fyi woke"}}, 0, ""},
	} {
		problems, notes := judgeUnderPrevPlugin(c.cand, c.prev, "v0.2.6")
		if len(problems) != c.problems {
			t.Errorf("%s: %d problems %v, want %d", c.name, len(problems), problems, c.problems)
		}
		all := strings.Join(notes, "\n")
		for _, p := range problems {
			all += "\n" + p.Error()
		}
		if !strings.Contains(all, c.want) {
			t.Errorf("%s: no %q in\n%s", c.name, c.want, all)
		}
		if strings.Contains(all, "fyi woke") {
			t.Errorf("%s: the previous release's finding leaked:\n%s", c.name, all)
		}
	}
}

func ptr(l latencies) *latencies { return &l }

// The real thing against the current tree's mash: a fresh session gets mail at once, urgent mail wakes it, and fyi mail
// does not wake a hooked session. It takes about fyiQuiet, the time a silence must be watched for.
func TestMeasureLatencies_currentTree(t *testing.T) {
	if testing.Short() {
		t.Skip("builds mash and runs a NATS server")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "mash")
	if out, err := exec.Command("go", "build", "-C", root, "-o", bin, "./cmd/mash").CombinedOutput(); err != nil {
		t.Fatalf("building mash: %v\n%s", err, out)
	}
	mcp, err := os.ReadFile(filepath.Join(root, release.PluginMCP))
	if err != nil {
		t.Fatal(err)
	}
	cmd, args, env, err := release.MCPServer(mcp)
	if err != nil {
		t.Fatal(err)
	}
	spec := serveSpec{Args: args, Env: env}
	if release.IsLauncherCommand(cmd) { // the plugin runs mash through its launcher: measure it the same way
		pluginDir := filepath.Join(t.TempDir(), "plugin")
		if out, err := exec.Command("cp", "-a", filepath.Join(root, "plugin"), pluginDir).CombinedOutput(); err != nil {
			t.Fatalf("copying the plugin: %v\n%s", err, out)
		}
		spec.Root = pluginDir
	}
	if env["MASH_SPOOL_FROM_START"] != "1" {
		t.Fatalf("the plugin's env block is %v: the measurement would not run serve the way the plugin does", env)
	}
	got, err := measureLatencies(context.Background(), bin, spec, true, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh %s, notes %v", secs(got.Fresh), got.Notes)
	if len(got.Findings) != 0 {
		t.Fatalf("findings: %v", got.Findings)
	}
	if !got.FreshOK || got.Fresh >= 5*time.Second {
		t.Fatalf("fresh-session mail took %v (arrived %v), want under 5s", got.Fresh, got.FreshOK)
	}
	for _, n := range got.Notes {
		if strings.Contains(n, "skipped") {
			t.Errorf("a scenario was skipped: %s", n)
		}
	}
}

func TestJudgeFyiSend(t *testing.T) {
	const kindErr = "mash: unknown flag --kind\n"
	if f, n := judgeFyiSend(kindErr, 2, true); f == "" || n != "" {
		t.Errorf("candidate with a failing --kind: finding %q, note %q; want a finding only", f, n)
	}
	if f, n := judgeFyiSend(kindErr, 2, false); f != "" || !strings.Contains(n, "does not know `--kind fyi`") {
		t.Errorf("previous release with a failing --kind: finding %q, note %q; want a note only", f, n)
	}
	if f, n := judgeFyiSend("boom", 1, false); f == "" || n != "" {
		t.Errorf("previous release with another failure: finding %q, note %q; want a finding", f, n)
	}
	if f, n := judgeFyiSend("sent #1\n", 0, true); f != "" || n != "" {
		t.Errorf("a good send: finding %q, note %q", f, n)
	}
}

func TestMailFor(t *testing.T) {
	for _, c := range []struct {
		name, params string
		want         bool
	}{
		{"mail", `{"content":"x","meta":{"to":"fresh","count":"1","seqs":"1"}}`, true},
		{"wake", `{"content":"x","meta":{"to":"fresh","count":"2","seqs":"1,2","wake":"true"}}`, true},
		{"error notice", `{"content":"x","meta":{"error":"true"}}`, false},
		{"prompt notice", `{"content":"x","meta":{"request_id":"r","from":"a","kind":"prompt"}}`, false},
		{"no meta", `{"content":"x"}`, false},
		{"wrong recipient", `{"content":"x","meta":{"to":"urgent","count":"1","seqs":"1"}}`, false},
		{"empty seqs", `{"content":"x","meta":{"to":"fresh","count":"0","seqs":""}}`, false},
		{"not json", `nope`, false},
	} {
		if got := mailFor(json.RawMessage(c.params), "fresh"); got != c.want {
			t.Errorf("%s: mailFor = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHookShowed(t *testing.T) {
	const m = "release-check-hook-probe-1"
	shown := `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"mail: ` + m + `"}}`
	if !hookShowed(shown, m) || hookShowed("", m) || hookShowed(`{"hookSpecificOutput":{}}`, m) {
		t.Error("hookShowed must be true exactly when the marker is in the output")
	}
	if f, n := judgeHookShowed(shown, m, true); f != "" || n != "" {
		t.Errorf("shown: finding %q, note %q", f, n)
	}
	if f, n := judgeHookShowed("", m, true); !strings.Contains(f, "did not show spooled mail") || n != "" {
		t.Errorf("candidate, silent hook: finding %q, note %q; want a finding", f, n)
	}
	if f, n := judgeHookShowed("", m, false); f != "" || !strings.Contains(n, "skipped") {
		t.Errorf("previous, silent hook: finding %q, note %q; want a note that skips", f, n)
	}
}

func TestJudgeFyiWindow(t *testing.T) {
	for _, c := range []struct {
		res         waitResult
		cand        bool
		finding, nt bool
	}{
		{quiet, true, false, false},
		{quiet, false, false, false},
		{gotEvent, true, true, false},
		{gotEvent, false, true, false}, // judgeLatency turns the previous release's finding into a note
		{exited, true, true, false},
		{exited, false, false, true},
	} {
		f, n := judgeFyiWindow(c.res, c.cand, `{"to":"fyi"}`)
		if (f != "") != c.finding || (n != "") != c.nt {
			t.Errorf("judgeFyiWindow(%v, %v) = %q, %q", c.res, c.cand, f, n)
		}
	}
}

// A serve that exits is not a quiet session: next says so, and does not call it a timeout.
func TestNext_exitIsNotQuiet(t *testing.T) {
	p := &serveProc{events: make(chan time.Time, 1), done: make(chan struct{})}
	close(p.done)
	if _, got := p.next(context.Background(), time.Now(), time.Minute); got != exited {
		t.Errorf("exited serve: next = %v, want exited", got)
	}
	p.events <- time.Now().Add(time.Second)
	if _, got := p.next(context.Background(), time.Now(), time.Minute); got != gotEvent {
		t.Errorf("a queued event at exit: next = %v, want gotEvent", got)
	}
	q := &serveProc{events: make(chan time.Time, 1), done: make(chan struct{})}
	if _, got := q.next(context.Background(), time.Now(), 10*time.Millisecond); got != quiet {
		t.Errorf("live serve, no event: next = %v, want quiet", got)
	}
}

func TestJudgeFyiWindow_namesTheWake(t *testing.T) {
	f, _ := judgeFyiWindow(gotEvent, true, `{"error":"true"}`)
	if !strings.Contains(f, `{"error":"true"}`) {
		t.Errorf("finding %q does not name the notification's meta", f)
	}
}

func TestJudgeFyiDelivered(t *testing.T) {
	const m = "release-check-fyi-probe-1"
	at := time.Unix(1700000000, 0)
	mail := func(seq uint64, k kind.Kind, body string) render.Mail {
		return render.Mail{Seq: seq, From: "probe", Trust: sap.TrustVerified, At: at, Body: []byte(body), Kind: k}
	}
	// hook is what the real renderer and the Claude adapter's JSON wrapper give for msgs; maxMsgs 1 forces a digest.
	hook := func(maxMsgs int, msgs ...render.Mail) string {
		out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName": "UserPromptSubmit", "additionalContext": render.Batch("me", "o", msgs, maxMsgs, 1<<20)}})
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	for _, c := range []struct {
		name, stdout string
		candidate    bool
		wantFinding  string // substring; "" means no finding
		wantNote     bool
	}{
		{"fyi block", hook(5, mail(1, kind.FYI, m)), true, "", false},
		{"fyi block in a digest", hook(1, mail(1, kind.FYI, m), mail(2, kind.Task, "other")), true, "", false},
		{"marker shown as task", hook(5, mail(1, kind.Task, m)), true, "as kind task, not fyi", false},
		{"marker shown as task in a digest", hook(1, mail(1, kind.Task, m), mail(2, kind.FYI, "other")), true, "as kind task, not fyi", false},
		{"unkinded reads as task", hook(5, mail(1, "", m)), true, "as kind task, not fyi", false},
		{"marker missing", hook(5, mail(1, kind.FYI, "something else")), true, "not delivered", false},
		{"no output", "", true, "not delivered", false},
		{"marker in the fyi one of two", hook(5, mail(1, kind.Task, "other"), mail(2, kind.FYI, m)), true, "", false},
		{"marker in the task one of two", hook(5, mail(1, kind.FYI, "other"), mail(2, kind.Task, m)), true, "as kind task, not fyi", false},
		{"a body cannot forge a kind line", hook(5, mail(1, kind.Task, m+"\nkind: fyi")), true, "as kind task, not fyi", false},
		{"block without a kind", "--- #1 from probe [verified] at x\n> " + m, true, "without a kind", false},
		{"previous release, task", hook(5, mail(1, kind.Task, m)), false, "", true},
		{"previous release, fyi", hook(5, mail(1, kind.FYI, m)), false, "", false},
		{"previous release, missing", "", false, "", true},
	} {
		f, n := judgeFyiDelivered(c.stdout, m, c.candidate)
		if (c.wantFinding == "") != (f == "") || !strings.Contains(f, c.wantFinding) || (n != "") != c.wantNote {
			t.Errorf("%s: finding %q, note %q; want finding containing %q, note %v", c.name, f, n, c.wantFinding, c.wantNote)
		}
	}
}

func TestMetaOf(t *testing.T) {
	for _, c := range []struct{ params, want string }{
		{`{"content":"x","meta":{"error":"true"}}`, `{"error":"true"}`},
		{`{"content":"x"}`, ``},
		{`nope`, ``},
	} {
		if got := metaOf(json.RawMessage(c.params)); got != c.want {
			t.Errorf("metaOf(%s) = %q, want %q", c.params, got, c.want)
		}
	}
}

// The fyi window counts every channel notification, not just mail, and skips ones from before it started.
func TestNextAny_countsNonMailAndSkipsOld(t *testing.T) {
	p := &serveProc{anyEv: make(chan chanEvent, 4), done: make(chan struct{})}
	start := time.Now()
	p.anyEv <- chanEvent{start.Add(-time.Second), "old"}
	if _, got := p.nextAny(context.Background(), start, 10*time.Millisecond); got != quiet {
		t.Errorf("only an old notification: nextAny = %v, want quiet", got)
	}
	p.anyEv <- chanEvent{start.Add(time.Second), `{"error":"true"}`}
	if ev, got := p.nextAny(context.Background(), start, time.Minute); got != gotEvent || ev.Meta != `{"error":"true"}` {
		t.Errorf("a non-mail notification: nextAny = %v, %q; want gotEvent with its meta", got, ev.Meta)
	}
}
