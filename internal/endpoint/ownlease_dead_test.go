package endpoint

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// #1327: OwnLease must not report a lease up when its holder on this machine is gone.
func TestOwnLeaseHolderLiveness(t *testing.T) {
	machine := needMachine(t)
	host, _ := os.Hostname()
	cases := []struct {
		name     string
		machine  string
		pid      string
		gone     bool
		consults bool
		want     bool
	}{
		{"live holder, same machine", machine, "4242", false, true, true},
		{"gone holder, same machine", machine, "4242", true, true, false},
		{"other machine is never judged", "other-machine", "4242", true, false, true},
		{"no machine is never judged", "", "4242", true, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "a")
			consulted := false
			stubHolderGone(t, func(string, string) bool { consulted = true; return c.gone })
			putLease(t, f, tkey("a"), leaseValue{Instance: "x", Host: host, Version: "v", Machine: c.machine, PID: c.pid, Since: "x"})
			ep, err := OpenClient(context.Background(), f.cfg("a"))
			if err != nil {
				t.Fatal(err)
			}
			defer ep.Close()
			_, up := ep.OwnLease(context.Background())
			if up != c.want {
				t.Errorf("up = %v, want %v", up, c.want)
			}
			if consulted != c.consults {
				t.Errorf("holderGone consulted = %v, want %v", consulted, c.consults)
			}
		})
	}
}

// #1327: a crashed holder's pid reused by this process must not read as online. The recorded start time decides, so a
// lease with our pid and another start is gone, and our own genuine lease is up.
func TestOwnLeaseReusedPID(t *testing.T) {
	machine := needMachine(t)
	host, _ := os.Hostname()
	own, err := procStart(os.Getpid())
	if err != nil || own == "" {
		t.Skipf("no start time: %v", err)
	}
	cases := []struct {
		name  string
		start string
		want  bool
	}{
		{"own pid, different start (reused pid)", own + "1", false},
		{"own pid, own start (genuine lease)", own, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "a")
			putLease(t, f, tkey("a"), leaseValue{Instance: "x", Host: host, Version: "v", Machine: machine, PID: fmt.Sprint(os.Getpid()), Start: c.start, Since: "x"})
			ep, err := OpenClient(context.Background(), f.cfg("a"))
			if err != nil {
				t.Fatal(err)
			}
			defer ep.Close()
			if _, up := ep.OwnLease(context.Background()); up != c.want {
				t.Errorf("up = %v, want %v", up, c.want)
			}
		})
	}
}
