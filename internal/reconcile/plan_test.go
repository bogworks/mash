package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/store"
)

func switches(group string, mail, prompt, status, hb listset.Switch) []listset.SwitchEntry {
	return []listset.SwitchEntry{
		{Group: group, Verb: listset.Mail, Switch: mail},
		{Group: group, Verb: listset.Prompt, Switch: prompt},
		{Group: group, Verb: listset.Status, Switch: status},
		{Group: group, Verb: listset.Heartbeat, Switch: hb},
	}
}

// testSet is a list set with one group, foo.c, and the senders foo.c.core and foo.c.docs.
func testSet() listset.ListSet {
	return listset.ListSet{
		Owner:    "foo",
		Switches: switches("foo.c", listset.Shared, listset.Closed, listset.Closed, listset.Closed),
		Senders:  []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}},
	}
}

func userKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := kp.PublicKey()
	return p
}

func acctKey(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := kp.PublicKey()
	return kp, p
}

// fakeIssued is an IssueSource.
type fakeIssued struct {
	recs    []store.Issued
	hist    map[string]admin.IssuedTimes
	histErr map[string]error
	proven  bool
	why     string
}

func (f *fakeIssued) Issued(context.Context) ([]store.Issued, bool, error) { return f.recs, true, nil }
func (f *fakeIssued) IssuedBucketProof(context.Context) (bool, string, error) {
	return f.proven, f.why, nil
}
func (f *fakeIssued) IssuedHistoryByKey(_ context.Context, k string) (admin.IssuedTimes, bool, error) {
	if err := f.histErr[k]; err != nil {
		return admin.IssuedTimes{}, false, err
	}
	t, ok := f.hist[k]
	return t, ok, nil
}

// world is the account the planner is run against.
type world struct {
	t       *testing.T
	now     time.Time
	acct    string
	acctKP  nkeys.KeyPair
	scoped  string // the mash scoped key of foo.c
	plain   string // a non-mash signing key
	admin   string
	exempt  string
	claims  *jwt.AccountClaims
	issued  *fakeIssued
	servers []apply.ConnzServer
}

func newWorld(t *testing.T) *world {
	t.Helper()
	kp, acct := acctKey(t)
	_, scoped := acctKey(t)
	_, plain := acctKey(t)
	w := &world{t: t, now: time.Unix(1_800_000_000, 0).UTC(), acct: acct, acctKP: kp, scoped: scoped, plain: plain,
		admin: userKey(t), exempt: userKey(t), issued: &fakeIssued{proven: true,
			hist: map[string]admin.IssuedTimes{}, histErr: map[string]error{}}}
	w.claims = jwt.NewAccountClaims(acct)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = scoped, "foo.c"
	w.claims.SigningKeys.AddScopedSigner(sc)
	return w
}

// conn makes a connection of user key signed by issuer, in the account.
func (w *world) conn(key, issuer, name string, iat int64) apply.ConnzConn {
	return apply.ConnzConn{Key: key, Account: w.acct, Name: "client-" + name, IssuerKey: issuer, CID: uint64(len(w.servers)) + 1,
		IP: "10.0.0.7", UserName: name, IssuedAt: iat}
}

func (w *world) connect(cs ...apply.ConnzConn) {
	w.servers = append(w.servers, apply.ConnzServer{Name: "s1", Conns: cs, Total: len(cs)})
}

func (w *world) record(key, address string, issuedAt time.Time) {
	w.issued.recs = append(w.issued.recs, store.Issued{PublicKey: key, Address: address, Kind: store.IssuedEndpoint,
		IssuedAt: issuedAt.Format(time.RFC3339), Purpose: store.PurposeIssue})
	w.issued.hist[key] = admin.IssuedTimes{Latest: issuedAt, Max: issuedAt, Revisions: 1}
}

func (w *world) build() Plan {
	w.t.Helper()
	p, err := Build(context.Background(), Input{
		Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(), Claims: w.claims,
		Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin, w.exempt},
		Servers: w.servers, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct})
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func finding(p Plan, key string) (Finding, bool) {
	for _, f := range p.Findings {
		if f.Key == key {
			return f, true
		}
	}
	return Finding{}, false
}

