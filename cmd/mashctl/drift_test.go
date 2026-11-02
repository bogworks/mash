package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/store"
)

// driftBase declares no endpoint: `mashctl add` makes x and y, so their records, creds and zone keys are real.
const driftBase = `default: deny
rules:
  - {name: a-to-b, action: allow, from: {zone: a}, to: {zone: b}}
`

// newDriftEnv is a bootstrap server with the directory made, endpoints x (zone a, label team=red) and y (zone b)
// added with `mashctl add` and the policy applied: the clean state.
func newDriftEnv(t *testing.T) *applyEnv {
	t.Helper()
	e := newApplyEnv(t, driftBase)
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env["MASH_INSECURE_LOOPBACK"] = "1"
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	for _, args := range [][]string{{"add", "x", "--zone", "a", "--label", "team=red"}, {"add", "y", "--zone", "b"}, {"policy", "apply"}} {
		if out, errb, code := e.run(args...); code != 0 {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	return e
}

func (e *applyEnv) credsPath(name string) string { return filepath.Join(e.root, "cfg", name+".creds") }

// admin opens the directory admin; the returned func closes it.
func (e *applyEnv) admin() (*admin.Admin, context.Context, func()) {
	e.t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.Creds))
	if err != nil {
		e.t.Fatal(err)
	}
	a, err := admin.New(nc, "alice")
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	return a, ctx, func() { cancel(); nc.Close() }
}

// pushClaims rewrites the account JWT on the resolver with mutate, signed by the operator key, as another tool would.
func (e *applyEnv) pushClaims(mutate func(*jwt.AccountClaims)) {
	e.t.Helper()
	ac := e.claims()
	mutate(ac)
	op, _, err := e.ks.Operator()
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := ac.Encode(op)
	if err != nil {
		e.t.Fatal(err)
	}
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	acks, err := apply.NATS{Conn: nc}.Push(context.Background(), tok, nil)
	if err != nil || len(acks) == 0 || !acks[0].OK() {
		e.t.Fatalf("push: %v %+v", err, acks)
	}
}

// drift runs `policy drift --json` and returns the report and the exit code.
func (e *applyEnv) drift(extra ...string) (driftReport, int) {
	e.t.Helper()
	out, errb, code := e.run(append([]string{"policy", "drift", "--json"}, extra...)...)
	var r driftReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		e.t.Fatalf("drift --json: %v\n%s\n%s", err, out, errb)
	}
	e.noSeeds(out + errb)
	return r, code
}

func kinds(s stateReport) []string {
	var out []string
	for _, f := range s.Findings {
		out = append(out, f.Kind+" "+f.Subject)
	}
	return out
}

func wantState(t *testing.T, name string, s stateReport, status string, findings ...string) {
	t.Helper()
	if s.Status != status || !slices.Equal(kinds(s), findings) && !(len(findings) == 0 && len(s.Findings) == 0) {
		t.Errorf("%s: status %s findings %q, want %s %q (error %q)", name, s.Status, kinds(s), status, findings, s.Error)
	}
}

