package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// revokeStub records the keys revoked and whether the endpoint's record still existed at that moment.
type revokeStub struct {
	mu    sync.Mutex
	calls []string
	err   error
	creds []string
	srv   []int
	probe func() []string // the directory records when the stub is called
	seen  [][]string
}

func (s *revokeStub) fn(_ context.Context, _ config.Config, env func(string) string, servers int, users []string, out io.Writer, _ func() time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, users...)
	s.creds = append(s.creds, env("MASH_SYS_CREDS"))
	s.srv = append(s.srv, servers)
	if s.probe != nil {
		s.seen = append(s.seen, s.probe())
	}
	if s.err != nil {
		return s.err
	}
	for _, u := range users {
		fmt.Fprintf(out, "revoked user key %s...: 1 server(s) acknowledged\n", ctl.ShortKey(u))
	}
	return nil
}

// stubRevoke replaces revokeUsers for the test and restores it.
func stubRevoke(t *testing.T) *revokeStub {
	t.Helper()
	s := &revokeStub{}
	old := revokeUsers
	revokeUsers = s.fn
	t.Cleanup(func() { revokeUsers = old })
	return s
}

// Every test in the package runs against a stub that revokes nothing: the real one needs system account credentials.
func init() { revokeUsers = (&revokeStub{}).fn }

func TestRetireRevokesBeforeDeleting(t *testing.T) {
	h := newFleetEnv(t)
	u := h.provisionDeclared("a")
	s := stubRevoke(t)
	s.probe = h.records
	h.env["MASH_SYS_CREDS"], h.env["MASH_SERVERS"] = "/x/sys.creds", "3"
	out, errb, code := h.run("retire", "a")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(s.calls) != 1 || s.calls[0] != u || s.creds[0] != "/x/sys.creds" || s.srv[0] != 3 {
		t.Fatalf("calls %v %v %v", s.calls, s.creds, s.srv)
	}
	if len(s.seen[0]) != 1 {
		t.Fatalf("the record was gone before the revoke: %v", s.seen[0])
	}
	if len(h.records()) != 0 {
		t.Error("record left")
	}
	if !strings.HasPrefix(out, "retired a\n") || !strings.Contains(out, "revoked in the account JWT") || strings.Contains(out, "not implemented") {
		t.Errorf("out %q", out)
	}
}

func TestRetireRefusedWhenRevokeFails(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("no system account credentials")
	out, errb, code := h.run("retire", "a")
	if code != 1 || strings.Contains(out, "retired") || !strings.Contains(errb, "no system account credentials") || !strings.Contains(errb, "--no-revoke") || !strings.Contains(errb, "nothing was removed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(h.records()) != 1 {
		t.Error("retire removed something though the revoke failed")
	}
}

