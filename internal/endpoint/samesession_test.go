package endpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// setSession makes this process look like a serve of the session id.
func setSession(t *testing.T, id SessionIdent) {
	t.Helper()
	old := OurSession
	OurSession = func() SessionIdent { return id }
	t.Cleanup(func() { OurSession = old })
}

// ours is an identity that can take over: a real parent pid with a start time, and a session id.
func ours(t *testing.T) SessionIdent {
	t.Helper()
	pp := os.Getppid()
	if pp <= 1 {
		t.Skip("no usable parent pid")
	}
	return SessionIdent{PPID: fmt.Sprint(pp), PStart: "12345", Session: "sess-1"}
}

func heldBy(t *testing.T, f *fixture, name string, lv leaseValue) jetstream.KeyValue {
	t.Helper()
	host, _ := os.Hostname()
	lv.Host, lv.Since, lv.PID = host, "x", "4242"
	if lv.Instance == "" {
		lv.Instance = "earlier"
	}
	return putLease(t, f, name, lv)
}

func TestSameSessionTakeoverBySessionID(t *testing.T) {
	id := ours(t)
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a")
	kv := heldBy(t, f, "a", leaseValue{Machine: needMachine(t), Session: id.Session})
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatalf("a later serve of the same session must take the lease: %v", err)
	}
	defer ep.Close()
	lv := readLease(t, kv, "a")
	if lv.Instance == "earlier" || lv.Session != id.Session || lv.PPID != id.PPID || lv.PStart != id.PStart {
		t.Fatalf("lease %+v", lv)
	}
}

func TestSameSessionTakeoverByParent(t *testing.T) {
	id := ours(t)
	id.Session = "" // no session id on our side: only the parent identifies the session
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a")
	kv := heldBy(t, f, "a", leaseValue{Machine: needMachine(t), PPID: id.PPID, PStart: id.PStart})
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatalf("same parent and start time: %v", err)
	}
	defer ep.Close()
	if lv := readLease(t, kv, "a"); lv.Instance == "earlier" {
		t.Fatalf("lease not taken: %+v", lv)
	}
}

// The takeover is made only for the same session: every other holder stays held and its lease is untouched.
func TestSameSessionNeverAcrossSessionsOrMachines(t *testing.T) {
	m := needMachine(t)
	base := ours(t)
	cases := []struct {
		name   string
		mine   SessionIdent
		holder leaseValue
		launch [2]string // ours, holder's
	}{
		{"different session id", base, leaseValue{Machine: m, Session: "sess-2"}, [2]string{}},
		// C1: an explicit session-id mismatch refuses even when the parent identity is shared.
		{"different non-empty session ids, same ppid and pstart", base, leaseValue{Machine: m, Session: "sess-2", PPID: base.PPID, PStart: base.PStart}, [2]string{}},
		{"different ppid", SessionIdent{PPID: base.PPID, PStart: base.PStart}, leaseValue{Machine: m, PPID: "999999", PStart: base.PStart}, [2]string{}},
		{"same ppid, different pstart", SessionIdent{PPID: base.PPID, PStart: base.PStart}, leaseValue{Machine: m, PPID: base.PPID, PStart: "54321"}, [2]string{}},
		{"same ppid, no pstart on the holder", SessionIdent{PPID: base.PPID, PStart: base.PStart}, leaseValue{Machine: m, PPID: base.PPID}, [2]string{}},
		{"same ppid, no pstart on ours", SessionIdent{PPID: base.PPID}, leaseValue{Machine: m, PPID: base.PPID, PStart: base.PStart}, [2]string{}},
		{"different machine", base, leaseValue{Machine: "other-boot/other-ns", Session: base.Session, PPID: base.PPID, PStart: base.PStart}, [2]string{}},
		{"holder records no machine", base, leaseValue{Session: base.Session, PPID: base.PPID, PStart: base.PStart}, [2]string{}},
		{"holder records no session fields", base, leaseValue{Machine: m}, [2]string{}},
		{"we know no session fields", SessionIdent{}, leaseValue{Machine: m, Session: base.Session, PPID: base.PPID, PStart: base.PStart}, [2]string{}},
		{"our parent is pid 1", SessionIdent{PPID: "1", PStart: base.PStart, Session: base.Session}, leaseValue{Machine: m, Session: base.Session, PPID: "1", PStart: base.PStart}, [2]string{}},
		{"our parent is pid 0", SessionIdent{PPID: "0", PStart: base.PStart, Session: base.Session}, leaseValue{Machine: m, Session: base.Session, PPID: "0", PStart: base.PStart}, [2]string{}},
		{"same session id, another launch", base, leaseValue{Machine: m, Session: base.Session, LaunchToken: "launch-b"}, [2]string{"launch-a", "launch-b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setSession(t, c.mine)
			stubHolderGone(t, func(string, string) bool { return false })
			f := newFixture(t, "a")
			kv := heldBy(t, f, "a", c.holder)
			cfg := f.cfg("a")
			cfg.LaunchToken = c.launch[0]
			_, err := Open(context.Background(), cfg)
			var held *LeaseHeldError
			if !errors.As(err, &held) {
				t.Fatalf("got %v, want held", err)
			}
			if lv := readLease(t, kv, "a"); lv.Instance != "earlier" {
				t.Fatalf("lease displaced: %+v", lv)
			}
		})
	}
}