func revocation(p Plan, key string) (Revocation, bool) {
	for _, r := range p.Revocations {
		if r.Key == key {
			return r, true
		}
	}
	return Revocation{}, false
}

func TestRecordedSenderNotInTheListSetIsRevoked(t *testing.T) {
	w := newWorld(t)
	old, kept := userKey(t), userKey(t)
	w.record(old, "foo_c_gone", w.now.Add(-time.Hour))
	w.record(kept, "foo_c_core", w.now.Add(-time.Hour))
	w.connect(w.conn(old, w.acct, "gone", w.now.Add(-time.Hour).Unix()))
	p := w.build()
	r, ok := revocation(p, old)
	if !ok || r.Reason != ReasonRecorded || r.Address != "foo_c_gone" || r.At != w.now.Format(time.RFC3339) {
		t.Fatalf("revocation = %+v %v", r, ok)
	}
	if len(r.Connections) != 1 || r.Connections[0].Host != "10.0.0.7" || r.Connections[0].Server != "s1" || r.Connections[0].Name != "gone" {
		t.Errorf("the live connection is not named: %+v", r.Connections)
	}
	if _, ok := revocation(p, kept); ok {
		t.Error("a recorded user whose sender is in the list set was revoked")
	}
	if p.Actions != 1 || p.Status != "reconciled: 1 actions" {
		t.Errorf("status %q actions %d", p.Status, p.Actions)
	}
}

func TestConnectedUnrecordedIsRevokedAsUntracked(t *testing.T) {
	w := newWorld(t)
	legacy := userKey(t)
	w.connect(w.conn(legacy, w.acct, "legacy", w.now.Add(-time.Hour).Unix()))
	p := w.build()
	r, ok := revocation(p, legacy)
	if !ok || r.Reason != ReasonUntracked || r.Address != "" {
		t.Fatalf("revocation = %+v %v", r, ok)
	}
	if !strings.Contains(p.Text(), "revoke "+legacy+": connected, untracked") || !strings.Contains(p.Text(), "host 10.0.0.7, server s1") {
		t.Errorf("the line does not name the connection:\n%s", p.Text())
	}
}

// A recorded key is revoked only when its history is shown complete (ADR 0115) and no revision is future-dated.
func TestRecordedKeyWithAnUnprovenOrFutureHistoryIsAFindingAndUntouched(t *testing.T) {
	cases := map[string]func(w *world, k string){
		"incomplete": func(w *world, k string) { w.issued.histErr[k] = &admin.IncompleteHistoryError{Key: k, Retained: 64} },
		"malformed":  func(w *world, k string) { w.issued.histErr[k] = &admin.MalformedIssuedAtError{Key: k, Revision: 3} },
		"future": func(w *world, k string) {
			w.issued.hist[k] = admin.IssuedTimes{Latest: w.now.Add(time.Hour), Max: w.now.Add(time.Hour), Revisions: 1}
		},
		"older revision in the future": func(w *world, k string) {
			w.issued.hist[k] = admin.IssuedTimes{Latest: w.now.Add(-time.Hour), Max: w.now.Add(time.Hour), Revisions: 2}
		},
		"at the cap": func(w *world, k string) {
			w.issued.hist[k] = admin.IssuedTimes{Latest: w.now.Add(-time.Hour), Max: w.now.Add(-time.Hour), Revisions: 64, Truncated: true}
		},
		"gone": func(w *world, k string) { delete(w.issued.hist, k) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			k := userKey(t)
			w.record(k, "foo_c_gone", w.now.Add(-time.Hour))
			w.connect(w.conn(k, w.acct, "gone", w.now.Add(-time.Hour).Unix()))
			mutate(w, k)
			p := w.build()
			if _, ok := revocation(p, k); ok {
				t.Fatal("the key is in the plan")
			}
			if f, ok := finding(p, k); !ok || !strings.Contains(f.Detail, "not revoked") {
				t.Fatalf("no finding for the key: %+v", p.Findings)
			}
			if p.Actions != 0 || p.Status != "reconciled: partial (1 unknown)" {
				t.Errorf("status %q actions %d", p.Status, p.Actions)
			}
		})
	}
}

