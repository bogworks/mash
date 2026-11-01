package natsauth_test

import (
	"bufio"
	"encoding/json"
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
