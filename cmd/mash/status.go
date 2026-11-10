package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"

	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bogworks/mash/internal/claudesettings"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// statusSource is what the status tool reads, live, in every serve mode. state is nil when no endpoint start was
// attempted (no name, or a config error); srv is set once the MCP server exists.
type statusSource struct {
	cfg        config.Config
	cfgErr     error
	pluginRoot string
	state      *atomic.Pointer[sessionState]
	srv        *atomic.Pointer[mcp.Server]
	// owned is when this session's SetSpool bound the spool (zero until then): a hook run before it belongs to
	// another session, the previous lease holder's.
	owned atomic.Pointer[time.Time]
	// home, managedDir and getenv are where the Claude Code settings are read from; empty or nil means the user's
	// home, the OS default managed directory and the process environment. Tests set them.
	home, managedDir string
	getenv           func(string) string
	// cli is `mash status` from a shell: nothing is connected, so the live-only lines (connection, delivery) say
	// that a running session reports them instead of printing a state they cannot know.
	cli bool
}

// settings reads Claude Code's managed sources and the home they were read from. home is "" when it is unknown.
func (s *statusSource) settings() (home string, srcs []claudesettings.Source) {
	home = s.home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return "", nil
	}
	managed := s.managedDir
	if managed == "" {
		// MASH_MANAGED_SETTINGS_DIR replaces the OS location; a relative value is ignored.
		if d := s.env("MASH_MANAGED_SETTINGS_DIR"); filepath.IsAbs(d) {
			managed = d
		}
	}
	return home, claudesettings.Sources(home, runtime.GOOS, managed)
}

func (s *statusSource) env(k string) string {
	if s.getenv != nil {
		return s.getenv(k)
	}
	return os.Getenv(k)
}

// channelNote says what a count of channel notifications does and does not show: mash writes them, and cannot know
// whether Claude Code registered it as a channel. The allowlist line above is mash's own reading of the settings
// files; /status in Claude Code is the authority.
const channelNote = "(Claude Code delivers them only if this plugin is on its channel allowlist and the session was started with --channels; the allowlist line is mash's reading of the settings files, /status in Claude Code is the authority)"

// infoTools are the tools every serve mode has, because they need no endpoint: version and status.
func infoTools(src *statusSource) []mcp.Tool {
	return []mcp.Tool{versionTool(src), statusTool(src)}
}

func versionTool(src *statusSource) mcp.Tool {
	return mcp.Tool{Name: "version", Description: "Show which mash this is: the mash binary's version and commit, the plugin's version, and this endpoint's name and owner.",
		Schema: schema(map[string]any{}),
		Call: func(_ context.Context, _ json.RawMessage) (string, error) {
			out := versionText(src.cfg.Name, src.cfg.Owner, src.pluginRoot) + "\n" + clientLine(src.clientInfo())
			if l := driftLine(pluginVersion(src.pluginRoot), endpoint.CurrentVersion()); l != "" {
				out += "\n" + l
			}
			return out, nil
		}}
}

func statusTool(src *statusSource) mcp.Tool {
	return mcp.Tool{Name: "status", Description: "Say what works and what is missing for mash in this session: the endpoint name, config, creds, connection and delivery, each with its state and the next step. Works in every mode, even with no endpoint.",
		Schema: schema(map[string]any{}),
		Call: func(_ context.Context, _ json.RawMessage) (string, error) {
			return statusText(src), nil
		}}
}

