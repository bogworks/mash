package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/testenv"
)

// detailOf is what `mash status --detail --json` says for env: the exit code, reason_code ("" when null) and the raw
// session object ("null" at no session).
func detailOf(t *testing.T, env map[string]string) (code int, reasonCode, session string, ready bool) {
	t.Helper()
	r := runMash(env, "status", "--detail", "--json")
	var m struct {
		Ready      bool            `json:"ready"`
		ReasonCode *string         `json:"reason_code"`
		Session    json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("status --detail --json: %+v: %v", r, err)
	}
	if m.ReasonCode != nil {
		reasonCode = *m.ReasonCode
	}
	return r.code, reasonCode, string(m.Session), m.Ready
}

// sessionDetail is what the status tool says, from a healthSvc that stands for a session, as the reason_code.
func sessionDetail(t *testing.T, h *healthSvc) string {
	t.Helper()
	h.session = true
	r, err := cmds.Status.Run(context.Background(), &cmdcore.Env{Svc: h}, cmds.StatusParams{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r.DetailValue())
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ReasonCode *string `json:"reason_code"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.ReasonCode == nil {
		t.Fatalf("no reason_code in %s (%v)", b, err)
	}
	return *m.ReasonCode
}

// Every reason_code the specification lists is produced by the failure it names, set where the failure is classified.
func TestStatusReasonCodeEveryValue(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	other := tlsWorld(t, "a")
	otherCreds, _ := other.e.Mint(t, tAddr("a")) // another operator: this server does not know its users
	bCreds, _ := w.e.Mint(t, tAddr("b"))
	ghostCreds, _ := w.e.Mint(t, tAddr("ghost")) // a user the server accepts, whose directory record does not exist
	badHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badHome, ".config", "mash"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badHome, ".config", "mash", "config.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := newWorld(t, "a") // a plain server behind a tls:// address
	plainURL := "tls://" + strings.TrimPrefix(plain.e.URL, "nats://")
	untrusted := strictEnv(w, "a", nil)
	delete(untrusted, "MASH_CA")

	// an identity the name rule refuses fails sign-in with an error that fits no other code
	_, retired := provisionNewForm(t, w.e, "acme", "retired", "me")
	retired["MASH_NAME_RULE_FILE"] = configARuleFile(t, t.TempDir(), "name-rule.json", configAMashRule)
	refusedByRule := strictEnv(w, "a", retired)

	shell := map[string]map[string]string{
		"unknown":             refusedByRule,
		"config_unreadable":   {"HOME": badHome, "MASH_ENDPOINT": tTo("a"), "MASH_SERVER": "tls://127.0.0.1:1"},
		"creds_missing":       strictEnv(w, "a", map[string]string{"MASH_CREDS": filepath.Join(t.TempDir(), "none.creds")}),
		"creds_refused":       strictEnv(w, "a", map[string]string{"MASH_CREDS": otherCreds}),
		"endpoint_deleted":    strictEnv(w, "a", map[string]string{"MASH_ENDPOINT": tTo("ghost"), "MASH_CREDS": ghostCreds}),
		"server_unreachable":  strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"}),
		"server_untrusted":    untrusted,
		"server_no_tls":       strictEnv(w, "a", map[string]string{"MASH_SERVER": plainURL, "MASH_OPEN_TIMEOUT": "5s"}),
		"creds_refused/other": strictEnv(w, "a", map[string]string{"MASH_CREDS": bCreds}), // another endpoint's creds
		"server_no_tls/plain": strictEnv(w, "a", map[string]string{"MASH_SERVER": strings.Replace(w.e.URL, "tls://", "nats://", 1)}),
	}
	got := map[string]bool{}
	for name, env := range shell {
		want, _, _ := strings.Cut(name, "/")
		code, rc, _, ready := detailOf(t, env)
		if code != 1 || ready || rc != want {
			t.Errorf("%s: exit %d ready %v reason_code %q, want %q", name, code, ready, rc, want)
		}
		got[rc] = true
	}

	// the session-only causes come from the session's own state
	lease := func(state string) func() (string, string) { return func() (string, string) { return state, "" } }
	sessionCases := map[string]*healthSvc{
		"no_endpoint":             {cfg: config.Config{}},
		"endpoints_ambiguous":     {cfg: config.Config{}, cfgErr: &config.SeveralEndpointsError{Addresses: []string{"o.g.a", "o.g.b"}}},
		"endpoint_held_elsewhere": {cfg: w.cfgOf(t, "a"), lease: lease(cmds.LeaseOther)},
		"endpoint_lost":           {cfg: w.cfgOf(t, "a"), lease: lease(cmds.LeaseLost)},
		"endpoint_not_taken":      {cfg: w.cfgOf(t, "a"), lease: lease(cmds.LeaseFailed)},
		"storage_unwritable":      {cfg: w.cfgOf(t, "a"), mailHeld: func() bool { return true }},
	}
	for want, h := range sessionCases {
		if rc := sessionDetail(t, h); rc != want {
			t.Errorf("session %s: reason_code %q", want, rc)
		}
		got[want] = true
	}

	all := []string{"no_endpoint", "endpoints_ambiguous", "config_unreadable", "creds_missing", "creds_refused", "endpoint_deleted",
		"server_unreachable", "server_untrusted", "server_no_tls", "endpoint_held_elsewhere", "endpoint_lost", "endpoint_not_taken",
		"storage_unwritable", "unknown"} // the specification's list, in its order
	var missing []string
	for _, c := range all {
		if !got[c] {
			missing = append(missing, c)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("no case produced %v", missing)
	}
}

// cfgOf is the configuration of endpoint name as a shell of the world would load it.
func (w *world) cfgOf(t *testing.T, name string) config.Config {
	t.Helper()
	env := envStrict(w.env[name])
	cfg, err := config.Load(config.LookupFromFunc(env), env("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// shellOf is `mash status --detail --json` at the shell of endpoint a with extra settings: the exit code, whether it is
// ready, the reason_code and the raw session.
func shellOf(t *testing.T, w *world, extra map[string]string) (reasonCode, session string, ready bool) {
	t.Helper()
	_, rc, s, ok := detailOf(t, strictEnv(w, "a", extra))
	return rc, s, ok
}

func hooksRecord(t *testing.T, sp spool.Spool, host, pid, hooks string) {
	t.Helper()
	b, _ := json.Marshal(endpoint.HookRecord{Hooks: hooks, Since: time.Now().Add(-time.Hour), Host: host, PID: pid, At: time.Now()})
	if err := sp.NoteHookState(b); err != nil {
		t.Fatal(err)
	}
}

// The shell reads the session from what serve recorded, whatever the sign-in said: with the server unreachable it shows
// the same session the tool does, and the root cause stays the reason.
func TestStatusSessionAtShellWithTheServerDown(t *testing.T) {
	w, s := hooklessWorld(t, time.Hour)
	const notRun = `{"holds_endpoint":true,"hooks":"not_run","storage":"ok"}`
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return got == notRun })
	if got := toolSession(t, s, 50); got != notRun {
		t.Fatalf("tool: %s", got)
	}
	rc, session, ready := shellOf(t, w, map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"})
	if ready || rc != "server_unreachable" || session != notRun {
		t.Errorf("server down: ready %v reason_code %q session %s", ready, rc, session)
	}
}

// A refused hook and a suspected session read the same at the shell and in the tool.
func TestStatusSessionRefusedAndSuspectedAtShellAndTool(t *testing.T) {
	// Two subtests, one after the other: each starts its own serve and restores the detector's knobs only after that
	// serve has stopped (a second hooklessWorld in the same test would write the knobs under the first serve's goroutines).
	t.Run("refused", func(t *testing.T) {
		w, s := hooklessWorld(t, time.Hour)
		sp := spoolFor(w, "a")
		eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return strings.Contains(got, "not_run") })
		if err := sp.NoteRefused(spool.Refusal{Time: time.Now().Add(time.Second), Why: "another session's"}); err != nil {
			t.Fatal(err)
		}
		const refused = `{"holds_endpoint":true,"hooks":"refused","storage":"ok"}`
		// Wait on the state itself, not on the refusal being seen by the first look: under load the shell and the serve
		// take their view of the spool a moment after the file is written.
		var got string
		eventually(t, 30*time.Second, "shell sees refused", func() bool { got, _ = shellSession(t, w); return got == refused })
		if got != refused {
			t.Errorf("shell: %s", got)
		}
		id := 160 // each poll is a new tool call, so a new request id
		eventually(t, 30*time.Second, "tool says refused", func() bool { id++; got = toolSession(t, s, id); return got == refused })
		if got != refused {
			t.Errorf("tool: %s", got)
		}
	})
	// suspected: a wake was sent (HooklessWakes is 1) and no hook ran; a new holder clears the refusal, so this is a fresh world
	t.Run("suspected", func(t *testing.T) {
		w2, s2 := hooklessWorld(t, time.Hour)
		sendAndWaitSpooled(t, w2, "wake me")
		const suspected = `{"holds_endpoint":true,"hooks":"suspected","storage":"ok"}`
		eventually(t, 15*time.Second, "shell sees suspected", func() bool { got, _ := shellSession(t, w2); return got == suspected })
		if got := toolSession(t, s2, 61); got != suspected {
			t.Errorf("tool: %s", got)
		}
	})
}

// One real tool call followed by the grace is not two calls: the recorder does not say degraded for a second call that
// never happened.
func TestStatusSessionNotDegradedByOneCall(t *testing.T) {
	w, s := hooklessWorld(t, 400*time.Millisecond)
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return strings.Contains(got, "not_run") })
	toolSession(t, s, 70) // the one call
	time.Sleep(1500 * time.Millisecond)
	if got, _ := shellSession(t, w); strings.Contains(got, "degraded") {
		t.Errorf("shell after one call and the grace: %s", got)
	}
	toolSession(t, s, 71) // a second, well after the first
	eventually(t, 10*time.Second, "degraded after two calls", func() bool { got, _ := shellSession(t, w); return strings.Contains(got, "degraded") })
}

// A record whose serve is gone is no session: the serve's process does not exist, or it is another machine's, or the serve
// stopped (it takes its own record with it).
func TestStatusSessionStaleRecordIsNull(t *testing.T) {
	w := newWorld(t, "a", "b")
	sp := spoolFor(w, "a")
	if err := os.MkdirAll(sp.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	hooksRecord(t, sp, host, strconv.Itoa(os.Getpid()), "")
	if got, _ := shellSession(t, w); !strings.Contains(got, `"holds_endpoint":true`) {
		t.Fatalf("a record of a live serve: %s", got)
	}
	hooksRecord(t, sp, host, "2147483646", "") // no such process
	if got, _ := shellSession(t, w); got != "null" {
		t.Errorf("pid dead: %s", got)
	}
	hooksRecord(t, sp, host+"-elsewhere", strconv.Itoa(os.Getpid()), "")
	if got, _ := shellSession(t, w); got != "null" {
		t.Errorf("another host: %s", got)
	}
}

func TestStatusSessionGoneWhenServeStops(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return got != "null" })
	s.in.Close()
	eventually(t, 10*time.Second, "the record is gone", func() bool { got, _ := shellSession(t, w); return got == "null" })
}

// A serve that no longer holds the endpoint takes its record away: the shell then names no session.
func TestStatusSessionGoneWhenTheLeaseIsLost(t *testing.T) {
	old := endpoint.LeaseRefresh
	endpoint.LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { endpoint.LeaseRefresh = old })
	w := tlsWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return got != "null" })
	kv := leaseKV(t, w)
	// another session takes the name: a Put overwrites the lease
	if _, err := kv.Put(context.Background(), tAddr("a").Flat(), []byte(`{"instance_id":"someone-else","host":"h","pid":"1"}`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, "the record is gone", func() bool { got, _ := shellSession(t, w); return got == "null" })
}

// The storage is decided by the shell trying the folder: a read-only spool directory is failing, and the mail is held.
func TestStatusSessionStorageFailingAtShell(t *testing.T) {
	w, _ := hooklessWorld(t, time.Hour)
	sp := spoolFor(w, "a")
	eventually(t, 10*time.Second, "shell sees the session", func() bool { got, _ := shellSession(t, w); return strings.Contains(got, `"storage":"ok"`) })
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	if sp.Writable() {
		t.Skip("a read-only folder is still writable here")
	}
	got, out := shellSession(t, w)
	if !strings.Contains(got, `"storage":"failing"`) || !strings.Contains(out, `"reason_code":"storage_unwritable"`) {
		t.Errorf("read-only folder: %s", out)
	}
	if err := os.Chmod(sp.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, _ := shellSession(t, w); !strings.Contains(got, `"storage":"ok"`) {
		t.Errorf("writable again: %s", got)
	}
}

// A server that refuses our client certificate refuses who we are: creds_refused, not server_untrusted. A server we
// do not trust is server_untrusted.
func TestStatusReasonCodeClientCertRefused(t *testing.T) {
	w := newWorldWith(t, testenv.Options{TLS: true, RequireClientCert: true}, "a")
	env := strictEnv(w, "a", map[string]string{"MASH_CA": w.e.CAFile})
	code, rc, _, ready := detailOf(t, env)
	if code != 1 || ready || rc != "creds_refused" {
		t.Errorf("no client certificate: exit %d ready %v reason_code %q, want creds_refused", code, ready, rc)
	}
	delete(env, "MASH_CA")
	if code, rc, _, _ = detailOf(t, env); code != 1 || rc != "server_untrusted" {
		t.Errorf("untrusted server: exit %d reason_code %q, want server_untrusted", code, rc)
	}
}
