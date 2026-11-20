package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// toolObject calls the tool and returns its text and structured object.
func toolObject(t *testing.T, s *session, id int, name string) (string, map[string]any) {
	t.Helper()
	r := s.call(t, id, name, `{}`)
	sc, _ := r["structuredContent"].(map[string]any)
	return resultText(r), sc
}

// deliveryRow is the doctor object's delivery check.
func deliveryRow(t *testing.T, sc map[string]any) map[string]any {
	t.Helper()
	rows, _ := sc["checks"].([]any)
	for _, r := range rows {
		if m := r.(map[string]any); m["name"] == "delivery" {
			return m
		}
	}
	t.Fatalf("no delivery row in %v", sc)
	return nil
}

// Inside a session, status and doctor say whether this session holds the endpoint. Another session holds it: not
// ready, not online, the delivery row fails. Once it is free and this session takes it: ready, online.
func TestSessionStatusAnotherSessionHolds(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := tlsWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, tAddr("a").Flat())
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)

	text, sc := toolObject(t, s, 10, "status")
	if want := tTo("a") + ": not ready: another session holds this endpoint\nhint: mash doctor"; text != want {
		t.Fatalf("status: %q, want %q", text, want)
	}
	if sc["ready"] != false || sc["online"] != false || sc["reason"] != "another session holds this endpoint" || sc["endpoint"] != tTo("a") {
		t.Fatalf("status object: %v", sc)
	}
	text, sc = toolObject(t, s, 11, "doctor")
	if !strings.Contains(text, "\ndelivery  FAIL  another session holds this endpoint\n") || !strings.Contains(text, "\n1 problem.") {
		t.Fatalf("doctor: %q", text)
	}
	if row := deliveryRow(t, sc); row["state"] != "fail" || row["detail"] != "another session holds this endpoint" || sc["problems"] != float64(1) {
		t.Fatalf("doctor object: %v", sc)
	}
	// at a shell nothing changes: it sees the endpoint held, and no session to deliver to
	if r := runMash(w.env["a"], "status"); r.out != tTo("a")+": ready, online\n" || r.code != 0 {
		t.Fatalf("shell status: %+v", r)
	}
	if r := runMash(w.env["a"], "doctor"); !strings.Contains(r.out, "delivery  skip  needs a session\n") || r.code != 0 {
		t.Fatalf("shell doctor: %+v", r)
	}

	freeLease(t, kv, tAddr("a").Flat())
	s.awaitOnline(t)
	text, sc = toolObject(t, s, 12, "status")
	if text != tTo("a")+": ready, online" || sc["ready"] != true || sc["online"] != true || sc["reason"] != nil {
		t.Fatalf("status after taking it: %q %v", text, sc)
	}
	hookRan(t, w, s, "a")
	text, sc = toolObject(t, s, 13, "doctor")
	if !strings.Contains(text, "\ndelivery  ok\n") || !strings.HasSuffix(text, "No problems.") || sc["problems"] != float64(0) {
		t.Fatalf("doctor after taking it: %q", text)
	}
}

// A newer serve of the same session takes the endpoint: the old serve says it lost it to a newer one; the new serve
// holds it (a handover is not a loss), so it says ready, online.
func TestSessionStatusLostToANewerOne(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	endpoint.LeaseRefresh = 100 * time.Millisecond
	asSession(t)
	w := tlsWorld(t, "a")
	old := startServe(t, w.env["a"])
	old.initialize(t)
	old.tools(t)
	eventually(t, 5*time.Second, "old online", func() bool { return runMash(w.env["a"], "status").out == tTo("a")+": ready, online\n" })
	if text, _ := toolObject(t, old, 9, "status"); text != tTo("a")+": ready, online" {
		t.Fatalf("old serve while it holds: %q", text)
	}
	fresh := startServe(t, w.env["a"])
	fresh.initialize(t)
	fresh.tools(t)

	want := tTo("a") + ": not ready: this session lost this endpoint to a newer one\nhint: mash doctor"
	id := 20
	eventually(t, 10*time.Second, "the old serve says it lost the endpoint", func() bool {
		id++
		text, _ := toolObject(t, old, id, "status")
		return text == want
	})
	_, sc := toolObject(t, old, 40, "status")
	if sc["ready"] != false || sc["online"] != false || sc["reason"] != "this session lost this endpoint to a newer one" {
		t.Fatalf("old status object: %v", sc)
	}
	text, sc := toolObject(t, old, 41, "doctor")
	if !strings.Contains(text, "\ndelivery  FAIL  this session lost this endpoint to a newer one\n") || deliveryRow(t, sc)["detail"] != "this session lost this endpoint to a newer one" {
		t.Fatalf("old doctor: %q", text)
	}
	eventually(t, 10*time.Second, "the new serve holds the endpoint", func() bool {
		id++
		text, _ := toolObject(t, fresh, id, "status")
		return text == tTo("a")+": ready, online"
	})
	text, _ = toolObject(t, fresh, 42, "doctor")
	if !strings.Contains(text, "\ndelivery  warn  hooks have not run in this session yet; they run with\n                the first prompt or tool call\n") {
		t.Fatalf("new doctor: %q", text)
	}
}

// A session that could not take the endpoint says why, on the line after the words.
func TestSessionStatusCouldNotTake(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	transientMax = 50 * time.Millisecond
	w := tlsWorld(t, "a")
	openEndpoint = func(_ context.Context, _ config.Config) (*endpoint.Endpoint, error) {
		return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrNoServers)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)

	text, sc := toolObject(t, s, 10, "status")
	lines := strings.Split(text, "\n")
	if len(lines) != 3 || lines[0] != tTo("a")+": not ready: this session could not take this endpoint:" ||
		!strings.HasPrefix(lines[1], "  the server did not answer: ") || lines[2] != "hint: mash doctor" {
		t.Fatalf("status: %q", text)
	}
	cause := strings.TrimPrefix(lines[1], "  ")
	if sc["ready"] != false || sc["online"] != false || sc["reason"] != "this session could not take this endpoint: "+cause {
		t.Fatalf("status object: %v", sc)
	}
	text, sc = toolObject(t, s, 11, "doctor")
	wantRow := "\ndelivery  FAIL  this session could not take this endpoint:\n                " + cause + "\n"
	if !strings.Contains(text, wantRow) || deliveryRow(t, sc)["detail"] != "this session could not take this endpoint: "+cause {
		t.Fatalf("doctor: %q\nwant %q", text, wantRow)
	}
	// at a shell nothing changes: nothing holds the endpoint
	if r := runMash(w.env["a"], "status"); r.out != tTo("a")+": ready, offline\n" || r.code != 0 {
		t.Fatalf("shell status: %+v", r)
	}
}
