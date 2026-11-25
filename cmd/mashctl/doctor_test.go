package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/cellwidth"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// doctorJSON runs `mashctl doctor --json` through ctlRun and returns the report by check name.
func doctorJSON(t *testing.T, run func(args ...string) (string, string, int), args ...string) (doctorReport, map[string]doctorCheck, int) {
	t.Helper()
	out, errb, code := run(append([]string{"doctor", "--json"}, args...)...)
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor --json: %v\n%s\n%s", err, out, errb)
	}
	by := map[string]doctorCheck{}
	for _, c := range rep.Checks {
		by[c.Name] = c
	}
	return rep, by, code
}

// storageRun runs verbs against the embedded server of f, which has no system account credentials.
func (f *auditFixture) run(t *testing.T) func(args ...string) (string, string, int) {
	return func(args ...string) (string, string, int) { return ctlRun(t, f.e, args...) }
}

// cleanText is the output of a doctor run with the server's address replaced, for comparing with the spec.
func cleanText(e *applyEnv, s string) string {
	return strings.ReplaceAll(s, e.srv.ClientURL(), "tls://localhost:4222")
}

// All well: every check on its line, then "No problems."; and the same in JSON.
func TestDoctorAllWell(t *testing.T) {
	e := newXYEnv(t)
	out, errb, code := e.run("doctor")
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       ok    every connected user was issued by mashctl\n\nNo problems.\n"
	if code != 0 || cleanText(e, out) != want {
		t.Fatalf("exit %d stderr %q\n%q\nwant\n%q", code, errb, cleanText(e, out), want)
	}

	rep, by, code := doctorJSON(t, e.run)
	if code != 0 || rep.Problems != 0 || rep.Warnings != 0 || len(rep.Checks) != 6 {
		t.Fatalf("%d %+v", code, rep)
	}
	raw, _, _ := e.run("doctor", "--json")
	for _, key := range []string{`"problems": 0`, `"warnings": 0`, `"name": "server"`, `"state": "ok"`, `"detail": ""`, `"fix": null`} {
		if !strings.Contains(raw, key) {
			t.Errorf("JSON lacks %s:\n%s", key, raw)
		}
	}
	if by["server"].Detail != e.srv.ClientURL() || by["endpoints"].Detail != "2 records, 2 inboxes" {
		t.Errorf("checks %+v", by)
	}
	noSecrets(t, out, raw)
	// --strict changes nothing when there is nothing to warn about
	if _, _, code := e.run("doctor", "--strict"); code != 0 {
		t.Errorf("--strict on a clean run: exit %d", code)
	}
}

// A problem still prints every row (ok ones too), then the count and the one hint; exit 1.
func TestDoctorAProblemShowsTheFailureAndTheFix(t *testing.T) {
	e := newXYEnv(t)
	nc := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ghost := xy("ghost")
	if _, err := js.CreateStream(context.Background(), store.InboxConfigFor(ghost, store.DefaultLimits())); err != nil {
		t.Fatal(err)
	}
	out, errb, code := e.run("doctor")
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  FAIL  1 inbox has no endpoint record: alice.g.ghost\nkeys       ok    every connected user was issued by mashctl\n\n1 problem.\n"
	if code != 1 || cleanText(e, out) != want || errb != "hint: mashctl endpoint show alice.g.ghost\n" {
		t.Fatalf("exit %d\n%q\nstderr %q\nwant\n%q", code, cleanText(e, out), errb, want)
	}
	if strings.Contains(out, "hint:") {
		t.Errorf("a hint on stdout: %q", out)
	}
	rep, by, code := doctorJSON(t, e.run)
	c := by["endpoints"]
	if code != 1 || rep.Problems != 1 || c.State != "fail" || c.Fix == nil || *c.Fix != "mashctl endpoint show alice.g.ghost" {
		t.Fatalf("%d %+v", code, rep)
	}
	if len(rep.Checks) != 6 || by["keys"].State != "ok" {
		t.Errorf("a failed endpoints check must not stop the keys check: %+v", rep.Checks)
	}
}

// Without system_creds the keys check warns: exit 0, and 1 with --strict.
func TestDoctorWarningAndStrict(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("a"))
	out, errb, code := ctlRun(t, f.e, "doctor")
	want := "config     ok\nserver     ok    " + f.e.URL + "\nsign-in    ok\nstorage    ok\nendpoints  ok    1 record, 1 inbox\nkeys       warn  no system_creds in the configuration; connected keys were not checked\n\nNo problems, 1 warning.\n"
	const wantHint = "hint: mashctl init --config <file>\n"
	if code != 0 || out != want || errb != wantHint {
		t.Fatalf("exit %d stderr %q\n%q\nwant\n%q", code, errb, out, want)
	}
	if out, errb, code := ctlRun(t, f.e, "doctor", "--strict"); code != 1 || out != want || errb != wantHint {
		t.Fatalf("--strict: exit %d\n%q\nstderr %q", code, out, errb)
	}
	rep, by, code := doctorJSON(t, f.run(t), "--strict")
	if code != 1 || rep.Problems != 0 || rep.Warnings != 1 || by["keys"].State != "warn" || by["endpoints"].Detail != "1 record, 1 inbox" {
		t.Fatalf("%d %+v", code, rep)
	}
}

// --audit-users without system_creds cannot audit anyone: the keys row fails and the exit is 1; plain doctor still warns.
func TestDoctorAuditUsersNoSysCreds(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("a"))
	out, errb, code := ctlRun(t, f.e, "doctor", "--audit-users")
	line := "keys       FAIL  no system_creds: connected users cannot be audited\n"
	if code != 1 || !strings.Contains(out, line) || !strings.Contains(errb, "hint: mashctl init --config <file>\n") {
		t.Fatalf("exit %d stderr %q\n%q", code, errb, out)
	}
	if out, _, code := ctlRun(t, f.e, "doctor"); code != 0 || !strings.Contains(out, "keys       warn  no system_creds in the configuration; connected keys were not checked\n") {
		t.Fatalf("plain doctor: exit %d\n%q", code, out)
	}
}

// Wrong command lines are usage errors that attempt nothing; the removed verbs are unknown commands.
func TestDoctorUsageAndRemovedVerbs(t *testing.T) {
	f := newAuditEnv(t)
	for args, want := range map[string]string{
		"doctor --x":        "error: unknown option --x\nhint: mashctl doctor --help\n",
		"doctor --dry-run":  "error: unknown option --dry-run\nhint: mashctl doctor --help\n",
		"doctor extra":      "error: too many arguments\nhint: mashctl doctor --help\n",
		"audit":             "error: unknown command \"audit\"\n",
		"issued check":      "error: unknown command \"issued\"\n",
		"issued list":       "error: unknown command \"issued\"\n",
		"audit --creds-dir": "error: unknown command \"audit\"\n",
	} {
		out, errb, code := ctlRun(t, f.e, strings.Fields(args)...)
		if code != 2 || out != "" || errb != want {
			t.Errorf("%s: exit %d stdout %q stderr %q, want %q", args, code, out, errb, want)
		}
	}
	for _, h := range []string{"--help", "-h"} {
		out, _, code := ctlRun(t, f.e, "doctor", h)
		if code != 0 || !strings.HasPrefix(out, "usage: mashctl doctor") {
			t.Errorf("doctor %s: %d %q", h, code, out)
		}
	}
	if out, _, code := ctlRun(t, f.e, "--help"); code != 0 || !strings.Contains(out, "mashctl doctor") || strings.Contains(out, "mashctl audit") || strings.Contains(out, "issued") {
		t.Errorf("top-level help: %d\n%s", code, out)
	}
}

