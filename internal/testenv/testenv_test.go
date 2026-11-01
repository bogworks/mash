package testenv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestStartAndMint(t *testing.T) {
	e := Start(t)
	admin := e.Conn(t, e.AdminCreds)
	js, err := jetstream.New(admin)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := js.AccountInfo(ctx); err != nil {
		t.Fatalf("admin JetStream: %v", err)
	}

	creds, user := e.Mint(t, "x")
	if !strings.HasPrefix(user, "U") {
		t.Fatalf("user %q", user)
	}
	errs := make(chan error, 4)
	nc, err := nats.Connect(e.URL, natsauth.UserCredentials(creds), nats.CustomInboxPrefix("_INBOX.x"),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errs <- err }))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if err := nc.Publish("agents.mail.cc."+e.Owner+".x", []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := nc.Publish("not.allowed", []byte("no")); err != nil {
		t.Fatal(err)
	}
	nc.Flush()
	select {
	case err := <-errs:
		if !strings.Contains(strings.ToLower(err.Error()), "permissions violation") ||
			!strings.Contains(err.Error(), "not.allowed") {
			t.Fatalf("unexpected async error %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publishing outside the template must be a permissions violation")
	}
}

func TestRestartKeepsStore(t *testing.T) {
	e := Start(t)
	js, _ := jetstream.New(e.Conn(t, e.AdminCreds))
	ctx := context.Background()
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "s", Subjects: []string{"s.>"}, Storage: jetstream.FileStorage}); err != nil {
		t.Fatal(err)
	}
	url := e.URL
	e.Restart(t)
	if e.URL != url {
		t.Fatalf("restart moved the server: %s -> %s", url, e.URL)
	}
	js2, _ := jetstream.New(e.Conn(t, e.AdminCreds))
	if _, err := js2.Stream(ctx, "s"); err != nil {
		t.Fatalf("stream lost across restart: %v", err)
	}
}
