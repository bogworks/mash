package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// Every write a guarded command makes after it took the account lock is preceded by a lock check (guard.Before). These
// tests drop the lock right before step k, for each k, and require the command to stop before that step's write: the
// state is the state at the moment the lock was dropped, and no later check or write follows (REQ-016, ADR 0111).

// loseLockOf removes the account lock key from the lease bucket, as the expiry and a takeover by an adoption would.
func loseLockOf(t *testing.T, a *admin.Admin) {
	t.Helper()
	kv, err := jsOf(t, a).KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete(context.Background(), "limits-lock."+admin.AccountLockName); err != nil {
		t.Fatal(err)
	}
}

// stepHook records every step a guarded command checks before, and runs at(k, step) at the k-th (1-based) before the
// check itself.
type stepHook struct {
	steps []string
}

func installStepHook(t *testing.T, at func(k int, step string)) *stepHook {
	t.Helper()
	h := &stepHook{}
	t.Cleanup(admin.SetBeforeHook(func(step string) {
		h.steps = append(h.steps, step)
		if at != nil {
			at(len(h.steps), step)
		}
	}))
	return h
}

// dropAdded is a state without its directory records and creds files lines.
func dropAdded(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "records=") && !strings.Contains(l, ".creds=") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// guardedScenario is a guarded command on a fresh account: run it, and read the state it may change.
type guardedScenario struct {
	run      func() (out, errb string, code int)
	state    func() string
	rollback bool // the command undoes its own earlier writes when it stops (add)
	lose     func()
}

func addEnvScenario(t *testing.T, g guarded, pre func(h *addEnv)) guardedScenario {
	h := newFleetEnv(t)
	if pre != nil {
		pre(h)
	}
	args := g.args
	if g.build != nil {
		args = g.build(h)
	}
	revokes := fakeRevoke(t, nil)
	return guardedScenario{
		run: func() (string, string, int) { return h.run(args...) },
		state: func() string {
			var lb []string
			for _, n := range []string{"a", "b"} {
				l, ok := h.recordLabels(n)
				lb = append(lb, fmt.Sprint(n, ok, l))
			}
			var st []string
			for _, l := range strings.Split(h.state(), "\n") {
				if !strings.Contains(l, ".lock=") { // the policy file's own lock file comes and goes
					st = append(st, l)
				}
			}
			return strings.Join(st, "\n") + "\n" + strings.Join(lb, ";") + fmt.Sprintf("\nrevocations=%d", len(*revokes))
		},
		rollback: g.name == "add",
		lose:     func() { loseLock(t, h) },
	}
}

func TestLostLockStopsBeforeEachWriteOfEveryGuardedCommand(t *testing.T) {
	rotate := func(h *addEnv) { h.seedWith("a", extraTag) } // narrowing: a reissue rotates and revokes
	cmds := []struct {
		g   guarded
		pre func(h *addEnv)
	}{
		{guarded{name: "add", args: []string{"add", "c", "--label", "env=red"}}, nil},
		{guarded{name: "reissue", args: []string{"reissue", "a"}}, rotate},
		{guarded{name: "reissue, add-only", args: []string{"reissue", "a"}}, func(h *addEnv) { h.seedWith("a", nil) }},
		{guarded{name: "label", args: []string{"label", "b", "env=prod"}}, provisionB},
		{guarded{name: "unlabel", args: []string{"unlabel", "b", "team"}}, func(h *addEnv) { provisionB(h); h.run("label", "b", "team=x") }},
		{guarded{name: "retire", args: []string{"retire", "b"}}, provisionB},
		{guarded{name: "set-meta --label", args: []string{"set-meta", "b", "--label", "k=v"}}, provisionB},
		{guarded{name: "provision --label", args: []string{"provision"}, build: provisionWith("--label", "k=v")}, nil},
	}
	for _, c := range cmds {
		t.Run(c.g.name, func(t *testing.T) {
			// A first run, with the lock kept, lists the steps in order.
			s := addEnvScenario(t, c.g, c.pre)
			rec := installStepHook(t, nil)
			if out, errb, code := s.run(); code != 0 {
				t.Fatalf("the command does not succeed with the lock held: %d %q %q", code, out, errb)
			}
			steps := append([]string(nil), rec.steps...)
			if len(steps) == 0 {
				t.Fatal("the command checked the lock before no write")
			}
			for k := 1; k <= len(steps); k++ {
				t.Run(fmt.Sprintf("lock lost before %s", steps[k-1]), func(t *testing.T) {
					var snap string
					s := addEnvScenario(t, c.g, c.pre)
					h := installStepHook(t, func(n int, step string) {
						if n == k {
							snap = s.state()
							s.lose()
						}
					})
					out, errb, code := s.run()
					if code == 0 || !strings.Contains(errb, "lock") || !strings.Contains(errb, steps[k-1]) {
						t.Fatalf("the command went on after the lock was lost before %q: %d %q %q", steps[k-1], code, out, errb)
					}
					if len(h.steps) != k || h.steps[k-1] != steps[k-1] {
						t.Errorf("steps checked %v, want %v up to %d: a later step was reached", h.steps, steps, k)
					}
					after := s.state()
					if s.rollback {
						// add undoes its record and creds file (the append-only issue record stays): nothing is left of them.
						if strings.Contains(after, ".creds=") || !strings.Contains(after, "\nrecords=\n") {
							t.Errorf("a record or creds file was left behind after %q:\n%s", steps[k-1], after)
						}
						after, snap = dropAdded(after), dropAdded(snap)
					}
					if after != snap {
						t.Errorf("a write was made at or after %q:\nat the loss\n%s\nafter\n%s", steps[k-1], snap, after)
					}
				})
			}
		})
	}
}