// A failed config stops everything after it: the later rows are skip with what they need, and nothing else is counted.
func TestDoctorStopsAtTheFirstFailedCheck(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": home + "/none"}
	var out, errb bytes.Buffer
	code := runEnv([]string{"doctor", "--json"}, func(k string) string { return env[k] }, &out, &errb)
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%v\n%s\n%s", err, out.String(), errb.String())
	}
	if code != 1 || rep.Problems != 1 || rep.Warnings != 0 || len(rep.Checks) != 6 || rep.Checks[0].Name != "config" || rep.Checks[0].State != "fail" ||
		rep.Checks[0].Detail == "" || rep.Checks[0].Fix == nil || *rep.Checks[0].Fix != "mashctl init --config <file>" {
		t.Fatalf("%d %+v", code, rep)
	}
	for i, needs := range []string{"needs the config", "needs the server", "needs sign-in", "needs the storage", "needs sign-in"} {
		if c := rep.Checks[i+1]; c.State != "skip" || c.Detail != needs || c.Fix != nil {
			t.Errorf("row %d: %+v", i+1, c)
		}
	}
	out.Reset()
	code = runEnv([]string{"doctor"}, func(k string) string { return env[k] }, &out, &errb)
	if code != 1 || !strings.HasPrefix(out.String(), "config     FAIL  ") || !strings.HasSuffix(out.String(), "\n\n1 problem.\n") ||
		strings.Count(out.String(), "\n") != 8 || strings.Contains(out.String(), "hint:") || !strings.HasSuffix(errb.String(), "hint: mashctl init --config <file>\n") {
		t.Fatalf("%d %q stderr %q", code, out.String(), errb.String())
	}
}

// A server that does not answer skips the checks that need it.
func TestDoctorServerDown(t *testing.T) {
	e := newXYEnv(t)
	e.env["MASH_SERVER"] = "nats://127.0.0.1:1"
	rep, by, code := doctorJSON(t, e.run)
	if code != 1 || len(rep.Checks) != 6 || by["config"].State != "ok" || by["server"].State != "fail" || by["server"].Detail != serverSilent ||
		by["server"].Fix == nil || *by["server"].Fix != "mashctl status" {
		t.Fatalf("%d %+v", code, rep)
	}
	for _, n := range []string{"sign-in", "storage", "endpoints", "keys"} {
		if by[n].State != "skip" {
			t.Errorf("%s ran without the server: %+v", n, by[n])
		}
	}
}

// Creds the server refuses fail sign-in, and the server itself is up.
func TestDoctorSignInRefused(t *testing.T) {
	e := newXYEnv(t)
	other := newApplyEnv(t) // another deployment's admin creds
	e.env["MASH_ADMIN_CREDS"] = other.pl.Creds
	rep, by, code := doctorJSON(t, e.run)
	if code != 1 || len(rep.Checks) != 6 || by["server"].State != "ok" || by["sign-in"].State != "fail" || by["sign-in"].Fix == nil {
		t.Fatalf("%d %+v", code, rep)
	}
	for _, n := range []string{"storage", "endpoints", "keys"} {
		if by[n].State != "skip" {
			t.Errorf("%s ran without sign-in: %+v", n, by[n])
		}
	}
}

// A missing bucket fails storage and skips the endpoints check; a cap below the configured one warns.
func TestDoctorStorage(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("a"))
	ctx := context.Background()
	_, by, code := doctorJSON(t, f.run(t))
	if by["storage"].State != "ok" || by["endpoints"].State != "ok" || code != 0 {
		t.Fatalf("%d %+v", code, by)
	}

	s, err := f.js.Stream(ctx, "KV_"+store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := s.Info(ctx)
	cfg := info.Config
	cfg.MaxBytes = 2 << 20
	if _, err := f.js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	_, by, code = doctorJSON(t, f.run(t))
	if c := by["storage"]; c.State != "warn" || !strings.Contains(c.Detail, "below the configured") || code != 0 {
		t.Fatalf("cap: %d %+v", code, c)
	}

	if err := f.js.DeleteKeyValue(ctx, store.LeaseBucket); err != nil {
		t.Fatal(err)
	}
	rep, by, code := doctorJSON(t, f.run(t))
	if c := by["storage"]; c.State != "fail" || c.Detail != "the "+store.LeaseBucket+" bucket is missing" || c.Fix == nil || code != 1 {
		t.Fatalf("missing: %d %+v", code, c)
	}
	if c := by["endpoints"]; c.State != "skip" || c.Detail != "needs the storage" {
		t.Errorf("endpoints ran without storage: %+v", rep.Checks)
	}
}

// A lease bucket that allows rollup writes is a storage failure until init hardens it.
func TestDoctorLeaseBucketThatAllowsRollup(t *testing.T) {
	f := newAuditEnv(t)
	ctx := context.Background()
	if err := f.js.DeleteKeyValue(ctx, store.LeaseBucket); err != nil {
		t.Fatal(err)
	}
	if _, err := f.js.CreateKeyValue(ctx, store.LeaseConfigFor(store.DefaultLimits())); err != nil { // as an older mash made it
		t.Fatal(err)
	}
	_, by, code := doctorJSON(t, f.run(t))
	if c := by["storage"]; c.State != "fail" || !strings.Contains(c.Detail, "allows rollup writes") || code != 1 {
		t.Fatalf("%d %+v", code, c)
	}
	makeStorageFor(t, f.e)
	if _, by, code := doctorJSON(t, f.run(t)); by["storage"].State != "ok" || code != 0 {
		t.Fatalf("after init: %d %+v", code, by["storage"])
	}
}

// Endpoint records and inboxes are compared both ways, and the mail an inbox lost unread is a failure.
func TestDoctorEndpoints(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("a"))
	f.endpoint(t, ad("b"))
	ctx := context.Background()

	if err := f.js.DeleteStream(ctx, ad("b").InboxStream()); err != nil {
		t.Fatal(err)
	}
	_, by, code := doctorJSON(t, f.run(t))
	if c := by["endpoints"]; c.State != "fail" || c.Detail != "1 endpoint record has no inbox: o.g.b" || c.Fix == nil || *c.Fix != "mashctl endpoint show o.g.b" || code != 1 {
		t.Fatalf("%d %+v", code, c)
	}

	f.endpoint(t, ad("c"))
	f.capInbox(t, ad("c"), 4096)
	f.publishBig(t, ad("c").Subjects().Mail, 20) // nothing read: the cap dropped the oldest
	_, by, _ = doctorJSON(t, f.run(t))
	c := by["endpoints"]
	if c.State != "fail" || !strings.HasSuffix(c.Detail, "; 1 inbox has lost mail unread: o.g.c") || c.Fix == nil || *c.Fix != "mashctl endpoint show o.g.b" {
		t.Fatalf("%+v", c)
	}
}

// An inbox near its cap that lost nothing unread is a warning, not a problem.
func TestDoctorNearFullInboxWarns(t *testing.T) {
	f := newAuditEnv(t)
	f.endpoint(t, ad("full"))
	f.capInbox(t, ad("full"), 4096)
	f.publishBig(t, ad("full").Subjects().Mail, 7) // about 3.8 KiB of 4 KiB, nothing dropped
	rep, by, code := doctorJSON(t, f.run(t))
	c := by["endpoints"]
	if code != 0 || rep.Problems != 0 || c.State != "warn" || c.Detail != "1 inbox is 90% full or more: o.g.full" || c.Fix == nil || *c.Fix != "mashctl limits apply o.g.full" {
		t.Fatalf("%d %+v", code, c)
	}
	if _, _, code := doctorJSON(t, f.run(t), "--strict"); code != 1 {
		t.Errorf("--strict exit %d", code)
	}
}

