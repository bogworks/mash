package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/scripts/upgradeproof/inventory"
)

// config is what the command line says.
type config struct {
	commit, version, prev, out string
	pack, repo                 string
	work                       string
	venue                      string // where the run happens, in the receipt's words
	releaseRepo                string // the GitHub repository (owner/name) the previous release is read from
	prevSumsSHA                string // the sha256 of the previous release's SHA256SUMS, verified by the launcher on its host
}

const (
	owner  = "acme"
	group  = "demo"
	oldTag = "v0.5.10"
)

// The longest a command of the run may take, by kind.
const (
	cloneTimeout    = 10 * time.Minute
	checkoutTimeout = 5 * time.Minute
	compileTimeout  = 15 * time.Minute
	buildTimeout    = 20 * time.Minute
	setupTimeout    = 5 * time.Minute
	scriptTimeout   = 10 * time.Minute
	doctorTimeout   = 2 * time.Minute
)

// certLife is how long the mirror's certificates are valid.
const certLife = 24 * time.Hour

var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// relBase is the directory the previous release's files are downloaded from.
func relBase(repo string) string {
	return "https://github.com/" + repo + "/releases/download/" + oldTag
}

// persona is one person on the machine with a home of their own: Alice and Bob run sessions, the admin runs mashctl.
type persona struct {
	name string // alice, bob, admin, mallory
	addr string // acme.demo.alice; empty for the admin
	home string
	cfg  string // the mash configuration folder
	st   string // the state folder (spool, hook log)
}

func (p *persona) binDir() string { return filepath.Join(p.home, ".local", "bin") }
func (p *persona) bin(n string) string {
	return filepath.Join(p.binDir(), n)
}

// proc is what the proof records of a running session.
type proc struct {
	Persona   string `json:"persona"`
	PID       int    `json:"pid"`
	Reported  string `json:"reported_version"`
	InitVer   string `json:"initialize_version"`
	ExePath   string `json:"executable_path"`
	ExeSHA256 string `json:"executable_sha256"`
	Started   string `json:"started"`
}

// invSet is one recipient's inventory: the messages stored for it and what became of them.
type invSet struct {
	Entries []inventory.Entry
	Tally   *inventory.Tally
}

// runner holds the state of one run.
type runner struct {
	cfg    config
	log    *logger
	steps  []*stepRecord
	goos   string
	goarch string

	pathVar  string // the machine's PATH
	toolsDir string // gh, nats-server, nats, each in its own folder
	work     string
	src      string

	personas map[string]*persona
	sessions map[string]*session
	procs    []proc

	prevDir    string
	prevSums   string
	prevSetup  string
	prevInstal string
	cand       *candidateSet
	mir        *mirror
	natsDir    string
	natsPID    int

	inv    map[string]*invSet // by recipient address
	before []inventory.Row    // Alice's listing before the upgrade, as mashctl gave it
	evid   map[string]any     // what goes to evidence/
}

func (r *runner) baseEnv() []string {
	// The run's own folder is its home: nothing of the machine's real home is read or written.
	return []string{"PATH=" + r.pathVar, "HOME=" + filepath.Join(r.work, "runhome"), "LC_ALL=C.UTF-8",
		"GOPATH=" + filepath.Join(r.work, "go"), "GOCACHE=" + filepath.Join(r.work, "gocache")}
}

