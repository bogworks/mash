package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/testenv"
)

const credsSecret = "SECRET-CREDS-SENTINEL-do-not-print"

const pwSecret = "s3cretpw"

func toolNames(t *testing.T, s *session) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tl := range s.tools(t) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	return names
}

// toolText calls the MCP tool name and returns its text; it fails if the tool is missing or answers an error.
func toolText(t *testing.T, s *session, id int, name string) string {
	t.Helper()
	r := s.call(t, id, name, `{}`)
	if r["isError"] == true {
		t.Fatalf("%s errored: %v", name, r)
	}
	return resultText(r)
}

// reportText calls the MCP tool name and returns its text whether or not the result is marked an error: a report that
// exits nonzero (a problem, not ready) is an error result carrying the report.
func reportText(t *testing.T, s *session, id int, name string) string {
	t.Helper()
	return resultText(s.call(t, id, name, `{}`))
}

func wantAll(t *testing.T, what, out string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(out, sub) {
			t.Errorf("%s: want %q in:\n%s", what, sub, out)
		}
	}
}

func spoolFor(w *world, name string) spool.Spool {
	return spool.Spool{Dir: spool.Dir(filepath.Join(w.env[name]["HOME"], ".local", "state", "mash"), w.e.Owner, tAddr(name).Flat())}
}

func TestRedactText(t *testing.T) {
	for _, in := range []string{
		"nats://alice:s3cretpw@127.0.0.1:1",
		"nats://s3cretpw@127.0.0.1:1",
		"nats://a:s3cretpw@h1:4222,nats://b:s3cretpw@h2:4222",
		"alice:s3cretpw@h1:4222,h2:4222",
		"nats://alice:s3cretpw@127.0.0.1:1/%zz",
		"nats://alice:s3c@retpw@127.0.0.1:1",
		"mash: connect nats://alice:s3cretpw@127.0.0.1:1: no servers available",
		"nats://alice:s3cretpw@[::1",
	} {
		out := redactText(in)
		if strings.Contains(out, "retpw") || strings.Contains(out, "alice:") || !strings.Contains(out, "REDACTED@") {
			t.Errorf("redactText(%q) = %q", in, out)
		}
	}
	if got := redactText("nats://127.0.0.1:4222"); got != "nats://127.0.0.1:4222" {
		t.Errorf("no userinfo changed: %q", got)
	}
}

// ran is one command line run through the real entry point.
type ran struct {
	code      int
	out, errs string
}

func runMash(env map[string]string, args ...string) ran {
	var out, errb bytes.Buffer
	code := run(args, envStrict(env), strings.NewReader(""), &out, quiet(&errb))
	return ran{code, out.String(), errb.String()}
}

// tlsWorld is a world whose server speaks TLS, with each endpoint's environment naming its CA: nothing in its
// reports is a plain-text warning.
func tlsWorld(t *testing.T, names ...string) *world {
	t.Helper()
	w := newWorldWith(t, testenv.Options{TLS: true}, names...)
	for _, n := range names {
		w.env[n]["MASH_CA"] = w.e.CAFile
	}
	return w
}

// strict is the environment of endpoint a with nothing of the test process's own in it.
func strictEnv(w *world, name string, extra map[string]string) map[string]string {
	e := map[string]string{}
	for k, v := range w.env[name] {
		e[k] = v
	}
	for k, v := range extra {
		e[k] = v
	}
	return e
}

func TestStatusReadyOffline(t *testing.T) {
	w := tlsWorld(t, "a")
	r := runMash(w.env["a"], "status")
	if r.code != 0 || r.out != tTo("a")+": ready, offline\n" || r.errs != "" {
		t.Fatalf("status: %+v", r)
	}
	j := runMash(w.env["a"], "status", "--json")
	if j.code != 0 || j.out != `{"endpoint":"`+tTo("a")+`","ready":true,"online":false,"reason":null}`+"\n" || j.errs != "" {
		t.Fatalf("status --json: %+v", j)
	}
}

func TestStatusReadyOnlineWhileASessionHoldsTheEndpoint(t *testing.T) {
	w := tlsWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 10*time.Second, "status says online", func() bool {
		return runMash(w.env["a"], "status").out == tTo("a")+": ready, online\n"
	})
	// the session's own tool says the same
	if got := toolText(t, s, 20, "status"); got != tTo("a")+": ready, online" {
		t.Fatalf("status tool: %q", got)
	}
}

