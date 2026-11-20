package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/bootstrap"
)

// Scenario is one acceptance test of contract section 6 (or the selfcheck).
type Scenario struct {
	ID       string
	Scenario string // the text of the record's scenario section
	Body     func(a *AT)
}

// AT is the state of one scenario run: its own directory, its server, its sessions and its assertions.
type AT struct {
	R       *Run
	ID      string
	Dir     string // <run>/<id>: everything this scenario makes, apart from output
	C       *Checks
	Scratch []string

	startSeq int
	srvDir   string
	srvPID   int
	secrets  map[string][]byte
}

// NewAT makes the scenario's directory.
func NewAT(r *Run, id string) (*AT, error) {
	a := &AT{R: r, ID: id, Dir: filepath.Join(r.Dir, id), C: &Checks{}, secrets: map[string][]byte{}}
	if err := os.Mkdir(a.Dir, 0o700); err != nil {
		return nil, err
	}
	r.Runner.mu.Lock()
	a.startSeq = r.Runner.seq + 1
	r.Runner.mu.Unlock()
	a.srvDir = filepath.Join(a.Dir, "server")
	return a, nil
}

// Home makes the scratch home name under the scenario directory.
func (a *AT) Home(name string) (Home, error) {
	h, err := NewHome(filepath.Join(a.Dir, name))
	if err == nil {
		a.Scratch = append(a.Scratch, a.ID+"/"+name)
	}
	return h, err
}

// tlsURL is the URL of the one local server: TLS, as mashctl init --local writes it to config.json (REQ-014).
func (a *AT) tlsURL() string { return "tls://127.0.0.1:" + strconv.Itoa(a.R.Port) }

// caPath is the CA certificate (public) that mashctl init --local writes into the server directory.
func (a *AT) caPath() string { return filepath.Join(a.srvDir, bootstrap.CACert) }

// Conn is the connection settings of every endpoint and mashctl command of the scenario: the one local server over
// TLS, trusting the CA init --local made, with no insecure_plaintext override (REQ-014, AT-001). The endpoint is a
// placeholder that gives the commands their owner, alice; a session names its own (SessionEnv), which comes later and wins.
func (a *AT) Conn() []string {
	return []string{"MASH_SERVER=" + a.tlsURL(), "MASH_CA=" + a.caPath(), "MASH_ENDPOINT=alice.dev.ctl"}
}

// Env is the environment for a command in home h: scratch values only, plus extra.
func (a *AT) Env(h Home, extra ...string) []string {
	return BuildEnv(h, []string{filepath.Dir(a.R.NatsBin)}, extra...)
}

// Ctl runs mashctl in home h.
func (a *AT) Ctl(label string, h Home, extra []string, args ...string) Result {
	return a.R.Runner.RunIn(h.Dir, label, append([]string{a.R.Mashctl}, args...), a.Env(h, extra...), "")
}

// Mash runs the mash command line in home h.
func (a *AT) Mash(label string, h Home, extra []string, args ...string) Result {
	return a.R.Runner.RunIn(h.Dir, label, append([]string{a.R.Mash}, args...), a.Env(h, extra...), "")
}

// SessionEnv is the extra environment of a session for owner.group.name: the connection settings and the address,
// with a state directory of its own. An empty group makes an address with no group (alice.NAME), which is refused.
func (a *AT) SessionEnv(h Home, group, name string, more ...string) []string {
	addr := "alice." + name
	if group != "" {
		addr = "alice." + group + "." + name
	}
	e := append(a.Conn(), "MASH_ENDPOINT="+addr, "MASH_STATE_DIR="+filepath.Join(h.State, name))
	return append(e, more...)
}

// StartSession starts `mash serve` for the endpoint and takes it through the MCP handshake.
func (a *AT) StartSession(h Home, group, name string, more ...string) (*Proc, []string, error) {
	extra := a.SessionEnv(h, group, name, more...)
	p, err := a.R.StartProc("serve-"+name, []string{a.R.Mash, "serve"}, a.Env(h, extra...))
	if err != nil {
		return nil, nil, err
	}
	if err := p.Handshake(20 * time.Second); err != nil {
		return p, extra, err
	}
	return p, extra, nil
}

