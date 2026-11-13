package policyapply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
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

// liveProv is the test's Provisioner. CreateInbox checks, at the moment it is called, that every server holds the
// target generation, that the generation is not yet committed and that foo.c.new has no inbox stream, then creates the
// inbox stream and the durable consumer on the cluster the way mash does (store.InboxConfigIdentity,
// store.ConsumerConfig). IssueCredential mints a real user of foo.c.new. Both are create-if-absent.
type liveProv struct {
	t      *testing.T
	e      *at010Env
	ctx    context.Context
	ev     *events
	cands  []Candidate
	user   ProbeCred
	ledger Ledger
	admin  []byte // credentials of a user signed by the account key, for the JetStream API
	mu     sync.Mutex
	creds  []byte
	issued time.Time
}

// inbox returns the inbox stream and consumer existence of address, read through server name.
func (p *liveProv) inbox(ctx context.Context, name, address string) (stream, consumer bool, err error) {
	nc, err := connectTo(ctx, name, p.e.urls[name], p.admin, 5*time.Second)
	if err != nil {
		return false, false, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return false, false, err
	}
	id, err := p.identity(address)
	if err != nil {
		return false, false, err
	}
	st, err := js.Stream(ctx, sap.InboxStream(id.Flat()))
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if _, err := st.Consumer(ctx, store.Consumer); err != nil {
		if errors.Is(err, jetstream.ErrConsumerNotFound) {
			return true, false, nil
		}
		return true, false, err
	}
	return true, true, nil
}

// identity is the endpoint identity of an address range.owner.name: the range is the agent token position.
func (p *liveProv) identity(address string) (sap.EndpointIdentity, error) {
	parts := strings.Split(address, ".")
	if len(parts) != 3 {
		return sap.EndpointIdentity{}, fmt.Errorf("address %q is not range.owner.name", address)
	}
	return sap.New(parts[0], parts[1], parts[2])
}

// waitJS waits until the cluster's JetStream answers for the account.
func (p *liveProv) waitJS(ctx context.Context) {
	p.t.Helper()
	var last error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		nc, err := connectTo(ctx, "s1", p.e.urls["s1"], p.admin, 5*time.Second)
		if err != nil {
			last = err
			continue
		}
		js, err := jetstream.New(nc)
		if err == nil {
			actx, cancel := context.WithTimeout(ctx, 3*time.Second) // requests are dropped until a meta leader is elected
			_, err = js.AccountInfo(actx)
			cancel()
		}
		nc.Close()
		if err == nil {
			return
		}
		last = err
	}
	p.t.Fatalf("JetStream is not ready: %v", last)
}

func (p *liveProv) CreateInbox(ctx context.Context, address string) error {
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
	if rec, err := p.ledger.InForce(); err != nil || rec.Generation != 1 {
		p.t.Errorf("generation in force at CreateInbox = %d (%v): provisioning is before the commit", rec.Generation, err)
	}
	if st, co, err := p.inbox(ctx, "s1", address); err != nil || st || co {
		p.t.Errorf("before CreateInbox: stream=%v consumer=%v err=%v", st, co, err)
	}
	id, err := p.identity(address)
	if err != nil {
		return err
	}
	nc, err := connectTo(ctx, "s1", p.e.urls["s1"], p.admin, 5*time.Second)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	// The JetStream meta group may still be electing a leader: retry briefly.
	var last error
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		var s jetstream.Stream
		if s, last = js.CreateOrUpdateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits())); last != nil {
			continue
		}
		if _, last = s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); last == nil {
			return nil
		}
	}
	return last
}

func (p *liveProv) IssueCredential(_ context.Context, address string) error {
	p.ev.add("issue:" + address)
	creds, _ := p.e.realUser(p.user)
	p.mu.Lock()
	p.creds, p.issued = creds, time.Now()
	p.mu.Unlock()
	return nil
}

// adminCreds returns credentials of a user signed by the account's own key: it holds the JetStream API, as mash's admin
// does.
func (e *at010Env) adminCreds() []byte {
	kp := at010Must(nkeys.CreateUser())
	uc := jwt.NewUserClaims(at010Must(kp.PublicKey()))
	tok := at010Must(uc.Encode(e.acct))
	return at010Must(jwt.FormatUserConfig(tok, at010Must(kp.Seed())))
}

func TestAT014Live(t *testing.T) {
	if testing.Short() {
		t.Skip("live two-server cluster")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e := newLiveEnvJS(t, at014Prev(), true)
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
	order := NewOrder(c1, c2, ex) // only to check the derivation; RunOrdered derives its own
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
	ledger := NewMemLedger(listset.Record{Generation: 1})
	prov := &liveProv{
		t: t, e: e, ctx: ctx, ev: ev, user: newUser, ledger: ledger, admin: e.adminCreds(),
		cands: []Candidate{
			{Generation: 2, Compiled: c2, RevokedKeys: ex.RevokedKeys, Signers: e.signers(c2)},
			{Generation: 1, Compiled: c1, Signers: e.signers(c1)},
		},
	}
	prov.waitJS(ctx)
	// No inbox stream or consumer exists for foo.c.new before the run.
	for n := range e.urls {
		if st, co, err := prov.inbox(ctx, n, "foo.c.new"); err != nil || st || co {
			t.Fatalf("before the run, server %s: stream=%v consumer=%v err=%v", n, st, co, err)
		}
	}
	in := RunInput{
		Cluster: liveEvCluster{e.cluster, ev}, Prober: prober, Witnesses: wit, Ledger: ledger,
		Signers: e.signers(c2), PreviousSigners: e.signers(c1), OldKeys: map[string][]string{"foo.c.core": {oldPub}},
		Account: e.acctPub, Inventory: 2, ListSet: at014Target(), Received: []byte("at014"),
		Previous: c1, RevokedKeys: ex.RevokedKeys,
		Signer: SignerFuncs{
			SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return e.account(c1, revoked...), nil },
			SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
				// Nothing for foo.c.new exists when the JWT is signed, which is before the push.
				if st, co, err := prov.inbox(ctx, "s1", "foo.c.new"); err != nil || st || co {
					t.Errorf("at signing: stream=%v consumer=%v err=%v", st, co, err)
				}
				e.waitNewSecond()
				tok := e.account(c, oldPub)
				e.setCurrent(tok)
				return tok, nil
			},
		},
	}
	os, err := RunOrdered(ctx, in, prov)
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
	if rec, _ := ledger.InForce(); rec.Generation != 2 {
		t.Errorf("generation in force = %d", rec.Generation)
	}
	// The inbox stream and the consumer of foo.c.new exist on the cluster.
	for n := range e.urls {
		if st, co, err := prov.inbox(ctx, n, "foo.c.new"); err != nil || !st || !co {
			t.Errorf("after the run, server %s: stream=%v consumer=%v err=%v", n, st, co, err)
		}
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