// An unfinished retirement fails; a retired-name bucket at half its cap warns.
func TestDoctorRetiredNames(t *testing.T) {
	f := newAuditEnv(t)
	ctx := context.Background()
	kv, err := f.js.KeyValue(ctx, store.RetiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	put := func(instance, state string) {
		t.Helper()
		v, _ := json.Marshal(store.Retired{Owner: "acme", Group: "demo", Instance: instance, User: "UKEY", RetiredAt: "2026-11-10T01:02:03Z", State: state})
		if _, err := kv.Put(ctx, "acme_demo_"+instance, v); err != nil {
			t.Fatal(err)
		}
	}
	put("done", store.RetiredFinal)
	if _, by, code := doctorJSON(t, f.run(t)); by["endpoints"].State != "ok" || code != 0 {
		t.Fatalf("a finished retirement: %d %+v", code, by["endpoints"])
	}
	put("half", store.RetiredPending)
	_, by, code := doctorJSON(t, f.run(t))
	if c := by["endpoints"]; c.State != "fail" || c.Detail != "1 retirement is not finished: acme.demo.half" || c.Fix == nil || *c.Fix != "mashctl endpoint delete acme.demo.half" || code != 1 {
		t.Fatalf("%d %+v", code, c)
	}

	// the alarm: a bucket capped at 16 KiB and filled to half
	if err := f.js.DeleteKeyValue(ctx, store.RetiredBucket); err != nil {
		t.Fatal(err)
	}
	cfg := store.RetiredConfig()
	cfg.MaxBytes = 16 << 10
	if kv, err = f.js.CreateKeyValue(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		put(fmt.Sprintf("m%d", i), store.RetiredFinal)
		st, _ := kv.Status(ctx)
		if st.Bytes()*2 >= 16<<10 {
			break
		}
		if i > 200 {
			t.Fatal("never reached half")
		}
	}
	_, by, code = doctorJSON(t, f.run(t))
	if c := by["endpoints"]; c.State != "warn" || !strings.HasPrefix(c.Detail, "the retired-name bucket is ") || code != 0 {
		t.Fatalf("alarm: %d %+v", code, c)
	}
}

// The helpers the endpoints check reads the snapshot with.
func TestInboxProblemsAndNote(t *testing.T) {
	calm := auditInbox{Present: true, Msgs: 3, MaxBytes: 1000, Bytes: 100, FillPercent: 10}
	if inboxProblems("k", calm) != nil || inboxNote("k", calm) != "" {
		t.Error("a calm inbox reports nothing")
	}
	turnover := auditInbox{Present: true, Evicted: 5, EvictedUnread: 0, FillPercent: 50}
	if inboxProblems("k", turnover) != nil {
		t.Error("read mail that aged out is not a problem")
	}
	full := auditInbox{Present: true, MaxBytes: 1000, Bytes: 950, FillPercent: 95}
	if inboxProblems("k", full) != nil || !strings.Contains(inboxNote("k", full), "95% full") {
		t.Error("near full is a note")
	}
	lost := auditInbox{Present: true, Evicted: 9, EvictedUnread: 4, MaxBytes: 1000, Bytes: 950, FillPercent: 95}
	p := inboxProblems("k", lost)
	if len(p) != 1 || !strings.Contains(p[0].Error, "at least 4 unread message(s) lost") || !strings.Contains(p[0].Error, "95% full") || inboxNote("k", lost) != "" {
		t.Errorf("%+v", p)
	}
}

func TestRetiredAlarmBoundary(t *testing.T) {
	cases := []struct {
		used  uint64
		max   int64
		alarm bool
	}{{0, 100, false}, {49, 100, false}, {50, 100, true}, {51, 100, true}, {1, 1, true}, {0, 0, false}}
	for _, c := range cases {
		if got := retiredAlarm(c.used, c.max); got != c.alarm {
			t.Errorf("retiredAlarm(%d, %d) = %t, want %t", c.used, c.max, got, c.alarm)
		}
	}
}

func TestBuildRetiredUncappedIsAProblemNotAnAlarm(t *testing.T) {
	r, probs := buildRetired(admin.AuditRetired{Present: true, Bytes: 100, MaxBytes: 0})
	if r.Alarm || len(probs) != 1 || !strings.Contains(probs[0].Error, "no size cap") {
		t.Errorf("%+v %+v", r, probs)
	}
}

// Keys: a key signed outside mashctl is connected and mash did not issue it. It is never a failure by itself; only
// --audit-users fails on it.
func TestDoctorKeysForeignUserIsAClauseNotAFailure(t *testing.T) {
	e := newXYEnv(t)
	rogue := e.connect(e.mintRogue("rogue"), nats.Name("rogue-client"))
	if !rogue.IsConnected() {
		t.Fatal("the rogue did not connect")
	}
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       ok    mashctl's users ok; 1 other user, not issued by mashctl\n\nNo problems.\n"
	for _, args := range [][]string{{"doctor"}, {"doctor", "--strict"}} {
		out, errb, code := e.run(args...)
		if code != 0 || cleanText(e, out) != want || errb != "" {
			t.Fatalf("%v: exit %d\n%q\nstderr %q\nwant\n%q", args, code, cleanText(e, out), errb, want)
		}
	}
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" || by["keys"].Detail != "mashctl's users ok; 1 other user, not issued by mashctl" || by["keys"].Fix != nil {
		t.Fatalf("json: %d %+v", code, by["keys"])
	}
	rogue.Close()
	time.Sleep(200 * time.Millisecond)
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].Detail != "every connected user was issued by mashctl" {
		t.Fatalf("after the rogue left: %d %+v", code, by["keys"])
	}
}

// --audit-users fails on a user mashctl did not issue, names it, and never points at `mashctl init`; with --strict too.
func TestDoctorAuditUsersFailsOnAForeignUser(t *testing.T) {
	e := newXYEnv(t)
	key := rogueKey(t, e, "tool-a")
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       FAIL  1 connected user was not issued by mashctl:\n                 " + key + "\n\n1 problem.\n"
	const hint = "hint: revoke it with the tool that issued it (mashctl revokes only its own)\n"
	for _, args := range [][]string{{"doctor", "--audit-users"}, {"doctor", "--strict", "--audit-users"}, {"doctor", "--audit-users", "--strict"}} {
		out, errb, code := e.run(args...)
		if code != 1 || cleanText(e, out) != want || errb != hint {
			t.Fatalf("%v: exit %d\n%q\nstderr %q\nwant\n%q", args, code, cleanText(e, out), errb, want)
		}
		if strings.Contains(out+errb, "init --config") || strings.Contains(out+errb, "mashctl init") {
			t.Errorf("%v: points at init: %q %q", args, out, errb)
		}
	}
	_, by, code := doctorJSON(t, e.run, "--audit-users")
	if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != "1 connected user was not issued by mashctl:\n"+key || c.Fix == nil || *c.Fix != "revoke it with the tool that issued it (mashctl revokes only its own)" {
		t.Fatalf("json: %d %+v", code, by["keys"])
	}
}

// Only mash's users: the three modes pass with the unchanged ok text, and a 0.5.7-style config.json (no new keys, and
// an unknown key such as a leftover external_keys) works the same.
func TestDoctorOnlyMashUsersPassesInEveryMode(t *testing.T) {
	e := newXYEnv(t)
	const want = "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       ok    every connected user was issued by mashctl\n\nNo problems.\n"
	check := func(label string) {
		t.Helper()
		for _, args := range [][]string{{"doctor"}, {"doctor", "--strict"}, {"doctor", "--audit-users"}, {"doctor", "--strict", "--audit-users"}} {
			out, errb, code := e.run(args...)
			if code != 0 || cleanText(e, out) != want || errb != "" {
				t.Fatalf("%s %v: exit %d\n%q\nstderr %q", label, args, code, cleanText(e, out), errb)
			}
		}
	}
	check("fixture config")
	e.writeConfig(`{"system_creds":"` + e.sysCreds + `"}`) // a 0.5.7-style file
	check("0.5.7 config")
	e.writeConfig(`{"system_creds":"` + e.sysCreds + `","external_keys":["` + e.acctPub + `"]}`) // an unknown key is ignored
	check("unknown external_keys")
}

// The admin key this command connects with is not exempt: unrecorded, --audit-users reports it; once recorded it is clean.
func TestDoctorKeysHasNoExemptionForTheCommandsOwnAdmin(t *testing.T) {
	e := newXYEnv(t)
	path, _ := mintUnrecordedAdmin(t, e)
	e.env["MASH_ADMIN_CREDS"] = path
	if _, by, code := doctorJSON(t, e.run, "--audit-users"); code != 1 || !strings.HasPrefix(by["keys"].Detail, "1 connected user was not issued by mashctl:\nU") {
		t.Fatalf("an unrecorded admin was not reported: %d %+v", code, by["keys"])
	}
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" || !strings.Contains(by["keys"].Detail, "other user") {
		t.Fatalf("default: %d %+v", code, by["keys"])
	}
}

// A key the record holds as revoked that is still connected is its own failure.
func TestDoctorKeysReportsARevokedKeyStillConnected(t *testing.T) {
	e := newXYEnv(t)
	e.connect(natsauth.UserCredentials(e.credsPath(xy("x").String())), nats.Name("x-client"))
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	_, yKey := credsSubject(t, e.credsPath(xy("y").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, yKey, time.Now()); err != nil { // revoked and not connected: no finding
		t.Fatal(err)
	}
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" {
		t.Fatalf("revoked, not connected: %d %+v", code, by["keys"])
	}
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, by, code := doctorJSON(t, e.run); code != 1 || by["keys"].State != "fail" || by["keys"].Detail != "1 revoked key is still connected" {
		t.Fatalf("revoked and connected: %d %+v", code, by["keys"])
	}
}