// StartServer runs `mashctl init --local --owner alice --dir <srv> --port P --start` in home h, records the server's
// PID (mashctl writes it to server.pid) and checks the first run.
func (a *AT) StartServer(h Home) bool {
	if err := waitPortFree(a.R.Port, 5*time.Second); err != nil {
		a.C.Check("the port is free before the server starts", false, err.Error())
		return false
	}
	res := a.Ctl("init-local", h, nil, "init", "--local", "--owner", "alice", "--dir", a.srvDir, "--port", strconv.Itoa(a.R.Port), "--start")
	ok := a.C.Check("mashctl init --local --owner alice --start brings up one local server", res.Code == 0, exitDetail(res))
	// The CA key and the server key are private: every line of each is searched for in all captured output.
	for _, k := range []struct{ label, file string }{{"ca key", bootstrap.CAKey}, {"server key", bootstrap.ServerKey}} {
		if kb, err := os.ReadFile(filepath.Join(a.srvDir, k.file)); err == nil {
			registerKeyLines(a.secrets, k.label, kb)
		}
	}
	b, err := os.ReadFile(filepath.Join(a.srvDir, bootstrap.ServerPID))
	if err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil {
			a.srvPID = pid
			a.R.record(pid, a.srvDir)
		}
	}
	if ok && a.srvPID == 0 {
		a.C.Check("the server's PID is recorded in server.pid", false, "")
		return false
	}
	return ok
}

// StopServer stops the server by its recorded PID and waits for the port.
func (a *AT) StopServer() {
	if a.srvPID == 0 {
		return
	}
	stopPID(a.srvPID, a.srvDir, a.R.Runner.logf)
	a.R.forget(a.srvPID)
	a.srvPID = 0
	_ = waitPortFree(a.R.Port, 10*time.Second)
}

// Watch reads the secret in a seed or JWT file and remembers it, so that the end of the scenario can search all
// captured output for exactly that text. The text is kept in memory only.
func (a *AT) Watch(label, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var s []byte
	switch {
	case strings.Contains(label, "seed"):
		s = seedRE.Find(b)
	default:
		s = jwtRE.Find(b)
	}
	if s == nil {
		s = []byte(strings.TrimSpace(string(b)))
	}
	if len(s) >= 20 {
		a.secrets[label] = s
	}
}

// Finish stops (and waits for) everything the scenario started, scans the output of this scenario for any seed or JWT, writes the
// record and returns it.
func (a *AT) Finish(sc Scenario) Record {
	a.R.StopProcs() // the sessions first, and wait: nothing may write after the scan below
	a.StopServer()
	found, err := scanDir(a.R.Out)
	a.C.Check("INV-001: no command output holds a seed or a JWT", err == nil && len(a.ownLeaks(found)) == 0, "")
	specific, err2 := scanFor(a.R.Out, a.secrets)
	a.C.Check("INV-001: no command output holds a key or JWT this scenario made", err2 == nil && len(a.ownLeaks(specific)) == 0, "")
	ce := a.R.Runner.CaptureErrors()
	a.C.Check("INV-001: every command's output was captured completely (no capture or read error)", len(ce) == 0, strings.Join(ce, "; "))
	leaks := a.ownLeaks(append(found, specific...))
	rec := Record{RunID: a.R.ID, Revision: a.R.Revision, AT: sc.ID, Scenario: sc.Scenario, Env: a.R.EnvLines(a.Scratch), Observed: a.C.All(), Leaks: leaks}
	return rec
}

var seqRE = regexp.MustCompile(`^(\d+)-`)

