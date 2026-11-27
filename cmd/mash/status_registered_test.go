package main

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// #1794: serve takes the endpoint's lease at once but registers the SAP service only after the MCP handshake. Until
// then status says "ready, offline" (who agrees: no registration answers); once the handshake is done it says
// "ready, online".
func TestStatusOnlineOnlyOnceRegistered(t *testing.T) {
	w := newWorld(t, "a")
	env := w.env["a"]
	s := startServe(t, env)
	// No initialize yet: wait until the lease is held.
	cfg, err := config.Load(config.LookupFromFunc(envFrom(env)), env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "serve holds the lease", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ep, err := endpoint.OpenClientWith(ctx, cfg, endpoint.ClientOptions{Lease: true})
		if err != nil {
			return false
		}
		defer ep.Close()
		_, held := ep.OwnLease(ctx)
		return held
	})
	want := tTo("a") + ": ready, offline\n"
	if r := runMash(env, "status"); r.out != want || r.code != 0 {
		t.Fatalf("status before the handshake: %+v, want %q", r, want)
	}
	if r := runMash(env, "who"); r.code != 0 {
		t.Fatalf("who: %+v", r)
	}
	s.initialize(t)
	s.tools(t)
	want = tTo("a") + ": ready, online\n"
	eventually(t, 15*time.Second, "status online after the handshake", func() bool {
		r := runMash(env, "status")
		return r.out == want && r.code == 0
	})
}
