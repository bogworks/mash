// Package natsauth connects to NATS with a user creds file without turning the client into a signing oracle.
//
// The NATS client signs the server-supplied INFO nonce with the user's nkey seed, whatever bytes the server sent. The
// same seed signs every mash mail (AGENT-SENDER-V1) and registration (AGENT-ID-V1), so a hostile server could send a
// chosen mail signed-input as its "nonce" and collect a valid mail signature (SAP §13.12). A real nats-server nonce is
// 15 characters of base64 raw-URL; the callbacks here sign nothing else. Every mash signed input holds newlines, which
// the nonce alphabet excludes, so no mash signature can be produced for a nonce.
package natsauth

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

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

// TLSOptions are the client options for mash's TLS settings: ca is a PEM file of root CAs that replaces the system
// roots, and cert and key are a PEM client certificate and its key. An empty setting adds nothing.
func TLSOptions(ca, cert, key string) []nats.Option {
	var opts []nats.Option
	if ca != "" {
		opts = append(opts, nats.RootCAs(ca))
	}
	if cert != "" || key != "" {
		opts = append(opts, nats.ClientCert(cert, key))
	}
	return opts
}

// TLSHint appends the setting to change to a connect error that is a TLS failure: a server certificate the client
// does not trust points at tls_ca, a server that refuses the client's certificate (or wants one) at tls_cert and tls_key.
// Any other error is returned unchanged.
func TLSHint(err error) error {
	if err == nil {
		return nil
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	msg := err.Error()
	switch {
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host), strings.Contains(msg, "x509:"):
		return fmt.Errorf("%w (the server's certificate is not trusted: set tls_ca or MASH_TLS_CA to the PEM file of the CA that signed it)", err)
	case strings.Contains(msg, "remote error: tls:"), strings.Contains(msg, "closed by remote after TLS handshake"):
		return fmt.Errorf("%w (the server refused the client certificate or requires one: set tls_cert and tls_key, or MASH_TLS_CERT and MASH_TLS_KEY, to a certificate the server's CA signed)", err)
	}
	return err
}
