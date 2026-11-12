package natsauth_test

import (
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
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
	} {
		opts, err := natsauth.RequireEndpointTLS(tc.url)
		if tc.wantErr != "" {
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
