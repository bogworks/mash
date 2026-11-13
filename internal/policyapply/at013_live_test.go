package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

// AT-013 (contract mash-messaging-policy, REQ-014): on a live two-server cluster a sender's tags narrow. The new
// credential goes to a public key the sender's host made; the old keys are revoked only after the servers show a
// connection with the new one; the session keeps sending to a receiver still allowed; both old keys are refused on every
// server. A credential that is never placed, and a report that it was placed with no connection after it, each end at
// the deadline with the old keys revoked and the sender reported not connected. A sender that leaves the set has its keys
// revoked.

// at013Prev: status is shared in foo.c (a template change in the target, so the push closes every connection under the
// key), mail is closed and foo.c.core may mail docs and sdk by allow tags.
func at013Prev() listset.ListSet {
	return listset.ListSet{
		Range:    "foo",
		Switches: switches("foo.c", listset.Closed, listset.Closed, listset.Shared, listset.Closed),
		Senders: []listset.SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.docs", "foo.c.sdk"}},
			{Sender: "foo.c.docs"},
			{Sender: "foo.c.sdk"},
		},
	}
}

// at013Target closes status and takes core's allow of docs away: core's tags narrow, and sdk is still allowed.
func at013Target() listset.ListSet {
	return listset.ListSet{
		Base:     1,
		Range:    "foo",
		Switches: switches("foo.c", listset.Closed, listset.Closed, listset.Closed, listset.Closed),
		Senders: []listset.SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.sdk"}},
			{Sender: "foo.c.docs"},
			{Sender: "foo.c.sdk"},
		},
	}
}

// at013GonePrev has foo.c.gone, which the target drops.
func at013GonePrev() listset.ListSet {
	ls := at013Prev()
	ls.Senders = []listset.SenderLists{{Sender: "foo.c.core", Allow: []string{"foo.c.sdk"}}, {Sender: "foo.c.gone"}, {Sender: "foo.c.sdk"}}
	return ls
}

func at013GoneTarget() listset.ListSet {
	ls := at013Target()
	ls.Senders = []listset.SenderLists{{Sender: "foo.c.core", Allow: []string{"foo.c.sdk"}}, {Sender: "foo.c.sdk"}}
	return ls
}

const at013Receiver = "foo.c.sdk"

// at013Host is the sender's host: it makes its own key pair, tells mash only the public key, and connects with the
// delivered JWT and its own seed.
type at013Host struct {
	t    *testing.T
	kp   nkeys.KeyPair
	pub  string
	urls string
	mode string // "ok", "error" (the credential is never placed) or "silent" (placed is reported, nothing happens)

	mu sync.Mutex
	nc *nats.Conn
}

func newAT013Host(t *testing.T, e *at010Env, mode string) *at013Host {
	t.Helper()
	kp := at010Must(nkeys.CreateUser())
	var urls []string
	for _, u := range e.urls {
		urls = append(urls, u)
	}
	h := &at013Host{t: t, kp: kp, pub: at010Must(kp.PublicKey()), urls: strings.Join(urls, ","), mode: mode}
	t.Cleanup(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.nc != nil {
			h.nc.Close()
		}
	})
	return h
}

func (h *at013Host) Deliver(_ context.Context, _, userJWT string) error {
	switch h.mode {
	case "error":
		return errors.New("the wrangler could not place the credential")
	case "silent":
		return nil
	}
	seed, _ := h.kp.Seed()
	creds, err := jwt.FormatUserConfig(userJWT, seed)
	if err != nil {
		return err
	}
	nc, err := nats.Connect(h.urls, nats.UserCredentialBytes(creds), nats.ReconnectWait(100*time.Millisecond), nats.MaxReconnects(-1))
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.nc = nc
	h.mu.Unlock()
	return nil
}

func (h *at013Host) connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nc != nil && h.nc.IsConnected()
}

// publish sends tag to the receiver every 50 ms through get() until stop is closed.
func publishLoop(subject, tag string, get func() *nats.Conn, stop <-chan struct{}) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if nc := get(); nc != nil {
				_ = nc.Publish(subject, []byte(tag))
			}
		}
	}
}

// at013Conns records when the servers first showed the new key.
type at013Conns struct {
	Connections
	mu   sync.Mutex
	seen time.Time
}

func (c *at013Conns) Connected(ctx context.Context, pub string) (bool, error) {
	ok, err := c.Connections.Connected(ctx, pub)
	if ok {
		c.mu.Lock()
		if c.seen.IsZero() {
			c.seen = time.Now()
		}
		c.mu.Unlock()
	}
	return ok, err
}

func (c *at013Conns) seenAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen
}

