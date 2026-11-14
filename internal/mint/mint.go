// Package mint makes the NATS user JWT and creds of one endpoint: signed by the account signing key and
// carrying the endpoint's own permission set (admin.Permissions). There is no per-zone scoped signing key and no tag
// (ADR 0091): the address is the fence, and the key that signs a user is the account's own.
// It does no network I/O; the key comes through the Keys seam (ADR 0052).
package mint

import (
	"errors"
	"fmt"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
)

// Keys is the key store Mint signs with; *ctlkeys.Authority satisfies it.
type Keys interface {
	Account() (nkeys.KeyPair, string, error)
}

// Minted is one issued user. Creds holds the user seed: never log or print it.
type Minted struct {
	Name       string
	UserPub    string
	AccountPub string
	JWT        string
	Creds      []byte // the creds file contents (JWT and seed): a secret
}

// Mint issues the user JWT of endpoint name of owner, signed by the account signing key with the permission set of
// admin.Permissions(owner, name). With userSeed nil it makes a fresh user key; with a user seed (a re-issue) it keeps
// that user key.
func Mint(keys Keys, owner, name string, userSeed []byte) (*Minted, error) {
	reissue := userSeed != nil
	acctKP, acctPub, err := keys.Account()
	if err != nil {
		return nil, err
	}
	defer acctKP.Wipe()
	if !nkeys.IsValidPublicAccountKey(acctPub) {
		return nil, errors.New("mint: the account signing key is not an account key")
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
	// The account's own key signs, so the user carries its permissions in the JWT itself.
	uc.Permissions = admin.Permissions(owner, name)
	token, err := uc.Encode(acctKP)
	if err != nil {
		return nil, fmt.Errorf("mint: sign: %w", err)
	}
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return nil, fmt.Errorf("mint: creds: %w", err)
	}
	return &Minted{
		Name: name, UserPub: userPub, AccountPub: acctPub, JWT: token, Creds: creds,
	}, nil
}
