package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// fakeConnz answers the CONNZ ping and the server's direct subject like one server with the connection ids in pages(call,
// offset): a script that decides, for the call-th follow-up request, which connections the page holds and what total it
// reports. The first reply (the ping's) is page(0, 0).
type fakeConnz struct {
	mu    sync.Mutex
	calls int // follow-up requests, over all scans
	first int
}

func startFakeConnz(t *testing.T, page func(call, offset int) (cids []uint64, total int)) (*nats.Conn, *fakeConnz) {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Port: -1, NoLog: true, NoSigs: true, NoSystemAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	f := &fakeConnz{}
	reply := func(offset int, cids []uint64, total int) []byte {
		conns := []map[string]any{}
		for _, c := range cids {
			conns = append(conns, map[string]any{"authorized_user": fmt.Sprintf("U%d", c), "account": "A", "cid": c})
		}
		b, _ := json.Marshal(map[string]any{"server": map[string]any{"name": "s1", "id": "SID"},
			"data": map[string]any{"total": total, "offset": offset, "connections": conns}})
		return b
	}
	if _, err := nc.Subscribe(connzPingSubject, func(m *nats.Msg) {
		cids, total := page(0, 0)
		_ = m.Respond(reply(0, cids, total))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := nc.Subscribe(fmt.Sprintf(connzDirectSubject, "SID"), func(m *nats.Msg) {
		var req struct {
			Offset int `json:"offset"`
		}
		_ = json.Unmarshal(m.Data, &req)
		f.mu.Lock()
		f.calls++
		call := f.calls
		f.mu.Unlock()
		cids, total := page(call, req.Offset)
		_ = m.Respond(reply(req.Offset, cids, total))
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return nc, f
}

// Four connections, pages of two, nothing changing: read whole, complete.
func TestConnzReconcilesAStablePagedScan(t *testing.T) {
	all := []uint64{1, 2, 3, 4}
	nc, _ := startFakeConnz(t, func(call, offset int) ([]uint64, int) { return all[offset : offset+2], 4 })
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 || got[0].Incomplete != "" || len(got[0].Conns) != 4 {
		t.Fatalf("%v %+v", err, got)
	}
}

// A connection that closes between the pages shifts the second page: it repeats one connection and skips another.
// Every scan sees the same shift, so after connzScanTries scans the server is marked incomplete, never clean.
func TestConnzShiftedPageIsIncompleteNotClean(t *testing.T) {
	nc, f := startFakeConnz(t, func(call, offset int) ([]uint64, int) {
		if offset == 0 {
			return []uint64{1, 2}, 4
		}
		return []uint64{2, 3}, 4 // 2 again; 4 never seen
	})
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].Incomplete == "" || !strings.Contains(got[0].Incomplete, "changed while it was read") {
		t.Fatalf("not marked incomplete: %+v", got[0])
	}
	seen := map[uint64]bool{}
	for _, c := range got[0].Conns {
		if seen[c.CID] {
			t.Errorf("connection %d listed twice", c.CID)
		}
		seen[c.CID] = true
	}
	if f.calls < connzScanTries { // the retries asked again
		t.Errorf("only %d follow-up requests", f.calls)
	}
}

// A total that moves between the first and the last page does not reconcile, even when the collected count matches the
// first total.
func TestConnzTotalMovingIsIncomplete(t *testing.T) {
	nc, _ := startFakeConnz(t, func(call, offset int) ([]uint64, int) {
		if offset == 0 {
			return []uint64{1, 2}, 4
		}
		return []uint64{3, 4}, 5 // a fifth connection opened meanwhile
	})
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 || got[0].Incomplete == "" {
		t.Fatalf("%v %+v", err, got)
	}
}

// A scan that disagrees once and then agrees is complete: the retry reads the server again.
func TestConnzRetryHeals(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	nc, _ := startFakeConnz(t, func(call, offset int) ([]uint64, int) {
		mu.Lock()
		defer mu.Unlock()
		if offset == 0 {
			return []uint64{1, 2}, 4
		}
		asked++
		if asked == 1 {
			return []uint64{2, 3}, 4 // shifted, once
		}
		return []uint64{3, 4}, 4
	})
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 || got[0].Incomplete != "" || len(got[0].Conns) != 4 {
		t.Fatalf("%v %+v", err, got)
	}
}

// The equal-total, no-duplicate shift: page 1 holds CIDs 1,2 of four; before page 2 CID 1 closes and CID 5 opens, so
// page 2 (offset 2) holds 4,5. The collected ids are unique, as many as the total, and CID 3 is missing. One scan looks
// consistent, so a multi-page scan is accepted only when a second scan collects the same set. Here the list keeps
// changing (scan k reads k,k+1 and then k+3,k+4), so no two scans agree and the server is incomplete, never clean.
func TestConnzEqualTotalShiftIsIncompleteNotClean(t *testing.T) {
	var mu sync.Mutex
	scan := 0
	nc, f := startFakeConnz(t, func(call, offset int) ([]uint64, int) {
		mu.Lock()
		defer mu.Unlock()
		if offset == 0 {
			scan++
			return []uint64{uint64(scan), uint64(scan + 1)}, 4
		}
		k := uint64(scan)
		return []uint64{k + 3, k + 4}, 4
	})
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	if got[0].Incomplete == "" || !strings.Contains(got[0].Incomplete, "changed while it was read") {
		t.Fatalf("a shifted multi-page scan was not marked incomplete: %+v", got[0])
	}
	if f.calls < connzScanTries {
		t.Errorf("only %d follow-up requests: the scans were not repeated", f.calls)
	}
}

// A single page is one snapshot: accepted on the first scan, with no second pass.
func TestConnzSinglePageIsAcceptedOnce(t *testing.T) {
	nc, f := startFakeConnz(t, func(call, offset int) ([]uint64, int) { return []uint64{1, 2, 3, 4}, 4 })
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 8)
	if err != nil || len(got) != 1 || got[0].Incomplete != "" || len(got[0].Conns) != 4 || f.calls != 0 {
		t.Fatalf("%v %+v calls=%d", err, got, f.calls)
	}
}

// A stable multi-page server is accepted after two identical passes (the ping's pass and one more: three follow-up
// requests are not needed, two pages each means three follow-ups in all: page 2, then page 1 and page 2 again).
func TestConnzStableMultiPageNeedsTwoIdenticalPasses(t *testing.T) {
	all := []uint64{1, 2, 3, 4}
	nc, f := startFakeConnz(t, func(call, offset int) ([]uint64, int) { return all[offset : offset+2], 4 })
	got, err := NATS{Conn: nc}.Connz(context.Background(), "A", nil, 2)
	if err != nil || len(got) != 1 || got[0].Incomplete != "" || len(got[0].Conns) != 4 {
		t.Fatalf("%v %+v", err, got)
	}
	if f.calls != 3 {
		t.Errorf("%d follow-up requests, want 3 (two passes)", f.calls)
	}
}