func TestUnrecordedKeyIssuedByAScopedKeyIsAFindingNotARevocation(t *testing.T) {
	w := newWorld(t)
	k := userKey(t)
	w.connect(w.conn(k, w.scoped, "orphan", w.now.Add(-time.Hour).Unix()))
	p := w.build()
	if _, ok := revocation(p, k); ok {
		t.Fatal("a list-set credential without its record was revoked as untracked")
	}
	if f, ok := finding(p, k); !ok || f.Detail != "a list-set credential without its record" {
		t.Fatalf("findings = %+v", p.Findings)
	}
}

func TestUnrecordedKeyWithNoOrInconsistentIssuerIsAFindingNotARevocation(t *testing.T) {
	w := newWorld(t)
	empty, stranger := userKey(t), userKey(t)
	_, other := acctKey(t)
	w.connect(w.conn(empty, "", "empty", 1), w.conn(stranger, other, "stranger", 1))
	p := w.build()
	if len(p.Revocations) != 0 {
		t.Fatalf("revoked: %+v", p.Revocations)
	}
	for _, k := range []string{empty, stranger} {
		if f, ok := finding(p, k); !ok || !strings.Contains(f.Detail, "connection with no reported issuer: cannot tell whether a list-set key signed it") {
			t.Errorf("no finding for %s: %+v", k, p.Findings)
		}
	}
}

func TestWithoutBucketProofEveryUnrecordedConnectedKeyIsAFinding(t *testing.T) {
	w := newWorld(t)
	w.claims.SigningKeys.Add(w.plain)
	w.issued.proven, w.issued.why = false, "the issue record's stream has no "+store.IssuedMarkerKey+" marker"
	a, b := userKey(t), userKey(t)
	w.connect(w.conn(a, w.acct, "a", 1), w.conn(b, w.plain, "b", 1))
	p := w.build()
	if len(p.Revocations) != 0 {
		t.Fatalf("revocations without proof: %+v", p.Revocations)
	}
	for _, k := range []string{a, b} {
		if f, ok := finding(p, k); !ok || !strings.Contains(f.Detail, "cannot prove no record was lost") {
			t.Errorf("no finding for %s: %+v", k, p.Findings)
		}
	}
	// the same account with a proven bucket revokes both
	w.issued.proven = true
	if p := w.build(); len(p.Revocations) != 2 {
		t.Errorf("proven bucket: %+v", p.Revocations)
	}
}

func TestExemptKeysAreInNoActionAndNoFinding(t *testing.T) {
	w := newWorld(t)
	// the exempt user is recorded for a sender that is not in the list set, and connected; the admin is connected and
	// unrecorded; both are signed by the account key
	w.record(w.exempt, "svc.legacy", w.now.Add(-time.Hour))
	w.connect(w.conn(w.exempt, w.acct, "svc", 1), w.conn(w.admin, w.acct, "admin", 1))
	p := w.build()
	for _, k := range []string{w.exempt, w.admin} {
		if _, ok := revocation(p, k); ok {
			t.Errorf("exempt key %s revoked", k)
		}
		if _, ok := finding(p, k); ok {
			t.Errorf("exempt key %s is a finding", k)
		}
	}
	if p.Status != "reconciled: nothing to do" {
		t.Errorf("status %q", p.Status)
	}
}

func TestRevocationTimeIsTheLaterOfNowAndTheJWTIssueTime(t *testing.T) {
	w := newWorld(t)
	past, future := userKey(t), userKey(t)
	w.connect(w.conn(past, w.acct, "past", w.now.Add(-time.Hour).Unix()), w.conn(future, w.acct, "future", w.now.Add(time.Hour).Unix()))
	p := w.build()
	if r, _ := revocation(p, past); r.At != w.now.Format(time.RFC3339) {
		t.Errorf("past iat: at %s, want now %s", r.At, w.now.Format(time.RFC3339))
	}
	if r, _ := revocation(p, future); r.At != w.now.Add(time.Hour).Format(time.RFC3339) {
		t.Errorf("future iat: at %s", r.At)
	}
}

