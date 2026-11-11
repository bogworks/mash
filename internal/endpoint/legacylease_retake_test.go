package endpoint

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// With the legacy retry loop running, the old endpoint takes the lease again as soon as the other session's lease is
// gone. A message nak'd under the lost lease is then redelivered to it and shown exactly once, through its own spool: the
// message is never lost, whichever endpoint becomes the holder (delivery is at-least-once).
func TestLegacyLeaseEpochLostBeforeSpoolWriteRetakenByTheSameEndpointShowsItOnce(t *testing.T) {
	tinyRetry(t)
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	kv, fired := loseAt(t, f, ep, &testLegacyCheckSeam)
	f.sendTo(t, snd, "old", "raced")
	<-fired
	if ep.legacyReading() {
		t.Fatal("the lease loss was not noticed")
	}
	time.Sleep(3 * SpoolCheck)
	if hasLegacy(waiting(t, sp)) {
		t.Fatal("a legacy entry was written after the lease was lost")
	}
	if err := (*kv).Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	shown := func() int {
		n := 0
		for _, e := range waiting(t, sp) {
			if e.Stream == LegacyStream && bytes.Contains(e.Body, []byte("raced")) {
				n++
			}
		}
		return n
	}
	until(t, "the old endpoint took the lease again and spooled the message", func() bool { return shown() > 0 })
	time.Sleep(300 * time.Millisecond)
	if n := shown(); n != 1 {
		t.Fatalf("the old endpoint shows the message %d times, want once", n)
	}
}