// at013Revoker records when the first revocation began.
type at013Revoker struct {
	Revoker
	mu    sync.Mutex
	first time.Time
}

func (r *at013Revoker) Revoke(ctx context.Context, pubs []string) error {
	r.mu.Lock()
	if r.first.IsZero() {
		r.first = time.Now()
	}
	r.mu.Unlock()
	return r.Revoker.Revoke(ctx, pubs)
}

func (r *at013Revoker) firstAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.first
}

// at013 is one live scenario.
type at013 struct {
	t       *testing.T
	ctx     context.Context
	e       *at010Env
	c1, c2  *listset.Compiled
	host    *at013Host
	oldPubs map[string][]string // address -> old keys
	oldCred map[string][][]byte
	conns   *at013Conns
	revoker *at013Revoker
	plan    *RotationPlan
	ledger  *MemLedger
	prober  *LiveProber
	status  Status
	// message arrivals at the monitor, by tag
	mu       sync.Mutex
	arrivals []time.Time
	byTag    map[string]int
}

// at013Run builds the cluster for prev, issues two old credentials for each of oldFor, runs target with the rotation of
// narrowed (if any) and the removal of leaving, and returns the scenario after Run.
func at013Run(t *testing.T, prev, target listset.ListSet, mode string, deadline time.Duration, narrowed bool, oldFor []string, leaving []string, publish bool) *at013 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	e := newLiveEnv(t, prev)
	s := &at013{t: t, ctx: ctx, e: e, c1: compile(t, prev), oldPubs: map[string][]string{}, oldCred: map[string][][]byte{}, byTag: map[string]int{}}
	_, c2, err := listset.Accept(listset.Record{Generation: 1}, target, []byte("at013"))
	if err != nil {
		t.Fatal(err)
	}
	s.c2 = c2
	s.ledger = NewMemLedger(listset.Record{Generation: 1})

	// The old credentials, as their hosts made them: only the public key is known to the record.
	for _, addr := range oldFor {
		var pc ProbeCred
		for _, c := range credsOf(s.c1) {
			if c.Sender == addr {
				pc = c
			}
		}
		for range 2 {
			creds, pub := e.realUser(pc)
			s.oldPubs[addr] = append(s.oldPubs[addr], pub)
			s.oldCred[addr] = append(s.oldCred[addr], creds)
			if err := e.rec.Add(ctx, IssueEntry{Address: addr, PublicKey: pub, Purpose: PurposeIssue, Tags: pc.Tags, Generation: 1, IssuedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	}

	s.prober = &LiveProber{URLs: e.urls, Mint: e.mint}
	wit := &LiveWitnesses{}
	t.Cleanup(wit.Close)
	revoke := sync.OnceValue(func() error {
		return RevokeMinted(context.Background(), e, e.rec, time.Now(), s.prober, wit)
	})
	t.Cleanup(func() { _ = revoke() })
	// The first connection loads the account on a server.
	if err := wit.Open(ctx, e.urls, e.mint, credsOf(s.c1)); err != nil {
		t.Fatalf("open witnesses: %v", err)
	}

	// The monitor is signed by the account key itself, so it sees every message to the receiver.
	subject := listset.Subject(listset.Mail, at013Receiver)
	var stop chan struct{}
	var senders sync.WaitGroup
	var oldNC *nats.Conn
	if publish {
		mkp := at010Must(nkeys.CreateUser())
		mpub := at010Must(mkp.PublicKey())
		mtok := at010Must(jwt.NewUserClaims(mpub).Encode(e.acct))
		mseed := at010Must(mkp.Seed())
		mcreds := at010Must(jwt.FormatUserConfig(mtok, mseed))
		mon, err := nats.Connect(e.urls["s1"], nats.UserCredentialBytes(mcreds))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mon.Close)
		if _, err := mon.Subscribe(subject, func(m *nats.Msg) {
			s.mu.Lock()
			s.arrivals = append(s.arrivals, time.Now())
			s.byTag[string(m.Data)]++
			s.mu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
		if err := mon.Flush(); err != nil {
			t.Fatal(err)
		}
		oldNC, err = nats.Connect(e.urls["s1"]+","+e.urls["s2"], nats.UserCredentialBytes(s.oldCred["foo.c.core"][0]),
			nats.ReconnectWait(100*time.Millisecond), nats.MaxReconnects(-1))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(oldNC.Close)
		stop = make(chan struct{})
	}

	s.host = newAT013Host(t, e, mode)
	s.conns = &at013Conns{Connections: NATSConnections{Conn: e.sys}}
	s.revoker = &at013Revoker{Revoker: e}
	s.plan = &RotationPlan{
		Leaving: leaving, Generation: 2,
		Issuer: GroupUserIssuer{Signer: e, Account: e.acctPub}, Deliverer: s.host, Conns: s.conns,
		Record: e.rec, Revoker: s.revoker, Poll: 50 * time.Millisecond,
	}
	if narrowed {
		for _, u := range c2.Users {
			if u.Sender == "foo.c.core" {
				s.plan.Narrowed = append(s.plan.Narrowed, NarrowedSender{Address: u.Sender, Name: u.Name, Tags: u.Tags, NewPub: s.host.pub})
			}
		}
	}
	after := RotationsAfterPush(s.plan)

	if publish {
		senders.Add(2)
		go func() {
			defer senders.Done()
			publishLoop(subject, "old", func() *nats.Conn { return oldNC }, stop)
		}()
		go func() {
			defer senders.Done()
			publishLoop(subject, "new", func() *nats.Conn {
				s.host.mu.Lock()
				defer s.host.mu.Unlock()
				return s.host.nc
			}, stop)
		}()
		// Wait for the old session to be sending.
		deadlineAt := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadlineAt) {
			s.mu.Lock()
			n := len(s.arrivals)
			s.mu.Unlock()
			if n >= 3 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	in := RunInput{
		Cluster: e.cluster, Prober: s.prober, Witnesses: wit, Ledger: s.ledger,
		Account: e.acctPub, Inventory: 2, ListSet: target, Received: []byte("at013"),
		Previous: s.c1, Signers: e.signers(s.c2), PreviousSigners: e.signers(s.c1),
		Signer: SignerFuncs{
			SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return e.account(s.c1, revoked...), nil },
			SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
				e.waitNewSecond()
				tok := e.account(c)
				e.setCurrent(tok)
				return tok, nil
			},
		},
		AfterPush: func(ctx context.Context) (map[string][]string, []string, error) {
			s.plan.Deadline = time.Now().Add(deadline)
			return after(ctx)
		},
	}
	st, err := Run(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	s.status = st
	if publish {
		time.Sleep(500 * time.Millisecond)
		close(stop)
		senders.Wait()
	}
	return s
}

// maxGap is the largest gap between two messages from the sender, old or new connection, from the first to the last.
func (s *at013) maxGap() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gap time.Duration
	for i := 1; i < len(s.arrivals); i++ {
		gap = max(gap, s.arrivals[i].Sub(s.arrivals[i-1]))
	}
	return gap
}

// requireRevoked requires every key revoked in the record at or after notBefore, and in the revocation list of every
// server.
func (s *at013) requireRevoked(keys []string, notBefore time.Time) {
	s.t.Helper()
	byKey := map[string]IssueEntry{}
	for _, en := range s.e.entries() {
		byKey[en.PublicKey] = en
	}
	for _, k := range keys {
		en, ok := byKey[k]
		if !ok || en.RevokedAt == nil {
			s.t.Errorf("key %s is not marked revoked in the issue record", k)
		} else if en.RevokedAt.Before(notBefore.Truncate(time.Second)) { // the record keeps whole seconds
			s.t.Errorf("key %s revoked at %v, before %v", k, en.RevokedAt, notBefore)
		}
	}
	held := s.e.heldJWTs(s.ctx)
	if len(held) != 2 {
		s.t.Fatalf("servers answering: %d", len(held))
	}
	for n, c := range held {
		for _, k := range keys {
			if _, has := c.Revocations[k]; !has {
				s.t.Errorf("server %s does not revoke %s", n, k)
			}
		}
	}
}

func (s *at013) requireOldRefused(addr string) {
	s.t.Helper()
	for _, creds := range s.oldCred[addr] {
		s.e.refusedConnect(s.ctx, creds)
	}
}

func TestAT013Live(t *testing.T) {
	if testing.Short() {
		t.Skip("live two-server cluster")
	}
	t.Run("narrowing hands over to the new key", func(t *testing.T) {
		t.Parallel()
		s := at013Run(t, at013Prev(), at013Target(), "ok", 15*time.Second, true, []string{"foo.c.core"}, nil, true)
		st := s.status
		if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 || len(st.NotConnected) != 0 {
			t.Fatalf("status: %+v", st)
		}
		r := s.plan.Results
		if len(r) != 1 || !r[0].Connected || r[0].DeliverError != "" || r[0].NewPub != s.host.pub {
			t.Fatalf("results: %+v", r)
		}
		old := s.oldPubs["foo.c.core"]
		if want := slices.Sorted(slices.Values(old)); !slices.Equal(r[0].Revoked, want) {
			t.Errorf("revoked %v, want %v", r[0].Revoked, want)
		}
		// The old keys were revoked only after the servers showed the new key.
		seen := s.conns.seenAt()
		if seen.IsZero() {
			t.Fatal("the servers never showed the new key")
		}
		if first := s.revoker.firstAt(); first.Before(seen) {
			t.Errorf("revocation began at %v, before the new key was seen at %v", first, seen)
		}
		s.requireRevoked(old, seen)
		s.requireOldRefused("foo.c.core")
		// The new key is in the record as a rotation, not revoked, and works on every server.
		for _, en := range s.e.entries() {
			if en.PublicKey == s.host.pub && (en.Purpose != PurposeRotation || en.RevokedAt != nil || en.Generation != 2) {
				t.Errorf("new key entry = %+v", en)
			}
		}
		if !s.host.connected() {
			t.Error("the host's new session is not connected")
		}
		s.mu.Lock()
		oldN, newN := s.byTag["old"], s.byTag["new"]
		s.mu.Unlock()
		gap := s.maxGap()
		t.Logf("AT-013: largest gap between messages from the sender: %v (%d from the old key, %d from the new)", gap, oldN, newN)
		if oldN == 0 || newN == 0 {
			t.Errorf("messages from old %d, new %d", oldN, newN)
		}
		if gap >= 3*time.Second {
			t.Errorf("gap %v, want under one reconnect (3 s)", gap)
		}
		// A later issue request for a revoked key is refused, in the record and by Rotate.
		if err := s.e.rec.Add(s.ctx, IssueEntry{Address: "foo.c.core", PublicKey: old[0], Purpose: PurposeIssue}); !errors.Is(err, ErrRevokedKey) {
			t.Errorf("Add of a revoked key: %v", err)
		}
		_, err := Rotate(s.ctx, RotateInput{
			Address: "foo.c.core", Name: "core", Tags: []string{"allow:c.sdk"}, NewPub: old[1], Generation: 3,
			Issuer: GroupUserIssuer{Signer: s.e, Account: s.e.acctPub}, Deliverer: s.host, Conns: s.conns,
			Record: s.e.rec, Revoker: s.revoker, Deadline: time.Now().Add(time.Second),
		})
		if !errors.Is(err, ErrRevokedKey) {
			t.Errorf("Rotate to a revoked key: %v", err)
		}
		if g, _ := s.ledger.InForce(); g.Generation != 2 {
			t.Errorf("generation in force %d", g.Generation)
		}
	})

	for _, mode := range []string{"error", "silent"} {
		name := map[string]string{"error": "failed delivery", "silent": "false report"}[mode]
		t.Run(name+" ends at the deadline", func(t *testing.T) {
			t.Parallel()
			s := at013Run(t, at013Prev(), at013Target(), mode, 2*time.Second, true, []string{"foo.c.core"}, nil, false)
			st := s.status
			if st.State != StatePartial || st.InForce || !slices.Equal(st.NotConnected, []string{"foo.c.core"}) {
				t.Fatalf("status: %+v", st)
			}
			if !slices.ContainsFunc(st.Failing, func(f Failure) bool { return f.Sender == "foo.c.core" && f.Check == CheckNotConnected }) {
				t.Errorf("failing: %+v", st.Failing)
			}
			if SenderPassed(st, "foo.c.core") {
				t.Error("a not-connected sender passed")
			}
			r := s.plan.Results
			if len(r) != 1 || r[0].Connected || (r[0].DeliverError != "") != (mode == "error") {
				t.Fatalf("results: %+v", r)
			}
			if !s.conns.seenAt().IsZero() {
				t.Error("the new key was seen")
			}
			// The old keys were revoked anyway, and nothing was committed.
			s.requireRevoked(s.oldPubs["foo.c.core"], s.revoker.firstAt())
			s.requireOldRefused("foo.c.core")
			if g, _ := s.ledger.InForce(); g.Generation != 1 {
				t.Errorf("generation in force %d: something was committed", g.Generation)
			}
		})
	}

	t.Run("a removed sender is revoked and refused", func(t *testing.T) {
		t.Parallel()
		s := at013Run(t, at013GonePrev(), at013GoneTarget(), "ok", 5*time.Second, false, []string{"foo.c.gone"}, []string{"foo.c.gone"}, false)
		st := s.status
		if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 || len(st.NotConnected) != 0 {
			t.Fatalf("status: %+v", st)
		}
		s.requireRevoked(s.oldPubs["foo.c.gone"], s.revoker.firstAt())
		s.requireOldRefused("foo.c.gone")
		if err := s.e.rec.Add(s.ctx, IssueEntry{Address: "foo.c.gone", PublicKey: s.oldPubs["foo.c.gone"][0], Purpose: PurposeIssue}); !errors.Is(err, ErrRevokedKey) {
			t.Errorf("Add of a revoked key: %v", err)
		}
	})
}
