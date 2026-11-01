// Package natsauth connects to NATS with a user creds file without turning the client into a signing oracle.
//
// The NATS client signs the server-supplied INFO nonce with the user's nkey seed, whatever bytes the server sent. The
// same seed signs every mash mail (AGENT-SENDER-V1) and registration (AGENT-ID-V1), so a hostile server could send a
// chosen mail signed-input as its "nonce" and collect a valid mail signature (SAP §13.12). A real nats-server nonce is
// 15 characters of base64 raw-URL; the callbacks here sign nothing else. Every mash signed input holds newlines, which
// the nonce alphabet excludes, so no mash signature can be produced for a nonce.
package natsauth

import (
	"fmt"
	"os"
	"regexp"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// nonceRE is the shape of a server nonce: nats-server sends 15 base64 raw-URL characters. The range leaves room for
// other server builds without admitting anything with a newline or other punctuation.
var nonceRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,32}$`)

// CheckNonce refuses anything that is not shaped like a server nonce.
func CheckNonce(nonce []byte) error {
	if !nonceRE.Match(nonce) {
		return fmt.Errorf("natsauth: refusing to sign a connect challenge that is not a server nonce (%d bytes)", len(nonce))
	}
	return nil
}

func sigFor(read func() ([]byte, error)) nats.SignatureHandler {
	return func(nonce []byte) ([]byte, error) {
		if err := CheckNonce(nonce); err != nil {
			return nil, err
		}
		raw, err := read()
		if err != nil {
			return nil, err
		}
		kp, err := nkeys.ParseDecoratedNKey(raw)
		if err != nil {
			return nil, fmt.Errorf("natsauth: no usable user seed: %w", err)
		}
		defer kp.Wipe()
		return kp.Sign(nonce)
	}
}

func jwtFor(read func() ([]byte, error)) nats.UserJWTHandler {
	return func() (string, error) {
		raw, err := read()
		if err != nil {
			return "", err
		}
		return nkeys.ParseDecoratedJWT(raw)
	}
}

// UserCredentials is nats.UserCredentials for a creds file (read at each connect, so a rotated file is picked up),
// refusing to sign a nonce that is not a server nonce.
func UserCredentials(file string) nats.Option {
	read := func() ([]byte, error) { return os.ReadFile(file) }
	return nats.UserJWT(jwtFor(read), sigFor(read))
}

// UserCredentialBytes is nats.UserCredentialBytes with the same refusal.
func UserCredentialBytes(creds []byte) nats.Option {
	read := func() ([]byte, error) { return append([]byte(nil), creds...), nil } // a copy: Wipe zeroes the parsed seed, which aliases its input
	return nats.UserJWT(jwtFor(read), sigFor(read))
}
