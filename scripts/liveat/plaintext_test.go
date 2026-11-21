package main

import (
	"net"
	"testing"
	"time"
)

func TestConnCounterCountsConnects(t *testing.T) {
	cc, err := newConnCounter()
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	if cc.Count() != 0 {
		t.Fatal("a new listener has connects")
	}
	c, err := net.Dial("tcp", cc.Addr())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for cc.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if cc.Count() != 1 {
		t.Fatalf("count = %d, want 1", cc.Count())
	}
}

func TestPlainRefusalOK(t *testing.T) {
	good := Result{Code: 2, Stderr: `mash: refusing a plain nats:// connection to "127.0.0.1": set MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1; use a tls:// URL`}
	for name, c := range map[string]struct {
		r        Result
		connects int64
		want     bool
	}{
		"refused, no connection":     {good, 0, true},
		"a connection was made":      {good, 1, false},
		"wrong exit code":            {Result{Code: 1, Stderr: good.Stderr}, 0, false},
		"does not name the override": {Result{Code: 2, Stderr: "mash: refused, use tls://"}, 0, false},
		"does not name the fix":      {Result{Code: 2, Stderr: "set MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1"}, 0, false},
		"started and ran":            {Result{Code: 0}, 0, false},
	} {
		if got, detail := plainRefusalOK(c.r, 2, c.connects); got != c.want {
			t.Errorf("%s: got %v, want %v (%s)", name, got, c.want, detail)
		}
	}
}

func TestPlainWarnedOK(t *testing.T) {
	warn := "warning: encryption to the server is OFF (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set)"
	for name, c := range map[string]struct {
		r        Result
		connects int64
		want     bool
	}{
		"connected and warned on stderr": {Result{Code: 1, Stderr: warn}, 1, true},
		"no connection":                  {Result{Stderr: warn}, 0, false},
		"no warning":                     {Result{Stderr: "mash: connect failed"}, 1, false},
		"warning on stdout":              {Result{Stdout: warn, Stderr: warn}, 1, false},
	} {
		if got, detail := plainWarnedOK(c.r, c.connects); got != c.want {
			t.Errorf("%s: got %v, want %v (%s)", name, got, c.want, detail)
		}
	}
}

func TestDoctorWarnedOnStderr(t *testing.T) {
	warn := "warning: encryption to the server is OFF (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set)"
	report := "config    ok\ncreds     ok\nserver    FAIL  the server did not answer: nats://127.0.0.1:1\n\n1 problem.\n"
	for name, c := range map[string]struct {
		r    Result
		want bool
	}{
		"report on stdout, warning on stderr": {Result{Code: 1, Stdout: report, Stderr: warn}, true},
		"no warning on stderr":                {Result{Code: 1, Stdout: report}, false},
		"warning only on stdout":              {Result{Code: 1, Stdout: report + warn + "\n"}, false},
	} {
		if got, detail := doctorWarnedOnStderr(c.r); got != c.want {
			t.Errorf("%s: got %v, want %v (%s)", name, got, c.want, detail)
		}
	}
}
