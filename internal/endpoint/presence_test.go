package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

type presenceClock struct {
	mu sync.Mutex
	t  time.Time
}

func newPresenceClock() *presenceClock { return &presenceClock{t: time.Unix(1_800_000_000, 0)} }
func (c *presenceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *presenceClock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func countingProbe(n *atomic.Int64, err error) func(context.Context) error {
	return func(context.Context) error { n.Add(1); return err }
}

func TestPresenceCacheTTL(t *testing.T) {
	clk := newPresenceClock()
	c := &PresenceCache{Now: clk.Now}
	var n atomic.Int64
	probe := countingProbe(&n, nil)
	ctx := context.Background()
	if probed, err := c.Ensure(ctx, probe); !probed || err != nil {
		t.Fatalf("first: %v %v", probed, err)
	}
	clk.Add(PresenceCacheTTL - time.Second)
	if probed, err := c.Ensure(ctx, probe); probed || err != nil || n.Load() != 1 {
		t.Fatalf("within the TTL: probed=%v err=%v probes=%d", probed, err, n.Load())
	}
	clk.Add(time.Second)
	if probed, _ := c.Ensure(ctx, probe); !probed || n.Load() != 2 {
		t.Fatalf("at the TTL: probed=%v probes=%d", probed, n.Load())
	}
}

// The TTL counts from the start of the probe, not from when it finished.
func TestPresenceCacheCountsFromProbeStart(t *testing.T) {
	clk := newPresenceClock()
	c := &PresenceCache{Now: clk.Now}
	if _, err := c.Ensure(context.Background(), func(context.Context) error { clk.Add(2 * time.Second); return nil }); err != nil {
		t.Fatal(err)
	}
	clk.Add(PresenceCacheTTL - 2*time.Second)
	var n atomic.Int64
	if probed, _ := c.Ensure(context.Background(), countingProbe(&n, nil)); !probed {
		t.Fatal("the TTL did not count from the probe's start")
	}
}

func TestPresenceCacheFailureRetriesButNotWithinTheGap(t *testing.T) {
	clk := newPresenceClock()
	c := &PresenceCache{Now: clk.Now}
	var n atomic.Int64
	boom := errors.New("boom")
	ctx := context.Background()
	if probed, err := c.Ensure(ctx, countingProbe(&n, boom)); !probed || !errors.Is(err, boom) {
		t.Fatalf("failure: %v %v", probed, err)
	}
	clk.Add(PresenceRetryGap - time.Second)
	if probed, err := c.Ensure(ctx, countingProbe(&n, nil)); probed || !errors.Is(err, boom) || n.Load() != 1 {
		t.Fatalf("within the retry gap: probed=%v err=%v probes=%d", probed, err, n.Load())
	}
	clk.Add(time.Second)
	if probed, err := c.Ensure(ctx, countingProbe(&n, nil)); !probed || err != nil || n.Load() != 2 {
		t.Fatalf("after the retry gap: probed=%v err=%v probes=%d", probed, err, n.Load())
	}
	// A failed probe after a success leaves the cache stale (the success is not renewed) and the gap applies.
	clk.Add(PresenceCacheTTL)
	if probed, err := c.Ensure(ctx, countingProbe(&n, boom)); !probed || err == nil {
		t.Fatal("expected a failed probe")
	}
	clk.Add(time.Second)
	if probed, _ := c.Ensure(ctx, countingProbe(&n, nil)); probed {
		t.Fatal("probe storm after a failure")
	}
}

func TestPresenceCacheSingleFlight(t *testing.T) {
	clk := newPresenceClock()
	c := &PresenceCache{Now: clk.Now}
	var n atomic.Int64
	release := make(chan struct{})
	boom := errors.New("shared failure")
	probe := func(context.Context) error { n.Add(1); <-release; return boom }
	const callers = 16
	var wg sync.WaitGroup
	var probedCount, sharedErr atomic.Int64
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := c.Ensure(context.Background(), probe)
			if p {
				probedCount.Add(1)
			}
			if errors.Is(err, boom) {
				sharedErr.Add(1)
			}
		}()
	}
	for n.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // let the others reach the wait
	close(release)
	wg.Wait()
	if n.Load() != 1 || probedCount.Load() != 1 || sharedErr.Load() != callers {
		t.Fatalf("probes=%d probed=%d sharedErr=%d", n.Load(), probedCount.Load(), sharedErr.Load())
	}
}

// A caller that gives up does not fail the callers waiting on its probe: one of them probes again.
func TestPresenceCacheLeaderGivesUp(t *testing.T) {
	c := &PresenceCache{}
	lctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	leader := make(chan error, 1)
	go func() {
		_, err := c.Ensure(lctx, func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() })
		leader <- err
	}()
	<-started
	var n atomic.Int64
	waiter := make(chan error, 1)
	go func() { _, err := c.Ensure(context.Background(), countingProbe(&n, nil)); waiter <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader: %v", err)
	}
	if err := <-waiter; err != nil || n.Load() != 1 {
		t.Fatalf("waiter: err=%v probes=%d", err, n.Load())
	}
}

