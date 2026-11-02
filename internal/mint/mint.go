// Package mint makes the NATS user JWT and creds of one policy endpoint: signed by the endpoint's zone scoped signing
// key, carrying the endpoint's compiled tags and nothing else (the zone key's template grants the permissions).
// It does no network I/O; the keys come through the Keys seam (ADR 0052, ADR 0053).
package mint

import (
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/policy"
)

// Keys is the key store Mint signs with; *ctlkeys.Authority satisfies it.
type Keys interface {
	Account() (nkeys.KeyPair, string, error)
	ZoneKey(zone string, create bool) (nkeys.KeyPair, error)
}

// Minted is one issued user. Creds holds the user seed: never log or print it.
type Minted struct {
	Name       string
	Zone       string
	UserPub    string
	AccountPub string
	JWT        string
	Creds      []byte // the creds file contents (JWT and seed): a secret
	Tags       []string
}

// ScopeVerifier is implemented by a Keys that can see the account JWT the zone keys are published in. Mint calls it
// with the zone key's public key and the zone, and it returns an error unless that key is a jwt.UserScope signing
// key of the account. The key store alone (ctlkeys.Authority) holds seeds, not the account JWT, so it does not
// implement it; Mint then relies on the checks in checkZoneKey, and `policy apply` is what publishes and verifies
// the scope in the account JWT.
type ScopeVerifier interface {
	VerifyZoneScope(zone, zonePub string) error
}

// checkZoneKey refuses to mint with a key that would give users no restrictions. Users minted under a scoped
// signing key carry empty permission limits and take the key's template; signed by a plain account key, they would
// carry empty limits and so be unrestricted. The key must be an account-type key, must not be the account identity
// key itself (which is not scoped), and, where the Keys can see the account JWT, must be a UserScope key in it.
func checkZoneKey(keys Keys, zone string, zoneKP nkeys.KeyPair, acctPub string) error {
	zonePub, err := zoneKP.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(zonePub) {
		return fmt.Errorf("mint: the signing key of zone %q is not an account signing key", zone)
	}
	if zonePub == acctPub {
		return fmt.Errorf("mint: the signing key of zone %q is the account identity key, not a scoped signing key: users it signs would have no restrictions", zone)
	}
	if v, ok := keys.(ScopeVerifier); ok {
		if err := v.VerifyZoneScope(zone, zonePub); err != nil {
			return fmt.Errorf("mint: zone %q: %w", zone, err)
		}
	}
	return nil
}

// Mint issues the user JWT of endpoint name of c. With userSeed nil it makes a fresh user key; with a user seed (a
// re-issue) it keeps that user key. The caller decides whether the zone's signing key may be created: with
// createZoneKey false a missing key is an error wrapping ctlkeys.ErrNoZoneKey, since a key made now would not be in
// the account JWT the zone's existing users were issued under.
func Mint(keys Keys, c *policy.Compiled, name string, userSeed []byte, createZoneKey bool) (*Minted, error) {
	var ep *policy.CompiledEndpoint
	for i := range c.Endpoints {
		if c.Endpoints[i].Name == name {
			ep = &c.Endpoints[i]
			break
		}
	}
	if ep == nil {
		return nil, fmt.Errorf("mint: endpoint %q is not declared in the policy", name)
	}
	reissue := userSeed != nil
	acctKP, acctPub, err := keys.Account()
	if err != nil {
		return nil, err
	}
	acctKP.Wipe() // only the public key is needed: users are signed by the zone key
	zoneKP, err := keys.ZoneKey(ep.Zone, createZoneKey)
	if err != nil {
		if !createZoneKey {
			return nil, fmt.Errorf("mint: zone %q has no signing key: %w", ep.Zone, err)
		}
		return nil, err
	}
	defer zoneKP.Wipe()
	if err := checkZoneKey(keys, ep.Zone, zoneKP, acctPub); err != nil {
		return nil, err
	}

	var userKP nkeys.KeyPair
	if reissue {
		userKP, err = nkeys.FromSeed(userSeed)
		if err != nil {
			return nil, errors.New("mint: the user seed is not a valid NATS seed")
		}
		if pub, err := userKP.PublicKey(); err != nil || !nkeys.IsValidPublicUserKey(pub) {
			userKP.Wipe()
			return nil, errors.New("mint: the seed is not a user seed")
		}
	} else if userKP, err = nkeys.CreateUser(); err != nil {
		return nil, fmt.Errorf("mint: generate user key: %w", err)
	}
	defer userKP.Wipe()
	userPub, err := userKP.PublicKey()
	if err != nil {
		return nil, err
	}
	seed, err := userKP.Seed()
	if err != nil {
		return nil, errors.New("mint: user seed unavailable")
	}
	seed = append([]byte(nil), seed...)
	defer clear(seed)

	uc := jwt.NewUserClaims(userPub)
	uc.Name = name
	uc.IssuerAccount = acctPub
	// A scoped signing key's users carry no permissions or limits of their own: the key's template applies only to
	// such users, and a populated set makes it not apply or the user fail to connect. That makes the key the only
	// thing restricting the user, so the key is checked above (checkZoneKey), not the empty limits set here.
	uc.UserPermissionLimits = jwt.UserPermissionLimits{}
	uc.Tags.Add(ep.Tags...)
	token, err := uc.Encode(zoneKP)
	if err != nil {
		return nil, fmt.Errorf("mint: sign: %w", err)
	}
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return nil, fmt.Errorf("mint: creds: %w", err)
	}
	return &Minted{
		Name: name, Zone: ep.Zone, UserPub: userPub, AccountPub: acctPub, JWT: token, Creds: creds,
		Tags: append([]string{}, ep.Tags...),
	}, nil
}
