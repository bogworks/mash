package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A mirror serves the candidate's release files over https from this machine, with a certificate made for the run by a
// certificate authority made for the run. The authority is written to caPath, and install.sh is told to trust it
// (CURL_CA_BUNDLE), so no setting of the host changes.
type mirror struct {
	srv    *http.Server
	ln     net.Listener
	base   string // https://127.0.0.1:PORT/v<version>
	caPath string

	mu   sync.Mutex
	reqs []string
}

func startMirror(dir, urlPath, caPath string) (*mirror, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "at011 run authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(certLife),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(certLife),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	m := &mirror{ln: ln, caPath: caPath}
	m.base = fmt.Sprintf("https://%s%s", ln.Addr().String(), urlPath)
	files := http.FileServer(http.Dir(dir))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.reqs = append(m.reqs, r.Method+" "+r.URL.Path)
		m.mu.Unlock()
		files.ServeHTTP(w, r)
	})
	m.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}}}}
	go func() { _ = m.srv.ServeTLS(ln, "", "") }()
	return m, nil
}

func (m *mirror) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.reqs...)
}

func (m *mirror) close() { _ = m.srv.Close() }

// requestedFiles is the names fetched (by GET) from the mirror since the first n requests.
func (m *mirror) requestedFiles(from int) []string {
	var out []string
	for _, r := range m.requests()[from:] {
		if p, ok := strings.CutPrefix(r, "GET "); ok {
			out = append(out, filepath.Base(p))
		}
	}
	return out
}
