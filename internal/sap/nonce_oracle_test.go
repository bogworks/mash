package sap

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/natsauth"
)

// fakeServer speaks just enough NATS to send an INFO with the chosen nonce and report the CONNECT's sig field.
func fakeServer(t *testing.T, nonce string) (url string, sigs chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	sigs = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		info, _ := json.Marshal(map[string]any{"server_id": "X", "version": "2.10.0", "proto": 1, "headers": true,
			"auth_required": true, "nonce": nonce, "max_payload": 1048576})
		_, _ = c.Write([]byte("INFO " + string(info) + "\r\n"))
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "CONNECT ") {
			close(sigs)
			return
		}
		var cn struct {
			Sig string `json:"sig"`
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "CONNECT ")), &cn)
		sigs <- cn.Sig
	}()
	return "nats://" + ln.Addr().String(), sigs
}

func credsFile(t *testing.T, kp nkeys.KeyPair) string {
	t.Helper()
	seed, _ := kp.Seed()
	raw := []byte("-----BEGIN NATS USER JWT-----\neyJhbGciOiJlZDI1NTE5LW5rZXkifQ.e30.c2ln\n------END NATS USER JWT------\n\n" +
		"-----BEGIN USER NKEY SEED-----\n" + string(seed) + "\n------END USER NKEY SEED------\n")
	p := filepath.Join(t.TempDir(), "u.creds")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// forgedMailNonce is the AGENT-SENDER-V1 input of a mail the victim never sent, as a hostile server's "nonce".
func forgedMailNonce(id Identity, subject string, body []byte, ts time.Time) (nonce string, h SenderHeader) {
	h = SenderHeader{V: 1, Account: id.Account, User: id.User, Name: "victim", Sub: subject,
		TS: ts.UTC().Format("2006-01-02T15:04:05Z"), Nonce: "forged-1"}
	return string(signedInput(h.Account, h.User, h.Sub, h.TS, h.Nonce, body)), h
}

func oracleSetup(t *testing.T) (Identity, nkeys.KeyPair, string) {
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	id, err := NewIdentity(seed, b13Account)
	if err != nil {
		t.Fatal(err)
	}
	return id, kp, credsFile(t, kp)
}

// The stock client signs whatever nonce the server sends: that signature is a valid mail signature (the oracle).
func TestStockClientIsSigningOracle(t *testing.T) {
	id, _, creds := oracleSetup(t)
	body := []byte("pay the invoice")
	subject := "agents.mail.cc.o.bob"
	nonce, h := forgedMailNonce(id, subject, body, time.Now())
	url, sigs := fakeServer(t, nonce)
	_, _ = nats.Connect(url, nats.UserCredentials(creds), nats.Timeout(2*time.Second), nats.NoReconnect())
	sig, ok := <-sigs
	if !ok {
		t.Fatal("no CONNECT")
	}
	h.Sig = sig
	if _, tr := VerifyStored(h.Encode(), subject, body); tr != TrustVerified {
		t.Fatalf("expected the oracle to yield a verified mail, got %s", tr)
	}
}

// natsauth refuses: nothing is signed, nothing is sent.
func TestNatsauthRefusesMailNonce(t *testing.T) {
	id, _, creds := oracleSetup(t)
	nonce, _ := forgedMailNonce(id, "agents.mail.cc.o.bob", []byte("x"), time.Now())
	for _, n := range []string{nonce, "AGENT-SENDER-V1\nA\nU\n", "short", strings.Repeat("a", 33), "abc def ghijklm", ""} {
		url, sigs := fakeServer(t, n)
		nc, err := nats.Connect(url, natsauth.UserCredentials(creds), nats.Timeout(2*time.Second), nats.NoReconnect())
		if err == nil {
			nc.Close()
			t.Fatalf("connected with nonce %q", n)
		}
		if sig, ok := <-sigs; ok {
			t.Fatalf("signed nonce %q: %s", n, sig)
		}
	}
}

// A server-shaped nonce is still signed, and verifies as a plain nkey signature.
func TestNatsauthSignsServerNonce(t *testing.T) {
	_, kp, creds := oracleSetup(t)
	const nonce = "Kq3_-x9ZaB0cD1e" // 15 raw-URL characters, as nats-server sends
	url, sigs := fakeServer(t, nonce)
	_, _ = nats.Connect(url, natsauth.UserCredentials(creds), nats.Timeout(2*time.Second), nats.NoReconnect())
	sig, ok := <-sigs
	if !ok {
		t.Fatal("no CONNECT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || kp.Verify([]byte(nonce), raw) != nil {
		t.Fatalf("bad signature over the nonce: %v", err)
	}
}
