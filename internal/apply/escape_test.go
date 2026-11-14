package apply

import (
	"context"
	"github.com/nats-io/nkeys"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

const hostileRole = "legacy\x1b[2K\r\nREFUSED: none"

func noRawControls(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\x1b' || r == '\r' {
			t.Fatalf("%s carries a raw control character %q:\n%q", what, r, s)
		}
	}
}

// A hostile server's scoped-signer role must reach neither the revoke refusal nor the drift findings raw.
func TestHostileRoleIsEscaped(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	sk, _ := nkeys.CreateAccount()
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pubOf(t, sk), hostileRole
		cl.SigningKeys.AddScopedSigner(sc)
	})
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk}, []string{userKey(t)})
	if err == nil {
		t.Fatal("revoked over a scoped signer")
	}
	noRawControls(t, "revoke refusal", err.Error())
	if !strings.Contains(err.Error(), `legacy\x1b[2K\x0d\x0aREFUSED: none`) {
		t.Fatalf("escaped role missing:\n%s", err)
	}
	fs, err := AccountFindings(context.Background(), fk, ks, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		noRawControls(t, "finding", f.Subject+f.Detail)
	}
}

// An ack message and server name from a hostile server are escaped in the refusal text.
func TestHostileAckIsEscaped(t *testing.T) {
	_, fk := setup(t)
	fk.acks = []ServerAck{{Server: "s1\x1b[2K", Code: 500, Message: "bad\r\n"}}
	_, why := pushAndCheck(context.Background(), fk, "tok", nil)
	noRawControls(t, "push refusal", why)
}
