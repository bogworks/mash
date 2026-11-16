package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type pollResult struct {
	at      time.Duration
	outcome string // "exposed", "refused", "down"
}

// pollNode tries the creds against one node every 100 ms from t0 until the node has refused them for four polls in a
// row (or the limit passes) and returns every attempt.
func pollNode(url, creds string, t0 time.Time, limit time.Duration) []pollResult {
	var res []pollResult
	refusedRun := 0
	for time.Since(t0) < limit {
		err := connectAs(url, creds)
		r := pollResult{at: time.Since(t0)}
		switch {
		case err == nil:
			r.outcome = "exposed"
		case strings.Contains(strings.ToLower(err.Error()), "authorization"):
			r.outcome = "refused"
		default:
			r.outcome = "down"
		}
		res = append(res, r)
		if r.outcome == "refused" {
			refusedRun++
			if refusedRun >= 4 {
				return res
			}
		} else {
			refusedRun = 0
		}
		time.Sleep(100 * time.Millisecond)
	}
	return res
}

// With the resolver interval at 5 s, a node that was down for a revocation and restarts keeps accepting the revoked
// credential until its first resolver sync, then refuses it. The test measures, per restart, when the client port first
// accepts a connection (t_accept), when the node first refuses the retired credential for good (t_first_refusal), and
// the window between them. Variant (a) polls from the moment the node is launched; variant (b) starts polling only once
// the node serves clients and has formed its routes. The bound asserted is loose on purpose.
func TestLiveRevocationWindowRestartedNode(t *testing.T) {
	if testing.Short() {
		t.Skip("live three-node cluster")
	}
	const interval = 5 * time.Second
	h := newClusterEnvInterval(t, 3, nil, interval)
	if out, errb, code := h.run("init"); code != 0 {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	const down = 2
	for rep, variant := range []string{"a", "a", "a", "b"} {
		name := "e" + string(rune('1'+rep))
		if out, errb, code := h.run("add", name); code != 0 {
			t.Fatalf("add %s: %d %s %s", name, code, out, errb)
		}
		creds := filepath.Join(h.dir, name+".creds")
		raw, err := os.ReadFile(creds)
		if err != nil {
			t.Fatal(err)
		}
		kept := filepath.Join(t.TempDir(), name+"-kept.creds")
		if err := os.WriteFile(kept, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ { // the credential works on every node
			if err := connectAs(h.c.NodeURL(i), kept); err != nil {
				t.Fatalf("rep %d: %s's credential on node %d before retire: %v", rep+1, name, i+1, err)
			}
		}
		h.c.Stop(down)
		time.Sleep(3 * time.Second) // leader election
		time.Sleep(1100 * time.Millisecond)
		out, errb, code := h.run("retire", name)
		if code != 0 || !strings.Contains(out, "pending_servers:") {
			t.Fatalf("retire %s with a node down: %d %s %s", name, code, out, errb)
		}
		for i := 0; i < 2; i++ { // control: the nodes that took the revocation refuse at once
			if err := connectAs(h.c.NodeURL(i), kept); err == nil || !strings.Contains(strings.ToLower(err.Error()), "authorization") {
				t.Fatalf("rep %d: node %d did not refuse the retired credential at once: %v", rep+1, i+1, err)
			}
		}

		t0 := time.Now()
		h.c.Launch(down)
		var res []pollResult
		var ready, routes time.Duration
		if variant == "a" {
			res = pollNode(h.c.NodeURL(down), kept, t0, interval+20*time.Second) // polls while the node starts
		} else {
			h.c.Await(down)
			ready = time.Since(t0)
			for h.c.NumRoutes(down) < 2 && time.Since(t0) < 30*time.Second {
				time.Sleep(50 * time.Millisecond)
			}
			routes = time.Since(t0)
			res = pollNode(h.c.NodeURL(down), kept, t0, interval+20*time.Second)
		}
		var tAccept, tRefusal time.Duration = -1, -1
		for i, r := range res {
			if tAccept < 0 && r.outcome != "down" {
				tAccept = r.at
			}
			if r.outcome == "refused" && tRefusal < 0 {
				stays := true
				for _, q := range res[i:] {
					if q.outcome != "refused" {
						stays = false
					}
				}
				if stays && len(res)-i >= 4 {
					tRefusal = r.at
				}
			}
		}
		exposed := 0
		for _, r := range res {
			if r.outcome == "exposed" {
				exposed++
			}
		}
		if tAccept < 0 || tRefusal < 0 {
			t.Fatalf("rep %d (%s): no stable refusal: accept=%v refusal=%v attempts=%d", rep+1, variant, tAccept, tRefusal, len(res))
		}
		window := tRefusal - tAccept
		t.Logf("rep %d variant %s: interval=%s ready=%s routes=%s t_accept=%s t_first_refusal=%s window=%s restart_to_refusal=%s exposed_polls=%d of %d",
			rep+1, variant, interval, ready.Round(time.Millisecond), routes.Round(time.Millisecond), tAccept.Round(time.Millisecond),
			tRefusal.Round(time.Millisecond), window.Round(time.Millisecond), tRefusal.Round(time.Millisecond), exposed, len(res))
		if window > interval+5*time.Second {
			t.Errorf("rep %d: the revoked credential was accepted for %s after the client port opened, over interval+5s", rep+1, window)
		}
		// the live credentials of the others still work on the restarted node, and the cluster settles before the next round
		h.auditClean(90 * time.Second)
	}
}
