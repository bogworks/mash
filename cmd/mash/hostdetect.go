package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/spool"
)

// adapterFromOwner is the adapter a hook uses when MASH_HOST did not name one: a that was detected, unless the spool's
// owner record names another host (serve wrote it from the MCP client's name, which the hook cannot see). A record
// without a host, an unreadable one, or a spool that is not there leaves a as it is, so Claude Code's hook is untouched.
func adapterFromOwner(cfg config.Config, a host.Adapter) host.Adapter {
	dir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		return a
	}
	name := host.OwnerHost(spool.Spool{Dir: dir}.Owner())
	if name == "" || name == a.Name() {
		return a
	}
	if h, err := host.Select(name); err == nil && !host.IsHeadless(h) {
		return h
	}
	return a
}

// hostSettle resolves serve's host once the MCP client has said who it is. Before that serve holds det.Adapter, and
// everything it does early must be right whichever host turns out to be the client's:
//
//   - endpoint.SetSessionEnv: the Claude variable (CLAUDE_CODE_SESSION_ID) is simply unset under Codex and Pi, so
//     the lease's session id is "" either way, as it is for those hosts' own sources (Env "").
//   - noteWaiting records host.SessionID(adapter, env): the same variable, so "" under Codex either way.
//   - Claude's settings checks (the allowlist and environment warnings) would be wrong noise under another host, so
//     they wait for the answer when the detection is pending, and run at once otherwise, as before.
//   - The words in the inert and degraded instructions are said before any client is known; they name the detected
//     (assumed) host. Nothing is registered with the endpoint's name until the endpoint is up, which is after the answer.
//
// Everything that depends on the host's identity (SpoolsFromStart, the owner record, the waker, the service
// description prefix, the status words) is read after settle.
type hostSettle struct {
	det    host.Detection
	src    *statusSource
	stderr io.Writer
	once   sync.Once
	final  host.Detection
}

// settle runs once, after the client's initialize (the channel-ready gate): it settles a pending detection with the
// client's name, publishes the result to status, and does what waited for it. It returns the host serve uses from here.
func (h *hostSettle) settle(srv *mcp.Server) host.Adapter {
	h.once.Do(func() {
		h.final = h.det
		if h.det.Pending() {
			name, _ := srv.ClientInfo()
			h.final = h.det.WithClient(name)
		}
		d := h.final
		h.src.late.Store(&d)
		endpoint.SetServicePrefix(d.Adapter.Words().ServicePrefix)
		if h.det.Pending() {
			if d.Assumed() {
				fmt.Fprintln(h.stderr, d.AssumedMessage())
			}
			reportSettings(h.src, h.stderr)
		}
	})
	return h.final.Adapter
}

// trackThread starts what a host that learns its session id late needs: when the thread id arrives on a tools/call,
// the spool's owner record is rewritten with it (the hook's session_id is the same value), so rule 1 can match. tok is
// the record SetSpool wrote. It returns when ctx ends. The record is replaced only if it still is tok (another serve
// that took the spool is not overwritten), under the spool lock a hook holds across its owner check.
func trackThread(ctx context.Context, srv *mcp.Server, so host.SessionOwner, sp spool.Spool, tok string, stderr io.Writer) {
	seen := make(chan struct{}, 1)
	srv.OnThread(func(string) {
		select {
		case seen <- struct{}{}:
		default:
		}
	})
	// A call that arrived before the callback was set.
	if srv.ThreadID() != "" {
		select {
		case seen <- struct{}{}:
		default:
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-seen:
			}
			id := srv.ThreadID()
			next := so.OwnerTokenFor(os.Getenv, id)
			if next == tok {
				continue
			}
			unlock, err := sp.Lock()
			if err != nil {
				fmt.Fprintf(stderr, "mash: spool: recording the thread id: %v\n", err)
				continue
			}
			err = sp.ReplaceOwnerRecord(tok, next)
			unlock()
			if err != nil {
				fmt.Fprintf(stderr, "mash: spool: recording the thread id: %v\n", err)
				continue
			}
			tok = next
		}
	}()
}