func TestRetireNoRevokeSkips(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("must not be called")
	out, errb, code := h.run("retire", "a", "--no-revoke")
	if code != 0 || len(s.calls) != 0 || !strings.Contains(out, "not revoked") {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
	// the warning is on stderr before the removal and again after it
	if n := strings.Count(errb, "WARNING: --no-revoke: a's NATS user key U"); n != 2 || !strings.Contains(errb, "is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid.") {
		t.Errorf("warning count %d: %q", n, errb)
	}
}

func TestRetireForceWithoutRecordRevokesNothing(t *testing.T) {
	h := newFleetEnv(t)
	s := stubRevoke(t)
	out, errb, code := h.run("retire", "a", "--force")
	if code != 0 || len(s.calls) != 0 || !strings.Contains(errb, "nothing was revoked") {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
}

func TestRetireBadServersAndUsage(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_SERVERS"] = "zero"
	if _, errb, code := h.run("retire", "a"); code != 2 || !strings.Contains(errb, "MASH_SERVERS must be") {
		t.Fatalf("%d %q", code, errb)
	}
	delete(h.env, "MASH_SERVERS")
	if _, errb, code := h.run("retire", "a", "--bogus"); code != 2 || !strings.Contains(errb, "unknown option") || !strings.Contains(errb, "[--no-revoke]") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestDeprovisionAcceptsServersAndSysCredsFlags(t *testing.T) {
	h := newAddEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	if _, errb, code := h.run("deprovision", "a", "--sys-creds=/s.creds", "--servers=2"); code != 0 || len(s.calls) != 1 || s.srv[0] != 2 || s.creds[0] != "/s.creds" || len(h.records()) != 0 {
		t.Fatalf("%d %q %v %v", code, errb, s.srv, s.creds)
	}
}

func TestRevokeUserKeysNoSysCredsSaysMashSysCreds(t *testing.T) {
	err := revokeUserKeys(context.Background(), config.Config{Dir: t.TempDir()}, func(string) string { return "" }, 0, nil, io.Discard, nil)
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "pass --sys-creds FILE or set MASH_SYS_CREDS") {
		t.Errorf("%v", err)
	}
}

func TestDeprovisionRevokesBeforeRemoving(t *testing.T) {
	h := newAddEnv(t)
	u := h.provisionDeclared("a")
	s := stubRevoke(t)
	s.probe = h.records
	h.env["MASH_SYS_CREDS"], h.env["MASH_SERVERS"] = "/s.creds", "2"
	out, errb, code := h.run("deprovision", "a")
	if code != 0 || s.srv[0] != 2 || len(s.calls) != 1 || s.calls[0] != u || s.creds[0] != "/s.creds" || len(s.seen[0]) != 1 || len(h.records()) != 0 {
		t.Fatalf("%d %v %v %q %q", code, s.calls, s.seen, out, errb)
	}
	if !strings.Contains(out, "revoked in the account JWT") || strings.Contains(out, "operator tooling") {
		t.Errorf("out %q", out)
	}
}

func TestDeprovisionRefusedWhenRevokeFailsAndNoRevoke(t *testing.T) {
	h := newAddEnv(t)
	u := h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("not confirmed")
	_, errb, code := h.run("deprovision", "a")
	if code != 1 || !strings.Contains(errb, "not confirmed") || !strings.Contains(errb, "--no-revoke") || len(h.records()) != 1 {
		t.Fatalf("%d %q", code, errb)
	}
	// --user form refuses the same way
	if _, errb, code = h.run("deprovision", "a", "--user", u); code != 1 || len(h.records()) != 1 {
		t.Fatalf("--user: %d %q", code, errb)
	}
	s.calls = nil
	out, errb, code := h.run("deprovision", "a", "--no-revoke")
	if code != 0 || len(s.calls) != 0 || !strings.Contains(out, "not revoked") || len(h.records()) != 0 {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
	if n := strings.Count(errb, "WARNING: --no-revoke: a's NATS user key "+u+" is NOT revoked and can still connect"); n != 2 {
		t.Errorf("warning count %d: %q", n, errb)
	}
}

func TestDeprovisionUserFormAndMismatch(t *testing.T) {
	h := newAddEnv(t)
	u := h.provisionDeclared("a")
	_, other := h.e.Mint(t, "other")
	s := stubRevoke(t)
	// the record names another user: the guard refuses and nothing is revoked
	if _, _, code := h.run("deprovision", "a", "--user", other); code == 0 || len(s.calls) != 0 || len(h.records()) != 1 {
		t.Fatalf("mismatch: %d %v", code, s.calls)
	}
	out, errb, code := h.run("deprovision", "a", "--user", u)
	if code != 0 || len(s.calls) != 1 || s.calls[0] != u || !strings.Contains(out, "revoked in the account JWT") {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
}

func TestDeprovisionUserFormWithoutRecordRevokesNothing(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "ghost")
	s := stubRevoke(t)
	if _, errb, code := ctlRun(t, e, "deprovision", "ghost", "--user", u); code == 0 || len(s.calls) != 0 || !strings.Contains(errb, "no directory record") {
		t.Fatalf("%d %v %q", code, s.calls, errb)
	}
}

func TestDeprovisionWithoutRecordRevokesNothing(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	s := stubRevoke(t)
	if _, errb, code := ctlRun(t, e, "deprovision", "ghost"); code != 1 || len(s.calls) != 0 || !strings.Contains(errb, "no endpoint named ghost") {
		t.Fatalf("%d %v %q", code, s.calls, errb)
	}
}

func TestDeprovisionDrainedRevokesOnlyWhenGuardPasses(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "c")
	if _, errb, code := ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account); code != 0 {
		t.Fatal(errb)
	}
	s := stubRevoke(t)
	out, _, _ := ctlRun(t, e, "suspend", "c")
	if !strings.Contains(out, "new mail parked; the endpoint's credentials are still valid") || len(s.calls) != 0 {
		t.Fatalf("suspend: %q %v", out, s.calls)
	}
	id := regexp.MustCompile(`id ([0-9a-f]{32}),`).FindStringSubmatch(out)[1]
	if _, _, code := ctlRun(t, e, "deprovision", "c", "--drained-to", "7", "--suspension", id); code == 0 || len(s.calls) != 0 {
		t.Fatalf("wrong seq: %d %v", code, s.calls)
	}
	out, errb, code := ctlRun(t, e, "deprovision", "c", "--suspension", id, "--drained-to", "0", "--no-revoke")
	if code != 0 || len(s.calls) != 0 || strings.Count(errb, "WARNING: --no-revoke: c's NATS user key "+u+" is NOT revoked") != 2 {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
}

func TestDeprovisionDrainedRevokes(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "c")
	ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account)
	s := stubRevoke(t)
	out, _, _ := ctlRun(t, e, "suspend", "c")
	id := regexp.MustCompile(`id ([0-9a-f]{32}),`).FindStringSubmatch(out)[1]
	out, errb, code := ctlRun(t, e, "deprovision", "c", "--suspension", id, "--drained-to", "0")
	if code != 0 || len(s.calls) != 1 || s.calls[0] != u || !strings.Contains(out, "revoked in the account JWT") {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
}

func TestUsageTexts(t *testing.T) {
	if !strings.Contains(retireUsage, "[--force] [--no-revoke]") || !strings.Contains(retireUsage, "--sys-creds FILE") || !strings.Contains(retireUsage, "--servers N") {
		t.Errorf("retireUsage %q", retireUsage)
	}
}

// shortUser rewrites name's directory record to carry a user value that is not a key (3 characters).
func (h *addEnv) shortUser(name, user string) {
	h.t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	rec, err := store.GetRecord(ctx, kv, name)
	if err != nil {
		h.t.Fatal(err)
	}
	rec.User = user
	b, _ := json.Marshal(rec)
	if _, err := kv.Put(ctx, name, b); err != nil {
		h.t.Fatal(err)
	}
}

func TestDeprovisionWithAShortUserValueDoesNotPanic(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("w")
	h.shortUser("w", "abc")
	s := stubRevoke(t)
	s.err = errors.New("resolver down")
	_, errb, code := h.run("deprovision", "w")
	if code != 1 || !strings.Contains(errb, "user key abc...: resolver down") || len(h.records()) != 1 {
		t.Fatalf("%d %q", code, errb)
	}
	s.err = nil
	out, errb, code := h.run("deprovision", "w")
	if code != 0 || !strings.Contains(out, "user key abc... revoked in the account JWT") || len(h.records()) != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestRetireWithAShortUserValueDoesNotPanic(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	h.shortUser("b", "abc")
	s := stubRevoke(t)
	s.err = errors.New("resolver down")
	if _, errb, code := h.run("retire", "b"); code != 1 || !strings.Contains(errb, "user key abc...") {
		t.Fatalf("%d %q", code, errb)
	}
	s.err = nil
	out, errb, code := h.run("retire", "b")
	if code != 0 || !strings.Contains(out, "user key abc... revoked") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

// credRevokeRun runs `credential revoke` against the test server with revokeUsers and keyRevoked stubbed.
func credRevokeRun(t *testing.T, pub string, revoke func(io.Writer) error, keyIsRevoked bool, args ...string) (int, string, string) {
	t.Helper()
	h := newFleetEnv(t)
	oldR, oldK := revokeUsers, keyRevoked
	revokeUsers = func(_ context.Context, _ config.Config, _ func(string) string, _ int, _ []string, out io.Writer, _ func() time.Time) error {
		return revoke(out)
	}
	keyRevoked = func(context.Context, config.Config, func(string) string, int, string) (bool, error) {
		return keyIsRevoked, nil
	}
	t.Cleanup(func() { revokeUsers, keyRevoked = oldR, oldK })
	var out, errb bytes.Buffer
	env := func(k string) string { return h.env[k] }
	rc := run(append([]string{"credential", "revoke", pub}, args...), env, config.LookupFromFunc(env), &out, &errb)
	return rc, out.String(), errb.String()
}

func TestCredentialRevokeOutcomesAndExitCodes(t *testing.T) {
	_, pub, _ := credential.NewKey()
	acked := func(n int) func(io.Writer) error {
		return func(w io.Writer) error {
			fmt.Fprintf(w, "revoked user key %s...: %d server(s) acknowledged\n", ctl.ShortKey(pub), n)
			return nil
		}
	}
	for _, tc := range []struct {
		name    string
		revoke  func(io.Writer) error
		known   bool
		args    []string
		rc      int
		outcome string
		acked   int
		refusal string
	}{
		{"revoked", acked(2), true, []string{"--servers", "2"}, 0, "revoked", 2, ""},
		{"unchanged", func(w io.Writer) error {
			fmt.Fprintf(w, "user key %s...: already revoked\n", ctl.ShortKey(pub))
			return nil
		}, true, nil, 0, "unchanged", 0, ""},
		{"fewer acknowledged", acked(1), true, []string{"--servers", "3"}, 5, "partial", 1, ""},
		{"not confirmed", func(io.Writer) error { return &ackedError{err: errors.New("not confirmed"), acked: 2} }, true, []string{"--servers", "3"}, 5, "partial", 2, ""},
		{"error", func(io.Writer) error { return errors.New("no system account credentials") }, true, nil, 6, "error", 0, ""},
		{"not found", acked(1), false, nil, 1, "refused", 0, "not_found"},
	} {
		for _, asJSON := range []bool{false, true} {
			args := tc.args
			if asJSON {
				args = append(append([]string{}, args...), "--json")
			}
			rc, so, se := credRevokeRun(t, pub, tc.revoke, tc.known, args...)
			if rc != tc.rc {
				t.Fatalf("%s json=%v: rc %d, want %d: %q %q", tc.name, asJSON, rc, tc.rc, so, se)
			}
			if !asJSON {
				if tc.outcome == "revoked" && !strings.Contains(so, "server(s) acknowledged") {
					t.Errorf("%s: text lost: %q", tc.name, so)
				}
				continue
			}
			if strings.Count(so, "\n") != 1 {
				t.Fatalf("%s: stdout is not one line: %q", tc.name, so)
			}
			var got credential.RevokeResult
			if err := json.Unmarshal([]byte(so), &got); err != nil {
				t.Fatalf("%s: %v %q", tc.name, err, so)
			}
			if got.Outcome != tc.outcome || got.PublicKey != pub || got.ServersAcknowledged != tc.acked {
				t.Errorf("%s: %+v", tc.name, got)
			}
			if (tc.refusal == "") != (got.Refusal == nil) || got.Refusal != nil && got.Refusal.Code != tc.refusal {
				t.Errorf("%s: refusal %+v", tc.name, got.Refusal)
			}
		}
	}
}
