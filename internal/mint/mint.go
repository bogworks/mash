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
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/sap"
)

// Keys is the key store Mint signs with; *ctlkeys.Authority satisfies it.
//
// Account is the account IDENTITY key. Signing is the account SIGNING key an operator with strict_signing_key_usage
// requires; it returns ctlkeys.ErrNoSigningKey (tested with errors.Is) when none is configured, and then the identity
// key signs, as before.
type Keys interface {
	Account() (nkeys.KeyPair, string, error)
	Signing() (nkeys.KeyPair, string, error)
}

// signer is the key that signs a user and what the claims then say: with a signing key it is that key and
// issuerAccount is the account's identity public key; without one it is the identity key and issuerAccount is "".
type signer struct {
	kp            nkeys.KeyPair
	acctPub       string
	issuerAccount string
}

func (s signer) wipe() { s.kp.Wipe() }

func loadSigner(keys Keys) (signer, error) {
	acctKP, acctPub, err := keys.Account()
	if err != nil {
		return signer{}, err
	}
	if !nkeys.IsValidPublicAccountKey(acctPub) {
		acctKP.Wipe()
		return signer{}, errors.New("mint: the account signing key is not an account key")
	}
	skp, spub, err := keys.Signing()
	if errors.Is(err, ctlkeys.ErrNoSigningKey) {
		return signer{kp: acctKP, acctPub: acctPub}, nil
	}
	acctKP.Wipe()
	if err != nil {
		return signer{}, err
	}
	if !nkeys.IsValidPublicAccountKey(spub) {
		skp.Wipe()
		return signer{}, errors.New("mint: the account signing key is not an account key")
	}
	return signer{kp: skp, acctPub: acctPub, issuerAccount: acctPub}, nil
}

// Minted is one issued user. Creds holds the user seed: never log or print it.
type Minted struct {
	Name       string
	UserPub    string
	AccountPub string
	JWT        string
	Creds      []byte // the creds file contents (JWT and seed): a secret
}

// Mint issues the user JWT of the endpoint at addr, named by its flat key, signed by the account signing key with the
// permission set of admin.Permissions(addr). With userSeed nil it makes a fresh user key; with a user seed (a re-issue) it keeps
// that user key.
func Mint(keys Keys, addr sap.Address, userSeed []byte) (*Minted, error) {
	reissue := userSeed != nil
	sg, err := loadSigner(keys)
	if err != nil {
		return nil, err
	}
	defer sg.wipe()
	acctPub := sg.acctPub

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

	token, err := signUser(sg, addr, userPub)
	if err != nil {
		return nil, err
	}
	name := addr.String()
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return nil, fmt.Errorf("mint: creds: %w", err)
	}
	return &Minted{
		Name: name, UserPub: userPub, AccountPub: acctPub, JWT: token, Creds: creds,
	}, nil
}

// SignUser signs the user JWT of the endpoint at addr for userPub, a user public key the endpoint made: no seed is
// involved. The claims are Mint's: the dotted address as name, admin.Permissions(addr), no tag, signed by the account
// signing key. It returns the JWT and the account public key.
func SignUser(keys Keys, addr sap.Address, userPub string) (token, acctPub string, err error) {
	if !nkeys.IsValidPublicUserKey(userPub) {
		return "", "", errors.New("mint: not a user public key")
	}
	sg, err := loadSigner(keys)
	if err != nil {
		return "", "", err
	}
	defer sg.wipe()
	token, err = signUser(sg, addr, userPub)
	return token, sg.acctPub, err
}

func signUser(sg signer, addr sap.Address, userPub string) (string, error) {
	uc := jwt.NewUserClaims(userPub)
	uc.Name = addr.String()
	// The account's own key signs, so the user carries its permissions in the JWT itself. A signing key names the
	// account it signs for (issuer_account); the identity key is the account and names nothing.
	uc.Permissions = *admin.Permissions(addr)
	uc.IssuerAccount = sg.issuerAccount
	token, err := uc.Encode(sg.kp)
	if err != nil {
		return "", fmt.Errorf("mint: sign: %w", err)
	}
	return token, nil
}
