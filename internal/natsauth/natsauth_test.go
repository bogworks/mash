package natsauth_test

import (
	"bufio"
	"encoding/json"
	"errors"
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
