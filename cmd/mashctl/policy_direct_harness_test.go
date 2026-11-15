package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
)

const (
	// serveWait bounds every wait of the direct test; a loaded runner under -race has taken several seconds for one apply.
	serveWait = 60 * time.Second
	// servePoll is how often a wait looks again.
	servePoll = 50 * time.Millisecond

	clusterName = "direct-cluster"
	// clusterSync is the resolver's sync interval: so long that a server that missed a push does not catch up by itself,
	// which is what makes a push to every server visible.
	clusterSync = time.Hour
	// clusterFirstLine and clusterSecondLine are the max_control_line of the two servers, different so that the limits
	// read from each are told apart.
	clusterFirstLine  = 4096
	clusterSecondLine = 2048
	// clusterProbeWait bounds one attempt to ask the account's JetStream before the cluster has a leader.
	clusterProbeWait = 2 * time.Second
)

// mustT is its value, or a panic (which fails the test) when err is set: mustT(f()) for a two-value f.
func mustT[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// serveEnv is an operator-mode server, initialised, with the admin's connection. Nothing serves on it.
type serveEnv struct {
	*applyEnv
	nc *nats.Conn // the admin's connection
}

// policyServeVet is the check of every client URL the driver derives for a server: the TLS requirement and the
// loopback rule of the admin connection.
func policyServeVet(insecureLoopback bool) func(string) error {
	return func(u string) error {
		_, err := natsauth.RequireTLS(u, insecureLoopback)
		return err
	}
}

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l := mustT(net.Listen("tcp", "127.0.0.1:0"))
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newServeEnvCluster is a serveEnv whose server "s1" is one of two routed servers of a cluster, JetStream on both. The
// second, "s2", has its own resolver directory and store and the same operator and account JWTs. The account is
// adopted when adopt is true.
func newServeEnvCluster(t *testing.T, adopt bool) (*serveEnv, *server.Server) {
	t.Helper()
	firstRoute, secondRoute := freePort(t), freePort(t)
	routes := server.RoutesFromStr(fmt.Sprintf("nats-route://127.0.0.1:%d,nats-route://127.0.0.1:%d", firstRoute, secondRoute))
	var second *server.Server
	// A JetStream cluster elects its meta leader only when both servers are up, so the second is started before the
	// first, each with the other's route, and both are awaited together.
	ae := newApplyEnvOpts(t, func(e *applyEnv) { second = startClusterSecond(t, e, secondRoute, routes) }, func(o *server.Options) {
		o.ServerName = "s1"
		o.MaxControlLine = clusterFirstLine
		o.Cluster.Name, o.Cluster.Host, o.Cluster.Port = clusterName, "127.0.0.1", firstRoute
		o.Routes = routes
	})
	waitCluster(t, ae, second)
	e := &serveEnv{applyEnv: ae}
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	if adopt {
		e.adoptLater()
	}
	e.nc = mustT(nats.Connect(e.srv.ClientURL(), append(natsauth.TLSOptions(e.tlsCA, "", ""), natsauth.UserCredentials(e.env["MASH_ADMIN_CREDS"]))...))
	t.Cleanup(e.nc.Close)
	return e, second
}

// clusterSecondDir is the resolver directory of the second server of a cluster env, by env: a test that makes the server
// refuse a push takes the write permission off it.
var clusterSecondDir sync.Map

// startClusterSecond starts the second server of the cluster over the first's operator and account JWTs.
func startClusterSecond(t *testing.T, e *applyEnv, route int, routes []*url.URL) *server.Server {
	t.Helper()
	opts := mustT(server.ProcessConfigFile(e.pl.ConfPath()))
	dir := t.TempDir()
	clusterSecondDir.Store(e, dir)
	res := mustT(server.NewDirAccResolver(dir, 0, clusterSync, server.NoDelete))
	// The first server's JWTs are in its config, in the resolver preload; its resolver directory is not written yet.
	conf := mustT(os.ReadFile(e.pl.ConfPath()))
	for _, pub := range []string{e.pl.System, e.acctPub} {
		m := regexp.MustCompile(pub + `:\s*"([^"]+)"`).FindSubmatch(conf)
		if m == nil {
			t.Fatalf("no JWT for %s in the server config", pub)
		}
		if err := res.Store(pub, string(m[1])); err != nil {
			t.Fatal(err)
		}
	}
	opts.Port, opts.NoLog, opts.NoSigs = -1, true, true
	opts.ServerName = "s2"
	opts.MaxControlLine = clusterSecondLine
	opts.AccountResolver = res
	opts.JetStream, opts.StoreDir = true, t.TempDir()
	opts.Cluster.Name, opts.Cluster.Host, opts.Cluster.Port = clusterName, "127.0.0.1", route
	opts.Routes = routes
	s := mustT(server.NewServer(opts))
	go s.Start()
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	return s
}

// waitCluster waits until the servers route to each other and the account's JetStream answers.
func waitCluster(t *testing.T, e *applyEnv, second *server.Server) {
	t.Helper()
	if !second.ReadyForConnections(serveWait) {
		t.Fatal("the second server did not start")
	}
	deadline := time.Now().Add(serveWait)
	var last error
	for time.Now().Before(deadline) {
		if e.srv.NumRoutes() > 0 && second.NumRoutes() > 0 {
			nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.Creds))
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), clusterProbeWait)
				js, jerr := jetstream.New(nc)
				_, ierr := js.AccountInfo(ctx)
				cancel()
				nc.Close()
				if jerr == nil && ierr == nil {
					return
				}
				last = errors.Join(jerr, ierr)
			} else {
				last = err
			}
		}
		time.Sleep(servePoll)
	}
	t.Fatalf("the servers did not form a JetStream cluster: %v", last)
}

