package natsauth_test

import (
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/testenv"
)

func optionsOf(t *testing.T, opts []nats.Option) nats.Options {
	t.Helper()
	var o nats.Options
	for _, f := range opts {
		if err := f(&o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestRequireEndpointTLS(t *testing.T) {
	for _, tc := range []struct {
		url     string
		wantErr string // "" = accepted
		host    string // the host the error names
		secure  bool
	}{
		{"nats://127.0.0.1:4222", "", "", false},
		{"nats://localhost:4222", "", "", false},
		{"nats://[::1]:4222", "", "", false},
		{"ws://127.0.0.1:8080", "", "", false},
		{"127.0.0.1:4222", "", "", false},
		{"nats://127.0.0.1:4222,nats://[::1]:4222", "", "", false},
		{"tls://example.com:4222", "", "", true},
		{"wss://example.com", "", "", true},
		{"tls://127.0.0.1:4222", "", "", true},
		{"tls://a.example.com:4222,tls://b.example.com:4222", "", "", true},
		{"nats://10.0.0.5:4222", "plain nats://", "10.0.0.5", false},
		{"nats://example.com", "plain nats://", "example.com", false},
		{"example.com:4222", "plain nats://", "example.com", false},
		{"NATS://Example.com:4222", "plain nats://", "Example.com", false},
		{"ws://example.com:80", "plain ws://", "example.com", false},
		{"tls://a.example.com:4222,nats://b.example.com:4222", "plain nats://", "b.example.com", false},
		{"nats://127.0.0.1:4222, nats://b.example.com:4222", "plain nats://", "b.example.com", false},
		{"nats://127.0.0.1.example.com:4222", "plain nats://", "127.0.0.1.example.com", false},
		{"foo://remote:4222", "unsupported scheme \"foo\": use tls://", "", false},
		{"FOO://remote:4222", "unsupported scheme \"foo\": use tls://", "", false},
		{"foo://127.0.0.1:4222", "unsupported scheme \"foo\": use tls://", "", false},
		{"tls://a:4222,foo://b:4222", "unsupported scheme \"foo\": use tls://", "", false},
		{"nats://remote:4222", "plain nats://", "remote", false},
		{"remote:4222", "plain nats://", "remote", false},
		{"ws://remote:8080", "plain ws://", "remote", false},
		{"wss://remote:443", "", "", true},
		{"TLS://remote:4222", "", "", true},
	} {
		opts, err := natsauth.RequireEndpointTLS(tc.url)
		if tc.wantErr != "" {
			if strings.HasPrefix(tc.wantErr, "unsupported") {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("%s: err %v, want %q", tc.url, err, tc.wantErr)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), `"`+tc.host+`"`) ||
				!strings.Contains(err.Error(), "MASH_REQUIRE_TLS") || !strings.Contains(err.Error(), "use tls:// with the server's certificate (tls_ca for a private CA)") {
				t.Errorf("%s: err %v, want %q naming %q, the setting and the fix", tc.url, err, tc.wantErr, tc.host)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.url, err)
			continue
		}
		o := optionsOf(t, opts)
		if o.Secure != tc.secure {
			t.Errorf("%s: Secure = %v, want %v", tc.url, o.Secure, tc.secure)
		}
		if !o.IgnoreDiscoveredServers {
			t.Errorf("%s: discovered servers are not ignored", tc.url)
		}
	}
}

// The error never carries the URL's userinfo, however the userinfo is written.
func TestRequireEndpointTLSRedactsUserinfo(t *testing.T) {
	for _, url := range []string{
		"nats://user:s3cret@10.0.0.5:4222",
		"nats://s3cret@10.0.0.5",
		"10.0.0.5:4222?token=s3cret",
		"tls://a:4222,nats://u:s3cret@host.example.com:4222/path?x=s3cret",
		"ws://u:s3cret@host.example.com/x",
	} {
		_, err := natsauth.RequireEndpointTLS(url)
		if err == nil {
			t.Fatalf("%s: accepted", url)
		}
		if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "user:") {
			t.Errorf("%s: error leaks: %v", url, err)
		}
	}
}

func TestPlainRemoteCountsEverySchemeButTLSAndWSS(t *testing.T) {
	for url, want := range map[string]bool{
		"foo://remote:4222": true, "FOO://remote:4222": true, "remote:4222": true, "nats://remote:4222": true,
		"ws://remote:8080": true, "tls://remote:4222": false, "wss://remote:443": false, "TLS://remote": false,
		"foo://127.0.0.1:4222": false, "foo://localhost": false,
	} {
		if _, _, got := natsauth.PlainRemote(url); got != want {
			t.Errorf("PlainRemote(%q) = %v, want %v", url, got, want)
		}
	}
}

// fakeConn is a connection that reports a remote address of the test's choosing.
type fakeConn struct {
	net.Conn
	remote net.Addr
}

func (c fakeConn) RemoteAddr() net.Addr { return c.remote }

// A plain localhost URL whose name resolves to a non-loopback address is refused at dial, so credentials never go there;
// the dialer is the one the option set carries, and it is the same at every reconnect.
func TestRequireEndpointTLSPinsPlainLoopbackAtDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			defer c.Close()
		}
	}()
	// every dial reaches the local listener but reports a non-loopback remote address, as a hostile resolution would
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.9"), Port: 4222}
	var dials int
	restore := natsauth.SetBaseDial(func(network, address string) (net.Conn, error) {
		dials++
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return fakeConn{c, remote}, nil
	})
	defer restore()

	opts, err := natsauth.RequireEndpointTLS("nats://localhost:4222")
	if err != nil {
		t.Fatal(err)
	}
	o := optionsOf(t, opts)
	if o.CustomDialer == nil {
		t.Fatal("a plain loopback connection has no custom dialer")
	}
	if _, err := o.CustomDialer.Dial("tcp", "localhost:4222"); err == nil || !strings.Contains(err.Error(), "resolved to 192.0.2.9") {
		t.Errorf("dial err %v, want a refusal naming the resolved address", err)
	}
	if _, err := nats.Connect("nats://localhost:4222", append(opts, nats.Timeout(time.Second))...); err == nil {
		t.Error("connect to a localhost that resolves off-host succeeded")
	}
	if dials < 2 {
		t.Errorf("dialer used %d times, want it applied by the client too", dials)
	}

	// tls:// entries use no loopback dialer
	opts, err = natsauth.RequireEndpointTLS("tls://example.com:4222,nats://127.0.0.1:4222")
	if err != nil {
		t.Fatal(err)
	}
	if o := optionsOf(t, opts); o.CustomDialer != nil || !o.Secure {
		t.Errorf("tls list: dialer set = %v, secure = %v, want no dialer and secure", o.CustomDialer != nil, o.Secure)
	}
}