func TestDriftCleanStateExitsZero(t *testing.T) {
	e := newDriftEnv(t)
	out, errb, code := e.run("policy", "drift")
	if code != 0 || errb != "" {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	for _, w := range []string{"account: ok", "keys: ok", "directory: ok", "issued: ok", "Not covered by drift:", "nsc", "no drift"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	e.noSeeds(out)
	r, code := e.drift()
	if code != 0 || r.Drift {
		t.Fatalf("json: %d %+v", code, r)
	}
	for name, s := range map[string]stateReport{"account": r.Account, "keys": r.Keys, "directory": r.Directory, "issued": r.Issued} {
		wantState(t, name, s, statusOK)
	}
}

func TestDriftJSONShapeIsStable(t *testing.T) {
	e := newDriftEnv(t)
	out, _, code := e.run("policy", "drift", "--json")
	if code != 0 {
		t.Fatal(out)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"account", "directory", "drift", "issued", "keys", "not_covered", "policy"}; !slices.Equal(keys, want) {
		t.Fatalf("top-level keys %v, want %v", keys, want)
	}
	var acct map[string]json.RawMessage
	if err := json.Unmarshal(m["account"], &acct); err != nil {
		t.Fatal(err)
	}
	if string(acct["status"]) != `"ok"` || string(acct["findings"]) != `[]` {
		t.Fatalf("account: %s", m["account"])
	}
	// a drifting state carries kind, subject and detail per finding
	if err := os.Remove(filepath.Join(e.keyDir, "zones", "b.nk")); err != nil {
		t.Fatal(err)
	}
	out, _, code = e.run("policy", "drift", "--json")
	if code != 1 || !strings.Contains(out, `"kind": "key-file-missing"`) || !strings.Contains(out, `"subject": "b"`) || !strings.Contains(out, `"drift": true`) {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestDriftAccountSignerRemovedServerSide(t *testing.T) {
	e := newDriftEnv(t)
	bpub, _ := e.ks.ZonePublic("b")
	e.pushClaims(func(ac *jwt.AccountClaims) { delete(ac.SigningKeys, bpub) })
	r, code := e.drift()
	if code != 1 || !r.Drift {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "account", r.Account, statusDrift, "zone-missing-on-server b")
	wantState(t, "keys", r.Keys, statusOK)
	wantState(t, "directory", r.Directory, statusOK)
	wantState(t, "issued", r.Issued, statusOK)
	if !strings.Contains(strings.Join(r.Account.Findings[0].Lines, "\n"), "pub allow") {
		t.Errorf("no permission lines: %+v", r.Account.Findings[0])
	}
	// a changed template, and a zone the file lacks, are findings too
	e = newDriftEnv(t)
	apub, _ := e.ks.ZonePublic("a")
	extra, err := e.ks.ZoneKey("stray", true) // a zone key of the key store whose zone the file lacks
	if err != nil {
		t.Fatal(err)
	}
	epub, _ := extra.PublicKey()
	e.pushClaims(func(ac *jwt.AccountClaims) {
		sc, _ := ac.SigningKeys.GetScope(apub)
		us := sc.(*jwt.UserScope)
		us.Template.Pub.Allow.Add("agents.mail.*.alice.zzz.*")
		ac.SigningKeys[apub] = us
		stray := jwt.NewUserScope()
		stray.Key, stray.Role = epub, "stray"
		ac.SigningKeys[epub] = stray
	})
	r, code = e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "account", r.Account, statusDrift, "zone-not-in-file stray", "zone-template-differs a")
	if !strings.Contains(strings.Join(r.Account.Findings[1].Lines, "\n"), "- agents.mail.*.alice.zzz.*") {
		t.Errorf("template diff lines: %+v", r.Account.Findings[1])
	}
}

func TestDriftZoneKeyFileDeleted(t *testing.T) {
	e := newDriftEnv(t)
	if err := os.Remove(filepath.Join(e.keyDir, "zones", "b.nk")); err != nil {
		t.Fatal(err)
	}
	r, code := e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "keys", r.Keys, statusDrift, "key-file-missing b")
	wantState(t, "directory", r.Directory, statusOK)
	// The same fact seen from the other two states: apply would refuse (the server's key is not the key store's, which
	// has none), and y's creds cannot be checked against a zone key that is gone.
	wantState(t, "account", r.Account, statusDrift, "zone-key-differs b")
	wantState(t, "issued", r.Issued, statusDrift, "creds-unreadable y")

	// A key file for a zone the account has no signer for, and a different key under a known zone.
	e = newDriftEnv(t)
	if _, err := e.ks.ZoneKey("leftover", true); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "keys", r.Keys, statusDrift, "key-file-extra leftover")
	wantState(t, "account", r.Account, statusOK)

	e = newDriftEnv(t)
	other, _ := nkeys.CreateAccount()
	seed, _ := other.Seed()
	if err := os.WriteFile(filepath.Join(e.keyDir, "zones", "b.nk"), append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "keys", r.Keys, statusDrift, "key-differs b")
}

func TestDriftDirectoryFindings(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()

	// a label changed on the record
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatal(err)
	}
	r, code := e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "directory", r.Directory, statusDrift, "label-differs x")
	if !strings.Contains(r.Directory.Findings[0].Detail, `label team is "blue" on the record, "red" in the file`) {
		t.Errorf("detail: %s", r.Directory.Findings[0].Detail)
	}
	wantState(t, "account", r.Account, statusOK)
	wantState(t, "keys", r.Keys, statusOK)
	wantState(t, "issued", r.Issued, statusOK)

	// an extra record, and a record with no zone
	recs, err := adm.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var xrec = recs[0]
	for _, rec := range recs {
		if rec.Name == "y" {
			xrec = rec
		}
	}
	if err := adm.Deprovision(ctx, "y"); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Provision(ctx, "y", xrec.User, xrec.Account, "", admin.Meta{}); err != nil { // legacy: no zone
		t.Fatal(err)
	}
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, xrec.Account, "", admin.Meta{Zone: "a"}); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "directory", r.Directory, statusDrift, "record-extra ghost", "label-differs x", "label-differs y", "zone-unset y")

	// a record the file declares and the directory lacks
	if err := adm.Deprovision(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "directory", r.Directory, statusDrift, "record-extra ghost", "record-missing x", "label-differs y", "zone-unset y")
}

