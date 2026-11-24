package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// Inside a session that has not connected yet, status says server_unreachable as the reason_code (#1438): when the
// server does not answer the check, and when the session's first open failed because the server did not answer. The
// usual text and the usual object (no reason_code) are unchanged.
func TestStatusToolBeforeFirstConnectionIsServerUnreachable(t *testing.T) {
	check := func(t *testing.T, s *session, id int, wantText string) {
		t.Helper()
		r := s.call(t, id, "status", `{"detail":true}`)
		sc, _ := r["structuredContent"].(map[string]any)
		if sc["ready"] != false || sc["online"] != false || sc["reason_code"] != "server_unreachable" {
			t.Errorf("detail: structured %v", sc)
		}
		if !strings.Contains(resultText(r), wantText) {
			t.Errorf("detail text %q lacks %q", resultText(r), wantText)
		}
		r = s.call(t, id+1, "status", `{}`)
		sc, _ = r["structuredContent"].(map[string]any)
		if _, has := sc["reason_code"]; has || sc["ready"] != false || !strings.Contains(resultText(r), "not ready: ") {
			t.Errorf("usual: structured %v, text %q", sc, resultText(r))
		}
	}
	t.Run("server down", func(t *testing.T) {
		setTiming(t, 20*time.Millisecond, 20*time.Second)
		transientMax = 50 * time.Millisecond
		w := newWorld(t, "a")
		s := startServe(t, strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"}))
		s.initialize(t)
		s.tools(t)
		check(t, s, 50, "not ready: the server did not answer: tls://127.0.0.1:1")
	})
	t.Run("first open failed", func(t *testing.T) {
		setTiming(t, 20*time.Millisecond, 20*time.Second)
		transientMax = 50 * time.Millisecond
		w := newWorld(t, "a")
		openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
			return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrNoServers)
		}
		s := startServe(t, w.env["a"])
		s.initialize(t)
		s.tools(t)
		s.await(t, 5*time.Second, isChannel) // the retrying notice: the first open has failed
		check(t, s, 60, "this session could not take this endpoint")
	})
}

// A session that could not take its endpoint answers by the cause: a server that did not answer is server_unreachable,
// refused credentials creds_refused, a deleted endpoint endpoint_deleted, anything else endpoint_not_taken.
func TestStatusSessionLeaseFailedCodeFollowsTheCause(t *testing.T) {
	w := newWorld(t, "a")
	for cause, want := range map[string]string{
		cmds.CauseServerSilent + ": tls://x:4222": "server_unreachable",
		cmds.CauseCredsRefused:                    "creds_refused",
		cmds.ReasonEndpointDeleted:                "endpoint_deleted",
		"":                                        "endpoint_not_taken",
		"the name is in use":                      "endpoint_not_taken",
	} {
		h := &healthSvc{cfg: w.cfgOf(t, "a"), lease: func() (string, string) { return cmds.LeaseFailed, cause }}
		if rc := sessionDetail(t, h); rc != want {
			t.Errorf("cause %q: reason_code %q, want %q", cause, rc, want)
		}
	}
}
