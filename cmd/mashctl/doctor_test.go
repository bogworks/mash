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
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
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
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       ok    every connected key was issued here\n\nNo problems.\n"
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
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  FAIL  1 inbox has no endpoint record: alice.g.ghost\nkeys       ok    every connected key was issued here\n\n1 problem.\n"
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

// Wrong command lines are usage errors that attempt nothing; the removed verbs are unknown commands.
func TestDoctorUsageAndRemovedVerbs(t *testing.T) {
	f := newAuditEnv(t)
	for args, want := range map[string]string{
		"doctor --x":        "error: unknown option --x\nhint: mashctl doctor --help\n",
		"doctor --dry-run":  "error: unknown option --dry-run\nhint: mashctl doctor --help\n",
		"doctor extra":      "error: too many arguments\nhint: mashctl doctor --help\n",
		"audit":             "error: unknown command \"audit\"\nhint: mashctl --help\n",
		"issued check":      "error: unknown command \"issued\"\nhint: mashctl --help\n",
		"issued list":       "error: unknown command \"issued\"\nhint: mashctl --help\n",
		"audit --creds-dir": "error: unknown command \"audit\"\nhint: mashctl --help\n",
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

// Keys: a key signed outside mashctl is connected and mash did not issue it.
func TestDoctorKeysReportsAKeyMashDidNotIssue(t *testing.T) {
	e := newXYEnv(t)
	rogue := e.connect(e.mintRogue("rogue"), nats.Name("rogue-client"))
	if !rogue.IsConnected() {
		t.Fatal("the rogue did not connect")
	}
	out, errb, code := e.run("doctor")
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       FAIL  1 connected key was not issued here:\n                 KEY\n\n1 problem.\n"
	if code != 1 || userKeyRE.ReplaceAllString(cleanText(e, out), "KEY") != want || errb != "hint: if another tool issued it, list it in config.json external_keys; otherwise revoke it\n" {
		t.Fatalf("exit %d\n%q\nstderr %q\nwant\n%q", code, cleanText(e, out), errb, want)
	}
	rogue.Close()
	time.Sleep(200 * time.Millisecond)
	if _, by, code := doctorJSON(t, e.run); code != 0 || by["keys"].State != "ok" {
		t.Fatalf("after the rogue left: %d %+v", code, by["keys"])
	}
}

// The admin key this command connects with is not exempt: unrecorded, it is reported; once recorded it is clean.
func TestDoctorKeysHasNoExemptionForTheCommandsOwnAdmin(t *testing.T) {
	e := newXYEnv(t)
	path, _ := mintUnrecordedAdmin(t, e)
	e.env["MASH_ADMIN_CREDS"] = path
	if _, by, code := doctorJSON(t, e.run); code != 1 || !strings.HasPrefix(by["keys"].Detail, "1 connected key was not issued here:\nU") {
		t.Fatalf("an unrecorded admin was not reported: %d %+v", code, by["keys"])
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
	if c := by["keys"]; !strings.HasPrefix(c.Detail, "1 server(s) replied") || !strings.Contains(c.Detail, "; 1 connected key was not issued here:\nU") {
		t.Fatalf("%+v", c)
	}
	e.servers(1)
	if _, by, code := doctorJSON(t, e.run); by["keys"].State != "fail" || code != 1 { // the rogue is still connected
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

// setExternal writes config.json with the system creds and the given raw external_keys value.
func (e *applyEnv) setExternal(raw string) {
	e.t.Helper()
	e.writeConfig(`{"system_creds":"` + e.sysCreds + `","external_keys":` + raw + `}`)
}

var userKeyRE = regexp.MustCompile(`U[A-Z2-7]{55}`)

const keysOKLine = "keys       ok    every connected key was issued here"

// A listed connected key passes and is counted; an unlisted one still fails, with and without --strict; an empty or
// absent list changes nothing.
func TestDoctorKeysExternalKeys(t *testing.T) {
	e := newXYEnv(t)
	listed := rogueKey(t, e, "tool-a")

	// absent and empty: today's failure, byte for byte
	failFor := func(key string) string {
		return "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       FAIL  1 connected key was not issued here:\n                 " + key + "\n\n1 problem.\n"
	}
	const unlistedHint = "hint: if another tool issued it, list it in config.json external_keys; otherwise revoke it\n"
	failWant := failFor(listed)
	for _, raw := range []string{"", "null", "[]"} {
		if raw != "" {
			e.setExternal(raw)
		}
		out, errb, code := e.run("doctor")
		if code != 1 || cleanText(e, out) != failWant || errb != unlistedHint {
			t.Fatalf("external_keys %q: exit %d\n%q\nstderr %q", raw, code, cleanText(e, out), errb)
		}
	}

	e.setExternal(`["` + listed + `"]`)
	out, errb, code := e.run("doctor")
	want := "config     ok\nserver     ok    tls://localhost:4222\nsign-in    ok\nstorage    ok\nendpoints  ok    2 records, 2 inboxes\nkeys       ok    every connected key was issued here or listed (1 external)\n\nNo problems.\n"
	if code != 0 || cleanText(e, out) != want || errb != "" {
		t.Fatalf("listed: exit %d\n%q\nstderr %q", code, cleanText(e, out), errb)
	}
	if _, errb, code := e.run("doctor", "--strict"); code != 0 {
		t.Fatalf("listed, strict: %d %q", code, errb)
	}

	// a second, unlisted key: the failure stays, the listed one is not counted as a failure
	unlisted := rogueKey(t, e, "tool-b")
	failWant = failFor(unlisted)
	for _, args := range [][]string{{"doctor"}, {"doctor", "--strict"}} {
		out, errb, code := e.run(args...)
		if code != 1 || cleanText(e, out) != failWant || errb != unlistedHint {
			t.Fatalf("%v: exit %d\n%q\nstderr %q", args, code, cleanText(e, out), errb)
		}
	}
	if _, by, code := doctorJSON(t, e.run); code != 1 || by["keys"].State != "fail" || by["keys"].Detail != "1 connected key was not issued here:\n"+unlisted {
		t.Fatalf("json: %d %+v", code, by["keys"])
	}
}

// The issue record wins: a revoked key that is still connected fails even when it is also listed.
func TestDoctorKeysExternalKeysDoNotHideARevokedKey(t *testing.T) {
	e := newXYEnv(t)
	e.connect(natsauth.UserCredentials(e.credsPath(xy("x").String())), nats.Name("x-client"))
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	adm, ctx, closeAdm := e.admin()
	defer closeAdm()
	if err := adm.MarkRevoked(ctx, xKey, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	e.setExternal(`["` + xKey + `"]`)
	if _, by, code := doctorJSON(t, e.run); code != 1 || by["keys"].State != "fail" || by["keys"].Detail != "1 revoked key is still connected" {
		t.Fatalf("%d %+v", code, by["keys"])
	}
}

// A malformed entry is a configuration error that names the entry.
func TestDoctorExternalKeysMalformedIsAConfigError(t *testing.T) {
	e := newXYEnv(t)
	acct := e.acctPub
	for raw, named := range map[string]string{
		`"` + acct + `"`:   "external_keys",
		`[42]`:             "entry 1 (not shown)",
		`["` + acct + `"]`: "entry 1 (not shown)",
		`["garbage"]`:      "entry 1 (not shown)",
		`["*"]`:            "entry 1 (not shown)",
		`[""]`:             "entry 1 (not shown)",
	} {
		e.setExternal(raw)
		_, by, code := doctorJSON(t, e.run)
		c := by["config"]
		if code != 1 || c.State != "fail" || !strings.Contains(c.Detail, "external_keys") || !strings.Contains(c.Detail, named) {
			t.Errorf("%s: %d %+v", raw, code, c)
		}
	}
}

// A key issued here and also listed stays issued: it is not counted as external.
func TestDoctorKeysIssuedAndListedIsNotExternal(t *testing.T) {
	e := newXYEnv(t)
	_, xKey := credsSubject(t, e.credsPath(xy("x").String()))
	e.connect(natsauth.UserCredentials(e.credsPath(xy("x").String())), nats.Name("x-client"))
	e.setExternal(`["` + xKey + `"]`)
	out, errb, code := e.run("doctor")
	if code != 0 || errb != "" || !strings.Contains(cleanText(e, out), keysOKLine+"\n") || strings.Contains(out, "external") {
		t.Fatalf("exit %d\n%q\nstderr %q", code, cleanText(e, out), errb)
	}
}

// An operator key, an account key, a seed, an entry with a line break and a repeated entry are each refused; a seed is
// never printed, in the config line or in --json.
func TestDoctorExternalKeysRefusedEntries(t *testing.T) {
	e := newXYEnv(t)
	op, _ := nkeys.CreateOperator()
	opub, _ := op.PublicKey()
	u, _ := nkeys.CreateUser()
	upub, _ := u.PublicKey()
	seed, _ := u.Seed()
	for name, raw := range map[string]string{
		"operator":   `["` + opub + `"]`,
		"account":    `["` + e.acctPub + `"]`,
		"seed":       `["` + string(seed) + `"]`,
		"newline":    `["` + upub + `\n"]`,
		"whitespace": `[" ` + upub + `"]`,
		"duplicate":  `["` + upub + `","` + upub + `"]`,
	} {
		e.setExternal(raw)
		out, errb, code := e.run("doctor", "--json")
		var rep doctorReport
		if code != 1 || json.Unmarshal([]byte(out), &rep) != nil {
			t.Errorf("%s: exit %d %q", name, code, out)
			continue
		}
		for _, c := range rep.Checks {
			if c.Name == "config" && (c.State != "fail" || !strings.Contains(c.Detail, "external_keys")) {
				t.Errorf("%s: %+v", name, c)
			}
		}
		if strings.Contains(out+errb, string(seed)) || strings.Contains(out+errb, opub) || strings.Contains(out+errb, e.acctPub) {
			t.Errorf("%s: a key that must not be echoed was printed: %q %q", name, out, errb)
		}
	}
}

// Five unlisted keys: three whole keys and "and 2 more", each on its own line, every line of the human output within
// 80 columns; --json keeps the keys whole in detail.
func TestDoctorKeysManyUnlistedKeysOnLinesOfTheirOwn(t *testing.T) {
	e := newXYEnv(t)
	for i := 0; i < 5; i++ {
		rogueKey(t, e, fmt.Sprintf("tool-%d", i))
	}
	out, _, code := e.run("doctor")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	text := cleanText(e, out)
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		if len(l) > 80 {
			t.Errorf("%d columns: %q", len(l), l)
		}
	}
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "keys ") })
	if i < 0 || len(lines) < i+5 || lines[i] != "keys       FAIL  5 connected keys were not issued here:" || lines[i+4] != strings.Repeat(" ", 17)+"and 2 more" {
		t.Fatalf("%q", text)
	}
	for _, l := range lines[i+1 : i+4] {
		if !regexp.MustCompile(`^ {17}U[A-Z2-7]{55}$`).MatchString(l) {
			t.Errorf("not an indented key: %q", l)
		}
	}
	_, by, _ := doctorJSON(t, e.run)
	dl := strings.Split(by["keys"].Detail, "\n")
	if len(dl) != 5 || dl[0] != "5 connected keys were not issued here:" || dl[4] != "and 2 more" {
		t.Fatalf("%q", by["keys"].Detail)
	}
	for _, k := range dl[1:4] {
		if !userKeyRE.MatchString(k) || len(k) != 56 {
			t.Errorf("key not whole: %q", k)
		}
	}
}
