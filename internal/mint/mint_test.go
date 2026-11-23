package mint

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/sap"
)

// fakeKeys is an in-memory Keys.
type fakeKeys struct {
	acct nkeys.KeyPair
	sign nkeys.KeyPair // the account signing key; nil means none is configured
}

func (f *fakeKeys) Signing() (nkeys.KeyPair, string, error) {
	if f.sign == nil {
		return nil, "", ctlkeys.ErrNoSigningKey
	}
	seed, _ := f.sign.Seed()
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, "", err
	}
	pub, _ := kp.PublicKey()
	return kp, pub, nil
}

func newFakeKeys(t *testing.T) *fakeKeys {
	t.Helper()
	a, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeKeys{acct: a}
}

func (f *fakeKeys) Account() (nkeys.KeyPair, string, error) {
	seed, _ := f.acct.Seed()
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, "", err
	}
	pub, _ := kp.PublicKey()
	return kp, pub, nil
}

// The Keys the mint takes is the account key alone: *ctlkeys.Authority satisfies it, and has no zone key to give.
var _ Keys = (*ctlkeys.Authority)(nil)

// testAddr is the endpoint the tests mint.
var testAddr = sap.Address{Owner: "o", Group: "g", Instance: "a"}

func TestMintVerify(t *testing.T) {
	keys := newFakeKeys(t)
	m, err := Mint(keys, testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(m.JWT)
	if err != nil {
		t.Fatal(err)
	}
	_, apub, _ := keys.Account()
	if uc.Issuer != apub || m.AccountPub != apub || uc.Name != testAddr.String() || uc.Subject != m.UserPub {
		t.Errorf("claims: issuer %s (want the account key %s) name %s", uc.Issuer, apub, uc.Name)
	}
	if len(uc.Tags) != 0 {
		t.Errorf("tags %v: ADR 0091 removed them", uc.Tags)
	}
	if !reflect.DeepEqual(uc.Permissions, *admin.Permissions(testAddr)) {
		t.Errorf("permissions %+v", uc.Permissions)
	}
	if !slices.Contains(uc.Pub.Allow, "agents.mail.o.*.*") {
		t.Errorf("the owner mail line is missing: %v", uc.Pub.Allow)
	}
	for _, l := range append(append([]string{}, uc.Pub.Allow...), uc.Sub.Allow...) {
		if strings.Contains(l, "{{") {
			t.Errorf("a template line in a user's own permissions: %s", l)
		}
	}
	if uc.Expires != 0 {
		t.Error("user has an expiry")
	}
	kp, err := jwt.ParseDecoratedNKey(m.Creds)
	if err != nil {
		t.Fatal(err)
	}
	if pub, _ := kp.PublicKey(); pub != m.UserPub {
		t.Error("creds seed is not the user key")
	}
	if tok, err := jwt.ParseDecoratedJWT(m.Creds); err != nil || tok != m.JWT {
		t.Error("creds JWT differs")
	}
}

func TestMintReissue(t *testing.T) {
	keys := newFakeKeys(t)
	m1, err := Mint(keys, testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := jwt.ParseDecoratedNKey(m1.Creds)
	seed, _ := kp.Seed()
	m2, err := Mint(keys, testAddr, seed)
	if err != nil {
		t.Fatal(err)
	}
	if m2.UserPub != m1.UserPub {
		t.Error("re-issue changed the user key")
	}
	// not a user seed, not a seed
	acctSeed, _ := keys.acct.Seed()
	if _, err := Mint(keys, testAddr, acctSeed); err == nil || !strings.Contains(err.Error(), "not a user seed") {
		t.Errorf("account seed: %v", err)
	}
	if _, err := Mint(keys, testAddr, []byte("junk")); err == nil {
		t.Error("junk seed accepted")
	}
}

func TestMintErrorsHideSeeds(t *testing.T) {
	_, err := Mint(newFakeKeys(t), testAddr, []byte("SUAJUNKJUNKJUNK"))
	if err == nil || strings.Contains(err.Error(), "SUAJ") {
		t.Fatalf("%v", err)
	}
}

// TestMintLive connects a minted user to an in-process operator-mode server whose account has no scoped signer and
// checks that the creds authenticate (no zone key is needed) and that the permissions in the JWT apply.
func TestMintLive(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a nats-server")
	}
	keys := newFakeKeys(t)
	ma, err := Mint(keys, testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	url := startServer(t, keys)

	var mu sync.Mutex
	var errs []string
	nc, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(5*time.Second),
		nats.UserCredentialBytes(ma.Creds),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			mu.Lock()
			errs = append(errs, err.Error())
			mu.Unlock()
		}))
	if err != nil {
		t.Fatalf("connect with minted creds: %v", err)
	}
	defer nc.Close()
	for _, s := range []string{"agents.mail.o.g.b", "agents.mail.other.g.b"} {
		if err := nc.Publish(s, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	nc.Flush()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got := strings.Join(errs, "\n")
	mu.Unlock()
	if strings.Contains(got, `"agents.mail.o.g.b"`) {
		t.Errorf("mail to a peer of the owner refused:\n%s", got)
	}
	if !strings.Contains(got, `Publish to "agents.mail.other.g.b"`) {
		t.Errorf("mail to another owner not refused:\n%s", got)
	}

	// A re-issue keeps the user key and still connects.
	kp, _ := jwt.ParseDecoratedNKey(ma.Creds)
	seed, _ := kp.Seed()
	re, err := Mint(keys, testAddr, seed)
	if err != nil {
		t.Fatal(err)
	}
	nc2, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(5*time.Second), nats.UserCredentialBytes(re.Creds))
	if err != nil {
		t.Fatalf("connect with re-issued creds: %v", err)
	}
	nc2.Close()
}