// Fewer servers answering than the configuration lists is not a clean answer; a finding is still reported with it.
func TestDoctorKeysRefusesAnIncompleteServerSet(t *testing.T) {
	e := newXYEnv(t)
	e.servers(2)
	rep, by, code := doctorJSON(t, e.run)
	c := by["keys"]
	if code != 1 || rep.Problems != 1 || c.State != "fail" || !strings.Contains(c.Detail, "1 server(s) replied") {
		t.Fatalf("%d %+v", code, rep)
	}
	e.connect(e.mintRogue("rogue"))
	_, by, _ = doctorJSON(t, e.run)
	if c := by["keys"]; !strings.HasPrefix(c.Detail, "1 server(s) replied") || !strings.HasSuffix(c.Detail, "\n1 other user, not issued by mashctl") {
		t.Fatalf("%+v", c)
	}
	_, by, _ = doctorJSON(t, e.run, "--audit-users")
	if c := by["keys"]; !strings.HasPrefix(c.Detail, "1 server(s) replied") || !strings.Contains(c.Detail, "\n1 connected user was not issued by mashctl:\nU") {
		t.Fatalf("%+v", c)
	}
	e.servers(1)
	if _, by, code := doctorJSON(t, e.run); by["keys"].State != "ok" || code != 0 { // the rogue is still connected, and no failure
		t.Fatalf("%d %+v", code, by["keys"])
	}
	if _, by, code := doctorJSON(t, e.run, "--audit-users"); by["keys"].State != "fail" || code != 1 {
		t.Fatalf("%d %+v", code, by["keys"])
	}
}

// A system credentials file that cannot be used is a failure with its reason.
func TestDoctorKeysUnusableSystemCreds(t *testing.T) {
	e := newXYEnv(t)
	e.writeConfig(`{"system_creds":"/nonexistent/sys.creds"}`)
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 1 || c.State != "fail" || !strings.Contains(c.Detail, "system account credentials") {
		t.Fatalf("%d %+v", code, c)
	}
}

// When the first problem names no fix there is no hint, even if a later row has one.
func TestDoctorNoHintWhenTheFirstProblemHasNoFix(t *testing.T) {
	rep := doctorReport{Checks: []doctorCheck{}}
	fix := "mashctl status"
	rep.add("config", stateOK, "", "")
	rep.add("server", stateFail, "no answer", "")
	rep.add("sign-in", stateFail, "refused", fix)
	rep.complete()
	if rep.Checks[1].Fix != nil || rep.hint() != "" {
		t.Fatalf("hint %q for %+v", rep.hint(), rep.Checks)
	}
	var out, hw bytes.Buffer
	rep.writeText(&out, &hw)
	if strings.Contains(out.String(), "hint:") || !strings.HasSuffix(out.String(), "\n\n2 problems.\n") || hw.Len() != 0 {
		t.Errorf("%q stderr %q", out.String(), hw.String())
	}
	// and a warnings-only run whose first warning has no fix
	w := doctorReport{Checks: []doctorCheck{}}
	w.add("config", stateWarn, "old", "")
	w.add("keys", stateWarn, "x", fix)
	w.complete()
	if w.hint() != "" {
		t.Errorf("warning hint %q", w.hint())
	}
}

// Skip rows are printed in the text with what they need, and counted as neither problems nor warnings.
func TestDoctorSkipRowsInText(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": home + "/none"}
	var out, errb bytes.Buffer
	runEnv([]string{"doctor"}, func(k string) string { return env[k] }, &out, &errb)
	for _, line := range []string{"server     skip  needs the config\n", "sign-in    skip  needs the server\n", "storage    skip  needs sign-in\n",
		"endpoints  skip  needs the storage\n", "keys       skip  needs sign-in\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("missing %q in\n%s", line, out.String())
		}
	}
	if !strings.Contains(out.String(), "\n\n1 problem.\n") {
		t.Errorf("skip rows were counted:\n%s", out.String())
	}
}

// A plain URL anywhere in the list is refused by the sign-in step, and that is a configuration error (exit 2), as it is
// when the first server is the plain one.
func TestDoctorPlainServerAfterReachableOneIsExit2(t *testing.T) {
	ca, cert, key := writeTestPKI(t, t.TempDir())
	e := newApplyEnvOpts(t, nil, func(o *server.Options) {
		cfg, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: cert, KeyFile: key})
		if err != nil {
			t.Fatal(err)
		}
		o.TLSConfig, o.TLS, o.TLSTimeout = cfg, true, 5
	})
	delete(e.env, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
	e.env["MASH_CA"] = ca
	live := fmt.Sprintf("tls://127.0.0.1:%d", e.srv.Addr().(*net.TCPAddr).Port)
	e.env["MASH_SERVER"] = live + ",nats://127.0.0.1:1"
	out, errb, code := e.run("doctor")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %q %q", code, out, errb)
	}
}

// The same refusal when the first server cannot be reached: the result does not depend on which server answers.
func TestDoctorPlainServerAfterUnreachableOneIsExit2(t *testing.T) {
	ca, cert, key := writeTestPKI(t, t.TempDir())
	e := newApplyEnvOpts(t, nil, func(o *server.Options) {
		cfg, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: cert, KeyFile: key})
		if err != nil {
			t.Fatal(err)
		}
		o.TLSConfig, o.TLS, o.TLSTimeout = cfg, true, 5
	})
	delete(e.env, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
	e.env["MASH_CA"] = ca
	e.env["MASH_SERVER"] = "tls://127.0.0.1:1,nats://127.0.0.1:2"
	out, errb, code := e.run("doctor")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %q %q", code, out, errb)
	}
}

// rogueKey mints a user signed outside mashctl, connects it and returns its public key.
func rogueKey(t *testing.T, e *applyEnv, name string) string {
	t.Helper()
	akp, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	ukp, _ := nkeys.CreateUser()
	pub, _ := ukp.PublicKey()
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	uc.Permissions = *admin.Permissions(sap.Address{Owner: "alice", Group: "g", Instance: name})
	tok, err := uc.Encode(akp)
	if err != nil {
		t.Fatal(err)
	}
	nc := e.connect(nats.UserJWT(func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return ukp.Sign(n) }), nats.Name(name+"-client"))
	if !nc.IsConnected() {
		t.Fatal("the rogue did not connect")
	}
	return pub
}

var userKeyRE = regexp.MustCompile(`U[A-Z2-7]{55}`)

// A revoked key that is still connected fails without --audit-users, and its own fix stays when another user is there.
func TestDoctorRevokedStillConnectedFailsWithoutAudit(t *testing.T) {
	e := newXYEnv(t)
	e.connect(natsauth.UserCredentials(e.credsPath(xy("x").String())), nats.Name("x-client"))
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	rogueKey(t, e, "tool-a")
	for _, args := range [][]string{{}, {"--strict"}} {
		_, by, code := doctorJSON(t, e.run, args...)
		if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != "1 revoked key is still connected\n1 other user, not issued by mashctl" || c.Fix != nil {
			t.Fatalf("%v: %d %+v", args, code, c)
		}
	}
	_, by, code := doctorJSON(t, e.run, "--audit-users")
	c := by["keys"]
	if code != 1 || !strings.HasPrefix(c.Detail, "1 revoked key is still connected\n1 connected user was not issued by mashctl:\nU") || c.Fix != nil {
		t.Fatalf("audit: %d %+v", code, c)
	}
}

// The clause is singular for one user and plural for more, and the audit failure follows the same rule.
func TestDoctorForeignUserCountText(t *testing.T) {
	e := newXYEnv(t)
	rogueKey(t, e, "tool-a")
	_, by, _ := doctorJSON(t, e.run)
	if d := by["keys"].Detail; d != "mashctl's users ok; 1 other user, not issued by mashctl" {
		t.Fatalf("%q", d)
	}
	rogueKey(t, e, "tool-b")
	_, by, _ = doctorJSON(t, e.run)
	if d := by["keys"].Detail; d != "mashctl's users ok; 2 other users, not issued by mashctl" {
		t.Fatalf("%q", d)
	}
	out, _, _ := e.run("doctor")
	if !strings.Contains(out, "keys       ok    mashctl's users ok; 2 other users, not issued by mashctl\n") {
		t.Fatalf("%q", out)
	}
	_, by, _ = doctorJSON(t, e.run, "--audit-users")
	if d := by["keys"].Detail; !strings.HasPrefix(d, "2 connected users were not issued by mashctl:\n") {
		t.Fatalf("%q", d)
	}
}

