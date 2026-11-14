package endpoint

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/testenv"
)

// requireTLSBound bounds a connect that must not happen.
const requireTLSBound = 3 * time.Second

// nonLoopbackAddr is an address of this machine that is not loopback, or "" when it has none.
func nonLoopbackAddr(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			return ipn.IP.String()
		}
	}
	return ""
}

// recordingListener counts the connections made to it, on every interface.
func recordingListener(t *testing.T) (port string, accepted *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted = &atomic.Int64{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, accepted
}

// With require_tls on, a plain URL to a remote host is refused before any connection is made, for the lease-taking
// Open and for the one-shot client, and the error names the host, the setting and the fix, never the userinfo.
func TestRequireTLSRefusesPlainRemoteBeforeDial(t *testing.T) {
	remote := nonLoopbackAddr(t)
	if remote == "" {
		t.Skip("no non-loopback address on this machine")
	}
	port, accepted := recordingListener(t)
	f := newFixture(t, "a")
	for name, url := range map[string]string{
		"nats":      "nats://u:s3cret@" + remote + ":" + port,
		"bare":      remote + ":" + port,
		"list":      "nats://127.0.0.1:1," + "nats://" + remote + ":" + port,
		"list only": "tls://127.0.0.1:1,nats://" + remote + ":" + port,
	} {
		cfg := f.cfg("a")
		cfg.URL, cfg.RequireTLS = url, true
		ctx, cancel := context.WithTimeout(context.Background(), requireTLSBound)
		_, err := OpenClient(ctx, cfg)
		_, err2 := Open(ctx, cfg)
		cancel()
		for _, e := range []error{err, err2} {
			if e == nil {
				t.Fatalf("%s: connected under require_tls", name)
			}
			for _, want := range []string{remote, "MASH_REQUIRE_TLS", "use tls:// with the server's certificate (tls_ca for a private CA)"} {
				if !strings.Contains(e.Error(), want) {
					t.Errorf("%s: error %q lacks %q", name, e, want)
				}
			}
			if strings.Contains(e.Error(), "s3cret") {
				t.Errorf("%s: error leaks the password: %v", name, e)
			}
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("%d connections were made to the remote host under require_tls", n)
	}
}

// With require_tls off, the same plain URL to a remote host connects, as it always did.
func TestRequireTLSOffAllowsPlainRemote(t *testing.T) {
	remote := nonLoopbackAddr(t)
	if remote == "" {
		t.Skip("no non-loopback address on this machine")
	}
	port, accepted := recordingListener(t)
	f := newFixture(t, "a")
	cfg := f.cfg("a")
	cfg.URL = "nats://" + remote + ":" + port
	ctx, cancel := context.WithTimeout(context.Background(), requireTLSBound)
	defer cancel()
	if _, err := OpenClient(ctx, cfg); err != nil && strings.Contains(err.Error(), "MASH_REQUIRE_TLS") {
		t.Fatalf("refused with require_tls off: %v", err)
	}
	if accepted.Load() == 0 {
		t.Fatal("no connection attempt: a plain remote URL must be tried when require_tls is off")
	}
}

// Plain text to loopback is allowed in both states, for 127.0.0.1, localhost and [::1].
func TestRequireTLSLoopbackAlwaysAllowed(t *testing.T) {
	f := newFixture(t, "a")
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.e.URL, "nats://"))
	for _, on := range []bool{false, true} {
		for _, host := range []string{"127.0.0.1", "localhost", "[::1]"} {
			cfg := f.cfg("a")
			cfg.URL, cfg.RequireTLS = "nats://"+host+":"+port, on
			ctx, cancel := context.WithTimeout(context.Background(), requireTLSBound)
			ep, err := OpenClient(ctx, cfg)
			cancel()
			if err != nil && strings.Contains(err.Error(), "MASH_REQUIRE_TLS") {
				t.Errorf("require_tls=%v %s: refused: %v", on, host, err)
			}
			if err == nil {
				ep.Close()
			}
		}
	}
	// The one that must connect: the test server's own URL, loopback and plain, with require_tls on.
	cfg := f.cfg("a")
	cfg.RequireTLS = true
	ep, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("loopback plain under require_tls: %v", err)
	}
	ep.Close()
}

// A tls:// URL is accepted and the connection really uses TLS: a plain server behind a tls:// URL fails.
func TestRequireTLSAcceptsTLSAndEnforcesIt(t *testing.T) {
	f := newFixtureWith(t, testenv.Options{TLS: true}, "a")
	cfg := f.cfg("a")
	cfg.TLSCA, cfg.RequireTLS = f.e.CAFile, true
	ep, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("tls:// under require_tls: %v", err)
	}
	ep.Close()

	plain := newFixture(t, "a")
	pcfg := plain.cfg("a")
	pcfg.URL = "tls://" + strings.TrimPrefix(plain.e.URL, "nats://")
	pcfg.RequireTLS = true
	ctx, cancel := context.WithTimeout(context.Background(), requireTLSBound)
	defer cancel()
	if ep, err := OpenClient(ctx, pcfg); err == nil {
		ep.Close()
		t.Fatal("a plain server behind a tls:// URL connected")
	}
}
