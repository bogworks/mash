package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

// How long a test lets a blocked command show that it is blocked, and the wait it gives a command for the lock when
// the lock is held on purpose.
const (
	blockedFor    = 600 * time.Millisecond
	shortLockWait = 500 * time.Millisecond
	finishWithin  = 30 * time.Second
)

const busyText = "another policy change or an adoption is in progress; nothing was changed"

type runResult struct {
	out, errb string
	code      int
}

// goRun runs a command in a goroutine and returns the channel of its result.
func (h *addEnv) goRun(args ...string) <-chan runResult {
	ch := make(chan runResult, 1)
	go func() {
		out, errb, code := h.run(args...)
		ch <- runResult{out, errb, code}
	}()
	return ch
}

func waitResult(t *testing.T, ch <-chan runResult, what string) runResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(finishWithin):
		t.Fatalf("%s did not finish", what)
		return runResult{}
	}
}

// holdLabelSeam makes `label` stop between its guard and its write until release is closed; entered gets a value
// each time a label command reaches it.
func holdLabelSeam(t *testing.T, release <-chan struct{}) <-chan struct{} {
	t.Helper()
	entered := make(chan struct{}, 4)
	afterLabelLoad = func() {
		entered <- struct{}{}
		<-release
	}
	t.Cleanup(func() { afterLabelLoad = nil })
	return entered
}

