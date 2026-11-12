package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
)

func TestKeyRevokedCoversEveryServer(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	u := userKey(t)
	clean := fk.token
	ac := jwt.NewAccountClaims(apub)
	ac.Revocations = jwt.RevocationList{}
	ac.Revocations.Revoke(u, time.Unix(1_700_000_000, 0))
	revoked, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	two := ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	p := RevokeParams{Keys: ks, Resolver: fk, Servers: 2}
	fk.servers = two

	// one copy lacks the revocation and one has it: revoked, whichever server answers first
	for _, all := range [][]string{{clean, revoked}, {revoked, clean}} {
		fk.all = all
		if got, err := KeyRevoked(context.Background(), p, u); err != nil || !got {
			t.Fatalf("mixed copies %d: %v %v", len(all), got, err)
		}
	}
	// every copy clean
	fk.all = []string{clean, clean}
	if got, err := KeyRevoked(context.Background(), p, u); err != nil || got {
		t.Fatalf("clean: %v %v", got, err)
	}
	// a silent server: refuse, even though the one that answered is clean
	fk.all = []string{clean}
	if got, err := KeyRevoked(context.Background(), p, u); err == nil || got || !strings.Contains(err.Error(), "only 1 of 2") {
		t.Fatalf("silent: %v %v", got, err)
	}
	// a server with no JWT
	fk.all = []string{clean, ""}
	if _, err := KeyRevoked(context.Background(), p, u); err == nil {
		t.Fatal("a server holding no JWT was read as clean")
	}
	// an unverifiable copy (another account's)
	other := userKey(t)
	bad, _ := jwt.NewAccountClaims(other).Encode(op)
	fk.all = []string{clean, bad}
	if _, err := KeyRevoked(context.Background(), p, u); err == nil {
		t.Fatal("an unverifiable copy was skipped")
	}
	// an incomplete set: --servers says 3 but two replied
	fk.all = []string{clean, clean}
	p3 := p
	p3.Servers = 3
	if _, err := KeyRevoked(context.Background(), p3, u); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("incomplete set: %v", err)
	}
	// a refused server set
	fk.serversErr = fmt.Errorf("%w: too many", ErrServerSetRefused)
	if _, err := KeyRevoked(context.Background(), p, u); !errors.Is(err, ErrServerSetRefused) {
		t.Fatalf("refused set: %v", err)
	}
	if fk.pushes != 0 {
		t.Fatal("KeyRevoked pushed")
	}
}