func TestPresenceCacheWaiterContext(t *testing.T) {
	c := &PresenceCache{}
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = c.Ensure(context.Background(), func(context.Context) error { close(started); <-release; return nil })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Ensure(ctx, func(context.Context) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter: %v", err)
	}
	close(release)
}

// infoCounter counts the $SRV.INFO.agents requests on the server.
func (f *fixture) infoCounter(t *testing.T) *atomic.Int64 {
	t.Helper()
	nc := f.e.Conn(t, f.e.AdminCreds)
	var n atomic.Int64
	if _, err := nc.Subscribe("$SRV.INFO."+sap.ServiceName, func(*nats.Msg) { n.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return &n
}

func onlineSet(ps []Presence) map[string]bool {
	m := map[string]bool{}
	for _, p := range ps {
		m[p.Record.Name] = p.Online
	}
	return m
}

// Bounds for TestServeHoldsNoHeartbeatSubscription. The deadline is only reached when something is wrong; the quiet
// period and the window are what the test observes for.
const (
	hbReadyDeadline = 30 * time.Second
	hbQuiet         = 300 * time.Millisecond // no message at the endpoint for this long: its start-up has settled
	hbWindowBeats   = 10                     // heartbeat intervals observed
)

// An idle serving endpoint holds no heartbeat subscription: heartbeats published around it reach no one on its
// connection (ADR 0066). A witness on another connection proves the heartbeats were flowing. The endpoint's own
// background reads (the lease watch) also arrive on its connection, so they are turned off for the test, and the
// window opens only after the witness has heard both endpoints and the endpoint's inbound count has stopped moving.
func TestServeHoldsNoHeartbeatSubscription(t *testing.T) {
	oh, op := HeartbeatInterval, DirectoryPoll
	HeartbeatInterval, DirectoryPoll = 50*time.Millisecond, time.Hour
	t.Cleanup(func() { HeartbeatInterval, DirectoryPoll = oh, op })
	setVar(t, &leaseWatchEvery, time.Hour)
	f := newFixture(t, "a", "b", "w")
	a := f.serve(t, "a", noPrompt)
	f.serve(t, "b", noPrompt)

	witness := f.e.ConnAs(t, f.creds["w"], "w")
	var beats atomic.Int64
	var heard sync.Map // the endpoints whose heartbeat the witness has received
	if _, err := witness.Subscribe("agents.hb.*."+f.e.Owner+".*", func(m *nats.Msg) {
		beats.Add(1)
		heard.Store(m.Subject[strings.LastIndex(m.Subject, ".")+1:], true)
	}); err != nil {
		t.Fatal(err)
	}
	if err := witness.Flush(); err != nil {
		t.Fatal(err)
	}
	// Ready: both endpoints are heartbeating, and nothing has reached a for a quiet period.
	waitFor(t, "the witness to hear both endpoints", hbReadyDeadline, func() bool {
		_, ha := heard.Load("a")
		_, hb := heard.Load("b")
		return ha && hb
	})
	last, since := a.nc.Stats().InMsgs, time.Now()
	waitFor(t, "the endpoint's start-up traffic to settle", hbReadyDeadline, func() bool {
		if n := a.nc.Stats().InMsgs; n != last {
			last, since = n, time.Now()
		}
		time.Sleep(10 * time.Millisecond)
		return time.Since(since) >= hbQuiet
	})
	before := a.nc.Stats().InMsgs
	b0 := beats.Load()
	time.Sleep(hbWindowBeats * HeartbeatInterval)
	if got := beats.Load() - b0; got < hbWindowBeats/2 {
		t.Fatalf("the witness saw %d heartbeats over %d intervals: the test is not exercising heartbeats", got, hbWindowBeats)
	}
	if d := a.nc.Stats().InMsgs - before; d != 0 {
		t.Fatalf("an idle endpoint received %d messages while others sent heartbeats", d)
	}
}

func TestWhoFreshCachesAndExpires(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	f.serve(t, "b", noPrompt)
	n := f.infoCounter(t)
	clk := newPresenceClock()
	a.presence.Now = clk.Now
	ctx := context.Background()
	ps, err := a.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if on := onlineSet(ps); !on["a"] || !on["b"] {
		t.Fatalf("presence %v", on)
	}
	if _, err := a.WhoFresh(ctx); err != nil {
		t.Fatal(err)
	}
	clk.Add(PresenceCacheTTL - time.Second)
	if _, err := a.WhoFresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("%d probes within the TTL, want 1", got)
	}
	clk.Add(time.Second)
	if _, err := a.WhoFresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("%d probes after the TTL, want 2", got)
	}
}

