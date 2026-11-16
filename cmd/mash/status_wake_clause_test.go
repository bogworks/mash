package main

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/spool"
)

// hooksRanSource is a statusSource whose session owns the spool and has had a hook run since, with a server that has
// sent the given number of channel notifications.
func hooksRanSource(t *testing.T, notifications int) *statusSource {
	t.Helper()
	cfg := config.Config{Name: "a", Owner: "o", StateDir: t.TempDir()}
	sdir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: sdir}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	own := time.Now().Add(-time.Minute)
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	srv := mcp.New(strings.NewReader(""), io.Discard, "mash", "test", "", nil)
	for i := 0; i < notifications; i++ {
		if err := srv.Notify("x", nil); err != nil {
			t.Fatal(err)
		}
	}
	src := &statusSource{cfg: cfg, srv: &atomic.Pointer[mcp.Server]{}, getenv: func(string) string { return "" }}
	src.srv.Store(srv)
	src.owned.Store(&own)
	return src
}

func hooksRanLine(t *testing.T, src *statusSource) string {
	t.Helper()
	lines := deliveryLines(src)
	for _, l := range lines {
		if strings.HasPrefix(l, "delivery: hooks have run in this session") {
			return l
		}
	}
	t.Fatalf("no hooks-have-run line in %q", lines)
	return ""
}

func TestDeliveryNoWakeClauseWithoutNotification(t *testing.T) {
	l := hooksRanLine(t, hooksRanSource(t, 0))
	if strings.Contains(l, "wake") || !strings.HasSuffix(l, "); mail also arrives with tool results") {
		t.Fatalf("got %q", l)
	}
}

func TestDeliveryWakeClauseAfterNotification(t *testing.T) {
	l := hooksRanLine(t, hooksRanSource(t, 1))
	if !strings.Contains(l, "); the last wake started a turn; mail also arrives with tool results") {
		t.Fatalf("got %q", l)
	}
}
