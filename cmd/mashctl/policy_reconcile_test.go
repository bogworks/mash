package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/store"
)

var updateReconcileGolden = flag.Bool("update-reconcile", false, "rewrite cmd/mashctl/testdata/policy_reconcile_*.golden")

// fixedUser is a user key pair from a fixed seed, so a golden file lists the users in a stable order.
func fixedUser(t *testing.T, b byte) nkeys.KeyPair {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = b
	}
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, seed)
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

// goldenText normalises what differs between runs of the same scenario: the account, the revocation time and the connection id.
var (
	reAccount = regexp.MustCompile(`account A[A-Z0-9]{55}`)
	reAt      = regexp.MustCompile(`\[at [0-9T:Z-]+\]`)
	reCID     = regexp.MustCompile(`cid [0-9]+`)
	reJAcct   = regexp.MustCompile(`"account": "A[A-Z0-9]{55}"`)
	reJAt     = regexp.MustCompile(`"at": "[^"]+"`)
	reJCID    = regexp.MustCompile(`"cid": [0-9]+`)
)

func normaliseReconcile(s string) string {
	s = reAccount.ReplaceAllString(s, "account <ACCOUNT>")
	s = reAt.ReplaceAllString(s, "[at <TIME>]")
	s = reCID.ReplaceAllString(s, "cid <CID>")
	s = reJAcct.ReplaceAllString(s, `"account": "<ACCOUNT>"`)
	s = reJAt.ReplaceAllString(s, `"at": "<TIME>"`)
	return reJCID.ReplaceAllString(s, `"cid": 0`)
}

func checkReconcileGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *updateReconcileGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (run with -update-reconcile after checking the diff)\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// adoptedPreview is an adopted account with a plain signing key, a user signed by it, a legacy user signed by the account
// key and a recorded user whose sender is not in the first list set, all connected, and the first list set in a file.
type adoptedPreview struct {
	e                                         *applyEnv
	file, submitter                           string
	plainPub, legacy, signed, recorded, admin string
}