func TestWhoFreshConcurrentCallsShareOneProbe(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	f.serve(t, "b", noPrompt)
	n := f.infoCounter(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps, err := a.WhoFresh(context.Background())
			if err != nil || !onlineSet(ps)["b"] {
				t.Errorf("who: %v %v", onlineSet(ps), err)
			}
		}()
	}
	wg.Wait()
	if got := n.Load(); got != 1 {
		t.Fatalf("%d probes for concurrent calls, want 1", got)
	}
}

// An endpoint that stopped answering is offline at the next probe, not after the 90 s window; its last answer stays.
func TestWhoFreshOfflineAtTheNextProbe(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	a := f.serve(t, "a", noPrompt)
	f.serve(t, "b", noPrompt)
	c := f.serve(t, "c", noPrompt)
	clk := newPresenceClock()
	a.presence.Now = clk.Now
	ctx := context.Background()
	ps, err := a.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if on := onlineSet(ps); !on["b"] || !on["c"] {
		t.Fatalf("presence %v", on)
	}
	var cSeen time.Time
	for _, p := range ps {
		if p.Record.Name == "c" {
			cSeen = p.LastSeen
		}
	}
	c.svc().stop()
	clk.Add(PresenceCacheTTL)
	ps, err = a.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	on := onlineSet(ps)
	if !on["b"] || on["c"] {
		t.Fatalf("presence %v: b answered and c did not", on)
	}
	for _, p := range ps {
		if p.Record.Name == "c" && !p.LastSeen.Equal(cSeen) {
			t.Fatalf("c's last answer moved: %v then %v", cSeen, p.LastSeen)
		}
	}
}

func TestWhoFreshProbeFailureIsReported(t *testing.T) {
	f := newFixture(t, "a")
	a := f.serve(t, "a", noPrompt)
	a.nc.Close()
	if _, err := a.WhoFresh(context.Background()); err == nil {
		t.Fatal("no error from a probe on a closed connection")
	}
}

// A registration that does not verify, or whose key is not the directory's for that name, is not counted.
func TestWhoFreshIgnoresForgedRegistrations(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	a := f.serve(t, "a", noPrompt) // b is in the directory but not running
	forger := f.e.ConnAs(t, f.creds["c"], "c")
	cid := identity(t, f.creds["c"])
	bPrompt := sap.For(f.e.Owner, "b").Prompt
	reg := func(user, sig string) []byte {
		md := map[string]string{"owner": f.e.Owner, "agent": "claude-code", "user_nkey": user, "account": f.e.Account, "id_sig": sig}
		b, _ := json.Marshal(map[string]any{"metadata": md, "endpoints": []map[string]string{{"name": "prompt", "subject": bPrompt}}})
		return b
	}
	goodForC, err := cid.IDSig("claude-code", f.e.Owner, bPrompt)
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	if _, err := forger.Subscribe("$SRV.INFO."+sap.ServiceName, func(m *nats.Msg) {
		switch mode.Load() {
		case 0: // b's key, a signature that does not verify
			_ = m.Respond(reg(f.users["b"], "bm90LWEtc2lnbmF0dXJl"))
		case 1: // a valid signature by c's key over b's subject, claiming to be b: not the key the directory has for b
			_ = m.Respond(reg(f.users["c"], goodForC))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := forger.Flush(); err != nil {
		t.Fatal(err)
	}
	clk := newPresenceClock()
	a.presence.Now = clk.Now
	for i := range 2 {
		mode.Store(int32(i))
		ps, err := a.WhoFresh(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if on := onlineSet(ps); on["b"] {
			t.Fatalf("forgery %d counted: %v", i, on)
		}
		clk.Add(PresenceCacheTTL)
	}
}

// A probe nothing verified answers (here: nobody is running; at start-up: the service is not registered yet) is not
// kept for the cache time: it is retried after the retry gap and is not an error.
func TestWhoFreshUnansweredProbeRetriesAfterTheGap(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.leanClient(t, "a", ClientOptions{FullDirectory: true})
	n := f.infoCounter(t)
	clk := newPresenceClock()
	a.presence.Now = clk.Now
	call := func() {
		t.Helper()
		ps, err := a.WhoFresh(context.Background())
		if err != nil || onlineSet(ps)["b"] {
			t.Fatalf("who: %v %v", onlineSet(ps), err)
		}
	}
	call()
	clk.Add(PresenceRetryGap - time.Second)
	call()
	if got := n.Load(); got != 1 {
		t.Fatalf("%d probes within the retry gap, want 1", got)
	}
	clk.Add(time.Second)
	call()
	if got := n.Load(); got != 2 {
		t.Fatalf("%d probes after the retry gap, want 2", got)
	}
}
