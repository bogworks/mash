package main

import (
	"fmt"
	"testing"
	"time"
)

// maxServeStartWaves is what `mash serve` may spend, after it has connected, before it is running and ready: the
// lease held, the service registered, the inbox consumer pulling. Research's budget for a slow link.
const maxServeStartWaves = 7

// consumerPull is the subject of the inbox consumer's first pull request: the last thing startup does.
const consumerPull = "$JS.API.CONSUMER.MSG.NEXT."

// Serve's startup costs a bounded number of dependent round trips that does not grow with the directory.
func TestServeStartupRoundTrips(t *testing.T) {
	waves := map[int]int{}
	for _, n := range []int{10, 40} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			r := newRTTWorld(t, n)
			env := map[string]string{}
			for k, v := range r.env["peer1"] { // an endpoint no session is serving
				env[k] = v
			}
			env["MASH_URL"] = r.px.URL()
			r.px.Reset()
			s := startServe(t, env)
			s.initialize(t)
			if got := len(s.tools(t)); got < 5 {
				t.Fatalf("tools/list: %d tools", got)
			}
			eventually(t, 10*time.Second, "the inbox consumer pulling", func() bool {
				_, ok := r.px.WavesThrough(consumerPull)
				return ok
			})
			if out := resultText(s.call(t, 3, "who", `{}`)); out == "" {
				t.Fatal("who answered nothing")
			}
			w, _ := r.px.WavesThrough(consumerPull)
			waves[n] = w
			t.Logf("serve startup over %d directory records: %d waves after connect\n%s", n, w-1, r.px.Describe())
			if w-1 > maxServeStartWaves {
				t.Errorf("serve startup took %d waves after connect, want at most %d\n%s", w-1, maxServeStartWaves, r.px.Describe())
			}
		})
	}
	if waves[10] != waves[40] {
		t.Errorf("serve startup took %d waves at 10 directory records but %d at 40: the cost grows with the directory", waves[10], waves[40])
	}
}
