package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// strictConvert on a directory shaped like the setup script's: the operator is strict, the preloaded account JWTs are
// signed by a declared operator signing key and list an account signing key, and the creds are signed by signing keys.
func TestStrictConvert(t *testing.T) {
	dir := t.TempDir()
	mk := func(sub string) { _ = os.MkdirAll(filepath.Join(dir, sub), 0o700) }
	mk("keys")
	mk("creds")
	mk("resolver")
	opKP, _ := nkeys.CreateOperator()
	opPub, _ := opKP.PublicKey()
	sysKP, _ := nkeys.CreateAccount()
	sysPub, _ := sysKP.PublicKey()
	acctKP, _ := nkeys.CreateAccount()
	acctPub, _ := acctKP.PublicKey()
	if _, err := writeSeed(filepath.Join(dir, fileOperatorNK), opKP); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSeed(filepath.Join(dir, fileAccountNK), acctKP); err != nil {
		t.Fatal(err)
	}
	otok, _ := jwt.NewOperatorClaims(opPub).Encode(opKP)
	stok, _ := jwt.NewAccountClaims(sysPub).Encode(opKP)
	atok, _ := jwt.NewAccountClaims(acctPub).Encode(opKP)
	os.WriteFile(filepath.Join(dir, fileOperator), []byte(otok+"\n"), 0o600)
	conf := "operator: x\nsystem_account: " + sysPub + "\nresolver_preload: {\n  " + sysPub + ": \"" + stok + "\"\n  " + acctPub + ": \"" + atok + "\"\n}\n"
	os.WriteFile(filepath.Join(dir, fileServerConf), []byte(conf), 0o600)
	os.WriteFile(filepath.Join(dir, "resolver", acctPub+".jwt"), []byte(atok), 0o600)

	secrets := map[string][]byte{}
	sf, err := strictConvert(dir, 4222, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) == 0 {
		t.Error("no seed was registered for the output scan")
	}
	oc, err := jwt.DecodeOperatorClaims(strings.TrimSpace(string(mustReadFile(t, filepath.Join(dir, fileOperator)))))
	if err != nil || !oc.StrictSigningKeyUsage || len(oc.SigningKeys) != 1 {
		t.Fatalf("operator: %+v %v", oc, err)
	}
	osk := oc.SigningKeys[0]
	cb := string(mustReadFile(t, filepath.Join(dir, fileServerConf)))
	for _, pub := range []string{sysPub, acctPub} {
		i := strings.Index(cb, pub+`: "`)
		if i < 0 {
			t.Fatalf("no preload for %s", pub)
		}
		tok := cb[i+len(pub)+3:]
		tok = tok[:strings.Index(tok, `"`)]
		ac, err := jwt.DecodeAccountClaims(tok)
		if err != nil || ac.Issuer != osk || len(ac.SigningKeys) != 1 {
			t.Fatalf("account %s: %+v %v", pub, ac, err)
		}
	}
	rt := string(mustReadFile(t, filepath.Join(dir, "resolver", acctPub+".jwt")))
	if ac, err := jwt.DecodeAccountClaims(rt); err != nil || ac.Issuer != osk || !ac.SigningKeys.Contains(sf.SignPub) {
		t.Fatalf("resolver copy: %v", err)
	}
	for file, acct := range map[string]string{fileAdminCreds: acctPub, fileSysCreds: sysPub} {
		uc, err := credsUserClaims(filepath.Join(dir, file))
		if err != nil || uc.IssuerAccount != acct || uc.Issuer == acct {
			t.Fatalf("%s: %+v %v", file, uc, err)
		}
	}
	setup := string(mustReadFile(t, sf.Setup))
	for _, w := range []string{"signing_key: " + sf.AccountSigningSeed, "operator_key: " + sf.OperatorSeed, "account_key: " + filepath.Join(dir, fileAccountNK)} {
		if !strings.Contains(setup, w) {
			t.Errorf("setup file lacks %q", w)
		}
	}
	if seedRE.MatchString(setup) {
		t.Error("the setup file holds a seed")
	}
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