func TestSigningKeysStillPresentAndRemovedByTheFirstApply(t *testing.T) {
	w := newWorld(t)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = w.plain, "other.role"
	w.claims.SigningKeys.AddScopedSigner(sc)
	signed := userKey(t)
	w.connect(w.conn(signed, w.plain, "signed", 1))
	p := w.build()
	if len(p.SigningKeys) != 1 || p.SigningKeys[0].Key != w.plain || p.SigningKeys[0].State != StateStillPresent {
		t.Fatalf("signing keys = %+v", p.SigningKeys)
	}
	if len(p.SigningKeys[0].Signed) != 1 || p.SigningKeys[0].Signed[0].Key != signed {
		t.Errorf("signed users = %+v", p.SigningKeys[0].Signed)
	}
	if p.Status != "reconciled: partial (1 unknown)" || p.Actions != 1 { // the key still present is a finding; its signed user is untracked and revoked
		t.Errorf("status %q actions %d", p.Status, p.Actions)
	}
	if !strings.Contains(p.Text(), "signing key "+w.plain+": still present") || p.Unknown != 1 {
		t.Errorf("text:\n%s\nunknown %d", p.Text(), p.Unknown)
	}

	// before the first apply, against a file, the same key is one the apply removes, and is not a finding
	in := Input{Account: w.acct, Now: w.now, Source: SourceListSetFile, ListSet: testSet(), Claims: w.claims,
		Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin}, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct}
	fp, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp.SigningKeys) != 1 || fp.SigningKeys[0].State != StateRemovedByApply || fp.Unknown != 0 {
		t.Errorf("file plan: %+v unknown %d", fp.SigningKeys, fp.Unknown)
	}
	if !strings.Contains(fp.Text(), "signing key "+w.plain+": removed by the first apply") {
		t.Errorf("text:\n%s", fp.Text())
	}
}

func TestIssuerProblemsAreInThePlan(t *testing.T) {
	w := newWorld(t)
	w.claims.SigningKeys.Add(w.plain)
	w.connect(w.conn(w.exempt, w.plain, "svc", 1))
	p := w.build()
	if len(p.IssuerProblems) != 1 || p.IssuerProblems[0].User != w.exempt || p.IssuerProblems[0].Issuer != w.plain {
		t.Fatalf("issuer problems = %+v", p.IssuerProblems)
	}
	var nk *admin.IssuersNotKeptError
	if !errors.As(p.IssuerErr(), &nk) {
		t.Errorf("IssuerErr = %v", p.IssuerErr())
	}
}

func TestAdminUserUnderAKeyThatLeavesIsAnIssuerProblem(t *testing.T) {
	w := newWorld(t)
	w.claims.SigningKeys.Add(w.plain)
	in := Input{Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(), Claims: w.claims,
		Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin}, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.plain}
	p, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.IssuerProblems) != 1 || p.IssuerProblems[0].Role != "admin" || p.IssuerProblems[0].Issuer != w.plain {
		t.Errorf("issuer problems = %+v", p.IssuerProblems)
	}
	in.AdminIssuer = w.scoped // a kept scoped key is fine
	if p, _ = Build(context.Background(), in); len(p.IssuerProblems) != 0 {
		t.Errorf("issuer problems = %+v", p.IssuerProblems)
	}
}

func TestStatusLines(t *testing.T) {
	w := newWorld(t)
	if p := w.build(); p.Status != "reconciled: nothing to do" {
		t.Errorf("empty: %q", p.Status)
	}
	w.connect(w.conn(userKey(t), w.acct, "a", 1), w.conn(userKey(t), w.acct, "b", 1))
	if p := w.build(); p.Status != "reconciled: 2 actions" {
		t.Errorf("actions: %q", p.Status)
	}
	w.servers = nil
	w.connect(w.conn(userKey(t), w.scoped, "c", 1))
	if p := w.build(); p.Status != "reconciled: partial (1 unknown)" {
		t.Errorf("partial: %q", p.Status)
	}
}

