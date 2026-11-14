// Package testenv runs an in-process nats-server in operator mode for
// integration tests. It mirrors production: an operator, a system account,
// and account MASH with JetStream, whose users are issued by MASH's signing
// key and carry mash's real permission template.
package testenv

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
)

// Owner is the SAP owner used in tests.
const Owner = "o"

// Env is one running test server and its keys.
type Env struct {
	URL         string
	Owner       string
	Account     string // MASH account public key
	AdminCreds  string // path of the admin's creds file
	AccountSeed string // path of the MASH account identity seed file (0600), as ctlkeys.ImportAccount takes

	CAFile, ClientCert, ClientKey string // PEM paths, set when the server runs with TLS (the client pair only if it requires one)

	srv    *server.Server
	maxPay int32
	tls    bool
	verify bool

	serverCert, serverKey string
	sysPub                string
	port                  int
	dir                   string
	acctSK                nkeys.KeyPair
	operator              *jwt.OperatorClaims
	resolver              *server.MemAccResolver
}

// must panics on error; inside a test the panic fails that test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Options shape the server for tests of a hosted service's limits. The zero value is the default server.
type Options struct {
	// MaxPayload is the server's max_payload in bytes; 0 keeps nats-server's default (1 MiB).
	MaxPayload int32
	// RequireMaxBytes makes the MASH account refuse a stream or bucket config with no MaxBytes, as Synadia's
	// accounts do (err_code 10113).
	RequireMaxBytes bool
	// TLS serves tls://127.0.0.1 with a certificate from a throwaway CA, whose PEM is Env.CAFile.
	TLS bool
	// RequireClientCert (with TLS) makes the server verify client certificates; Env.ClientCert and Env.ClientKey
	// are one the CA signed.
	RequireClientCert bool
}

// Start launches the server; it is shut down when the test ends.
func Start(t *testing.T) *Env { t.Helper(); return StartWith(t, Options{}) }

// StartWith is Start with options.
func StartWith(t *testing.T, o Options) *Env {
	t.Helper()
	e, err := launch(t.TempDir(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Shutdown)
	return e
}

// Launch is Start without a test: the release check drives real mash binaries against the same server. dir holds the
// server's store and creds files and is the caller's to remove; stop shuts the server down.
func Launch(dir string) (e *Env, stop func(), err error) {
	e, err = launch(dir, Options{})
	if err != nil {
		return nil, nil, err
	}
	return e, e.Shutdown, nil
}

func launch(dir string, o Options) (*Env, error) {
	opKP := must(nkeys.CreateOperator())
	opPub := must(opKP.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "test-operator"
	opJWT := must(oc.Encode(opKP))
	op := must(jwt.DecodeOperatorClaims(opJWT))

	resolver := &server.MemAccResolver{}
	account := func(name string, js bool) (nkeys.KeyPair, string, nkeys.KeyPair, error) {
		kp := must(nkeys.CreateAccount())
		pub := must(kp.PublicKey())
		sk := must(nkeys.CreateAccount())
		skPub := must(sk.PublicKey())
		ac := jwt.NewAccountClaims(pub)
		ac.Name = name
		ac.SigningKeys.Add(skPub)
		if js {
			ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: 256 << 20, MemoryStorage: 64 << 20, Streams: 50, Consumer: 100,
				MaxBytesRequired: o.RequireMaxBytes}
		}
		tok := must(ac.Encode(opKP))
		return kp, pub, sk, resolver.Store(pub, tok)
	}
	_, sysPub, _, err := account("SYS", false)
	if err != nil {
		return nil, err
	}
	mashKP, mashPub, mashSK, err := account("MASH", true)
	if err != nil {
		return nil, err
	}
	mashSeed := must(mashKP.Seed())
	seedPath := filepath.Join(dir, "mash-account.nk")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(seedPath, append(mashSeed, '\n'), 0o600); err != nil {
		return nil, err
	}

	e := &Env{Owner: Owner, Account: mashPub, AccountSeed: seedPath, dir: dir, acctSK: mashSK, operator: op, resolver: resolver, maxPay: o.MaxPayload, tls: o.TLS, verify: o.RequireClientCert}
	if o.TLS {
		if err := e.setupTLS(dir, o); err != nil {
			return nil, err
		}
	}
	e.sysPub, e.port = sysPub, -1
	if err := e.start(); err != nil {
		return nil, err
	}

	var full jwt.Permissions // admin: unrestricted within MASH
	if e.AdminCreds, _, err = e.mint("admin", full); err != nil {
		e.Shutdown()
		return nil, err
	}
	return e, nil
}

func (e *Env) start() error {
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
		MaxPayload:       e.maxPay,
	}
	if e.tls {
		tc, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: e.serverCert, KeyFile: e.serverKey, CaFile: e.CAFile, Verify: e.verify, Timeout: 5})
		if err != nil {
			return err
		}
		opts.TLSConfig, opts.TLSTimeout, opts.TLSVerify = tc, 5, e.verify
	}
	s, err := server.NewServer(opts)
	if err != nil {
		return err
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		s.Shutdown()
		return errors.New("nats-server did not start")
	}
	e.srv = s
	e.URL = s.ClientURL()
	e.port = s.Addr().(*net.TCPAddr).Port
	return nil
}