// ownLeaks keeps the leaks found in output this scenario captured (the output directory is shared by a run of all).
func (a *AT) ownLeaks(in []Leak) []Leak {
	var out []Leak
	for _, l := range in {
		if m := seqRE.FindStringSubmatch(l.File); m != nil {
			if n, _ := strconv.Atoi(m[1]); n < a.startSeq {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

var unknownRE = regexp.MustCompile(`(?i)unknown (command|verb|noun|subcommand)`)

// exitDetail is the observed exit code, and says so when the command is not known to this build.
func exitDetail(r Result) string {
	d := "exit " + strconv.Itoa(r.Code)
	if r.Code != 0 && unknownRE.MatchString(r.Text()) {
		d += ": the CLI form does not exist in this build"
	}
	return d
}

func digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// refusalCode reads the refusal code from a command's --json document: "refusal" as a string, or an object with a
// "code". "" when there is none.
func refusalCode(stdout string) string {
	var doc map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc) != nil {
		return ""
	}
	raw, ok := doc["refusal"]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Code
	}
	return ""
}

// findObjects returns every JSON object nested anywhere in doc that has all of the named keys.
func findObjects(doc any, keys ...string) []map[string]any {
	var out []map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			has := true
			for _, k := range keys {
				if _, ok := t[k]; !ok {
					has = false
				}
			}
			if has {
				out = append(out, t)
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(doc)
	return out
}

func parseJSON(s string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(strings.TrimSpace(s)), &v)
	return v, err
}

// Snapshot is the state of the server and the files, as far as the harness can see without a NATS client of its own:
// the directory (mashctl list), the audit (directory, streams, consumers, leases, creds files), the issue record, and a
// digest of each home's file tree (all of it, except snapshotExclusions).
type Snapshot struct {
	Hash map[string]string
	Raw  map[string]string
	Bad  []string // read-only commands that failed
}

// Snapshot reads the state. ctlHome is where mashctl runs; trees are the homes whose files are digested.
func (a *AT) Snapshot(label string, ctlHome Home, trees ...Home) Snapshot {
	s := Snapshot{Hash: map[string]string{}, Raw: map[string]string{}}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"list", []string{"list"}},
		{"audit", []string{"audit", "--json"}},
		{"issued", []string{"issued", "list", "--json"}},
	} {
		r := a.Ctl("snap-"+label+"-"+c.name, ctlHome, a.Conn(), c.args...)
		if r.Code != 0 {
			s.Bad = append(s.Bad, fmt.Sprintf("%s exit %d", strings.Join(c.args, " "), r.Code))
		}
		s.Raw[c.name] = r.Stdout
		hashed := r.Stdout
		if c.name == "audit" {
			hashed = withoutGenerated(r.Stdout)
		}
		s.Hash[c.name] = digest(hashed)
	}
	hashes, bad := treeHashes(trees, snapshotExclusions)
	for k, v := range hashes {
		s.Hash[k] = v
	}
	s.Bad = append(s.Bad, bad...)
	return s
}

// snapshotExclusions are the exact relative paths (under each home) a refusal may legitimately change, so the
// snapshot leaves them out. Each entry needs a reason; an empty list means every file of every home is compared.
// AT-003 says "files byte-identical", so nothing is excluded unless a live run shows a refusal must write it.
var snapshotExclusions = []string{}

// exclusionNote is the text a "files byte-identical" assertion carries about snapshotExclusions: nothing, or each
// exclusion with its reason.
func exclusionNote() string {
	if len(snapshotExclusions) == 0 {
		return ""
	}
	return " except " + strings.Join(snapshotExclusions, ", ")
}

// treeHashes digests each home's file tree, leaving out only the exact relative paths in exclude. A read error is
// never turned into a value: it comes back as a failed read naming the path, and the home has no hash.
func treeHashes(trees []Home, exclude []string) (map[string]string, []string) {
	out := map[string]string{}
	var bad []string
	for _, h := range trees {
		t, err := treeHash(h.Dir, exclude...)
		if err != nil {
			bad = append(bad, "files "+h.Dir+": "+err.Error())
			continue
		}
		out["files:"+filepath.Base(h.Dir)] = t
	}
	return out, bad
}

// Diff names what differs between two snapshots.
func (s Snapshot) Diff(o Snapshot) []string {
	var d []string
	for k, v := range s.Hash {
		if o.Hash[k] != v {
			d = append(d, k)
		}
	}
	sort.Strings(d)
	return d
}

// withoutGenerated is the audit JSON without its top-level "generated" timestamp, re-encoded with sorted keys: two audits of
// an unchanged server are equal even when the calls fall in different seconds. Text that is not a JSON object is returned
// as it is.
func withoutGenerated(raw string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	delete(m, "generated")
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}
