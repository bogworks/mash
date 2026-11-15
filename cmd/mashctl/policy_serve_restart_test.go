package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
)

// errKilled is what every protected write and witness read says after the simulated crash.
var errKilled = errors.New("the process was killed here (test)")

// killAt is the crash of a `policy serve` process at a named step of a run. Once armed, at the first call with the step
// it cancels the service's context and refuses the call; every later call of the dying process is refused too, so
// nothing more is written, which is what a process that stopped at that point leaves behind. A graceful shutdown would
// let the run finish, so the context is cancelled from inside the run, at the step.
type killAt struct {
	step  string
	stop  func() // cancels the service's context
	armed atomic.Bool
	once  sync.Once
	fired chan struct{}
	dead  atomic.Bool
}

func newKillAt(step string) *killAt { return &killAt{step: step, fired: make(chan struct{})} }

func (k *killAt) hook(_ context.Context, step string) error {
	if k.armed.Load() && step == k.step {
		k.dead.Store(true)
		k.once.Do(func() {
			k.stop()
			close(k.fired)
		})
	}
	if k.dead.Load() {
		return errKilled
	}
	return nil
}

// withStep installs the Step seam of the service for the test.
func withStep(t *testing.T, f func(ctx context.Context, step string) error) {
	t.Helper()
	old := policyServeStep
	policyServeStep = f
	t.Cleanup(func() { policyServeStep = old })
}

// restart starts `policy serve` again (with --servers set to the count given, 1 by default) on the same stores after the first process was killed and has exited. It is
// start for a second process: the first start's cleanup already waits on e.done, so this one only cancels its own
// context and restores the seams.
func (e *serveEnv) restart(servers ...string) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.stop = cancel
	ready := make(chan struct{})
	var once sync.Once
	oldCtx, oldReady := policyServeContext, policyServeReady
	policyServeContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	policyServeReady = func() { once.Do(func() { close(ready) }) }
	e.t.Cleanup(func() { cancel(); policyServeContext, policyServeReady = oldCtx, oldReady })
	e.done = make(chan int, 1)
	count := "1"
	if len(servers) > 0 {
		count = servers[0]
	}
	args := []string{"policy", "serve", "--range", serveRange, "--servers", count}
	go func() {
		var out bytes.Buffer
		e.done <- runEnv(args, func(k string) string { return e.env[k] }, &out, e.logs)
	}()
	select {
	case <-ready:
	case code := <-e.done:
		e.t.Fatalf("the restarted service exited %d: %s", code, e.logs.String())
	case <-time.After(serveWait):
		e.t.Fatal("the restarted service did not start")
	}
}

// waitExit waits for the killed process to leave `policy serve` and puts its exit code back for the cleanup.
func (e *serveEnv) waitExit() int {
	e.t.Helper()
	select {
	case code := <-e.done:
		e.done <- code
		return code
	case <-time.After(serveStopWait):
		e.t.Fatal("the killed service did not exit")
		return -1
	}
}

// revocations is the revocation list of the account JWT the servers hold: public key to time.
func (e *serveEnv) revocations() map[string]int64 {
	e.t.Helper()
	return maps.Clone(map[string]int64(e.claims().Revocations))
}

// templateOf is the group's scoped template on the servers, as JSON.
func (e *serveEnv) templateOf(group string) string {
	e.t.Helper()
	us, _ := e.scopedFor(group)
	if us == nil {
		e.t.Fatalf("no scoped signer for %s", group)
	}
	return string(mustT(json.Marshal(us.Template)))
}

// recordIssued writes the party's credential into the issue record as serve's issuer does, so a list set that removes
// the party has a key to revoke.
func (e *serveEnv) recordIssued(adm policyapply.IssuedAdmin, p *sendParty, doc []byte, gen uint64) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), serveWait)
	defer cancel()
	err := policyapply.NewAdminIssueRecord(adm).Add(ctx, policyapply.IssueEntry{
		Address: p.id.String(), PublicKey: p.pub, Purpose: policyapply.PurposeIssue, Tags: e.tagsOf(doc, p.id.String()),
		Generation: gen, IssuedAt: time.Now(),
	})
	if err != nil {
		e.t.Fatalf("record %s: %v", p.id, err)
	}
}

