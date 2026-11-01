package natsauth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
)

// notJWTCreds are the creds-file shapes of servers mash does not support, and a JWT whose seed is missing.
func notJWTCreds(t *testing.T) map[string]string {
	t.Helper()
	acct, _ := nkeys.CreateAccount()
	user, _ := nkeys.CreateUser()
	pub, _ := user.PublicKey()
	tok, err := jwt.NewUserClaims(pub).Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := user.Seed()
	return map[string]string{
		"password line":    "alice:s3cretpassw0rd\n",
		"bare token":       "s3cr3ttokenvalue\n",
		"plain nkey seed":  string(seed) + "\n",
		"empty file":       "",
		"garbage":          "\x00\xff not a creds file",
		"JWT without seed": tok + "\n",
	}
}

func TestCheckCredsNamesOperatorModeJWT(t *testing.T) {
	for name, body := range notJWTCreds(t) {
		t.Run(name, func(t *testing.T) {
			path := "/x/a.creds"
			for _, err := range []error{
				natsauth.CheckCreds(path, []byte(body)),
				func() error { _, err := sap.IdentityFromCredsAt(path, []byte(body)); return err }(),
			} {
				if err == nil || !strings.Contains(err.Error(), "operator-mode JWT credentials") || !strings.Contains(err.Error(), path) {
					t.Fatalf("got %v", err)
				}
				if !errors.Is(err, natsauth.ErrNotJWTCreds) {
					t.Fatalf("not ErrNotJWTCreds: %v", err)
				}
				if strings.Contains(err.Error(), "s3cretpassw0rd") || strings.Contains(err.Error(), "s3cr3ttokenvalue") {
					t.Fatalf("error leaks the file content: %v", err)
				}
			}
			if _, err := sap.IdentityFromCreds([]byte(body)); err == nil || !strings.Contains(err.Error(), "operator-mode JWT credentials") {
				t.Fatalf("IdentityFromCreds: %v", err)
			}
		})
	}
}
