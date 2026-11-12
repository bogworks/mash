package policyapply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

// AT-010 (contract mash-messaging-policy, REQ-011): the applied-policy gate against a live two-server cluster in
// operator mode, each server with a full account resolver. The list sets are set1 and set2 of the unit tests: set2
// closes mail for the group (a template change, so a probe with the old tags is refused) and takes core's allow of
// docs away (a tag change, so only revoking the old key refuses the old credential and the gate probes the target
// credential).

const (
	// at010Sync is the resolver's sync interval: so long that a server that missed a push does not catch up by itself.
	at010Sync = time.Hour
	at010Wait = 15 * time.Second
)

type at010Env struct {
	t       *testing.T
	op      nkeys.KeyPair
	acct    nkeys.KeyPair
	acctPub string
	sysKP   nkeys.KeyPair
	sysPub  string
	opts    []*jwt.OperatorClaims
	sysJWT  string
	scoped  map[string]nkeys.KeyPair // owner group -> scoped signing key
	dirs    map[string]string
	srvs    map[string]*server.Server
	urls    map[string]string
	sys     *nats.Conn
	cluster NATSCluster

	mu   sync.Mutex
	cur  string // the account JWT last pushed
	iat  int64  // its issue time
	rec  *MemIssueRecord
	mint *ProbeMinter
}

// at010Must returns v, or panics: a setup failure aborts the test and the panic names it.
func at010Must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("at010 setup: %v", err))
	}
	return v
}

func newAT010Env(t *testing.T) *at010Env {
	t.Helper()
	e := &at010Env{t: t, scoped: map[string]nkeys.KeyPair{}, dirs: map[string]string{}, srvs: map[string]*server.Server{}, urls: map[string]string{}, rec: &MemIssueRecord{}}
	e.op = at010Must(nkeys.CreateOperator())
	opPub := at010Must(e.op.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "at010"
	opJWT := at010Must(oc.Encode(e.op))
	e.opts = []*jwt.OperatorClaims{at010Must(jwt.DecodeOperatorClaims(opJWT))}
	e.acct = at010Must(nkeys.CreateAccount())
	e.acctPub = at010Must(e.acct.PublicKey())
	e.sysKP = at010Must(nkeys.CreateAccount())
	e.sysPub = at010Must(e.sysKP.PublicKey())
	sc := jwt.NewAccountClaims(e.sysPub)
	sc.Name = "SYS"
	e.sysJWT = at010Must(sc.Encode(e.op))
	e.scoped["foo.c"] = at010Must(nkeys.CreateAccount())
	e.mint = &ProbeMinter{Signer: e, Account: e.acctPub, Record: e.rec}

	gen1 := e.account(compile(t, set1()))
	e.setCurrent(gen1)
	for _, n := range []string{"s1", "s2"} {
		e.dirs[n] = t.TempDir()
	}
	s1 := e.start("s1", true, "")
	routes := fmt.Sprintf("nats-route://127.0.0.1:%d", s1.ClusterAddr().Port)
	e.start("s2", true, routes)
	e.waitRoutes()

	sysUser := at010Must(nkeys.CreateUser())
	sysUserPub := at010Must(sysUser.PublicKey())
	tok := at010Must(jwt.NewUserClaims(sysUserPub).Encode(e.sysKP))
	e.sys = at010Must(nats.Connect(e.urls["s1"], nats.UserJWT(
		func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return sysUser.Sign(n) })))
	t.Cleanup(e.sys.Close)
	e.cluster = NATSCluster{Conn: e.sys, Inventory: 2}
	return e
}

func (e *at010Env) setCurrent(tok string) {
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		e.t.Fatal(err)
	}
	e.mu.Lock()
	e.cur, e.iat = tok, c.IssuedAt
	e.mu.Unlock()
}

// SignUser makes the env the probe minter's GroupSigner.
func (e *at010Env) SignUser(group string, uc *jwt.UserClaims) (string, error) {
	kp, ok := e.scoped[group]
	if !ok {
		return "", errors.New("no scoped key for " + group)
	}
	return uc.Encode(kp)
}