// Restart stops the server and starts it again on the same port and store.
func (e *Env) Restart(t *testing.T) {
	t.Helper()
	e.srv.Shutdown()
	e.srv.WaitForShutdown()
	if err := e.start(); err != nil {
		t.Fatal(err)
	}
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
	return e.minted(t, name, admin.Permissions(e.Owner, name))
}

// MintRaw issues a user with arbitrary permissions (for negative tests).
func (e *Env) MintRaw(t *testing.T, name string, p jwt.Permissions) (string, string) {
	t.Helper()
	return e.minted(t, name, p)
}

func (e *Env) minted(t *testing.T, name string, p jwt.Permissions) (string, string) {
	t.Helper()
	path, pub, err := e.mint(name, p)
	if err != nil {
		t.Fatal(err)
	}
	return path, pub
}

func (e *Env) mint(name string, p jwt.Permissions) (string, string, error) {
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
		return "", "", err
	}
	return path, upub, nil
}

// MintEndpoint is Mint without a test.
func (e *Env) MintEndpoint(name string) (creds, userPub string, err error) {
	return e.mint(name, admin.Permissions(e.Owner, name))
}

// Conn connects with a creds file; it is closed when the test ends.
func (e *Env) Conn(t *testing.T, creds string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(e.URL, append(natsauth.TLSOptions(e.CAFile, e.ClientCert, e.ClientKey), natsauth.UserCredentials(creds))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// ConnAs connects as endpoint name, using its private inbox prefix.
func (e *Env) ConnAs(t *testing.T, creds, name string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(e.URL, append(natsauth.TLSOptions(e.CAFile, e.ClientCert, e.ClientKey), natsauth.UserCredentials(creds), nats.CustomInboxPrefix("_INBOX."+name))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// Connections is the number of client connections the server holds now.
func (e *Env) Connections() int { return e.srv.NumClients() }

// MintUser issues a user whose publish and subscribe allow lists are exactly pub and sub, and returns its .creds
// bytes and public key. Nothing is written to disk. It is what a fake Synadia hands out for a user it was asked to
// create with those lists.
func (e *Env) MintUser(pub, sub []string) (creds []byte, userPub string, err error) {
	var p jwt.Permissions
	p.Pub.Allow.Add(pub...)
	p.Sub.Allow.Add(sub...)
	return e.MintUserWith(p, nil)
}

// MintUserWith is MintUser for a full permission set (deny lists, a response permission) and, if limits is not nil,
// the user's limits (otherwise unlimited, as a user created with -1 limits has).
func (e *Env) MintUserWith(p jwt.Permissions, limits *jwt.NatsLimits) (creds []byte, userPub string, err error) {
	return e.MintUserMod(p, limits, nil)
}

// MintUserMod is MintUserWith that lets mod change the claims (connection types, source CIDRs, times) before signing.
func (e *Env) MintUserMod(p jwt.Permissions, limits *jwt.NatsLimits, mod func(*jwt.UserClaims)) (creds []byte, userPub string, err error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return nil, "", err
	}
	userPub, err = ukp.PublicKey()
	if err != nil {
		return nil, "", err
	}
	seed, err := ukp.Seed()
	if err != nil {
		return nil, "", err
	}
	uc := jwt.NewUserClaims(userPub)
	uc.Name = "minted-user"
	uc.IssuerAccount = e.Account
	uc.Permissions = p
	if limits != nil {
		uc.NatsLimits = *limits
	}
	if mod != nil {
		mod(uc)
	}
	tok, err := uc.Encode(e.acctSK)
	if err != nil {
		return nil, "", err
	}
	creds, err = jwt.FormatUserConfig(tok, seed)
	return creds, userPub, err
}
