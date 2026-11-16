package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// Two Codex serves of one endpoint under one parent (threads of one shared app server) are not one session: the
// second waits and never takes the first's live lease, whether Codex was named by MASH_HOST or found only by the MCP
// client's name, which arrives after the first lease is opened (codex review H1, ADR 0143).
func TestCodexServesUnderOneParentDoNotTakeEachOthersLease(t *testing.T) {
	for _, how := range []string{"MASH_HOST", "client name only"} {
		t.Run(how, func(t *testing.T) {
			setTiming(t, 200*time.Millisecond, 20*time.Second)
			endpoint.LeaseRefresh = 100 * time.Millisecond
			t.Cleanup(func() { endpoint.SetNoSessionIdentity(false) })
			asSession(t) // the same parent and session id, as Claude Code's serves would have: takeover would apply
			t.Setenv("MASH_LAUNCH_TOKEN", "")
			w := newWorld(t, "a")
			if how == "MASH_HOST" {
				w.env["a"]["MASH_HOST"] = "codex"
			}
			kv := leaseKV(t, w)
			holder := func() map[string]any {
				e, err := kv.Get(context.Background(), "a")
				if err != nil {
					return nil
				}
				var m map[string]any
				_ = json.Unmarshal(e.Value(), &m)
				return m
			}
			init := func(s *session) {
				if how == "MASH_HOST" {
					s.initialize(t)
				} else {
					s.initializeAs(t, "codex-mcp-client")
				}
			}
			s1 := startServe(t, w.env["a"])
			init(s1)
			eventually(t, 10*time.Second, "first holds the lease", func() bool { return holder() != nil })
			time.Sleep(300 * time.Millisecond) // a refresh or two, and the client's settle
			first := holder()
			if first == nil || (first["ppid"] != nil && first["ppid"] != "") || (first["session"] != nil && first["session"] != "") {
				t.Fatalf("a Codex lease records a parent or session identity: %v", first)
			}
			s2 := startServe(t, w.env["a"])
			init(s2)
			time.Sleep(1500 * time.Millisecond) // several retries of the second
			if got := holder(); got == nil || got["instance_id"] != first["instance_id"] {
				t.Fatalf("the second Codex serve took the live lease: %v, was %v", got, first)
			}
			if strings.Contains(s2.stderr(), "took over") {
				t.Fatalf("second serve took over:\n%s", s2.stderr())
			}
			s1.in.Close() // the first ends and gives the lease back: the second gets it
			s1.exited(t, 5*time.Second)
			eventually(t, 10*time.Second, "second gets the lease after the first exits", func() bool {
				got := holder()
				return got != nil && got["instance_id"] != first["instance_id"]
			})
		})
	}
}
