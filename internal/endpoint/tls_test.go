package endpoint

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestTLSPrivateCA(t *testing.T) {
	f := newFixtureWith(t, testenv.Options{TLS: true}, "a")
	if !strings.HasPrefix(f.e.URL, "tls://") {
		t.Fatalf("URL = %s, want tls://", f.e.URL)
	}
	cfg := f.cfg("a")
	cfg.InsecurePlaintext = false // these tests are about TLS: no override

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
	cfg.InsecurePlaintext = false // these tests are about TLS: no override
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

// REQ-014: a plain URL with no override is refused before any connection, loopback included; a listener records connects.
func TestPlainURLRefusedWithoutTheOverrideAndNoConnectionMade(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var n atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			c.Close()
		}
	}()
	f := newFixture(t, "a")
	cfg := f.cfg("a")
	cfg.InsecurePlaintext = false // this test is about TLS: no override
	for _, url := range []string{"nats://" + ln.Addr().String(), ln.Addr().String(), "ws://" + ln.Addr().String()} {
		cfg.URL = url
		for name, open := range map[string]func() error{
			"OpenClient": func() error { _, err := OpenClient(context.Background(), cfg); return err },
			"Open":       func() error { _, err := Open(context.Background(), cfg); return err },
		} {
			err := open()
			if err == nil || !strings.Contains(err.Error(), "insecure_plaintext") || !strings.Contains(err.Error(), "MASH_INSECURE_PLAINTEXT=1") {
				t.Errorf("%s %s: err = %v, want the refusal naming the override", name, url, err)
			}
		}
	}
	if got := n.Load(); got != 0 {
		t.Fatalf("%d connections reached the listener, want none", got)
	}
}