// statusText is one line per item: "<item>: <STATE> - <detail>", with "Next: <step>" when the state is not OK.
// It never prints the contents of the creds file, and strips a password from the url.
func statusText(src *statusSource) string {
	cfg := src.cfg
	lines := []string{nameLine(cfg)}
	if l := identityLine(cfg); l != "" {
		lines = append(lines, l)
	}
	lines = append(lines, configLine(cfg, src.cfgErr), credsLine(cfg), tlsLine(cfg), connectionLine(src))
	if l := authLine(cfg); l != "" {
		lines = slices.Insert(lines, len(lines)-1, l)
	}
	lines = append(lines, clockLine(clockSkewOf(src)))
	lines = append(lines, allowlistLine(src))
	if l := environmentLine(src); l != "" {
		lines = append(lines, l)
	}
	lines = append(lines, deliveryLines(src)...)
	lines = append(lines, relayLine(src))
	lines = append(lines, "version: "+versionLine(endpoint.CurrentVersion(), endpoint.CurrentCommit(), nil))
	if !src.cli {
		lines = append(lines, clientStatusLine(src.clientInfo()))
	}
	if l := driftLine(pluginVersion(src.pluginRoot), endpoint.CurrentVersion()); l != "" {
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}

func nameLine(cfg config.Config) string {
	switch {
	case cfg.Name == "":
		return "name: MISSING - not set. Next: start Claude Code with MASH_NAME=<endpoint>"
	case cfg.AgentToken != "":
		if _, err := cfg.Identity(); err != nil {
			return fmt.Sprintf("name: FAILED - %s. Next: fix MASH_AGENT_TOKEN, owner or MASH_NAME", err)
		}
	case !config.ValidName(cfg.Name):
		return fmt.Sprintf("name: FAILED - %q is not a valid endpoint name. Next: start Claude Code with MASH_NAME=<endpoint>, using 1-48 lower-case letters, digits and single hyphens, starting with a letter", cfg.Name)
	}
	return "name: OK - " + cfg.Name
}

// identityLine names the identity form when a three-token identity is configured, and is empty otherwise.
func identityLine(cfg config.Config) string {
	if cfg.AgentToken == "" {
		return ""
	}
	id, err := cfg.Identity()
	if err != nil {
		return ""
	}
	return "identity: " + id.String() + " (three tokens)"
}

func configLine(cfg config.Config, cfgErr error) string {
	if cfgErr != nil && !errors.Is(cfgErr, config.ErrNoURL) && !errors.Is(cfgErr, config.ErrNoOwner) {
		// The error stopped loading before the environment applied, so an empty field is unresolved, not missing.
		line := fmt.Sprintf("config: FAILED - %s. Next: fix it in ~/.config/mash/config.json or the environment", redactText(cfgErr.Error()))
		var un []string
		if cfg.URL == "" {
			un = append(un, "url")
		}
		if cfg.Owner == "" {
			un = append(un, "owner")
		}
		if len(un) > 0 {
			line += "; " + strings.Join(un, " and ") + " could not be resolved because of this error"
		}
		return line
	}
	var missing []string
	if cfg.URL == "" {
		missing = append(missing, `url is not set. Next: set "url" in ~/.config/mash/config.json or the MASH_URL environment variable`)
	}
	if cfg.Owner == "" {
		missing = append(missing, `owner is not set. Next: set "owner" in ~/.config/mash/config.json or the MASH_OWNER environment variable`)
	}
	if len(missing) > 0 {
		return "config: MISSING - " + strings.Join(missing, "; ")
	}
	return fmt.Sprintf("config: OK - url %s, owner %s", redactURL(cfg.URL), cfg.Owner)
}

var (
	// schemeUserinfo matches the userinfo of any scheme://user:pass@ or scheme://token@, up to the last @ of the
	// authority, so a password containing @ is covered too.
	schemeUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s,]*@`)
	// bareUserinfo matches user:pass@host with no scheme, as in a NATS server list.
	bareUserinfo = regexp.MustCompile(`(^|[\s,'"(])[^\s/,@:'"]+:[^\s/,'"]*@`)
)

// redactText replaces the credentials in every URL in s, however s parses: a url, a comma-separated server list, a
// malformed url, or an error message that embeds one.
func redactText(s string) string {
	s = schemeUserinfo.ReplaceAllString(s, "${1}REDACTED@")
	return bareUserinfo.ReplaceAllString(s, "${1}REDACTED@")
}

func redactURL(s string) string { return redactText(s) }

// credsPath is the creds file serve will use: MASH_CREDS or "creds", else the endpoint's default; "" with no name.
func credsPath(cfg config.Config) string {
	switch {
	case cfg.Creds != "":
		return cfg.Creds
	case cfg.Name != "" && cfg.Dir != "":
		return cfg.Find(cfg.CredsName() + ".creds")
	}
	return ""
}

// credsLine names the path and whether the file exists and can be opened. It only opens the file, never reads it.
func credsLine(cfg config.Config) string {
	p := credsPath(cfg)
	if p == "" {
		return "creds: n/a - no endpoint name, so no creds file is chosen (the default is ~/.config/mash/<endpoint>.creds)"
	}
	next := fmt.Sprintf("Next: put the endpoint's creds file at %s, or set MASH_CREDS (or \"creds\" in config.json) to its path", p)
	fi, err := os.Stat(p)
	switch {
	case os.IsNotExist(err):
		return fmt.Sprintf("creds: MISSING - %s does not exist. %s", p, next)
	case err != nil:
		return fmt.Sprintf("creds: FAILED - %s is not readable (%s). %s", p, redactText(err.Error()), next)
	case fi.IsDir():
		return fmt.Sprintf("creds: FAILED - %s is a directory, not a creds file. %s", p, next)
	}
	f, err := os.Open(p)
	if err != nil {
		return fmt.Sprintf("creds: FAILED - %s is not readable (%s). Next: make it readable by this user", p, redactText(err.Error()))
	}
	f.Close()
	return fmt.Sprintf("creds: OK - %s exists and is readable", p)
}

const cliLiveNote = "not checked from the CLI (a running session reports it)"

// tlsLine warns when the url is plain nats:// (or ws://) to a non-loopback host.
func tlsLine(cfg config.Config) string {
	switch w := config.PlainTextWarning(cfg.URL); {
	case cfg.URL == "":
		return "tls: n/a - no url"
	case w != "":
		return fmt.Sprintf("tls: WARN - %s %s", redactURL(cfg.URL), w)
	case strings.Contains(cfg.URL, "tls://") || strings.Contains(cfg.URL, "wss://"):
		return "tls: OK - tls:// connection"
	}
	return "tls: n/a - loopback"
}

// authLine warns when a url in the (comma-separated) server list carries userinfo, a password or a token, which mash
// never uses: it authenticates with operator-mode JWT credentials only. "" when no url does. The secret is not printed.
func authLine(cfg config.Config) string {
	for _, e := range strings.Split(cfg.URL, ",") {
		_, rest, ok := strings.Cut(strings.TrimSpace(e), "://")
		if !ok {
			rest = strings.TrimSpace(e)
		}
		rest, _, _ = strings.Cut(rest, "/")
		if strings.Contains(rest, "@") {
			return "auth: WARN - the url carries a password or token, but mash authenticates with operator-mode JWT credentials only (creds file). Next: create a user with nsc or Synadia Cloud and set creds"
		}
	}
	return ""
}

func connectionLine(src *statusSource) string {
	cfg := src.cfg
	switch {
	case src.cli:
		return "connection: " + cliLiveNote
	case src.cfgErr != nil:
		return "connection: FAILED - not started, because of the config problem above"
	case cfg.Name == "":
		return "connection: n/a - no endpoint name, so there is nothing to connect"
	case src.state == nil:
		return "connection: STARTING"
	}
	st := src.state.Load()
	switch {
	case st == nil:
		return "connection: STARTING - mash is opening endpoint " + cfg.Name
	case st.ep != nil:
		return fmt.Sprintf("connection: OK - endpoint %s is running", cfg.Name)
	case st.failed != nil:
		return fmt.Sprintf("connection: FAILED - %s. Next: fix the cause (see the creds and config lines) and restart Claude Code", redactText(st.failed.Error()))
	case st.retrying != nil:
		return fmt.Sprintf("connection: RETRYING - could not reach the server, last error: %s. mash retries on its own", redactText(st.retrying.Error()))
	case st.waiting != nil:
		return fmt.Sprintf("connection: WAITING - the lease for endpoint %s is held by another session (host %s, pid %s, since %s). mash retries on its own. Next: end that session, or start this one with a different MASH_NAME",
			cfg.Name, redactText(st.waiting.Host), redactText(st.waiting.PID), redactText(st.waiting.Since))
	}
	return "connection: STARTING - mash is opening endpoint " + cfg.Name
}

// clockWarn is the skew past which status warns, well inside sap.LiveSkew so there is time to fix it.
const clockWarn = 10 * time.Second

// clockSkewOf is the endpoint's last measured skew from the server; ok is false with no endpoint or no reading yet.
func clockSkewOf(src *statusSource) (time.Duration, bool) {
	if src.state == nil {
		return 0, false
	}
	if st := src.state.Load(); st != nil && st.ep != nil {
		return st.ep.ClockSkew()
	}
	return 0, false
}

// clockLine is the clock line for a measured skew (server minus local); ok is false when there is none.
func clockLine(skew time.Duration, ok bool) string {
	if !ok {
		return "clock: n/a - not connected"
	}
	n := int64((skew.Abs() + time.Second/2) / time.Second)
	if skew.Abs() > clockWarn {
		return fmt.Sprintf("clock: WARN - %ds from the server (limit %d s: beyond it peers look offline and mail is [stale]). Next: sync this machine's clock (NTP)", n, int(sap.LiveSkew/time.Second))
	}
	return fmt.Sprintf("clock: OK - within %ds of the server", n)
}

// reportSettings prints once, at serve start, the allowlist line when it is not OK and the environment line when it
// applies, so a log read later shows why the channel or the environment is missing.
func reportSettings(src *statusSource, stderr io.Writer) {
	if l := allowlistLine(src); !strings.HasPrefix(l, allowlistPrefix+"OK") {
		fmt.Fprintln(stderr, "mash: "+l)
	}
	if l := environmentLine(src); l != "" {
		fmt.Fprintln(stderr, "mash: "+l)
	}
	if l := tlsLine(src.cfg); strings.HasPrefix(l, "tls: WARN") {
		fmt.Fprintln(stderr, "mash: "+l)
	}
	if l := authLine(src.cfg); l != "" {
		fmt.Fprintln(stderr, "mash: "+l)
	}
}

const allowlistPrefix = "channel allowlist: "

// allowlistLine says whether the managed settings Claude Code applies list mash as a channel plugin. It reads the
// files only (read-only, key names and booleans), never states more than they show, and says UNKNOWN when it cannot
// read them. It cannot see an MDM policy, which ranks above the files.
func allowlistLine(src *statusSource) string {
	home, srcs := src.settings()
	if home == "" {
		return allowlistPrefix + "UNKNOWN - the home directory is not known. Next: confirm with /status in Claude Code"
	}
	for _, s := range srcs {
		if s.Err != nil {
			return allowlistPrefix + "UNKNOWN - " + redactText(s.Err.Error()) + ". Next: fix or remove that file, and confirm with /status in Claude Code"
		}
	}
	win, shadowed := claudesettings.Effective(srcs)
	if win == nil {
		return allowlistPrefix + "MISSING - no managed source lists mash@mash. Next: see docs/install.md section 3"
	}
	if win.AllowsMash == nil || !*win.AllowsMash {
		for _, s := range shadowed {
			if s.AllowsMash != nil && *s.AllowsMash {
				return fmt.Sprintf(allowlistPrefix+"SHADOWED - %s (%s) lists mash, but %s (%s) take precedence and do not. Next: ask an organisation Owner to add {\"marketplace\":\"mash\",\"plugin\":\"mash\"} to allowedChannelPlugins in the Claude.ai admin console (and enable Channels); confirm with /status",
					s.Name, s.Path, win.Name, win.Path)
			}
		}
		return allowlistPrefix + "MISSING - no managed source lists mash@mash. Next: see docs/install.md section 3"
	}
	if win.ChannelsEnabled == nil || !*win.ChannelsEnabled {
		how := "sets channelsEnabled false"
		if win.ChannelsEnabled == nil {
			how = "does not set channelsEnabled to true (Team and Enterprise need it)"
		}
		return fmt.Sprintf(allowlistPrefix+"OFF - %s %s. Next: set channelsEnabled to true there (see docs/install.md section 3); confirm with /status", win.Name, how)
	}
	return fmt.Sprintf(allowlistPrefix+"OK - %s enables channels and lists mash@mash", win.Name)
}

// environmentLine is the environment line when Claude Code limits an MCP server's environment, else "".
func environmentLine(src *statusSource) string {
	home, srcs := src.settings()
	if home == "" {
		return ""
	}
	if on, _ := claudesettings.StrippedEnv(home, srcs, src.env); on {
		return "environment: STRIPPED - Claude Code passes MCP servers only a baseline environment (" + claudesettings.MCPAllowlistEnv + "). Next: put url and owner in ~/.config/mash/config.json; the endpoint name is per launch (ADR 0012) and cannot reach serve in this mode, so mash is unsupported here until the organisation drops the setting"
	}
	return ""
}

func deliveryLines(src *statusSource) []string {
	cfg := src.cfg
	if src.cli {
		return []string{"delivery: " + cliLiveNote + ": hook state and channel notification counts exist only inside a running session"}
	}
	var out []string
	switch {
	case cfg.Name == "" || cfg.Owner == "":
		out = append(out, "delivery: hooks n/a - no endpoint")
	default:
		sdir, derr := endpoint.SpoolDir(cfg)
		if derr != nil {
			out = append(out, "delivery: hooks n/a - no valid endpoint identity")
			break
		}
		sp := spool.Spool{Dir: sdir}
		own := src.owned.Load()
		if own == nil {
			out = append(out, "delivery: hooks n/a - this session does not hold the lease yet")
		} else if at, ok := sp.LastHook(); ok && !at.Before(*own) {
			out = append(out, fmt.Sprintf("delivery: hooks have run in this session (last at %s); the last wake started a turn; mail also arrives with tool results", at.Format(time.RFC3339)))
		} else if st, wakes := hooklessOf(src); st == endpoint.HooklessProven {
			out = append(out, fmt.Sprintf("delivery: DEGRADED - no mash hook has run since %s although this session has made tool calls. Likely cause: %s. Mail now: read each message with the read tool. Next: %s",
				own.Format(time.RFC3339), hooklessCause(sp, *own), hooklessFix(sp, *own)))
		} else if rat, why, ok := sp.Refused(); ok && rat.After(*own) {
			out = append(out, fmt.Sprintf("delivery: REFUSED - mash's hook runs in this session but is not recognised as this session's (%s, last at %s). Next: start Claude Code with MASH_LAUNCH_TOKEN=$(uuidgen) claude ... (a fresh value per launch; one reused across launches defeats the check), and report your setup (see docs/operations.md)",
				redactText(why), rat.Format(time.RFC3339)))
		} else if st == endpoint.HooklessSuspected {
			out = append(out, fmt.Sprintf("delivery: SUSPECTED - the last %d wakes started no turn (no hook since %s); channel writes are not counted as delivery; re-waking stopped", wakes, own.Format(time.RFC3339)))
		} else if wakes > 0 {
			out = append(out, fmt.Sprintf("delivery: hooks have not run yet in this session; the last %d wake(s) started no turn", wakes))
		} else {
			out = append(out, "delivery: hooks have not run yet in this session (they run with the first prompt or tool call)")
		}
	}
	n, last := int64(0), time.Time{}
	if srv := src.srv.Load(); srv != nil {
		n, last = srv.Notified()
	}
	line := fmt.Sprintf("delivery: channel notifications sent: %d %s", n, channelNote)
	if n > 0 {
		line += fmt.Sprintf(", last at %s", last.Format(time.RFC3339))
	}
	return append(out, line)
}

// readRelayCounters reads the counters file under the endpoint's state location.
func readRelayCounters(cfg config.Config) (endpoint.RelayCounters, bool) {
	o, n, err := endpoint.StateLocation(cfg)
	if err != nil {
		return endpoint.RelayCounters{}, false
	}
	return endpoint.ReadRelayCounters(cfg.StateDir, o, n)
}

// relayLine shows the relay-trace counters (ADR 0064). Inside serve they are the running endpoint's; from the shell
// (another process) they are what the serve of this endpoint wrote to its state directory, with the start time of that
// run, since the shell cannot tell whether it is still running. Missing or unreadable: "none recorded".
func relayLine(src *statusSource) string {
	const none = "relay trace: none recorded"
	cfg := src.cfg
	var c endpoint.RelayCounters
	since := ""
	if src.cli {
		var ok bool
		if c, ok = readRelayCounters(cfg); !ok {
			return none
		}
		since = fmt.Sprintf(" (since %s)", c.Started.UTC().Format(time.RFC3339))
	} else {
		if src.state == nil {
			return none
		}
		st := src.state.Load()
		if st == nil || st.ep == nil {
			return none
		}
		c = st.ep.RelayCounters()
	}
	return fmt.Sprintf("relay trace: inbound with via %d, sends within %ds of another sender's mail %d, declared relays %d%s",
		c.InboundVia, c.WindowS, c.RelayCandidates, c.RelayedDeclared, since)
}

// hooklessOf is serve's verdict on whether no mash hook runs in this session: none when there is no endpoint yet.
func hooklessOf(src *statusSource) (endpoint.Hookless, int) {
	if src.state == nil {
		return endpoint.HooklessNone, 0
	}
	if st := src.state.Load(); st != nil && st.ep != nil {
		return st.ep.Hookless()
	}
	return endpoint.HooklessNone, 0
}

// hooklessCause is the likeliest reason no hook has run. There is no reader of Claude Code's settings files in mash,
// so it comes from what the hooks left behind: a refusal, else the causes that leave nothing.
func hooklessCause(sp spool.Spool, own time.Time) string {
	if rat, why, ok := sp.Refused(); ok && rat.After(own) {
		return fmt.Sprintf("mash's hook runs but is not recognised as this session's (%s, last at %s)", redactText(why), rat.Format(time.RFC3339))
	}
	return "hooks are disabled (disableAllHooks, or allowManagedHooksOnly without mash force-enabled), or not loaded, or `mash` is not on the hook's PATH (see hook.log)"
}

func hooklessFix(sp spool.Spool, own time.Time) string {
	if rat, _, ok := sp.Refused(); ok && rat.After(own) {
		return "start Claude Code with MASH_LAUNCH_TOKEN=<any unique value>, and report your setup (see docs/operations.md)"
	}
	return "remove disableAllHooks from the settings; under allowManagedHooksOnly an administrator force-enables mash@mash in managed enabledPlugins; check hook.log (see docs/operations.md)"
}

// statusCmd is `mash status`: the status tool's lines for this environment, read from the shell, before Claude Code
// is launched. It connects to nothing and takes no lease. It is a report, so a missing name or config is output, not
// a failure.
func statusCmd(cfg config.Config, cfgErr error, env func(string) string, stdout io.Writer) int {
	src := &statusSource{cfg: cfg, cfgErr: cfgErr, cli: true, home: env("HOME"), getenv: env}
	fmt.Fprintln(stdout, statusText(src))
	return 0
}

// clientInfo is what the MCP client sent in initialize; empty before one is seen or without a server.
func (s *statusSource) clientInfo() (name, version string) {
	if s.srv == nil {
		return "", ""
	}
	if srv := s.srv.Load(); srv != nil {
		return srv.ClientInfo()
	}
	return "", ""
}

// claudeCodeClient is the clientInfo name Claude Code sends.
const claudeCodeClient = "claude-code"

// clientLine is "client: <name> <version>" as the client sent it, with Claude Code's name written out.
func clientLine(name, version string) string {
	if name == "" && version == "" {
		return "client: unknown (no initialize seen)"
	}
	shown := name
	if name == claudeCodeClient {
		shown = "Claude Code"
	}
	return strings.TrimSpace("client: " + shown + " " + version)
}

// clientStatusLine is clientLine, or a warning when Claude Code is older than host.MinClaudeCode. Other clients, and
// versions that are not semver, get no warning: mash does not guess their version schemes.
func clientStatusLine(name, version string) string {
	if name != claudeCodeClient {
		return clientLine(name, version)
	}
	v, ok := parseSemver(version)
	min, _ := parseSemver(host.MinClaudeCode)
	if !ok || !semverLess(v, min) {
		return clientLine(name, version)
	}
	return fmt.Sprintf("client: WARN - Claude Code %s is older than %s, the oldest version mash has a passing live receipt on. mash does not refuse it. Next: update Claude Code", version, host.MinClaudeCode)
}

// parseSemver reads MAJOR.MINOR.PATCH, with an optional leading v and any -prerelease or +build suffix ignored.
func parseSemver(s string) (v [3]int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func semverLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// driftLine warns when the plugin's and the binary's versions differ in major or minor. It is empty when either is
// unknown or not semver (a dev build), and when they agree.
func driftLine(pluginVer, binaryVer string) string {
	p, pok := parseSemver(pluginVer)
	b, bok := parseSemver(binaryVer)
	if !pok || !bok || (p[0] == b[0] && p[1] == b[1]) {
		return ""
	}
	return fmt.Sprintf("plugin: WARN - plugin %s and binary %s differ in major or minor. Next: update both to the same release (claude plugin update mash@mash and the mash binary)", pluginVer, binaryVer)
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
