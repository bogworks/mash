package endpoint

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

// A legacy message delivered to a callback that is still running when the lease is lost is nak'd, not left unacked: when
// the same endpoint takes the lease again it gets the message promptly, not after the consumer's AckWait.
func TestLegacyCallbackAfterLeaseLossNaksSoRetakeShowsItOnce(t *testing.T) {
	tinyRetry(t)
	var once, release sync.Once
	block, entered := make(chan struct{}), make(chan struct{})
	unblock := func() { release.Do(func() { close(block) }) }
	testLegacyHandlerGate = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-block
		}
	}
	t.Cleanup(func() { unblock(); testLegacyHandlerGate = nil })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	f.sendTo(t, snd, "old", "gone-nak")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the legacy callback never ran")
	}
	kv := takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	unblock() // the callback now finds the lease gone
	time.Sleep(500 * time.Millisecond)
	if err := kv.Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	shown := func() int {
		n := 0
		for _, e := range waiting(t, sp) {
			if e.Stream == LegacyStream && bytes.Contains(e.Body, []byte("gone-nak")) {
				n++
			}
		}
		return n
	}
	deadline := time.Now().Add(10 * time.Second)
	for shown() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if shown() == 0 {
		t.Fatal("timed out: the message was not redelivered after the lease was taken again (left unacked until AckWait)")
	}
	time.Sleep(300 * time.Millisecond)
	if n := shown(); n != 1 {
		t.Fatalf("the message is shown %d times, want once", n)
	}
}