func TestIncompleteScanIsAFinding(t *testing.T) {
	w := newWorld(t)
	p, err := Build(context.Background(), Input{Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(),
		Claims: w.claims, Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin}, Issued: w.issued,
		AdminUser: w.admin, AdminIssuer: w.acct, ScanIncomplete: "server s2 did not answer"})
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := finding(p, ""); !ok || !strings.Contains(f.Detail, "server s2 did not answer") || p.Status != "reconciled: partial (1 unknown)" {
		t.Errorf("findings %+v status %q", p.Findings, p.Status)
	}
}

func TestPlanJSONIsStable(t *testing.T) {
	w := newWorld(t)
	p := w.build()
	b, err := p.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"signing_keys", "revocations", "findings", "issuer_problems"} {
		if v, ok := m[k].([]any); !ok || v == nil {
			t.Errorf("%s is %v, want []", k, m[k])
		}
	}
	for _, k := range []string{"account", "source", "generation", "actions", "unknown", "status"} {
		if _, ok := m[k]; !ok {
			t.Errorf("no %s", k)
		}
	}
}

func TestExpectedSigners(t *testing.T) {
	got, err := ExpectedSigners([]string{"foo.c"}, map[string]string{"foo.c": "AHELD", "bar.c": "AOTHER"})
	if err != nil || len(got) != 1 || got["foo.c"] != "AHELD" {
		t.Errorf("held: %v %v", got, err)
	}
	if _, err = ExpectedSigners([]string{"foo.c"}, nil); err == nil || !strings.Contains(err.Error(), "key store holds no signing key for group foo.c") {
		t.Errorf("a group with no held key must be an error naming the key store: %v", err)
	}
}

// Another range's owner-group key, held by the key store, is mash's and stays: it is not a signing-key line, not
// counted, and a user it signed is neither a signing-key finding nor an issuer problem.
func TestAnotherRangesHeldScopedKeyIsKept(t *testing.T) {
	w := newWorld(t)
	_, other := acctKey(t)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = other, "bar.c"
	w.claims.SigningKeys.AddScopedSigner(sc)
	signed := w.exempt
	w.connect(w.conn(signed, other, "svc", 1))
	in := Input{Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(), Claims: w.claims,
		Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped, "bar.c": other},
		Exempt: []string{w.admin, signed}, Servers: w.servers, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct}
	p, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SigningKeys) != 0 || p.Unknown != 0 || p.Status != "reconciled: nothing to do" {
		t.Errorf("signing keys %+v unknown %d status %q", p.SigningKeys, p.Unknown, p.Status)
	}
	if len(p.IssuerProblems) != 0 {
		t.Errorf("a user signed by another range's kept key is flagged: %+v", p.IssuerProblems)
	}
	if strings.Contains(p.Text(), other) {
		t.Errorf("text names the other range's key:\n%s", p.Text())
	}
}

// A scoped key whose role names an owner group but that the key store does not hold is not mash's.
func TestScopedKeyNotHeldIsNonMash(t *testing.T) {
	w := newWorld(t)
	_, stray := acctKey(t)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = stray, "bar.c"
	w.claims.SigningKeys.AddScopedSigner(sc)
	w.connect(w.conn(w.exempt, stray, "svc", 1))
	p := w.build() // Held has foo.c only
	if len(p.SigningKeys) != 1 || p.SigningKeys[0].Key != stray || p.SigningKeys[0].State != StateStillPresent {
		t.Fatalf("signing keys = %+v", p.SigningKeys)
	}
	if len(p.IssuerProblems) != 1 || p.IssuerProblems[0].Issuer != stray {
		t.Errorf("issuer problems = %+v", p.IssuerProblems)
	}
}

