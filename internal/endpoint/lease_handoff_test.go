package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// deadPID is the pid of a process that ran and was waited for, so it is gone.
func deadPID(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run true: %v", err)
	}
	return fmt.Sprint(cmd.Process.Pid)
}

func putLease(t *testing.T, f *fixture, name string, lv leaseValue) jetstream.KeyValue {
	t.Helper()
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	val, _ := json.Marshal(lv)
	if _, err := kv.Put(context.Background(), name, val); err != nil {
		t.Fatal(err)
	}
	return kv
}

func readLease(t *testing.T, kv jetstream.KeyValue, name string) leaseValue {
	t.Helper()
	en, err := kv.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	var lv leaseValue
	if err := json.Unmarshal(en.Value(), &lv); err != nil {
		t.Fatal(err)
	}
	return lv
}

func stubHolderGone(t *testing.T, fn func(string, string) bool) {
	old := holderGone
	holderGone = fn
	t.Cleanup(func() { holderGone = old })
}

// needMachine skips where the OS gives no machine identity, and returns ours.
func needMachine(t *testing.T) string {
	t.Helper()
	m := ourMachine()
	if m == "" {
		t.Skip("no machine identity on this OS")
	}
	return m
}

func TestLeaseTakenOverFromDeadHolderOnSameMachine(t *testing.T) {
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	kv := putLease(t, f, "a", leaseValue{Instance: "dead", Host: host, Machine: needMachine(t), PID: deadPID(t), Since: "x"})
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatalf("open over a dead holder: %v", err)
	}
	defer ep.Close()
	if lv := readLease(t, kv, "a"); lv.Instance == "dead" || lv.PID != fmt.Sprint(os.Getpid()) {
		t.Fatalf("lease %+v", lv)
	}
}

func TestLeaseNotTakenFromLiveHolderOnSameHost(t *testing.T) {
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	putLease(t, f, "a", leaseValue{Instance: "live", Host: host, Machine: needMachine(t), PID: "4242", Since: "x"})
	stubHolderGone(t, func(string, string) bool { return false })
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
}

// The H1 case: a live holder on another host or container that shares our hostname has its pid absent here, but it
// is not gone.
func TestLeaseSameHostnameOtherMachineNotTakenOver(t *testing.T) {
	needMachine(t)
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	kv := putLease(t, f, "a", leaseValue{Instance: "remote", Host: host, Machine: "other-boot/other-ns", PID: deadPID(t), Since: "x"})
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
	if lv := readLease(t, kv, "a"); lv.Instance != "remote" {
		t.Fatalf("lease displaced: %+v", lv)
	}
}

// A lease from a serve that did not record a machine is never fast-taken; it expires normally.
func TestLeaseNoMachineNotTakenOver(t *testing.T) {
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	kv := putLease(t, f, "a", leaseValue{Instance: "old", Host: host, PID: deadPID(t), Since: "x"})
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
	if lv := readLease(t, kv, "a"); lv.Instance != "old" {
		t.Fatalf("lease displaced: %+v", lv)
	}
}

func TestLeaseRecordsMachine(t *testing.T) {
	want := needMachine(t)
	f := newFixture(t, "a")
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	var lv leaseValue
	if err := json.Unmarshal(ep.lease(), &lv); err != nil || lv.Machine == "" || lv.Machine != want {
		t.Fatalf("lease machine %q, want %q (%v)", lv.Machine, want, err)
	}
}

func TestMachineID(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if _, err := machineID(); err == nil {
			t.Error("want an error where the machine identity is unknown")
		}
		return
	}
	a, err := machineID()
	if err != nil || a == "" {
		t.Fatalf("machineID: %q %v", a, err)
	}
	if b, _ := machineID(); a != b {
		t.Errorf("unstable: %q then %q", a, b)
	}
}

func TestLeaseOtherHostIsNeverJudged(t *testing.T) {
	f := newFixture(t, "a")
	stubHolderGone(t, func(string, string) bool { t.Error("holderGone consulted"); return true })
	for _, host := range []string{"otherhost", ""} {
		putLease(t, f, "a", leaseValue{Instance: "far", Host: host, PID: "4242", Since: "x"})
		_, err := Open(context.Background(), f.cfg("a"))
		var held *LeaseHeldError
		if !errors.As(err, &held) {
			t.Fatalf("host %q: got %v, want held", host, err)
		}
	}
}

