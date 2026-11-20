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
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// ErrNotJWTCreds marks a creds file that does not hold a user JWT and its seed.
var ErrNotJWTCreds = errors.New("not operator-mode JWT credentials")

// CredsError says a creds file is not operator-mode JWT credentials, wrapping cause (never the file's content). path
// names the file; "" says "the creds file".
func CredsError(path string, cause error) error {
	what := "the creds file"
	if path != "" {
		what = path
	}
	return fmt.Errorf("mash: %s is not operator-mode JWT credentials (a .creds file with a user JWT and its seed, from `nsc` or Synadia Cloud); token, user/password and plain-nkey servers are not supported: %w: %w", what, ErrNotJWTCreds, cause)
}

// CheckCreds returns a CredsError unless raw holds a user JWT and its seed.
func CheckCreds(path string, raw []byte) error {
	tok, err := nkeys.ParseDecoratedJWT(raw)
	if err == nil {
		_, err = jwt.DecodeUserClaims(tok)
	}
	if err != nil {
		return CredsError(path, fmt.Errorf("no user JWT: %w", err))
	}
	kp, err := nkeys.ParseDecoratedNKey(raw)
	if err != nil {
		return CredsError(path, fmt.Errorf("no user seed: %w", err))
	}
	kp.Wipe()
	return nil
}

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

func sigFor(path string, read func() ([]byte, error)) nats.SignatureHandler {
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
			return nil, CredsError(path, fmt.Errorf("no usable user seed: %w", err))
		}
		defer kp.Wipe()
		return kp.Sign(nonce)
	}
}

func jwtFor(path string, read func() ([]byte, error)) nats.UserJWTHandler {
	return func() (string, error) {
		raw, err := read()
		if err != nil {
			return "", err
		}
		tok, err := nkeys.ParseDecoratedJWT(raw)
		if err != nil || !strings.HasPrefix(tok, "eyJ") || strings.Count(tok, ".") != 2 { // the shape only; CheckCreds decodes the claims
			return "", CredsError(path, errors.New("no user JWT"))
		}
		return tok, nil
	}
}

// UserCredentials is nats.UserCredentials for a creds file (read at each connect, so a rotated file is picked up),
// refusing to sign a nonce that is not a server nonce.
func UserCredentials(file string) nats.Option {
	read := func() ([]byte, error) { return os.ReadFile(file) }
	return nats.UserJWT(jwtFor(file, read), sigFor(file, read))
}

// UserCredentialBytes is nats.UserCredentialBytes with the same refusal.
func UserCredentialBytes(creds []byte) nats.Option {
	read := func() ([]byte, error) { return append([]byte(nil), creds...), nil } // a copy: Wipe zeroes the parsed seed, which aliases its input
	return nats.UserJWT(jwtFor("", read), sigFor("", read))
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
// does not trust points at "ca" (MASH_CA), a server that refuses the client's certificate (or wants one) at tls_cert and tls_key.
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
		return fmt.Errorf("%w (the server's certificate is not trusted: set \"ca\" or MASH_CA to the PEM file of the CA that signed it)", err)
	case strings.Contains(msg, "remote error: tls:"), strings.Contains(msg, "closed by remote after TLS handshake"):
		return fmt.Errorf("%w (the server refused the client certificate or requires one: set tls_cert and tls_key, or MASH_TLS_CERT and MASH_TLS_KEY, to a certificate the server's CA signed)", err)
	}
	return err
}

const noServersHint = "(check MASH_SERVER and that at least one listed server is up)"

// NoServersHint appends what to check to a "no servers available for connection" error: the URL list, and that one
// listed server is up. Any other error is returned unchanged.
func NoServersHint(err error) error {
	if err == nil || strings.HasSuffix(err.Error(), noServersHint) {
		return err
	}
	if errors.Is(err, nats.ErrNoServers) || strings.Contains(err.Error(), "no servers available for connection") {
		return fmt.Errorf("%w %s", err, noServersHint)
	}
	return err
}

// serverParts splits one server URL (a scheme is optional; a port is optional) into its lower-case scheme ("nats" when
// there is none) and its host.
func serverParts(entry string) (scheme, host string) {
	e := strings.TrimSpace(entry)
	scheme = "nats"
	if sch, rest, ok := strings.Cut(e, "://"); ok {
		scheme, e = strings.ToLower(sch), rest
	}
	e, _, _ = strings.Cut(e, "/")
	if i := strings.LastIndex(e, "@"); i >= 0 {
		e = e[i+1:]
	}
	if h, _, err := net.SplitHostPort(e); err == nil {
		return scheme, h
	}
	return scheme, strings.Trim(e, "[]")
}

// plainHint is the tail of every refusal of a plain connection: the setting that allows it and the fix.
const plainHint = "set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (MASH_CA or \"ca\" for a private CA)"

// plainRefusal is the error for a plain scheme (every one but tls and wss) to host, with no override. It names the
// scheme and the host, never userinfo.
func plainRefusal(scheme, host string) error {
	return fmt.Errorf("mash: refusing a plain %s:// connection to %q: every mash connection requires TLS, to any host (loopback included); %s", scheme, host, plainHint)
}

// PlainWarning is the line printed to standard error when a plain connection to host is made under insecure_plaintext.
func PlainWarning(host string) string {
	return fmt.Sprintf("mash: WARNING: plain-text connection to %s (insecure_plaintext is set); anyone on the network can read mail and obtain signatures", host)
}

// RequireTLS is the one rule for every connection mash and mashctl make (admin, system and endpoint), applied to url (a
// comma-separated server list) before anything is connected. Every entry must be tls:// or wss://, to any host
// (loopback included). An entry whose scheme is not nats, tls, ws or wss is refused as unsupported in every case (the
// client would dial it as plain TCP). A plain entry (nats://, ws://, or no scheme) is refused unless insecurePlaintext
// is set. The errors name the scheme and the host, never the URL's userinfo.
//
// With insecurePlaintext set, a plain entry is allowed to any host: no TLS is forced (a list may mix plain and TLS
// entries, and each works as its scheme says), and warning names the first plain host ("" when no entry is plain); the
// caller prints it to standard error. Otherwise the list is all TLS and nats.Secure() makes the connection fail when
// the server does not offer TLS. Servers a server advertises are never added to the client's pool.
func RequireTLS(url string, insecurePlaintext bool) (opts []nats.Option, warning string, err error) {
	var plainHost string
	var plain, secure bool
	for _, e := range strings.Split(url, ",") {
		if strings.TrimSpace(e) == "" {
			continue
		}
		scheme, host := serverParts(e)
		host, _, _ = strings.Cut(host, "?")
		host, _, _ = strings.Cut(host, "#")
		switch scheme {
		case "tls", "wss":
			secure = true
		case "nats", "ws":
			if !insecurePlaintext {
				return nil, "", plainRefusal(scheme, host)
			}
			if !plain {
				plainHost = host
			}
			plain = true
		default:
			return nil, "", fmt.Errorf("mash: unsupported scheme %q for %q (use tls://, or wss://): the client would dial it as plain text; the fix is to use a tls:// URL with the server's certificate (MASH_CA or \"ca\" for a private CA)", scheme, host)
		}
	}
	opts = []nats.Option{nats.IgnoreDiscoveredServers()}
	if plain {
		return opts, PlainWarning(plainHost), nil
	}
	if secure {
		opts = append(opts, nats.Secure())
	}
	return opts, "", nil
}