// Five foreign users with --audit-users: three whole keys and "and 2 more", each on its own line, every line of the
// human output within 80 columns; --json keeps the keys whole in detail.
func TestDoctorAuditUsersManyKeysOnLinesOfTheirOwn(t *testing.T) {
	e := newXYEnv(t)
	for i := 0; i < 5; i++ {
		rogueKey(t, e, fmt.Sprintf("tool-%d", i))
	}
	out, errb, code := e.run("doctor", "--audit-users")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	text := cleanText(e, out)
	lines := strings.Split(text, "\n")
	for _, l := range append(lines, strings.Split(errb, "\n")...) {
		if len(l) > 80 {
			t.Errorf("%d columns: %q", len(l), l)
		}
	}
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "keys ") })
	if i < 0 || len(lines) < i+5 || lines[i] != "keys       FAIL  5 connected users were not issued by mashctl:" || lines[i+4] != strings.Repeat(" ", 17)+"and 2 more" {
		t.Fatalf("%q", text)
	}
	for _, l := range lines[i+1 : i+4] {
		if !regexp.MustCompile(`^ {17}U[A-Z2-7]{55}$`).MatchString(l) {
			t.Errorf("not an indented key: %q", l)
		}
	}
	_, by, _ := doctorJSON(t, e.run, "--audit-users")
	dl := strings.Split(by["keys"].Detail, "\n")
	if len(dl) != 5 || dl[0] != "5 connected users were not issued by mashctl:" || dl[4] != "and 2 more" {
		t.Fatalf("%q", by["keys"].Detail)
	}
	for _, k := range dl[1:4] {
		if !userKeyRE.MatchString(k) || len(k) != 56 {
			t.Errorf("key not whole: %q", k)
		}
	}
}

// Without the issue record every connected user would look foreign, so the keys row fails, with or without the audit,
// and never counts foreign users.
func TestDoctorKeysFailsWhenTheIssueRecordIsMissing(t *testing.T) {
	e := newXYEnv(t)
	rogueKey(t, e, "tool-a")
	nc := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if err := js.DeleteKeyValue(context.Background(), store.IssuedBucket); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{}, {"--strict"}, {"--audit-users"}} {
		_, by, code := doctorJSON(t, e.run, args...)
		if code != 1 { // FAIL in every mode: the record is mashctl's own state
			t.Errorf("%v: exit %d, want 1", args, code)
		}
		c := by["keys"]
		if c.State != "fail" || c.Detail != "no issue record; mashctl cannot tell its users from others" || c.Fix == nil || *c.Fix != "mashctl init --config <file>" {
			t.Fatalf("%v: %+v", args, c)
		}
		if len("keys       FAIL  "+c.Detail) > 80 {
			t.Errorf("%v: line too long", args)
		}
		out, _, hcode := e.run(append([]string{"doctor"}, args...)...)
		if hcode != 1 || !strings.Contains(cleanText(e, out), "no issue record") {
			t.Errorf("%v: human output exit %d: %q", args, hcode, cleanText(e, out))
		}
	}
}

// A connection with no key is an unidentified user: it is counted as unrecorded and shown as one line, "(no key reported)"
// or "(no key reported, N connections)", first among the key lines (within the cap of three lines); "and N more" counts
// the users not shown.
func TestDoctorKeysCountsAConnectionWithNoKey(t *testing.T) {
	conns := []apply.ConnzServer{{Conns: []apply.ConnzConn{{Key: ""}, {Key: "UAAA"}, {Key: ""}}}}
	un, kl, rv := connectedFindings(conns, nil)
	if len(rv) != 0 || kl != 2 || !slices.Equal(un, []string{"UAAA"}) {
		t.Fatalf("%q %d %q", un, kl, rv)
	}
	if got := keyLines(un, kl); got != "\n(no key reported, 2 connections)\nUAAA" {
		t.Fatalf("%q", got)
	}
	if got := keyLines(nil, 1); got != "\n(no key reported)" {
		t.Fatalf("%q", got)
	}
	if got := keyLines([]string{"UA", "UB", "UC", "UD"}, 2); got != "\n(no key reported, 2 connections)\nUA\nUB\nand 2 more" {
		t.Fatalf("%q", got)
	}
	if got := keyLines([]string{"UA", "UB", "UC", "UD"}, 0); got != "\nUA\nUB\nUC\nand 1 more" {
		t.Fatalf("%q", got)
	}
}

// With --audit-users and a revoked key still connected, no line of the human output passes 80 columns.
func TestDoctorAuditUsersRevokedKeyLinesFit80Columns(t *testing.T) {
	e := newXYEnv(t)
	e.connect(natsauth.UserCredentials(e.credsPath(xy("x").String())), nats.Name("x-client"))
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	rogueKey(t, e, "tool-a")
	for _, args := range [][]string{{"doctor", "--audit-users"}, {"doctor"}} {
		out, errb, _ := e.run(args...)
		for _, l := range strings.Split(cleanText(e, out)+errb, "\n") {
			if len(l) > 80 {
				t.Errorf("%v: %d columns: %q", args, len(l), l)
			}
		}
	}
}

// maxLine is the longest line of a rendered doctor output, and the line itself.
func maxLine(out string) (int, string) {
	n, at := 0, ""
	for _, l := range strings.Split(out, "\n") {
		if len(l) > n {
			n, at = len(l), l
		}
	}
	return n, at
}

// A revoked key still connected plus churn of mashctl's own users: the failure and the warning are lines of their own,
// in the text and in the JSON detail, and none passes 80 columns.
func TestDoctorKeysRevokedAndMashChurnLinesFit80Columns(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	calls := scriptScans(t, keyScan(true, xKey), keyScan(true), keyScan(true, xKey))
	_, by, code := doctorJSON(t, e.run)
	*calls = 0
	const want = "1 revoked key is still connected\nmashctl's users kept connecting and leaving; read 3 times\nwait a minute and run it again"
	if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != want || c.Fix != nil {
		t.Fatalf("%d %+v", code, c)
	}
	out, errb, _ := e.run("doctor")
	text := cleanText(e, out)
	if !strings.Contains(text, "keys       FAIL  1 revoked key is still connected\n                 mashctl's users kept connecting and leaving; read 3 times\n                 wait a minute and run it again\n") {
		t.Fatalf("%q", text)
	}
	if n, l := maxLine(text + errb); n > 80 {
		t.Fatalf("%d columns: %q", n, l)
	}
}

// With --audit-users, two servers whose lists kept changing, one of them unnamed (known by its 56-character id), are one
// line each; no line passes 80 columns and the long id is shortened.
func TestDoctorAuditUsersChurnOfTwoServersLinesFit80Columns(t *testing.T) {
	e := newXYEnv(t)
	id := strings.Repeat("N", 56)
	short := id[:10] + "..." + id[:11]
	sc := keyScan(true)
	sc.Incomplete = "server s1's connected users kept changing\nserver " + short + "'s connected users kept changing"
	scriptScans(t, sc)
	_, by, code := doctorJSON(t, e.run, "--audit-users")
	want := "server s1's connected users kept changing\nserver " + short + "'s connected users kept changing\nread 3 times; wait a minute and run it again"
	if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != want {
		t.Fatalf("%d %+v", code, c)
	}
	out, errb, _ := e.run("doctor", "--audit-users")
	text := cleanText(e, out)
	if n, l := maxLine(text + errb); n > 80 {
		t.Fatalf("%d columns: %q", n, l)
	}
	for _, l := range []string{"server s1's connected", "server " + short + "'s connected"} {
		if strings.Count(text, l) != 1 {
			t.Fatalf("%q once: %q", l, text)
		}
	}
	// Other incomplete reasons that run long are continued on lines of their own.
	long := "2 server(s) replied, and they name different clusters (first seen: " + strings.Repeat("c", 30) + "): membership cannot be verified from here"
	sc.Incomplete, sc.Unsettled = long, false
	scriptScans(t, sc)
	out, errb, _ = e.run("doctor", "--audit-users")
	if n, l := maxLine(cleanText(e, out) + errb); n > 80 {
		t.Fatalf("%d columns: %q", n, l)
	}
}

