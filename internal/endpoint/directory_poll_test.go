package endpoint

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// dirTraffic counts the requests on the directory stream, seen by a core subscription of the test's admin connection.
type dirTraffic struct{ state, filtered, gets atomic.Int64 }

func (d *dirTraffic) snap() [3]int64 {
	return [3]int64{d.state.Load(), d.filtered.Load(), d.gets.Load()}
}

func (f *fixture) watchDirTraffic(t *testing.T) *dirTraffic {
	t.Helper()
	nc := f.e.Conn(t, f.e.AdminCreds)
	d := &dirTraffic{}
	if _, err := nc.Subscribe("$JS.API.STREAM.INFO.KV_"+store.DirectoryBucket, func(m *nats.Msg) {
		if strings.Contains(string(m.Data), "subjects_filter") {
			d.filtered.Add(1)
		} else {
			d.state.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.Subscribe("$JS.API.DIRECT.GET.KV_"+store.DirectoryBucket+".>", func(*nats.Msg) { d.gets.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return d
}

func setPoll(t *testing.T, poll, full time.Duration) {
	t.Helper()
	op, of := DirectoryPoll, DirectoryFullReload
	DirectoryPoll = poll
	if full > 0 {
		DirectoryFullReload = full
	}
	t.Cleanup(func() { DirectoryPoll, DirectoryFullReload = op, of })
}

func waitFor(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An idle directory costs one small request per poll: no filtered stream info and no direct get after the first load.
func TestDirectoryPollIdleCostsOneStateRequest(t *testing.T) {
	setPoll(t, 100*time.Millisecond, 0)
	f := newFixture(t, "a", "b", "c")
	d := f.watchDirTraffic(t)
	f.run(t, "a")
	// the first poll (within one interval) reloads once: no state recorded yet
	time.Sleep(600 * time.Millisecond)
	before := d.snap()
	time.Sleep(1500 * time.Millisecond)
	after := d.snap()
	states := after[0] - before[0]
	if states < 8 || states > 20 { // ~15 polls of 75..125 ms
		t.Fatalf("state requests in 1.5 s at a 100 ms poll: %d", states)
	}
	if after[1] != before[1] || after[2] != before[2] {
		t.Fatalf("an unchanged directory was reloaded: filtered infos %d -> %d, direct gets %d -> %d",
			before[1], after[1], before[2], after[2])
	}
}

func TestDirectoryPollSeesChangeAddAndDelete(t *testing.T) {
	setPoll(t, 100*time.Millisecond, 0)
	f := newFixture(t, "a", "b")
	d := f.watchDirTraffic(t)
	a, _ := f.run(t, "a")
	time.Sleep(500 * time.Millisecond)

	role := "reviewer"
	if _, err := f.adm.SetMeta(context.Background(), "b", admin.MetaUpdate{Role: &role}); err != nil {
		t.Fatal(err)
	}
	roleOf := func(n string) string {
		for _, p := range a.Who() {
			if p.Record.Name == n {
				return p.Record.Role
			}
		}
		return "<absent>"
	}
	waitFor(t, "a changed record seen", 3*time.Second, func() bool { return roleOf("b") == "reviewer" })

	g0 := d.gets.Load()
	f.add(t, "c")
	waitFor(t, "an added record seen", 3*time.Second, func() bool { return hasName(a.Who(), "c") })
	if d.gets.Load() == g0 {
		t.Fatal("the add was seen without a reload")
	}
	if err := f.adm.Deprovision(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a deleted record gone", 3*time.Second, func() bool { return !hasName(a.Who(), "c") })
	if !hasName(a.Who(), "b") {
		t.Fatal("b must remain")
	}
}

// A directory stream deleted and recreated with the same records is reloaded: its Created (and last time) differ even
// when the message count and last sequence repeat.
func TestDirectoryPollSeesRecreatedStream(t *testing.T) {
	setPoll(t, 100*time.Millisecond, 0)
	f := newFixture(t, "a", "b")
	d := f.watchDirTraffic(t)
	a, _ := f.run(t, "a")
	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := kv.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string][]byte{}
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		vals[k] = e.Value()
	}
	g0 := d.gets.Load() // before the stream is touched: a poll may reload while the records are being put back
	if err := js.DeleteKeyValue(ctx, store.DirectoryBucket); err != nil {
		t.Fatal(err)
	}
	kv, err = js.CreateKeyValue(ctx, store.DirectoryConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if _, err := kv.Put(ctx, k, vals[k]); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "a reload after the stream was recreated", 3*time.Second, func() bool { return d.gets.Load() > g0 })
	if !hasName(a.Who(), "b") {
		t.Fatal("b must still be known")
	}
}

// With nothing changing, the forced reload still happens (jittered 0.75x to 1.25x of DirectoryFullReload).
func TestDirectoryPollForcedReload(t *testing.T) {
	setPoll(t, 100*time.Millisecond, 300*time.Millisecond)
	f := newFixture(t, "a", "b")
	d := f.watchDirTraffic(t)
	f.run(t, "a")
	time.Sleep(500 * time.Millisecond)
	g0 := d.gets.Load()
	waitFor(t, "a forced reload with an unchanged state", 4*time.Second, func() bool { return d.gets.Load() > g0 })
	g1 := d.gets.Load()
	waitFor(t, "and another", 4*time.Second, func() bool { return d.gets.Load() > g1 })
}

func setFetchHook(t *testing.T, h func(key string) error) {
	t.Helper()
	fetchRecordHook.Store(&h)
	t.Cleanup(func() { fetchRecordHook.Store(nil) })
}

// A transient failure to read one record is not a successful load: the old record stays, the next poll reloads in
// full (not the forced reload), and the directory converges once the read works.
func TestDirectoryPollTransientFailureKeepsRecordAndRetries(t *testing.T) {
	setPoll(t, 100*time.Millisecond, time.Hour)
	f := newFixture(t, "a", "b")
	d := f.watchDirTraffic(t)
	a, _ := f.run(t, "a")
	waitFor(t, "b known", 3*time.Second, func() bool { return hasName(a.Who(), "b") })
	time.Sleep(500 * time.Millisecond) // the first poll has recorded the state

	var failing atomic.Bool
	failing.Store(true)
	setFetchHook(t, func(key string) error {
		if key == "b" && failing.Load() {
			return errors.New("timeout")
		}
		return nil
	})
	role := "reviewer"
	if _, err := f.adm.SetMeta(context.Background(), "b", admin.MetaUpdate{Role: &role}); err != nil {
		t.Fatal(err)
	}
	roleOf := func() string {
		for _, p := range a.Who() {
			if p.Record.Name == "b" {
				return p.Record.Role
			}
		}
		return "<absent>"
	}
	g0 := d.gets.Load()
	waitFor(t, "a reload", 3*time.Second, func() bool { return d.gets.Load() > g0 })
	// The failure repeats at every poll: direct gets keep coming (a full reload each poll, no recorded state) while
	// the old record stays.
	g1 := d.gets.Load()
	time.Sleep(600 * time.Millisecond)
	if d.gets.Load() < g1+3 {
		t.Fatalf("direct gets did not resume at the next polls: %d -> %d", g1, d.gets.Load())
	}
	if r := roleOf(); r == "<absent>" || r == "reviewer" {
		t.Fatalf("b's record after failed reads: role %q (want the old record kept)", r)
	}
	failing.Store(false)
	waitFor(t, "convergence", 3*time.Second, func() bool { return roleOf() == "reviewer" })
	// converged: the state is recorded, so no more direct gets
	time.Sleep(300 * time.Millisecond)
	g2 := d.gets.Load()
	time.Sleep(600 * time.Millisecond)
	if d.gets.Load() != g2 {
		t.Fatalf("still reloading after convergence: %d -> %d", g2, d.gets.Load())
	}
}

// A key that is gone is dropped and is not a failure: no retries.
func TestDirectoryPollDeletedKeyIsNotRetried(t *testing.T) {
	setPoll(t, 100*time.Millisecond, time.Hour)
	f := newFixture(t, "a", "b", "c")
	d := f.watchDirTraffic(t)
	a, _ := f.run(t, "a")
	time.Sleep(500 * time.Millisecond)
	if err := f.adm.Deprovision(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "c dropped", 3*time.Second, func() bool { return !hasName(a.Who(), "c") })
	time.Sleep(300 * time.Millisecond)
	g := d.gets.Load()
	time.Sleep(600 * time.Millisecond)
	if d.gets.Load() != g {
		t.Fatalf("a deleted key caused retries: %d -> %d", g, d.gets.Load())
	}
}

// installDirectory: a failed key keeps its old record, a gone key is dropped.
func TestInstallDirectoryFailedKeepsGoneDrops(t *testing.T) {
	e := &Endpoint{dir: map[string]store.Record{"x": {Name: "x", User: "ux"}, "y": {Name: "y", User: "uy"}}}
	failed := e.installDirectory(map[string]recordResult{
		"x": {err: errors.New("timeout")},
		"y": {err: jetstream.ErrKeyNotFound, gone: true},
		"z": {err: errors.New("timeout")},
	})
	if failed != 2 {
		t.Fatalf("failed = %d", failed)
	}
	if _, ok := e.dir["x"]; !ok {
		t.Fatal("x dropped")
	}
	if _, ok := e.dir["y"]; ok {
		t.Fatal("y kept")
	}
	if e.byUser["ux"] != "x" {
		t.Fatal("byUser")
	}
}
