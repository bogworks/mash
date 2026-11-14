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

// driftBase declares no endpoint: `mashctl add` makes x and y, so their records and creds are real.
const driftBase = `default: deny
rules:
  - {name: a-to-b, action: allow, from: {net: a}, to: {net: b}}
`

// newDriftEnv is a bootstrap server with the directory made, endpoints x (net a, label team=red) and y (net b)
// added with `mashctl add`: the clean state.
func newDriftEnv(t *testing.T) *applyEnv {
	t.Helper()
	e := newApplyEnv(t, driftBase)
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env["MASH_INSECURE_LOOPBACK"] = "1"
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	for _, args := range [][]string{{"add", "x", "--label", "net=a", "--label", "team=red"}, {"add", "y", "--label", "net=b"}} {
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
	for _, w := range []string{"account: ok", "directory: ok", "issued: ok", "Not covered by drift:", "nsc", "no drift"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "keys:") {
		t.Errorf("the keys state is gone (ADR 0091):\n%s", out)
	}
	e.noSeeds(out)
	r, code := e.drift()
	if code != 0 || r.Drift {
		t.Fatalf("json: %d %+v", code, r)
	}
	for name, s := range map[string]stateReport{"account": r.Account, "directory": r.Directory, "issued": r.Issued} {
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
	if want := []string{"account", "directory", "drift", "issued", "not_covered", "policy"}; !slices.Equal(keys, want) {
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
	adm, ctx, done := e.admin()
	defer done()
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatal(err)
	}
	out, _, code = e.run("policy", "drift", "--json")
	if code != 1 || !strings.Contains(out, `"kind": "label-differs"`) || !strings.Contains(out, `"subject": "x"`) || !strings.Contains(out, `"drift": true`) {
		t.Fatalf("%d\n%s", code, out)
	}
}

// The Z1 guard's finding: an account JWT that still carries a scoped signing key is a zone policy of an older release.
func TestDriftReportsAnAppliedZonePolicy(t *testing.T) {
	e := newDriftEnv(t)
	sk, _ := nkeys.CreateAccount()
	spub, _ := sk.PublicKey()
	e.pushClaims(func(ac *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = spub, "red"
		ac.SigningKeys.AddScopedSigner(sc)
	})
	r, code := e.drift()
	if code != 1 || !r.Drift {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "account", r.Account, statusDrift, apply.KindZonePolicyApplied+" "+spub)
	if d := r.Account.Findings[0].Detail; !strings.Contains(d, "ADR 0091") || !strings.Contains(d, "stay on the last zoned release") {
		t.Errorf("finding: %s", d)
	}
	wantState(t, "directory", r.Directory, statusOK)
	wantState(t, "issued", r.Issued, statusOK)
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
	wantState(t, "issued", r.Issued, statusOK)

	// an extra record, and a record with no labels
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
	if _, err := adm.Provision(ctx, "y", xrec.User, xrec.Account, "", admin.Meta{}); err != nil { // no labels
		t.Fatal(err)
	}
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, xrec.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "directory", r.Directory, statusDrift, "record-extra ghost", "label-differs x", "label-differs y")

	// a record the file declares and the directory lacks
	if err := adm.Deprovision(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	r, _ = e.drift()
	wantState(t, "directory", r.Directory, statusDrift, "record-extra ghost", "record-missing x", "label-differs y")
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
	fakeRevoke(t, nil) // a reissue of creds that differ rotates the user key
	e := newDriftEnv(t)
	akp, _, _ := e.ks.Account()
	e.rewriteCreds("y", akp, "allow:a.x") // the right signer, a tag only a zone key's template read
	r, code := e.drift()
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale y")
	if d := r.Issued.Findings[0].Detail; !strings.Contains(d, "it carries tags") {
		t.Errorf("detail: %s", d)
	}
	wantState(t, "account", r.Account, statusOK)
	wantState(t, "directory", r.Directory, statusOK)

	// the same compare as `reissue --changed` finds it, and a signer that is not the account key is named
	if out, _, _ := e.run("reissue", "--changed"); !strings.Contains(out, "reissued y") {
		t.Fatalf("reissue --changed did not pick up y: %s", out)
	}
	rogue, _ := nkeys.CreateAccount()
	e.rewriteCreds("x", rogue)
	r, _ = e.drift()
	wantState(t, "issued", r.Issued, statusDrift, "creds-stale x")
	if d := r.Issued.Findings[0].Detail; !strings.Contains(d, "signed by ") || !strings.Contains(d, "not the account signing key") {
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

// A creds file minted by `mashctl add` matches what a mint issues now: nothing is stale.
func TestDriftFreshCredsAreNotStale(t *testing.T) {
	e := newDriftEnv(t)
	r, _ := e.drift()
	wantState(t, "issued", r.Issued, statusOK)
}

func TestDriftUnreadableStateIsUnknownAndNotOK(t *testing.T) {
	e := newDriftEnv(t)
	e.env["MASH_SYS_CREDS"] = filepath.Join(e.root, "none.creds")
	r, code := e.drift()
	if code != 1 || r.Account.Status != statusUnknown || r.Directory.Status != statusOK || r.Issued.Status != statusOK {
		t.Fatalf("%d %+v", code, r)
	}
	if out, _, code := e.run("policy", "drift", "--nope"); code != 2 || out != "" {
		t.Fatalf("usage: %d %q", code, out)
	}
	if _, _, code := e.run("policy", "drift", "--creds-dir"); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}

// `policy repair` runs apply as its first step; apply has no replacement yet (ADR 0091), so repair reports that refusal
// and stops at step 1, with and without --dry-run, writing nothing: no account change, no key file, no record.
func TestRepairStopsAtTheApplyRefusal(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue", "stray": "yes"}}); err != nil {
		t.Fatal(err)
	}
	jwt0, keys0, recs0 := e.storeState(adm, ctx)
	for _, args := range [][]string{{"policy", "repair"}, {"policy", "repair", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(errb, "no replacement until the label policy model ships") || !strings.Contains(errb, "nothing was changed") ||
			!strings.Contains(out, "step 1, account") || !strings.Contains(out, "repair stopped at step 1") || strings.Contains(out, "step 2") || strings.Contains(out, "nothing to push") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
		e.noSeeds(out + errb)
		jwt1, keys1, recs1 := e.storeState(adm, ctx)
		if jwt1 != jwt0 || !maps.Equal(keys1, keys0) || !maps.Equal(recs1, recs0) {
			t.Fatalf("%v changed the server, the key store or the directory", args)
		}
	}
}

func TestRepairReportsWhatItNeverDoes(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, e.acctPub, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "x" { // x is zoned on record (made with the retained admin API: no command writes a zone): repair reports it, never un-zones it
			if err := adm.Deprovision(ctx, "x"); err != nil {
				t.Fatal(err)
			}
			if _, err := adm.Provision(ctx, "x", rec.User, rec.Account, "", admin.Meta{Zone: "b", Labels: rec.Labels}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// the zoned record is the guard's (ADR 0091): the apply step of repair refuses, and nothing is changed.
	out, errb, code := e.run("policy", "repair")
	if code != 1 || !strings.Contains(out+errb, `record x carries zone "b": zones are removed (ADR 0091)`) {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	recs, _ = adm.List(ctx)
	names := map[string]string{}
	for _, rec := range recs {
		names[rec.Name] = rec.Zone
	}
	if names["ghost"] != "" || names["x"] != "b" {
		t.Fatalf("repair changed or deleted records: %v", names)
	}
}

func TestRepairStopsWhenApplyRefusesAndDoesNothingDestructive(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	if _, err := adm.SetMeta(ctx, "x", admin.MetaUpdate{Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatal(err)
	}
	// a zone policy of an older release is applied: the Z1 guard refuses, so repair stops at its first step
	e.applyZonePolicy()
	before := e.accountJWT()
	out, errb, code := e.run("policy", "repair")
	if code != 1 || !strings.Contains(errb, "a zone policy is applied") || !strings.Contains(out, "repair stopped at step 1") || strings.Contains(out, "step 2") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	if e.accountJWT() != before {
		t.Fatal("a refused repair changed the account")
	}
	recs, _ := adm.List(ctx)
	for _, rec := range recs {
		if rec.Name == "x" && rec.Labels["team"] != "blue" {
			t.Fatalf("a refused repair changed a record: %v", rec.Labels)
		}
	}
	for _, args := range [][]string{{"--servers", "0"}, {"--bogus"}} {
		if _, _, code := e.run(append([]string{"policy", "repair"}, args...)...); code != 2 {
			t.Fatalf("repair %v: exit %d, want 2", args, code)
		}
	}
}

func TestRepairRejectsTheZoneFlags(t *testing.T) {
	e := newDriftEnv(t)
	before := e.accountJWT()
	for _, args := range [][]string{{"--remove-zone", "b"}, {"--replace-zone-key=b"}, {"--keep-zone", "b"}} {
		out, errb, code := e.run(append([]string{"policy", "repair"}, args...)...)
		if code != 2 || out != "" || !strings.Contains(errb, "ADR 0091") {
			t.Fatalf("repair %v: exit %d\n%s\n%s", args, code, out, errb)
		}
		if strings.Contains(strings.SplitN(errb, "usage:", 2)[len(strings.SplitN(errb, "usage:", 2))-1], "zone") {
			t.Fatalf("repair usage still lists a zone flag:\n%s", errb)
		}
	}
	if e.accountJWT() != before {
		t.Fatal("a rejected repair changed the account")
	}
}

// changeLines are the per-record change lines of a repair run, in the order printed.
func changeLines(out string) []string {
	var l []string
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "set label ") || strings.HasPrefix(t, "clear label ") {
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
	for _, f := range files {
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
	}
}

func TestDriftRecordExtraNamesTheFix(t *testing.T) {
	e := newDriftEnv(t)
	adm, ctx, done := e.admin()
	defer done()
	ghost, _ := nkeys.CreateUser()
	gpub, _ := ghost.PublicKey()
	if _, err := adm.Provision(ctx, "ghost", gpub, e.acctPub, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run("policy", "drift")
	if code != 1 || !strings.Contains(out, "record-extra ghost") ||
		!strings.Contains(out, "ghost looks like an interrupted add (a directory record or creds file exists but the policy file has no endpoint ghost): run mashctl retire ghost --force --delete-creds, then mashctl add ghost again") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestPrintStateEscapesServerText(t *testing.T) {
	var b strings.Builder
	printState(&b, "account", stateReport{Status: statusDrift, Findings: []apply.Finding{{Kind: "k", Subject: "z\x1b[2K", Detail: "d\r\n", Lines: []string{"+ \x1b[31m"}}}, Notes: []string{"n\x1b"}})
	if strings.ContainsAny(b.String(), "\x1b\r") || !strings.Contains(b.String(), `z\x1b[2K`) {
		t.Fatalf("%q", b.String())
	}
}

// zoneRecord adds the zoned record that no command can make any more (ADR 0091), through the retained admin API.
func (e *applyEnv) zoneRecord(name, zone string) {
	e.t.Helper()
	adm, ctx, done := e.admin()
	defer done()
	u, _ := nkeys.CreateUser()
	upub, _ := u.PublicKey()
	if _, err := adm.Provision(ctx, name, upub, e.acctPub, "", admin.Meta{Zone: zone}); err != nil {
		e.t.Fatal(err)
	}
}

// newZonedRecordEnv is a bootstrap server with the directory made and no endpoint and no applied policy: the zoned
// record is added by the test, after which `policy apply` is refused (ADR 0091) and drift still reads.
func newZonedRecordEnv(t *testing.T) *applyEnv {
	t.Helper()
	e := newApplyEnv(t, driftBase)
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env["MASH_INSECURE_LOOPBACK"] = "1"
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	return e
}

// A directory record that carries a zone is an error for drift, whether or not the file declares it.
func TestDriftReportsZonedRecordAsError(t *testing.T) {
	e := newZonedRecordEnv(t)
	e.zoneRecord("zoned", "red")
	r, code := e.drift()
	wantState(t, "directory", r.Directory, statusDrift, "record-extra zoned", "zoned-record zoned")
	if code == 0 {
		t.Error("drift exits 0 with a zoned record")
	}
	want := `record zoned carries zone "red": zones are removed (ADR 0091); run ` + "`mashctl unzone zoned`" + ` to make it five-token, then upgrade`
	found := false
	for _, f := range r.Directory.Findings {
		found = found || f.Kind == kindZonedRecord && f.Detail == want
	}
	if !found {
		t.Errorf("no finding with %q: %+v", want, r.Directory.Findings)
	}
}

// `mashctl policy apply` refuses to run while a zoned record exists, and changes nothing.
func TestPolicyApplyRefusesZonedRecord(t *testing.T) {
	e := newZonedRecordEnv(t)
	e.zoneRecord("zoned", "red")
	jwtBefore := e.accountJWT()
	out, errb, code := e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, `record zoned carries zone "red": zones are removed (ADR 0091)`) {
		t.Fatalf("apply: %d\n%s\n%s", code, out, errb)
	}
	if e.accountJWT() != jwtBefore {
		t.Error("apply pushed the account despite the refusal")
	}
}