// An incomplete reason that wraps, or holds a word longer than a line, still fits 80 columns beside a revoked key.
func TestDoctorKeysLongIncompleteReasonWithRevokedKeyFits80Columns(t *testing.T) {
	word := ""
	for i := 0; i < 100; i++ {
		word += string(rune('a' + i%26))
	}
	for name, reason := range map[string]string{
		"wraps":    "2 server(s) replied, and they name different clusters (first seen: " + strings.Repeat("c", 30) + "): membership cannot be verified",
		"longword": "server " + word + " did not reply",
	} {
		t.Run(name, func(t *testing.T) {
			e := newXYEnv(t)
			_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
			adm, ctx, closeAdm := e.admin()
			defer closeAdm()
			if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			sc := keyScan(false, xKey)
			sc.Incomplete = reason
			scriptScans(t, sc)
			out, errb, _ := e.run("doctor", "--audit-users")
			text := cleanText(e, out)
			if n, l := maxLine(text + errb); n > 80 {
				t.Fatalf("%d columns: %q", n, l)
			}
			if !strings.Contains(text, "1 revoked key is still connected") {
				t.Fatalf("%q", text)
			}
			if name == "longword" {
				var got strings.Builder
				for _, l := range strings.Split(text, "\n") {
					for _, c := range strings.Fields(l) {
						if len(c) > 0 && strings.Contains(word, c) {
							got.WriteString(c)
						}
					}
				}
				if !strings.Contains(got.String(), word) {
					t.Fatalf("word lost: %q", text)
				}
			}
		})
	}
}

// scriptScans makes the keys check read the scripted scans in order (the last one repeats) and returns the number of
// reads made. churnPause is 0 for the test.
func scriptScans(t *testing.T, scans ...connScan) *int {
	t.Helper()
	oldScan, oldPause := scanConns, churnPause
	t.Cleanup(func() { scanConns, churnPause = oldScan, oldPause })
	churnPause = 0
	calls := 0
	scanConns = func(context.Context, *nats.Conn, string, int) (connScan, error) {
		s := scans[min(calls, len(scans)-1)]
		calls++
		return s, nil
	}
	return &calls
}

// keyScan is one read in which each key is connected once, with connection id 1 (see cidScan).
func keyScan(unsettled bool, keys ...string) connScan {
	cids := map[string]uint64{}
	for _, k := range keys {
		cids[k] = 1
	}
	return cidScan(unsettled, cids)
}

// cidScan is one read of server s1 with each key connected under the given connection id; a key "" is a keyless
// connection.
func cidScan(unsettled bool, cids map[string]uint64) connScan {
	var cs []apply.ConnzConn
	for k, id := range cids {
		cs = append(cs, apply.ConnzConn{Key: k, CID: id})
	}
	sc := connScan{Conns: []apply.ConnzServer{{Name: "s1", Conns: cs}}}
	if unsettled {
		sc.Incomplete = "server s1's connected users kept changing"
		sc.Unsettled = true
	}
	return sc
}

// checkFits80 runs doctor with args and fails if any line passes 80 columns; it returns the text so the caller can
// assert the line it meant to measure is there. The caller resets the scripted read counter first.
func checkFits80(t *testing.T, e *applyEnv, args ...string) string {
	t.Helper()
	out, errb, _ := e.run(args...)
	text := cleanText(e, out) + errb
	for _, l := range strings.Split(text, "\n") {
		if len(l) > 80 {
			t.Errorf("%v: %d columns: %q", args, len(l), l)
		}
	}
	return text
}

// Churn only among users mashctl did not issue is not a finding: ok in both modes, the count shown.
func TestDoctorKeysChurnOfForeignUsersIsNotAFinding(t *testing.T) {
	e := newXYEnv(t)
	calls := scriptScans(t, keyScan(true, "UFOREIGN1"), keyScan(true, "UFOREIGN2"), keyScan(true, "UFOREIGN1", "UFOREIGN3"))
	for _, args := range [][]string{{}, {"--strict"}} {
		*calls = 0
		_, by, code := doctorJSON(t, e.run, args...)
		if c := by["keys"]; code != 0 || c.State != "ok" || c.Detail != "mashctl's users ok; 2 other users, not issued by mashctl" {
			t.Fatalf("%v: %d %+v", args, code, c)
		}
	}
	*calls = 0
	if text := checkFits80(t, e, "doctor"); !strings.Contains(text, "mashctl's users ok; 2 other users") {
		t.Fatalf("not measuring the ok line: %q", text)
	}
}

// Persistent churn of one of mashctl's users warns after three reads: exit 0, and 1 with --strict.
func TestDoctorKeysChurnOfMashUsersWarns(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	calls := scriptScans(t, keyScan(true, xKey), keyScan(true), keyScan(true, xKey))
	const line = "mashctl's users kept connecting and leaving; read 3 times"
	for _, c := range []struct {
		args []string
		exit int
	}{{nil, 0}, {[]string{"--strict"}, 1}} {
		*calls = 0
		_, by, code := doctorJSON(t, e.run, c.args...)
		k := by["keys"]
		if code != c.exit || k.State != "warn" || !strings.HasPrefix(k.Detail, line) || k.Fix != nil || *calls != 3 {
			t.Fatalf("%v: %d calls %d %+v", c.args, code, *calls, k)
		}
	}
	*calls = 0
	out, _, _ := e.run("doctor")
	if !strings.Contains(cleanText(e, out), "keys       warn  "+line) {
		t.Fatalf("%q", cleanText(e, out))
	}
	*calls = 0
	if text := checkFits80(t, e, "doctor"); !strings.Contains(text, "keys       warn  "+line) {
		t.Fatalf("not measuring the warn line: %q", text)
	}
}

// A row with a revoked key still connected prints no hint, even when a churn warning follows it: rerunning doctor does
// not end the connection. The churn warning alone has no hint either: waiting is not a fix.
func TestDoctorKeysRevokedAndChurnHasNoHint(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	calls := scriptScans(t, keyScan(true, xKey), keyScan(true), keyScan(true, xKey))
	out, errb, code := e.run("doctor")
	if code != 1 || !strings.Contains(cleanText(e, out), "FAIL") || strings.Contains(errb, "hint:") {
		t.Fatalf("revoked and churn: %d %q %q", code, out, errb)
	}
	*calls = 0
	_, by, _ := doctorJSON(t, e.run)
	if c := by["keys"]; c.State != "fail" || c.Fix != nil {
		t.Fatalf("json: %+v", c)
	}
	// Churn alone, with no revoked key, has no hint.
	e2 := newXYEnv(t)
	_, x2 := credsSubject(t, e2.credsPath(xy("x").String()))
	scriptScans(t, keyScan(true, x2), keyScan(true), keyScan(true, x2))
	out, errb, code = e2.run("doctor")
	if code != 0 || !strings.Contains(cleanText(e2, out), "warn") || strings.Contains(errb, "hint:") {
		t.Fatalf("churn alone: %d %q %q", code, out, errb)
	}
}

// With --audit-users an unsettled read fails in plain words, and never names the scan's inner workings.
func TestDoctorAuditUsersFailsOnChurn(t *testing.T) {
	e := newXYEnv(t)
	scriptScans(t, keyScan(true))
	_, by, code := doctorJSON(t, e.run, "--audit-users")
	k := by["keys"]
	if code != 1 || k.State != "fail" || k.Detail != "server s1's connected users kept changing\nread 3 times; wait a minute and run it again" || k.Fix != nil {
		t.Fatalf("%d %+v", code, k)
	}
	if low := strings.ToLower(k.Detail); strings.Contains(low, "connz") || strings.Contains(low, "nats") || strings.Contains(low, "scans") {
		t.Fatalf("%q", k.Detail)
	}
	if text := checkFits80(t, e, "doctor", "--audit-users"); !strings.Contains(text, "kept changing") {
		t.Fatalf("not measuring the churn line: %q", text)
	}
}

// A read that settles on the second or third try gives the usual ok line.
func TestDoctorKeysChurnThatSettlesIsOK(t *testing.T) {
	e := newXYEnv(t)
	for n := 1; n <= 2; n++ {
		scans := make([]connScan, 0, 3)
		for i := 0; i < n; i++ {
			scans = append(scans, keyScan(true, "UFOREIGN1"))
		}
		scans = append(scans, keyScan(false, "UFOREIGN1"))
		calls := scriptScans(t, scans...)
		_, by, code := doctorJSON(t, e.run, "--strict")
		if c := by["keys"]; code != 0 || c.State != "ok" || c.Detail != "mashctl's users ok; 1 other user, not issued by mashctl" || *calls != n+1 {
			t.Fatalf("settles after %d: %d calls %d %+v", n, code, *calls, c)
		}
	}
}

