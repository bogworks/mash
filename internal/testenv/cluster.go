package testenv

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/natsauth"
)

// clusterWait bounds every wait of the cluster; a loaded runner under -race can be slow to elect a leader.
const clusterWait = 60 * time.Second

// clusterSync is how often each node's account resolver asks its peers for newer account JWTs. A node that was down gets
// what it missed (a revocation) at its next sync; the product default is a minute, the cluster here uses a few seconds so a
// test can wait for it.
const clusterSync = 2 * time.Second

// Cluster is N routed nats-server nodes in operator mode, JetStream on every node, each with a full account resolver
// of its own. It has the operator, system account and MASH account of a real deployment, with their seeds in files:
// what `mashctl` needs to retire a user through the system account. Each node keeps its client port, route port,
// store and resolver directory across Stop and Start.
type Cluster struct {
	Owner   string
	Account string // MASH account public key
	// OperatorSeed and AccountSeed are 0600 seed files (the operator signing seed and the account identity seed), as
	// ctlkeys.ImportOperator and ImportAccount take them.
	OperatorSeed, AccountSeed string
	AdminCreds, SysCreds      string // creds files: the MASH account's admin (unrestricted) and a system account user
	// URL is every node's client URL, comma separated, so a connection finds a node that is up.
	URL string

	t        *testing.T
	op       nkeys.KeyPair
	opts     []*jwt.OperatorClaims
	acctKP   nkeys.KeyPair
	sysPub   string
	nodes    []*clusterNode
	cluster  string
	routes   string
	interval time.Duration
}

