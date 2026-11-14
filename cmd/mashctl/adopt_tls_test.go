package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// writeTestPKI writes a fresh CA and a server certificate for 127.0.0.1 into dir and returns the CA, cert and key paths.
func writeTestPKI(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mash test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, typ string, der []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return write("ca.pem", "CERTIFICATE", caDER), write("server.pem", "CERTIFICATE", srvDER), write("server.key", "EC PRIVATE KEY", keyDER)
}

// newTLSAdopter is an adopter whose source server (operator mode, full resolver) listens with TLS. The adopter's
// environment has no MASH_INSECURE_LOOPBACK. url is the tls:// URL of the server.
func newTLSAdopter(t *testing.T) (a *adopter, url, caPath string) {
	t.Helper()
	caPath, certPath, keyPath := writeTestPKI(t, t.TempDir())
	e := newApplyEnvOpts(t, nil, func(o *server.Options) {
		cfg, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: certPath, KeyFile: keyPath})
		if err != nil {
			t.Fatal(err)
		}
		o.TLSConfig, o.TLS, o.TLSTimeout = cfg, true, 5
	})
	a = newAdopter(t, e)
	delete(a.env, "MASH_INSECURE_LOOPBACK")
	return a, fmt.Sprintf("tls://127.0.0.1:%d", e.srv.Addr().(*net.TCPAddr).Port), caPath
}

func TestAdoptTLSTrustedCADryRun(t *testing.T) {
	a, url, ca := newTLSAdopter(t)
	a.env["MASH_TLS_CA"] = ca
	before := tree(t, a.root)
	out, errb, code := a.run("adopt", "--server", url, "--owner", "alice", "--account", "MASH",
		"--operator-key", a.opSeed, "--account-key", a.acSeed, "--sys-creds", a.sys, "--dry-run")
	if code != 0 || errb != "" {
		t.Fatalf("dry run over TLS: %d\n%s\n%s", code, out, errb)
	}
	for _, want := range []string{"All checks passed", "nothing is written"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	sameTree(t, before, tree(t, a.root))
}

func TestAdoptTLSUntrustedCARefused(t *testing.T) {
	a, url, _ := newTLSAdopter(t) // no tls_ca: system roots do not trust the test CA
	a.refuses(1, []string{"not trusted", "tls_ca"}, "--server", url, "--dry-run")
}

func TestAdoptPlainWithoutInsecureLoopbackRefused(t *testing.T) {
	// A plain nats:// URL to a non-loopback host is refused by RequireTLS before any connection is made. (A plain URL
	// to the loopback address of a TLS server is not refused: nats.Secure upgrades it to TLS.)
	a, _, ca := newTLSAdopter(t)
	a.env["MASH_TLS_CA"] = ca
	a.refuses(1, []string{"refusing a plain nats://", "tls_ca", "nothing was changed"}, "--server", "nats://example.invalid:4222", "--dry-run")
}