func newAdoptedPreview(t *testing.T) *adoptedPreview {
	t.Helper()
	e := newApplyEnv(t).withDirectory()
	plainKP, _ := nkeys.FromRawSeed(nkeys.PrefixByteAccount, make([]byte, 32))
	plainPub, _ := plainKP.PublicKey()
	cl := e.claims()
	cl.SigningKeys.Add(plainPub)
	e.pushAccount(e.signAccount(cl))
	akp := mustAccountKP(t, e)
	legacy := e.mintUserFor(akp, "", "legacy-lane", fixedUser(t, 1))
	signed := e.mintUserFor(plainKP, e.acctPub, "plain-lane", fixedUser(t, 2))
	recorded := e.mintUserFor(akp, "", "gone-lane", fixedUser(t, 3))
	for _, u := range []liveUser{legacy, signed, recorded} {
		e.connect(u.opt(), nats.Name("client-of-"+u.pub[:4]))
	}
	if err := e.adminOnly().RecordIssued(context.Background(), store.Issued{PublicKey: recorded.pub, Address: "foo.alice.gone", Kind: store.IssuedEndpoint,
		IssuedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	submitter := userPub(t)
	if out, errb, code := e.run("policy", "adopt", "--submitter", submitter); code != 0 {
		t.Fatalf("adopt: %d %s %s", code, out, errb)
	}
	if _, err := e.ks.GroupKey("foo.alice", true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "first.json")
	if err := os.WriteFile(file, at015ListSet(e.acctPub), 0o600); err != nil {
		t.Fatal(err)
	}
	adm, _ := credsUserKey(e.pl.Creds)
	return &adoptedPreview{e: e, file: file, submitter: submitter, plainPub: plainPub, legacy: legacy.pub, signed: signed.pub, recorded: recorded.pub, admin: adm}
}

func (e *applyEnv) adminOnly() *admin.Admin {
	e.t.Helper()
	a, err := admin.New(e.connect(natsauth.UserCredentials(e.pl.Creds)), "alice")
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func TestPolicyReconcileDryRunTextGolden(t *testing.T) {
	p := newAdoptedPreview(t)
	out, errb, code := p.e.run("policy", "reconcile", "--listset", p.file)
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	checkReconcileGolden(t, "policy_reconcile_dryrun.golden", normaliseReconcile(out))
	p.e.noSeeds(out + errb)
}

func TestPolicyReconcileDryRunJSONGolden(t *testing.T) {
	p := newAdoptedPreview(t)
	out, errb, code := p.e.run("policy", "reconcile", "--listset", p.file, "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
	checkReconcileGolden(t, "policy_reconcile_dryrun.golden.json", normaliseReconcile(out))
}

// --yes is refused in this version with exactly one line, and changes nothing: the account JWT, the issue record and
// the marker are as they were, and the legacy user is still connected.
func TestPolicyReconcileYesIsRefused(t *testing.T) {
	p := newAdoptedPreview(t)
	before := p.e.accountJWT()
	for _, args := range [][]string{{"--yes"}, {"--yes", "--listset", p.file}, {"--json", "--yes"}} {
		out, errb, code := p.e.run(append([]string{"policy", "reconcile"}, args...)...)
		if code != 1 || out != "" || errb != "mashctl: policy reconcile: no signer configured: run without --yes to see the plan\n" {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
	// the same with nothing configured at all: it does not even connect
	var out, errb strings.Builder
	if code := runEnv([]string{"policy", "reconcile", "--yes"}, func(string) string { return "" }, &out, &errb); code != 1 ||
		errb.String() != "mashctl: policy reconcile: no signer configured: run without --yes to see the plan\n" {
		t.Errorf("bare --yes: %d %q", code, errb.String())
	}
	if p.e.accountJWT() != before || len(p.e.claims().Revocations) != 0 {
		t.Error("--yes changed the account")
	}
}

func TestPolicyReconcileUsageAndRefusals(t *testing.T) {
	p := newAdoptedPreview(t)
	for _, args := range [][]string{
		{"--nope"}, {"extra"}, {"--listset"}, {"--servers", "0"}, {"--servers=x"}, {"--account", "not-a-key"}, {"--range", "UPPER"}, {"--json=1"},
	} {
		out, errb, code := p.e.run(append([]string{"policy", "reconcile"}, args...)...)
		if code != 2 || out != "" || !strings.Contains(errb, "usage: mashctl policy reconcile") {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
	// no list set in force and no file: a failure with the way forward, not an empty plan
	out, errb, code := p.e.run("policy", "reconcile", "--bucket", "mash_nobody_policy")
	if code != 1 || out != "" || !strings.Contains(errb, "mashctl: policy reconcile:") {
		t.Errorf("no bucket: %d %q %q", code, out, errb)
	}
	out, errb, code = p.e.run("policy", "reconcile")
	if code != 1 || !strings.Contains(errb, "no policy bucket") {
		t.Errorf("no bucket setting: %d %q %q", code, out, errb)
	}
}

func TestPolicyReconcileNeedsAnAdoptedAccount(t *testing.T) {
	e := newApplyEnv(t).withDirectory()
	file := filepath.Join(t.TempDir(), "first.json")
	if err := os.WriteFile(file, at015ListSet(e.acctPub), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ks.GroupKey("foo.alice", true); err != nil {
		t.Fatal(err)
	}
	out, errb, code := e.run("policy", "reconcile", "--listset", file)
	if code != 1 || out != "" || !strings.Contains(errb, "not adopted") {
		t.Errorf("%d %q %q", code, out, errb)
	}
}

// The dry run exits 1 when a connected exempt user is signed by a signing key that does not stay, prints the plan all the
// same, and says to reissue the user under the account key.
func TestPolicyReconcileIssuerCheckFailureExitsOne(t *testing.T) {
	p := newAdoptedPreview(t)
	if out, errb, code := p.e.run("policy", "adopt", "--submitter", p.submitter, "--exempt", p.signed); code != 0 {
		t.Fatalf("adopt --exempt: %d %s %s", code, out, errb)
	}
	out, errb, code := p.e.run("policy", "reconcile", "--listset", p.file)
	if code != 1 || !strings.Contains(out, "issuer check failed: exempt user "+p.signed+" is signed by "+p.plainPub) || !strings.HasSuffix(out, "\n") {
		t.Errorf("%d\n%s", code, out)
	}
	if !strings.Contains(errb, "reissue them under the account key first") || !strings.Contains(errb, p.signed) {
		t.Errorf("stderr: %q", errb)
	}
	// the exempt user is in no action
	if strings.Contains(out, "revoke "+p.signed) {
		t.Errorf("an exempt user is in the plan:\n%s", out)
	}
	jout, _, jcode := p.e.run("policy", "reconcile", "--listset", p.file, "--json")
	if jcode != 1 || !strings.Contains(jout, `"issuer_problems": [`) || !strings.Contains(jout, `"role": "exempt"`) {
		t.Errorf("json: %d\n%s", jcode, jout)
	}
}
