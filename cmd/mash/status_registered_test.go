package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
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
	// who agrees: no registration answers, so it shows the endpoint offline
	if r := runMash(env, "who"); r.code != 0 || !whoShows(r.out, tTo("a"), "offline") {
		t.Fatalf("who before the handshake: %+v, want %s offline", r, tTo("a"))
	}
	s.initialize(t)
	s.tools(t)
	want = tTo("a") + ": ready, online\n"
	eventually(t, 15*time.Second, "status online after the handshake", func() bool {
		r := runMash(env, "status")
		return r.out == want && r.code == 0
	})
	if r := runMash(env, "who"); r.code != 0 || !whoShows(r.out, tTo("a"), "online") {
		t.Fatalf("who after the handshake: %+v, want %s online", r, tTo("a"))
	}
}

// #1794: doctor does not use the online answer, so it sends no presence probe ($SRV.INFO.agents); status does send one.
func TestDoctorSendsNoPresenceProbe(t *testing.T) {
	w := newWorld(t, "a")
	env := w.env["a"]
	s := startServe(t, env)
	s.initialize(t)
	s.tools(t)
	// the lease is held and the endpoint registered: the case where status probes
	eventually(t, 15*time.Second, "status online", func() bool {
		r := runMash(env, "status")
		return r.out == tTo("a")+": ready, online\n" && r.code == 0
	})
	spy := w.e.Conn(t, w.e.AdminCreds)
	var probes atomic.Int64
	sub, err := spy.Subscribe("$SRV.INFO."+sap.ServiceName, func(*nats.Msg) { probes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	if err := spy.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // let anything the serve itself sends settle
	probes.Store(0)
	if r := runMash(env, "doctor"); r.code != 0 {
		t.Fatalf("doctor: %+v", r)
	}
	time.Sleep(300 * time.Millisecond)
	if n := probes.Load(); n != 0 {
		t.Fatalf("doctor sent %d presence probes, want none", n)
	}
	// the same spy sees status's probe, so the silence above is not a blind subscription
	if r := runMash(env, "status"); r.code != 0 {
		t.Fatalf("status: %+v", r)
	}
	eventually(t, 5*time.Second, "status sends a presence probe", func() bool { return probes.Load() > 0 })
}
