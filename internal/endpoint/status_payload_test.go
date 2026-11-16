package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

func statusKeys(t *testing.T, b []byte) (map[string]json.RawMessage, []string) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload %q: %v", b, err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return m, keys
}

func askStatus(t *testing.T, f *fixture, ep *Endpoint) []byte {
	t.Helper()
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	m, err := nc.Request(ep.subj.Status, nil, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m.Data
}

var sapKeys = []string{"agent", "endpoints", "instance_id", "interval_s", "owner", "protocol_version", "session", "ts"}

func withKeys(base []string, add ...string) []string {
	o := append(append([]string{}, base...), add...)
	sort.Strings(o)
	return o
}

type unmarshalable struct{}

func (unmarshalable) MarshalJSON() ([]byte, error) { return nil, bytes.ErrTooLarge }

func TestLeakageGuardAndSharedBuilder(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.serve(t, "a", noPrompt)
	ps := map[string]any{"state": "blocked", "kind": "permission"}
	a.SetHeartbeatExtras(func() map[string]any { return map[string]any{"program_status": ps} })
	a.SetStatusExtras(func() map[string]any {
		return map[string]any{"program_detail": map[string]string{"title": "a (Claude Code)", "msg": "waiting for permission"}}
	})

	hm, hk := statusKeys(t, a.heartbeat())
	if want := withKeys(sapKeys, "program_status"); !reflect.DeepEqual(hk, want) {
		t.Fatalf("heartbeat keys %v, want %v", hk, want)
	}
	rm, rk := statusKeys(t, askStatus(t, f, a))
	if want := withKeys(hk, "program_detail"); !reflect.DeepEqual(rk, want) {
		t.Fatalf("reply keys %v, want %v", rk, want)
	}
	if !bytes.Equal(hm["program_status"], rm["program_status"]) {
		t.Fatalf("program_status differs: %s vs %s", hm["program_status"], rm["program_status"])
	}
	if string(hm["program_status"]) != `{"kind":"permission","state":"blocked"}` {
		t.Fatalf("program_status %s", hm["program_status"])
	}

	// The published beat carries program_status and never program_detail.
	nc := f.e.ConnAs(t, f.creds["a"], "a")
	sub, err := nc.SubscribeSync(a.subj.HB)
	if err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	a.publishHeartbeat()
	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pm, pk := statusKeys(t, msg.Data)
	if !reflect.DeepEqual(pk, hk) || !bytes.Equal(pm["program_status"], hm["program_status"]) {
		t.Fatalf("published keys %v: %s", pk, msg.Data)
	}
	// A signed heartbeat still verifies with the extra field.
	if _, tr := sap.VerifyLive(msg.Header.Get(sap.HeaderName), msg.Subject, msg.Data, time.Now(), sap.NewNonceSet(time.Minute)); tr != sap.TrustVerified {
		t.Fatalf("heartbeat with program_status: trust %s", tr)
	}

	// No providers: the plain §8.3 payload both ways.
	a.SetHeartbeatExtras(nil)
	a.SetStatusExtras(nil)
	if _, k := statusKeys(t, a.heartbeat()); !reflect.DeepEqual(k, sapKeys) {
		t.Fatalf("plain heartbeat keys %v", k)
	}
	if _, k := statusKeys(t, a.statusPayload()); !reflect.DeepEqual(k, sapKeys) {
		t.Fatalf("plain reply keys %v", k)
	}
}

func TestExtrasContract(t *testing.T) {
	f := newFixture(t, "a")
	a, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	logs := &lockedBuf{}
	a.log = log.New(logs, "", 0)
	const secret = "SECRET-PANIC-TEXT"
	good := map[string]any{"program_status": map[string]string{"state": "idle"}}

	bad := map[string]func() map[string]any{
		"panic":       func() map[string]any { panic(secret) },
		"collision":   func() map[string]any { return map[string]any{"program_status": 1, "ts": "x"} },
		"unmarshable": func() map[string]any { return map[string]any{"program_status": 1, "x": unmarshalable{}} },
		"channel":     func() map[string]any { return map[string]any{"x": make(chan int)} },
	}
	for name, p := range bad {
		for _, kind := range []string{"heartbeat", "status"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				a.SetHeartbeatExtras(func() map[string]any { return good })
				a.SetStatusExtras(nil)
				if kind == "heartbeat" {
					a.SetHeartbeatExtras(p)
				} else {
					a.SetStatusExtras(p)
				}
				var out []byte
				if kind == "heartbeat" {
					out = a.heartbeat()
				} else {
					out = a.statusPayload()
				}
				_, k := statusKeys(t, out) // the beat is still built
				want := sapKeys
				if kind == "status" {
					want = withKeys(sapKeys, "program_status") // only the status extras are dropped
				}
				if !reflect.DeepEqual(k, want) {
					t.Fatalf("keys %v, want %v", k, want)
				}
			})
		}
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), bytes.ErrTooLarge.Error()) {
		t.Fatalf("a failure log carries the provider's text: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "extras") {
		t.Fatalf("failures were not logged: %q", logs.String())
	}

	// A status extra may not reuse a key the heartbeat carries.
	a.SetHeartbeatExtras(func() map[string]any { return good })
	a.SetStatusExtras(func() map[string]any { return map[string]any{"program_status": "x", "program_detail": "y"} })
	if _, k := statusKeys(t, a.statusPayload()); !reflect.DeepEqual(k, withKeys(sapKeys, "program_status")) {
		t.Fatalf("reply with a heartbeat-key collision: %v", k)
	}
}