// The admin user key in use is exempt even when the adoption marker does not record it (rotated credentials): it is
// never revoked, and the plan says to adopt again.
func TestAdminKeyInUseNotInTheMarkerIsNeverRevoked(t *testing.T) {
	w := newWorld(t)
	rotated := userKey(t)
	w.connect(w.conn(rotated, w.acct, "admin", w.now.Add(-time.Hour).Unix()))
	p, err := Build(context.Background(), Input{
		Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(), Claims: w.claims,
		Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin, w.exempt},
		Servers: w.servers, Issued: w.issued, AdminUser: rotated, AdminIssuer: w.acct})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := revocation(p, rotated); ok || p.Actions != 0 {
		t.Errorf("the admin key in use was revoked: %+v", p.Revocations)
	}
	f, ok := finding(p, rotated)
	if !ok || !strings.Contains(f.Detail, "admin user KEY in use is not in the adoption marker's exempt set: run `mashctl policy adopt` again to record it") {
		t.Fatalf("finding = %+v %v", f, ok)
	}
	if !strings.HasPrefix(p.Status, "reconciled: partial") {
		t.Errorf("status %q", p.Status)
	}
}

// Two connections of one exempt user whose JWTs are signed by different keys: the issuer that is not kept is refused
// even though the other one is kept.
func TestExemptUserWithOneConnectionUnderARemovedIssuerIsRefused(t *testing.T) {
	w := newWorld(t)
	w.connect(w.conn(w.exempt, w.plain, "svc", w.now.Add(-time.Hour).Unix()), w.conn(w.exempt, w.acct, "svc", w.now.Add(-time.Hour).Unix()))
	w.claims.SigningKeys.Add(w.plain)
	p := w.build()
	if len(p.IssuerProblems) != 1 || p.IssuerProblems[0] != (admin.IssuerProblem{Role: "exempt", User: w.exempt, Issuer: w.plain}) {
		t.Fatalf("issuer problems = %+v", p.IssuerProblems)
	}
	if p.IssuerErr() == nil {
		t.Error("no issuer error")
	}
}