// staleCreds rewrites name's creds file with a JWT signed by signer carrying tags, on the same user key.
func (e *applyEnv) rewriteCreds(name string, signer nkeys.KeyPair, tags ...string) {
	e.t.Helper()
	raw, err := os.ReadFile(e.credsPath(name))
	if err != nil {
		e.t.Fatal(err)
	}
	ukp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		e.t.Fatal(err)
	}
	upub, _ := ukp.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name, uc.IssuerAccount = name, e.acctPub
	uc.UserPermissionLimits = jwt.UserPermissionLimits{}
	uc.Tags.Add(tags...)
	tok, err := uc.Encode(signer)
	if err != nil {
		e.t.Fatal(err)
	}
	seed, _ := ukp.Seed()
	out, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.credsPath(name), out, 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func TestDriftIssuedCredsStale(t *testing.T) {
	e := newDriftEnv(t)
	zkb, _ := e.ks.ZoneKey("b", false)
	e.rewriteCreds("y", zkb, "allow:a.x") // right signer, a tag the file no longer gives
	r, code := e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale y")
	if d := r.Issued.Findings[0].Detail; !strings.Contains(d, "tags to remove: allow:a.x") {
		t.Errorf("detail: %s", d)
	}
	wantState(t, "account", r.Account, statusOK)
	wantState(t, "keys", r.Keys, statusOK)
	wantState(t, "directory", r.Directory, statusOK)

	// the same compare as `reissue --changed` finds it, and a signer that is not the zone key is named
	if out, _, _ := e.run("reissue", "--changed"); !strings.Contains(out, "reissued y") {
		t.Fatalf("reissue --changed did not pick up y: %s", out)
	}
	rogue, _ := nkeys.CreateAccount()
	e.rewriteCreds("x", rogue)
	r, _ = e.drift()
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale x")
	if d := r.Issued.Findings[0].Detail; !strings.Contains(d, "signed by ") || !strings.Contains(d, "not the zone key") {
		t.Errorf("detail: %s", d)
	}

	// --creds-dir: a copy in another directory is what is compared; a missing file is a note, not drift
	other := t.TempDir()
	r, code = e.drift("--creds-dir", other)
	if code != 0 || r.Issued.Status != statusOK || len(r.Issued.Notes) != 2 {
		t.Fatalf("creds-dir: %d %+v", code, r.Issued)
	}
	raw, _ := os.ReadFile(e.credsPath("x"))
	if err := os.WriteFile(filepath.Join(other, "x.creds"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift("--creds-dir", other)
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale x")
}

func TestDriftUnreadableStateIsUnknownAndNotOK(t *testing.T) {
	e := newDriftEnv(t)
	e.env["MASH_SYS_CREDS"] = filepath.Join(e.root, "none.creds")
	r, code := e.drift()
	if code != 1 || r.Account.Status != statusUnknown || r.Keys.Status != statusUnknown || r.Directory.Status != statusOK || r.Issued.Status != statusOK {
		t.Fatalf("%d %+v", code, r)
	}
	if out, _, code := e.run("policy", "drift", "--nope"); code != 2 || out != "" {
		t.Fatalf("usage: %d %q", code, out)
	}
	if _, _, code := e.run("policy", "drift", "--creds-dir"); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}

func TestRepairFixesAccountKeysAndDirectoryAndListsIssued(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()

	// (a) the account lost y's signer, (b) y's zone key file is gone, (c) x's label changed and y has no zone.
	bpub, _ := e.ks.ZonePublic("b")
	e.pushClaims(func(ac *jwt.AccountClaims) { delete(ac.SigningKeys, bpub) })
	if err := os.Remove(filepath.Join(e.keyDir, "zones", "b.nk")); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue", "stray": "yes"}}); err != nil {
		t.Fatal(err)
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "y" {
			if err := adm.Deprovision(ctx, "y"); err != nil {
				t.Fatal(err)
			}
			if _, err := adm.Provision(ctx, "y", rec.User, rec.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	xBefore, _ := os.ReadFile(e.credsPath("x"))
	if _, code := e.drift(); code != 1 {
		t.Fatal("no drift before repair")
	}

	out, errb, code := e.run("policy", "repair")
	if code != 0 || errb != "" {
		t.Fatalf("repair: %d\n%s\n%s", code, out, errb)
	}
	e.noSeeds(out + errb)
	for _, w := range []string{
		"step 1, zone key files: created b",
		"pushed:",
		"set zone y b",
		"clear label x stray=yes",
		"set label x team=red (was blue)",
		"repair is additive EXCEPT that it clears every label the file does not name",
		"y is stale: signed by",
		"not executed; to reissue them run: mashctl reissue y",
		"not a transaction",
		"never revokes",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("repair output lacks %q:\n%s", w, out)
		}
	}
	if after, _ := os.ReadFile(e.credsPath("x")); string(after) != string(xBefore) {
		t.Fatal("repair changed a creds file")
	}

	// drift after repair: clean except the issued state (y's creds are signed by the lost key)
	r, code := e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "account", r.Account, statusOK)
	wantState(t, "keys", r.Keys, statusOK)
	wantState(t, "directory", r.Directory, statusOK)
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale y")

	// a second repair does nothing
	jwtNow := e.accountJWT()
	out, errb, code = e.run("policy", "repair")
	if code != 0 || errb != "" {
		t.Fatalf("second repair: %d\n%s\n%s", code, out, errb)
	}
	for _, w := range []string{"step 1, zone key files: nothing to do", "already up to date", "step 3, directory records:\n  nothing to do"} {
		if !strings.Contains(out, w) {
			t.Errorf("second repair lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "set zone") || strings.Contains(out, "set label") || strings.Contains(out, "clear label") {
		t.Errorf("second repair changed a record:\n%s", out)
	}
	if e.accountJWT() != jwtNow {
		t.Fatal("the second repair changed the account JWT")
	}

	// the listed command does fix (d)
	if out, errb, code := e.run("reissue", "y"); code != 0 {
		t.Fatalf("%s %s", out, errb)
	}
	if r, code := e.drift(); code != 0 || r.Drift {
		t.Fatalf("after reissue: %d %+v", code, r)
	}
}

func TestRepairReportsWhatItNeverDoes(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, e.acctPub, "", admin.Meta{Zone: "a"}); err != nil {
		t.Fatal(err)
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "x" { // x moved to another zone on record: never moved back by repair
			if err := adm.Deprovision(ctx, "x"); err != nil {
				t.Fatal(err)
			}
			if _, err := adm.Provision(ctx, "x", rec.User, rec.Account, "", admin.Meta{Zone: "b", Labels: rec.Labels}); err != nil {
				t.Fatal(err)
			}
		}
	}
	out, _, code := e.run("policy", "repair")
	if code != 0 || !strings.Contains(out, "reported, not repaired") || !strings.Contains(out, "record-extra ghost") || !strings.Contains(out, "zone-differs x") {
		t.Fatalf("%d\n%s", code, out)
	}
	recs, _ = adm.List(ctx)
	names := map[string]string{}
	for _, rec := range recs {
		names[rec.Name] = rec.Zone
	}
	if names["ghost"] != "a" || names["x"] != "b" {
		t.Fatalf("repair changed or deleted records: %v", names)
	}
}

func TestRepairStopsWhenApplyRefusesAndDoesNothingDestructive(t *testing.T) {
	e := newDriftEnv(t)
	// zone b leaves the file: apply refuses (a zone on the server the file lacks) unless acknowledged
	if err := os.WriteFile(e.policy, []byte("default: deny\nendpoints:\n  x: {labels: {zone: a}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := e.accountJWT()
	out, _, code := e.run("policy", "repair")
	if code != 1 || !strings.Contains(out, "REFUSED unless --remove-zone b or --keep-zone b") || !strings.Contains(out, "repair stopped at step 2") || strings.Contains(out, "step 3") {
		t.Fatalf("%d\n%s", code, out)
	}
	if e.accountJWT() != before {
		t.Fatal("a refused repair changed the account")
	}
	// with --keep-zone the same flag set as apply goes through
	out, _, code = e.run("policy", "repair", "--keep-zone", "b")
	if code != 0 || !strings.Contains(out, "step 3") {
		t.Fatalf("%d\n%s", code, out)
	}
	for _, args := range [][]string{{"--servers", "0"}, {"--bogus"}} {
		if _, _, code := e.run(append([]string{"policy", "repair"}, args...)...); code != 2 {
			t.Fatalf("repair %v: exit %d, want 2", args, code)
		}
	}
}

func TestRepairRejectsRemoveZoneAndReplaceZoneKey(t *testing.T) {
	e := newDriftEnv(t)
	before := e.accountJWT()
	for _, args := range [][]string{{"--remove-zone", "b"}, {"--replace-zone-key=b"}, {"--keep-zone", "b", "--remove-zone", "c"}} {
		out, errb, code := e.run(append([]string{"policy", "repair"}, args...)...)
		if code != 2 || out != "" || !strings.Contains(errb, "mashctl policy apply") || !strings.Contains(errb, "stop working") {
			t.Fatalf("repair %v: exit %d\n%s\n%s", args, code, out, errb)
		}
		if strings.Contains(errb, "usage: mashctl policy repair") && (strings.Contains(strings.SplitN(errb, "usage:", 2)[1], "--remove-zone") || strings.Contains(strings.SplitN(errb, "usage:", 2)[1], "--replace-zone-key")) {
			t.Fatalf("repair usage still lists the flags:\n%s", errb)
		}
	}
	if e.accountJWT() != before {
		t.Fatal("a rejected repair changed the account")
	}
	// --keep-zone and --servers stay allowed
	if out, errb, code := e.run("policy", "repair", "--keep-zone", "zzz-not-on-server", "--servers", "1"); code == 2 {
		t.Fatalf("--keep-zone/--servers rejected as usage: %s %s", out, errb)
	}
}

func TestRepairUsesOnePolicySnapshotForEveryStep(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	// x's label drifted and y lost its zone in the directory: repair has records to fix from the file.
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatal(err)
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "y" {
			if err := adm.Deprovision(ctx, "y"); err != nil {
				t.Fatal(err)
			}
			if _, err := adm.Provision(ctx, "y", rec.User, rec.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	jwtBefore := e.accountJWT()
	orig, _ := os.ReadFile(e.policy)
	changed := strings.NewReplacer("team: red, zone: a", "team: green, zone: a", "y:\n    labels: {zone: b}", "y:\n    labels: {zone: a}").Replace(string(orig))
	if changed == string(orig) {
		t.Fatal("test setup: the rewrite changed nothing")
	}
	var stages []string
	repairHook = func(stage string) {
		stages = append(stages, stage)
		if stage == "snapshot" {
			if err := os.WriteFile(e.policy, []byte(changed), 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	defer func() { repairHook = func(string) {} }()

	out, errb, code := e.run("policy", "repair")
	if code != 0 || errb != "" {
		t.Fatalf("repair: %d\n%s\n%s", code, out, errb)
	}
	if strings.Join(stages, ",") != "snapshot,applied" {
		t.Fatalf("hook stages %v", stages)
	}
	if !strings.Contains(out, "set zone y b") || !strings.Contains(out, "team=red") || strings.Contains(out, "green") {
		t.Fatalf("repair did not act on the original snapshot:\n%s", out)
	}
	if e.accountJWT() != jwtBefore {
		t.Fatal("the account was pushed from a different policy than the snapshot")
	}
	recs, _ = adm.List(ctx)
	for _, rec := range recs {
		switch rec.Name {
		case "y":
			if rec.Zone != "b" {
				t.Fatalf("y zone %q, want b (snapshot)", rec.Zone)
			}
		case "x":
			if rec.Labels["team"] != "red" {
				t.Fatalf("x labels %v, want team=red (snapshot)", rec.Labels)
			}
		}
	}
}

// changeLines are the per-record change lines of a repair run, in the order printed.
func changeLines(out string) []string {
	var l []string
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "set zone ") || strings.HasPrefix(t, "set label ") || strings.HasPrefix(t, "clear label ") {
			l = append(l, t)
		}
	}
	return l
}

// storeState is everything repair could write: the account JWT, every key file and every directory record.
func (e *applyEnv) storeState(adm interface {
	List(context.Context) ([]store.Record, error)
}, ctx context.Context) (jwtTok string, keys map[string]string, recs map[string]string) {
	e.t.Helper()
	keys, recs = map[string]string{}, map[string]string{}
	files, _ := filepath.Glob(filepath.Join(e.keyDir, "*"))
	zs, _ := filepath.Glob(filepath.Join(e.keyDir, "zones", "*"))
	for _, f := range append(files, zs...) {
		if b, err := os.ReadFile(f); err == nil {
			keys[f] = string(b)
		}
	}
	rs, err := adm.List(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rs {
		b, _ := json.Marshal(r)
		recs[r.Name] = string(b)
	}
	return e.accountJWT(), keys, recs
}

func TestRepairDryRunWritesNothingAndMatchesRealRun(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()

	// drift of every kind repair can fix: the account lost b's signer, b's key file is gone, x has a changed and a stray
	// label, y has no zone; ghost is a record the file does not declare, with labels and a zone.
	bpub, _ := e.ks.ZonePublic("b")
	e.pushClaims(func(ac *jwt.AccountClaims) { delete(ac.SigningKeys, bpub) })
	if err := os.Remove(filepath.Join(e.keyDir, "zones", "b.nk")); err != nil {
		t.Fatal(err)
	}
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue", "stray": "yes"}}); err != nil {
		t.Fatal(err)
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "y" {
			if err := adm.Deprovision(ctx, "y"); err != nil {
				t.Fatal(err)
			}
			if _, err := adm.Provision(ctx, "y", rec.User, rec.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, e.acctPub, "", admin.Meta{Zone: "a", Labels: map[string]string{"team": "green", "keep": "me"}}); err != nil {
		t.Fatal(err)
	}

	jwt0, keys0, recs0 := e.storeState(adm, ctx)
	for _, args := range [][]string{{"policy", "repair", "--dry-run"}, {"policy", "repair", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 0 || errb != "" {
			t.Fatalf("dry run: %d\n%s\n%s", code, out, errb)
		}
		e.noSeeds(out + errb)
		jwt1, keys1, recs1 := e.storeState(adm, ctx)
		if jwt1 != jwt0 || !maps.Equal(keys1, keys0) || !maps.Equal(recs1, recs0) {
			t.Fatalf("--dry-run changed the server, the key store or the directory:\n%s", out)
		}
		for _, w := range []string{"dry run: nothing is written", "repair is additive EXCEPT", "would create b", "dry run, nothing was changed on the server",
			"clear label x stray=yes", "set label x team=red (was blue)", "set zone y b", "reported, not repaired", "record-extra ghost"} {
			if !strings.Contains(out, w) {
				t.Errorf("dry-run output lacks %q:\n%s", w, out)
			}
		}
	}
	dry, _, _ := e.run("policy", "repair", "--dry-run")
	if strings.Contains(dry, "ghost:") && strings.Contains(strings.Join(changeLines(dry), "\n"), "ghost") {
		t.Errorf("a dry run lists a change for an undeclared endpoint:\n%s", dry)
	}

	out, errb, code := e.run("policy", "repair")
	if code != 0 || errb != "" {
		t.Fatalf("repair: %d\n%s\n%s", code, out, errb)
	}
	if got, want := changeLines(out), changeLines(dry); !slices.Equal(got, want) || len(want) != 4 {
		t.Fatalf("real run change lines %q, dry run %q", got, want)
	}
	if !slices.Contains(changeLines(out), "clear label x stray=yes") {
		t.Fatalf("no clear-label line:\n%s", out)
	}
	// the undeclared endpoint's record is byte-identical after the real run, and only reported
	_, _, recs2 := e.storeState(adm, ctx)
	if recs2["ghost"] != recs0["ghost"] || recs2["ghost"] == "" {
		t.Fatalf("repair changed the record of an undeclared endpoint:\nbefore %s\nafter  %s", recs0["ghost"], recs2["ghost"])
	}
	if !strings.Contains(out, "record-extra ghost") || strings.Contains(strings.Join(changeLines(out), "\n"), "ghost") {
		t.Fatalf("ghost was not only reported:\n%s", out)
	}
	if recs2["x"] == recs0["x"] || recs2["y"] == recs0["y"] {
		t.Fatal("the real run did not change x and y")
	}
}

func TestDriftReportsUnknownSigningKeys(t *testing.T) {
	e := newDriftEnv(t)
	other, _ := nkeys.CreateAccount()
	opub, _ := other.PublicKey()
	e.pushClaims(func(ac *jwt.AccountClaims) { ac.SigningKeys.Add(opub) })
	r, code := e.drift()
	if code != 1 || !r.Drift {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "account", r.Account, statusDrift, "signing-key-unknown "+opub)
	if d := r.Account.Findings[0].Detail; !strings.Contains(d, "will not carry signing keys it did not create") {
		t.Errorf("finding: %s", d)
func TestDriftRecordExtraNamesTheFix(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, e.acctPub, "", admin.Meta{Zone: "a"}); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run("policy", "drift")
	if code != 1 || !strings.Contains(out, "record-extra ghost") ||
		!strings.Contains(out, "ghost looks like an interrupted add (a directory record or creds file exists but the policy file has no endpoint ghost): run mashctl retire ghost --force --delete-creds, then mashctl add ghost again") {
		t.Fatalf("%d\n%s", code, out)
	}
}
