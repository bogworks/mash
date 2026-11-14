package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/bogworks/mash/internal/reconcile"
	"github.com/bogworks/mash/internal/store"
)

// pushAccount re-signs the account's claims with mod applied, under the operator key, and pushes them to the server
// through the system account (what the apply push does). The new JWT must be newer than the one the server holds.
func (e *applyEnv) pushAccount(tok string) {
	e.t.Helper()
	nc := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	m, err := nc.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(tok), 5*time.Second)
	if err != nil {
		e.t.Fatal(err)
	}
	if !strings.Contains(string(m.Data), "200") {
		e.t.Fatalf("claims update: %s", m.Data)
	}
}

// signAccount encodes cl under the operator key once the clock is past the JWT it replaces.
func (e *applyEnv) signAccount(cl *jwt.AccountClaims) string {
	e.t.Helper()
	op, _, err := e.ks.Operator()
	if err != nil {
		e.t.Fatal(err)
	}
	for time.Now().Unix() <= cl.IssuedAt {
		time.Sleep(100 * time.Millisecond)
	}
	tok, err := cl.Encode(op)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// liveUser is a user JWT signed by signer for the account, and the means to connect with it.
type liveUser struct {
	pub  string
	tok  string
	seed nkeys.KeyPair
}

func (u liveUser) opt() nats.Option {
	return nats.UserJWT(func() (string, error) { return u.tok, nil }, func(n []byte) ([]byte, error) { return u.seed.Sign(n) })
}

// mintUser signs a user for name with signer; issuerAccount is set when signer is a signing key, not the account key.
func (e *applyEnv) mintUser(signer nkeys.KeyPair, issuerAccount, name string) liveUser {
	e.t.Helper()
	ukp, _ := nkeys.CreateUser()
	return e.mintUserFor(signer, issuerAccount, name, ukp)
}

// mintUserFor is mintUser for a given user key pair.
func (e *applyEnv) mintUserFor(signer nkeys.KeyPair, issuerAccount, name string, ukp nkeys.KeyPair) liveUser {
	e.t.Helper()
	pub, _ := ukp.PublicKey()
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	uc.IssuerAccount = issuerAccount
	tok, err := uc.Encode(signer)
	if err != nil {
		e.t.Fatal(err)
	}
	return liveUser{pub: pub, tok: tok, seed: ukp}
}

// at015ListSet is the first list set: range foo, owner group foo.alice with the one sender foo.alice.core.
func at015ListSet(account string) []byte {
	return []byte(fmt.Sprintf(`{"schema":%q,"account":%q,"range":"foo","generation":1,"base_generation":0,
"groups":{"alice":{"mail":"shared","prompt":"closed","status":"closed","heartbeat":"closed"}},
"senders":{"foo.alice.core":{"allow":[],"deny":[]}},"issued_at":"2026-11-14T00:00:00Z"}`, policydoc.DocumentSchema, account))
}

// AT-015, second sentence: an account that holds an untracked legacy credential, a recorded user outside the list set and
// a non-mash signing key is adopted and gets its first list set through the production signer (policyserve.Issuer),
// which drops the signing key; the dry run then plans the two revocations and `--yes --plan DIGEST` executes them: the
// users' reconnects are refused. The users are connected throughout; the admin and system users are exempt.
func TestPolicyReconcileAT015Live(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	ctx := context.Background()

	// the account as it is before adoption: a plain signing key, a user signed by it, a legacy user signed by the account key
	plainKP, _ := nkeys.CreateAccount()
	plainPub, _ := plainKP.PublicKey()
	cl := e.claims()
	cl.SigningKeys.Add(plainPub)
	e.pushAccount(e.signAccount(cl))
	legacy := e.mintUser(mustAccountKP(t, e), "", "legacy-lane")
	plainUser := e.mintUser(plainKP, e.acctPub, "plain-lane")
	recorded := e.mintUser(mustAccountKP(t, e), "", "gone-lane")
	legacyConn := e.connect(legacy.opt(), nats.Name("legacy-client"), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
	e.connect(plainUser.opt())
	e.connect(recorded.opt(), nats.Name("gone-client"))
	if err := e.adminOnly().RecordIssued(ctx, store.Issued{PublicKey: recorded.pub, Address: "foo.alice.gone", Kind: store.IssuedEndpoint,
		IssuedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	submitter := userPub(t)
	if out, errb, code := e.run("policy", "adopt", "--submitter", submitter); code != 0 {
		t.Fatalf("adopt: %d %s %s", code, out, errb)
	}

	// the group's scoped key is the key store's
	gkp, err := e.ks.GroupKey("foo.alice", true)
	if err != nil {
		t.Fatal(err)
	}
	gpub, _ := gkp.PublicKey()
	raw := at015ListSet(e.acctPub)
	file := filepath.Join(t.TempDir(), "first.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// before the first apply: the preview names the signing key the apply removes and the legacy user it would revoke
	out, errb, code := e.run("policy", "reconcile", "--listset", file, "--json")
	if code != 0 {
		t.Fatalf("preview: %d %s %s", code, out, errb)
	}
	t.Logf("preview:\n%s", func() string { o, _, _ := e.run("policy", "reconcile", "--listset", file); return o }())
	var pre reconcile.Plan
	if err := json.Unmarshal([]byte(out), &pre); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if pre.Source != reconcile.SourceListSetFile || len(pre.SigningKeys) != 1 || pre.SigningKeys[0].Key != plainPub || pre.SigningKeys[0].State != reconcile.StateRemovedByApply {
		t.Fatalf("preview signing keys: %+v", pre.SigningKeys)
	}
	if got := pre.SigningKeys[0].Signed; len(got) != 1 || got[0].Key != plainUser.pub || got[0].Conn.Name != "plain-lane" {
		t.Errorf("the users the signing key signed: %+v", got)
	}
	if r, ok := planRevocation(pre, legacy.pub); !ok || r.Reason != reconcile.ReasonUntracked || r.Connections[0].Name != "legacy-lane" || r.Connections[0].Client != "legacy-client" {
		t.Errorf("preview revocation of the legacy user: %+v %v", r, ok)
	}

	const bucket = "mash_foo_policy"
	ledger, a, cluster := e.applyFirstProduction(t, bucket, raw)
	// the plain signing key is gone from the JWT of every server
	jwts, err := cluster.AccountJWTs(ctx, e.acctPub)
	if err != nil || len(jwts) != 1 {
		t.Fatalf("server JWTs: %v %v", jwts, err)
	}
	for srv, tok := range jwts {
		c, err := jwt.DecodeAccountClaims(tok)
		if err != nil {
			t.Fatal(err)
		}
		if _, has := c.SigningKeys[plainPub]; has || len(c.SigningKeys) != 1 || c.SigningKeys[gpub] == nil {
			t.Fatalf("server %s: signing keys after the first apply: %v", srv, c.SigningKeys)
		}
	}
	if got := e.claims().SigningKeys; len(got) != 1 || got[gpub] == nil {
		t.Fatalf("the account's signing keys after the apply: %v", got)
	}

	// after it: the signing key is gone and the legacy user is the one revocation
	out, errb, code = e.run("policy", "reconcile", "--bucket", bucket, "--json")
	if code != 0 {
		t.Fatalf("plan: %d %s %s", code, out, errb)
	}
	var plan reconcile.Plan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Source != reconcile.SourceInForce || plan.Generation != 1 || len(plan.SigningKeys) != 0 {
		t.Fatalf("plan: %+v", plan)
	}
	if len(plan.Revocations) != 2 {
		t.Fatalf("revocations: %+v", plan.Revocations)
	}
	if r, ok := planRevocation(plan, legacy.pub); !ok || r.Reason != reconcile.ReasonUntracked {
		t.Fatalf("revocations: %+v", plan.Revocations)
	}
	if r, ok := planRevocation(plan, recorded.pub); !ok || r.Reason != reconcile.ReasonRecorded || r.Address != "foo.alice.gone" {
		t.Fatalf("revocations: %+v", plan.Revocations)
	}
	if plan.Status != "reconciled: 2 actions" || plan.Unknown != 0 || !strings.HasPrefix(plan.PlanDigest, "sha256:") {
		t.Errorf("status %q unknown %d", plan.Status, plan.Unknown)
	}
	text, _, code := e.run("policy", "reconcile", "--bucket", bucket)
	t.Logf("dry run:\n%s", text)
	digest := plan.PlanDigest
	if code != 0 || !strings.Contains(text, "revoke "+legacy.pub+": connected, untracked") ||
		!strings.HasSuffix(text, "run again with --yes --plan "+digest+" to revoke\nplan: "+digest+"\nreconciled: 2 actions\n") {
		t.Errorf("text: %d\n%s", code, text)
	}
	// the dry run changed nothing: the users are still connected and nothing is revoked
	if !legacyConn.IsConnected() || len(e.claims().Revocations) != 0 {
		t.Fatal("the dry run changed the account")
	}

	// --yes: with the digest of the dry run
	out, errb, code = e.run("policy", "reconcile", "--bucket", bucket, "--yes", "--plan", digest)
	t.Logf("--yes:\n%s%s", out, errb)
	if code != 0 || !strings.Contains(out, "revoked "+legacy.pub+": "+reconcile.ReasonUntracked+"\n") ||
		!strings.Contains(out, "revoked "+recorded.pub+": "+reconcile.ReasonRecorded+"\n") || !strings.HasSuffix(out, "reconciled: 2 revoked\n") {
		t.Fatalf("--yes: %d %q %q", code, out, errb)
	}
	for _, k := range []string{legacy.pub, recorded.pub} {
		if _, ok := e.claims().Revocations[k]; !ok {
			t.Fatalf("the server does not hold the revocation of %s", k)
		}
		// the in-force JWT is the revoking one, so the next apply keeps the revocation
		if tok, _ := ledger.InForceJWT(); revocationOf(t, tok, k) == 0 {
			t.Errorf("the JWT in force does not carry the revocation of %s", k)
		}
	}
	// the ledger: the same generation, the same list set
	if rec, err := ledger.InForce(); err != nil || rec.Generation != 1 {
		t.Errorf("ledger after --yes: %+v %v", rec, err)
	}
	// the issue record: the recorded user is marked revoked; the legacy user never had a record and has none now
	recs, _, err := a.Issued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawRecorded bool
	for _, r := range recs {
		switch r.PublicKey {
		case recorded.pub:
			sawRecorded = true
			if r.RevokedAt == "" {
				t.Error("the recorded user is not marked revoked in the issue record")
			}
		case legacy.pub:
			t.Error("the legacy user has an issue record")
		}
	}
	if !sawRecorded {
		t.Error("the recorded user's issue record is gone")
	}
	deadline := time.Now().Add(10 * time.Second)
	for legacyConn.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if legacyConn.IsConnected() {
		t.Error("the revoked user is still connected")
	}
	for _, u := range []liveUser{legacy, recorded} {
		if nc, err := nats.Connect(e.srv.ClientURL(), u.opt(), nats.MaxReconnects(0), nats.Timeout(3*time.Second)); err == nil {
			nc.Close()
			t.Errorf("the revoked user %.8s could connect again", u.pub)
		}
	}
	text, _, code = e.run("policy", "reconcile", "--bucket", bucket)
	if code != 0 || !strings.HasSuffix(text, "reconciled: nothing to do\n") {
		t.Errorf("after the executor: %d\n%s", code, text)
	}
}

// applyFirstProduction applies the first list set (raw, range foo) through policyserve.Applier.Apply, the Applier `policy
// serve` builds, with the drop fields serve sets (policyServeDropFields): its Issuer drops a non-mash signing key once the
// admin user and the exempt users are shown to stay under kept keys. It creates the policy bucket and returns its ledger,
// an admin handle and the cluster.
func (e *applyEnv) applyFirstProduction(t *testing.T, bucket string, raw []byte) (*policyapply.KVLedger, *admin.Admin, policyapply.NATSCluster) {
	t.Helper()
	ctx := context.Background()
	admConn := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(admConn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: bucket}); err != nil {
		t.Fatal(err)
	}
	ledger, err := policyapply.NewKVLedger(ctx, js, e.acctPub, policyapply.KVLedgerOptions{Bucket: bucket})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := policydoc.Decode(raw, policydoc.DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sysConn := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	cluster := policyapply.NATSCluster{Conn: sysConn, Inventory: 1}
	a, err := admin.New(admConn, "alice")
	if err != nil {
		t.Fatal(err)
	}
	adminUser, err := credsUserKey(e.pl.Creds)
	if err != nil {
		t.Fatal(err)
	}
	adminIssuer, err := credsIssuer(e.pl.Creds)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := apply.LoadTrust(e.ks)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, bucket)
	if err != nil {
		t.Fatal(err)
	}
	// The Applier `policy serve` runs, with the drop fields set by serve's own helper.
	app := &policyserve.Applier{
		Account: e.acctPub, Range: "foo", Ledger: ledger, Sys: sysConn, Keys: e.ks, Trust: trust, Record: policyapply.NewAdminIssueRecord(a), KV: kv,
		Servers: 1, URL: e.srv.ClientURL(), Inboxes: a, Provisioner: nopProvisioner{},
		Load: func(_ context.Context, u string) (*nats.Conn, error) {
			return nats.Connect(u, natsauth.UserCredentials(e.pl.Creds), nats.MaxReconnects(0))
		},
	}
	policyServeDropFields(app, a, sysConn, adminUser, adminIssuer)
	var d policyintake.Doc
	d.Raw, d.Account, d.Range, d.Generation, d.BaseGeneration = raw, doc.Account, doc.Range, doc.Generation, doc.BaseGeneration
	if _, err := app.Apply(ctx, d, nopGuard{}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if st, err := ledger.InForce(); err != nil || st.Generation != 1 {
		t.Fatalf("first apply: in force %+v, %v", st, err)
	}
	return ledger, a, cluster
}

// --yes with a plan digest that is not the plan now is refused, and changes nothing: the dry run's plan is stale once
// another untracked user connects, and a digest that was never a plan is refused alike.
func TestPolicyReconcileYesWithAStalePlanIsRefused(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	legacy := e.mintUser(mustAccountKP(t, e), "", "legacy-lane")
	legacyConn := e.connect(legacy.opt(), nats.Name("legacy-client"), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
	if out, errb, code := e.run("policy", "adopt", "--submitter", userPub(t)); code != 0 {
		t.Fatalf("adopt: %d %s %s", code, out, errb)
	}
	const bucket = "mash_foo_policy"
	ledger, _, _ := e.applyFirstProduction(t, bucket, at015ListSet(e.acctPub))

	out, errb, code := e.run("policy", "reconcile", "--bucket", bucket, "--json")
	var old reconcile.Plan
	if code != 0 || json.Unmarshal([]byte(out), &old) != nil || len(old.Revocations) != 1 || old.PlanDigest == "" {
		t.Fatalf("dry run: %d %s %s", code, out, errb)
	}
	// another untracked user connects after the dry run
	late := e.mintUser(mustAccountKP(t, e), "", "late-lane")
	e.connect(late.opt(), nats.Name("late-client"))

	jwtBefore, _ := ledger.InForceJWT()
	serverBefore := e.accountJWT()
	for _, digest := range []string{old.PlanDigest, "sha256:" + strings.Repeat("0", 64)} {
		out, errb, code := e.run("policy", "reconcile", "--bucket", bucket, "--yes", "--plan", digest)
		if code != 1 || out != "" || !strings.Contains(errb, "the plan changed since the dry run: run it again") {
			t.Errorf("%s: %d %q %q", digest, code, out, errb)
		}
	}
	if jwtAfter, _ := ledger.InForceJWT(); jwtAfter != jwtBefore || e.accountJWT() != serverBefore || len(e.claims().Revocations) != 0 {
		t.Error("a refused --yes changed the account")
	}
	if !legacyConn.IsConnected() {
		t.Error("a refused --yes disconnected a user")
	}
	// the fresh plan's digest is accepted
	out, errb, code = e.run("policy", "reconcile", "--bucket", bucket, "--json")
	var fresh reconcile.Plan
	if code != 0 || json.Unmarshal([]byte(out), &fresh) != nil || fresh.PlanDigest == old.PlanDigest || len(fresh.Revocations) != 2 {
		t.Fatalf("fresh dry run: %d %s %s", code, out, errb)
	}
	if out, errb, code := e.run("policy", "reconcile", "--bucket", bucket, "--yes", "--plan", fresh.PlanDigest); code != 0 || !strings.HasSuffix(out, "reconciled: 2 revoked\n") {
		t.Fatalf("--yes with the fresh digest: %d %q %q", code, out, errb)
	}
}

func mustAccountKP(t *testing.T, e *applyEnv) nkeys.KeyPair {
	t.Helper()
	kp, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func planRevocation(p reconcile.Plan, key string) (reconcile.Revocation, bool) {
	for _, r := range p.Revocations {
		if r.Key == key {
			return r, true
		}
	}
	return reconcile.Revocation{}, false
}

func revocationOf(t *testing.T, tok, key string) int64 {
	t.Helper()
	cl, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	return cl.Revocations[key]
}

// nopProvisioner provisions nothing: the first list set's addresses need no inbox for this test.
type nopProvisioner struct{}

func (nopProvisioner) CreateInbox(context.Context, string) error     { return nil }
func (nopProvisioner) IssueCredential(context.Context, string) error { return nil }

type nopGuard struct{}

func (nopGuard) Before(context.Context, string) error { return nil }
