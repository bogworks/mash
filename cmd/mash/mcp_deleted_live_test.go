package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/nats-io/jwt/v2"
)

// #1396, the live gap: an endpoint deleted while serve runs. The revoked connection's reconnect gives no auth error,
// only timeouts, so mcpSvc.frame words any failing call as the deleted endpoint once the creds file is gone and set aside.
func frameSvc(t *testing.T, creds string) (*mcpSvc, *endpoint.Endpoint) {
	t.Helper()
	w := newWorld(t, "a")
	ep := w.openEP(t, "a")
	var st atomic.Pointer[sessionState]
	st.Store(&sessionState{ep: ep})
	cfg := config.Config{Creds: creds, Name: "a"}
	return &mcpSvc{state: &st, name: cfg.DisplayName(), callTimeout: time.Second, cfg: cfg}, ep
}

func TestFrameSaysDeletedForAFailedCallOnceTheCredsAreSetAside(t *testing.T) {
	creds := filepath.Join(t.TempDir(), "bob.creds")
	svc, ep := frameSvc(t, creds)
	timeout := context.DeadlineExceeded
	want := "error: " + ep.Address().String() + ": " + cmdcore.ReasonEndpointDeleted + "\nhint: mash doctor"

	// No marker: the error stays as it was.
	if err := svc.frame(ep, timeout); err != timeout {
		t.Errorf("no marker: %v", err)
	}
	if err := os.WriteFile(creds+".deleted", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Marker and creds gone: any failure is the deleted sentence, as a tool error.
	for _, e := range []error{timeout, errors.New("nats: timeout")} {
		if err := svc.frame(ep, e); err == nil || err.Error() != want {
			t.Errorf("%v: %v", e, err)
		}
	}
	// A usage error keeps its text, bare or wrapped; a Code 1 failure (plainFail's form of a timeout) is still deleted.
	for _, e := range []error{cliframe.Usagef("send id must be 1 to 128 characters"), cliframe.Usagef("send id \"authorization violation\" has a space"), fmt.Errorf("send: %w", cliframe.Usagef("bad"))} {
		if err := svc.frame(ep, e); err != e {
			t.Errorf("usage error %v: %v", e, err)
		}
	}
	if err := svc.frame(ep, cliframe.Failf("inbox: context deadline exceeded")); err == nil || err.Error() != want {
		t.Errorf("wrapped timeout: %v", err)
	}
	if err := svc.frame(ep, nil); err != nil {
		t.Errorf("success: %v", err)
	}
	// A reused path (creds present beside the marker): unchanged.
	if err := os.WriteFile(creds, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.frame(ep, timeout); err != timeout {
		t.Errorf("creds present: %v", err)
	}
}

// The real thing: mash serve over MCP stdio against a real NATS server. The endpoint is deleted as mashctl does it
// (the user key revoked at the server, the creds file renamed to .deleted); inbox, read and send then answer with the
// spec's sentence as a tool error. who is not an error: the answer is whatever the server still gives.
func TestServeToolsSayDeletedAfterTheEndpointIsDeletedDuringTheSession(t *testing.T) {
	w := newWorld(t, "a", "b")
	env := strictEnv(w, "a", map[string]string{"MASH_CALL_TIMEOUT": "1s"})
	creds := env["MASH_CREDS"]
	b, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(b)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}

	s := startServe(t, env)
	s.initialize(t)
	if n := len(s.tools(t)); n != 8 {
		t.Fatalf("serve exposes %d tools, want 8", n)
	}
	call := func(id int, tool, args string) (map[string]any, string) {
		r := s.call(t, id, tool, args)
		return r, resultText(r)
	}
	if r, txt := call(10, "inbox", `{}`); r["isError"] == true {
		t.Fatalf("inbox before the delete: %s", txt)
	}

	if r, txt := call(11, "who", `{}`); r["isError"] == true {
		t.Fatalf("who before the delete: %s", txt)
	}

	w.e.Revoke(t, uc.Subject)
	if err := os.Rename(creds, creds+".deleted"); err != nil {
		t.Fatal(err)
	}

	// who asks every endpoint (the answer is reused for 30 seconds), so one asked just before the delete still succeeds.
	r, txt := call(30, "who", `{}`)
	if r["isError"] == true || !strings.Contains(txt, tTo("a")) {
		t.Errorf("who just after the delete: isError=%v %q, want the directory", r["isError"], txt)
	}

	want := "error: " + tTo("a") + ": " + cmdcore.ReasonEndpointDeleted + "\nhint: mash doctor"
	for i, c := range []struct{ tool, args string }{
		{"inbox", `{}`},
		{"read", `{"number":1}`},
		{"send", `{"address":"` + tTo("b") + `","message":"hi"}`},
	} {
		r, txt := call(20+i, c.tool, c.args)
		if r["isError"] != true || txt != want {
			t.Errorf("%s: isError=%v %q, want %q", c.tool, r["isError"], txt, want)
		}
	}
	// A bad argument fails before any server call and keeps its own error, not the deleted wording.
	r, txt = call(40, "send", `{"address":"`+tTo("b")+`","message":"hi","id":"has space"}`)
	if r["isError"] != true || !strings.Contains(txt, "send id") || strings.Contains(txt, cmdcore.ReasonEndpointDeleted) {
		t.Errorf("send with a bad id: isError=%v %q, want its own error", r["isError"], txt)
	}
	// Auth words in a bad id must not make it a sign-in failure either.
	r, txt = call(41, "send", `{"address":"`+tTo("b")+`","message":"hi","id":"authorization violation"}`)
	if r["isError"] != true || !strings.Contains(txt, "send id") || strings.Contains(txt, cmdcore.ReasonEndpointDeleted) {
		t.Errorf("send with an auth-worded bad id: isError=%v %q, want its own error", r["isError"], txt)
	}
	// The revoked connection is still reconnecting, so serve takes about 10 seconds to release its lease and exit:
	// wait for it here, longer than the harness's own cleanup would.
	s.in.Close()
	select {
	case code := <-s.done:
		s.done <- code
	case <-time.After(30 * time.Second):
		t.Error("serve did not exit when stdin closed")
	}
}
