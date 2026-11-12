package main

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// freshRollupGuard gives the test its own once-per-process guard and resets it after.
func freshRollupGuard(t *testing.T) {
	t.Helper()
	leaseRollupWarned = sync.Once{}
	t.Cleanup(func() { leaseRollupWarned = sync.Once{} })
}

// setLeaseRollup sets whether the lease stream allows rollup, over the admin connection.
func (w *world) setLeaseRollup(t *testing.T, allow bool) {
	t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := store.LeaseStreamConfig(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AllowRollup = allow
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func (w *world) openEP(t *testing.T, name string) *endpoint.Endpoint {
	t.Helper()
	env := w.env[name]
	cfg, err := config.Load(config.LookupFromFunc(envFrom(env)), env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	ep, err := endpoint.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	return ep
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestLeaseRollupWarningIsWrittenOncePerProcess(t *testing.T) {
	freshRollupGuard(t)
	w := newWorld(t, "a")
	w.setLeaseRollup(t, true)
	ep := w.openEP(t, "a")
	var out bytes.Buffer
	var mu sync.Mutex
	locked := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return out.Write(p)
	})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); warnLeaseRollup(context.Background(), ep, locked) }()
	}
	wg.Wait()
	warnLeaseRollup(context.Background(), ep, locked) // a reopen in the same process
	if got := out.String(); got != endpoint.LeaseRollupStartWarning+"\n" {
		t.Fatalf("want exactly one warning line, got %q", got)
	}
}

// A clean check must not suppress a later warning.
func TestLeaseRollupCleanCheckDoesNotConsumeTheGuard(t *testing.T) {
	freshRollupGuard(t)
	w := newWorld(t, "a")
	ep := w.openEP(t, "a")
	var out bytes.Buffer
	warnLeaseRollup(context.Background(), ep, &out)
	if out.Len() != 0 {
		t.Fatalf("hardened bucket wrote %q", out.String())
	}
	w.setLeaseRollup(t, true)
	warnLeaseRollup(context.Background(), ep, &out)
	warnLeaseRollup(context.Background(), ep, &out)
	if got := out.String(); got != endpoint.LeaseRollupStartWarning+"\n" {
		t.Fatalf("want one warning after the bucket loosened, got %q", got)
	}
}

// A check that cannot be answered says nothing and leaves the guard for a later warning. (The denied-permission read
// is covered in the endpoint package, which can swap the connection; here the connection is closed.)
func TestLeaseRollupUnanswerableCheckWritesNothing(t *testing.T) {
	freshRollupGuard(t)
	w := newWorld(t, "a")
	w.setLeaseRollup(t, true)
	ep := w.openEP(t, "a")
	ep.Close()
	var out bytes.Buffer
	warnLeaseRollup(context.Background(), ep, &out)
	if out.Len() != 0 {
		t.Fatalf("unanswerable check wrote %q", out.String())
	}
	ep2 := w.openEP(t, "a")
	warnLeaseRollup(context.Background(), ep2, &out)
	if got := out.String(); got != endpoint.LeaseRollupStartWarning+"\n" {
		t.Fatalf("guard was consumed: %q", got)
	}
}
