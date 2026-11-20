package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
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

func (s *revokeStub) fn(_ context.Context, _ config.Config, env func(string) string, servers int, users []string, out io.Writer, _ func() time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, users...)
	s.creds = append(s.creds, env("MASH_SYS_CREDS"))
	s.srv = append(s.srv, servers)
	if s.probe != nil {
		s.seen = append(s.seen, s.probe())
	}
	if s.err != nil {
		return 0, s.err
	}
	for _, u := range users {
		fmt.Fprintf(out, "revoked user key %s...: 1 server(s) acknowledged\n", ctl.ShortKey(u))
	}
	return fakeAcked, nil
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
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
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
	if out != "Deleted endpoint o.g.a\n" {
		t.Errorf("out %q", out)
	}
}

func TestRetireRefusedWhenRevokeFails(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("no system account credentials")
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || out != "" || !strings.Contains(errb, "error: ") || !strings.Contains(errb, "no system account credentials") || strings.Contains(errb, "--no-revoke") || !strings.Contains(errb, "nothing was removed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(h.records()) != 1 {
		t.Error("retire removed something though the revoke failed")
	}
}

// --no-revoke, --force and --delete-creds are gone: each is an unknown option, and nothing is attempted.
func TestRemovedDeleteFlagsAreUnknownOptions(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	for _, f := range []string{"--no-revoke", "--force", "--delete-creds"} {
		out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes", f)
		want := "error: unknown option " + strconv.Quote(f) + "\nhint: mashctl endpoint delete --help\n"
		if code != 2 || out != "" || errb != want {
			t.Errorf("%s: %d %q %q", f, code, out, errb)
		}
	}
	if len(s.calls) != 0 || len(h.records()) != 1 {
		t.Errorf("something ran: calls %v records %v", s.calls, h.records())
	}
}

func TestRetireNoRecordRevokesNothing(t *testing.T) {
	h := newFleetEnv(t)
	s := stubRevoke(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || len(s.calls) != 0 || out != "" || !strings.Contains(errb, "o.g.a does not exist") {
		t.Fatalf("%d %v %q %q", code, s.calls, out, errb)
	}
}

func TestRetireBadServersAndUsage(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_SERVERS"] = "zero"
	if _, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes"); code != 2 || !strings.Contains(errb, "MASH_SERVERS must be") {
		t.Fatalf("%d %q", code, errb)
	}
	delete(h.env, "MASH_SERVERS")
	if _, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes", "--bogus"); code != 2 || !strings.Contains(errb, "unknown option") || !strings.Contains(errb, "endpoint delete --help") {
		t.Fatalf("%d %q", code, errb)
	}
}

func TestEndpointDeleteAcceptsServersAndSysCredsFlags(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	if _, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes", "--sys-creds=/s.creds", "--servers=2"); code != 0 || len(s.calls) != 1 || s.srv[0] != 2 || s.creds[0] != "/s.creds" || len(h.records()) != 0 {
		t.Fatalf("%d %q %v %v", code, errb, s.srv, s.creds)
	}
}

func TestRevokeUserKeysNoSysCredsSaysMashSysCreds(t *testing.T) {
	_, err := revokeUserKeys(context.Background(), config.Config{Dir: t.TempDir()}, func(string) string { return "" }, 0, nil, io.Discard, nil)
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "pass --sys-creds FILE or set MASH_SYS_CREDS") {
		t.Errorf("%v", err)
	}
}

func TestUsageTexts(t *testing.T) {
	if !strings.Contains(endpointDeleteUsage, "[--yes] [--dry-run]") || strings.Contains(endpointDeleteUsage, "--force") || strings.Contains(endpointDeleteUsage, "--no-revoke") || strings.Contains(endpointDeleteUsage, "--delete-creds") || !strings.Contains(endpointDeleteUsage, "--sys-creds FILE") || !strings.Contains(endpointDeleteUsage, "--servers N") {
		t.Errorf("endpointDeleteUsage %q", endpointDeleteUsage)
	}
}

// shortUser rewrites name's directory record to carry a user value that is not a key (3 characters).
func (h *fleetEnv) shortUser(instance, user string) {
	h.t.Helper()
	name := ad(instance).Flat()
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

func TestRetireWithAShortUserValueDoesNotPanic(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	h.shortUser("b", "abc")
	s := stubRevoke(t)
	s.err = errors.New("resolver down")
	if _, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes"); code != 1 || !strings.Contains(errb, "user key abc...") {
		t.Fatalf("%d %q", code, errb)
	}
	s.err = nil
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 0 || out != "Deleted endpoint o.g.b\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}
