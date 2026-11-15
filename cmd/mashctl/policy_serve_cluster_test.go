package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policyserve"
)

const (
	clusterName = "serve-cluster"
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

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l := mustT(net.Listen("tcp", "127.0.0.1:0"))
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newServeEnvCluster is a serveEnv whose server "s1" is one of two routed servers of a cluster, JetStream on both. The
// second, "s2", has its own resolver directory and store, the same operator and account JWTs and no listener
// on the first's files. It returns the env and the second server. The environment is adopted when adopt is true.
func newServeEnvCluster(t *testing.T, adopt bool) (*serveEnv, *server.Server) {
	t.Helper()
	firstRoute, secondRoute := freePort(t), freePort(t)
	routes := server.RoutesFromStr(fmt.Sprintf("nats-route://127.0.0.1:%d,nats-route://127.0.0.1:%d", firstRoute, secondRoute))
	var second *server.Server
	// A JetStream cluster elects its meta leader only when both servers are up, so the second is started before the
	// first, each with the other's route, and both are awaited together.
	e := newServeEnvFull(t, adopt, func(e *applyEnv) { second = startClusterSecond(t, e, secondRoute, routes) }, func(o *server.Options) {
		o.ServerName = "s1"
		o.MaxControlLine = clusterFirstLine
		o.Cluster.Name, o.Cluster.Host, o.Cluster.Port = clusterName, "127.0.0.1", firstRoute
		o.Routes = routes
	}, func(e *applyEnv) {
		waitCluster(t, e, second)
	})
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

// waitCluster waits until the servers route to each other and the account's JetStream answers: the meta group elects its
// leader only when both servers are up.
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

// With two routed servers, `policy serve --servers 2` runs the gate's two-server path: both servers answer the limits
// query, both receive the account JWT and the status says applied for each.
func TestPolicyServeAppliesOnTwoRoutedServers(t *testing.T) {
	withProvisioner(t, &recordingProv{})
	e, _ := newServeEnvCluster(t, true)
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))

	// The system account sees both servers and each reports its own limit.
	var got []policyserve.ServerInfo
	deadline := time.Now().Add(serveWait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), serveWait)
		got, _ = policyserve.ReadServers(ctx, sys, 2)
		cancel()
		if len(got) == 2 {
			break
		}
		time.Sleep(servePoll)
	}
	lines := map[string]int{}
	for _, s := range got {
		lines[s.Name] = s.Limits.MaxConnectLine
	}
	if len(got) != 2 || lines["s1"] != clusterFirstLine || lines["s2"] != clusterSecondLine {
		t.Fatalf("limits read from the servers: %v", lines)
	}

	e.start("--servers", "2")
	e.put(signed(t, e.sub, e.serveDoc(1, 0, nil)))
	st := e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	checkTwoServers(t, st, 1)
	held := e.accountJWTs(sys)
	first := checkHeldJWTs(t, e, held)
	if e.inForce() != 1 {
		t.Fatalf("generation in force %d", e.inForce())
	}

	// Generation 2 only adds an allow: both servers take it.
	e.put(signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"foo.c.lead", "foo.c.sdk"}, "deny": []string{}}
	})))
	st = e.waitStatus("generation 2 applied", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StateApplied })
	checkTwoServers(t, st, 2)
	second := checkHeldJWTs(t, e, e.accountJWTs(sys))
	if second != first {
		t.Error("the servers' template changed on a change that only adds an allow")
	}
	if e.inForce() != 2 {
		t.Fatalf("generation in force %d", e.inForce())
	}
	e.noSeeds(e.logs.String())
	if code := e.shutdown(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.logs.String())
	}
}

// checkTwoServers requires the status to name both servers, none missing and nothing failing.
func checkTwoServers(t *testing.T, st *policyapply.Status, gen uint64) {
	t.Helper()
	names := []string{}
	for _, s := range st.Servers {
		names = append(names, s.Server)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"s1", "s2"}) || len(st.NotConnected) != 0 || len(st.Failing) != 0 || !st.InForce || st.Generation != gen {
		t.Fatalf("status %+v", st)
	}
}

// checkHeldJWTs requires both servers to hold the same account JWT shape (the group's scoped key and its template) and
// returns that shape.
func checkHeldJWTs(t *testing.T, e *serveEnv, held map[string]*jwt.AccountClaims) string {
	t.Helper()
	if len(held) != 2 || held["s1"] == nil || held["s2"] == nil {
		t.Fatalf("account JWTs held by %d servers", len(held))
	}
	gpub := mustT(mustT(e.ks.GroupKey(serveGroup, false)).PublicKey())
	var shapes []string
	for name, c := range held {
		var tpl *jwt.UserScope
		for _, s := range scopedSigners(c) {
			if s.Role == serveGroup {
				tpl = s
			}
		}
		if tpl == nil || tpl.Key != gpub || len(c.SigningKeys) != 1 {
			t.Fatalf("server %s holds no scoped key for %s (%d signing keys)", name, serveGroup, len(c.SigningKeys))
		}
		shapes = append(shapes, string(mustT(json.Marshal(c.SigningKeys))))
	}
	if shapes[0] != shapes[1] {
		t.Fatalf("the servers hold different signing keys:\n%s\n%s", shapes[0], shapes[1])
	}
	return shapes[0]
}
