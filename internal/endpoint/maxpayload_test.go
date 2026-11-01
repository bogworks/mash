package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func newFixtureWith(t *testing.T, o testenv.Options, names ...string) *fixture {
	t.Helper()
	e := testenv.StartWith(t, o)
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{e: e, adm: adm, creds: map[string]string{}, users: map[string]string{}}
	for _, n := range names {
		f.add(t, n)
	}
	return f
}

func inboxMsgs(t *testing.T, f *fixture, name string) uint64 {
	t.Helper()
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	s, err := js.Stream(context.Background(), "inbox-"+name)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

func TestSendFollowsTheServersMaxPayload(t *testing.T) {
	const cap = 64 << 10
	f := newFixtureWith(t, testenv.Options{MaxPayload: cap}, "a", "b")
	b := f.client(t, "b")
	ctx := context.Background()

	if _, err := b.Send(ctx, "a", strings.Repeat("x", cap-4096), 0); err != nil {
		t.Fatalf("a message under the server's cap: %v", err)
	}
	if got := inboxMsgs(t, f, "a"); got != 1 {
		t.Fatalf("inbox holds %d, want 1", got)
	}

	_, err := b.Send(ctx, "a", strings.Repeat("x", cap), 0)
	if err == nil {
		t.Fatal("a message over the server's cap was sent")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "mash: message is ") || !strings.Contains(msg, "bytes with headers; this server accepts at most 65536 (its max_payload). "+
		"Send a smaller message or put the content in a file and send the path.") || strings.Contains(msg, "nats:") {
		t.Fatalf("error %q", msg)
	}
	if got := inboxMsgs(t, f, "a"); got != 1 {
		t.Fatalf("the refused message was published: inbox holds %d", got)
	}
}

func TestSendKeepsTheOneMiBCapOnTheDefaultServer(t *testing.T) {
	f := newFixture(t, "a", "b")
	b := f.client(t, "b")
	if got := b.nc.MaxPayload(); got != store.MaxMsgSize {
		t.Fatalf("default server max_payload %d", got)
	}
	_, err := b.Send(context.Background(), "a", strings.Repeat("x", store.MaxMsgSize), 0)
	if err == nil || !strings.Contains(err.Error(), "the limit is 1048576") || strings.Contains(err.Error(), "max_payload") {
		t.Fatalf("error %v", err)
	}
	if _, err := b.Send(context.Background(), "a", strings.Repeat("x", store.MaxMsgSize-4096), 0); err != nil {
		t.Fatalf("a message under 1 MiB: %v", err)
	}
}
