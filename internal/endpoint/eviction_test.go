package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// fillInbox caps name's inbox at 4096 bytes and publishes n messages of about 550 bytes, as a peer flooding it would.
func (f *fixture) fillInbox(t *testing.T, name string, n int) {
	t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(ctx, sap.InboxStream(name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := info.Config
	cfg.MaxBytes = 4096
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("x", 500))
	for i := 0; i < n; i++ {
		if _, err := js.Publish(ctx, sap.For(f.e.Owner, name).Mail, body); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboxEvictionLineOnAFloodedInbox(t *testing.T) {
	f := newFixture(t, "a")
	f.fillInbox(t, "a", 20)
	ep := f.client(t, "a")
	line, ok := ep.InboxEvictionLine(context.Background())
	if !ok || !strings.HasPrefix(line, "inbox: WARN - ") || !strings.Contains(line, "unread message(s) lost to eviction") ||
		!strings.Contains(line, "mashctl audit") || !strings.Contains(line, "docs/operations.md") {
		t.Fatalf("flooded inbox: %q %v", line, ok)
	}
}

// Once the endpoint has caught up (its consumer jumped over the gap), the loss is still shown.
func TestInboxEvictionLineSurvivesTheConsumerCatchingUp(t *testing.T) {
	f := newFixture(t, "a")
	f.fillInbox(t, "a", 20)
	ep, _ := f.run(t, "a")
	deadline := time.Now().Add(10 * time.Second)
	for {
		js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
		c, err := js.Consumer(context.Background(), sap.InboxStream("a"), store.Consumer)
		if err == nil {
			if ci, err := c.Info(context.Background()); err == nil && ci.NumPending == 0 && ci.Delivered.Stream > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("endpoint never caught up")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if line, ok := ep.InboxEvictionLine(context.Background()); !ok || !strings.Contains(line, "unread message(s) lost to eviction") {
		t.Fatalf("after catching up: %q %v", line, ok)
	}
}

func TestInboxEvictionLineIsEmptyForACleanInbox(t *testing.T) {
	f := newFixture(t, "a", "b")
	b := f.client(t, "b")
	if _, err := b.Send(context.Background(), "a", "hello", 0); err != nil {
		t.Fatal(err)
	}
	ep := f.client(t, "a")
	if line, ok := ep.InboxEvictionLine(context.Background()); ok {
		t.Fatalf("clean inbox: %q", line)
	}
}

// An endpoint whose user may not read the stream or consumer info (or whose read fails) gets no line, and no error.
func TestInboxEvictionLineIsSilentWhenInfoIsNotPermitted(t *testing.T) {
	f := newFixture(t, "a")
	f.fillInbox(t, "a", 20)
	ep := f.client(t, "a")
	if _, ok := ep.InboxEvictionLine(context.Background()); !ok {
		t.Fatal("the line should show with the endpoint's own permissions")
	}
	creds, _ := f.e.MintRaw(t, "nopriv", jwt.Permissions{
		Pub: jwt.Permission{Allow: []string{"_INBOX.>"}}, Sub: jwt.Permission{Allow: []string{"_INBOX.>"}}})
	js, err := jetstream.New(f.e.ConnAs(t, creds, "nopriv"))
	if err != nil {
		t.Fatal(err)
	}
	ep.js = js
	start := time.Now()
	if line, ok := ep.InboxEvictionLine(context.Background()); ok || line != "" {
		t.Fatalf("info denied: %q %v", line, ok)
	}
	if d := time.Since(start); d > 2*evictionInfoTimeout {
		t.Fatalf("took %s", d)
	}
}

func TestEvictionLineNearFullOnly(t *testing.T) {
	// formatting alone: no loss, 95% full
	line, ok := EvictionLine(store9x())
	if !ok || !strings.Contains(line, "95% full") || strings.Contains(line, "evicted (") {
		t.Fatalf("%q", line)
	}
}

func store9x() store.InboxEviction { return store.InboxEviction{Fill: 95} }
