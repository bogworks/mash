package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// verifyZoneScope checks that a zone's signing key is a scoped signer in the account JWT; a test replaces it.
var verifyZoneScope = verifyZoneScopeLive

// scopeKeys is the key store plus mint.ScopeVerifier: it makes mint.Mint check, before a user is minted for an
// existing zone, that the zone key is published in the account JWT as a jwt.UserScope signer. The connection is
// opened lazily, inside VerifyZoneScope.
type scopeKeys struct {
	*ctlkeys.Authority
	cfg config.Config
	env func(string) string
}

// VerifyZoneScope implements mint.ScopeVerifier.
func (s scopeKeys) VerifyZoneScope(zone, zonePub string) error {
	return verifyZoneScope(context.Background(), s.cfg, s.env, s.Authority, zone, zonePub)
}

// verifyZoneScopeLive reads the account JWT with the system account credentials and verifies it before trusting it.
func verifyZoneScopeLive(ctx context.Context, cfg config.Config, env func(string) string, keys *ctlkeys.Authority, zone, zonePub string) error {
	if _, err := sysCredsPath("", env, cfg); err != nil {
		return errors.New("no system account credentials: set MASH_SYS_CREDS to a file of system account user creds (a server `mashctl init --local` stood up has them in <server dir>/sys.creds); add needs them to verify the zone key's scope in the account JWT")
	}
	_, accountPub, err := keys.Account()
	if err != nil {
		return err
	}
	trust, err := apply.LoadTrust(keys)
	if err != nil {
		return err
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	token, err := apply.NATS{Conn: nc}.Lookup(ctx, accountPub)
	if err != nil {
		return err
	}
	return checkZoneScope(token, trust, accountPub, zone, zonePub)
}

// checkZoneScope verifies the account JWT token and returns an error unless zonePub is a jwt.UserScope signing key in it.
func checkZoneScope(token string, trust apply.Trust, accountPub, zone, zonePub string) error {
	claims, err := apply.VerifyAccountJWT(token, accountPub, trust)
	if err != nil {
		return err
	}
	sc, _ := claims.SigningKeys.GetScope(zonePub)
	if _, ok := sc.(*jwt.UserScope); !ok {
		return fmt.Errorf("the signing key of zone %s is not published in the account JWT as a scoped signer, so users it signs would have no restrictions: run `mashctl policy apply` (and `mashctl drift` to check) before adding to this zone", zone)
	}
	return nil
}