// The race the lock closes: a guarded command that passed the guard and has not written yet, and `policy adopt`
// started meanwhile. Adopt blocks until the command is done; the command's write goes through (it started first);
// a command started after the adoption is refused.
func TestAdoptWaitsForAGuardedCommandInFlight(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	release := make(chan struct{})
	entered := holdLabelSeam(t, release)

	labelRes := h.goRun("label", "a", "env=prod")
	select {
	case <-entered:
	case <-time.After(finishWithin):
		t.Fatal("label never reached the seam")
	}
	sub := userPub(t)
	adoptRes := h.goRun("policy", "adopt", "--submitter", sub, "--sys-creds", writeUserCreds(t, t.TempDir()))
	select {
	case r := <-adoptRes:
		t.Fatalf("adopt finished while a guarded command was between its guard and its write: %d %q %q", r.code, r.out, r.errb)
	case <-time.After(blockedFor):
	}
	if out, _, _ := h.run("policy", "adopt", "--show"); !strings.Contains(out, "not adopted") {
		t.Fatalf("the marker was written while the command held the lock: %q", out)
	}
	close(release)

	if r := waitResult(t, labelRes, "label"); r.code != 0 || !strings.Contains(r.out, "labeled a") {
		t.Fatalf("the command that started first did not go through: %d %q %q", r.code, r.out, r.errb)
	}
	if l, _ := h.recordLabels("a"); l["env"] != "prod" {
		t.Errorf("the label write is not in the directory record: %v", l)
	}
	if r := waitResult(t, adoptRes, "adopt"); r.code != 0 {
		t.Fatalf("adopt after the command: %d %q %q", r.code, r.out, r.errb)
	}
	out, errb, code := h.run("label", "a", "env=dev")
	if code != 1 || !strings.Contains(errb, "label is refused: this account is adopted") {
		t.Fatalf("a command started after the adoption: %d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("a"); l["env"] != "prod" {
		t.Errorf("the refused command changed the directory record: %v", l)
	}
}

// Every guarded command, when the account lock stays held by someone else, refuses with the busy text and changes
// nothing.
func TestGuardedCommandsRefuseWhileTheLockIsHeld(t *testing.T) {
	t.Cleanup(ctl.SetAccountLockWait(shortLockWait))
	for _, g := range addEnvGuarded() {
		t.Run(g.name, func(t *testing.T) {
			h := newFleetEnv(t)
			if g.pre != nil {
				g.pre(h)
			}
			args := g.args
			if g.build != nil {
				args = g.build(h)
			}
			lk, err := h.adminOf().LockAccount(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer lk.Release()
			before := h.state()
			out, errb, code := h.run(args...)
			if code != 1 || !strings.Contains(errb, busyText) {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			if after := h.state(); after != before {
				t.Errorf("a refused command changed the account:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// policy adopt that cannot take the lock refuses and writes nothing.
func TestAdoptRefusesWhileAGuardedCommandHoldsTheLock(t *testing.T) {
	t.Cleanup(ctl.SetAccountLockWait(shortLockWait))
	h := newFleetEnv(t)
	lk, err := h.adminOf().LockAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lk.Release()
	out, errb, code := h.run("policy", "adopt", "--submitter", userPub(t), "--sys-creds", writeUserCreds(t, t.TempDir()))
	if code != 1 || !strings.Contains(errb, busyText) {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if out, _, _ := h.run("policy", "adopt", "--show"); !strings.Contains(out, "not adopted") {
		t.Errorf("a refused adopt wrote the marker: %q", out)
	}
}

// The lock is free again after a guarded command ends, however it ends: refused, failed, done, or panicking.
func TestGuardedCommandReleasesTheLockOnEveryExit(t *testing.T) {
	free := func(t *testing.T, h *addEnv) {
		t.Helper()
		lk, err := h.adminOf().WithAccountLockWait(shortLockWait).LockAccount(context.Background())
		if err != nil {
			t.Fatalf("the account lock was left held: %v", err)
		}
		lk.Release()
	}
	t.Run("success", func(t *testing.T) {
		h := newFleetEnv(t)
		h.provisionDeclared("a")
		if _, errb, code := h.run("label", "a", "env=prod"); code != 0 {
			t.Fatal(errb)
		}
		free(t, h)
	})
	t.Run("failure after the guard", func(t *testing.T) {
		h := newFleetEnv(t)
		if _, _, code := h.run("label", "nosuch", "env=prod"); code != 1 {
			t.Fatalf("code %d", code)
		}
		free(t, h)
	})
	t.Run("refused as adopted", func(t *testing.T) {
		h := newFleetEnv(t)
		h.adoptAs(userPub(t))
		if _, _, code := h.run("label", "a", "env=prod"); code != 1 {
			t.Fatalf("code %d", code)
		}
		free(t, h)
	})
	t.Run("panic", func(t *testing.T) {
		h := newFleetEnv(t)
		h.provisionDeclared("a")
		afterLabelLoad = func() { panic("boom") }
		t.Cleanup(func() { afterLabelLoad = nil })
		func() {
			defer func() { _ = recover() }()
			h.run("label", "a", "env=prod")
		}()
		afterLabelLoad = nil
		free(t, h)
	})
}

// Two guarded commands serialise: the second does not reach its write until the first has finished.
func TestTwoGuardedCommandsSerialise(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	h.provisionDeclared("b")
	release := make(chan struct{})
	entered := holdLabelSeam(t, release)
	first := h.goRun("label", "a", "env=prod")
	select {
	case <-entered:
	case <-time.After(finishWithin):
		t.Fatal("the first label never reached the seam")
	}
	second := h.goRun("label", "b", "env=dev")
	select {
	case <-entered:
		t.Fatal("the second command got past its guard while the first held the lock")
	case <-time.After(blockedFor):
	}
	close(release)
	for _, c := range []struct {
		ch   <-chan runResult
		name string
	}{{first, "first"}, {second, "second"}} {
		if r := waitResult(t, c.ch, c.name); r.code != 0 {
			t.Fatalf("%s: %d %q %q", c.name, r.code, r.out, r.errb)
		}
	}
	la, _ := h.recordLabels("a")
	lb, _ := h.recordLabels("b")
	if la["env"] != "prod" || lb["env"] != "dev" {
		t.Errorf("both writes should be in the directory: a %v, b %v", la, lb)
	}
}

// loseLock removes the account lock key from the lease bucket, as the expiry and a takeover by an adoption would.
func loseLock(t *testing.T, h *addEnv) {
	t.Helper()
	kv, err := jsOf(t, h.adminOf()).KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete(context.Background(), "limits-lock."+admin.AccountLockName); err != nil {
		t.Fatal(err)
	}
}

// A lock lost between the guard and the write stops the command before the write: label leaves the record alone.
func TestLostLockStopsLabelBeforeItsWrite(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	afterLabelLoad = func() { loseLock(t, h) }
	t.Cleanup(func() { afterLabelLoad = nil })
	out, errb, code := h.run("label", "a", "env=prod")
	if code != 1 || !strings.Contains(errb, "lock") || strings.Contains(out, "labeled") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("a"); len(l) != 0 {
		t.Errorf("the record changed after the lock was lost: %v", l)
	}
}

// A lock lost after add's directory record and creds file are written stops add before the creds file, and the record is
// are rolled back: nothing is left of the add.
func TestLostLockStopsAddBeforeCredsAndPolicy(t *testing.T) {
	h := newFleetEnv(t)
	afterProvision = func() { loseLock(t, h) }
	t.Cleanup(func() { afterProvision = nil })
	out, errb, code := h.run("add", "c", "--label", "env=red")
	if code != 1 || !strings.Contains(errb, "lock") || strings.Contains(out, "added") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	for _, r := range h.records() {
		if strings.HasPrefix(r, "c/") {
			t.Errorf("the record of c was left behind: %v", h.records())
		}
	}
	if _, err := os.Lstat(h.creds("c")); err == nil {
		t.Error("the creds file of c was left behind")
	}
}

func natsConnect(t *testing.T, e *applyEnv) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), nats.UserCredentials(e.pl.Creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}