// killAndResume runs generation 1, then submits generation 2 (which removes the sender sdk, revoking its key, and closes
// the status verb, a template change), kills the service at step, restarts it on the same stores and the same document,
// and checks the resume.
func killAndResume(t *testing.T, step string) {
	k := newKillAt(step)
	withStep(t, k.hook)
	e := newServeEnvNoService(t, false)
	adm := e.adminOnly()
	_, _, sdk := e.provisionParty(adm, "core"), e.provisionParty(adm, "lead"), e.provisionParty(adm, "sdk")
	e.adoptLater() // the endpoints exist first: the service finds each address's inbox and provisions nothing
	e.start()

	doc1 := signed(t, e.sub, e.serveDoc(1, 0, nil))
	e.put(doc1)
	e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	e.recordIssued(adm, sdk, doc1, 1)
	gen1Template := e.templateOf(serveGroup)

	k.stop = e.stop
	k.armed.Store(true)
	doc2 := signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["groups"].(map[string]any)["c"].(map[string]any)["status"] = "closed"
		delete(m["senders"].(map[string]any), "foo.c.sdk")
	}))
	e.put(doc2)
	select {
	case <-k.fired:
	case <-time.After(serveWait):
		t.Fatalf("the run never reached %q; log:\n%s", step, e.logs.String())
	}
	e.waitExit()

	// After the kill: the servers hold the target (its template and the revocation of sdk's key), the ledger still has
	// generation 1 in force and generation 2 is not applied.
	killedRev := e.revocations()
	if _, ok := killedRev[sdk.pub]; !ok {
		t.Fatalf("after the kill the revocations are %v, want sdk's key %s", killedRev, sdk.pub)
	}
	if e.templateOf(serveGroup) == gen1Template {
		t.Fatal("after the kill the servers do not hold the target template")
	}
	if e.inForce() != 1 {
		t.Fatalf("after the kill the generation in force is %d, want 1", e.inForce())
	}
	if v, ok := e.status(); ok && v.Status != nil && v.Status.Generation == 2 && v.Status.State == policyapply.StateApplied {
		t.Fatalf("after the kill generation 2 is already applied: %+v", v.Status)
	}

	// Restart on the same stores with no hook: the intake replays the key's latest revision, which is not the document
	// in force, and the generation resumes to applied.
	k.armed.Store(false)
	k.dead.Store(false)
	e.restart()
	st := e.waitStatus("generation 2 applied after the restart", func(s *policyapply.Status) bool {
		return s.Generation == 2 && s.State == policyapply.StateApplied
	})
	if !st.InForce {
		t.Fatalf("status %+v", st)
	}
	if e.inForce() != 2 {
		t.Fatalf("after the restart the generation in force is %d, want 2", e.inForce())
	}
	if e.templateOf(serveGroup) == gen1Template {
		t.Error("after the restart the servers do not hold the target template")
	}
	// No revocation is lowered: every one present after the kill is present after the resume, with a time not earlier.
	after := e.revocations()
	for key, was := range killedRev {
		now, ok := after[key]
		switch {
		case !ok:
			t.Errorf("the revocation of %s was dropped by the resume", key)
		case now < was:
			t.Errorf("the revocation of %s was lowered from %d to %d", key, was, now)
		}
	}
	e.noSeeds(e.logs.String())
}

// Killed after the push and before the commit, `policy serve` resumes on restart: the account JWT on the servers is
// already the target, the ledger still has generation 1 in force, and the restarted service takes generation 2 to
// applied without lowering a revocation.
func TestPolicyServeResumesAfterKillBetweenPushAndCommit(t *testing.T) {
	killAndResume(t, policyapply.FenceCommit)
}

// Killed while the gate waits on the witnesses, after the push and the revocation of the removed sender's key, the
// service resumes on restart to applied without lowering a revocation.
func TestPolicyServeResumesAfterKillWhileWaitingOnWitnesses(t *testing.T) {
	killAndResume(t, "witness")
}

// heldTemplates is the group's scoped template each server holds, by server name, as JSON.
func (e *serveEnv) heldTemplates(sys *nats.Conn) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for name, c := range e.accountJWTs(sys) {
		for _, us := range scopedSigners(c) {
			if us.Role == serveGroup {
				out[name] = string(mustT(json.Marshal(us.Template)))
			}
		}
	}
	return out
}

