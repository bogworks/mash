package apply

import (
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

func scopedClaims(t *testing.T, keys ...string) *jwt.AccountClaims {
	t.Helper()
	acct, _ := nkeys.CreateAccount()
	pub, _ := acct.PublicKey()
	cl := jwt.NewAccountClaims(pub)
	for _, k := range keys {
		sc := jwt.NewUserScope()
		sc.Key = k
		sc.Role = "r"
		cl.SigningKeys.AddScopedSigner(sc)
	}
	return cl
}

func newPub(t *testing.T) string {
	t.Helper()
	kp, _ := nkeys.CreateAccount()
	p, _ := kp.PublicKey()
	return p
}

func TestHasZoneScopeExemptsOwnerGroupSigners(t *testing.T) {
	group, zone := newPub(t), newPub(t)
	groups := map[string]bool{group: true}
	if HasZoneScope(scopedClaims(t, group), groups) {
		t.Fatal("an owner group signer alone is not a zone policy")
	}
	if !HasZoneScope(scopedClaims(t, zone), groups) {
		t.Fatal("an unknown scoped signer is a zone key")
	}
	if !HasZoneScope(scopedClaims(t, group, zone), groups) {
		t.Fatal("a zone key beside a group signer is still a zone policy")
	}
	if HasZoneScope(nil, groups) {
		t.Fatal("no JWT has no policy")
	}
}