// The reissue of a rotating endpoint, with the lock lost after the new creds were staged: the old user is not
// revoked, the old creds file and the directory record are untouched, and a second run finishes the rotation.
func TestLostLockAfterStagingDoesNotRevokeTheOldUser(t *testing.T) {
	h := newFleetEnv(t)
	h.seedWith("a", extraTag)
	revokes := fakeRevoke(t, nil)
	oldCreds, err := os.ReadFile(h.creds("a"))
	if err != nil {
		t.Fatal(err)
	}
	_, oldPub := h.credsClaims("a")
	recsBefore := strings.Join(h.records(), ",")
	restore := admin.SetBeforeHook(func(step string) {
		if strings.Contains(step, "revocation of the old user key") {
			loseLock(t, h)
		}
	})
	t.Cleanup(restore)
	out, errb, code := h.run("reissue", "a")
	if code == 0 || len(*revokes) != 0 {
		t.Fatalf("%d, revocations %v\n%s\n%s", code, *revokes, out, errb)
	}
	for _, w := range []string{"lock", "staged in", "the old key " + oldPub[:8] + "... is not revoked", "run mashctl reissue a again", "recorded as issued"} {
		if !strings.Contains(errb, w) {
			t.Errorf("the message lacks %q: %s", w, errb)
		}
	}
	if now, _ := os.ReadFile(h.creds("a")); string(now) != string(oldCreds) {
		t.Error("the old creds file was replaced")
	}
	if got := strings.Join(h.records(), ","); got != recsBefore {
		t.Errorf("the directory record changed: %s -> %s", recsBefore, got)
	}
	if _, err := os.Lstat(h.creds("a") + ".next"); err != nil {
		t.Errorf("the staged file is missing: %v", err)
	}

	// The lost lock is gone: the same command, run again, finishes the rotation.
	restore()
	out, errb, code = h.run("reissue", "a")
	if code != 0 || len(*revokes) != 1 {
		t.Fatalf("second run: %d, revocations %d\n%s\n%s", code, len(*revokes), out, errb)
	}
	if _, newPub := h.credsClaims("a"); newPub == oldPub {
		t.Error("the creds file still holds the old key")
	}
}

// A lock lost after the old key was revoked stops the reissue before the directory record and the creds file, and the
// message says what was done.
func TestLostLockAfterRevocationLeavesTheRecordAndFileAndSaysSo(t *testing.T) {
	h := newFleetEnv(t)
	h.seedWith("a", extraTag)
	revokes := fakeRevoke(t, nil)
	oldCreds, _ := os.ReadFile(h.creds("a"))
	installStepHook(t, func(_ int, step string) {
		if strings.Contains(step, "directory record update") {
			loseLock(t, h)
		}
	})
	out, errb, code := h.run("reissue", "a")
	if code == 0 || len(*revokes) != 1 {
		t.Fatalf("%d, revocations %d\n%s\n%s", code, len(*revokes), out, errb)
	}
	for _, w := range []string{"is revoked", "its directory record was not updated", "running mashctl reissue a again finishes it"} {
		if !strings.Contains(errb, w) {
			t.Errorf("the message lacks %q: %s", w, errb)
		}
	}
	if now, _ := os.ReadFile(h.creds("a")); string(now) != string(oldCreds) {
		t.Error("the creds file was replaced after the lock was lost")
	}
}

// initApplyEnv runs `mashctl init` (the directory and the keys' account) on a fresh apply environment.
func initApplyEnv(t *testing.T, e *applyEnv) {
	t.Helper()
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
}