// A server that did not answer is not churn: it fails in plain mode, and is not retried.
func TestDoctorKeysSetIncompleteIsNotChurn(t *testing.T) {
	e := newXYEnv(t)
	sc := keyScan(false, "UFOREIGN1")
	sc.Incomplete = "1 server(s) replied of 2 expected"
	calls := scriptScans(t, sc)
	_, by, code := doctorJSON(t, e.run)
	if k := by["keys"]; code != 1 || k.State != "fail" || !strings.Contains(k.Detail, "1 server(s) replied") || *calls != 1 {
		t.Fatalf("%d calls %d %+v", code, *calls, k)
	}
}

func runBoth(t *testing.T, e *applyEnv, calls *int, wantState string, strictExit int, f func(c doctorCheck)) {
	t.Helper()
	for _, args := range [][]string{{}, {"--strict"}} {
		*calls = 0
		_, by, code := doctorJSON(t, e.run, args...)
		want := 0
		if len(args) > 0 {
			want = strictExit
		}
		if c := by["keys"]; code != want || c.State != wantState {
			t.Fatalf("%v: %d %+v", args, code, c)
		}
		if f != nil {
			f(by["keys"])
		}
	}
}

// A revoked key seen in an earlier unsettled read but not the last still fails.
func TestDoctorKeysRevokedSeenInEarlierReadFails(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	calls := scriptScans(t, keyScan(true, xKey, "UFOREIGN1"), keyScan(true, "UFOREIGN1"), keyScan(true, "UFOREIGN1"))
	for _, args := range [][]string{{}, {"--strict"}} {
		*calls = 0
		_, by, code := doctorJSON(t, e.run, args...)
		if c := by["keys"]; code != 1 || c.State != "fail" || !strings.HasPrefix(c.Detail, "1 revoked key is still connected") || c.Fix != nil {
			t.Fatalf("%v: %d %+v", args, code, c)
		}
	}
}

// A foreign key that reconnects (same key, new connection id) is not mashctl's churn.
func TestDoctorKeysForeignReconnectIsNotChurn(t *testing.T) {
	e := newXYEnv(t)
	calls := scriptScans(t, cidScan(true, map[string]uint64{"UFOREIGN1": 1}), cidScan(true, map[string]uint64{"UFOREIGN1": 2}), cidScan(true, map[string]uint64{"UFOREIGN1": 3}))
	runBoth(t, e, calls, "ok", 0, func(c doctorCheck) {
		if c.Detail != "mashctl's users ok; 1 other user, not issued by mashctl" {
			t.Fatalf("%+v", c)
		}
	})
}

// Churn of keyless connections only is not mashctl's.
func TestDoctorKeysKeylessChurnIsNotChurn(t *testing.T) {
	e := newXYEnv(t)
	calls := scriptScans(t, cidScan(true, map[string]uint64{"": 1}), cidScan(true, map[string]uint64{"": 2}), cidScan(true, map[string]uint64{"": 3, "UFOREIGN1": 9}))
	runBoth(t, e, calls, "ok", 0, nil)
}

// Unsettled reads that do not differ even by connection id are not mashctl's churn.
func TestDoctorKeysUnsettledButIdenticalIsOK(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	calls := scriptScans(t, cidScan(true, map[string]uint64{xKey: 5, "UFOREIGN1": 1}))
	runBoth(t, e, calls, "ok", 0, nil)
}

// A recorded key whose connection ids differ between reads is mashctl's churn.
func TestDoctorKeysRecordedCIDChangeWarns(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	calls := scriptScans(t, cidScan(true, map[string]uint64{xKey: 1}), cidScan(true, map[string]uint64{xKey: 2}), cidScan(true, map[string]uint64{xKey: 2}))
	runBoth(t, e, calls, "warn", 1, func(c doctorCheck) {
		if !strings.HasPrefix(c.Detail, "mashctl's users kept connecting and leaving; read 3 times") || c.Fix != nil {
			t.Fatalf("%+v", c)
		}
	})
}

// The deadline after one complete read is unsettled handling, not a failure from the error. One unsettled read cannot
// rule out mashctl's users, so it warns; --audit-users fails on the unsettled list.
func TestDoctorKeysDeadlineAfterOneRead(t *testing.T) {
	e := newXYEnv(t)
	oldScan, oldPause := scanConns, churnPause
	t.Cleanup(func() { scanConns, churnPause = oldScan, oldPause })
	churnPause = 0
	oldDeadline := applyDeadline
	t.Cleanup(func() { applyDeadline = oldDeadline })
	applyDeadline = 300 * time.Millisecond
	calls := 0
	scanConns = func(ctx context.Context, _ *nats.Conn, _ string, _ int) (connScan, error) {
		calls++
		if calls > 1 {
			<-ctx.Done() // doctor's own deadline ends the read
			return connScan{}, ctx.Err()
		}
		return keyScan(true), nil
	}
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 0 || c.State != "warn" || !strings.HasPrefix(c.Detail, "mashctl's users kept connecting and leaving; read once") {
		t.Fatalf("%d %+v", code, c)
	}
	calls = 0
	_, by, code = doctorJSON(t, e.run, "--audit-users")
	if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != "server s1's connected users kept changing\nread once; wait a minute and run it again" {
		t.Fatalf("%d %+v", code, c)
	}
	// An error on the first read still fails.
	scanConns = func(context.Context, *nats.Conn, string, int) (connScan, error) {
		return connScan{}, context.DeadlineExceeded
	}
	if _, by, code := doctorJSON(t, e.run); code != 1 || by["keys"].State != "fail" {
		t.Fatalf("%d %+v", code, by["keys"])
	}
}

// A scan whose pages never reconcile is unsettled (worth another read) only when the server set is complete and every
// server answered: with a server missing it is incomplete, not churn, and the keys row fails with the init hint after
// one read.
func TestDoctorKeysIncompleteServerSetWithShiftingPagesIsNotChurn(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	fake := fakeShiftingConnz(t, xKey)
	// The scan itself: one server expected (complete) is unsettled; two expected (one answered) is not.
	sc, err := scanConnections(context.Background(), fake, "A", 1)
	if err != nil || !sc.Unsettled || !strings.Contains(sc.Incomplete, "kept changing") {
		t.Fatalf("complete set: %v %+v", err, sc)
	}
	sc, err = scanConnections(context.Background(), fake, "A", 2)
	if err != nil || sc.Unsettled || sc.Incomplete == "" || !strings.Contains(sc.Incomplete, "kept changing") {
		t.Fatalf("incomplete set: %v %+v", err, sc)
	}
	// Through doctor: the same incomplete scan fails, once, with the init hint.
	oldScan, oldPause := scanConns, churnPause
	t.Cleanup(func() { scanConns, churnPause = oldScan, oldPause })
	churnPause = 0
	reads := 0
	scanConns = func(ctx context.Context, _ *nats.Conn, account string, _ int) (connScan, error) {
		reads++
		return scanConnections(ctx, fake, account, 2)
	}
	_, by, code := doctorJSON(t, e.run)
	k := by["keys"]
	if code != 1 || k.State != "fail" || reads != 1 || !strings.Contains(k.Detail, "kept changing") || k.Fix == nil || *k.Fix != "mashctl init --config <file>" {
		t.Fatalf("%d reads %d: %+v", code, reads, k)
	}
}

// The deadline arriving during the pause between reads stops the loop: the row is decided from the reads so far.
func TestDoctorKeysDeadlineDuringPauseBetweenReads(t *testing.T) {
	e := newXYEnv(t)
	oldScan, oldPause, oldDeadline := scanConns, churnPause, applyDeadline
	t.Cleanup(func() { scanConns, churnPause, applyDeadline = oldScan, oldPause, oldDeadline })
	churnPause = time.Minute // far longer than the deadline
	applyDeadline = 200 * time.Millisecond
	calls := 0
	scanConns = func(context.Context, *nats.Conn, string, int) (connScan, error) {
		calls++
		return keyScan(true), nil
	}
	start := time.Now()
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 0 || c.State != "warn" || !strings.HasPrefix(c.Detail, "mashctl's users kept connecting and leaving; read once") || calls != 1 {
		t.Fatalf("%d calls %d %+v", code, calls, c)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatalf("the pause was not cut short: %v", time.Since(start))
	}
	calls = 0
	_, by, code = doctorJSON(t, e.run, "--audit-users")
	if c := by["keys"]; code != 1 || c.State != "fail" || c.Detail != "server s1's connected users kept changing\nread once; wait a minute and run it again" || calls != 1 {
		t.Fatalf("%d calls %d %+v", code, calls, c)
	}
}

