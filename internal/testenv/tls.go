package testenv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pki is a throwaway CA.
type pki struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newPKI() (*pki, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mash test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &pki{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// issue returns the PEM certificate and key of a leaf signed by the CA, for the server (127.0.0.1) or a client.
func (p *pki) issue(serial int64, server bool) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "mash test leaf"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if server {
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, p.cert, &key.PublicKey, p.key)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), nil
}

func writePEM(dir, name string, b []byte) (string, error) {
	p := filepath.Join(dir, name)
	return p, os.WriteFile(p, b, 0o600)
}

// setupTLS makes the CA, the server certificate and, when o.RequireClientCert, a client certificate, in dir.
func (e *Env) setupTLS(dir string, o Options) error {
	ca, err := newPKI()
	if err != nil {
		return err
	}
	cert, key, err := ca.issue(2, true)
	if err != nil {
		return err
	}
	for _, f := range []struct {
		dst  *string
		name string
		data []byte
	}{{&e.CAFile, "ca.pem", ca.pem}, {&e.serverCert, "server.pem", cert}, {&e.serverKey, "server-key.pem", key}} {
		if *f.dst, err = writePEM(dir, f.name, f.data); err != nil {
			return err
		}
	}
	if !o.RequireClientCert {
		return nil
	}
	cert, key, err = ca.issue(3, false)
	if err != nil {
		return err
	}
	if e.ClientCert, err = writePEM(dir, "client.pem", cert); err != nil {
		return err
	}
	e.ClientKey, err = writePEM(dir, "client-key.pem", key)
	return err
}

// OtherCA writes the PEM of a CA that signed nothing the server holds, to test a CA the server does not chain to.
func OtherCA(t *testing.T) string {
	t.Helper()
	ca, err := newPKI()
	if err != nil {
		t.Fatal(err)
	}
	p, err := writePEM(t.TempDir(), "other-ca.pem", ca.pem)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