func TestStatusRefusedCredentials(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t) // another account and operator: this server does not know its users
	creds, _ := other.Mint(t, tAddr("a"))
	env := strictEnv(w, "a", map[string]string{"MASH_CREDS": creds})
	r := runMash(env, "status")
	if r.code != 1 || r.out != tTo("a")+": not ready: the server refused its credentials\n" || r.errs != "hint: mash doctor\n" {
		t.Fatalf("status: %+v", r)
	}
	j := runMash(env, "status", "--json")
	if j.code != 1 || j.out != `{"endpoint":"`+tTo("a")+`","ready":false,"online":false,"reason":"the server refused its credentials"}`+"\n" || j.errs != "" {
		t.Fatalf("status --json: %+v", j)
	}
}

func TestStatusServerNotAnswering(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"})
	r := runMash(env, "status")
	if r.code != 1 || r.out != tTo("a")+": not ready: the server did not answer: tls://127.0.0.1:1\n" || r.errs != "hint: mash doctor\n" {
		t.Fatalf("status: %+v", r)
	}
}

func TestStatusConfigAndCredsReasons(t *testing.T) {
	w := tlsWorld(t, "a")
	missing := filepath.Join(t.TempDir(), "none.creds")
	if r := runMash(strictEnv(w, "a", map[string]string{"MASH_CREDS": missing}), "status"); r.code != 1 ||
		r.out != tTo("a")+": not ready: the creds file is missing: "+missing+"\n" || r.errs != "hint: mash doctor\n" {
		t.Fatalf("missing creds: %+v", r)
	}
	// a config.json that does not parse is a reason, not a failure of the command
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runMash(map[string]string{"HOME": home, "MASH_ENDPOINT": tTo("a"), "MASH_SERVER": "nats://alice:" + pwSecret + "@127.0.0.1:1"}, "status")
	if r.code != 1 || !strings.HasPrefix(r.out, "mash: not ready: ") || !strings.Contains(r.out, "config.json") || strings.Contains(r.out, pwSecret) || r.errs != "hint: mash doctor\n" {
		t.Fatalf("bad config.json: %+v", r)
	}
	// no server: the reason says so
	r = runMash(map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": tTo("a")}, "status")
	if r.code != 1 || r.out != tTo("a")+": not ready: no server is set\n" {
		t.Fatalf("no server: %+v", r)
	}
}

// At a shell with no endpoint chosen and none installed, status and doctor give the specification's error: stderr, exit 2.
func TestStatusAndDoctorNoEndpointInstalled(t *testing.T) {
	for _, verb := range []string{"status", "doctor"} {
		r := runMash(map[string]string{"HOME": t.TempDir()}, verb)
		if r.code != 1 || r.out != "" || r.errs != "error: no endpoint is installed here\n" {
			t.Fatalf("%s: %+v", verb, r)
		}
		j := runMash(map[string]string{"HOME": t.TempDir()}, verb, "--json")
		if j.code != 1 || j.errs != "" || j.out != `{"error":"no endpoint is installed here"}`+"\n" {
			t.Fatalf("%s --json: %+v", verb, j)
		}
	}
}

// Several endpoints installed and none chosen: the one error of the specification, on stderr, exit 2.
func TestStatusAndDoctorSeveralInstalled(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		b, err := os.ReadFile(w.env[n]["MASH_CREDS"])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tTo(n)+".creds"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home, "MASH_SERVER": w.env["a"]["MASH_SERVER"], "MASH_CA": w.e.CAFile}
	for _, verb := range []string{"status", "doctor"} {
		r := runMash(env, verb)
		want := "error: more than one endpoint is installed here: " + tTo("a") + ", " + tTo("b") + "\n"
		if r.code != 2 || r.out != "" || r.errs != want+"hint: mash "+verb+" --endpoint "+tTo("a")+"\n" {
			t.Fatalf("%s: %+v", verb, r)
		}
		j := runMash(env, verb, "--json")
		if j.code != 2 || j.errs != "" || !strings.HasPrefix(j.out, `{"error":"more than one endpoint is installed here: `) {
			t.Fatalf("%s --json: %+v", verb, j)
		}
		// naming one is the way out
		if r := runMash(env, verb, "--endpoint", tTo("b")); r.code != 0 {
			t.Fatalf("%s --endpoint: %+v", verb, r)
		}
	}
}

