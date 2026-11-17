package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

func presenceOf(name string, online bool) endpoint.Presence {
	return endpoint.Presence{Record: store.Record{Name: name, Owner: "o"}, Online: online}
}

func statusJSON(extra string) []byte {
	return []byte(`{"agent":"agents","owner":"o","instance_id":"I1","protocol_version":"0.3"` + extra + `}`)
}

// The state column: online peers show their report, offline "-", not reported "-", no answer in time "?"; the new
// column is last on its line and nothing before it moves. JSON carries activity and activity_trust only when reported.
func TestWhoStateColumnAndJSON(t *testing.T) {
	w := newWorld(t, "a")
	ep := w.openEP(t, "a")
	nc := w.e.Conn(t, w.e.AdminCreds)
	reply := func(name string, body []byte, delay time.Duration) {
		t.Helper()
		if _, err := nc.Subscribe(sap.For(w.e.Owner, name).Status, func(m *nats.Msg) {
			time.Sleep(delay)
			_ = m.Respond(body)
		}); err != nil {
			t.Fatal(err)
		}
	}
	reply("blk", statusJSON(`,"program_status":{"state":"blocked","kind":"question"},"program_detail":{"msg":"HIDDEN"}`), 0)
	reply("wrk", statusJSON(`,"program_status":{"state":"working"},"delivery":{"health":"holding","reason":"held_unacked","note":"HIDDEN2"}`), 0)
	reply("plain", statusJSON(``), 0)
	reply("slow", statusJSON(`,"program_status":{"state":"working"}`), 10*time.Second) // never back inside the budget
	reply("gone", statusJSON(`,"program_status":{"state":"working"}`), 0)              // answers, but is listed offline
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	ps := []endpoint.Presence{presenceOf("blk", true), presenceOf("gone", false), presenceOf("mute", true),
		presenceOf("plain", true), presenceOf("slow", true), presenceOf("wrk", true)}

	start := time.Now()
	act := whoActivityOf(context.Background(), ep, ps)
	if d := time.Since(start); d > whoActivityBudget+500*time.Millisecond {
		t.Fatalf("the column took %v, budget %v", d, whoActivityBudget)
	}
	if _, asked := act["gone"]; asked {
		t.Fatal("an offline endpoint was asked")
	}
	wantDel := map[string]string{"blk": "-", "gone": "-", "mute": "?", "plain": "-", "slow": "?", "wrk": "holding(held_unacked)"}
	want := map[string]string{"blk": "blocked(question)", "gone": "-", "mute": "?", "plain": "-", "slow": "?", "wrk": "working"}
	text := whoRender(ps, whoOutput{Activity: act}, false, nil)
	lines := strings.Split(text, "\n")
	if len(lines) != len(ps) {
		t.Fatalf("lines: %q", text)
	}
	for _, l := range lines {
		cols := strings.Split(l, "\t")
		// state is followed by the delivery column, last.
		name := cols[0]
		if len(cols) < 4 || cols[len(cols)-2] != "state "+want[name] || cols[len(cols)-1] != "delivery "+wantDel[name] {
			t.Errorf("%s: columns %q, want state %q then delivery %q", name, cols, want[name], wantDel[name])
		}
		if cols[1] != "online" && cols[1] != "offline" {
			t.Errorf("existing column moved: %q", l)
		}
	}
	if strings.Contains(text, "HIDDEN") || strings.Contains(text, "delivery ok") {
		t.Fatal("free text shown")
	}
	// Without the column asked for, the text is what it was.
	if plain := whoRender(ps, whoOutput{}, false, nil); strings.Contains(plain, "state") {
		t.Fatalf("no column was asked for: %q", plain)
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoRender(ps, whoOutput{JSON: true, Activity: act}, false, nil)), &arr); err != nil {
		t.Fatal(err)
	}
	for _, e := range arr {
		name := e["name"].(string)
		switch name {
		case "blk":
			a, _ := e["activity"].(map[string]any)
			if a["state"] != "blocked" || a["kind"] != "question" || e["activity_trust"] != "claimed" {
				t.Errorf("blk: %v", e)
			}
		case "wrk":
			a, _ := e["activity"].(map[string]any)
			if a["state"] != "working" || a["kind"] != nil || e["activity_trust"] != "claimed" ||
				e["delivery"] != "holding" || e["delivery_reason"] != "held_unacked" {
				t.Errorf("wrk: %v", e)
			}
		default:
			if _, has := e["delivery"]; has || e["delivery_reason"] != nil {
				t.Errorf("%s: delivery present though not reported: %v", name, e)
			}
			if _, has := e["activity"]; has || e["activity_trust"] != nil {
				t.Errorf("%s: activity present though not reported: %v", name, e)
			}
		}
	}
}

// The CLI default line is a launcher's contract (ADR 0034): it has no state column and makes no status request. --long
// and --json ask; so does the MCP who tool. A real peer reports no state yet, so the column shows "-" here (the
// reported values are TestWhoStateColumnAndJSON's). --names asks nothing.
func TestWhoStateCLIDefaultUnchangedAndOptIn(t *testing.T) {
	w := newWorld(t, "a", "p")
	nc := w.e.Conn(t, w.e.AdminCreds)
	var asked atomic.Int64
	if _, err := nc.Subscribe(sap.For(w.e.Owner, "p").Status, func(m *nats.Msg) {
		asked.Add(1) // counts only; the real p below answers
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	startServe(t, w.env["p"]).initialize(t)
	line := func(out string) string { return whoLine(t, out, "p") }

	eventually(t, 5*time.Second, "p online", func() bool { o, _ := w.cli(t, "a", "who"); return strings.Contains(o, "p\tonline") })
	out, _ := w.cli(t, "a", "who")
	if got := line(out); !strings.HasPrefix(got, "p\tonline") || strings.Contains(got, "state") {
		t.Fatalf("default who line: %q", got)
	}
	if strings.Contains(out, "state") || strings.Contains(out, "delivery") || asked.Load() != 0 {
		t.Fatalf("default who: %q, %d status requests", out, asked.Load())
	}
	out, _ = w.cli(t, "a", "who", "--names")
	if strings.Contains(out, "state") || asked.Load() != 0 {
		t.Fatalf("--names: %q, %d status requests", out, asked.Load())
	}

	before := asked.Load()
	out, _ = w.cli(t, "a", "who", "--long")
	if got := line(out); !strings.HasSuffix(got, "\tstate -\tdelivery ok") {
		t.Fatalf("--long line: %q", got)
	}
	if asked.Load() == before {
		t.Fatal("--long made no status request")
	}

	before = asked.Load()
	out, _ = w.cli(t, "a", "who", "--json")
	if asked.Load() == before {
		t.Fatal("--json made no status request")
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) == 0 {
		t.Fatalf("--json: %s", out)
	}

	// The MCP tool: text carries the column and it asks, in text and in JSON.
	s := startServe(t, w.env["a"])
	s.initialize(t)
	call := func(id int, args string) string {
		s.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"who","arguments":%s}}`, id, args))
		r := s.await(t, 10*time.Second, idIs(float64(id)))["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	before = asked.Load()
	if got := line(call(41, `{"name":"p"}`)); !strings.HasSuffix(got, "\tstate -\tdelivery ok") || asked.Load() == before {
		t.Fatalf("MCP who text line: %q (%d new requests)", got, asked.Load()-before)
	}
	if txt := call(42, `{"name":"p","format":"json"}`); !strings.Contains(txt, `"name": "p"`) {
		t.Fatalf("MCP who json: %s", txt)
	}
}