// env is the environment of a command run as p. withGH puts gh on PATH; natsTools puts the local server's tools on it.
func (r *runner) env(p *persona, withGH, natsTools bool, extra ...string) []string {
	dirs := []string{p.binDir()}
	if withGH {
		dirs = append(dirs, filepath.Join(r.toolsDir, "gh"))
	}
	if natsTools {
		dirs = append(dirs, filepath.Join(r.toolsDir, "nats-server"), filepath.Join(r.toolsDir, "nats"))
	}
	dirs = append(dirs, r.pathVar)
	e := []string{"PATH=" + strings.Join(dirs, string(os.PathListSeparator)), "HOME=" + p.home, "LC_ALL=C.UTF-8",
		"MASH_CONFIG_DIR=" + p.cfg, "MASH_STATE_DIR=" + p.st, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1"}
	if p.addr != "" {
		e = append(e, "MASH_ENDPOINT="+p.addr)
	}
	return append(e, extra...)
}

// noPluginRoot is a plugin root with no manifest: a path under the run's work directory that setup
// checked is absent and that nothing creates.
func (r *runner) noPluginRoot() string { return filepath.Join(r.work, "no-plugin-root") }

// sessionEnv is the environment Claude Code gives a session and its hooks.
func (r *runner) sessionEnv(p *persona) []string {
	return r.env(p, false, false, "MASH_SPOOL_FROM_START=1", "CLAUDE_CODE_SESSION_ID=sess-"+p.name,
		"MASH_LAUNCH_TOKEN=tok-"+p.name, "MASH_PLUGIN_ROOT="+r.noPluginRoot(), "MASH_BATCH_MSGS=1000", "MASH_BATCH_BYTES=100000000")
}

func (r *runner) mash(p *persona, args ...string) cmdResult {
	return r.run(r.env(p, false, false), p.home, doctorTimeout, p.bin("mash"), args...)
}

func (r *runner) mashctl(args ...string) cmdResult {
	a := r.personas["admin"]
	return r.run(r.env(a, false, false), a.home, doctorTimeout, a.bin("mashctl"), append([]string{"--config-dir", a.cfg}, args...)...)
}

// install runs an install.sh as p for the version tag. base is the release mirror ("" for the real release); withGH says
// whether gh is on PATH for this one invocation.
func (r *runner) install(p *persona, script, tag, base string, withGH bool) cmdResult {
	extra := []string{"MASH_VERSION=" + tag, "MASH_REPO=" + r.cfg.releaseRepo}
	if base != "" {
		extra = append(extra, "MASH_RELEASE_BASE="+base, "CURL_CA_BUNDLE="+r.mir.caPath, "SSL_CERT_FILE="+r.mir.caPath)
	}
	env := r.env(p, withGH, false, extra...)
	var dropped []string
	if !withGH {
		var path string
		path, dropped = dropFromPath(envValue(env, "PATH"), "gh")
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + path
			}
		}
		if onPath(path, "gh") {
			return cmdResult{Err: fmt.Errorf("gh is on the PATH of the gh-free install"), Code: -1}
		}
	}
	if len(dropped) > 0 {
		r.log.printf("  note: gh-free install: PATH without the folders holding gh: %s", strings.Join(dropped, ", "))
	}
	res := r.run(env, p.home, scriptTimeout, "bash", script)
	if len(dropped) > 0 {
		res.Out = "gh-free install: dropped from PATH (they hold gh): " + strings.Join(dropped, ", ") + "\n" + res.Out
	}
	return res
}

// dropFromPath returns pathVar without every folder that holds an executable name (order kept), and the folders dropped.
func dropFromPath(pathVar, name string) (string, []string) {
	var keep, dropped []string
	for _, d := range strings.Split(pathVar, string(os.PathListSeparator)) {
		if d != "" && onPath(d, name) {
			dropped = append(dropped, d)
			continue
		}
		keep = append(keep, d)
	}
	return strings.Join(keep, string(os.PathListSeparator)), dropped
}

func envValue(env []string, k string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, k+"="); ok {
			return v
		}
	}
	return ""
}

// shown is one message a hook put in front of the session.
type shown struct {
	Seq    uint64
	Sender string
	Token  string
}

var (
	blockRE = regexp.MustCompile(`(?m)^--- #(\d+) from (\S+)`)
	tokRE   = regexp.MustCompile(`\[tok:([A-Za-z0-9]+)\]`)
	sentRE  = regexp.MustCompile(`number (\d+)`)
)

// parseHook is the messages in a hook's output (the additionalContext of its JSON, or the text itself).
func parseHook(out string) []shown {
	text := out
	var o struct {
		H struct {
			Ctx string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &o) == nil && o.H.Ctx != "" {
		text = o.H.Ctx
	}
	idx := blockRE.FindAllStringSubmatchIndex(text, -1)
	var res []shown
	for i, m := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		n, _ := strconv.ParseUint(text[m[2]:m[3]], 10, 64)
		s := shown{Seq: n, Sender: text[m[4]:m[5]]}
		if t := tokRE.FindStringSubmatch(text[m[0]:end]); t != nil {
			s.Token = t[1]
		}
		res = append(res, s)
	}
	return res
}

