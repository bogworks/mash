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

// maxServeStartAttempts bounds how often one directory size is measured. See #1854.
const maxServeStartAttempts = 3

// Serve's startup costs a bounded number of dependent round trips that does not grow with the directory.
//
// The RTT proxy counts a wave as the requests sent between server responses, so a request from a concurrent goroutine
// (a second KV stream info, say) that lands just after a response was delivered splits one wave in two (#1854).
// That adds a wave only occasionally, about one run in fifty under -race, while a real regression adds a wave on every
// run. So each size is measured up to maxServeStartAttempts times, stopping at the first attempt within the limit, and
// the minimum count seen is the one compared across sizes.
func TestServeStartupRoundTrips(t *testing.T) {
	waves := map[int]int{}
	for _, n := range []int{10, 40} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			best, last := 0, ""
			attempts := 0
			for attempts < maxServeStartAttempts {
				attempts++
				w, desc := measureServeStartWaves(t, n)
				t.Logf("attempt %d: serve startup over %d directory records: %d waves after connect\n%s", attempts, n, w-1, desc)
				if attempts == 1 || w < best {
					best = w
				}
				last = desc
				if w-1 <= maxServeStartWaves {
					break
				}
			}
			waves[n] = best
			if best-1 > maxServeStartWaves {
				t.Errorf("serve startup took %d waves after connect in each of %d attempts (fewest seen), want at most %d\n%s", best-1, attempts, maxServeStartWaves, last)
			}
		})
	}
	if waves[10] != waves[40] {
		t.Errorf("serve startup took %d waves at 10 directory records but %d at 40: the cost grows with the directory", waves[10], waves[40])
	}
}

// measureServeStartWaves starts a fresh world and serve over n directory records and returns the waves the proxy
// counted through the inbox consumer's first pull, with the proxy's description of them.
func measureServeStartWaves(t *testing.T, n int) (int, string) {
	t.Helper()
	r := newRTTWorld(t, n)
	env := map[string]string{}
	for k, v := range r.env["peer1"] { // an endpoint no session is serving
		env[k] = v
	}
	env["MASH_SERVER"] = r.px.URL()
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
	return w, r.px.Describe()
}
