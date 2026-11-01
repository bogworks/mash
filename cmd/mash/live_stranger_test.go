package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// The live tier of the "setups unlike ours" matrix (opt-in: MASH_LIVE_CLAUDE=1; it spends a few short haiku turns).
// A real Claude Code, used as found, runs the plugin installed from this checkout (a local marketplace, into an
// isolated config) against a scratch endpoint pair on the in-process NATS server. Scenarios:
//
//	L1  plain claude, no managed settings            (S1)
//	L2  team policy with server-managed settings     (S2)  never runs here: needs an organisation account
//	L3  IDE-like minimal environment                 (S3)
//	L4  hooks disabled                               (S4)  needs the DEGRADED status line (gate 3, #200)
//
// Operator rules this file keeps:
//   - Every scenario has its own temp HOME and, except L3 (which models a bare launch), its own CLAUDE_CONFIG_DIR (or the
//     in-place dir, below), and
//     DISABLE_AUTOUPDATER=1.
//   - The child's environment is built from scratch (lsEnv): PATH, TERM, LANG and what the scenario sets. Nothing from
//     the caller's MASH_*, ANTHROPIC_*, CLAUDE_* or key-like variables is copied.
//   - No key is ever in the child's environment (the model can run `env`). Authentication is a credentials file copied
//     from the path in MASH_LIVE_CLAUDE_CREDS to <config dir>/.credentials.json at 0600 and removed in t.Cleanup.
//     The mash identity (MASH_NAME, MASH_URL, MASH_OWNER, MASH_SPOOL_FROM_START) and MASH_CREDS, a path to the scratch
//     endpoint's creds file (not key material), are in the child's environment so they reach serve and the hooks:
//     Claude Code hands its environment to both. Whether it does, in every setup, is part of what these runs show.
//   - Bash is limited to `sleep`, the model is haiku, a scenario is capped at 5 minutes and 8 turns.
//   - The receipt directory (MASH_LIVE_RECEIPT_DIR, default a fresh temp dir) gets timeline.log and RECEIPT.md only:
//     extracted facts, scrubbed. Raw streams and transcripts carry the session's whole system context and are never
//     written anywhere.
//
// Run: MASH_LIVE_CLAUDE=1 MASH_LIVE_CLAUDE_CREDS=<path to a Claude Code .credentials.json> \
//
//	MASH_LIVE_RECEIPT_DIR=<dir> go test ./cmd/mash -run LiveStranger -count=1 -v -timeout 40m
//
// In-place config mode: with MASH_LIVE_CLAUDE_CONFIG_DIR set to an existing directory that already holds a logged-in
// Claude Code config (a dedicated seat), no credentials are copied and MASH_LIVE_CLAUDE_CREDS is not needed. Every
// scenario, L3 included, runs with CLAUDE_CONFIG_DIR set to that directory (a bare launch would use HOME/.claude, which
// has no login). The plugin install writes into it at user scope, so each scenario's cleanup runs
// `claude plugin uninstall mash@mash` and `claude plugin marketplace remove mash` (failures are logged, not fatal); nothing
// else in the directory is written or deleted, and its path is scrubbed from the receipt. The directory must have a
// single consumer while the run lasts, and the tests must not run with -parallel (a second scenario in it is a t.Fatal).
//
// Manual add-ons (not scripted, listed in the receipt): a VS Code extension session and a desktop-app session
// (`ps -o pid,ppid,comm` chains for serve and a hook; whether the hook drained), and the hook's CLAUDE_CODE_SESSION_ID
// before and after /clear and after --resume.

const (
	lsMaxTurns   = "8"
	lsScenarioTO = 5 * time.Minute
	lsStatusTool = "mcp__plugin_mash_mash__status"
	lsWhoTool    = "mcp__plugin_mash_mash__who"
)

var lsScenarios = []struct{ id, title string }{
	{"L1", "plain claude, no managed settings"},
	{"L2", "team policy: server-managed settings without mash"},
	{"L3", "minimal environment (IDE-like launch)"},
	{"L4", "hooks disabled"},
	{"D1", "two live processes on one session id"},
	{"D2", "two live processes on one session id, distinct launch tokens"},
}

// ---- recording: timeline.log and RECEIPT.md ----

type lsRow struct{ id, check, result, detail string }

type lsRecorder struct {
	mu      sync.Mutex
	dir     string
	rows    []lsRow
	secrets []string // literal strings that must never reach a file
	facts   map[string]string
	base    *lsClaudeID
}

var ls = &lsRecorder{facts: map[string]string{}}

