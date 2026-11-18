package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
)

// otherOwner is an endpoint of owner bob on the same server: a connection with his own reply prefix whose async
// errors (the server's permission violations) are collected.
type otherOwner struct {
	nc   *nats.Conn
	mu   sync.Mutex
	errs []string
}

func (f *fixture) otherOwner(t *testing.T) *otherOwner {
	t.Helper()
	a, err := sap.NewAddress("bob", testGroup, "x")
	if err != nil {
		t.Fatal(err)
	}
	creds, _ := f.e.Mint(t, a)
	o := &otherOwner{}
	nc, err := nats.Connect(f.e.URL, append(natsauth.TLSOptions(f.e.CAFile, f.e.ClientCert, f.e.ClientKey),
		nats.UserCredentials(creds), nats.CustomInboxPrefix(a.InboxPrefix()),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			o.mu.Lock()
			o.errs = append(o.errs, err.Error())
			o.mu.Unlock()
		}))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	o.nc = nc
	return o
}

// violations waits up to d for at least one publish permission violation and returns what it saw.
func (o *otherOwner) violations(d time.Duration) []string {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		o.mu.Lock()
		var got []string
		for _, e := range o.errs {
			if strings.Contains(strings.ToLower(e), "permissions violation for publish") {
				got = append(got, e)
			}
		}
		o.mu.Unlock()
		if len(got) > 0 || time.Now().After(end) {
			return got
		}
	}
}

// stoppedService runs name's service until stop is called, and returns the genuine registration it answered.
func (f *fixture) stoppedService(t *testing.T, name string, asker *Endpoint) (info []byte, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(PromptEvent) {}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done; ep.Close() }) }
	t.Cleanup(stop)
	m, err := asker.Conn().Request("$SRV.INFO.agents", nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m.Data, stop
}

// An endpoint of another owner sees a discovery request and its reply subject, but cannot answer it: the server
// refuses the publish, and the forged answer for an offline endpoint is never accepted.
func TestOtherOwnerCannotForgeDiscoveryAnswer(t *testing.T) {
	f := newFixture(t, "b", "ghost")
	b := f.client(t, "b")
	bob := f.otherOwner(t)
	spy, err := bob.nc.SubscribeSync("$SRV.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	forged, _ := json.Marshal(map[string]any{
		"name": "agents", "type": "io.nats.micro.v1.info_response",
		"metadata":  map[string]string{"owner": f.e.Owner},
		"endpoints": []map[string]string{{"name": "prompt", "subject": taddr("ghost").Subjects().Prompt}},
	})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			m, err := spy.NextMsg(50 * time.Millisecond)
			select {
			case <-stop:
				return
			default:
			}
			if err == nil && strings.HasPrefix(m.Subject, "$SRV.INFO") && m.Reply != "" {
				_ = bob.nc.Publish(m.Reply, forged)
				_ = bob.nc.Flush()
			}
		}
	}()

	if subj, err := Discover(context.Background(), b.Conn(), taddr("ghost")); err == nil {
		t.Fatalf("an offline endpoint was discovered through a forged answer: %q", subj)
	}
	if v := bob.violations(2 * time.Second); len(v) == 0 {
		t.Fatalf("the server did not refuse the forged answer; errors: %q", bob.errs)
	}
}

// A genuine signed registration replayed by an endpoint of another owner into a presence probe is refused, so who
// does not show the offline endpoint online; and the endpoints of the owner do not answer him at all.
func TestOtherOwnerCannotReplayPresence(t *testing.T) {
	f := newFixture(t, "a", "b", "ghost")
	f.serve(t, "a", noPrompt)
	b := f.client(t, "b")
	bob := f.otherOwner(t)

	genuine, stopGhost := f.stoppedService(t, "ghost", b)
	stopGhost()

	spy, err := bob.nc.SubscribeSync("$SRV.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			m, err := spy.NextMsg(50 * time.Millisecond)
			select {
			case <-stop:
				return
			default:
			}
			if err == nil && strings.HasPrefix(m.Subject, "$SRV.INFO") && strings.HasPrefix(m.Reply, "_INBOX."+f.e.Owner+".") {
				_ = bob.nc.Publish(m.Reply, genuine)
				_ = bob.nc.Flush()
			}
		}
	}()

	ps, err := b.WhoFresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawGhost, sawA bool
	for _, p := range ps {
		switch flatOf(p.Record) {
		case tkey("ghost"):
			sawGhost = true
			if p.Online {
				t.Fatal("the offline endpoint shows online after a replayed registration")
			}
		case tkey("a"):
			sawA = true
			if !p.Online {
				t.Fatal("the running endpoint of the same owner is not online")
			}
		}
	}
	if !sawGhost || !sawA {
		t.Fatalf("who is missing rows: %+v", ps)
	}
	if v := bob.violations(2 * time.Second); len(v) == 0 {
		t.Fatalf("the server did not refuse the replay; errors: %q", bob.errs)
	}

	// Bob's own discovery request gets no answer from owner o's endpoints: their publish to his inbox is refused.
	inbox := bob.nc.NewInbox()
	sub, err := bob.nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.nc.PublishRequest("$SRV.INFO.agents", inbox, nil); err != nil {
		t.Fatal(err)
	}
	if m, err := sub.NextMsg(1500 * time.Millisecond); err == nil {
		t.Fatalf("an endpoint of another owner answered bob: %q", m.Data)
	}
}

// The same owner is unchanged: discovery, a prompt round trip and presence all work.
func TestSameOwnerRepliesStillWork(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.serve(t, "a", func(ep *Endpoint, p PromptEvent) { _ = ep.Reply(p.RequestID, "pong", true) })
	b := f.client(t, "b")
	subj, err := Discover(context.Background(), b.Conn(), taddr("a"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Prompt(ctx, b.Conn(), b.Identity(), tkey("b"), subj, "ping", &out); err != nil || out.String() != "pong" {
		t.Fatalf("prompt: %q %v", out.String(), err)
	}
	ps, err := b.WhoFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if flatOf(p.Record) == tkey("a") && !p.Online {
			t.Fatal("a is not online")
		}
	}
}
