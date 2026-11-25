package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An error before a command runs is one framed error: text on stderr with a hint, or with --json {"error","hint"} on
// stdout and nothing on stderr, with the same exit code.
func TestPreRunErrorsAreFramed(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name  string
		env   map[string]string
		args  []string
		code  int
		err   string // the error line, without "error: "
		hint  string
		jsonO string // expected stdout with --json
	}{
		{"global flag without value", nil, []string{"status", "--endpoint"}, 2, "--endpoint needs a value", "mash --help",
			`{"error":"--endpoint needs a value","hint":"mash --help"}`},
		{"plain URL refused", map[string]string{"MASH_ENDPOINT": "o.dev.a", "MASH_SERVER": "nats://x:4222"}, []string{"send", "o.dev.a", "x"}, 2, "", "mash doctor", ""},
		{"no command", nil, []string{}, 2, "mash needs a command", "mash --help", `{"error":"mash needs a command","hint":"mash --help"}`},
	}
	for _, c := range cases {
		env := map[string]string{"HOME": home}
		for k, v := range c.env {
			env[k] = v
		}
		out, errb, code := statusOut(t, env, c.args...)
		if code != c.code || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.Contains(errb, "\nhint: "+c.hint+"\n") || strings.Count(errb, "\n") != 2 {
			t.Errorf("%s: text: %d %q %q", c.name, code, out, errb)
		}
		if c.err != "" && errb != "error: "+c.err+"\nhint: "+c.hint+"\n" {
			t.Errorf("%s: text: %q", c.name, errb)
		}
		jargs := append([]string{"--json"}, c.args...) // first: after a flag that needs a value it would be taken as the value
		jout, jerr, jcode := statusOut(t, env, jargs...)
		if jcode != code || jerr != "" || !strings.HasPrefix(jout, `{"error":"`) || !strings.HasSuffix(jout, `","hint":"`+c.hint+"\"}\n") || strings.Count(jout, "\n") != 1 {
			t.Errorf("%s: json: %d %q %q", c.name, jcode, jout, jerr)
		}
		if c.jsonO != "" && jout != c.jsonO+"\n" {
			t.Errorf("%s: json: %q", c.name, jout)
		}
	}
}

// A config that cannot be read is a failure (exit 1) with the status hint; status itself still reports it, as its reason.
func TestPreRunConfigErrorIsFramed(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/config.json", `{not json`); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": "o.dev.a"}
	out, errb, code := statusOut(t, env, "--config-dir", dir, "send", "o.dev.a", "x")
	if code != 1 || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.HasSuffix(errb, "\nhint: mash doctor\n") {
		t.Fatalf("text: %d %q %q", code, out, errb)
	}
	jout, jerr, jcode := statusOut(t, env, "--config-dir", dir, "--json", "send", "o.dev.a", "x")
	if jcode != 1 || jerr != "" || !strings.HasPrefix(jout, `{"error":"`) || !strings.HasSuffix(jout, `","hint":"mash doctor"}`+"\n") {
		t.Fatalf("json: %d %q %q", jcode, jout, jerr)
	}
	if sout, _, scode := statusOut(t, env, "--config-dir", dir, "status"); scode != 1 || sout == "" {
		t.Fatalf("status: %d %q", scode, sout)
	}
}

// status with a stray argument is a usage error.
func TestStatusStrayArgumentIsFramed(t *testing.T) {
	out, errb, code := statusOut(t, map[string]string{"HOME": t.TempDir()}, "status", "extra")
	if code != 2 || out != "" || !strings.HasPrefix(errb, "error: unexpected argument ") || !strings.HasSuffix(errb, "\nhint: mash status --help\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }

// mash send with no message reads standard input; a pipe that never writes ends at the stdin limit (stdinLimit, 15 minutes)
// with exit 1, before any endpoint is opened.
func TestSendImplicitStdinDeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open, never written
	env := envFrom(map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": "o.dev.a", "MASH_SERVER": "tls://127.0.0.1:1"})
	defer func(old time.Duration) { stdinLimit = old }(stdinLimit)
	stdinLimit = 300 * time.Millisecond
	var o, e strings.Builder
	start := time.Now()
	code := run([]string{"send", "o.dev.b"}, env, r, &o, quiet(&e))
	if code != 1 || o.String() != "" || e.String() != "error: no message text arrived on standard input\n" || time.Since(start) > 10*time.Second {
		t.Fatalf("%d %q %q after %v", code, o.String(), e.String(), time.Since(start))
	}
	o.Reset()
	e.Reset()
	code = run([]string{"send", "o.dev.b", "--json"}, env, r, &o, quiet(&e))
	if code != 1 || e.String() != "" || o.String() != `{"error":"no message text arrived on standard input"}`+"\n" {
		t.Fatalf("json: %d %q %q", code, o.String(), e.String())
	}
}

// The errors before who runs are framed as JSON with --json: on stdout, nothing on stderr, and
// the same exit code as the text form.
func TestWhoJSONFramesPreVerbErrors(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]string{
		"broken config": {"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")},
		"plain url":     plainEnv(t, "nats://127.0.0.1:1"),
	} {
		var o, e, o2, e2 strings.Builder
		codeText := run([]string{"who"}, envStrict(env), strings.NewReader(""), &o2, &e2)
		code := run([]string{"who", "--json"}, envStrict(env), strings.NewReader(""), &o, &e)
		if code == 0 || code != codeText || e.String() != "" || !strings.HasPrefix(o.String(), `{"error":`) || e2.String() == "" {
			t.Fatalf("%s: %d/%d out=%q err=%q text-err=%q", name, code, codeText, o.String(), e.String(), e2.String())
		}
	}
}
