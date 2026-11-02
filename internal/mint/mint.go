// Package mint makes the NATS user JWT and creds of one policy endpoint: signed by the endpoint's zone scoped signing
// key, carrying the endpoint's compiled tags and nothing else (the zone key's template grants the permissions).
// It does no network I/O; the keys come through the Keys seam (ADR 0052, ADR 0053).
package mint

import (
	"errors"
	"fmt"
	"reflect"

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

// Mint issues the user JWT of endpoint name of c. With userSeed nil it makes a fresh user key and creates the zone's
// signing key if the zone is new; with a user seed (a re-issue) it keeps that user key, and a missing zone key is an
// error, since apply never made it.
func Mint(keys Keys, c *policy.Compiled, name string, userSeed []byte) (*Minted, error) {
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
	zoneKP, err := keys.ZoneKey(ep.Zone, !reissue)
	if err != nil {
		if reissue {
			return nil, fmt.Errorf("mint: zone %q has no signing key (run mashctl apply first): %w", ep.Zone, err)
		}
		return nil, err
	}
	defer zoneKP.Wipe()

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
	// such users, and a populated set makes it not apply or the user fail to connect.
	uc.UserPermissionLimits = jwt.UserPermissionLimits{}
	uc.Tags.Add(ep.Tags...)
	if !reflect.DeepEqual(uc.UserPermissionLimits, jwt.UserPermissionLimits{}) {
		return nil, errors.New("mint: a scoped user must carry no permission limits")
	}
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