// account builds the account JWT of a compiled generation: a scoped signing key per owner group, role = group, and the
// revocations.
func (e *at010Env) account(c *listset.Compiled, revoked ...string) string {
	ac := jwt.NewAccountClaims(e.acctPub)
	ac.Name = "MASH"
	for _, tpl := range c.Templates {
		sc := jwt.NewUserScope()
		sc.Key = at010Must(e.scoped[tpl.Group].PublicKey())
		sc.Role = tpl.Group
		sc.Template.Permissions = tpl.Permissions
		ac.SigningKeys.AddScopedSigner(sc)
	}
	for _, r := range revoked {
		ac.Revoke(r)
	}
	return at010Must(ac.Encode(e.op))
}

// waitNewSecond waits until the clock is past the issue time of the JWT last pushed: a server keeps the newer JWT of
// two, and an issue time has a resolution of one second.
func (e *at010Env) waitNewSecond() {
	e.mu.Lock()
	iat := e.iat
	e.mu.Unlock()
	for time.Now().Unix() <= iat {
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *at010Env) start(name string, fresh bool, routes string) *server.Server {
	t := e.t
	t.Helper()
	res := at010Must(server.NewDirAccResolver(e.dirs[name], 0, at010Sync, server.NoDelete))
	if fresh {
		if err := res.Store(e.sysPub, e.sysJWT); err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		cur := e.cur
		e.mu.Unlock()
		if err := res.Store(e.acctPub, cur); err != nil {
			t.Fatal(err)
		}
	}
	o := &server.Options{
		Host: "127.0.0.1", Port: -1, ServerName: name, NoLog: true, NoSigs: true,
		TrustedOperators: e.opts, AccountResolver: res, SystemAccount: e.sysPub,
	}
	o.Cluster.Name = "at010"
	o.Cluster.Host = "127.0.0.1"
	o.Cluster.Port = -1
	if routes != "" {
		o.Routes = server.RoutesFromStr(routes)
	}
	s := at010Must(server.NewServer(o))
	go s.Start()
	if !s.ReadyForConnections(at010Wait) {
		t.Fatalf("nats-server %s did not start", name)
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	e.srvs[name] = s
	e.urls[name] = s.ClientURL()
	return s
}

func (e *at010Env) stop(name string) {
	e.srvs[name].Shutdown()
	e.srvs[name].WaitForShutdown()
	delete(e.srvs, name)
	delete(e.urls, name)
}

func (e *at010Env) waitRoutes() {
	e.t.Helper()
	deadline := time.Now().Add(at010Wait)
	for time.Now().Before(deadline) {
		ok := len(e.srvs) == 2
		for _, s := range e.srvs {
			ok = ok && s.NumRoutes() > 0
		}
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("the servers did not route to each other")
}

// waitServers waits until the system account sees both servers.
func (e *at010Env) waitServers(ctx context.Context) {
	e.t.Helper()
	deadline := time.Now().Add(at010Wait)
	for time.Now().Before(deadline) {
		if set, err := e.cluster.Servers(ctx); err == nil && set.Complete(2) {
			return
		}
	}
	e.t.Fatal("the system account does not see both servers")
}

// pushTo pushes tok to the named servers and requires each to acknowledge it.
func (e *at010Env) pushTo(ctx context.Context, tok string, names ...string) {
	e.t.Helper()
	acks, err := e.cluster.Push(ctx, tok, names)
	if err != nil {
		e.t.Fatalf("push: %v", err)
	}
	got := map[string]bool{}
	for _, a := range acks {
		if !a.OK() {
			e.t.Fatalf("push refused by %s: %d %s", a.Server, a.Code, a.Message)
		}
		got[a.Server] = true
	}
	for _, n := range names {
		if !got[n] {
			e.t.Fatalf("no acknowledgement from %s: %+v", n, acks)
		}
	}
	e.setCurrent(tok)
}

// Revoke is the test's Revoker: it adds the keys to the revocation list of the JWT last pushed, signs it with the
// operator key and pushes it to every server.
func (e *at010Env) Revoke(ctx context.Context, pubs []string) error {
	e.waitNewSecond()
	e.mu.Lock()
	cur := e.cur
	e.mu.Unlock()
	claims, err := jwt.DecodeAccountClaims(cur)
	if err != nil {
		return err
	}
	for _, p := range pubs {
		claims.Revoke(p)
	}
	tok, err := claims.Encode(e.op)
	if err != nil {
		return err
	}
	set, err := e.cluster.Servers(ctx)
	if err != nil {
		return err
	}
	acks, err := e.cluster.Push(ctx, tok, set.Names)
	if err != nil {
		return err
	}
	if len(acks) != len(set.Names) {
		return fmt.Errorf("%d acknowledgements for %d servers", len(acks), len(set.Names))
	}
	for _, a := range acks {
		if !a.OK() {
			return fmt.Errorf("refused by %s: %d %s", a.Server, a.Code, a.Message)
		}
	}
	e.setCurrent(tok)
	return nil
}

// realUser mints a real user of the sender, as its host would have: a key pair whose seed mash never sees. Only the
// public key and the credentials (to test a connection) are returned.
func (e *at010Env) realUser(p ProbeCred) (creds []byte, pub string) {
	kp := at010Must(nkeys.CreateUser())
	pub = at010Must(kp.PublicKey())
	uc := jwt.NewUserClaims(pub)
	uc.SetScoped(true)
	uc.Name = p.Name
	uc.Tags.Add(p.Tags...)
	uc.IssuerAccount = e.acctPub
	tok := at010Must(e.SignUser(p.Sender[:strings.LastIndex(p.Sender, ".")], uc))
	seed := at010Must(kp.Seed())
	return at010Must(jwt.FormatUserConfig(tok, seed)), pub
}

func credsOf(c *listset.Compiled) []ProbeCred {
	var out []ProbeCred
	for _, u := range c.Users {
		out = append(out, ProbeCred{Sender: u.Sender, Name: u.Name, Tags: slices.Clone(u.Tags)})
	}
	return out
}

func (e *at010Env) gateInput(rec listset.Record, c2 *listset.Compiled, ex Expect, p *LiveProber, w *LiveWitnesses, c1 *listset.Compiled) GateInput {
	return GateInput{
		Cluster: e.cluster, Prober: p, Witnesses: w, Account: e.acctPub, Inventory: 2,
		Record: rec, Compiled: c2, Previous: &Candidate{Generation: 1, Compiled: c1}, Expect: ex,
	}
}

// heldJWTs reads the account JWT each server holds.
func (e *at010Env) heldJWTs(ctx context.Context) map[string]*jwt.AccountClaims {
	e.t.Helper()
	m, err := e.cluster.AccountJWTs(ctx, e.acctPub)
	if err != nil {
		e.t.Fatalf("account JWTs: %v", err)
	}
	out := map[string]*jwt.AccountClaims{}
	for n, tok := range m {
		c, err := jwt.DecodeAccountClaims(tok)
		if err != nil {
			e.t.Fatalf("server %s holds no readable account JWT: %v", n, err)
		}
		out[n] = c
	}
	return out
}

// checkProbesRevoked requires every key revoked on every server and marked revoked in the issue record, and every
// entry of the record to be a probe.
func (e *at010Env) checkProbesRevoked(ctx context.Context, keys []string) {
	e.t.Helper()
	held := e.heldJWTs(ctx)
	if len(held) != 2 {
		e.t.Fatalf("servers answered: %d", len(held))
	}
	for n, c := range held {
		for _, k := range keys {
			if _, ok := c.Revocations[k]; !ok {
				e.t.Errorf("server %s does not revoke %s", n, k)
			}
		}
	}
	entries := e.rec.Entries()
	byKey := map[string]IssueEntry{}
	for _, en := range entries {
		if en.Purpose != PurposeProbe {
			e.t.Errorf("entry %+v is not a probe", en)
		}
		if en.RevokedAt == nil {
			e.t.Errorf("probe %s for %s is not marked revoked", en.PublicKey, en.Address)
		}
		if !en.Expires.After(en.IssuedAt) || en.Expires.Sub(en.IssuedAt) > 10*time.Minute {
			e.t.Errorf("probe %s expires %s after it was issued", en.PublicKey, en.Expires.Sub(en.IssuedAt))
		}
		byKey[en.PublicKey] = en
	}
	for _, k := range keys {
		if _, ok := byKey[k]; !ok {
			e.t.Errorf("minted probe %s is not in the issue record", k)
		}
	}
}

// refusedConnect requires that a connection with the credentials is refused by every server in urls.
func (e *at010Env) refusedConnect(ctx context.Context, creds []byte) {
	e.t.Helper()
	for n, u := range e.urls {
		nc, err := connectTo(ctx, n, u, creds, 5*time.Second)
		if err == nil {
			nc.Close()
			e.t.Errorf("server %s accepted the revoked key", n)
		}
	}
}

type at010Run struct {
	e       *at010Env
	c1, c2  *listset.Compiled
	rec     listset.Record
	ex      Expect
	real    []byte
	realPub string
	prober  *LiveProber
	wit     *LiveWitnesses
	revoke  func() error
}

func newAT010Run(t *testing.T, e *at010Env) *at010Run {
	t.Helper()
	r := &at010Run{e: e, c1: compile(t, set1())}
	rec, c2, err := listset.Accept(listset.Record{Generation: 1}, set2(), []byte("at010"))
	if err != nil {
		t.Fatal(err)
	}
	r.rec, r.c2 = rec, c2
	// The old key of core, the sender whose tags narrow: a real user, minted as its host would.
	var core ProbeCred
	for _, c := range credsOf(r.c1) {
		if c.Sender == "foo.c.core" {
			core = c
		}
	}
	r.real, r.realPub = e.realUser(core)
	if r.ex, err = NewExpect(r.c1, r.c2, []string{r.realPub}); err != nil {
		t.Fatal(err)
	}
	if len(r.ex.Removed) == 0 || len(r.ex.CredentialOnly) == 0 {
		t.Fatalf("the fixture must have both kinds of narrowing: %d and %d", len(r.ex.Removed), len(r.ex.CredentialOnly))
	}
	r.prober = &LiveProber{URLs: e.urls, Mint: e.mint}
	r.wit = &LiveWitnesses{}
	t.Cleanup(r.wit.Close)
	// The probes are revoked when the gate ends, pass or fail: here by a cleanup, and explicitly where the test checks.
	r.revoke = sync.OnceValue(func() error {
		return RevokeMinted(context.Background(), e, e.rec, time.Now(), r.prober, r.wit)
	})
	t.Cleanup(func() { _ = r.revoke() })
	return r
}

// openWitnesses opens one witness per sender on each server in urls, with the previous generation's credentials.
// The prober shares the env's URL map, so it sees the new URL of a server that restarted.
func (r *at010Run) openWitnesses(ctx context.Context, urls map[string]string) {
	r.e.t.Helper()
	if err := r.wit.Open(ctx, urls, r.e.mint, credsOf(r.c1)); err != nil {
		r.e.t.Fatalf("open witnesses: %v", err)
	}
}

func (r *at010Run) minted() []string {
	all := slices.Concat(r.prober.Minted(), r.wit.Minted())
	sort.Strings(all)
	return slices.Compact(all)
}

func TestAT010Live(t *testing.T) {
	if testing.Short() {
		t.Skip("live two-server cluster")
	}
	t.Run("applied", at010Applied)
	t.Run("held_back", at010HeldBack)
}

// at010Applied: the narrowing is pushed to both servers and the gate passes.
func at010Applied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := newAT010Env(t)
	r := newAT010Run(t, e)

	// A server reports the account JWT only once the account is loaded, which the first connection does.
	r.openWitnesses(ctx, e.urls)
	// Before the push both servers hold the previous generation.
	for n, c := range e.heldJWTs(ctx) {
		if g, ok := generationOfClaims(c, []Candidate{{2, r.c2, r.ex.RevokedKeys}, {1, r.c1, nil}}); !ok || g != 1 {
			t.Fatalf("server %s holds generation %d (%v) before the push", n, g, ok)
		}
	}

	e.waitNewSecond()
	j2 := e.account(r.c2, r.realPub)
	e.pushTo(ctx, j2, "s1", "s2")

	st, err := Gate(ctx, e.gateInput(r.rec, r.c2, r.ex, r.prober, r.wit, r.c1))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("gate: %+v", st)
	}
	if g := gens(st); g["s1"] != "2" || g["s2"] != "2" {
		t.Errorf("servers = %v", g)
	}

	// The gate ran every probe on every server, and every witness is closed.
	if got, want := len(r.prober.Minted()), 2*(len(r.ex.Removed)+len(r.ex.CredentialOnly)); got != want {
		t.Errorf("probes minted = %d, want %d", got, want)
	}
	closed, _ := r.wit.Closed(ctx)
	if len(closed) != 2*len(r.c1.Users) {
		t.Errorf("witnesses = %d", len(closed))
	}
	for k, c := range closed {
		if !c {
			t.Errorf("witness %+v is open", k)
		}
	}

	// The probes are revoked at the end and the record says so; the real old key is refused on both servers.
	if err := r.revoke(); err != nil {
		t.Fatal(err)
	}
	e.checkProbesRevoked(ctx, r.minted())
	e.refusedConnect(ctx, r.real)
}

// at010HeldBack: server s2 does not take the push. It is down while the push is made and restarts afterwards with its
// resolver directory still holding the previous JWT; its resolver's sync interval is an hour, so it does not catch up
// by itself. The gate says partial and names s2; Converge brings s2 to the target.
func at010HeldBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e := newAT010Env(t)
	r := newAT010Run(t, e)

	e.stop("s2")
	r.openWitnesses(ctx, map[string]string{"s1": e.urls["s1"]})

	e.waitNewSecond()
	j2 := e.account(r.c2, r.realPub)
	e.pushTo(ctx, j2, "s1")

	routes := fmt.Sprintf("nats-route://127.0.0.1:%d", e.srvs["s1"].ClusterAddr().Port)
	e.start("s2", false, routes)
	e.waitRoutes()
	e.waitServers(ctx)
	// A connection to s2 before it takes the change: it loads the account from s2's own directory.
	r.openWitnesses(ctx, map[string]string{"s2": e.urls["s2"]})

	gi := e.gateInput(r.rec, r.c2, r.ex, r.prober, r.wit, r.c1)
	st, err := Gate(ctx, gi)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePartial || st.InForce {
		t.Fatalf("held back: %+v", st)
	}
	if g := gens(st); g["s1"] != "2" || g["s2"] != "1" {
		t.Fatalf("servers = %v (s2 must still hold the previous generation)", g)
	}
	if len(st.Failing) == 0 {
		t.Fatal("no failure")
	}
	for _, f := range st.Failing {
		if f.Server != "s2" {
			t.Errorf("failure names %q, want only s2: %+v", f.Server, f)
		}
	}
	if !hasFailure(st, "s2", "", CheckGeneration) || !hasFailure(st, "s2", "", CheckRevoked) {
		t.Errorf("failing = %+v", st.Failing)
	}
	b, _ := json.Marshal(st)
	for _, want := range []string{`"in_force":false`, `"state":"partial"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("status JSON lacks %s: %s", want, b)
		}
	}

	st, err = Converge(ctx, gi, j2, e.cur0(r))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("converge: %+v", st)
	}
	if g := gens(st); g["s1"] != "2" || g["s2"] != "2" {
		t.Errorf("servers = %v", g)
	}

	if err := r.revoke(); err != nil {
		t.Fatal(err)
	}
	e.checkProbesRevoked(ctx, r.minted())
	e.refusedConnect(ctx, r.real)
}

// cur0 is the previous generation's JWT, the one Converge would push back.
func (e *at010Env) cur0(r *at010Run) string { return e.account(r.c1) }
