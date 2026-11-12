package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/ledger"
)

// The delivery line of the presented ledger (ADR 0109), from a shell: the counts read from the local file, with the loss
// part said not to be computed (the CLI connects to nothing).
func TestStatusCLIShowsThePresentedLedger(t *testing.T) {
	w := newWorld(t, "a")
	p := spoolFor(w, "a").Dir + "/" + ledger.File
	if out, code := w.cli(t, "a", "status"); code != 0 || !strings.Contains(out, "\ndelivery: nothing presented yet (the local ledger is empty). "+ledger.Note+"\n") {
		t.Fatalf("no ledger: %d\n%s", code, out)
	}
	l := ledger.New(p)
	for _, f := range []func() error{
		func() error { return l.Note("g", ledger.Presented, 1, 2, 3) },
		func() error { return l.Note("g", ledger.Refused, 4) },
		func() error { return l.Note("g", ledger.Channel, 5, 6) },
		func() error { return l.NoteDuplicate("g", 7, 1) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	st, err := ledger.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "delivery: 3 presented, 1 refused, 1 duplicate, 2 channel-acked (not proven) since " + st.First.UTC().Format(time.RFC3339) +
		"; loss: not computed here (the status tool of a running session compares the ledger with the inbox). " + ledger.Note
	out, code := w.cli(t, "a", "status")
	if code != 0 {
		t.Fatalf("status: %d", code)
	}
	var got string
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "delivery: 3 presented") {
			got = ln
		}
	}
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if !strings.Contains(got, "not that the model read it") {
		t.Fatalf("the line does not say that presented is not read: %q", got)
	}
}

// In a running session the status tool adds the loss it can prove against the inbox: here an inbox that evicted mail
// before any ledger existed, so nothing is claimed lost but at least that many may be.
func TestStatusToolSaysWhatMayBeLostBeforeTheLedger(t *testing.T) {
	w := newWorld(t, "a")
	floodInbox(t, w, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	var line string
	id := 10
	eventually(t, 10*time.Second, "ledger line shows the evicted mail", func() bool {
		id++
		for _, ln := range strings.Split(statusOf(t, s, id), "\n") {
			if strings.HasPrefix(ln, "delivery: ") && strings.Contains(ln, "more may be lost before the ledger started") {
				line = ln
			}
		}
		return line != ""
	})
	// The mail the session then received by the channel is recorded as channel-acked, never as presented.
	if !strings.Contains(line, "loss: none known; at least ") || !strings.HasSuffix(line, ". "+ledger.Note) || strings.Contains(line, " 1 presented") {
		t.Fatalf("line %q", line)
	}
}