// A server that still holds the previous generation keeps the witnesses required. Of two routed servers the second
// refuses the push (its resolver directory cannot be written; the run goes on, as it does when a server is slow), and the
// run is stopped with the first on the target and the second on generation 1. The restarted run, once the second can take the push, still opens its witnesses: the first
// server's connections cannot close (it holds the target already) and the run does not reach applied, which is the
// gate saying a connection open before the change is still open. Only when every server holds the target does the next
// restart reach applied.
func TestPolicyServeRestartWithALaggingServerKeepsWitnesses(t *testing.T) {
	k := newKillAt(policyapply.FenceRevoke)
	withStep(t, k.hook)
	e, second := newServeEnvCluster(t, false)
	dirAny, ok := clusterSecondDir.Load(e.applyEnv)
	if !ok {
		t.Fatal("no resolver directory for the second server")
	}
	dir := dirAny.(string)
	_ = second
	adm := e.adminOnly()
	_, _, sdk := e.provisionParty(adm, "core"), e.provisionParty(adm, "lead"), e.provisionParty(adm, "sdk")
	e.adoptLater()
	e.start("--servers", "2")
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))

	doc1 := signed(t, e.sub, e.serveDoc(1, 0, nil))
	e.put(doc1)
	e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	e.recordIssued(adm, sdk, doc1, 1)
	gen1 := e.heldTemplates(sys)
	if len(gen1) != 2 || gen1["s1"] != gen1["s2"] {
		t.Fatalf("after generation 1 the servers hold %v", gen1)
	}

	// The second server takes no push from here on; the run is stopped when it is about to revoke the removed sender's key.
	lockResolver(t, dir, true)
	t.Cleanup(func() { lockResolver(t, dir, false) })
	k.stop = e.stop
	k.armed.Store(true)
	e.put(signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["groups"].(map[string]any)["c"].(map[string]any)["status"] = "closed"
		delete(m["senders"].(map[string]any), "foo.c.sdk")
	})))
	select {
	case <-k.fired:
	case <-time.After(serveWait):
		t.Fatalf("the run never reached the revocation; log:\n%s", e.logs.String())
	}
	e.waitExit()
	held := e.heldTemplates(sys)
	if held["s1"] == gen1["s1"] || held["s2"] != gen1["s2"] {
		t.Fatalf("after the kill the servers hold %v, want the first on the target and the second on generation 1 (%v)", held, gen1)
	}
	if e.inForce() != 1 {
		t.Fatalf("after the kill the generation in force is %d, want 1", e.inForce())
	}

	// The second server takes pushes again, and the service is restarted. It is behind, so the witnesses are required:
	// its push reaches the second server, whose connections close, and not the first, whose connections cannot.
	lockResolver(t, dir, false)
	k.armed.Store(false)
	k.dead.Store(false)
	e.restart("2")
	st := e.waitStatus("generation 2 partial after the restart", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StatePartial })
	if st.State == policyapply.StateApplied || st.InForce || e.inForce() != 1 {
		t.Fatalf("a lagging server was taken for settled: %+v, in force %d", st, e.inForce())
	}
	witness := false
	for _, f := range st.Failing {
		witness = witness || f.Check == policyapply.CheckWitness
	}
	if !witness {
		t.Errorf("the status names no witness failure: %+v", st)
	}
	if now := e.heldTemplates(sys); now["s1"] != held["s1"] || now["s2"] != held["s1"] {
		t.Fatalf("after the restarted run the servers hold %v, want both on the target", now)
	}

	// Every server holds the target now: the next restart settles the change and reaches applied.
	e.stop()
	e.waitExit()
	e.restart("2")
	st = e.waitStatus("generation 2 applied once every server holds it", func(s *policyapply.Status) bool {
		return s.Generation == 2 && s.State == policyapply.StateApplied
	})
	if !st.InForce || e.inForce() != 2 {
		t.Fatalf("status %+v, in force %d", st, e.inForce())
	}
}

// lockResolver takes the write permission off every file and directory under dir (or gives it back), so a server whose
// resolver stores its account JWTs there fails to store a pushed one.
func lockResolver(t *testing.T, dir string, lock bool) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		}
		if lock {
			mode &^= 0o200
		}
		return os.Chmod(path, mode)
	})
	if err != nil && lock {
		t.Fatal(err)
	}
}
