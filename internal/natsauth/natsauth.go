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
	"time"

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

// isLoopbackHost reports whether host is localhost or a loopback IP literal (127.0.0.0/8, ::1).
func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

// RequireTLS is the option set for a connection that carries admin or system credentials. The connection must use TLS:
// nats.Secure() makes it fail when the server does not offer TLS, and a plain nats:// or ws:// URL to a host that is
// not localhost or a loopback IP literal is refused before any connection is made (use tls://).
//
// insecureLoopback (the insecure_loopback setting) is the one explicit opt-in for plain text: it returns no option, so a
// plain server is accepted, but only when every host of url is localhost or a loopback IP literal; any other host is
// refused.
func RequireTLS(url string, insecureLoopback bool) ([]nats.Option, error) {
	for _, e := range strings.Split(url, ",") {
		scheme, host := serverParts(e)
		loop := isLoopbackHost(host)
		switch {
		case insecureLoopback && !loop:
			return nil, fmt.Errorf("mash: insecure_loopback (MASH_INSECURE_LOOPBACK) allows a plain connection only to localhost or a loopback address, and %q is not one: use tls:// with the server's certificate (tls_ca for a private CA)", host)
		case !insecureLoopback && !loop && (scheme == "nats" || scheme == "ws"):
			return nil, fmt.Errorf("mash: refusing a plain %s:// connection to %q with admin or system credentials: use tls:// with the server's certificate (tls_ca for a private CA). Only a loopback server may be used without TLS, and only with insecure_loopback set", scheme, host)
		}
	}
	// Servers a server advertises are never added to the client's pool: the connection goes only to the configured URL.
	if insecureLoopback {
		return []nats.Option{nats.IgnoreDiscoveredServers(), nats.SetCustomDialer(LoopbackDialer{})}, nil
	}
	return []nats.Option{nats.IgnoreDiscoveredServers(), nats.Secure()}, nil
}

// PlainRemote returns the scheme and host of the first entry of url (a comma-separated server list) that is plain text
// to a host that is not localhost or a loopback IP literal, and false when there is none. Only tls:// and wss:// are
// TLS: the client dials every other scheme (nats://, ws://, an unknown one such as foo://, or none) as plain TCP, so
// all of them count as plain here. An entry with no scheme is nats://, as the client reads it. The host never carries
// userinfo, a path or a query.
func PlainRemote(url string) (scheme, host string, found bool) {
	for _, e := range strings.Split(url, ",") {
		scheme, host := serverParts(e)
		host, _, _ = strings.Cut(host, "?")
		host, _, _ = strings.Cut(host, "#")
		if host == "" || isLoopbackHost(host) || scheme == "tls" || scheme == "wss" {
			continue
		}
		return scheme, host, true
	}
	return "", "", false
}

// RequireEndpointTLS is the option set for an endpoint connection when require_tls (MASH_REQUIRE_TLS) is on. Before any
// connection is made it refuses an entry whose scheme is not nats, tls, ws or wss (the client would dial an unknown
// scheme as plain TCP), and any plain entry (every scheme but tls and wss; none is nats://) to a host that is not
// localhost or a loopback IP literal. The errors name the scheme or the host, never the URL's userinfo. When any entry
// is tls:// or wss://, the whole list requires TLS: nats.Secure() makes the connection fail if the server does not
// offer it. Otherwise the connection is plain and every entry is loopback, so it is pinned to loopback at dial time
// (LoopbackDialer): localhost is checked again against the address it resolves to, at every connect and reconnect.
// Servers a server advertises are never added to the pool, so the check covers every address the client dials.
func RequireEndpointTLS(url string) ([]nats.Option, error) {
	for _, e := range strings.Split(url, ",") {
		switch scheme, _ := serverParts(e); scheme {
		case "nats", "tls", "ws", "wss":
		default:
			return nil, fmt.Errorf("mash: MASH_REQUIRE_TLS (require_tls) is set: unsupported scheme %q: use tls:// with the server's certificate (tls_ca for a private CA)", scheme)
		}
	}
	if scheme, host, ok := PlainRemote(url); ok {
		return nil, fmt.Errorf("mash: MASH_REQUIRE_TLS (require_tls) is set: refusing a plain %s:// connection to %q: use tls:// with the server's certificate (tls_ca for a private CA)", scheme, host)
	}
	opts := []nats.Option{nats.IgnoreDiscoveredServers()}
	for _, e := range strings.Split(url, ",") {
		if scheme, _ := serverParts(e); scheme == "tls" || scheme == "wss" {
			return append(opts, nats.Secure()), nil
		}
	}
	return append(opts, nats.SetCustomDialer(LoopbackDialer{})), nil
}

// baseDial opens the TCP connection for LoopbackDialer; a test replaces it to fake name resolution.
var baseDial = func(network, address string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 2 * time.Second}).Dial(network, address)
}

// LoopbackDialer dials only localhost or a loopback IP literal, at the first connect and at every reconnect. It is the
// guarantee behind insecure_loopback and behind a plain connection under require_tls: whatever address the client is handed, a plain connection with admin or system
// credentials is never opened to a host that is not on this machine.
type LoopbackDialer struct{}

// Dial implements nats.CustomDialer.
func (LoopbackDialer) Dial(network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("mash: a plain connection is allowed only to localhost or a loopback address: refusing to dial %q: %w", address, err)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("mash: a plain connection (insecure_loopback, or require_tls without tls://) is allowed only to localhost or a loopback address: refusing to dial %q", address)
	}
	conn, err := baseDial(network, address)
	if err != nil {
		return nil, err
	}
	// a name such as localhost is checked again against the address it resolved to
	if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !ta.IP.IsLoopback() {
		conn.Close()
		return nil, fmt.Errorf("mash: a plain connection is allowed only to a loopback address: %q resolved to %s", address, ta.IP)
	}
	return conn, nil
}
