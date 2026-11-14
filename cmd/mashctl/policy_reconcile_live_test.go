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
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/reconcile"
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

// The test signer: it builds the first generation's account claims from the JWT the server holds (every signing key
// replaced by the list set's scoped keys), and re-signs the previous claims with more keys revoked.
func (e *applyEnv) testSigner(signers map[string]string) policyapply.SignerFuncs {
	return policyapply.SignerFuncs{
		SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
			cl, err := jwt.DecodeAccountClaims(e.accountJWT())
			if err != nil {
				return "", err
			}
			cl.SigningKeys = jwt.SigningKeys{}
			for _, tpl := range c.Templates {
				sc := jwt.NewUserScope()
				sc.Key, sc.Role = signers[tpl.Group], tpl.Group
				sc.Template.Permissions = tpl.Permissions
				cl.SigningKeys.AddScopedSigner(sc)
			}
			return e.signAccount(cl), nil
		},
		SignPreviousFn: func(prev policyapply.PreviousClaims, revoked []string) (string, error) {
			cl, err := jwt.DecodeAccountClaims(prev.JWT)
			if err != nil {
				return "", err
			}
			for _, k := range revoked {
				cl.RevokeAt(k, time.Now())
			}
			return e.signAccount(cl), nil
		},
	}
}

// AT-015, second sentence: an account that holds an untracked legacy credential and a non-mash signing key is adopted and
// gets its first list set; the plan then revokes the first and reports the second gone, and the executor revokes the
// legacy user, whose reconnect is refused. The legacy user is connected throughout; the admin and system users are exempt.
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
	legacyConn := e.connect(legacy.opt(), nats.Name("legacy-client"), nats.MaxReconnects(-1), nats.ReconnectWait(50*time.Millisecond))
	e.connect(plainUser.opt())

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
	signers := map[string]string{"foo.alice": gpub}
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

	// the first list set is applied with the test signer, through policyapply.Run: the servers, the gate and the ledger
	admConn := e.connect(natsauth.UserCredentials(e.pl.Creds))
	js, err := jetstream.New(admConn)
	if err != nil {
		t.Fatal(err)
	}
	const bucket = "mash_foo_policy"
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
	signer := e.testSigner(signers)
	st, err := policyapply.Run(ctx, policyapply.RunInput{Cluster: cluster, Ledger: ledger, Account: e.acctPub, Inventory: 1,
		ListSet: doc.ListSet, Received: raw, Signer: signer, Signers: signers})
	if err != nil || st.State != policyapply.StateApplied {
		t.Fatalf("first apply: %v %+v", err, st)
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
	if len(plan.Revocations) != 1 || plan.Revocations[0].Key != legacy.pub || plan.Revocations[0].Reason != reconcile.ReasonUntracked {
		t.Fatalf("revocations: %+v", plan.Revocations)
	}
	if plan.Status != "reconciled: 1 actions" || plan.Unknown != 0 {
		t.Errorf("status %q unknown %d", plan.Status, plan.Unknown)
	}
	text, _, code := e.run("policy", "reconcile", "--bucket", bucket)
	t.Logf("dry run:\n%s", text)
	if code != 0 || !strings.Contains(text, "revoke "+legacy.pub+": connected, untracked") || !strings.HasSuffix(text, "reconciled: 1 actions\n") {
		t.Errorf("text: %d\n%s", code, text)
	}
	// the dry run changed nothing: the legacy user is still connected and nothing is revoked
	if !legacyConn.IsConnected() || len(e.claims().Revocations) != 0 {
		t.Fatal("the dry run changed the account")
	}

	// the executor, with the test signer, the way --yes will call it
	a, err := admin.New(admConn, "alice")
	if err != nil {
		t.Fatal(err)
	}
	res, err := reconcile.Execute(ctx, reconcile.ExecInput{Build: func(context.Context) (reconcile.Plan, error) { return plan, nil }, Seen: &plan, Ledger: ledger, Cluster: cluster, Account: e.acctPub, Inventory: 1,
		Signers: signers, Signer: signer, Marker: a})
	if err != nil {
		t.Fatalf("execute: %v %+v", err, res.Status)
	}
	if len(res.Revoked) != 1 || res.Revoked[0] != legacy.pub {
		t.Errorf("result %+v", res)
	}
	if _, ok := e.claims().Revocations[legacy.pub]; !ok {
		t.Fatal("the server does not hold the revocation")
	}
	// the in-force JWT is the revoking one, so the plan is now empty and the next apply keeps the revocation
	if tok, _ := ledger.InForceJWT(); revocationOf(t, tok, legacy.pub) == 0 {
		t.Error("the JWT in force does not carry the revocation")
	}
	deadline := time.Now().Add(10 * time.Second)
	for legacyConn.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if legacyConn.IsConnected() {
		t.Error("the revoked user is still connected")
	}
	if nc, err := nats.Connect(e.srv.ClientURL(), legacy.opt(), nats.MaxReconnects(0), nats.Timeout(3*time.Second)); err == nil {
		nc.Close()
		t.Error("the revoked user could connect again")
	}
	text, _, code = e.run("policy", "reconcile", "--bucket", bucket)
	if code != 0 || !strings.HasSuffix(text, "reconciled: nothing to do\n") {
		t.Errorf("after the executor: %d\n%s", code, text)
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
