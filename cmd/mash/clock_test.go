package main

import (
	"strings"
	"testing"
	"time"
)

func TestClockLine(t *testing.T) {
	warn := "clock: WARN - 45s from the server (limit 30 s: beyond it peers look offline and mail is [stale]). Next: sync this machine's clock (NTP)"
	for _, c := range []struct {
		skew time.Duration
		ok   bool
		want string
	}{
		{45 * time.Second, true, warn},
		{-45 * time.Second, true, warn},
		{11 * time.Second, true, "clock: WARN - 11s from the server"},
		{10 * time.Second, true, "clock: OK - within 10s of the server"},
		{300 * time.Millisecond, true, "clock: OK - within 0s of the server"},
		{0, false, "clock: n/a - not connected"},
	} {
		if got := clockLine(c.skew, c.ok); !strings.HasPrefix(got, c.want) {
			t.Errorf("clockLine(%v,%v) = %q, want prefix %q", c.skew, c.ok, got, c.want)
		}
	}
}
