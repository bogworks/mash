package apply

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// liveCreds mints creds for a fresh user of the account kp (a user JWT signed by the account key, issued now).
func liveCreds(t *testing.T, acct nkeys.KeyPair) (creds []byte, userPub string) {
	t.Helper()
	ukp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	userPub, _ = ukp.PublicKey()
	uc := jwt.NewUserClaims(userPub)
	uc.Name = "live"
	tok, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := ukp.Seed()
	creds, err = jwt.FormatUserConfig(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	return creds, userPub
}

func credsFile(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "u.creds")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// liveEnv is a real in-process operator-mode nats-server with a full (directory) resolver, holding the key store's
// account and a system account, and a system-account connection to it.
type liveEnv struct {
	ks      *ctlkeys.Authority
	srv     *server.Server
	sysNC   *nats.Conn
	acctKP  nkeys.KeyPair
	acctPub string
}

// startLive starts the server and connects the system user; both are closed when the test ends.
func startLive(t *testing.T, tune ...func(*server.Options)) *liveEnv {
	t.Helper()
	ks, err := ctlkeys.Open(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.InitOperator(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.InitAccount(); err != nil {
		t.Fatal(err)
	}
	opKP, opPub, err := ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	acctKP, acctPub, err := ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	sysKP, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	sysPub, _ := sysKP.PublicKey()

	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "live-operator"
	oc.SystemAccount = sysPub
	opJWT, err := oc.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := jwt.DecodeOperatorClaims(opJWT)
	if err != nil {
		t.Fatal(err)
	}
	acctJWT, err := jwt.NewAccountClaims(acctPub).Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	sysJWT, err := jwt.NewAccountClaims(sysPub).Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}

	res, err := server.NewDirAccResolver(t.TempDir(), 0, time.Minute, server.NoDelete)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Store(sysPub, sysJWT); err != nil {
		t.Fatal(err)
	}
	if err := res.Store(acctPub, acctJWT); err != nil {
		t.Fatal(err)
	}
	so := &server.Options{
		Host: "127.0.0.1", Port: -1, ServerName: "s1", NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{operator}, SystemAccount: sysPub, AccountResolver: res,
	}
	for _, f := range tune {
		f(so)
	}
	srv, err := server.NewServer(so)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}

	// A system user, to push and read the account JWT.
	sysCreds, _ := liveCreds(t, sysKP)
	sysNC, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(credsFile(t, sysCreds)), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sysNC.Close)

	return &liveEnv{ks: ks, srv: srv, sysNC: sysNC, acctKP: acctKP, acctPub: acctPub}
}

// TestRevokeUsersCutsOffAConnectedUser runs RevokeUsers against a real in-process operator-mode nats-server with a
// full (directory) account resolver, and shows that the revoked user's live connection is closed by the server and
// that the same creds are refused on a new connection.
func TestRevokeUsersCutsOffAConnectedUser(t *testing.T) {
	env := startLive(t)
	ks, srv, sysNC, acctKP := env.ks, env.srv, env.sysNC, env.acctKP
	var err error
	// The user: connects, and works.
	creds, userPub := liveCreds(t, acctKP)
	credPath := credsFile(t, creds)
	var mu sync.Mutex
	var lastErr error
	note := func(err error) {
		if err != nil {
			mu.Lock()
			lastErr = err
			mu.Unlock()
		}
	}
	nc, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(credPath), nats.NoReconnect(),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { note(err) }),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) { note(err) }))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if err := nc.Publish("live.before", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("the user does not work before the revocation: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, err := RevokeUsers(ctx, RevokeParams{Keys: ks, Resolver: NATS{Conn: sysNC, Inventory: 1}, Servers: 1}, []string{userPub})
	if err != nil {
		t.Fatalf("RevokeUsers: %v", err)
	}
	if len(got.Revoked) != 1 || got.Acked != 1 || !got.Complete {
		t.Fatalf("%+v", got)
	}

	// The server closes the live connection.
	deadline := time.Now().Add(5 * time.Second)
	for !nc.IsClosed() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !nc.IsClosed() {
		t.Fatal("the revoked user's connection is still open 5s after the update was acknowledged")
	}
	mu.Lock()
	why := lastErr
	mu.Unlock()
	if why == nil || !(errors.Is(why, nats.ErrAuthorization) || strings.Contains(strings.ToLower(why.Error()), "revoked")) {
		t.Fatalf("the connection closed, but not for an authorization reason: %v", why)
	}
	t.Logf("connection closed by the server: %v", why)

	// The same creds no longer connect.
	nc2, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(credPath), nats.NoReconnect(), nats.Timeout(3*time.Second))
	if err == nil {
		nc2.Close()
		t.Fatal("the revoked creds connected")
	}
	if !errors.Is(err, nats.ErrAuthorization) && !strings.Contains(strings.ToLower(err.Error()), "authorization") {
		t.Fatalf("new connect failed, but not with an authorization error: %v", err)
	}
	t.Logf("new connect refused: %v", err)

	// A user that was not revoked is unaffected.
	other, _ := liveCreds(t, acctKP)
	nc3, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(credsFile(t, other)), nats.NoReconnect())
	if err != nil {
		t.Fatalf("an unrevoked user was refused: %v", err)
	}
	nc3.Close()
}
