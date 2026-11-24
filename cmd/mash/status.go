package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/mcp"
)

// statusSource is what a serve's tools read, live, in every serve mode. state is nil when no endpoint start was
// attempted (no name, or a config error).
type statusSource struct {
	cfg        config.Config
	cfgErr     error
	pluginRoot string
	state      *atomic.Pointer[sessionState]
}

// mailHeld is whether the session holds mail because its mail folder cannot be written now: the spool it requires
// (MASH_SPOOL_REQUIRED) is failing, or a try at writing to the folder fails.
func (s *statusSource) mailHeld() bool {
	if s.state == nil {
		return false
	}
	st := s.state.Load()
	if st == nil || st.ep == nil {
		return false
	}
	return st.ep.StorageState() == endpoint.StorageFailing
}

// hookState is the session's hook state (endpoint.HookRan and the others): not_run until a spool is bound.
func (s *statusSource) hookState() string {
	if s.state != nil {
		if st := s.state.Load(); st != nil && st.ep != nil {
			return st.ep.HookState()
		}
	}
	return endpoint.HookNotRun
}

// lease is the session's own lease state for status and doctor: "" when it holds its endpoint (or has not tried
// yet, or hands over from an earlier serve of its own session, which is not a loss), cmds.LeaseOther while another
// session holds the endpoint, cmds.LeaseLost once a newer session took it, and cmds.LeaseFailed, with the cause, when
// serve could not take it (the server did not answer, or refused its credentials).
func (s *statusSource) lease() (state, cause string) {
	if s.state == nil {
		return "", ""
	}
	st := s.state.Load()
	switch {
	case st == nil:
	case st.lost != nil || st.superseded != nil:
		return cmds.LeaseLost, ""
	case st.waiting != nil && !st.waiting.SameSession:
		return cmds.LeaseOther, ""
	case st.retrying != nil:
		return cmds.LeaseFailed, leaseCause(s.cfg, st.retrying)
	case st.failed != nil:
		return cmds.LeaseFailed, leaseCause(s.cfg, st.failed)
	}
	return "", ""
}

// leaseCause words why a serve could not take its endpoint, as the sign-in row words a failed sign-in.
func leaseCause(cfg config.Config, err error) string {
	switch msg := err.Error(); {
	case isEndpointDeleted(err):
		return cmdcore.ReasonEndpointDeleted
	case endpoint.IsAuthFailure(err):
		return cmds.CauseCredsRefused
	case transient(err) || strings.Contains(msg, "no servers available"):
		return cmds.CauseServerSilent + ": " + config.RedactURL(cfg.URL)
	default:
		return redactText(strings.TrimPrefix(msg, "mash: "))
	}
}

// sessionSvc is the service the tools every serve mode has run against: the build (version) and the endpoint's checks
// (status and doctor), neither of which needs an endpoint start to have worked. The configuration is read from the
// source at each call, so a failure serve found after it began (a policy file it cannot read) is in the answer.
type sessionSvc struct {
	buildVersion
	src *statusSource
}

func (s sessionSvc) Health(ctx context.Context) (cmds.HealthReport, error) {
	rep, err := (&healthSvc{cfg: s.src.cfg, cfgErr: s.src.cfgErr, session: true, mailHeld: s.src.mailHeld, lease: s.src.lease, hooks: s.src.hookState}).Health(ctx)
	if err == nil {
		rep.Session = s.src.sessionNow(rep.Lease == "")
	}
	return rep, err
}

// ClaudeCheck is doctor's claude row; status does not ask for it.
func (s sessionSvc) ClaudeCheck(ctx context.Context) cmds.HealthCheck { return claudeCheck(ctx) }

// BinaryCheck is doctor's binary row; status does not ask for it.
func (s sessionSvc) BinaryCheck(ctx context.Context) cmds.HealthCheck { return binaryCheck(ctx) }

// sessionNow is what status --detail says about this session: whether it holds its endpoint, whether the agent host's hooks have delivered mail in it, and whether its mail folder works.
func (s *statusSource) sessionNow(noLease bool) *cmds.SessionState {
	st := &cmds.SessionState{HoldsEndpoint: false, Hooks: cmds.HooksNotRun, Storage: cmds.StorageOK}
	if s.state == nil {
		return st
	}
	cur := s.state.Load()
	if cur == nil || cur.ep == nil {
		return st
	}
	st.HoldsEndpoint = noLease
	st.Hooks = cur.ep.HookState()
	if s.mailHeld() {
		st.Storage = cmds.StorageFailing
	}
	return st
}

// infoTools are the tools every serve mode has, because they need no endpoint: version, status and doctor. They are
// the commands of the table, as MCP tools.
func infoTools(src *statusSource) []mcp.Tool {
	table := newCommands(func(context.Context) (*cmdcore.Env, error) {
		return &cmdcore.Env{Svc: sessionSvc{buildVersion: buildVersion{pluginRoot: src.pluginRoot}, src: src}}, nil
	})
	var out []mcp.Tool
	for _, n := range []string{"version", "status", "doctor"} {
		e, _ := table.Lookup(n)
		out = append(out, e.Tool(table.Env).MCP())
	}
	return out
}

var (
	// schemeUserinfo matches the userinfo of any scheme://user:pass@ or scheme://token@, up to the last @ of the
	// authority, so a password containing @ is covered too.
	schemeUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s,]*@`)
	// bareUserinfo matches user:pass@host with no scheme, as in a NATS server list.
	bareUserinfo = regexp.MustCompile(`(^|[\s,'"(])[^\s/,@:'"]+:[^\s/,'"]*@`)
	// jwtText matches a JWT (three base64url parts, the first starting with the encoded {"): a credential must never be
	// carried into an error, a recorded failure or a report.
	jwtText = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]*`)
	// seedText matches an nkey seed: S, a type letter (operator, account, user, server, cluster, private, curve), and base32.
	seedText = regexp.MustCompile(`S[OANCUPX][A-Z2-7]{50,}`)
)

// redactText replaces the credentials in every URL in s, however s parses: a url, a comma-separated server list, a
// malformed url, or an error message that embeds one; and any JWT or nkey seed in it.
func redactText(s string) string {
	s = schemeUserinfo.ReplaceAllString(s, "${1}REDACTED@")
	s = bareUserinfo.ReplaceAllString(s, "${1}REDACTED@")
	s = jwtText.ReplaceAllString(s, "REDACTED")
	return seedText.ReplaceAllString(s, "REDACTED")
}

// reportSettings prints once, at serve start, what the log of a session should say about its settings: the warning for a
// server address that carries a password or token. (The encryption-off line is printed by every command at start.)
func reportSettings(src *statusSource, stderr io.Writer) {
	if w := urlSecretWarning(src.cfg); w != "" {
		fmt.Fprintln(stderr, "mash: "+w)
	}
}

// pluginVersion is "version" from <root>/.claude-plugin/plugin.json, or "" when it cannot be read.
func pluginVersion(root string) string {
	if root == "" || strings.Contains(root, "${") {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Version)
}
