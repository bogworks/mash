package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

var shownSeq = regexp.MustCompile(`(?m)^--- #(\d+) from`)

// hookContext is the additionalContext in a hook's stdout ("" for none).
func hookContext(t *testing.T, stdout []byte) string {
	t.Helper()
	if len(bytes.TrimSpace(stdout)) == 0 {
		return ""
	}
	var o struct {
		Specific struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout, &o); err != nil {
		t.Fatalf("hook output %q: %v", stdout, err)
	}
	return o.Specific.Context
}

func seqsShown(text string) []int {
	var out []int
	for _, m := range shownSeq.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// runMashHook runs the built `mash hook` as a child of this test process. With a failpoint it waits for the hook to
// reach it, kills it with SIGKILL there, and returns what it had printed; without one it runs to the end.
func runMashHook(t *testing.T, env map[string]string, failpoint string) string {
	t.Helper()
	cmd := exec.Command(mashBinary(t), "hook")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if failpoint != "" {
		cmd.Env = append(cmd.Env, "MASH_HOOK_FAILPOINT="+failpoint)
	}
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PostToolUse"}`)
	var out bytes.Buffer
	cmd.Stdout = &out
	errp, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if failpoint == "" {
		_, _ = bufio.NewReader(errp).WriteTo(&bytes.Buffer{})
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		return hookContext(t, out.Bytes())
	}
	reached := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(errp)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "failpoint "+failpoint) {
				reached <- true
				return
			}
		}
		reached <- false
	}()
	select {
	case ok := <-reached:
		if !ok {
			cmd.Wait()
			t.Fatalf("the hook ended without reaching failpoint %s (stdout %q)", failpoint, out.String())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("failpoint %s not reached", failpoint)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait() // "signal: killed"
	return hookContext(t, out.Bytes())
}

// Hook delivery is at-least-once: a hook killed at any point of its run loses nothing, and what the next run shows
// again is only what the killed run had begun to show, each marked as re-delivered so the model does not act twice.
func TestHookCrashMatrix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGKILL semantics or flock on windows")
	}
	const note = " re-delivered after an interrupted hook: it may already have been shown; do not act on it twice)"
	for _, c := range []struct {
		failpoint string
		interrupt []int // what the killed run printed
		dups      []int // what the next run shows again, with the note
	}{
		{"before-print", nil, nil},
		{"after-print", []int{1, 2, 3}, []int{1, 2, 3}},
		{"after-first-drained", []int{1, 2, 3}, []int{2, 3}},
	} {
		t.Run(c.failpoint, func(t *testing.T) {
			env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1", "MASH_CREDS": "/none"}
			sp := spoolOf(env)
			if err := sp.Init(); err != nil {
				t.Fatal(err)
			}
			// The hook is a child of this process, so this process is the Claude process that owns the spool.
			if err := sp.Own("pid:" + strconv.Itoa(os.Getpid())); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 3; i++ {
				if err := sp.Put(spool.Entry{Seq: uint64(i), From: "b", Trust: "verified", At: time.Now(), Body: []byte("m" + strconv.Itoa(i))}); err != nil {
					t.Fatal(err)
				}
			}
			first := runMashHook(t, env, c.failpoint)
			second := runMashHook(t, env, "")
			if got := seqsShown(first); !equalInts(got, c.interrupt) {
				t.Fatalf("the killed run showed %v, want %v:\n%s", got, c.interrupt, first)
			}
			count := map[int]int{}
			for _, s := range append(seqsShown(first), seqsShown(second)...) {
				count[s]++
			}
			for i := 1; i <= 3; i++ {
				if count[i] == 0 {
					t.Errorf("#%d was lost (first run %v, second %v)", i, seqsShown(first), seqsShown(second))
				}
				if count[i] > 1 && !containsInt(c.interrupt, i) {
					t.Errorf("#%d shown twice but the killed run did not show it", i)
				}
			}
			for _, d := range c.dups {
				if count[d] != 2 {
					t.Errorf("#%d shown %d times, want 2", d, count[d])
				}
			}
			for _, s := range seqsShown(second) {
				has := strings.Contains(second, "(#"+strconv.Itoa(s)+note)
				// An entry the killed run printed is flagged on re-show; a first showing is not. before-print
				// marked before it died, so the flag may appear on an entry nothing printed: "may" holds.
				if containsInt(c.interrupt, s) && !has {
					t.Errorf("#%d re-shown without the re-delivered note:\n%s", s, second)
				}
				if c.failpoint != "before-print" && !containsInt(c.interrupt, s) && has {
					t.Errorf("#%d flagged re-delivered on its first showing", s)
				}
			}
			if again := runMashHook(t, env, ""); again != "" {
				t.Fatalf("a third run showed %q", again)
			}
			if w, _ := sp.Waiting(); len(w) != 0 {
				t.Fatalf("left waiting: %+v", w)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsInt(a []int, n int) bool {
	for _, x := range a {
		if x == n {
			return true
		}
	}
	return false
}

// A shown mark that cannot be written does not stop the print (at-least-once wins): the mail is shown and drained,
// and stderr says what failed.
func TestHookShowsWhenTheShownMarkFails(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1", "MASH_CREDS": "/none"}
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sp.Dir + "/shown"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp.Dir+"/shown", nil, 0o600); err != nil { // a file where the directory belongs
		t.Fatal(err)
	}
	if err := sp.Put(spool.Entry{Seq: 1, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("held")}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(hookContext(t, out.Bytes()), "> held") || !strings.Contains(errb.String(), "recording what is being shown") {
		t.Fatalf("stdout %q stderr %q", out.String(), errb.String())
	}
}