var (
	lsSeedRe = regexp.MustCompile(`\bS[ABNUX][A-Z2-7]{40,}\b`)
	lsJWTRe  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.?[A-Za-z0-9_-]*`)
	lsKeyRe  = regexp.MustCompile(`\b(?:sk|key|tok|pat)[-_][A-Za-z0-9_-]{12,}`)
	lsLongRe = regexp.MustCompile(`[A-Za-z0-9_-]{40,}`)
)

// scrub removes everything that looks like key material, the creds paths and the temp roots, and clips the result.
func (r *lsRecorder) scrub(s string, clip int) string {
	r.mu.Lock()
	secrets := append([]string(nil), r.secrets...)
	r.mu.Unlock()
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, x := range secrets {
		if len(x) >= 6 {
			s = strings.ReplaceAll(s, x, "<scrubbed>")
		}
	}
	s = lsSeedRe.ReplaceAllString(s, "<seed>")
	s = lsJWTRe.ReplaceAllString(s, "<jwt>")
	s = lsKeyRe.ReplaceAllString(s, "<key>")
	s = lsLongRe.ReplaceAllString(s, "<long-token>")
	if h := os.Getenv("HOME"); len(h) > 1 {
		s = strings.ReplaceAll(s, h, "~")
	}
	s = strings.Join(strings.Fields(s), " ")
	if clip > 0 && len(s) > clip {
		s = s[:clip] + "..."
	}
	return s
}

func (r *lsRecorder) addSecret(x ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secrets = append(r.secrets, x...)
}

// addFileSecrets registers the lines and JSON string values of a credentials file as things to scrub (kept in memory
// only).
func (r *lsRecorder) addFileSecrets(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if len(x) >= 12 {
				r.addSecret(x)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	var v any
	if json.Unmarshal(b, &v) == nil {
		walk(v)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); len(line) >= 20 {
			r.addSecret(line)
		}
	}
}

// receiptDir is MASH_LIVE_RECEIPT_DIR, else one fresh temp dir for the whole run (t.TempDir would vanish with the
// first test and take the later scenarios' rows with it).
func (r *lsRecorder) receiptDir(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir != "" {
		return r.dir
	}
	d := os.Getenv("MASH_LIVE_RECEIPT_DIR")
	if d == "" {
		var err error
		if d, err = os.MkdirTemp("", "mash-live-receipt-"); err != nil {
			t.Fatal(err)
		}
	} else if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	r.dir = d
	t.Logf("receipt directory: %s", d)
	return d
}

func (r *lsRecorder) log(t *testing.T, id, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf("%s [%s] %s", time.Now().UTC().Format("15:04:05.000"), id, r.scrub(fmt.Sprintf(format, args...), 400))
	t.Log(line)
	f, err := os.OpenFile(filepath.Join(r.receiptDir(t), "timeline.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// check records one row and fails the test when it does not hold.
func (r *lsRecorder) check(t *testing.T, id, name string, ok bool, detail string, args ...any) {
	t.Helper()
	res := "PASS"
	if !ok {
		res = "FAIL"
	}
	r.row(t, id, name, res, detail, args...)
	if !ok {
		t.Errorf("%s: %s: FAIL: %s", id, name, r.scrub(fmt.Sprintf(detail, args...), 300))
	}
}

// info records a fact that is observed, not asserted.
func (r *lsRecorder) info(t *testing.T, id, name, detail string, args ...any) {
	t.Helper()
	r.row(t, id, name, "RECORDED", detail, args...)
}

func (r *lsRecorder) row(t *testing.T, id, name, res, detail string, args ...any) {
	t.Helper()
	d := r.scrub(fmt.Sprintf(detail, args...), 300)
	r.mu.Lock()
	r.rows = append(r.rows, lsRow{id, name, res, d})
	r.mu.Unlock()
	r.log(t, id, "%s: %s (%s)", res, name, d)
}

// flush rewrites RECEIPT.md from the rows so far.
func (r *lsRecorder) flush(t *testing.T) {
	t.Helper()
	dir := r.receiptDir(t)
	r.mu.Lock()
	rows := append([]lsRow(nil), r.rows...)
	facts := r.facts
	r.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "# Stranger-matrix live tier: %s, mash\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "mash commit under test: %s. mash was built from this checkout (`go build ./cmd/mash`) and put first on the\n", facts["commit"])
	fmt.Fprintf(&b, "child's PATH. Claude Code: %s, resolved to %s (sha256 %s), unchanged across the run. Model haiku, at most\n", facts["claude_version"], facts["claude_path"], facts["claude_sha"])
	fmt.Fprintf(&b, "%s turns and 5 minutes per scenario, Bash limited to `sleep`. Each scenario had its own temp HOME and config\n", lsMaxTurns)
	b.WriteString("directory, a child environment built from scratch, and credentials copied in from a file and deleted afterwards.\n")
	b.WriteString("Only timeline.log (the harness clock) and this file are kept; raw streams and transcripts are never written.\n\n")
	b.WriteString("| Scenario | Check | Result | Detail |\n|---|---|---|---|\n")
	seen := map[string]bool{}
	for _, sc := range lsScenarios {
		for _, row := range rows {
			if row.id == sc.id {
				seen[sc.id] = true
				fmt.Fprintf(&b, "| %s %s | %s | %s | %s |\n", sc.id, sc.title, esc(row.check), row.result, esc(row.detail))
			}
		}
		if !seen[sc.id] {
			fmt.Fprintf(&b, "| %s %s | (not run in this invocation) | | |\n", sc.id, sc.title)
		}
	}
	b.WriteString("\nResult: fill in the `go test` result line here (`--- PASS: TestLiveStranger... (N s)` per scenario).\n")
	b.WriteString("\nManual add-ons for the same receipt (not scripted):\n")
	b.WriteString("- one VS Code extension session and one desktop-app session: `ps -o pid,ppid,comm` chains for serve and a hook, and whether the hook drained;\n")
	b.WriteString("- the hook's CLAUDE_CODE_SESSION_ID before and after `/clear` and after `--resume`;\n")
	b.WriteString("- L2 on a Claude.ai Team or Enterprise organisation: `/status` (Setting sources), `claude doctor`, and mash's `channel allowlist: SHADOWED` line.\n")
	if err := os.WriteFile(filepath.Join(dir, "RECEIPT.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// ---- the installed Claude Code must not change during the run ----

type lsClaudeID struct{ version, resolved, sha string }

func (c *lsClaudeID) String() string {
	return fmt.Sprintf("claude %s at %s sha256 %.12s", c.version, c.resolved, c.sha)
}

func lsIdentify(claude string) (*lsClaudeID, error) {
	out, err := exec.Command(claude, "--version").Output() // the only thing run here that spends nothing
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(claude) // readlink -f
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return &lsClaudeID{strings.TrimSpace(string(out)), resolved, hex.EncodeToString(sum[:])}, nil
}

// ---- a scenario's isolated host ----

type lsScenario struct {
	t       *testing.T
	id      string
	claude  string // as found on PATH
	root    string
	home    string
	cfg     string // the config dir: <home>/.claude unless CLAUDE_CONFIG_DIR is set
	cfgEnv  bool   // CLAUDE_CONFIG_DIR is set in the child's environment
	state   string
	work    string
	mashDir string
	repo    string
	inPlace bool // the config dir is the operator's own (MASH_LIVE_CLAUDE_CONFIG_DIR)
}

// lsMCPFailureKey is the MCP server name Claude Code caches a failed connect under.
const lsMCPFailureKey = "plugin:mash:mash"

// clearMashMCPFailureCache deletes only the plugin:mash:mash key from <cfgDir>/mcp-needs-auth-cache.json. Claude Code
// caches a failed connect there for about 15 minutes per config dir, across sessions. Other keys and the file's mode
// are kept, and the write is atomic (temp file and rename in the same dir). A missing file or invalid JSON is left alone.
func clearMashMCPFailureCache(t *testing.T, cfgDir string) {
	t.Helper()
	clearMCPFailureKey(cfgDir, func(format string, args ...any) { t.Helper(); ls.log(t, "-", format, args...) })
}

func clearMCPFailureKey(cfgDir string, logf func(string, ...any)) {
	const note = "cleared the cached MCP failure for plugin:mash:mash (Claude Code caches it for ~15 minutes per config dir)"
	p := filepath.Join(cfgDir, "mcp-needs-auth-cache.json")
	fi, err := os.Stat(p)
	if err != nil {
		logf("%s: nothing to do (%v)", note, err)
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		logf("%s: not done, read: %v", note, err)
		return
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		logf("%s: not done, not a JSON object (%v)", note, err)
		return
	}
	if _, ok := m[lsMCPFailureKey]; !ok {
		logf("%s: no such key cached", note)
		return
	}
	delete(m, lsMCPFailureKey)
	out, err := json.Marshal(m)
	if err != nil {
		logf("%s: not done, marshal: %v", note, err)
		return
	}
	tmp, err := os.CreateTemp(cfgDir, ".mcp-needs-auth-cache-*.tmp")
	if err != nil {
		logf("%s: not done, temp file: %v", note, err)
		return
	}
	_, werr := tmp.Write(out)
	cerr := tmp.Close()
	merr := os.Chmod(tmp.Name(), fi.Mode().Perm())
	if werr != nil || cerr != nil || merr != nil || os.Rename(tmp.Name(), p) != nil {
		os.Remove(tmp.Name())
		logf("%s: not done, write failed (%v %v %v)", note, werr, cerr, merr)
		return
	}
	logf("%s", note)
}

func lsRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// lsCfgInUse guards the in-place config dir: one scenario at a time.
var lsCfgInUse atomic.Bool

// lsStart gates and sets up one scenario: skips unless opted in with credentials (or an in-place config dir), records and guards the installed
// Claude Code, and builds the isolated HOME, config dir, credentials copy and working directory.
func lsStart(t *testing.T, id string, cfgEnv bool) *lsScenario {
	t.Helper()
	if os.Getenv("MASH_LIVE_CLAUDE") != "1" {
		t.Skip("set MASH_LIVE_CLAUDE=1 to run against the installed Claude Code")
	}
	inPlace := os.Getenv("MASH_LIVE_CLAUDE_CONFIG_DIR")
	credsSrc := os.Getenv("MASH_LIVE_CLAUDE_CREDS")
	if inPlace == "" && credsSrc == "" {
		t.Skip("set MASH_LIVE_CLAUDE_CREDS to the path of a Claude Code .credentials.json to authenticate the isolated config")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("no claude on PATH")
	}
	if inPlace != "" {
		abs, err := filepath.Abs(inPlace)
		if err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			t.Fatalf("MASH_LIVE_CLAUDE_CONFIG_DIR is not an existing directory")
		}
		inPlace = abs
		ls.addSecret(inPlace)
		if !lsCfgInUse.CompareAndSwap(false, true) {
			t.Fatalf("MASH_LIVE_CLAUDE_CONFIG_DIR is already in use by another scenario (no -parallel; one consumer only)")
		}
		t.Cleanup(func() { lsCfgInUse.Store(false) })
	} else {
		ls.addSecret(credsSrc)
		ls.addFileSecrets(credsSrc)
	}
	t.Cleanup(func() { ls.flush(t) })

	if inPlace != "" {
		cfgEnv = true // a bare launch (L3) would use HOME/.claude, which has no login
	}
	sc := &lsScenario{t: t, id: id, claude: claude, cfgEnv: cfgEnv, repo: lsRepoRoot(t), inPlace: inPlace != ""}
	// before and after the whole run: the first scenario sets the baseline, every scenario's end compares to it
	now, err := lsIdentify(claude)
	if err != nil {
		t.Fatalf("identify claude: %v", err)
	}
	ls.mu.Lock()
	if ls.base == nil {
		ls.base = now
		ls.facts["claude_version"], ls.facts["claude_path"], ls.facts["claude_sha"] = now.version, now.resolved, fmt.Sprintf("%.12s", now.sha)
		if out, err := exec.Command("git", "-C", sc.repo, "rev-parse", "HEAD").Output(); err == nil {
			c := strings.TrimSpace(string(out))
			if st, _ := exec.Command("git", "-C", sc.repo, "status", "--porcelain").Output(); len(st) > 0 {
				c += " (plus uncommitted changes)"
			}
			ls.facts["commit"] = c
		}
	}
	base := ls.base
	ls.mu.Unlock()
	ls.log(t, id, "before: %s (baseline %s)", now, base)
	if *now != *base {
		t.Fatalf("claude changed between scenarios: now %s, baseline %s", now, base)
	}
	t.Cleanup(func() {
		after, err := lsIdentify(claude)
		ls.log(t, id, "after: %v (err %v)", after, err)
		if err != nil || *after != *base {
			ls.check(t, id, "installed Claude Code unchanged across the run", false, "baseline %s, now %v (err %v)", base, after, err)
		}
	})

	sc.root, err = os.MkdirTemp("", "mash-live-"+strings.ToLower(id)+"-")
	if err != nil {
		t.Fatal(err)
	}
	ls.addSecret(sc.root)
	sc.home = filepath.Join(sc.root, "home")
	sc.state = filepath.Join(sc.root, "state")
	sc.work = filepath.Join(sc.root, "work") // a project dir with no settings or CLAUDE.md of this checkout
	sc.mashDir = filepath.Dir(mashBinary(t))
	sc.cfg = filepath.Join(sc.home, ".claude")
	if inPlace != "" {
		sc.cfg = inPlace
	} else if cfgEnv {
		sc.cfg = filepath.Join(sc.root, "config")
	}
	dirs := []string{sc.home, sc.state, sc.work}
	if inPlace == "" {
		dirs = append(dirs, sc.cfg)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if inPlace == "" {
		credsDst := filepath.Join(sc.cfg, ".credentials.json")
		t.Cleanup(func() { os.Remove(credsDst); os.RemoveAll(sc.root) })
		b, err := os.ReadFile(credsSrc)
		if err != nil {
			t.Fatalf("read MASH_LIVE_CLAUDE_CREDS: %v", err)
		}
		if err := os.WriteFile(credsDst, b, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Cleanup(func() { os.RemoveAll(sc.root) }) // the temp root only, never the config dir
		// Registered after the root's removal, so it runs before it (cleanups are last in, first out).
		// The cache clear is registered before the uninstall, so it runs after it.
		t.Cleanup(func() { clearMashMCPFailureCache(t, sc.cfg) })
		t.Cleanup(func() { sc.uninstallPlugin() })
		clearMashMCPFailureCache(t, sc.cfg) // a failure cached by an earlier scenario or run must not hide this one
	}
	// Onboarding is skipped by a minimal global config. Claude Code keeps it at <config dir>/.claude.json when
	// CLAUDE_CONFIG_DIR is set and at <home>/.claude.json otherwise; both are written (harmless where unused).
	// UNCERTAIN for 2.1.316: whether `-p` needs it at all, and whether it needs more than this.
	onboarding := []string{filepath.Join(sc.home, ".claude.json")}
	if inPlace == "" {
		onboarding = append(onboarding, filepath.Join(sc.cfg, ".claude.json")) // never into an in-place config dir
	}
	for _, p := range onboarding {
		if err := os.WriteFile(p, []byte(`{"hasCompletedOnboarding":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return sc
}

// lsEnv builds a child environment from scratch: PATH and what the scenario sets, TERM and LANG from the caller else
// fixed, HOME, DISABLE_AUTOUPDATER and (when the scenario uses one) CLAUDE_CONFIG_DIR. minimal drops TERM and LANG.
// It refuses any value that contains a registered secret.
func (sc *lsScenario) env(path string, minimal bool, extra map[string]string) map[string]string {
	e := map[string]string{"PATH": path, "HOME": sc.home, "DISABLE_AUTOUPDATER": "1"}
	if !minimal {
		e["TERM"], e["LANG"] = "xterm", "C.UTF-8"
		if v := os.Getenv("TERM"); v != "" {
			e["TERM"] = v
		}
		if v := os.Getenv("LANG"); v != "" {
			e["LANG"] = v
		}
	}
	if sc.cfgEnv {
		e["CLAUDE_CONFIG_DIR"] = sc.cfg
	}
	for k, v := range extra {
		e[k] = v
	}
	ls.mu.Lock()
	secrets := append([]string(nil), ls.secrets...)
	ls.mu.Unlock()
	for k, v := range e {
		for _, s := range secrets {
			// Registered paths are scrub targets for the receipt, not key material; the child's HOME lives under them.
			if len(s) >= 12 && !strings.HasPrefix(s, "/") && !strings.HasPrefix(k, "MASH_") && strings.Contains(v, s) { // MASH_* carries the scratch identity (url, owner), scrubbed from the receipt, not key material
				sc.t.Fatalf("child env %s would carry a secret", k)
			}
		}
		if strings.HasPrefix(k, "ANTHROPIC_") || strings.Contains(k, "API_KEY") || (strings.Contains(k, "TOKEN") && k != "MASH_LAUNCH_TOKEN") {
			sc.t.Fatalf("child env %s is key-like", k)
		}
	}
	return e
}

// identity is the mash env of endpoint name in w for a child: the identity and a creds PATH, never key material.
func (sc *lsScenario) identity(w *world, name string, extra map[string]string) map[string]string {
	src := w.env[name]
	ls.addFileSecrets(src["MASH_CREDS"])
	ls.addSecret(src["MASH_CREDS"], src["MASH_URL"], src["MASH_OWNER"], filepath.Dir(src["MASH_CREDS"]))
	out := map[string]string{"MASH_NAME": name, "MASH_URL": src["MASH_URL"], "MASH_OWNER": src["MASH_OWNER"],
		"MASH_CREDS": src["MASH_CREDS"], "MASH_SPOOL_FROM_START": "1", "XDG_STATE_HOME": sc.state}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (sc *lsScenario) mashPath() string { return sc.mashDir + ":/usr/bin:/bin" }

func lsList(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// command runs `claude args...` with exactly env: through `env -i` (so nothing is inherited, as an IDE's launch) with
// the absolute claude path.
func (sc *lsScenario) command(ctx context.Context, env map[string]string, args ...string) *exec.Cmd {
	envBin, err := exec.LookPath("env")
	if err != nil {
		envBin = "/usr/bin/env"
	}
	argv := append(append([]string{"-i"}, lsList(env)...), sc.claude)
	cmd := exec.CommandContext(ctx, envBin, append(argv, args...)...)
	cmd.Dir = sc.work
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) } // serve and hooks go too
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// installPlugin installs the plugin from the checkout as a local marketplace into the isolated config (the commands
// are `claude plugin marketplace add <path>` and `claude plugin install mash@mash`, from `claude plugin --help`).
func (sc *lsScenario) installPlugin(env map[string]string) {
	t := sc.t
	t.Helper()
	for _, args := range [][]string{{"plugin", "marketplace", "add", sc.repo}, {"plugin", "install", "mash@mash"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		out, err := sc.command(ctx, env, args...).CombinedOutput()
		cancel()
		ls.log(t, sc.id, "claude %s: err=%v out=%s", strings.Join(args[:2], " "), err, out)
		if err != nil {
			t.Fatalf("claude %s: %v", strings.Join(args, " "), err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := sc.command(ctx, env, "plugin", "list").CombinedOutput()
	ls.check(t, sc.id, "plugin mash@mash installed from the checkout", err == nil && strings.Contains(string(out), "mash"), "plugin list: %s", out)
}

// uninstallPlugin undoes installPlugin in an in-place config dir (user scope writes there): idempotent, failures are
// logged and never fatal. These two commands are the only writes into that directory besides Claude Code's own.
func (sc *lsScenario) uninstallPlugin() {
	env := sc.env(sc.mashPath(), false, nil)
	for _, args := range [][]string{{"plugin", "uninstall", "mash@mash"}, {"plugin", "marketplace", "remove", "mash"}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		out, err := sc.command(ctx, env, args...).CombinedOutput()
		cancel()
		ls.log(sc.t, sc.id, "cleanup claude %s: err=%v out=%s", strings.Join(args, " "), err, out)
	}
}

// ---- reading a stream-json run ----

type lsStream struct {
	mcp          []string // "name=status" for the init message's MCP servers
	tools        map[string]string
	toolNames    []string
	statusText   string
	resultText   string
	results      int
	numTurns     float64
	isError      bool
	hookEvents   int
	channelUsers int      // user-type messages that carry a <channel event
	notices      []string // non-conversation lines (and stderr lines) that mention the channel
	lines        int
	stderr       string
	runErr       error
	elapsed      time.Duration
	timedOut     bool
}

func lsText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (s *lsStream) feed(line string) {
	s.lines++
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return
	}
	typ, _ := m["type"].(string)
	sub, _ := m["subtype"].(string)
	switch typ {
	case "system":
		if strings.HasPrefix(sub, "hook_") {
			s.hookEvents++
		}
		if sub == "init" {
			if servers, ok := m["mcp_servers"].([]any); ok {
				for _, e := range servers {
					if sm, ok := e.(map[string]any); ok {
						s.mcp = append(s.mcp, fmt.Sprintf("%v=%v", sm["name"], sm["status"]))
					}
				}
			}
		} else if !strings.HasPrefix(sub, "hook_") && strings.Contains(strings.ToLower(line), "channel") {
			s.notices = append(s.notices, line)
		}
	case "assistant":
		msg, _ := m["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, c := range content {
			if cm, ok := c.(map[string]any); ok && cm["type"] == "tool_use" {
				id, _ := cm["id"].(string)
				name, _ := cm["name"].(string)
				if s.tools == nil {
					s.tools = map[string]string{}
				}
				s.tools[id] = name
				s.toolNames = append(s.toolNames, name)
			}
		}
	case "user":
		msg, _ := m["message"].(map[string]any)
		if strings.Contains(lsText(msg["content"]), "<channel") {
			s.channelUsers++
		}
		content, _ := msg["content"].([]any)
		for _, c := range content {
			cm, ok := c.(map[string]any)
			if !ok || cm["type"] != "tool_result" {
				continue
			}
			id, _ := cm["tool_use_id"].(string)
			if strings.HasSuffix(s.tools[id], "__status") {
				s.statusText = lsText(cm["content"])
			}
		}
	case "result":
		s.results++
		s.resultText, _ = m["result"].(string)
		s.numTurns, _ = m["num_turns"].(float64)
		s.isError, _ = m["is_error"].(bool)
	}
}

func (s *lsStream) used(tool string) bool {
	for _, n := range s.toolNames {
		if n == tool || strings.HasPrefix(n, tool) {
			return true
		}
	}
	return false
}

// lsFacts picks the lines of text that start with one of the prefixes (so a status text is recorded as the facts it
// states, not whole).
func lsFacts(text string, prefixes ...string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		for _, p := range prefixes {
			if strings.HasPrefix(strings.TrimSpace(l), p) {
				out = append(out, strings.TrimSpace(l))
			}
		}
	}
	return strings.Join(out, " ;; ")
}

// run runs claude -p with prompt on stdin for at most 5 minutes, feeds the stream into an lsStream and calls onLine
// (on the test goroutine) for every line, so the harness can send mail mid-turn. The stream itself is never stored.
func (sc *lsScenario) run(env map[string]string, extraArgs []string, prompt string, onLine func(*lsStream)) *lsStream {
	t := sc.t
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), lsScenarioTO)
	defer cancel()
	// UNCERTAIN for 2.1.316: the prompt goes on stdin (-p reads it when there is no argument) so the variadic
	// --allowedTools cannot swallow it; flags are those of `claude --help`.
	args := append([]string{"-p", "--model", "haiku", "--output-format", "stream-json", "--verbose", "--include-hook-events",
		"--max-turns", lsMaxTurns}, extraArgs...)
	args = append(args, "--allowedTools", "Bash(sleep:*),"+lsStatusTool+","+lsWhoTool)
	cmd := sc.command(ctx, env, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	st := &lsStream{}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start claude: %v", err)
	}
	lines := make(chan string, 64)
	waited := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
		waited <- cmd.Wait()
	}()
	for line := range lines {
		st.feed(line)
		if onLine != nil {
			onLine(st)
		}
	}
	st.runErr = <-waited
	st.elapsed = time.Since(start)
	st.timedOut = ctx.Err() != nil
	st.stderr = errb.String()
	for _, l := range strings.Split(st.stderr, "\n") {
		if strings.Contains(strings.ToLower(l), "channel") {
			st.notices = append(st.notices, l)
		}
	}
	ls.log(sc.t, sc.id, "claude exited after %s: err=%v lines=%d results=%d turns=%v tools=%v", st.elapsed.Round(time.Second), st.runErr, st.lines, st.results, st.numTurns, st.toolNames)
	return st
}

func lsSend(t *testing.T, w *world, id, marker string) {
	t.Helper()
	out, code := w.cli(t, "b", "send", "a", "--kind", "task", marker)
	if code != 0 {
		t.Fatalf("send %s: %s", marker, out)
	}
	ls.log(t, id, "sent %s", marker)
}

func lsMarker(id string) string {
	return fmt.Sprintf("MARK-%s-%d", id, time.Now().UnixNano()%1000000)
}

func lsNotice(st *lsStream) string {
	if len(st.notices) == 0 {
		return "no line mentioning the channel in the stream or on stderr"
	}
	return ls.scrub(st.notices[0], 220)
}

// managedPresent reports a managed-settings file on this host, which a "no managed settings" scenario cannot ignore.
func lsManagedPresent() string {
	for _, p := range []string{"/etc/claude-code/managed-settings.json", "/Library/Application Support/ClaudeCode/managed-settings.json"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, d := range []string{"/etc/claude-code/managed-settings.d", "/Library/Application Support/ClaudeCode/managed-settings.d"} {
		if es, err := os.ReadDir(d); err == nil && len(es) > 0 {
			return d
		}
	}
	return ""
}

// ---- L1 ----

func TestLiveStrangerL1PlainNoAllowlist(t *testing.T) {
	sc := lsStart(t, "L1", true)
	if p := lsManagedPresent(); p != "" {
		t.Skipf("this host has managed settings at %s; a plain-claude run needs none", p)
	}
	const id = "L1"
	w := newWorld(t, "a", "b")
	env := sc.env(sc.mashPath(), false, sc.identity(w, "a", nil))
	sc.installPlugin(env)

	m1, m2, m3 := lsMarker("L1a"), lsMarker("L1b"), lsMarker("L1c")
	lsSend(t, w, id, m1)
	lsSend(t, w, id, m2)
	prompt := "Do both of these in one step, as two parallel tool calls: run the shell command `sleep 20`, and call the mash status tool. " +
		"Then reply with one line that lists every token in this session starting with MARK-L1 (look in any mash mail context too), and nothing else."
	sent3 := false
	st := sc.run(env, nil, prompt, func(st *lsStream) {
		if !sent3 && st.used("Bash") { // the turn is running: mail arrives mid-turn
			sent3 = true
			lsSend(t, w, id, m3)
		}
	})

	ls.check(t, id, "claude finished inside the 5 minute cap", !st.timedOut && st.runErr == nil, "elapsed %s err %v", st.elapsed.Round(time.Second), st.runErr)
	ls.check(t, id, "mash MCP server connected (init message)", strings.Contains(strings.Join(st.mcp, " "), "mash=connected") || strings.Contains(strings.Join(st.mcp, " "), "plugin:mash:mash=connected"), "mcp servers: %s", strings.Join(st.mcp, ", "))
	ls.info(t, id, "startup notice about the channel", "%s", lsNotice(st))
	got := 0
	for _, m := range []string{m1, m2, m3} {
		if strings.Contains(st.resultText, m) {
			got++
		}
	}
	ls.check(t, id, "the turn names all three markers (2 before the prompt, 1 mid-turn)", got == 3, "%d of 3 markers named; turns %v", got, st.numTurns)
	ls.check(t, id, "no wake turn: exactly one result, no channel event in the conversation", st.results == 1 && st.channelUsers == 0, "results %d, channel-carrying user messages %d", st.results, st.channelUsers)
	ls.info(t, id, "hook lifecycle events in the stream", "%d", st.hookEvents)
	ls.check(t, id, "status: the allowlist is MISSING", strings.Contains(st.statusText, "channel allowlist: MISSING"), "status facts: %s", lsFacts(st.statusText, "channel allowlist:", "delivery:", "environment:"))
	eventually(t, 20*time.Second, "unread drains to 0", func() bool { return unreadOf(t, w, "a") == 0 })
	ls.check(t, id, "no mail lost: unread 0 afterwards", unreadOf(t, w, "a") == 0, "unread %d", unreadOf(t, w, "a"))
}

// ---- L2 ----

func TestLiveStrangerL2TeamPolicyShadowed(t *testing.T) {
	// Needs a Claude.ai Team or Enterprise organisation with server-managed settings; Claude Code may refresh or
	// ignore a planted remote-settings.json, so none is planted. CI covers mash's side (TestStrangerTeamPolicyShadowed).
	if os.Getenv("MASH_LIVE_CLAUDE") == "1" {
		ls.row(t, "L2", "server-managed settings without mash", "not run", "no organisation account")
		t.Cleanup(func() { ls.flush(t) })
	}
	t.Skip("not run: no organisation account")
}

// ---- L3 ----

func TestLiveStrangerL3MinimalEnvironment(t *testing.T) {
	sc := lsStart(t, "L3", false) // a bare launch sets no CLAUDE_CONFIG_DIR: the config is <home>/.claude
	const id = "L3"
	w := newWorld(t, "a", "b")
	base := sc.env("/usr/bin:/bin", true, nil) // exactly: env -i HOME=<tmp> PATH=/usr/bin:/bin (plus DISABLE_AUTOUPDATER)
	sc.installPlugin(base)

	// Part 1: no mash anywhere on PATH. A missing mash must be visible, and nothing may hang.
	prompt1 := "Run the shell command `sleep 1`, then reply with the single word done."
	st := sc.run(base, nil, prompt1, nil)
	ls.check(t, id, "no mash on PATH: nothing hangs (finished inside the cap, a result produced)", !st.timedOut && st.runErr == nil && st.results == 1, "elapsed %s err %v results %d", st.elapsed.Round(time.Second), st.runErr, st.results)
	ls.info(t, id, "no mash on PATH: MCP server state (init message)", "%s", strings.Join(st.mcp, ", "))
	logb, _ := os.ReadFile(filepath.Join(sc.home, ".local", "state", "mash", "hook.log"))
	hl := string(logb)
	ls.check(t, id, "no mash on PATH: hook.log says so", strings.Contains(hl, "no binary found") || strings.Contains(hl, "not found"), "hook.log (%d bytes): %s", len(logb), firstLines(hl, 3))

	ls.info(t, id, "a failed plugin MCP server is cached per config dir", "%s", "a failed plugin MCP server is cached per config dir for ~15 min")
	if sc.inPlace {
		clearMashMCPFailureCache(t, sc.cfg)
	}

	// Part 2: mash reachable, MASH_HOOK_TRACE=1 (the hook prints its pid chain, both session ids and the rule).
	m := lsMarker("L3")
	lsSend(t, w, id, m)
	env2 := sc.env(sc.mashPath(), true, sc.identity(w, "a", map[string]string{"MASH_HOOK_TRACE": "1"}))
	sp := spool.Spool{Dir: spool.Dir(filepath.Join(sc.state, "mash"), w.env["a"]["MASH_OWNER"], "a")}
	var owner string
	prompt2 := "Run the shell command `sleep 8`, then reply with one line listing every token in this session starting with MARK-L3 (look in any mash mail context too), and nothing else."
	seen := false
	st2 := sc.run(env2, nil, prompt2, func(st *lsStream) {
		if !seen && st.used("Bash") {
			seen = true
			deadline := time.Now().Add(6 * time.Second)
			for owner == "" && time.Now().Before(deadline) { // serve's identity record is there while the turn runs
				owner = sp.Owner()
				time.Sleep(200 * time.Millisecond)
			}
		}
	})
	ls.check(t, id, "mash reachable: finished and named the marker", !st2.timedOut && st2.runErr == nil && strings.Contains(st2.resultText, m), "elapsed %s err %v turns %v", st2.elapsed.Round(time.Second), st2.runErr, st2.numTurns)
	ls.info(t, id, "mash reachable: MCP server state (init message)", "%s", firstLines(strings.Join(st2.mcp, ", "), 1))
	ls.info(t, id, "mash reachable: the model's answer", "%s", firstLines(st2.resultText, 3))
	ls.info(t, id, "serve's owner record (parent pid, session id, launch token)", "%s", owner)
	logb2, _ := os.ReadFile(filepath.Join(sc.state, "mash", "hook.log"))
	traces := traceLines(string(logb2))
	ls.check(t, id, "MASH_HOOK_TRACE=1: the hook logged its pid chain and session ids", len(traces) > 0, "%d trace lines; first: %s", len(traces), firstLines(strings.Join(traces, "\n"), 1))
	matched := false
	for _, l := range traces {
		matched = matched || strings.Contains(l, "match=true")
	}
	ls.check(t, id, "a traced hook matched serve's ownership (and so drained)", matched, "trace lines: %s", firstLines(strings.Join(traces, "\n"), 2))
}

func firstLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[:n]
	}
	return strings.Join(l, " / ")
}

