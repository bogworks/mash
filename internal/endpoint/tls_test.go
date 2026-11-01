package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestTLSPrivateCA(t *testing.T) {
	f := newFixtureWith(t, testenv.Options{TLS: true}, "a")
	if !strings.HasPrefix(f.e.URL, "tls://") {
		t.Fatalf("URL = %s, want tls://", f.e.URL)
	}
	cfg := f.cfg("a")

	cfg.TLSCA = f.e.CAFile
	ep, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("trusted private CA: %v", err)
	}
	ep.Close()

	for name, ca := range map[string]string{"no CA": "", "other CA": testenv.OtherCA(t)} {
		cfg.TLSCA = ca
		_, err := OpenClient(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), "tls_ca") || !strings.Contains(err.Error(), "MASH_TLS_CA") {
			t.Errorf("%s: err = %v, want a hint naming tls_ca", name, err)
		}
	}
}

func TestTLSClientCert(t *testing.T) {
	f := newFixtureWith(t, testenv.Options{TLS: true, RequireClientCert: true}, "a")
	cfg := f.cfg("a")
	cfg.TLSCA = f.e.CAFile

	_, err := OpenClient(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
		t.Fatalf("no client cert: err = %v, want a hint naming tls_cert and tls_key", err)
	}

	cfg.TLSCert, cfg.TLSKey = f.e.ClientCert, f.e.ClientKey
	ep, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("with client cert: %v", err)
	}
	ep.Close()
}