// fakeShiftingConnz starts a bare nats-server whose system subjects answer like one real server "s1": the server ping,
// and CONNZ with two connections to a page and four in all. In every read (one CONNZ ping starts a read) the second
// page repeats the foreign connection of the first, so the pages never reconcile, and the foreign connection's id
// differs from one read to the next. The recorded key's connection (id 1) is the same in every read.
func fakeShiftingConnz(t *testing.T, recorded string) *nats.Conn {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Port: -1, NoLog: true, NoSigs: true, NoSystemAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL(), nats.Timeout(15*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	var mu sync.Mutex
	read := 0
	srvInfo := map[string]any{"name": "s1", "id": "SID"}
	reply := func(offset int) []byte {
		mu.Lock()
		foreign := uint64(100 * read)
		mu.Unlock()
		conn := func(key string, cid uint64) map[string]any {
			return map[string]any{"authorized_user": key, "account": "A", "cid": cid}
		}
		conns := []map[string]any{conn(recorded, 1), conn("UFOREIGN", foreign)}
		if offset > 0 {
			conns = []map[string]any{conn("UFOREIGN", foreign), conn("UOTHER", foreign+1)} // the foreign one again
		}
		b, _ := json.Marshal(map[string]any{"server": srvInfo,
			"data": map[string]any{"total": 4, "offset": offset, "connections": conns}})
		return b
	}
	subs := []struct {
		subject string
		h       nats.MsgHandler
	}{
		{"$SYS.REQ.SERVER.PING", func(m *nats.Msg) {
			b, _ := json.Marshal(map[string]any{"server": srvInfo})
			_ = m.Respond(b)
		}},
		{"$SYS.REQ.SERVER.PING.CONNZ", func(m *nats.Msg) {
			mu.Lock()
			read++
			mu.Unlock()
			_ = m.Respond(reply(0))
		}},
		{"$SYS.REQ.SERVER.SID.CONNZ", func(m *nats.Msg) {
			var req struct {
				Offset int `json:"offset"`
			}
			_ = json.Unmarshal(m.Data, &req)
			_ = m.Respond(reply(req.Offset))
		}},
	}
	for _, s := range subs {
		if _, err := nc.Subscribe(s.subject, s.h); err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return nc
}

// Foreign churn found by the real scan: the pages of one server really shift (so scanConnections itself marks the
// read unsettled), the foreign connection's id changes between reads, and the recorded key is steady. The row is ok.
func TestDoctorKeysRealUnsettledScanOfForeignChurnIsOK(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	fake := fakeShiftingConnz(t, xKey)
	oldScan, oldPause := scanConns, churnPause
	t.Cleanup(func() { scanConns, churnPause = oldScan, oldPause })
	churnPause = 0
	reads := 0
	scanConns = func(ctx context.Context, _ *nats.Conn, account string, servers int) (connScan, error) {
		sc, err := scanConnections(ctx, fake, account, servers)
		if err == nil {
			reads++
			if !sc.Unsettled || !strings.Contains(sc.Incomplete, "kept changing") {
				t.Errorf("read %d not unsettled by the real scan: %+v", reads, sc)
			}
		}
		return sc, err
	}
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 0 || c.State != "ok" || reads < 2 {
		t.Fatalf("%d reads, exit %d: %+v", reads, code, c)
	}
}

// A long word of multibyte characters is split between characters: every line is valid UTF-8 within the width, and the
// text is kept whole and in order.
func TestIncompleteLinesSplitsBetweenCharacters(t *testing.T) {
	word := strings.Repeat("é世🙂", 50) // 150 characters, 2, 3 and 4 bytes each
	in := "server " + word + " did not reply\nsecond " + strings.Repeat("世", 64)
	got := incompleteLines(in)
	var all strings.Builder
	for _, l := range strings.Split(got, "\n") {
		if !utf8.ValidString(l) {
			t.Fatalf("invalid UTF-8: %q", l)
		}
		if n := utf8.RuneCountInString(l); n > 80 || n > detailWidth {
			t.Fatalf("%d columns: %q", n, l)
		}
		all.WriteString(strings.ReplaceAll(l, " ", ""))
	}
	if want := strings.ReplaceAll(strings.ReplaceAll(in, " ", ""), "\n", ""); all.String() != want {
		t.Fatalf("text changed: %q", got)
	}
}

// Widths are terminal cells, not characters: words of wide (CJK, fullwidth, emoji) characters wrap so no line is wider
// than the detail's 63 cells, and the text is kept whole and in order.
func TestIncompleteLinesCountsCells(t *testing.T) {
	in := "server " + strings.Repeat("世界", 20) + " did not reply " + strings.Repeat("界", 10) + " " +
		strings.Repeat("🙂", 40) + " " + strings.Repeat("🚀", 24) + " " + strings.Repeat("ａ", 70) + "\nnext " + strings.Repeat("한국어 ", 30)
	got := incompleteLines(in)
	var all strings.Builder
	for _, l := range strings.Split(got, "\n") {
		if n := cellwidth.String(l); n > detailWidth {
			t.Fatalf("%d cells: %q", n, l)
		}
		all.WriteString(strings.ReplaceAll(l, " ", ""))
	}
	if want := strings.ReplaceAll(strings.ReplaceAll(in, " ", ""), "\n", ""); all.String() != want {
		t.Fatalf("text changed: %q", got)
	}
}

// A page request's own timeout is not doctor's deadline: with the doctor context still alive, the error fails the row
// even after an earlier complete read, with or without --audit-users.
func TestDoctorKeysPageTimeoutIsNotTheDeadline(t *testing.T) {
	e := newXYEnv(t)
	oldScan, oldPause := scanConns, churnPause
	t.Cleanup(func() { scanConns, churnPause = oldScan, oldPause })
	churnPause = 0
	for _, args := range [][]string{{}, {"--audit-users"}} {
		calls := 0
		scanConns = func(context.Context, *nats.Conn, string, int) (connScan, error) {
			calls++
			if calls > 1 {
				return connScan{}, fmt.Errorf("server s1: the next CONNZ page: %w", context.DeadlineExceeded)
			}
			return keyScan(true), nil
		}
		_, by, code := doctorJSON(t, e.run, args...)
		if c := by["keys"]; code != 1 || c.State != "fail" || !strings.Contains(c.Detail, "the next CONNZ page") {
			t.Fatalf("%v: %d %+v", args, code, c)
		}
	}
}

// A connection of mashctl's own that changed between a server's inner page scans is churn, though the reads agree.
func TestDoctorKeysInnerScanChangeOfMashUserWarns(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	sc := keyScan(true, xKey)
	sc.Conns[0].Changed = []apply.ConnzConn{{Key: xKey, CID: 7}}
	scriptScans(t, sc)
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 0 || c.State != "warn" || !strings.HasPrefix(c.Detail, "mashctl's users kept connecting and leaving") {
		t.Fatalf("%d %+v", code, c)
	}
	// The same change of a foreign key is not a finding.
	f := keyScan(true, "UFOREIGN1")
	f.Conns[0].Changed = []apply.ConnzConn{{Key: "UFOREIGN1", CID: 7}}
	scriptScans(t, f, f, f)
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" {
		t.Fatalf("%d %+v", code, by["keys"])
	}
}

// A revoked key seen only in some of a server's page scans (its Changed list) still fails; a foreign key there does not.
func TestDoctorKeysRevokedOnlyInChangedFails(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	sc := keyScan(false)
	sc.Conns[0].Changed = []apply.ConnzConn{{Key: xKey, CID: 7}}
	scriptScans(t, sc)
	_, by, code := doctorJSON(t, e.run)
	if c := by["keys"]; code != 1 || c.State != "fail" || !strings.HasPrefix(c.Detail, "1 revoked key is still connected") {
		t.Fatalf("%d %+v", code, c)
	}
	f := keyScan(false)
	f.Conns[0].Changed = []apply.ConnzConn{{Key: "UFOREIGN1", CID: 7}}
	scriptScans(t, f)
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" {
		t.Fatalf("%d %+v", code, by["keys"])
	}
}