// accountJWTs reads the account JWT each server holds, through the system account as the gate does.
func (e *serveEnv) accountJWTs(sys *nats.Conn) map[string]*jwt.AccountClaims {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	m, err := policyapply.NATSCluster{Conn: sys, Inventory: 2}.AccountJWTs(ctx, e.acctPub)
	if err != nil {
		e.t.Fatalf("account JWTs: %v", err)
	}
	out := map[string]*jwt.AccountClaims{}
	for n, tok := range m {
		out[n] = mustT(jwt.DecodeAccountClaims(tok))
	}
	return out
}

// serveDoc is the document of one generation; mod may change the fields.
func (e *serveEnv) serveDoc(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": policydoc.DocumentSchema, "account": e.acctPub, "generation": gen, "base_generation": base,
		"issued_at": "2026-11-14T12:00:00Z",
		"groups":    map[string]any{},
		"senders":   map[string]any{},
	}
	if mod != nil {
		mod(m)
	}
	return mustT(json.Marshal(m))
}

// adoptLater adopts the account after its endpoints were made, by writing the adoption marker.
func (e *serveEnv) adoptLater() {
	e.t.Helper()
	by, err := credsUserKey(e.env["MASH_ADMIN_CREDS"])
	if err != nil {
		e.t.Fatal(err)
	}
	submitter := mustT(mustT(nkeys.CreateAccount()).PublicKey())
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	if _, _, err := e.adminOnly().Adopt(ctx, e.acctPub, submitter, by, nil); err != nil {
		e.t.Fatalf("adopt: %v", err)
	}
}

// add runs `mashctl add NAME` in the environment and returns the credential path.
func (e *serveEnv) add(name string) string {
	e.t.Helper()
	out, errb, code := e.run("add", name)
	if code != 0 {
		e.t.Fatalf("add %s: %d %s %s", name, code, out, errb)
	}
	return filepath.Join(e.env["MASH_CONFIG_DIR"], name+".creds")
}

// adminOnly is an admin over a connection made with the admin credentials.
func (e *applyEnv) adminOnly() *admin.Admin {
	e.t.Helper()
	a, err := admin.New(e.connect(natsauth.UserCredentials(e.pl.Creds)), e.env["MASH_OWNER"])
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

// lockResolver takes the write permission off every file and directory under dir (or gives it back), so a server whose
// resolver stores its account JWTs there fails to store a pushed one.
func lockResolver(t *testing.T, dir string, lock bool) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		}
		if lock {
			mode &^= 0o200
		}
		return os.Chmod(path, mode)
	})
	if err != nil && lock {
		t.Fatal(err)
	}
}
