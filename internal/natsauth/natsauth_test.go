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
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

// A real nats-server accepts the connect, and its nonce passes the check.
func TestConnectsToRealServer(t *testing.T) {
	e := testenv.Start(t)
	creds, _ := e.Mint(t, sap.Address{Owner: "o", Group: "g", Instance: "x"})

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

	opts, warning, err := natsauth.RequireTLS(url, true)
	if err != nil {
		t.Fatal(err)
	}
	if warning == "" {
		t.Error("no warning for a plain connection under insecure_plaintext")
	}
	nc, err = nats.Connect(url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if d := nc.DiscoveredServers(); len(d) != 0 {
		t.Errorf("discovered servers %v, want none", d)
	}
	for _, s := range nc.Servers() {
		if strings.Contains(s, "192.0.2.7") {
			t.Errorf("the pool holds the advertised address: %v", nc.Servers())
		}
	}
	for _, u := range []string{url, "tls://example.com:4222"} {
		opts, _, err := natsauth.RequireTLS(u, true)
		if err != nil {
			t.Fatal(err)
		}
		if o := optionsOf(t, opts); !o.IgnoreDiscoveredServers {
			t.Errorf("%s: discovered servers are not ignored", u)
		}
	}
}

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

func TestRequireTLS(t *testing.T) {
	for _, tc := range []struct {
		url      string
		override bool
		wantErr  string // "" = accepted
		secure   bool   // the options force TLS
		warnHost string // the host the warning names ("" = no warning)
	}{
		{"tls://example.com:4222", false, "", true, ""},
		{"tls://127.0.0.1:4222", false, "", true, ""},
		{"tls://localhost:4222", false, "", true, ""},
		{"wss://[::1]:443", false, "", true, ""},
		{"tls://a.example.com:4222,tls://b.example.com:4222", false, "", true, ""},
		{"tls://example.com:4222", true, "", true, ""},
		{"nats://127.0.0.1:4222", false, "refusing a plain nats:// connection to \"127.0.0.1\"", false, ""},
		{"nats://localhost:4222", false, "refusing a plain nats:// connection to \"localhost\"", false, ""},
		{"nats://[::1]:4222", false, "refusing a plain nats:// connection to \"::1\"", false, ""},
		{"nats://example.com:4222", false, "refusing a plain nats:// connection to \"example.com\"", false, ""},
		{"ws://example.com:80", false, "refusing a plain ws://", false, ""},
		{"example.com:4222", false, "refusing a plain nats://", false, ""},
		{"127.0.0.1:4222", false, "refusing a plain nats://", false, ""},
		{"foo://example.com:4222", false, "unsupported scheme \"foo\"", false, ""},
		{"foo://127.0.0.1:4222", true, "unsupported scheme \"foo\"", false, ""},
		{"tls://a.example.com:4222,nats://b.example.com:4222", false, "refusing a plain nats:// connection to \"b.example.com\"", false, ""},
		{"nats://127.0.0.1:4222,tls://example.com:4222", false, "refusing a plain nats://", false, ""},
		{"nats://127.0.0.1:4222", true, "", false, "127.0.0.1"},
		{"nats://localhost:4222", true, "", false, "localhost"},
		{"nats://[::1]:4222", true, "", false, "::1"},
		{"nats://example.com:4222", true, "", false, "example.com"},
		{"ws://example.com:80", true, "", false, "example.com"},
		{"example.com:4222", true, "", false, "example.com"},
		{"tls://a.example.com:4222,nats://b.example.com:4222", true, "", false, "b.example.com"},
		{"nats://x.example.com:4222,nats://y.example.com:4222", true, "", false, "x.example.com"},
	} {
		opts, warning, err := natsauth.RequireTLS(tc.url, tc.override)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s override=%v: err %v, want %q", tc.url, tc.override, err, tc.wantErr)
			}
			if err != nil && (opts != nil || warning != "") {
				t.Errorf("%s: a refusal returned options or a warning", tc.url)
			}
			if err != nil && !strings.Contains(tc.wantErr, "unsupported") && !strings.Contains(err.Error(), "insecure_plaintext") {
				t.Errorf("%s: refusal does not name insecure_plaintext: %v", tc.url, err)
			}
			if err != nil && !strings.Contains(err.Error(), "tls_ca") {
				t.Errorf("%s: refusal does not name the fix: %v", tc.url, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s override=%v: %v", tc.url, tc.override, err)
			continue
		}
		o := optionsOf(t, opts)
		if o.Secure != tc.secure {
			t.Errorf("%s override=%v: Secure = %v, want %v", tc.url, tc.override, o.Secure, tc.secure)
		}
		if !o.IgnoreDiscoveredServers || o.CustomDialer != nil {
			t.Errorf("%s: IgnoreDiscoveredServers = %v, CustomDialer set = %v", tc.url, o.IgnoreDiscoveredServers, o.CustomDialer != nil)
		}
		want := ""
		if tc.warnHost != "" {
			want = "mash: WARNING: plain-text connection to " + tc.warnHost + " (insecure_plaintext is set); anyone on the network can read mail and obtain signatures"
		}
		if warning != want {
			t.Errorf("%s override=%v: warning %q, want %q", tc.url, tc.override, warning, want)
		}
	}
}

func TestRequireTLSNeverPrintsUserinfo(t *testing.T) {
	for _, url := range []string{"nats://user:s3cret@example.com:4222", "ws://tok3n@127.0.0.1:80", "foo://user:s3cret@example.com:4222"} {
		_, _, err := natsauth.RequireTLS(url, false)
		if err == nil {
			t.Fatalf("%s accepted", url)
		}
		for _, secret := range []string{"s3cret", "tok3n", "user:"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error carries userinfo: %v", url, err)
			}
		}
	}
	_, w, err := natsauth.RequireTLS("nats://user:s3cret@example.com:4222", true)
	if err != nil || strings.Contains(w, "s3cret") || !strings.Contains(w, "example.com") {
		t.Errorf("override: warning %q, err %v", w, err)
	}
}

func TestNoServersHint(t *testing.T) {
	for _, err := range []error{nats.ErrNoServers, fmt.Errorf("wrapped: %w", nats.ErrNoServers), errors.New("nats: no servers available for connection")} {
		got := natsauth.NoServersHint(err).Error()
		if !strings.Contains(got, "no servers available for connection") || !strings.HasSuffix(got, "(check MASH_URL and that at least one listed server is up)") {
			t.Errorf("hint = %q", got)
		}
		if again := natsauth.NoServersHint(natsauth.NoServersHint(err)).Error(); again != got {
			t.Errorf("not idempotent: %q", again)
		}
	}
	plain := errors.New("connection refused")
	if natsauth.NoServersHint(plain) != plain || natsauth.NoServersHint(nil) != nil {
		t.Error("another error must pass through unchanged")
	}
}
