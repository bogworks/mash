package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// With MASH_HOST=pi the hook reads the extension's JSON and prints the batch as plain text, and drains it.
func TestHookWithPiPrintsPlainText(t *testing.T) {
	env := hookEnv(t, map[string]string{"MASH_HOST": "pi"})
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Put(spool.Entry{Seq: 1, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("hello pi")}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"event":"tool_result","session":"s1"}`), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := out.String()
	if strings.HasPrefix(got, "{") || !strings.Contains(got, "> hello pi") || !strings.Contains(got, "1 message from b") {
		t.Fatalf("not plain text:\n%s", got)
	}
	if w, _ := sp.Waiting(); len(w) != 0 {
		t.Fatalf("left %+v", w)
	}
	// Claude's input is not Pi's: nothing is printed, nothing drained.
	if err := sp.Put(spool.Entry{Seq: 2, From: "b", Trust: "claimed", At: time.Now(), Body: []byte("again")}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
	if w, _ := sp.Waiting(); out.Len() != 0 || len(w) != 1 {
		t.Fatalf("out %q waiting %d", out.String(), len(w))
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