func TestLeaseTakeoverLosesToLiveWriter(t *testing.T) {
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	kv := putLease(t, f, "a", leaseValue{Instance: "old", Host: host, Machine: needMachine(t), PID: "4242", Since: "x"})
	live, _ := json.Marshal(leaseValue{Instance: "live", Host: host, PID: "4243", Since: "y"})
	stubHolderGone(t, func(string, string) bool {
		if _, err := kv.Put(context.Background(), "a", live); err != nil {
			t.Error(err)
		}
		return true
	})
	_, err := Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
	if lv := readLease(t, kv, "a"); lv.Instance != "live" {
		t.Fatalf("lease displaced: %+v", lv)
	}
}

// A holder in this very process is live whatever its command name (the test binary is not "mash").
func TestLeaseOwnPIDIsNeverDisplaced(t *testing.T) {
	f := newFixture(t, "a")
	first, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	stubHolderGone(t, func(string, string) bool { t.Error("holderGone consulted for own pid"); return true })
	_, err = Open(context.Background(), f.cfg("a"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
}

// liveChild starts a process that outlives the test body and returns its pid and real start time. It is copied to a
// name that is not "mash", the H1 case.
func liveChild(t *testing.T) (string, string) {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	bin := filepath.Join(t.TempDir(), "notmash")
	b, err := os.ReadFile(sleep)
	if err != nil || os.WriteFile(bin, b, 0o755) != nil {
		t.Skip("cannot copy sleep")
	}
	cmd := exec.Command(bin, "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	start, err := procStart(cmd.Process.Pid)
	if err != nil {
		t.Skipf("no start time: %v", err)
	}
	return fmt.Sprint(cmd.Process.Pid), start
}

func TestHolderGone(t *testing.T) {
	if !holderGoneOS(deadPID(t), "") {
		t.Error("an exited child must be gone")
	}
	pid, start := liveChild(t)
	if holderGoneOS(pid, start) {
		t.Error("a live process with the recorded start, whatever its name, must not be gone")
	}
	if !holderGoneOS(pid, start+"1") {
		t.Error("a live pid with a different start is a reused pid, so gone")
	}
	if holderGoneOS(pid, "") {
		t.Error("a lease with no start and a live pid must not be gone")
	}
	for _, bad := range []string{"", "abc", "0", "1", "-5"} {
		if holderGoneOS(bad, "x") {
			t.Errorf("pid %q must not be gone", bad)
		}
	}
}

func openOver(t *testing.T, lv leaseValue) (error, jetstream.KeyValue) {
	f := newFixture(t, "a")
	host, _ := os.Hostname()
	lv.Host = host
	lv.Machine = needMachine(t)
	kv := putLease(t, f, "a", lv)
	ep, err := Open(context.Background(), f.cfg("a"))
	if err == nil {
		ep.Close()
	}
	return err, kv
}

func TestLeaseLiveNonMashHolderKept(t *testing.T) {
	pid, start := liveChild(t)
	err, kv := openOver(t, leaseValue{Instance: "live", PID: pid, Start: start, Since: "x"})
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
	if lv := readLease(t, kv, "a"); lv.Instance != "live" {
		t.Fatalf("lease displaced: %+v", lv)
	}
}

func TestLeaseReusedPIDTakenOver(t *testing.T) {
	pid, start := liveChild(t)
	if err, _ := openOver(t, leaseValue{Instance: "old", PID: pid, Start: start + "1", Since: "x"}); err != nil {
		t.Fatalf("open over a reused pid: %v", err)
	}
}

func TestLeaseNoStartLivePIDKept(t *testing.T) {
	pid, _ := liveChild(t)
	err, _ := openOver(t, leaseValue{Instance: "live", PID: pid, Since: "x"})
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want held", err)
	}
}

func TestLeaseRecordsStart(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no start time on this OS")
	}
	f := newFixture(t, "a")
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	want, _ := procStart(os.Getpid())
	var lv leaseValue
	if err := json.Unmarshal(ep.lease(), &lv); err != nil || lv.Start == "" || lv.Start != want {
		t.Fatalf("lease start %q, want %q (%v)", lv.Start, want, err)
	}
}

func TestProcStart(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if _, err := procStart(os.Getpid()); err == nil {
			t.Error("want an error where the start time is unknown")
		}
		return
	}
	a, err := procStart(os.Getpid())
	if err != nil || a == "" {
		t.Fatalf("procStart: %q %v", a, err)
	}
	if b, _ := procStart(os.Getpid()); a != b {
		t.Errorf("unstable: %q then %q", a, b)
	}
	if _, err := procStart(1 << 30); err == nil {
		t.Error("want an error for a missing pid")
	}
}

// putLeaseKV is the lease bucket, read as the admin.
func putLeaseKV(t *testing.T, f *fixture) jetstream.KeyValue {
	t.Helper()
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}