func traceLines(log string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "mash hook: trace") {
			out = append(out, l)
		}
	}
	return out
}

// ---- L4 ----

// lsHasDegraded is true once gate 3's DEGRADED delivery line is in the product source. L4 reads the line, so it needs
// that gate; until then it skips, and starts running by itself when the gate lands.
func lsHasDegraded() bool {
	b, err := os.ReadFile("status.go")
	return err == nil && strings.Contains(string(b), "DEGRADED")
}

func TestLiveStrangerL4HooksDisabled(t *testing.T) {
	if !lsHasDegraded() {
		t.Skip("needs gate 3 (#200)")
	}
	sc := lsStart(t, "L4", true)
	const id = "L4"
	// Managed settings live in /etc and are never touched here, so mash is not on a channel allowlist and no wake
	// can arrive: only the part observable without one is asserted (the DEGRADED line after tool calls that carried no
	// hook). Wake turns, and the read-hint wake, are not run.
	w := newWorld(t, "a", "b")
	env := sc.env(sc.mashPath(), false, sc.identity(w, "a", nil))
	sc.installPlugin(env)
	settings := filepath.Join(sc.root, "no-hooks.json")
	if err := os.WriteFile(settings, []byte(`{"disableAllHooks": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m1, m2 := lsMarker("L4a"), lsMarker("L4b")
	lsSend(t, w, id, m1)
	prompt := "Do these steps in order. Step 1, in one step as two parallel tool calls: call the mash who tool, and run the shell command `sleep 15`. " +
		"Step 2, in one step as two parallel tool calls: call the mash who tool again, and call the mash status tool. Then reply with the single word done."
	sent := false
	st := sc.run(env, []string{"--settings", settings}, prompt, func(st *lsStream) {
		if !sent && st.used("Bash") {
			sent = true
			lsSend(t, w, id, m2)
		}
	})
	ls.check(t, id, "claude finished inside the 5 minute cap", !st.timedOut && st.runErr == nil, "elapsed %s err %v", st.elapsed.Round(time.Second), st.runErr)
	ls.check(t, id, "no hook ran (disableAllHooks)", st.hookEvents == 0, "hook lifecycle events %d", st.hookEvents)
	ls.info(t, id, "tool calls in order", "%s (turns %v)", strings.Join(st.toolNames, ", "), st.numTurns)
	ls.check(t, id, "status says delivery: DEGRADED after two tool calls", strings.Contains(st.statusText, "DEGRADED"), "status facts: %s", lsFacts(st.statusText, "channel allowlist:", "delivery:"))
	ls.row(t, id, "wake turns (at most 3) and the read-hint wake", "not run", "needs a managed allowlist in /etc, which is never touched")
}

func TestClearMCPFailureKey(t *testing.T) {
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }

	missing := t.TempDir()
	clearMCPFailureKey(missing, logf)
	if _, err := os.Stat(filepath.Join(missing, "mcp-needs-auth-cache.json")); err == nil {
		t.Fatal("a missing file must stay missing")
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "mcp-needs-auth-cache.json")
	in := `{"plugin:mash:mash":{"timestamp":1,"id":"x"},"other":{"timestamp":2,"id":"y"}}`
	if err := os.WriteFile(p, []byte(in), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	clearMCPFailureKey(dir, logf)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["plugin:mash:mash"]; ok || len(m) != 1 || m["other"]["id"] != "y" {
		t.Fatalf("got %s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}

	if err := os.WriteFile(p, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearMCPFailureKey(dir, logf)
	if b, _ := os.ReadFile(p); string(b) != "not json" {
		t.Fatalf("invalid JSON must be left alone, got %s", b)
	}
	if len(logs) == 0 {
		t.Fatal("nothing logged")
	}
}
