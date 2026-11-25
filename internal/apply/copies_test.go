package apply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// AccountClaimsCopies returns every distinct verified copy, newest first, and keeps CurrentAccountClaims's checks.
func TestAccountClaimsCopies(t *testing.T) {
	ks, op, acct := baseEnv(t)
	trust, _ := LoadTrust(ks)
	cl := jwt.NewAccountClaims(acct)
	old, newest := tokenAt(t, op, cl, 1000), tokenAt(t, op, cl, 3000)
	two := ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	run := func(set ServerSet, servers int, copies ...string) ([]*jwt.AccountClaims, error) {
		return AccountClaimsCopies(context.Background(), &multi{fake: &fake{servers: set}, copies: copies}, acct, trust, servers)
	}

	got, err := run(two, 2, old, newest)
	if err != nil || len(got) != 2 || got[0].IssuedAt != 3000 || got[1].IssuedAt != 1000 {
		t.Fatalf("%v %+v", err, got)
	}
	// agreement is one copy; a server with no JWT is an error, not a skipped reply
	if got, err = run(two, 2, newest, newest); err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err = run(two, 2, newest, ""); !errors.Is(err, ErrServerHasNoAccountJWT) || !strings.Contains(err.Error(), "1 of 2 servers hold no JWT") {
		t.Fatalf("%v", err)
	}
	// no JWT anywhere, a silent server, an incomplete set and a copy that does not verify are errors
	if _, err = run(two, 2, "", ""); !errors.Is(err, ErrServerHasNoAccountJWT) || !strings.Contains(err.Error(), "no server holds a JWT") {
		t.Fatalf("%v", err)
	}
	if _, err = run(two, 2, newest); err == nil || !strings.Contains(err.Error(), "only 1 of 2 servers answered") {
		t.Fatalf("%v", err)
	}
	if _, err = run(ServerSet{Names: []string{"s1"}, Expected: 1}, 2, newest); err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("%v", err)
	}
	stranger, _ := nkeys.CreateOperator()
	if _, err = run(two, 2, newest, tokenAt(t, stranger, cl, 2000)); err == nil || !strings.Contains(err.Error(), "refusing the resolver's account JWT") {
		t.Fatalf("%v", err)
	}
	// CurrentAccountClaims still answers the newest
	cur, err := CurrentAccountClaims(context.Background(), &multi{fake: &fake{servers: two}, copies: []string{old, newest}}, acct, trust, 2)
	if err != nil || cur.IssuedAt != 3000 {
		t.Fatalf("%v %+v", err, cur)
	}
}
