package enroll_test

import (
	"reflect"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

type fixedKeys struct{ kp nkeys.KeyPair }

func (f fixedKeys) Account() (nkeys.KeyPair, string, error) {
	seed, _ := f.kp.Seed()
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, "", err
	}
	pub, _ := kp.PublicKey()
	return kp, pub, nil
}

func (f fixedKeys) Signing() (nkeys.KeyPair, string, error) {
	return nil, "", ctlkeys.ErrNoSigningKey
}

func TestSignUserNoServer(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	d := enroll.Deps{Keys: fixedKeys{acct}, Account: acctPub}
	a := sap.Address{Owner: "o", Group: "g", Instance: "i"}
	_, pub := newKey(t)
	tok, err := d.SignUser(a, pub)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Name != "o.g.i" || uc.Subject != pub || uc.Issuer != acctPub || len(uc.Tags) != 0 {
		t.Fatalf("claims %q %q %q", uc.Name, uc.Subject, uc.Issuer)
	}
	if !reflect.DeepEqual(uc.Permissions, *admin.Permissions(a)) {
		t.Fatal("permissions")
	}
	op, _ := nkeys.CreateOperator()
	opPub, _ := op.PublicKey()
	for _, bad := range []string{"", "x", acctPub, opPub} {
		_, err := d.SignUser(a, bad)
		wantCode(t, err, "public_key_invalid")
	}
}
