package apply

import (
	"context"
	"errors"
	"strings"
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

// AppliedZonePolicy checks every server's copy of the account JWT, not only the newest (ADR 0091).
func TestAppliedZonePolicyChecksEveryCopy(t *testing.T) {
	ks, op, acct := baseEnv(t)
	zone := newPub(t)
	zoned := tokenAt(t, op, scopedClaims2(acct, zone), 1000)
	clean := tokenAt(t, op, jwt.NewAccountClaims(acct), 2000)
	two := ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	ask := func(fk *fake) (bool, error) { return AppliedZonePolicy(context.Background(), fk, ks, 2) }

	if got, err := ask(&fake{servers: two, all: []string{clean, zoned}}); err != nil || !got {
		t.Fatalf("older copy with a zone scope: %v %v", got, err)
	}
	if got, err := ask(&fake{servers: two, all: []string{clean, clean}}); err != nil || got {
		t.Fatalf("clean copies: %v %v", got, err)
	}
	if got, err := ask(&fake{servers: two, all: []string{clean, ""}}); err != nil || got {
		t.Fatalf("an empty reply is fine: %v %v", got, err)
	}
	foreign, _ := nkeys.CreateOperator()
	bad := tokenAt(t, foreign, jwt.NewAccountClaims(acct), 1)
	if got, err := ask(&fake{servers: two, all: []string{clean, bad}}); err == nil || got {
		t.Fatalf("an unverifiable copy: %v %v", got, err)
	}
	if got, err := ask(&fake{servers: two, all: []string{zoned}}); err == nil || got || !strings.Contains(err.Error(), "only 1 of 2 servers answered: a server may be down (bring it back and run the command again), or the server count is wrong (list every server under `servers` in the setup file and run mashctl init --config)") {
		t.Fatalf("fewer replies than servers: %v %v", got, err)
	}
	if err := RefuseZonePolicy(context.Background(), &fake{servers: two, all: []string{zoned}}, ks, 2, errors.New("applied")); err == nil || strings.Contains(err.Error(), "zone") || !strings.HasPrefix(err.Error(), "only 1 of 2 servers answered: ") {
		t.Fatalf("the refusal names zones or the wrong fix: %v", err)
	}
	if got, err := ask(&fake{servers: two, serversErr: errors.New("boom"), all: []string{zoned, zoned}}); err == nil || got {
		t.Fatalf("servers error: %v %v", got, err)
	}
	// an owner group signer alone is not a zone policy
	kp, err := ks.GroupKey("foo.c", true)
	if err != nil {
		t.Fatal(err)
	}
	grp := tokenAt(t, op, scopedClaims2(acct, pubOf(t, kp)), 3000)
	kp.Wipe()
	if got, err := ask(&fake{servers: two, all: []string{grp, grp}}); err != nil || got {
		t.Fatalf("group-key-only copy: %v %v", got, err)
	}
}

func scopedClaims2(acct string, keys ...string) *jwt.AccountClaims {
	cl := jwt.NewAccountClaims(acct)
	for _, k := range keys {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = k, "r"
		cl.SigningKeys.AddScopedSigner(sc)
	}
	return cl
}
