// Package testenv runs an in-process nats-server in operator mode for
// integration tests. It mirrors production: an operator, a system account,
// and account MASH with JetStream, whose users are issued by MASH's signing
// key and carry mash's real permission template.
package testenv

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/randybias/mash/internal/admin"
)

// Owner is the SAP owner used in tests.
const Owner = "o"

// Env is one running test server and its keys.
type Env struct {
	URL        string
	Owner      string
	Account    string // MASH account public key
	AdminCreds string // path of the admin's creds file

	srv      *server.Server
	sysPub   string
	port     int
	dir      string
	acctSK   nkeys.KeyPair
	operator *jwt.OperatorClaims
	resolver *server.MemAccResolver
}

// must panics on error; inside a test the panic fails that test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Start launches the server; it is shut down when the test ends.
func Start(t *testing.T) *Env {
	t.Helper()
	opKP := must(nkeys.CreateOperator())
	opPub := must(opKP.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "test-operator"
	opJWT := must(oc.Encode(opKP))
	op := must(jwt.DecodeOperatorClaims(opJWT))

	resolver := &server.MemAccResolver{}
	account := func(name string, js bool) (nkeys.KeyPair, string, nkeys.KeyPair) {
		kp := must(nkeys.CreateAccount())
		pub := must(kp.PublicKey())
		sk := must(nkeys.CreateAccount())
		skPub := must(sk.PublicKey())
		ac := jwt.NewAccountClaims(pub)
		ac.Name = name
		ac.SigningKeys.Add(skPub)
		if js {
			ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: 256 << 20, Streams: 50, Consumer: 100}
		}
		tok := must(ac.Encode(opKP))
		if err := resolver.Store(pub, tok); err != nil {
			t.Fatal(err)
		}
		return kp, pub, sk
	}
	_, sysPub, _ := account("SYS", false)
	_, mashPub, mashSK := account("MASH", true)

	dir := t.TempDir()
	e := &Env{Owner: Owner, Account: mashPub, dir: dir, acctSK: mashSK, operator: op, resolver: resolver}
	e.sysPub, e.port = sysPub, -1
	e.start(t)
	t.Cleanup(func() { e.srv.Shutdown(); e.srv.WaitForShutdown() })

	var full jwt.Permissions // admin: unrestricted within MASH
	e.AdminCreds, _ = e.mint(t, "admin", full)
	return e
}

func (e *Env) start(t *testing.T) {
	t.Helper()
	// NewServer mutates its options, so each start builds fresh ones.
	opts := &server.Options{
		Host:             "127.0.0.1",
		Port:             e.port,
		JetStream:        true,
		StoreDir:         filepath.Join(e.dir, "js"),
		TrustedOperators: []*jwt.OperatorClaims{e.operator},
		AccountResolver:  e.resolver,
		SystemAccount:    e.sysPub,
		NoLog:            true,
		NoSigs:           true,
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	e.srv = s
	e.URL = s.ClientURL()
	e.port = s.Addr().(*net.TCPAddr).Port
}

// Restart stops the server and starts it again on the same port and store.
func (e *Env) Restart(t *testing.T) {
	t.Helper()
	e.srv.Shutdown()
	e.srv.WaitForShutdown()
	e.start(t)
}

// Shutdown stops the server without restarting it.
func (e *Env) Shutdown() {
	e.srv.Shutdown()
	e.srv.WaitForShutdown()
}

// Mint issues an endpoint user with mash's permission template and writes
// its creds file. It returns the creds path and the user public key.
func (e *Env) Mint(t *testing.T, name string) (string, string) {
	t.Helper()
	return e.mint(t, name, admin.Permissions(e.Owner, name))
}

// MintRaw issues a user with arbitrary permissions (for negative tests).
func (e *Env) MintRaw(t *testing.T, name string, p jwt.Permissions) (string, string) {
	t.Helper()
	return e.mint(t, name, p)
}

func (e *Env) mint(t *testing.T, name string, p jwt.Permissions) (string, string) {
	t.Helper()
	ukp := must(nkeys.CreateUser())
	upub := must(ukp.PublicKey())
	seed := must(ukp.Seed())
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.IssuerAccount = e.Account
	uc.Permissions = p
	tok := must(uc.Encode(e.acctSK))
	creds := must(jwt.FormatUserConfig(tok, seed))
	path := filepath.Join(e.dir, fmt.Sprintf("%s-%s.creds", name, upub[:8]))
	if err := os.WriteFile(path, creds, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, upub
}

// Conn connects with a creds file; it is closed when the test ends.
func (e *Env) Conn(t *testing.T, creds string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(e.URL, nats.UserCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// ConnAs connects as endpoint name, using its private inbox prefix.
func (e *Env) ConnAs(t *testing.T, creds, name string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(e.URL, nats.UserCredentials(creds), nats.CustomInboxPrefix("_INBOX."+name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}