// A same-session holder whose lease changes under the takeover (the compare-and-set loses) stays held, flagged so the
// caller retries at once.
func TestSameSessionTakeoverLosesToLiveWriter(t *testing.T) {
	id := ours(t)
	setSession(t, id)
	f := newFixture(t, "a")
	kv := heldBy(t, f, "a", leaseValue{Machine: needMachine(t), Session: id.Session})
	live := leaseValue{Instance: "live", Host: "h", PID: "4243", Since: "y", Machine: ourMachine(), Session: id.Session}
	stubHolderGone(t, func(string, string) bool {
		putLease(t, f, "a", live) // a write between the read and the compare-and-set
		return false
	})
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) || !held.SameSession {
		t.Fatalf("got %v, want held with SameSession", err)
	}
	if lv := readLease(t, kv, "a"); lv.Instance != "live" {
		t.Fatalf("lease displaced: %+v", lv)
	}
}

// A cross-session holder is not flagged SameSession.
func TestHeldErrorSameSessionFlagOnlyForSameSession(t *testing.T) {
	id := ours(t)
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a")
	heldBy(t, f, "a", leaseValue{Machine: needMachine(t), Session: "another"})
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) || held.SameSession || held.PID != "4242" {
		t.Fatalf("got %v (%+v)", err, held)
	}
}

// The earlier serve's next refresh fails, with the new holder named and flagged as the same session; the earlier
// serve stops consuming (Run returns) and does not touch the new holder's lease when it closes.
func TestSameSessionEarlierServeStepsDown(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	id := ours(t)
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a", "snd")
	first, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- first.Run(ctx, (&events{}).deliver) }()
	second, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatalf("the later serve of the same session must take over: %v", err)
	}
	defer second.Close()
	select {
	case err := <-runErr:
		var lost *LeaseLostError
		if !errors.As(err, &lost) || !lost.SameSession || lost.PID != fmt.Sprint(os.Getpid()) {
			t.Fatalf("Run returned %v, want a same-session LeaseLostError naming the new holder", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier serve did not step down within a refresh interval")
	}
	first.Close()
	if lv := readLease(t, putLeaseKV(t, f), "a"); lv.Instance != second.instance {
		t.Fatalf("the earlier serve's close touched the new holder's lease: %+v", lv)
	}
}

// A lost lease to another session is not flagged as the same session.
func TestLeaseLostToOtherSessionIsNotSameSession(t *testing.T) {
	setSession(t, ours(t))
	f := newFixture(t, "a")
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	kv := putLeaseKV(t, f)
	if _, err := kv.Put(context.Background(), "a", []byte(`{"instance_id":"someone-else","host":"h","pid":"77","machine":"`+ourMachine()+`","session":"another"}`)); err != nil {
		t.Fatal(err)
	}
	err = ep.refreshLease(context.Background())
	var lost *LeaseLostError
	if !errors.As(err, &lost) || lost.SameSession || lost.PID != "77" || !strings.Contains(err.Error(), "lost the lease") {
		t.Fatalf("got %v (%+v)", err, lost)
	}
}

// The legacy identity's lease is taken over by the same rule (ADR 0082), so a new serve of the session reads the legacy
// stream at once; a legacy lease of another session is still not taken.
func TestSameSessionTakesLegacyLease(t *testing.T) {
	id := ours(t)
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "old")
	nf := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	kv := heldBy(t, f, "old", leaseValue{Machine: needMachine(t), Session: id.Session})
	ep, _, _ := f.runNF(t, nf)
	if !ep.legacyReading() {
		line, _ := ep.LegacyInboxLine()
		t.Fatalf("the legacy lease of the same session was not taken: %s", line)
	}
	if lv := readLease(t, kv, "old"); lv.Instance != ep.instance {
		t.Fatalf("legacy lease %+v", lv)
	}
}

func TestOtherSessionKeepsLegacyLease(t *testing.T) {
	id := ours(t)
	setSession(t, id)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "old")
	nf := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	kv := heldBy(t, f, "old", leaseValue{Machine: needMachine(t), Session: "another"})
	ep, _, _ := f.runNF(t, nf)
	if ep.legacyReading() {
		t.Fatal("took the legacy lease of another session")
	}
	if lv := readLease(t, kv, "old"); lv.Instance != "earlier" {
		t.Fatalf("legacy lease displaced: %+v", lv)
	}
}

// The parent fallback applies only when a side has no session id.
func TestSameSessionParentFallbackWhenOneSideHasNoSessionID(t *testing.T) {
	base := ours(t)
	for _, c := range []struct {
		name         string
		mine, holder string
	}{{"holder has none", "sess-1", ""}, {"we have none", "", "sess-2"}, {"neither", "", ""}} {
		t.Run(c.name, func(t *testing.T) {
			id := base
			id.Session = c.mine
			setSession(t, id)
			stubHolderGone(t, func(string, string) bool { return false })
			f := newFixture(t, "a")
			heldBy(t, f, "a", leaseValue{Machine: needMachine(t), Session: c.holder, PPID: id.PPID, PStart: id.PStart})
			ep, err := Open(context.Background(), f.cfg("a"))
			if err != nil {
				t.Fatalf("same parent, a side without a session id: %v", err)
			}
			ep.Close()
		})
	}
}
