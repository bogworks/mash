package natsauth_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/testenv"
)

// A real nats-server accepts the connect, and its nonce passes the check.
func TestConnectsToRealServer(t *testing.T) {
	e := testenv.Start(t)
	creds, _ := e.Mint(t, "x")

	c, err := net.Dial("tcp", strings.TrimPrefix(e.URL, "nats://"))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(c).ReadString('\n')
	_ = c.Close()
	var info struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "INFO ")), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Nonce) != 15 {
		t.Fatalf("server nonce %q, want 15 characters", info.Nonce)
	}
	if err := natsauth.CheckNonce([]byte(info.Nonce)); err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(e.URL, natsauth.UserCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	nc.Close()
}

func TestTLSOptions(t *testing.T) {
	if n := len(natsauth.TLSOptions("", "", "")); n != 0 {
		t.Fatalf("no settings: %d options", n)
	}
	if n := len(natsauth.TLSOptions("ca.pem", "c.pem", "k.pem")); n != 2 {
		t.Fatalf("all settings: %d options, want 2", n)
	}
}

func TestTLSHint(t *testing.T) {
	for _, c := range []struct{ err, want string }{
		{"tls: failed to verify certificate: x509: certificate signed by unknown authority", "tls_ca"},
		{"remote error: tls: bad certificate", "tls_cert and tls_key"},
		{"remote error: tls: certificate required", "tls_cert and tls_key"},
	} {
		if got := natsauth.TLSHint(errors.New(c.err)); !strings.Contains(got.Error(), c.want) || !strings.Contains(got.Error(), c.err) {
			t.Errorf("natsauth.TLSHint(%q) = %q, want it to name %q", c.err, got, c.want)
		}
	}
	plain := errors.New("connection refused")
	if natsauth.TLSHint(plain) != plain {
		t.Error("a non-TLS error must pass through unchanged")
	}
}

func TestTLSHintClosedAfterHandshake(t *testing.T) {
	err := errors.New("nats: tls error: connection closed by remote after TLS handshake: write: broken pipe")
	if got := natsauth.TLSHint(err).Error(); !strings.Contains(got, "tls_cert") || !strings.Contains(got, "tls_key") {
		t.Fatalf("hint = %q", got)
	}
}

func TestRequireTLS(t *testing.T) {
	for _, tc := range []struct {
		url      string
		insecure bool
		wantErr  string // "" = accepted
		secure   bool   // the options force TLS
	}{
		{"tls://example.com:4222", false, "", true},
		{"nats://127.0.0.1:4222", false, "", true}, // loopback plain URL: TLS is still required of the server
		{"nats://localhost:4222", false, "", true},
		{"nats://[::1]:4222", false, "", true},
		{"nats://10.0.0.5:4222", false, "refusing a plain nats://", false},
		{"nats://example.com:4222", false, "refusing a plain nats://", false},
		{"example.com:4222", false, "refusing a plain nats://", false},
		{"ws://example.com:80", false, "refusing a plain ws://", false},
		{"nats://127.0.0.1:4222", true, "", false},
		{"nats://127.1.2.3:4222", true, "", false},
		{"nats://localhost:4222", true, "", false},
		{"nats://[::1]:4222", true, "", false},
		{"nats://10.0.0.5:4222", true, "allows a plain connection only to localhost", false},
		{"tls://example.com:4222", true, "allows a plain connection only to localhost", false},
		{"nats://127.0.0.1:4222,nats://10.0.0.5:4222", true, "allows a plain connection only to localhost", false},
		{"nats://127.0.0.1.example.com:4222", true, "allows a plain connection only to localhost", false},
	} {
		opts, err := natsauth.RequireTLS(tc.url, tc.insecure)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s insecure=%v: err %v, want %q", tc.url, tc.insecure, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s insecure=%v: %v", tc.url, tc.insecure, err)
			continue
		}
		var o nats.Options
		for _, f := range opts {
			if err := f(&o); err != nil {
				t.Fatal(err)
			}
		}
		if o.Secure != tc.secure {
			t.Errorf("%s insecure=%v: Secure = %v, want %v", tc.url, tc.insecure, o.Secure, tc.secure)
		}
	}
}

func TestLoopbackDialer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	d := natsauth.LoopbackDialer{}
	c, err := d.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial: %v", err)
	}
	c.Close()
	for _, addr := range []string{"10.0.0.5:4222", "192.0.2.1:4222", "example.com:4222", "127.0.0.1.example.com:4222", "[2001:db8::1]:4222", "nohost"} {
		c, err := d.Dial("tcp", addr)
		if err == nil {
			c.Close()
			t.Errorf("dial %s succeeded, want a refusal", addr)
		}
	}
}

// fakeServer answers the NATS handshake and advertises connect_urls with a non-loopback address.
func fakeServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				fmt.Fprint(c, `INFO {"server_id":"x","version":"2.10.0","proto":1,"max_payload":1048576,"connect_urls":["192.0.2.7:4222"]}`+"\r\n")
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if strings.HasPrefix(line, "PING") {
						fmt.Fprint(c, "PONG\r\n")
					}
				}
			}()
		}
	}()
	return "nats://" + ln.Addr().String()
}

func TestAdvertisedServersAreNotUsed(t *testing.T) {
	url := fakeServer(t)
	// control: without the option the advertised address is taken into the pool
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	if len(nc.DiscoveredServers()) == 0 {
		t.Fatal("control: the fake server's advertised address was not discovered")
	}
	nc.Close()

	for _, insecure := range []bool{true, false} {
		opts, err := natsauth.RequireTLS(url, insecure)
		if err != nil {
			t.Fatal(err)
		}
		if !insecure {
			continue // the fake server offers no TLS; the option set is checked below
		}
		nc, err := nats.Connect(url, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if d := nc.DiscoveredServers(); len(d) != 0 {
			t.Errorf("discovered servers %v, want none", d)
		}
		for _, s := range nc.Servers() {
			if strings.Contains(s, "192.0.2.7") {
				t.Errorf("the pool holds the advertised address: %v", nc.Servers())
			}
		}
		nc.Close()
	}
	for _, insecure := range []bool{true, false} {
		opts, _ := natsauth.RequireTLS(url, insecure)
		var o nats.Options
		for _, f := range opts {
			if err := f(&o); err != nil {
				t.Fatal(err)
			}
		}
		if !o.IgnoreDiscoveredServers {
			t.Errorf("insecure=%v: discovered servers are not ignored", insecure)
		}
		if (o.CustomDialer != nil) != insecure {
			t.Errorf("insecure=%v: custom dialer set = %v", insecure, o.CustomDialer != nil)
		}
	}
}
