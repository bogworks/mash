package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/testenv"
)

// makeStorage makes mash's storage on the server nc is connected to, and records the admin user of adminCreds, as
// `mashctl init` does: for tests that need the storage but are not about init.
func makeStorage(t *testing.T, nc *nats.Conn, owner, adminCreds string) {
	t.Helper()
	a, err := admin.New(nc, owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := config.ReadCredsFile(adminCreds)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.RecordAdminCreds(ctx, a, owner, raw); err != nil {
		t.Fatal(err)
	}
}

// makeStorageFor is makeStorage on the server of e, as its admin.
func makeStorageFor(t *testing.T, e *testenv.Env) {
	t.Helper()
	makeStorage(t, e.Conn(t, e.AdminCreds), e.Owner, e.AdminCreds)
}

// makeStorage is makeStorage on the server of e, as the admin whose creds are given.
func (e *applyEnv) makeStorage(creds string) {
	e.t.Helper()
	makeStorage(e.t, e.connect(natsauth.UserCredentials(creds)), "alice", creds)
}
