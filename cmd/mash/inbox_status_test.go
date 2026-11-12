package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
)

// floodInbox caps a's inbox at 4096 bytes and fills it past that before a session starts, so mail is evicted unread.
func floodInbox(t *testing.T, w *world, name string) {
	t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
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
	for i := 0; i < 20; i++ {
		if _, err := js.Publish(ctx, sap.For(w.e.Owner, name).Mail, []byte(strings.Repeat("x", 500))); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStatusToolShowsInboxEviction(t *testing.T) {
	w := newWorld(t, "a")
	floodInbox(t, w, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	var out string
	id := 10
	eventually(t, 10*time.Second, "inbox line shows", func() bool {
		id++
		out = statusOf(t, s, id)
		return strings.Contains(out, "inbox: WARN - ")
	})
	wantAll(t, "inbox line", statusLine(t, out, "inbox: "), "unread message(s) were evicted", "read mail sooner", "mashctl audit", "docs/operations.md")
}

func TestStatusToolHasNoInboxLineForACleanInbox(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", "a", "hello"); code != 0 {
		t.Fatalf("send: %s", out)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	eventually(t, 10*time.Second, "connected", func() bool {
		return strings.Contains(statusOf(t, s, 20), "connection: OK")
	})
	if out := statusOf(t, s, 21); strings.Contains(out, "inbox: ") {
		t.Fatalf("clean inbox shows a line:\n%s", out)
	}
}

// From a shell nothing is connected, so there is no inbox line (and no failure).
func TestStatusCLIHasNoInboxLine(t *testing.T) {
	w := newWorld(t, "a")
	floodInbox(t, w, "a")
	out, code := w.cli(t, "a", "status")
	if code != 0 || strings.Contains(out, "inbox: WARN") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}
