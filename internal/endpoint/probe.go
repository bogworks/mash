package endpoint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/natsauth"
)

// CheckServer says whether a server of cfg.URL answers, without signing in: it connects with no credentials under the
// same TLS rules as an endpoint (a plain URL needs insecure_plaintext, the CA and client certificate apply). A server
// that answers and refuses the missing credentials has answered, so that is no error; so is a connection that is made.
// What it returns is the failure to reach a server, redacted. It opens nothing it leaves open.
func CheckServer(ctx context.Context, cfg config.Config) error {
	tlsReq, err := EndpointTLS(cfg)
	if err != nil {
		return err
	}
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		nats.Name("mash doctor"), nats.NoReconnect())
	nc, err := connect(ctx, cfg.URL, opts...)
	if err == nil {
		nc.Close()
		return nil
	}
	if isAuthFailure(err) {
		return nil
	}
	werr := fmt.Errorf("mash: connect %s: %w", config.RedactURL(cfg.URL), config.RedactErr(natsauth.TLSHint(err), cfg.URL))
	if kind := classifyServerError(err); kind != nil {
		return &ServerError{Kind: kind, Err: werr}
	}
	return werr
}

// The kinds of failure CheckServer tells apart, for errors.Is: each has its own text and fix in doctor, and is not
// "the server did not answer".
var (
	// ErrServerCertUntrusted: the server answered a TLS handshake with a certificate the client does not trust (a
	// self-signed or unknown CA, an expired certificate, a name that does not match).
	ErrServerCertUntrusted = errors.New("the server's certificate is not trusted")
	// ErrServerNotTLS: the address is tls:// but what answered does not speak TLS (a plain server, or not a NATS
	// server at all).
	ErrServerNotTLS = errors.New("the server does not speak TLS")
	// ErrServerRefusedClientCert: the server refused the client certificate, or requires one and got none.
	ErrServerRefusedClientCert = errors.New("the server refused the client certificate")
)

// ServerError is CheckServer's failure when it is one of the kinds above: Kind is the sentinel, Err the full error.
type ServerError struct {
	Kind error
	Err  error
}

func (e *ServerError) Error() string   { return e.Err.Error() }
func (e *ServerError) Unwrap() []error { return []error{e.Kind, e.Err} }

// classifyServerError sorts a failure to connect into the kinds of ServerError; nil when it is none of them.
func classifyServerError(err error) error {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	var rec tls.RecordHeaderError
	msg := err.Error()
	switch {
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host), strings.Contains(msg, "x509:"):
		return ErrServerCertUntrusted
	case errors.As(err, &rec), errors.Is(err, nats.ErrSecureConnWanted), strings.Contains(msg, "first record does not look like a TLS handshake"),
		strings.Contains(msg, "secure connection not available"):
		return ErrServerNotTLS
	case strings.Contains(msg, "remote error: tls:"), strings.Contains(msg, "closed by remote after TLS handshake"):
		return ErrServerRefusedClientCert
	}
	return nil
}

// RefusedFile is the file, in the endpoint's spool directory (SpoolDir of its configuration), that holds the number of
// unverified messages the endpoint refused. The contract with its writer (S7's refusing serve, which does not exist
// yet: nothing writes the file today):
//
//   - the name is "refused-unverified", directly in the spool directory;
//   - the content is one line, a non-negative whole number in decimal text, such as "3"; the trailing newline is
//     optional and surrounding white space is ignored;
//   - the writer keeps the running total, and replaces the file whole (write a temporary file in the same directory,
//     then rename it), so a reader never sees half a number.
const RefusedFile = "refused-unverified"

// RefusedCount is the number of unverified messages refused for the endpoint cfg configures: what RefusedFile holds
// (see there for the format and who writes it). A missing file is 0; so is an empty or unreadable file, and so is
// anything that is not one non-negative whole number (garbage, a negative number, several lines), with no error:
// doctor never fails over advice.
func RefusedCount(cfg config.Config) int {
	d, err := SpoolDir(cfg)
	if err != nil {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(d, RefusedFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