func TestStatusTakesNoArguments(t *testing.T) {
	for _, args := range [][]string{{"status", "extra"}, {"doctor", "extra"}, {"status", "--bogus"}, {"doctor", "--bogus"}} {
		r := runMash(map[string]string{"HOME": t.TempDir()}, args...)
		if r.code != 2 || r.out != "" || !strings.HasSuffix(r.errs, "\nhint: mash "+args[0]+" --help\n") && !strings.HasPrefix(r.errs, "error: unknown flag ") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestDoctorAllWell(t *testing.T) {
	w := tlsWorld(t, "a")
	r := runMash(w.env["a"], "doctor")
	want := "config    ok\n" +
		"claude    skip  no claude command found\n" +
		"creds     ok\n" +
		"server    ok    " + w.e.URL + "\n" +
		"sign-in   ok\n" +
		"delivery  skip  checked only inside a Claude Code session\n" +
		"refused   ok    no message was refused for lacking its sender's signature\n" +
		"\n" +
		"No problems.\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Fatalf("doctor:\n%+v\nwant %q", r, want)
	}
	j := runMash(w.env["a"], "doctor", "--json")
	var o struct {
		Endpoint string
		Problems int
		Warnings int
		Checks   []struct {
			Name, State string
			Detail, Fix *string
		}
	}
	if err := json.Unmarshal([]byte(j.out), &o); err != nil || j.code != 0 || j.errs != "" {
		t.Fatalf("doctor --json: %+v %v", j, err)
	}
	if o.Endpoint != tTo("a") || o.Problems != 0 || o.Warnings != 0 || len(o.Checks) != 7 {
		t.Fatalf("object %+v", o)
	}
	var names []string
	for _, c := range o.Checks {
		names = append(names, c.Name)
		if (c.State != "ok" && c.Name != "delivery" && c.Name != "claude") || (c.Name == "delivery" && (c.State != "skip" || *c.Detail != "checked only inside a Claude Code session")) || c.Fix != nil {
			t.Errorf("check %+v", c)
		}
	}
	if !reflect.DeepEqual(names, []string{"config", "claude", "creds", "server", "sign-in", "delivery", "refused"}) {
		t.Fatalf("rows %v", names)
	}
}

// A refused sign-in and refused mail: the specification's own example, with the fix as the one hint on stderr.
func TestDoctorAProblemAndAWarning(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t)
	creds, _ := other.Mint(t, tAddr("a"))
	env := strictEnv(w, "a", map[string]string{"MASH_CREDS": creds})
	writeRefused(t, env, 3)
	r := runMash(env, "doctor")
	want := "config    ok\n" +
		"claude    skip  no claude command found\n" +
		"creds     ok\n" +
		"server    ok    " + w.e.URL + "\n" +
		"sign-in   FAIL  the server refused these credentials\n" +
		"delivery  skip  needs sign-in\n" +
		"refused   warn  3 unverified messages were refused\n" +
		"\n" +
		"1 problem, 1 warning.\n"
	if r.code != 1 || r.out != want || r.errs != "" { // MASH_CREDS is a custom path: no join hint
		t.Fatalf("doctor:\n%+v\nwant %q", r, want)
	}
	j := runMash(env, "doctor", "--json")
	if j.code != 1 || j.errs != "" || !strings.Contains(j.out, `"problems":1,"warnings":1`) ||
		!strings.Contains(j.out, `{"name":"sign-in","state":"fail","detail":"the server refused these credentials","fix":null}`) {
		t.Fatalf("doctor --json: %+v", j)
	}
}

// A warning alone does not fail the command.
func TestDoctorAWarningAloneExitsZero(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", nil)
	writeRefused(t, env, 1)
	r := runMash(env, "doctor")
	if r.code != 0 || r.errs != "" || !strings.Contains(r.out, "refused   warn  1 unverified message was refused\n") || !strings.HasSuffix(r.out, "\n\n1 warning.\n") {
		t.Fatalf("doctor: %+v", r)
	}
	// and status is not about it
	if s := runMash(env, "status"); s.code != 0 || s.out != tTo("a")+": ready, offline\n" {
		t.Fatalf("status: %+v", s)
	}
}