func startServer(t *testing.T, keys *fakeKeys) string {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	opKP, _ := nkeys.CreateOperator()
	opPub, _ := opKP.PublicKey()
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "mint-live"
	opJWT, err := oc.Encode(opKP)
	must(err)
	op, err := jwt.DecodeOperatorClaims(opJWT)
	must(err)
	resolver := &server.MemAccResolver{}
	sysKP, _ := nkeys.CreateAccount()
	sysPub, _ := sysKP.PublicKey()
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"
	sysJWT, err := sc.Encode(opKP)
	must(err)
	must(resolver.Store(sysPub, sysJWT))

	acctPub, _ := keys.acct.PublicKey()
	ac := jwt.NewAccountClaims(acctPub)
	ac.Name = "MASH"
	acctJWT, err := ac.Encode(opKP)
	must(err)
	must(resolver.Store(acctPub, acctJWT))
	s, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver, SystemAccount: sysPub,
	})
	must(err)
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	return s.ClientURL()
}

// With a signing key the user JWT is issued by that key and names the account (the identity key) as issuer_account;
// without one the identity key signs and no issuer_account is set.
func TestMintSigningKey(t *testing.T) {
	keys := newFakeKeys(t)
	_, idPub, _ := keys.Account()
	m, err := Mint(keys, testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	uc, _ := jwt.DecodeUserClaims(m.JWT)
	if uc.Issuer != idPub || uc.IssuerAccount != "" {
		t.Fatalf("no signing key: issuer %s issuer_account %q", uc.Issuer, uc.IssuerAccount)
	}
	sk, _ := nkeys.CreateAccount()
	skPub, _ := sk.PublicKey()
	keys.sign = sk
	m, err = Mint(keys, testAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	uc, err = jwt.DecodeUserClaims(m.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Issuer != skPub || uc.IssuerAccount != idPub || m.AccountPub != idPub {
		t.Fatalf("signing key: issuer %s (want %s) issuer_account %s (want %s) account %s", uc.Issuer, skPub, uc.IssuerAccount, idPub, m.AccountPub)
	}
	// SignUser (a key the endpoint made) takes the same path.
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	tok, acct, err := SignUser(keys, testAddr, upub)
	if err != nil {
		t.Fatal(err)
	}
	uc, _ = jwt.DecodeUserClaims(tok)
	if uc.Issuer != skPub || uc.IssuerAccount != idPub || acct != idPub {
		t.Fatalf("SignUser: issuer %s issuer_account %s account %s", uc.Issuer, uc.IssuerAccount, acct)
	}
}

// A store that records a configured signing key but has lost signing.nk is refused: mint never falls back to the
// identity key, which a strict server rejects.
func TestMintRefusesWhenTheSigningKeyIsLost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	ks, err := ctlkeys.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.InitAccount(); err != nil {
		t.Fatal(err)
	}
	sk, _ := nkeys.CreateAccount()
	seed, _ := sk.Seed()
	src := filepath.Join(t.TempDir(), "sk")
	if err := os.WriteFile(src, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.ImportSigning(src); err != nil {
		t.Fatal(err)
	}
	if _, err := Mint(ks, testAddr, nil); err != nil {
		t.Fatalf("mint with the key: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	if _, err := Mint(ks, testAddr, nil); !errors.Is(err, ctlkeys.ErrSigningKeyLost) || !strings.Contains(err.Error(), "signing.nk") {
		t.Fatalf("mint after loss: %v", err)
	}
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	if _, _, err := SignUser(ks, testAddr, upub); !errors.Is(err, ctlkeys.ErrSigningKeyLost) {
		t.Fatalf("SignUser after loss: %v", err)
	}
}
