package mint

import (
	"errors"
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
	"github.com/bogworks/mash/internal/policy"
)

// fakeKeys is an in-memory Keys.
type fakeKeys struct {
	acct  nkeys.KeyPair
	zones map[string]nkeys.KeyPair
}

func newFakeKeys(t *testing.T) *fakeKeys {
	t.Helper()
	a, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeKeys{acct: a, zones: map[string]nkeys.KeyPair{}}
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

var errNoZone = ctlkeys.ErrNoZoneKey

func (f *fakeKeys) ZoneKey(zone string, create bool) (nkeys.KeyPair, error) {
	kp, ok := f.zones[zone]
	if !ok {
		if !create {
			return nil, errNoZone
		}
		kp, _ = nkeys.CreateAccount()
		f.zones[zone] = kp
	}
	seed, _ := kp.Seed()
	return nkeys.FromSeed(seed)
}

func compile(t *testing.T, src string) *policy.Compiled {
	t.Helper()
	f, err := policy.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMintVerify(t *testing.T) {
	c := compile(t, `
default: deny
endpoints:
  a: {labels: {zone: red}}
  b: {labels: {zone: blue}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
`)
	keys := newFakeKeys(t)
	m, err := Mint(keys, c, "a", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(m.JWT)
	if err != nil {
		t.Fatal(err)
	}
	zpub, _ := keys.zones["red"].PublicKey()
	_, apub, _ := keys.Account()
	if uc.Issuer != zpub || uc.IssuerAccount != apub || m.AccountPub != apub || uc.Name != "a" || uc.Subject != m.UserPub {
		t.Errorf("claims: issuer %s (want %s) issuer_account %s name %s", uc.Issuer, zpub, uc.IssuerAccount, uc.Name)
	}
	e := c.Endpoints[0]
	if !slices.Equal([]string(uc.Tags), e.Tags) || !slices.Equal(m.Tags, e.Tags) {
		t.Errorf("tags %v want %v", uc.Tags, e.Tags)
	}
	if !reflect.DeepEqual(uc.UserPermissionLimits, jwt.UserPermissionLimits{}) {
		t.Errorf("permission limits populated: %+v", uc.UserPermissionLimits)
	}
	if uc.Expires != 0 {
		t.Error("user has an expiry")
	}
	if _, ok := keys.zones["blue"]; ok {
		t.Error("made the key of a zone nobody was minted in")
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
	c := compile(t, "endpoints:\n  a: {labels: {zone: red}}\n")
	keys := newFakeKeys(t)
	m1, err := Mint(keys, c, "a", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := jwt.ParseDecoratedNKey(m1.Creds)
	seed, _ := kp.Seed()
	m2, err := Mint(keys, c, "a", seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if m2.UserPub != m1.UserPub {
		t.Error("re-issue changed the user key")
	}
	// not a user seed, not a seed
	acctSeed, _ := keys.acct.Seed()
	if _, err := Mint(keys, c, "a", acctSeed, false); err == nil || !strings.Contains(err.Error(), "not a user seed") {
		t.Errorf("account seed: %v", err)
	}
	if _, err := Mint(keys, c, "a", []byte("junk"), false); err == nil {
		t.Error("junk seed accepted")
	}
	// a zone with no key: error on re-issue, nothing created
	delete(keys.zones, "red")
	if _, err := Mint(keys, c, "a", seed, false); err == nil || !errors.Is(err, errNoZone) {
		t.Errorf("re-issue with no zone key: %v", err)
	}
	if _, ok := keys.zones["red"]; ok {
		t.Error("re-issue created the zone key")
	}
	if _, err := Mint(keys, c, "nope", nil, true); err == nil {
		t.Error("undeclared endpoint minted")
	}
}

func TestMintErrorsHideSeeds(t *testing.T) {
	c := compile(t, "endpoints:\n  a: {}\n")
	_, err := Mint(newFakeKeys(t), c, "a", []byte("SUAJUNKJUNKJUNK"), false)
	if err == nil || strings.Contains(err.Error(), "SUAJ") {
		t.Fatalf("%v", err)
	}
}

// TestMintLive connects minted users to an in-process operator-mode server whose account holds the zone keys as
// scoped signers, and checks one allowed and one refused publish.
func TestMintLive(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a nats-server")
	}
	c := compile(t, `
default: deny
endpoints:
  a: {labels: {zone: red}}
  b: {labels: {zone: blue}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
`)
	keys := newFakeKeys(t)
	ma, err := Mint(keys, c, "a", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Mint(keys, c, "b", nil, true); err != nil {
		t.Fatal(err)
	}
	url := startServer(t, c, keys)

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
	for _, s := range []string{"agents.mail.cc.o.blue.b", "agents.mail.cc.o.red.a"} {
		if err := nc.Publish(s, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	nc.Flush()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	got := strings.Join(errs, "\n")
	mu.Unlock()
	if strings.Contains(got, `"agents.mail.cc.o.blue.b"`) {
		t.Errorf("allowed publish refused:\n%s", got)
	}
	if !strings.Contains(got, `Publish to "agents.mail.cc.o.red.a"`) {
		t.Errorf("refused publish not refused:\n%s", got)
	}

	// A re-issue keeps the user key and still connects.
	kp, _ := jwt.ParseDecoratedNKey(ma.Creds)
	seed, _ := kp.Seed()
	re, err := Mint(keys, c, "a", seed, false)
	if err != nil {
		t.Fatal(err)
	}
	nc2, err := nats.Connect(url, nats.MaxReconnects(0), nats.Timeout(5*time.Second), nats.UserCredentialBytes(re.Creds))
	if err != nil {
		t.Fatalf("connect with re-issued creds: %v", err)
	}
	nc2.Close()
}

func startServer(t *testing.T, c *policy.Compiled, keys *fakeKeys) string {
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
	for _, z := range c.Zones {
		pub, _ := keys.zones[z.Name].PublicKey()
		scope := jwt.NewUserScope()
		scope.Key = pub
		scope.Role = z.Name
		scope.Template.Permissions = z.Perms
		ac.SigningKeys.AddScopedSigner(scope)
	}
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

func TestMintZoneKeyCreationIsTheCallers(t *testing.T) {
	c := compile(t, "endpoints:\n  a: {labels: {zone: red}}\n")
	keys := newFakeKeys(t)
	if _, err := Mint(keys, c, "a", nil, false); !errors.Is(err, ctlkeys.ErrNoZoneKey) {
		t.Fatalf("missing key, create false: %v", err)
	}
	if _, ok := keys.zones["red"]; ok {
		t.Fatal("the zone key was created although createZoneKey was false")
	}
	if _, err := Mint(keys, c, "a", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys.zones["red"]; !ok {
		t.Fatal("the zone key was not created")
	}
	if _, err := Mint(keys, c, "a", nil, false); err != nil {
		t.Fatalf("an existing key with create false: %v", err)
	}
}

// TestMintScopedUserCarriesEmptyPermissions: every minted JWT, decoded, has no permissions of its own (pub and sub
// allow and deny, response) and no subscription, data or payload limit set; the zone key's template is the only
// source of permissions. Every endpoint also carries the deny:_none placeholder whatever its zone.
func TestMintScopedUserCarriesEmptyPermissions(t *testing.T) {
	c := compile(t, `
default: deny
endpoints:
  a1: {labels: {zone: red, team: x}}
  a2: {labels: {zone: red}}
  b1: {labels: {zone: blue, tier: secret}}
  b2: {labels: {zone: blue}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
  - {name: d, action: deny, from: {team: x}, to: {tier: secret}}
`)
	keys := newFakeKeys(t)
	for _, e := range c.Endpoints {
		m, err := Mint(keys, c, e.Name, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		uc, err := jwt.DecodeUserClaims(m.JWT)
		if err != nil {
			t.Fatal(err)
		}
		pl := uc.UserPermissionLimits
		if len(pl.Pub.Allow)+len(pl.Pub.Deny)+len(pl.Sub.Allow)+len(pl.Sub.Deny) != 0 || pl.Resp != nil {
			t.Errorf("%s: permissions set: %+v", e.Name, pl.Permissions)
		}
		if pl.Subs != 0 || pl.Data != 0 || pl.Payload != 0 {
			t.Errorf("%s: limits set: subs %d data %d payload %d", e.Name, pl.Subs, pl.Data, pl.Payload)
		}
		if !reflect.DeepEqual(pl, jwt.UserPermissionLimits{}) {
			t.Errorf("%s: permission limits not zero: %+v", e.Name, pl)
		}
		if !slices.Contains([]string(uc.Tags), admin.NoDenyTag) {
			t.Errorf("%s: tags %v lack %s", e.Name, uc.Tags, admin.NoDenyTag)
		}
	}
}

// scopedKeys is a fakeKeys that also sees the account JWT's scopes.
type scopedKeys struct {
	*fakeKeys
	scopes jwt.SigningKeys
}

func (s scopedKeys) VerifyZoneScope(zone, zonePub string) error {
	sc, ok := s.scopes.GetScope(zonePub)
	if !ok {
		return errors.New("the signing key is not in the account JWT")
	}
	if _, isUser := sc.(*jwt.UserScope); !isUser {
		return errors.New("the signing key is not a user scope")
	}
	return nil
}

// TestMintRefusesAZoneKeyThatDoesNotRestrict: a key that is not a scoped signing key would give the minted users no
// restrictions at all, so Mint refuses it. These fail if the guard is removed.
func TestMintRefusesAZoneKeyThatDoesNotRestrict(t *testing.T) {
	c := compile(t, "default: deny\nendpoints:\n  a: {labels: {zone: red}}\n")

	// The account identity key as the zone key.
	k := newFakeKeys(t)
	k.zones["red"] = k.acct
	if m, err := Mint(k, c, "a", nil, false); err == nil || m != nil || !strings.Contains(err.Error(), "identity key") {
		t.Errorf("identity key as zone key: got %v, %v", m, err)
	}

	// A key of another type.
	k = newFakeKeys(t)
	uk, _ := nkeys.CreateUser()
	k.zones["red"] = uk
	if m, err := Mint(k, c, "a", nil, false); err == nil || m != nil || !strings.Contains(err.Error(), "not an account signing key") {
		t.Errorf("user key as zone key: got %v, %v", m, err)
	}

	// A Keys that sees the account JWT: absent from it, or present as something other than a UserScope.
	k = newFakeKeys(t)
	zk, _ := nkeys.CreateAccount()
	k.zones["red"] = zk
	zpub, _ := zk.PublicKey()
	sk := scopedKeys{k, jwt.SigningKeys{}}
	if m, err := Mint(sk, c, "a", nil, false); err == nil || m != nil || !strings.Contains(err.Error(), "not in the account JWT") {
		t.Errorf("zone key missing from the account JWT: got %v, %v", m, err)
	}
	sk.scopes.Add(zpub) // a plain signing key: no scope
	if m, err := Mint(sk, c, "a", nil, false); err == nil || m != nil || !strings.Contains(err.Error(), "not a user scope") {
		t.Errorf("plain signing key: got %v, %v", m, err)
	}
	sk.scopes[zpub] = jwt.NewUserScope()
	if _, err := Mint(sk, c, "a", nil, false); err != nil {
		t.Errorf("a UserScope key must mint: %v", err)
	}
}
