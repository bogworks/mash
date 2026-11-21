package endpoint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
)

func refusedCfg(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	d, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, d
}

// The count of refused unverified messages is what the file holds; anything else, or no file, is none.
func TestRefusedCount(t *testing.T) {
	cfg, dir := refusedCfg(t)
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("no file: %d", n)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]int{"3\n": 3, "12": 12, " 7 \n": 7, "0": 0, "": 0, "many": 0, "-2": 0, "1.5": 0, "5\n6\n": 0, "\x00\xff": 0} {
		if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if n := RefusedCount(cfg); n != want {
			t.Errorf("%q: %d, want %d", body, n, want)
		}
	}
	if n := RefusedCount(config.Config{}); n != 0 {
		t.Errorf("no endpoint: %d", n)
	}
}

// The contract with the writer (S7's refusing serve): the file is "refused-unverified" in the endpoint's spool directory,
// one decimal number, and the reader finds it where SpoolDir says. A fixed name: the writer is built against it.
func TestRefusedFileContract(t *testing.T) {
	if RefusedFile != "refused-unverified" {
		t.Fatalf("the file is %q", RefusedFile)
	}
	cfg, dir := refusedCfg(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "refused-unverified"), []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(cfg); n != 3 {
		t.Fatalf("a writer that follows the contract: %d", n)
	}
	// the file of another endpoint is not this one's
	other := cfg
	other.Name = "carol"
	if n := RefusedCount(other); n != 0 {
		t.Fatalf("another endpoint: %d", n)
	}
}

// The failures CheckServer tells apart, from the errors a TLS connection gives.
func TestClassifyServerError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"unknown authority", x509.UnknownAuthorityError{}, ErrServerCertUntrusted},
		{"wrapped unknown authority", fmt.Errorf("tls: failed to verify certificate: %w", x509.UnknownAuthorityError{}), ErrServerCertUntrusted},
		{"hostname", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "h"}, ErrServerCertUntrusted},
		{"expired", x509.CertificateInvalidError{Reason: x509.Expired}, ErrServerCertUntrusted},
		{"x509 text", errors.New("x509: certificate has expired"), ErrServerCertUntrusted},
		{"record header", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, ErrServerNotTLS},
		{"record header text", errors.New("tls: first record does not look like a TLS handshake"), ErrServerNotTLS},
		{"plain nats server", nats.ErrSecureConnWanted, ErrServerNotTLS},
		{"client cert refused", errors.New("remote error: tls: bad certificate"), ErrServerRefusedClientCert},
		{"nothing listens", errors.New("dial tcp 127.0.0.1:1: connect: connection refused"), nil},
	} {
		if got := classifyServerError(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
	e := &ServerError{Kind: ErrServerNotTLS, Err: errors.New("mash: connect x: boom")}
	if !errors.Is(e, ErrServerNotTLS) || errors.Is(e, ErrServerCertUntrusted) || e.Error() != "mash: connect x: boom" {
		t.Errorf("ServerError: %v", e)
	}
}

// CheckServer says a server answered when something speaks NATS and asks for credentials (or accepts none), and says
// the server did not answer when nothing listens; a plain URL is refused before anything is dialled.
func TestCheckServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	cfg := config.Config{URL: "tls://" + addr, Name: "bob", Owner: "acme", Group: "demo"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := CheckServer(ctx, cfg); err == nil {
		t.Fatal("a closed port answered")
	}
	cfg.URL = "nats://" + addr
	if err := CheckServer(ctx, cfg); err == nil || !strings.Contains(err.Error(), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION") {
		t.Fatalf("a plain URL with no override: %v", err)
	}
}
