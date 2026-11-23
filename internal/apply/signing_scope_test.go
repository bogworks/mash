package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

func importCfgSigner(t *testing.T, ks interface {
	ImportSigning(string) (string, bool, error)
}) string {
	t.Helper()
	sk, _ := nkeys.CreateAccount()
	seed, _ := sk.Seed()
	src := filepath.Join(t.TempDir(), "sk")
	if err := os.WriteFile(src, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ks.ImportSigning(src)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestRevokeRefusesAScopedConfiguredSigningKey(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	pub := importCfgSigner(t, ks)
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pub, "red"
		cl.SigningKeys.AddScopedSigner(sc)
	})
	before := fk.token
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil || !strings.Contains(err.Error(), "is a scoped signing key in the account JWT, nothing was changed") {
		t.Fatalf("%v", err)
	}
	if fk.pushes != 0 || fk.token != before {
		t.Fatal("something was pushed")
	}
}

func TestRevokeWarnsWhenTheConfiguredSigningKeyIsNotInTheAccountJWT(t *testing.T) {
	ks, fk := setup(t)
	importCfgSigner(t, ks)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	res, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Now: func() time.Time { return revokeAt }}, []string{userKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Warning, "is not in the account JWT") || !strings.Contains(res.Warning, "refused by the server") {
		t.Fatalf("warning: %q", res.Warning)
	}
	if fk.pushes == 0 {
		t.Fatal("revoke did not push")
	}
}

func TestAccountFindingsConfiguredSigningKey(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	pub := importCfgSigner(t, ks)
	fs, err := AccountFindings(context.Background(), fk, ks, 0)
	if err != nil || len(fs) != 1 || fs[0].Kind != KindUnknownKey || !strings.Contains(fs[0].Detail, "not in the account JWT") {
		t.Fatalf("missing: %+v %v", fs, err)
	}
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) { cl.SigningKeys.Add(pub) })
	if fs, err = AccountFindings(context.Background(), fk, ks, 0); err != nil || len(fs) != 0 {
		t.Fatalf("plain: %+v %v", fs, err)
	}
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pub, "red"
		cl.SigningKeys.AddScopedSigner(sc)
	})
	if fs, err = AccountFindings(context.Background(), fk, ks, 0); err != nil || len(fs) != 1 || fs[0].Kind != KindZonePolicyApplied {
		t.Fatalf("scoped: %+v %v", fs, err)
	}
}

// ADR 0124 / ADR 0158: a foreign scoped signing key (one mashctl did not create) beside the configured plain account
// signing key makes a revocation refuse before any push. The refusal names the scoped key by public key and role; the
// configured plain key is not what is blamed.
func TestSigningKeySet_RevokeRefusesAForeignScopedKeyBesideTheConfiguredPlainKey(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	cfg := importCfgSigner(t, ks)
	foreign, _ := nkeys.CreateAccount()
	fpub, _ := foreign.PublicKey()
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		cl.SigningKeys.Add(cfg)
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = fpub, "other.role"
		cl.SigningKeys.AddScopedSigner(sc)
	})
	before := fk.token
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil {
		t.Fatal("revoke accepted a foreign scoped signing key")
	}
	msg := err.Error()
	if !strings.Contains(msg, "scoped signing key "+fpub+" (role other.role)") || !strings.Contains(msg, "nothing was changed") {
		t.Fatalf("the refusal does not name the scoped key by public key and role: %v", err)
	}
	if strings.Contains(msg, cfg) || strings.Contains(msg, "configured signing_key") {
		t.Fatalf("the configured plain key is blamed: %v", err)
	}
	if fk.pushes != 0 || fk.token != before {
		t.Fatal("something was pushed")
	}
}
