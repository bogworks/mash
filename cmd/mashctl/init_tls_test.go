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

// newTLSIniter is an initer whose source server (operator mode, full resolver) listens with TLS. The initer's
// environment has no MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION. url is the tls:// URL of the server.
func newTLSIniter(t *testing.T) (a *initer, url, caPath string) {
	t.Helper()
	caPath, certPath, keyPath := writeTestPKI(t, t.TempDir())
	e := newApplyEnvOpts(t, nil, func(o *server.Options) {
		cfg, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: certPath, KeyFile: keyPath})
		if err != nil {
			t.Fatal(err)
		}
		o.TLSConfig, o.TLS, o.TLSTimeout = cfg, true, 5
	})
	a = newIniter(t, e)
	delete(a.env, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
	url = fmt.Sprintf("tls://127.0.0.1:%d", e.srv.Addr().(*net.TCPAddr).Port)
	a.vals["servers"] = []string{url}
	return a, url, caPath
}

func TestInitTLSTrustedCADryRun(t *testing.T) {
	a, url, ca := newTLSIniter(t)
	a.vals["ca"] = ca
	before := tree(t, a.root)
	out, errb, code := a.init("--dry-run")
	if want := "Would initialize mash on " + url + " for owner alice\nNothing was changed.\n"; code != 0 || errb != "" || out != want {
		t.Fatalf("dry run over TLS: %d\n%q\n%q", code, out, errb)
	}
	sameTree(t, before, tree(t, a.root))
	// and for real: config.json records the CA
	if out, errb, code = a.init(); code != 0 {
		t.Fatalf("init over TLS: %d\n%s\n%s", code, out, errb)
	}
	if cj := mustRead(t, filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")); !strings.Contains(string(cj), `"ca": "`+ca+`"`) {
		t.Errorf("config.json lacks the ca:\n%s", cj)
	}
}

func TestInitTLSUntrustedCARefused(t *testing.T) {
	a, _, _ := newTLSIniter(t) // no ca in the setup file: system roots do not trust the test CA
	a.refuses(1, []string{"not trusted", "\"ca\""}, nil)
	// a CA that did not sign the server's certificate is refused the same way
	otherCA, _, _ := writeTestPKI(t, t.TempDir())
	a.refuses(1, []string{"not trusted"}, map[string]any{"ca": otherCA})
}

func TestInitPlainWithoutSkipEncryptionRefused(t *testing.T) {
	// A plain nats:// URL to a non-loopback host is refused by RequireTLS before any connection is made. (A plain URL
	// to the loopback address of a TLS server is not refused: nats.Secure upgrades it to TLS.)
	a, _, ca := newTLSIniter(t)
	a.vals["ca"] = ca
	a.refuses(1, []string{"refusing a plain nats://", "MASH_CA", "nothing was changed"}, map[string]any{"servers": []string{"nats://example.invalid:4222"}})
}