// A plain 127.0.0.1 URL connects through the dialer, and the dialer is applied again on every reconnect.
func TestRequireEndpointTLSLoopbackDialerAppliesOnReconnect(t *testing.T) {
	e := testenv.Start(t)
	creds, _ := e.Mint(t, "x")
	var dials int32
	restore := natsauth.SetBaseDial(func(network, address string) (net.Conn, error) {
		atomic.AddInt32(&dials, 1)
		return (&net.Dialer{Timeout: 2 * time.Second}).Dial(network, address)
	})
	defer restore()
	// a relay on 127.0.0.1 whose connections the test can cut, to force a reconnect
	rl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	var mu sync.Mutex
	var open []net.Conn
	go func() {
		for {
			c, err := rl.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", strings.TrimPrefix(e.URL, "nats://"))
			if err != nil {
				c.Close()
				continue
			}
			mu.Lock()
			open = append(open, c, up)
			mu.Unlock()
			go func() { _, _ = io.Copy(up, c); up.Close() }()
			go func() { _, _ = io.Copy(c, up); c.Close() }()
		}
	}()
	url := "nats://" + rl.Addr().String()
	opts, err := natsauth.RequireEndpointTLS(url)
	if err != nil {
		t.Fatal(err)
	}
	reconnected := make(chan struct{}, 1)
	opts = append(opts, natsauth.UserCredentials(creds), nats.ReconnectWait(50*time.Millisecond),
		nats.ReconnectHandler(func(*nats.Conn) { reconnected <- struct{}{} }))
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if atomic.LoadInt32(&dials) != 1 {
		t.Fatalf("first connect dialed %d times, want 1 through the loopback dialer", dials)
	}
	// force a reconnect by closing the client's socket from the server side
	mu.Lock()
	for _, c := range open {
		c.Close()
	}
	mu.Unlock()
	select {
	case <-reconnected:
	case <-time.After(10 * time.Second):
		t.Fatal("no reconnect")
	}
	if atomic.LoadInt32(&dials) < 2 {
		t.Errorf("dialed %d times, want the dialer applied again at reconnect", dials)
	}
}
