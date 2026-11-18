package bootstrap

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
	"time"
)

// File names of the local certificate authority and the server certificate, inside the server directory.
const (
	CACert     = "ca.pem"
	CAKey      = "ca.key"
	ServerCert = "server.pem"
	ServerKey  = "server.key"
)

// certYears is how long the local CA and the server certificate are valid.
const certYears = 10

// localPKI is the PEM content of a new CA and of a server certificate it signed.
type localPKI struct {
	caCert, caKey, serverCert, serverKey []byte
}

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func pemKey(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// newLocalPKI makes an ECDSA P-256 CA ("mash local CA") and a server certificate for localhost, 127.0.0.1 and ::1.
func newLocalPKI() (*localPKI, error) {
	now := time.Now()
	notBefore, notAfter := now.Add(-time.Hour), now.AddDate(certYears, 0, 0)
	caPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mashctl: CA key: %w", err)
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	caTpl := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: "mash local CA"},
		NotBefore: notBefore, NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caPriv.PublicKey, caPriv)
	if err != nil {
		return nil, fmt.Errorf("mashctl: CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	srvPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mashctl: server key: %w", err)
	}
	if sn, err = serial(); err != nil {
		return nil, err
	}
	srvTpl := &x509.Certificate{SerialNumber: sn, Subject: pkix.Name{CommonName: "mash local server"},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, caCert, &srvPriv.PublicKey, caPriv)
	if err != nil {
		return nil, fmt.Errorf("mashctl: server certificate: %w", err)
	}
	caKeyPEM, err := pemKey(caPriv)
	if err != nil {
		return nil, err
	}
	srvKeyPEM, err := pemKey(srvPriv)
	if err != nil {
		return nil, err
	}
	return &localPKI{caCert: pemCert(caDER), caKey: caKeyPEM, serverCert: pemCert(srvDER), serverKey: srvKeyPEM}, nil
}
