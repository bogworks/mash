package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The certificate authority of AT-005 is written by the harness, not by mash (contract BND-003). It is pure Go,
// ECDSA P-256, valid 24 hours. The CA's private key lives in memory only and is never written; each leaf key is written
// 0600 inside the run directory and never printed.

const certValidity = 24 * time.Hour

// CA is a certificate authority made for one run.
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	key     *ecdsa.PrivateKey
}

// Leaf is one node's certificate and key. KeyPEM is secret and is only ever written to a 0600 file.
type Leaf struct {
	Name    string
	Cert    *x509.Certificate
	CertPEM []byte
	KeyPEM  []byte
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
}

// NewCA makes a self-signed CA certificate valid from a minute before now for 24 hours.
func NewCA(now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: "mash AT-005 test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key: key}, nil
}

// Issue makes a leaf for one node: server and client authentication (routes use it both ways), SANs 127.0.0.1 and
// localhost, a key of its own.
func (ca *CA) Issue(name string, now time.Time) (*Leaf, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &Leaf{
		Name: name, Cert: cert,
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}),
	}, nil
}

// Verify checks that the leaf chains to the CA for server and client use and covers 127.0.0.1 and localhost.
func (ca *CA) Verify(l *Leaf, now time.Time) error {
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := l.Cert.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
			return err
		}
	}
	if err := l.Cert.VerifyHostname("127.0.0.1"); err != nil {
		return err
	}
	return l.Cert.VerifyHostname("localhost")
}

// TLSFiles are the paths of one node's TLS files.
type TLSFiles struct{ CA, Cert, Key string }

// WriteTLS writes ca.pem, cert.pem and key.pem into dir (0700), each file 0600.
func WriteTLS(dir string, ca *CA, l *Leaf) (TLSFiles, error) {
	f := TLSFiles{CA: filepath.Join(dir, "ca.pem"), Cert: filepath.Join(dir, "cert.pem"), Key: filepath.Join(dir, "key.pem")}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return f, err
	}
	for p, b := range map[string][]byte{f.CA: ca.CertPEM, f.Cert: l.CertPEM, f.Key: l.KeyPEM} {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return f, err
		}
	}
	return f, nil
}

// fingerprint is the first 8 bytes of the SHA-256 of a certificate's DER form: public, safe in a record.
func fingerprint(c *x509.Certificate) string {
	h := sha256.Sum256(c.Raw)
	return hex.EncodeToString(h[:8])
}

// TLSDescribe describes the setup for a run record: public facts only, no key and no certificate private part.
func TLSDescribe(ca *CA, leaves []*Leaf) []string {
	out := []string{fmt.Sprintf("TLS: a certificate authority made by the harness for this run (ECDSA P-256, valid %s, CA key held in memory and never written), fingerprint %s", certValidity, fingerprint(ca.Cert))}
	for _, l := range leaves {
		out = append(out, fmt.Sprintf("TLS leaf %s: signed by that CA, server and client use, SANs 127.0.0.1 and localhost, key of its own (0600, not shown), fingerprint %s", l.Name, fingerprint(l.Cert)))
	}
	return out
}

// noPrivate reports an error if text holds PEM private-key armour.
func noPrivate(text string) error {
	if strings.Contains(text, "PRIVATE KEY") {
		return errors.New("text holds private key armour")
	}
	return nil
}
