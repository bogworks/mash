package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// A watcher whose Notify never returns does not hang shutdown: stopWatch gives up after the wait, says so once, and a
// second call returns at once.
func TestStopWatchBoundedWhenNotifyBlocks(t *testing.T) {
	tb := endpoint.OpenAskTable(filepath.Join(t.TempDir(), "asks.json"))
	if _, err := tb.Add("peer", "UP", 7, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the ask has expired
	entered := make(chan struct{}, 1)
	never := make(chan struct{}) // never closed: Notify is stuck for good
	w := endpoint.AskWatch{Table: tb, CatchUpWait: time.Millisecond,
		Activity: func(context.Context, string) endpoint.PeerState { return endpoint.PeerState{} },
		Notify: func(string) error {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-never
			return nil
		}}
	var log bytes.Buffer // written by stopWatch on this goroutine only
	const wait = 300 * time.Millisecond
	stop := startAskWatch(context.Background(), w, 10*time.Millisecond, wait, &log)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher never reached Notify")
	}
	start := time.Now()
	stop()
	if d := time.Since(start); d < wait || d > wait+2*time.Second {
		t.Fatalf("stopWatch took %v, want about %v", d, wait)
	}
	if got := strings.Count(log.String(), "answer-now watcher did not stop"); got != 1 {
		t.Fatalf("log lines %d, want 1: %q", got, log.String())
	}
	start = time.Now()
	stop()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("second stopWatch took %v", d)
	}
	if got := strings.Count(log.String(), "answer-now watcher did not stop"); got != 1 {
		t.Fatalf("log lines after second call %d, want 1", got)
	}
}
