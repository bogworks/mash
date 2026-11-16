package credential

import (
	"errors"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// NewKey makes a user key for an endpoint, locally. The seed is the endpoint's private key: the caller keeps it (a
// creds file of mode 0600 is enough) and never sends it anywhere; only publicKey goes in a Request. The caller wipes
// the seed when it is done with it.
func NewKey() (seed []byte, publicKey string, err error) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		return nil, "", errors.New("credential: could not make a user key")
	}
	defer kp.Wipe()
	// Seed returns the key pair's own buffer, which Wipe clears: the caller gets a copy.
	held, err := kp.Seed()
	if err != nil {
		return nil, "", errors.New("credential: could not read the new key")
	}
	seed = append([]byte{}, held...)
	publicKey, err = kp.PublicKey()
	if err != nil {
		clear(seed)
		return nil, "", errors.New("credential: could not read the new key")
	}
	return seed, publicKey, nil
}

// Assemble makes the contents of a creds file from the user JWT Issue returned and the endpoint's own seed. It refuses
// a seed that is not a user seed, a JWT that does not decode as a user JWT, and a JWT whose subject is not the seed's
// public key. No error carries the seed or the JWT.
func Assemble(token string, seed []byte) ([]byte, error) {
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, errors.New("credential: the seed is not a valid NATS seed")
	}
	defer kp.Wipe()
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(pub) {
		return nil, errors.New("credential: the seed is not a user seed")
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return nil, errors.New("credential: the JWT does not decode as a user JWT")
	}
	if uc.Subject != pub {
		return nil, errors.New("credential: the JWT is for another public key than this seed's")
	}
	out, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return nil, errors.New("credential: the creds file could not be made")
	}
	return out, nil
}
