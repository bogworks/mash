package policyapply

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
)

// AT-014 (contract mash-messaging-policy, REQ-015): a list set adds foo.c.new, which foo.c.core must not reach, while
// mail is shared in foo.c, and newly denies the existing foo.c.docs to foo.c.core. No inbox or credential exists for
// foo.c.new until core's gate has passed on a live two-server cluster; afterwards core cannot mail foo.c.new, and the
// denial of docs is reported applied only with core's gate passed.

// liveEvCluster records the time of every push the run makes.
type liveEvCluster struct {
	Cluster
	ev *events
}

func (c liveEvCluster) Push(ctx context.Context, tok string, expect []string) ([]apply.ServerAck, error) {
	c.ev.add("push")
	return c.Cluster.Push(ctx, tok, expect)
}

// liveProv is the test's Provisioner: CreateInbox checks, at the moment it is called, that every server holds the
// target generation, and IssueCredential mints a real user of foo.c.new.
type liveProv struct {
	t      *testing.T
	e      *at010Env
	ctx    context.Context
	ev     *events
	cands  []Candidate
	user   ProbeCred
	mu     sync.Mutex
	creds  []byte
	issued time.Time
}

func (p *liveProv) CreateInbox(_ context.Context, address string) error {
	p.ev.add("create:" + address)
	held := p.e.heldJWTs(p.ctx)
	if len(held) != 2 {
		p.t.Errorf("servers answering at CreateInbox: %d", len(held))
	}
	for n, c := range held {
		if g, ok := generationOfClaims(c, p.cands); !ok || g != 2 {
			p.t.Errorf("CreateInbox(%s) while server %s holds generation %d (%v): the gate had not passed", address, n, g, ok)
		}
	}
	return nil
}

func (p *liveProv) IssueCredential(_ context.Context, address string) error {
	p.ev.add("issue:" + address)
	creds, _ := p.e.realUser(p.user)
	p.mu.Lock()
	p.creds, p.issued = creds, time.Now()
	p.mu.Unlock()
	return nil
}

func TestAT014Live(t *testing.T) {
	if testing.Short() {
		t.Skip("live two-server cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e := newLiveEnv(t, at014Prev())
	c1 := compile(t, at014Prev())
	_, c2, err := listset.Accept(listset.Record{Generation: 1}, at014Target(), []byte("at014"))
	if err != nil {
		t.Fatal(err)
	}
	// The old key of core, as its host minted it: revoked by the change.
	var core ProbeCred
	for _, c := range credsOf(c1) {
		if c.Sender == "foo.c.core" {
			core = c
		}
	}
	oldCreds, oldPub := e.realUser(core)
	ex, err := NewExpect(c1, c2, []string{oldPub})
	if err != nil {
		t.Fatal(err)
	}
	order := NewOrder(c1, c2, ex)
	if len(order.NewAddresses) != 1 || order.NewAddresses[0].Address != "foo.c.new" ||
		!slices.Equal(order.NewAddresses[0].MustRefuse, []string{"foo.c.core"}) {
		t.Fatalf("order = %+v", order)
	}
	if len(order.NewlyDenied) != 1 || order.NewlyDenied[0] != (Denial{Sender: "foo.c.core", Receiver: "foo.c.docs"}) {
		t.Fatalf("order = %+v", order)
	}

	prober := &LiveProber{URLs: e.urls, Mint: e.mint}
	wit := &LiveWitnesses{}
	t.Cleanup(wit.Close)
	revoke := sync.OnceValue(func() error {
		return RevokeMinted(context.Background(), e, e.rec, time.Now(), prober, wit)
	})
	t.Cleanup(func() { _ = revoke() })
	// A server reports the account JWT only once the account is loaded, which the first connection does.
	if err := wit.Open(ctx, e.urls, e.mint, credsOf(c1)); err != nil {
		t.Fatalf("open witnesses: %v", err)
	}

	ev := &events{}
	var newUser ProbeCred
	for _, c := range credsOf(c2) {
		if c.Sender == "foo.c.new" {
			newUser = c
		}
	}
	prov := &liveProv{
		t: t, e: e, ctx: ctx, ev: ev, user: newUser,
		cands: []Candidate{{2, c2, ex.RevokedKeys}, {1, c1, nil}},
	}
	in := RunInput{
		Cluster: liveEvCluster{e.cluster, ev}, Prober: prober, Witnesses: wit, Ledger: NewMemLedger(listset.Record{Generation: 1}),
		Account: e.acctPub, Inventory: 2, ListSet: at014Target(), Received: []byte("at014"),
		Previous: c1, PreviousJWT: e.account(c1), RevokedKeys: ex.RevokedKeys,
		Sign: func(_ listset.Record, c *listset.Compiled) (string, error) {
			e.waitNewSecond()
			tok := e.account(c, oldPub)
			e.setCurrent(tok)
			return tok, nil
		},
	}
	os, err := RunOrdered(ctx, in, order, prov)
	if err != nil {
		t.Fatal(err)
	}
	st := os.Status
	if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("status: %+v", st)
	}
	// Nothing for foo.c.new before the push, and the steps in order after it.
	log := ev.get()
	pushes := 0
	for _, s := range log {
		if s == "push" {
			pushes++
		}
	}
	if pushes == 0 || log[0] != "push" || log[len(log)-2] != "create:foo.c.new" || log[len(log)-1] != "issue:foo.c.new" {
		t.Errorf("events = %v", log)
	}
	if !slices.Equal(os.Provisioned, []string{"foo.c.new"}) || len(os.Held) != 0 || len(os.Errors) != 0 {
		t.Errorf("ordered status = %+v", os)
	}
	// The denial of docs to core is reported applied, with core's gate passed.
	if !Applied(st, order.NewlyDenied[0]) || !SenderPassed(st, "foo.c.core") {
		t.Error("the denial of foo.c.docs to foo.c.core is not reported applied")
	}

	// After the gate core cannot mail foo.c.new on any server: the old credential (where it can be probed) and the
	// target credential are both refused.
	probed := 0
	for n := range e.urls {
		for _, r := range slices.Concat(ex.Removed, ex.CredentialOnly) {
			if r.Receiver != "foo.c.new" {
				continue
			}
			cred := r.Probe
			if slices.ContainsFunc(ex.CredentialOnly, func(x RemovedReach) bool { return x.Sender == r.Sender && x.Verb == r.Verb && x.Receiver == r.Receiver }) {
				cred = r.Current
			}
			refused, perr := prober.Refused(ctx, n, cred, r.Op, r.Subject)
			if perr != nil || !refused {
				t.Errorf("server %s: %s %s as %s: refused=%v err=%v", n, r.Op, r.Subject, r.Sender, refused, perr)
			}
			probed++
		}
	}
	if probed < 2 {
		t.Errorf("probed %d (server, reach) pairs", probed)
	}
	// foo.c.new's credential exists and works on every server, and core's old key is refused.
	prov.mu.Lock()
	creds, issued := prov.creds, prov.issued
	prov.mu.Unlock()
	if len(creds) == 0 || issued.IsZero() {
		t.Fatal("no credential issued for foo.c.new")
	}
	for n, u := range e.urls {
		nc, cerr := connectTo(ctx, n, u, creds, 5*time.Second)
		if cerr != nil {
			t.Errorf("server %s refused foo.c.new: %v", n, cerr)
			continue
		}
		nc.Close()
	}
	e.refusedConnect(ctx, oldCreds)
	if err := revoke(); err != nil {
		t.Fatal(err)
	}
}