func TestOffCycleBeat(t *testing.T) {
	const offCycleWindow = 700 * time.Millisecond
	f := newFixture(t, "a")
	ctx, cancel := context.WithCancel(context.Background())
	a, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	a.beatWindow, a.beatPoll = offCycleWindow, 20*time.Millisecond
	if err := a.StartService(ctx, func(PromptEvent) {}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.StopService(); cancel(); a.Close() })
	nc := f.e.ConnAs(t, f.creds["a"], "a")
	beats := make(chan *struct {
		at time.Time
		ps string
	}, 64)
	nonces := sap.NewNonceSet(time.Minute)
	var bad atomic.Value
	sub, err := nc.Subscribe(a.subj.HB, func(m *nats.Msg) {
		if _, tr := sap.VerifyLive(m.Header.Get(sap.HeaderName), m.Subject, m.Data, time.Now(), nonces); tr != sap.TrustVerified {
			bad.Store(string(tr))
		}
		var p struct {
			PS struct{ State string } `json:"program_status"`
		}
		_ = json.Unmarshal(m.Data, &p)
		beats <- &struct {
			at time.Time
			ps string
		}{time.Now(), p.PS.State}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	var state atomic.Value
	state.Store("idle")
	a.SetHeartbeatExtras(func() map[string]any {
		return map[string]any{"program_status": map[string]string{"state": state.Load().(string)}}
	})

	next := func(within time.Duration) *struct {
		at time.Time
		ps string
	} {
		select {
		case b := <-beats:
			return b
		case <-time.After(within):
			return nil
		}
	}
	first := next(2 * time.Second)
	if first == nil || first.ps != "idle" {
		t.Fatalf("no off-cycle beat for the first value: %+v", first)
	}
	// A burst inside the window: one trailing beat at the window's end, with the latest value.
	for _, s := range []string{"working", "blocked", "done", "error"} {
		state.Store(s)
		time.Sleep(30 * time.Millisecond)
	}
	got := next(3 * time.Second)
	if got == nil || got.ps != "error" {
		t.Fatalf("burst: want one beat with the latest value, got %+v", got)
	}
	if d := got.at.Sub(first.at); d < offCycleWindow-100*time.Millisecond {
		t.Fatalf("burst beat after %v, before the %v window ended", d, offCycleWindow)
	}
	if extra := next(2 * offCycleWindow); extra != nil {
		t.Fatalf("burst sent more than one beat: %+v", extra)
	}
	// A change after a quiet window goes at once.
	state.Store("idle")
	start := time.Now()
	if b := next(offCycleWindow); b == nil || b.ps != "idle" || time.Since(start) > 400*time.Millisecond {
		t.Fatalf("a change after a quiet window did not go at once: %+v after %v", b, time.Since(start))
	}
	// No change, no beat.
	if extra := next(2 * offCycleWindow); extra != nil {
		t.Fatalf("a beat without a change: %+v", extra)
	}
	if v := bad.Load(); v != nil {
		t.Fatalf("an off-cycle heartbeat did not verify: %v", v)
	}
}
