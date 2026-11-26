package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

// auditFixture is a test account on an embedded server with mash's storage made: endpoints, their mail and the damage
// a test does to them are put in by the methods below, and the verbs are run against it with ctlRun(t, f.e, ...).
type auditFixture struct {
	e  *testenv.Env
	js jetstream.JetStream
	nc *nats.Conn
}

func newAuditEnv(t *testing.T) *auditFixture {
	t.Helper()
	e := testenv.Start(t)
	makeStorageFor(t, e)
	nc := e.Conn(t, e.AdminCreds)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return &auditFixture{e: e, js: js, nc: nc}
}

func (f *auditFixture) publish(t *testing.T, subject string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := f.js.Publish(context.Background(), subject, []byte(fmt.Sprintf("m%d", i))); err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
	}
}

// publishBig publishes n messages of 500 bytes.
func (f *auditFixture) publishBig(t *testing.T, subject string, n int) {
	t.Helper()
	body := []byte(strings.Repeat("x", 500))
	for i := 0; i < n; i++ {
		if _, err := f.js.Publish(context.Background(), subject, body); err != nil {
			t.Fatal(err)
		}
	}
}

// capInbox sets the byte cap of the endpoint's inbox stream, below what a provisioned one allows, so a few messages
// fill it.
func (f *auditFixture) capInbox(t *testing.T, a sap.Address, maxBytes int64) {
	t.Helper()
	s, err := f.js.Stream(context.Background(), a.InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := info.Config
	cfg.MaxBytes = maxBytes
	if _, err := f.js.UpdateStream(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

// provision creates the directory record of a for user u through the admin API.
func (f *auditFixture) provision(t *testing.T, a sap.Address, u string) {
	t.Helper()
	adm, err := admin.New(f.e.Conn(t, f.e.AdminCreds), f.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), a, u, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatalf("provision %s: %v", a, err)
	}
}

// endpoint provisions the endpoint at a with a freshly minted user and returns the user's public key.
func (f *auditFixture) endpoint(t *testing.T, a sap.Address) string {
	t.Helper()
	_, u := f.e.Mint(t, a)
	f.provision(t, a, u)
	return u
}