func TestDoctorServerNotAnsweringAndCredsProblems(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"})
	r := runMash(env, "doctor")
	want := "config    ok\nclaude    skip  no claude command found\ncreds     ok\nserver    FAIL  the server did not answer: tls://127.0.0.1:1\nsign-in   skip  needs the server\ndelivery  skip  needs the server\nrefused   ok    no message was refused for lacking its sender's signature\n\n1 problem.\n"
	if r.code != 1 || r.out != want || r.errs != "" { // no command mends it: no hint
		t.Fatalf("doctor:\n%+v", r)
	}
	// creds readable by others: the fix is the chmod, and sign-in is not attempted
	if err := os.Chmod(w.env["a"]["MASH_CREDS"], 0o644); err != nil {
		t.Fatal(err)
	}
	p := w.env["a"]["MASH_CREDS"]
	r = runMash(w.env["a"], "doctor")
	if r.code != 1 || !strings.Contains(r.out, "creds     FAIL  "+config.Tilde(p)+" can be read by others\n") ||
		!strings.Contains(r.out, "sign-in   skip  needs the creds\ndelivery  skip  needs sign-in\n") || !strings.Contains(r.out, "refused   ok    ") || r.errs != "" { // chmod is not a command of ours: the row says what is wrong
		t.Fatalf("doctor, creds mode: %+v", r)
	}
	// status keeps its longer sentence with the full path; the doctor row above shows ~
	if r := runMash(w.env["a"], "status"); r.code != 1 || r.out != tTo("a")+": not ready: the creds file can be read by other users: "+p+"\n" {
		t.Fatalf("status, creds mode: %+v", r)
	}
	// not a creds file: its content is never printed
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(credsSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runMash(w.env["a"], "doctor")
	if r.code != 1 || !strings.Contains(r.out, "creds     FAIL  "+config.Tilde(p)+" is not a creds file\n") || strings.Contains(r.out+r.errs, credsSecret) {
		t.Fatalf("doctor, bad creds: %+v", r)
	}
	if r := runMash(w.env["a"], "status"); r.code != 1 || r.out != tTo("a")+": not ready: the creds file is not a creds file: "+p+"\n" || strings.Contains(r.out+r.errs, credsSecret) {
		t.Fatalf("status, bad creds: %+v", r)
	}
	// missing
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	r = runMash(w.env["a"], "doctor")
	if r.code != 1 || !strings.Contains(r.out, "creds     FAIL  "+config.Tilde(p)+" is missing\n") || r.errs != "" { // a custom creds path: join cannot mend it, no hint
		t.Fatalf("doctor, missing creds: %+v", r)
	}
}

// A plain URL: refused with no override (the refusal is the server row, not a failure of the command), a warning with it.
func TestDoctorPlainURL(t *testing.T) {
	w := newWorld(t, "a") // a plain server, with the override set
	r := runMash(w.env["a"], "doctor")
	if r.code != 0 || !strings.Contains(r.out, "server    warn  "+w.e.URL+" is plain text: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set\n") || !strings.Contains(r.out, "sign-in   ok\n") ||
		!strings.HasSuffix(r.out, "\n\n1 warning.\n") {
		t.Fatalf("doctor with the override: %+v", r)
	}
	env := strictEnv(w, "a", nil)
	delete(env, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
	r = runMash(env, "doctor")
	if r.code != 1 || !strings.Contains(r.out, "server    FAIL  the server address is not tls://: "+w.e.URL+"\n") ||
		!strings.Contains(r.out, "sign-in   skip  needs the server\ndelivery  skip  needs the server\nrefused   ok    no message was refused for lacking its sender's signature\n") ||
		strings.Contains(r.out, "MASH_CA") || r.errs != "" { // no command of ours fixes the address: no hint
		t.Fatalf("doctor without it: %+v", r)
	}
	if s := runMash(env, "status"); s.code != 1 || !strings.HasPrefix(s.out, tTo("a")+": not ready: ") || s.errs != "hint: mash doctor\n" {
		t.Fatalf("status without it: %+v", s)
	}
}

func TestDoctorShowsNoSecrets(t *testing.T) {
	home := t.TempDir()
	creds := filepath.Join(t.TempDir(), "a.creds")
	if err := os.WriteFile(creds, []byte("-----BEGIN NATS USER JWT-----\n"+credsSecret+"\n------END NATS USER JWT------\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"status", "doctor"} {
		r := runMash(map[string]string{"HOME": home, "MASH_ENDPOINT": tTo("a"), "MASH_CREDS": creds, "MASH_SERVER": "tls://user:hunter2pw@127.0.0.1:1",
			"MASH_OPEN_TIMEOUT": "2s"}, verb)
		for _, bad := range []string{credsSecret, "BEGIN NATS", "hunter2pw"} {
			if strings.Contains(r.out+r.errs, bad) {
				t.Fatalf("%s printed %q:\n%+v", verb, bad, r)
			}
		}
	}
}

// writeRefused records n refused unverified messages where the reading side looks for them.
func writeRefused(t *testing.T, env map[string]string, n int) {
	t.Helper()
	cfg, err := loadCfg(env)
	if err != nil {
		t.Fatal(err)
	}
	d, err := endpoint.SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, endpoint.RefusedFile), []byte(fmt.Sprint(n)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The doors agree: for each state of the endpoint, the CLI's --json is the MCP tool's structured result, and the MCP
// text is the CLI's stdout followed by its stderr hint (an MCP tool has no stderr).
func TestStatusAndDoctorDoorsAgree(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t)
	badCreds, _ := other.Mint(t, tAddr("a"))
	cases := map[string]map[string]string{
		"ready":     strictEnv(w, "a", nil),
		"refused":   strictEnv(w, "a", map[string]string{"MASH_CREDS": badCreds}),
		"no server": strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"}),
	}
	writeRefused(t, cases["refused"], 2)
	id := 100
	for name, env := range cases {
		s := startServe(t, env)
		s.initialize(t)
		for _, tool := range []string{"status", "doctor"} {
			id++
			if name == "ready" { // a hook has run in the session, so its delivery row is ok (the shell skips it, see below)
				hookRan(t, w, s, "a")
			}
			cli := runMash(env, tool)
			cj := runMash(env, tool, "--json")
			r := s.call(t, id, tool, `{}`)
			// a report that exits nonzero is marked an error, with its text and structured result kept
			if (r["isError"] == true) != (cli.code != 0) {
				t.Fatalf("%s/%s: isError %v, exit %d", name, tool, r["isError"], cli.code)
			}
			wantText := strings.TrimSuffix(cli.out, "\n")
			if strings.HasPrefix(cli.errs, "hint: ") {
				wantText += "\n" + strings.TrimSuffix(cli.errs, "\n")
			}
			// the one difference between the doors: a session checks delivery, a shell skips it ("checked only inside a Claude Code session")
			wantText = strings.Replace(wantText, "delivery  skip  checked only inside a Claude Code session", "delivery  ok", 1)
			if got := resultText(r); got != wantText {
				t.Errorf("%s/%s: MCP text %q, CLI %q", name, tool, got, wantText)
			}
			var cliObj map[string]any
			if err := json.Unmarshal([]byte(cj.out), &cliObj); err != nil {
				t.Fatalf("%s/%s: %q", name, tool, cj.out)
			}
			// the session is on the same configuration; whether it is online is its own business
			sc, _ := r["structuredContent"].(map[string]any)
			if tool == "status" {
				delete(cliObj, "online")
				delete(sc, "online")
			}
			if rows, _ := cliObj["checks"].([]any); tool == "doctor" {
				for _, row := range rows {
					if m := row.(map[string]any); m["name"] == "delivery" && m["detail"] == "checked only inside a Claude Code session" {
						m["state"], m["detail"] = "ok", nil
					}
				}
			}
			if !reflect.DeepEqual(cliObj, sc) {
				t.Errorf("%s/%s: structured %v, CLI --json %v", name, tool, sc, cliObj)
			}
			if cli.code != cj.code {
				t.Errorf("%s/%s: exit %d and %d", name, tool, cli.code, cj.code)
			}
		}
	}
}

// The degraded serve (no endpoint chosen, even with creds installed) answers version, status and doctor, and the answers
// say what to do.
func TestDegradedServeAnswersStatusAndDoctor(t *testing.T) {
	s := startServe(t, map[string]string{"HOME": t.TempDir()})
	s.initialize(t)
	if names := toolNames(t, s); len(names) != 3 || !names["version"] || !names["status"] || !names["doctor"] {
		t.Fatalf("tools %v", names)
	}
	// exit 1 at a shell, so an error result here: the line, then the hint on the next line, the structured result kept
	const want = "No endpoint is chosen for this session.\nhint: start the session with MASH_ENDPOINT=<address>"
	for id, tool := range map[int]string{10: "status", 11: "doctor"} {
		w := want
		if tool == "doctor" { // no command of ours names the endpoint of a session: doctor has no hint
			w = "No endpoint is chosen for this session."
		}
		if r := s.call(t, id, tool, `{}`); r["isError"] != true || resultText(r) != w {
			t.Fatalf("%s: %v", tool, r)
		}
	}
	r := s.call(t, 12, "status", `{}`)
	if sc, _ := r["structuredContent"].(map[string]any); !reflect.DeepEqual(sc, map[string]any{"endpoint": nil, "ready": false, "online": false, "reason": "no endpoint is chosen for this session"}) {
		t.Fatalf("status structured %v", r)
	}
	r = s.call(t, 13, "doctor", `{}`)
	sc, _ := r["structuredContent"].(map[string]any)
	rows, _ := sc["checks"].([]any)
	if sc == nil || sc["endpoint"] != nil || sc["problems"] != float64(1) || sc["warnings"] != float64(0) || len(rows) != 7 {
		t.Fatalf("structured %v", r)
	}
	if rows[2].(map[string]any)["state"] != "skip" || rows[2].(map[string]any)["detail"] != "needs the config" {
		t.Fatalf("rows %v", rows)
	}
	if r := s.call(t, 14, "status", `{"x":1}`); r["isError"] != true || !strings.Contains(resultText(r), "unknown argument x") {
		t.Errorf("an argument: %v", r)
	}
}

// The old rows are gone: no version, no mail count, no channel rows.
func TestStatusHasNoOldRows(t *testing.T) {
	w := tlsWorld(t, "a")
	r := runMash(w.env["a"], "status")
	if strings.Count(r.out, "\n") != 1 {
		t.Fatalf("status is more than one line: %q", r.out)
	}
	for _, old := range []string{"version", "name:", "config:", "creds:", "connection", "channel", "delivery", "unread", "relay trace", "clock"} {
		if strings.Contains(r.out, old) {
			t.Errorf("status still has %q: %q", old, r.out)
		}
	}
	d := runMash(w.env["a"], "doctor")
	for _, none := range []string{"plugin", "channel", "allowlist", "hook", "version"} {
		if strings.Contains(d.out, none) {
			t.Errorf("doctor has a %q row: %q", none, d.out)
		}
	}
}

// A server whose certificate the client does not trust (a self-signed one, or a CA it was not given) has its own text,
// not "did not answer": the reason in status, the detail and the fix in doctor, and the same in the JSON.
func TestStatusAndDoctorUntrustedCertificate(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", nil)
	delete(env, "MASH_CA")
	text := "the server's certificate is not trusted: " + w.e.URL
	r := runMash(env, "status")
	if r.code != 1 || r.out != tTo("a")+": not ready: "+text+"\n" || r.errs != "hint: mash doctor\n" {
		t.Fatalf("status: %+v", r)
	}
	j := runMash(env, "status", "--json")
	if j.code != 1 || j.out != `{"endpoint":"`+tTo("a")+`","ready":false,"online":false,"reason":"`+text+`"}`+"\n" {
		t.Fatalf("status --json: %+v", j)
	}
	d := runMash(env, "doctor")
	want := "config    ok\nclaude    skip  no claude command found\ncreds     ok\nserver    FAIL  " + text + "\nsign-in   skip  needs the server\ndelivery  skip  needs the server\nrefused   ok    no message was refused for lacking its sender's signature\n\n1 problem.\n"
	if d.code != 1 || d.out != want || d.errs != "" || strings.Contains(d.out, "did not answer") {
		t.Fatalf("doctor:\n%+v\nwant %q", d, want)
	}
	dj := runMash(env, "doctor", "--json")
	if !strings.Contains(dj.out, `{"name":"server","state":"fail","detail":"`+text+`","fix":null}`) || !strings.Contains(dj.out, `"hint":null`) {
		t.Fatalf("doctor --json: %+v", dj)
	}
}

// A plain (non-TLS) server behind a tls:// address has its own text too.
func TestStatusAndDoctorNotATLSServer(t *testing.T) {
	w := newWorld(t, "a") // a plain server
	url := "tls://" + strings.TrimPrefix(w.e.URL, "nats://")
	env := strictEnv(w, "a", map[string]string{"MASH_SERVER": url, "MASH_OPEN_TIMEOUT": "5s"})
	text := "the server did not offer TLS: " + url
	r := runMash(env, "status")
	if r.code != 1 || r.out != tTo("a")+": not ready: "+text+"\n" || r.errs != "hint: mash doctor\n" {
		t.Fatalf("status: %+v", r)
	}
	j := runMash(env, "status", "--json")
	if j.code != 1 || j.out != `{"endpoint":"`+tTo("a")+`","ready":false,"online":false,"reason":"`+text+`"}`+"\n" {
		t.Fatalf("status --json: %+v", j)
	}
	d := runMash(env, "doctor")
	want := "config    ok\nclaude    skip  no claude command found\ncreds     ok\nserver    FAIL  " + text + "\nsign-in   skip  needs the server\ndelivery  skip  needs the server\nrefused   ok    no message was refused for lacking its sender's signature\n\n1 problem.\n"
	if d.code != 1 || d.out != want || d.errs != "" || strings.Contains(d.out, "did not answer:") {
		t.Fatalf("doctor:\n%+v\nwant %q", d, want)
	}
	dj := runMash(env, "doctor", "--json")
	if !strings.Contains(dj.out, `{"name":"server","state":"fail","detail":"`+text+`","fix":null}`) {
		t.Fatalf("doctor --json: %+v", dj)
	}
}

// A cancelled call stops the checks at once, whatever the open timeout: a server that accepts and never speaks would
// otherwise hold a cancelled MCP call for open_timeout.
func TestHealthStopsWhenItsContextIsCancelled(t *testing.T) {
	w := tlsWorld(t, "a")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered, until the test ends
		}
	}()
	cfg, err := loadCfg(strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://" + ln.Addr().String(), "MASH_OPEN_TIMEOUT": "30s", "MASH_CLI_TIMEOUT": "60s"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err = (&healthSvc{cfg: cfg}).Health(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v after the cancel", d)
	}
}

// At a shell every hint goes to stderr and none to stdout, with or without --json.
func TestStatusAndDoctorHintsAreOnStderr(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t)
	creds, _ := other.Mint(t, tAddr("a"))
	// refused, with the creds at the path join writes: both commands have a fix to hint
	dir := t.TempDir()
	env := strictEnv(w, "a", map[string]string{"MASH_CONFIG_DIR": dir})
	delete(env, "MASH_CREDS")
	b, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, env["MASH_ENDPOINT"]+".creds"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"status", "doctor"} {
		r := runMash(env, cmd)
		if r.code != 1 || !strings.HasPrefix(r.errs, "hint: ") || strings.HasSuffix(strings.TrimSuffix(r.errs, "\n"), "\n") {
			t.Errorf("%s: %+v", cmd, r)
		}
		for _, l := range strings.Split(r.out, "\n") {
			if strings.HasPrefix(l, "hint:") {
				t.Errorf("%s: a hint line on stdout: %q", cmd, r.out)
			}
		}
		j := runMash(env, cmd, "--json")
		if j.code != 1 || strings.Contains(j.out, "\nhint:") || strings.HasPrefix(j.out, "hint:") || strings.Contains(j.errs, "hint") && !strings.HasPrefix(j.errs, "hint: ") {
			t.Errorf("%s --json: %+v", cmd, j)
		}
	}
}

// TestJSONPolicyByDestination: --json is one compact line when stdout is a pipe or a file and indented at a terminal
// (the rule "--json prints one object", #1043). A usage error needs no server; its object follows the same policy.
func TestJSONPolicyByDestination(t *testing.T) {
	old := cliframe.IsTerminal
	t.Cleanup(func() { cliframe.IsTerminal = old })
	for _, tc := range []struct {
		name string
		tty  bool
		want string
	}{
		{"non-terminal", false, `{"error":"unknown command \"nope\""}` + "\n"},
		{"terminal", true, "{\n  \"error\": \"unknown command \\\"nope\\\"\"\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliframe.IsTerminal = func(io.Writer) bool { return tc.tty }
			r := runMash(map[string]string{}, "nope", "--json")
			if r.code != 2 || r.out != tc.want || r.errs != "" {
				t.Errorf("code %d, stdout %q (want %q), stderr %q", r.code, r.out, tc.want, r.errs)
			}
		})
	}
}