// hook runs the session's hook as p once and returns the messages it showed and its raw output.
func (r *runner) hook(p *persona) ([]shown, string, error) {
	in := fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"sess-%s"}`, p.name)
	res := r.runIn(r.sessionEnv(p), p.home, in, doctorTimeout, p.bin("mash"), "hook")
	if !res.ok() {
		return nil, res.Out, fmt.Errorf("mash hook: exit %d %v", res.Code, res.Err)
	}
	return parseHook(res.Out), res.Out, nil
}

// drain runs the hook until it shows nothing (at most 12 times) and records everything it showed.
func (r *runner) drain(p *persona) error {
	for i := 0; i < 12; i++ {
		msgs, _, err := r.hook(p)
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}
		r.observe(p.addr, msgs)
	}
	return fmt.Errorf("the hook of %s still showed mail after 12 runs", p.name)
}

func (r *runner) invOf(addr string) *invSet {
	if r.inv[addr] == nil {
		r.inv[addr] = &invSet{Tally: inventory.NewTally()}
	}
	return r.inv[addr]
}

// observe tallies what a hook showed to the session of addr. A message is matched to the inventory by its number and
// token (and its sender, except for refused mail, whose sender line is not proof of anything); one that does not match
// is tallied under the identity it showed, so that it counts as an unexpected message.
func (r *runner) observe(addr string, msgs []shown) {
	iv := r.invOf(addr)
	for _, m := range msgs {
		id := inventory.ID{Seq: m.Seq, Sender: m.Sender, Nonce: m.Token}
		for _, e := range iv.Entries {
			if e.ID.Seq == m.Seq && e.ID.Nonce == m.Token && (e.Expect == inventory.Refused || e.ID.Sender == m.Sender) {
				id = e.ID
			}
		}
		iv.Tally.AddShown(id)
		r.log.printf("  shown to %s: %s", addr, id)
	}
}

func (r *runner) addEntry(addr string, e inventory.Entry) {
	iv := r.invOf(addr)
	iv.Entries = append(iv.Entries, e)
}

// sendMail has from mail to, with a token in the body, and returns the stream number the send reported.
func (r *runner) sendMail(from *persona, to *persona, token, label string) (uint64, error) {
	text := fmt.Sprintf("%s [tok:%s]", label, token)
	res := r.mash(from, "send", to.addr, text)
	if !res.ok() {
		return 0, fmt.Errorf("mash send %s: exit %d: %s", to.addr, res.Code, strings.TrimSpace(res.Out))
	}
	m := sentRE.FindStringSubmatch(res.Out)
	if m == nil {
		return 0, fmt.Errorf("mash send did not say the message number: %q", strings.TrimSpace(res.Out))
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n, nil
}

// mailEntry sends one message and inventories it.
func (r *runner) mailEntry(s *stepCtx, from, to *persona, token, label, phase string, expect inventory.Expect) bool {
	seq, err := r.sendMail(from, to, token, label)
	if !s.need(err == nil, "send "+token+" "+from.name+" to "+to.name, "%v", err) {
		return false
	}
	r.addEntry(to.addr, inventory.Entry{ID: inventory.ID{Seq: seq, Sender: from.addr, Nonce: token}, Phase: phase, Label: label, Expect: expect})
	return true
}

// rowsFrom reads the messages of a `mashctl endpoint inbox --json` or `mash inbox --json` listing: every object in the
// output that has a number is one row; it is refused when its trust is "refused" or it gives a reason.
func rowsFrom(text string) ([]inventory.Row, error) {
	i := strings.IndexAny(text, "{[")
	if i < 0 {
		return nil, fmt.Errorf("no JSON in %q", text)
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(text[i:]))
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var rows []inventory.Row
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			if n, ok := x["number"].(float64); ok {
				row := inventory.Row{Seq: uint64(n)}
				row.Sender, _ = x["from"].(string)
				trust, _ := x["trust"].(string)
				if reason, _ := x["reason"].(string); reason != "" {
					row.Reason = reason
					row.Refused = true
				}
				if trust == "refused" {
					row.Refused = true
				}
				if b, ok := x["refused"].(bool); ok && b {
					row.Refused = true
				}
				rows = append(rows, row)
				return
			}
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return rows, nil
}

func maxSeq(rows []inventory.Row) uint64 {
	var m uint64
	for _, r := range rows {
		if r.Seq > m {
			m = r.Seq
		}
	}
	return m
}

// adminInbox is the mashctl view of addr's inbox as rows.
func (r *runner) adminInbox(addr string) ([]inventory.Row, string, error) {
	res := r.mashctl("endpoint", "inbox", addr, "--json", "--limit", "1000")
	if !res.ok() {
		return nil, res.Out, fmt.Errorf("mashctl endpoint inbox: exit %d %v", res.Code, res.Err)
	}
	rows, err := rowsFrom(res.Out)
	return rows, res.Out, err
}
