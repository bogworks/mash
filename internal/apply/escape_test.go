package apply

import (
	"bytes"
	"context"
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

// A hostile server's scoped-signer role must reach neither the apply output nor the drift findings raw.
func TestHostileRoleIsEscaped(t *testing.T) {
	ks, fk, c, scoped, _, _ := unknownSetup(t)
	op, _, _ := ks.Operator()
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = scoped, hostileRole
		cl.SigningKeys.AddScopedSigner(sc)
	})
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	p := c
	p.Mode, p.Keys, p.Resolver = DryRun, ks, fk
	Run(context.Background(), p, &out)
	noRawControls(t, "apply output", out.String())
	if !strings.Contains(out.String(), `legacy\x1b[2K\x0d\x0aREFUSED: none`) {
		t.Fatalf("escaped role missing:\n%s", out.String())
	}
	fs, err := AccountFindings(context.Background(), Params{Compiled: c.Compiled, Keys: ks, Resolver: fk})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		noRawControls(t, "finding", f.Subject+f.Detail)
	}
}

// An ack message and server name from a hostile server are escaped in the refusal text.
func TestHostileAckIsEscaped(t *testing.T) {
	ks, fk, c := setup(t)
	_ = ks
	_ = c
	fk.acks = []ServerAck{{Server: "s1\x1b[2K", Code: 500, Message: "bad\r\n"}}
	_, why := pushAndCheck(context.Background(), fk, "tok", nil)
	noRawControls(t, "push refusal", why)
}