type clusterNode struct {
	name       string
	clientPort int
	routePort  int
	storeDir   string
	resolver   string
	srv        *server.Server
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// StartCluster starts n routed nodes and waits until JetStream is available for the MASH account on the cluster. The
// nodes are shut down when the test ends.
func StartCluster(t *testing.T, n int) *Cluster { return StartClusterInterval(t, n, clusterSync) }

// StartClusterInterval is StartCluster with the account resolver's sync interval set to interval (the `interval` key of
// the resolver block) on every node.
func StartClusterInterval(t *testing.T, n int, interval time.Duration) *Cluster {
	t.Helper()
	dir := t.TempDir()
	c := &Cluster{Owner: Owner, t: t, cluster: "mash-test-cluster", interval: interval}
	c.op = must(nkeys.CreateOperator())
	opPub := must(c.op.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "test-operator"
	sysKP := must(nkeys.CreateAccount())
	c.sysPub = must(sysKP.PublicKey())
	oc.SystemAccount = c.sysPub
	c.opts = []*jwt.OperatorClaims{must(jwt.DecodeOperatorClaims(must(oc.Encode(c.op))))}

	sc := jwt.NewAccountClaims(c.sysPub)
	sc.Name = "SYS"
	sysJWT := must(sc.Encode(c.op))
	c.acctKP = must(nkeys.CreateAccount())
	c.Account = must(c.acctKP.PublicKey())
	ac := jwt.NewAccountClaims(c.Account)
	ac.Name = "MASH"
	ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: 4 << 30, MemoryStorage: 64 << 20, Streams: 100, Consumer: 200}
	acctJWT := must(ac.Encode(c.op))

	c.OperatorSeed = filepath.Join(dir, "operator.nk")
	c.AccountSeed = filepath.Join(dir, "account.nk")
	for p, kp := range map[string]nkeys.KeyPair{c.OperatorSeed: c.op, c.AccountSeed: c.acctKP} {
		seed := must(kp.Seed())
		if err := os.WriteFile(p, append(seed, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.SysCreds = c.userCreds(dir, "sys", sysKP, jwt.Permissions{})
	c.AdminCreds = c.userCreds(dir, "admin", c.acctKP, jwt.Permissions{})

	var routes []string
	for i := 0; i < n; i++ {
		nd := &clusterNode{name: fmt.Sprintf("n%d", i+1), clientPort: freeTCPPort(t), routePort: freeTCPPort(t),
			storeDir: filepath.Join(dir, fmt.Sprintf("js%d", i+1)), resolver: filepath.Join(dir, fmt.Sprintf("res%d", i+1))}
		c.nodes = append(c.nodes, nd)
		routes = append(routes, fmt.Sprintf("nats-route://127.0.0.1:%d", nd.routePort))
		c.URL += map[bool]string{true: ",", false: ""}[i > 0] + fmt.Sprintf("nats://127.0.0.1:%d", nd.clientPort)
		if err := os.MkdirAll(nd.resolver, 0o700); err != nil {
			t.Fatal(err)
		}
		res := must(server.NewDirAccResolver(nd.resolver, 0, time.Hour, server.NoDelete))
		if err := res.Store(c.sysPub, sysJWT); err != nil {
			t.Fatal(err)
		}
		if err := res.Store(c.Account, acctJWT); err != nil {
			t.Fatal(err)
		}
	}
	c.routes = strings.Join(routes, ",")
	// A JetStream cluster elects its meta leader only when enough nodes are up: start them all, then wait for them.
	for i := range c.nodes {
		c.launch(i)
	}
	for i := range c.nodes {
		c.await(i)
	}
	t.Cleanup(func() {
		for _, nd := range c.nodes {
			if nd.srv != nil {
				nd.srv.Shutdown()
				nd.srv.WaitForShutdown()
			}
		}
	})
	c.waitJetStream(n)
	return c
}

func (c *Cluster) userCreds(dir, name string, signer nkeys.KeyPair, p jwt.Permissions) string {
	ukp := must(nkeys.CreateUser())
	upub := must(ukp.PublicKey())
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.Permissions = p
	tok := must(uc.Encode(signer))
	path := filepath.Join(dir, name+".creds")
	if err := os.WriteFile(path, must(jwt.FormatUserConfig(tok, must(ukp.Seed()))), 0o600); err != nil {
		c.t.Fatal(err)
	}
	return path
}

func (c *Cluster) launch(i int) {
	nd := c.nodes[i]
	res := must(server.NewDirAccResolver(nd.resolver, 0, c.interval, server.NoDelete))
	o := &server.Options{
		Host: "127.0.0.1", Port: nd.clientPort, ServerName: nd.name, NoLog: true, NoSigs: true,
		TrustedOperators: c.opts, AccountResolver: res, SystemAccount: c.sysPub,
		JetStream: true, StoreDir: nd.storeDir,
	}
	o.Cluster.Name, o.Cluster.Host, o.Cluster.Port = c.cluster, "127.0.0.1", nd.routePort
	o.Routes = server.RoutesFromStr(c.routes)
	s := must(server.NewServer(o))
	go s.Start()
	nd.srv = s
}

func (c *Cluster) await(i int) {
	if !c.nodes[i].srv.ReadyForConnections(clusterWait) {
		c.t.Fatalf("nats-server %s did not start", c.nodes[i].name)
	}
}

// NodeName is node i's server name, as the servers report it in their answers.
func (c *Cluster) NodeName(i int) string { return c.nodes[i].name }

// Nodes is the number of nodes the cluster was started with.
func (c *Cluster) Nodes() int { return len(c.nodes) }

// NodeURL is node i's client URL.
func (c *Cluster) NodeURL(i int) string {
	return fmt.Sprintf("nats://127.0.0.1:%d", c.nodes[i].clientPort)
}

// Stop shuts node i down (its store and resolver directory stay). It is the caller's record which node it stopped.
func (c *Cluster) Stop(i int) {
	c.t.Helper()
	nd := c.nodes[i]
	if nd.srv == nil {
		return
	}
	nd.srv.Shutdown()
	nd.srv.WaitForShutdown()
	nd.srv = nil
}

// Start starts node i again on its port, store and resolver directory and waits until it serves clients.
func (c *Cluster) Start(i int) {
	c.t.Helper()
	if c.nodes[i].srv != nil {
		return
	}
	c.launch(i)
	c.await(i)
}

// Launch starts stopped node i without waiting for it; Await waits until it serves clients. Start is both.
func (c *Cluster) Launch(i int) {
	c.t.Helper()
	if c.nodes[i].srv == nil {
		c.launch(i)
	}
}

// Await waits until node i serves clients.
func (c *Cluster) Await(i int) { c.t.Helper(); c.await(i) }

// NumRoutes is the number of routes node i has formed (0 when it is down).
func (c *Cluster) NumRoutes(i int) int {
	if s := c.nodes[i].srv; s != nil {
		return s.NumRoutes()
	}
	return 0
}

// Conn connects with the admin creds to every node (a node that is down is skipped); it is closed when the test ends.
func (c *Cluster) Conn(t *testing.T, creds string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(c.URL, natsauth.UserCredentials(creds), nats.Timeout(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// waitJetStream waits until all n nodes route to each other and the MASH account's JetStream answers, and until a
// 3-replica stream can be placed (a throwaway memory stream, deleted at once) when n >= 3.
func (c *Cluster) waitJetStream(n int) {
	c.t.Helper()
	deadline := time.Now().Add(clusterWait)
	var last error
	for time.Now().Before(deadline) {
		ok := true
		for _, nd := range c.nodes {
			if nd.srv == nil || nd.srv.NumRoutes() < n-1 {
				ok = false
			}
		}
		if ok {
			last = c.probe(n)
			if last == nil {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.t.Fatalf("the nodes did not form a JetStream cluster: %v", last)
}

func (c *Cluster) probe(n int) error {
	nc, err := nats.Connect(c.URL, natsauth.UserCredentials(c.AdminCreds), nats.Timeout(5*time.Second))
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := js.AccountInfo(ctx); err != nil {
		return err
	}
	if n < 3 {
		return nil
	}
	name := "testenv-probe"
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{"testenv.probe"}, Storage: jetstream.MemoryStorage, Replicas: 3}); err != nil {
		return err
	}
	return js.DeleteStream(ctx, name)
}