// While the scan is incomplete a silent server could hold a connection that makes the key protected: no connected,
// untracked key is revoked, each is a finding. A recorded user's revocation does not depend on the scan.
func TestIncompleteScanWithholdsUntrackedRevocations(t *testing.T) {
	w := newWorld(t)
	legacy := userKey(t)
	w.connect(w.conn(legacy, w.acct, "legacy", w.now.Add(-time.Hour).Unix()))
	p, err := Build(context.Background(), Input{Account: w.acct, Now: w.now, Source: SourceInForce, Generation: 1, ListSet: testSet(),
		Claims: w.claims, Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Exempt: []string{w.admin, w.exempt},
		Servers: w.servers, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct, ScanIncomplete: "server s2 did not answer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := revocation(p, legacy); ok {
		t.Errorf("revoked while the scan is incomplete: %+v", p.Revocations)
	}
	if f, ok := finding(p, legacy); !ok || !strings.Contains(f.Detail, "not revoked: the connection scan is incomplete") {
		t.Errorf("findings %+v", p.Findings)
	}
}

// The digest ignores times and connection details, and changes with what is revoked.
func TestPlanDigestIgnoresTimesAndConnectionsButNotTheRevocations(t *testing.T) {
	w := newWorld(t)
	old := userKey(t)
	w.record(old, "foo_c_gone", w.now.Add(-time.Hour))
	w.connect(w.conn(old, w.acct, "gone", w.now.Add(-time.Hour).Unix()))
	p1 := w.build()
	if !strings.HasPrefix(p1.PlanDigest, "sha256:") || len(p1.PlanDigest) != len("sha256:")+64 || p1.PlanDigest != p1.Digest() {
		t.Fatalf("digest %q", p1.PlanDigest)
	}
	w.now = w.now.Add(10 * time.Minute) // a later run: another revocation time
	w.servers = nil
	w.connect(w.conn(old, w.acct, "gone", w.now.Unix())) // reconnected: another connection id and iat
	p2 := w.build()
	if p2.Revocations[0].At == p1.Revocations[0].At {
		t.Fatal("the revocation time did not move: the test shows nothing")
	}
	if p2.PlanDigest != p1.PlanDigest {
		t.Errorf("the digest moved with times or connections: %s %s", p1.PlanDigest, p2.PlanDigest)
	}
	other := userKey(t)
	w.record(other, "foo_c_gone2", w.now.Add(-time.Hour))
	w.connect(w.conn(other, w.acct, "gone2", w.now.Unix()))
	if p3 := w.build(); p3.PlanDigest == p1.PlanDigest {
		t.Error("a second revocation did not change the digest")
	}
	if !strings.Contains(p1.Text(), "run again with --yes --plan "+p1.PlanDigest+" to revoke\nplan: "+p1.PlanDigest+"\nreconciled: 1 actions\n") {
		t.Errorf("text:\n%s", p1.Text())
	}
	pre := p1
	pre.Source, pre.PlanDigest = SourceListSetFile, ""
	if strings.Contains(pre.Text(), "plan: ") || strings.Contains(pre.Text(), "--yes --plan") {
		t.Errorf("a preview names a digest:\n%s", pre.Text())
	}
}

// A same-range group the incoming list set drops: its held key is removed by the apply (Issuer.Sign rebuilds this
// range's keys from the list set), so an exempt user it signed is an issuer problem, in a file preview and in force.
// Another range's held key stays, so a user it signed is not.
func TestSameRangeGroupTheListSetDropsIsNotKept(t *testing.T) {
	for _, src := range []Source{SourceListSetFile, SourceInForce} {
		t.Run(string(src), func(t *testing.T) {
			w := newWorld(t)
			_, dropped := acctKey(t)
			_, other := acctKey(t)
			for k, role := range map[string]string{dropped: "foo.old", other: "bar.c"} {
				sc := jwt.NewUserScope()
				sc.Key, sc.Role = k, role
				w.claims.SigningKeys.AddScopedSigner(sc)
			}
			svc, svc2 := userKey(t), userKey(t)
			w.connect(w.conn(svc, dropped, "svc", 1), w.conn(svc2, other, "svc2", 1))
			p, err := Build(context.Background(), Input{Account: w.acct, Now: w.now, Source: src, Generation: 1, ListSet: testSet(), Claims: w.claims,
				Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped, "foo.old": dropped, "bar.c": other},
				Exempt: []string{w.admin, svc, svc2}, Servers: w.servers, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct})
			if err != nil {
				t.Fatal(err)
			}
			if len(p.IssuerProblems) != 1 || p.IssuerProblems[0].User != svc || p.IssuerProblems[0].Issuer != dropped {
				t.Errorf("issuer problems = %+v", p.IssuerProblems)
			}
		})
	}
}

// The configured account signing key is mash's own plain signing key: it is not reported, in force or as removed by
// the first apply.
func TestAccountSigningKeyIsNotReported(t *testing.T) {
	w := newWorld(t)
	w.claims.SigningKeys.Add(w.plain)
	for _, src := range []Source{SourceInForce, SourceListSetFile} {
		gen := uint64(1)
		if src == SourceListSetFile {
			gen = 0
		}
		p, err := Build(context.Background(), Input{
			Account: w.acct, Now: w.now, Source: src, Generation: gen, ListSet: testSet(), Claims: w.claims,
			Signers: map[string]string{"foo.c": w.scoped}, Held: map[string]string{"foo.c": w.scoped}, Signing: w.plain,
			Exempt: []string{w.admin}, Issued: w.issued, AdminUser: w.admin, AdminIssuer: w.acct})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.SigningKeys) != 0 || p.Unknown != 0 {
			t.Errorf("source %v: signing keys %+v unknown %d", src, p.SigningKeys, p.Unknown)
		}
	}
}

// A plain signing key (another issuer's, or the signing_key) is carried: never a signing key still present or removed.
func TestPlainSigningKeysAreNeverReported(t *testing.T) {
	w := newWorld(t)
	w.claims.SigningKeys.Add(w.plain)
	p := w.build()
	if len(p.SigningKeys) != 0 {
		t.Fatalf("signing keys = %+v", p.SigningKeys)
	}
}
