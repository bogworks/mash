package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
)

// AT-4, the security boundaries. Every check is a NEGATIVE test: it fails when the protection it names is removed. The
// helpers (scanners, the refusal checker, the mail crafter, the pending judge) are pure and are shown to fail on a
// result that lacks the protection in at004_test.go; the live part (AT004 below) only gathers what they judge.

// at4Pending is the list of AT-4 checks whose protection is not built yet. A check listed here that fails is recorded
// as pending (not a pass); a check listed here that passes FAILS, saying the entry must go. So the table only shrinks.
var at4Pending = map[string]string{
	"plain-text-override": "core's R1-2: plain text only with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 (spec/rules.json), the warning on stderr, and MASH_INSECURE_PLAINTEXT refused naming the new variable",
	"unverified-refused":  "mash serve, mash inbox and mash read drop mail whose trust is not verified, and mash doctor counts it (spec/mash.json, 'Refusing unverified mail'); today the mail is delivered with its label",
	"join-secrets":        "core's `mash join` (not in this build): its --out and credentials file are refused over a file and a symlink, with no secret in any output",
}

// at4Verdict is how a check that may be listed in at4Pending is recorded.
type at4Verdict int

const (
	at4Pass    at4Verdict = iota // not listed, and it holds
	at4Fail                      // not listed and it does not hold, or listed and it holds
	at4Waiting                   // listed and it does not hold yet
)

// at4Judge is the whole rule of the pending table: listed says whether the check's key is in at4Pending, held whether
// the check's condition held.
func at4Judge(listed, held bool) at4Verdict {
	switch {
	case listed && held:
		return at4Fail // the protection exists: the entry is stale
	case listed:
		return at4Waiting
	case held:
		return at4Pass
	}
	return at4Fail
}

// pend records a check whose key may be listed in at4Pending.
func (a *AT) pend(key, name string, held bool, detail string) {
	reason, listed := at4Pending[key]
	switch at4Judge(listed, held) {
	case at4Pass:
		a.C.Check(name, true, detail)
	case at4Fail:
		if listed {
			a.C.Check(name, false, "the protection exists now: remove "+strconv.Quote(key)+" from at4Pending ("+detail+")")
		} else {
			a.C.Check(name, false, detail)
		}
	case at4Waiting:
		a.C.Pending(name, "waits for "+reason+"; observed: "+detail)
	}
}

// ---- scanning ---------------------------------------------------------------------------------------------------

// secretKinds names what text holds: each registered secret by its label, and a seed- or JWT-shaped string as "seed" or
// "jwt". It never returns the match.
func secretKinds(text string, secrets map[string][]byte) []string {
	var kinds []string
	b := []byte(text)
	for label, s := range secrets {
		if len(s) > 0 && bytes.Contains(b, s) {
			kinds = append(kinds, label)
		}
	}
	sort.Strings(kinds)
	return append(kinds, scanBytes(b)...)
}

// scanArgvs looks in every argument of every recorded command line for a registered secret or a seed- or JWT-shaped
// string: a secret on the command line is visible to every user of the machine in ps. A hit names the command's output
// label and the kind, never the match.
func scanArgvs(argvs []Argv, secrets map[string][]byte) []Leak {
	var leaks []Leak
	for _, av := range argvs {
		for _, kind := range secretKinds(strings.Join(av.Args, "\x00"), secrets) {
			leaks = append(leaks, Leak{File: av.Label + " (argv)", Kind: kind})
		}
	}
	return leaks
}

// scanProcCmdlines is scanArgvs over the live processes: the command line of every process under procRoot (/proc),
// read only. A hit names the pid.
func scanProcCmdlines(procRoot string, secrets map[string][]byte) ([]Leak, int, error) {
	l, n, _, err := scanProcCmdlinesPIDs(procRoot, secrets)
	return l, n, err
}

// scanProcCmdlinesPIDs is scanProcCmdlines that also returns the PIDs whose command line it read.
func scanProcCmdlinesPIDs(procRoot string, secrets map[string][]byte) ([]Leak, int, []int, error) {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, 0, nil, err
	}
	var pids []int
	var leaks []Leak
	n := 0
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil {
			continue // the process ended, or is not ours to read
		}
		n++
		pids = append(pids, pid)
		for _, kind := range secretKinds(strings.ReplaceAll(string(b), "\x00", "\x01"), secrets) {
			leaks = append(leaks, Leak{File: "process " + e.Name() + " (cmdline)", Kind: kind})
		}
	}
	return leaks, n, pids, nil
}

// procScanHolds judges a scan of the processes: it is only worth anything if it read at least two command lines and the
// server's own (srvPID) was among them; a scan that read nothing proves nothing.
func procScanHolds(leaks []Leak, n int, pids []int, srvPID int, err error) (bool, string) {
	var bad []string
	if err != nil {
		bad = append(bad, "the scan failed: "+err.Error())
	}
	if n < 2 {
		bad = append(bad, "only "+strconv.Itoa(n)+" process command line(s) were read: the scan proves nothing")
	}
	if srvPID <= 0 || !slices.Contains(pids, srvPID) {
		bad = append(bad, "the server's own process ("+strconv.Itoa(srvPID)+") was not among those read: the scan proves nothing")
	}
	if len(leaks) > 0 {
		bad = append(bad, leakList(leaks))
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, ""
}

// procCheck scans /proc now and records the check; when is "while a mash serve session is live" or "at the end".
func (a *AT) procCheck(when string) {
	pl, n, pids, err := scanProcCmdlinesPIDs("/proc", a.secrets)
	ok, d := procScanHolds(pl, n, pids, a.srvPID, err)
	a.C.Check("argv: none of the "+strconv.Itoa(n)+" processes in the container ("+when+"; nats-server and the sessions included) has a registered secret or a seed- or JWT-shaped string in its command line", ok, d)
}

// controlEverywhere judges that the signed control message reached all three places the unverified ones are looked for
// in: the session output, mash inbox and mash read, with mash inbox and mash read exiting 0. Without it a check that
// the unverified mail is absent proves nothing.
func controlEverywhere(marker, serveOut, inboxOut string, inboxCodes []int, readOut string, readCode int) (bool, string) {
	var bad []string
	for _, c := range inboxCodes {
		if c != 0 {
			bad = append(bad, "mash inbox exit "+strconv.Itoa(c))
		}
	}
	if len(inboxCodes) == 0 {
		bad = append(bad, "mash inbox was not run")
	}
	if readCode != 0 {
		bad = append(bad, "mash read exit "+strconv.Itoa(readCode))
	}
	for _, p := range []struct{ name, text string }{{"the session", serveOut}, {"mash inbox", inboxOut}, {"mash read", readOut}} {
		if !strings.Contains(p.text, marker) {
			bad = append(bad, "absent from "+p.name)
		}
	}
	if len(bad) > 0 {
		return false, "control not delivered: the check proves nothing (" + strings.Join(bad, "; ") + ")"
	}
	return true, "shown in the session, mash inbox and mash read"
}

// recreateRefusalRE is mashctl's own text for an address that was deleted or is taken (cmd/mashctl/endpoint_create.go).
var recreateRefusalRE = regexp.MustCompile(`(?i)was revoked|was deleted and its address cannot be used again|never reassigned|already exists`)

// recreateRefused judges the creation of a deleted address again: a refusal that gives that reason, not any failure.
func recreateRefused(r Result) (bool, string) {
	switch {
	case r.Code <= 0 || crashed(r):
		return false, "exit " + strconv.Itoa(r.Code) + " is not a refusal"
	case connErrRE.MatchString(r.Text()):
		return false, "the failure reads like a lost connection, not a refusal: " + exitDetail(r)
	case !recreateRefusalRE.MatchString(r.Text()):
		first, _, _ := strings.Cut(strings.TrimSpace(r.Text()), "\n")
		return false, "the failure does not say the address was deleted or exists: " + exitDetail(r) + "; it said: " + redact(first)
	}
	return true, exitDetail(r)
}

// labelled is the output of one command, for the scan of everything the commands said.
type labelled struct {
	Label string
	R     Result
}

// scanResults looks in the standard output and standard error of every result.
func scanResults(rs []labelled, secrets map[string][]byte) []Leak {
	var leaks []Leak
	for _, l := range rs {
		for _, kind := range secretKinds(l.R.Stdout, secrets) {
			leaks = append(leaks, Leak{File: l.Label + " (stdout)", Kind: kind})
		}
		for _, kind := range secretKinds(l.R.Stderr, secrets) {
			leaks = append(leaks, Leak{File: l.Label + " (stderr)", Kind: kind})
		}
	}
	return leaks
}

func leakList(ls []Leak) string {
	var s []string
	for _, l := range ls {
		s = append(s, l.File+": "+l.Kind)
	}
	return strings.Join(s, "; ")
}

// crashed is true when a command died instead of answering: killed by the timeout, or a Go panic.
func crashed(r Result) bool {
	return r.Code < 0 || strings.Contains(r.Text(), "panic:") || strings.Contains(r.Text(), "goroutine ")
}

// ---- files ------------------------------------------------------------------------------------------------------

// fileGuard is what is at a path, so that a refusal can be shown to have changed nothing there.
type fileGuard struct {
	Path   string
	Kind   string // "absent", "file", "symlink" or "other"
	Mode   fs.FileMode
	Data   []byte // the bytes of a file, or of a symlink's target when it can be read
	Link   string // a symlink's target path
	Target string // "absent" or "file" for a symlink's target
}

func takeGuard(path string) fileGuard {
	g := fileGuard{Path: path}
	st, err := os.Lstat(path)
	switch {
	case err != nil:
		g.Kind = "absent"
	case st.Mode()&fs.ModeSymlink != 0:
		g.Kind = "symlink"
		g.Link, _ = os.Readlink(path)
		if ts, terr := os.Stat(path); terr != nil {
			g.Target = "absent"
		} else {
			g.Target = "file"
			g.Mode = ts.Mode()
			g.Data, _ = os.ReadFile(path)
		}
	case st.Mode().IsRegular():
		g.Kind, g.Mode = "file", st.Mode()
		g.Data, _ = os.ReadFile(path)
	default:
		g.Kind = "other"
	}
	return g
}

// Diff says how after differs from g, or "" when nothing changed (kind, mode, bytes, link target).
func (g fileGuard) Diff(after fileGuard) string {
	name := filepath.Base(g.Path)
	switch {
	case g.Kind != after.Kind:
		return name + " was " + g.Kind + ", is " + after.Kind
	case g.Link != after.Link:
		return name + " now points elsewhere"
	case g.Target != after.Target:
		return name + "'s target was " + orNone(g.Target) + ", is " + orNone(after.Target)
	case g.Mode != after.Mode:
		return fmt.Sprintf("%s changed mode %04o to %04o", name, g.Mode.Perm(), after.Mode.Perm())
	case !bytes.Equal(g.Data, after.Data):
		return name + " (or its target) changed bytes"
	}
	return ""
}

// refusalHolds judges one command that had to refuse to write over something: it exited non-zero (not killed, not a
// panic), said why in words that match want, left every guarded path exactly as it was, and printed no secret.
func refusalHolds(r Result, before, after []fileGuard, want *regexp.Regexp, secrets map[string][]byte) (bool, string) {
	var bad []string
	if r.Code <= 0 || crashed(r) {
		bad = append(bad, "exit "+strconv.Itoa(r.Code)+" is not a refusal")
	}
	if strings.TrimSpace(r.Text()) == "" {
		bad = append(bad, "no message")
	} else if connErrRE.MatchString(r.Text()) {
		bad = append(bad, "the message reads like a failed connection, not a refusal")
	} else if !want.MatchString(r.Text()) {
		bad = append(bad, "the message does not say what was refused ("+want.String()+")")
	}
	for i := range before {
		if i < len(after) {
			if d := before[i].Diff(after[i]); d != "" {
				bad = append(bad, d)
			}
		}
	}
	if k := secretKinds(r.Text(), secrets); len(k) > 0 {
		bad = append(bad, "the output holds "+strings.Join(k, ", "))
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, "exit " + strconv.Itoa(r.Code)
}

// pathState is what a refusal test plants at the path a command wants to write.
type pathState string

const (
	psFile     pathState = "an existing file"
	psSymlink  pathState = "a symlink to a file"
	psDangling pathState = "a dangling symlink"
)

const plantedText = "KEEP ME: this existing file must not change\n"

// plant makes the state at path; the symlink target is made in tgtDir. It returns the paths to guard.
func plant(st pathState, path, tgtDir string) ([]string, error) {
	switch st {
	case psFile:
		return []string{path}, os.WriteFile(path, []byte(plantedText), 0o600)
	case psSymlink:
		tgt := filepath.Join(tgtDir, filepath.Base(path)+".target")
		if err := os.WriteFile(tgt, []byte(plantedText), 0o600); err != nil {
			return nil, err
		}
		return []string{path, tgt}, os.Symlink(tgt, path)
	case psDangling:
		tgt := filepath.Join(tgtDir, filepath.Base(path)+".nowhere")
		return []string{path, tgt}, os.Symlink(tgt, path)
	}
	return nil, fmt.Errorf("unknown state %q", st)
}

// ---- mail -------------------------------------------------------------------------------------------------------

// at4MailCase is one kind of mail an endpoint must not take for verified, and the trust label the stored message must
// carry (mashctl endpoint inbox, TRUST).
type at4MailCase struct {
	Name  string
	Trust string
}

var at4MailCases = []at4MailCase{
	{"absent", "absent"},
	{"claimed", "claimed"},
	{"unknown-signer", "unknown-signer"},
	{"stale", "stale"},
}

// craftMail makes the Agent-Sender header of one case, for a message to subject with payload. legit is the sender's
// own identity (the creds of an endpoint that exists); stranger signs with a key the directory has never seen; name is
// the sender's directory key. "" is no header at all. kind "control" is a correctly signed message, now.
func craftMail(kind string, legit, stranger sap.Identity, name, subject string, payload []byte, now time.Time) (string, error) {
	switch kind {
	case "absent":
		return "", nil
	case "claimed": // the header of an identity, with no signature
		return sap.SenderHeader{V: 1, Account: legit.Account, User: legit.User, Name: name}.Encode(), nil
	case "unknown-signer": // validly signed by a key that is in no directory record, under the legit sender's name
		h, err := stranger.Sign(subject, payload, name, now, "")
		return h.Encode(), err
	case "stale": // validly signed by the sender's own key, an hour before it is stored
		h, err := legit.Sign(subject, payload, name, now.Add(-time.Hour), "")
		return h.Encode(), err
	case "control":
		h, err := legit.Sign(subject, payload, name, now, "")
		return h.Encode(), err
	}
	return "", fmt.Errorf("unknown mail case %q", kind)
}

// storedTrust finds the trust label of the stored message numbered seq in `mashctl endpoint inbox --json`.
func storedTrust(inboxJSON string, seq uint64) (string, bool) {
	v, err := parseJSON(inboxJSON)
	if err != nil {
		return "", false
	}
	for _, o := range findObjects(v, "number", "trust") {
		if n, ok := o["number"].(float64); ok && uint64(n) == seq {
			t, _ := o["trust"].(string)
			return t, true
		}
	}
	return "", false
}

// trustLabelRE is a trust label as the endpoint prints it, in the header line of a message ("--- #4 from X [claimed] at ...",
// "#4<TAB>from X [claimed]<TAB>..."): the first one in a block.
var trustLabelRE = regexp.MustCompile(`\[(verified|claimed|absent|stale|unknown-signer|forged|malformed)\]`)

// shownLabels returns the trust label under which text presents the message that holds marker, once for each place it
// is shown: a block of a digest or of mash read ("--- #N ...") or a line of mash inbox. A block with no label reads
// "(none)". A message that is not shown at all returns nothing. The JSON-escaped newlines of a channel event are
// read as newlines; the preamble of a digest ("7 messages from ...") is not a message.
func shownLabels(text, marker string) []string {
	text = strings.ReplaceAll(text, `\n`, "\n")
	var blocks []string
	for _, part := range strings.Split(text, "--- #") {
		blocks = append(blocks, strings.Split(part, "\n#")...)
	}
	var out []string
	for _, bl := range blocks {
		if !strings.Contains(bl, marker) {
			continue
		}
		l := "(none)"
		if m := trustLabelRE.FindStringSubmatch(bl); m != nil {
			l = m[1]
		}
		out = append(out, l)
	}
	return out
}

// mislabelled says how a presentation of one message differs from the label it must have: it is shown under another
// label (never as verified when it is not), or not at all is fine ("" then).
func mislabelled(texts map[string]string, marker, want string) string {
	var bad []string
	for where, t := range texts {
		for _, l := range shownLabels(t, marker) {
			if l != want {
				bad = append(bad, where+" shows it as "+l)
			}
		}
	}
	sort.Strings(bad)
	return strings.Join(bad, "; ")
}

// ---- plain text -------------------------------------------------------------------------------------------------

var connErrRE = regexp.MustCompile(`(?i)no servers available|connection refused|i/o timeout|timed out|no route|unreachable|did not answer`)

// plainRefused judges one command run against a plain nats:// URL to a non-loopback address with no override: a
// non-zero exit, a message that tells the user to use tls:// and names the override, no sign that a connection was
// tried, and no wait for one (took is how long the command ran; limit well under the open timeout).
func plainRefused(r Result, took, limit time.Duration, override string) (bool, string) {
	var bad []string
	if r.Code <= 0 || crashed(r) {
		bad = append(bad, "exit "+strconv.Itoa(r.Code))
	}
	t := r.Text()
	if !strings.Contains(t, "tls://") {
		bad = append(bad, "the message does not name tls://")
	}
	if !strings.Contains(t, override) && !strings.Contains(t, "insecure_plaintext") {
		bad = append(bad, "the message does not name the override")
	}
	if connErrRE.MatchString(t) {
		bad = append(bad, "the message reads like a failed connection, not a refusal")
	}
	if took > limit {
		bad = append(bad, "it took "+took.Round(time.Millisecond).String()+": it waited for a connection")
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, "exit " + strconv.Itoa(r.Code) + " in " + took.Round(time.Millisecond).String()
}

// at4EncryptionWarning is the line the specification gives for the override (spec/rules.json).
const at4EncryptionWarning = "warning: encryption to the server is OFF (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set)"

// dangerousWarned judges a command run with the override against the counting listener: it connected, and the exact
// warning is the first line on stderr, never on stdout.
func dangerousWarned(r Result, connects int64) (bool, string) {
	onErr := strings.Contains(r.Stderr, at4EncryptionWarning)
	onOut := strings.Contains(r.Stdout, at4EncryptionWarning)
	return connects > 0 && onErr && !onOut, fmt.Sprintf("%d connection(s); warning on stderr %v, on stdout %v; %s", connects, onErr, onOut, exitDetail(r))
}

// oldNameRefused judges a command run with the old override MASH_INSECURE_PLAINTEXT=1: refused with an error that names
// the new variable, and no connection.
func oldNameRefused(r Result, connects int64) (bool, string) {
	ok := r.Code > 0 && !crashed(r) && strings.Contains(r.Text(), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION") && connects == 0
	return ok, fmt.Sprintf("%d connection(s); names the new variable %v; %s", connects, strings.Contains(r.Text(), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"), exitDetail(r))
}

// ---- the scenario -----------------------------------------------------------------------------------------------

// watchFile registers every secret a file holds: its whole text, and each seed- and JWT-shaped string in it.
func (a *AT) watchFile(label, path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return false
	}
	registerSecrets(a.secrets, label, b)
	return true
}

// registerSecrets adds the whole trimmed text of b, and every seed- and JWT-shaped string in it, under label.
func registerSecrets(secrets map[string][]byte, label string, b []byte) {
	if t := bytes.TrimSpace(b); len(t) >= 20 {
		secrets[label+" (whole file)"] = slices.Clone(t)
	}
	for i, s := range seedRE.FindAll(b, -1) {
		secrets[label+" seed "+strconv.Itoa(i+1)] = slices.Clone(s)
	}
	for i, s := range jwtRE.FindAll(b, -1) {
		secrets[label+" jwt "+strconv.Itoa(i+1)] = slices.Clone(s)
	}
}

// AT004 is the security boundaries.
var AT004 = Scenario{
	ID: "at004",
	Scenario: "AT-4, the security boundaries, every check a negative test that fails when its protection is removed: one local server; endpoints alice/dev/design and alice/dev/review made the contract's way on a separate endpoint machine. " +
		"(1) No secret in argv or output: every secret the scenario makes is registered (seeds, creds files, user JWTs, join files, the operator and account keys, the CA and server keys, the admin and system creds), " +
		"a wide set of mash and mashctl commands is run with --json, --verbose and failure paths (including a seed, a join file and a creds file fed to options that expect something else), " +
		"and all output, every recorded argv of the run and the command line of every process in the container are scanned for them. " +
		"(2) Secret files are refused over an existing file, a symlink and a dangling symlink, and left byte-identical (and the symlink target too): the creds file in the config folder, the join file of endpoint create and reissue --out, " +
		"the key and public key files of mash key create, the key pending file, and mash credential create --out. " +
		"(3) Unverified mail is refused: absent, claimed, unknown-signer and stale mail is published with the sender's own creds (headers crafted with the sap package), the recipient's session, mash inbox and mash read are checked, a properly signed control message is delivered, and mashctl endpoint inbox shows each stored message with its trust label. " +
		"(4) Plain text without the override is refused before connecting (a plain nats:// URL to a non-loopback address; mash and mashctl); the MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION behaviour is pending core's R1-2. " +
		"(5) A deleted endpoint's credential is accepted before the delete and refused after it, on this one server; the form on all three servers is AT-005's check (refusedOn and acceptedOn on the deleted review key). " +
		"Checks whose protection is not built are recorded pending, never as passes (at4Pending); `liveat all --strict` (the release gate) exits 3 while any is pending.",
	Body: func(a *AT) { a.at004() },
}

// at4 is the state of one AT-4 run.
type at4 struct {
	a              *AT
	ha, he, hn     Home
	design, review ep
	res            []labelled
	work           string // scratch directory for the file checks, 0700
	nwork          int
	designEnv      []string
	reviewEnv      []string
	runStart       int // the runner's argv count when the scenario began
}

func (a *AT) at004() {
	ha, e1 := a.Home("home-a") // mashctl host
	he, e2 := a.Home("home-e") // endpoint machine
	hn, e3 := a.Home("home-n") // a host with no account key
	if !a.C.Check("three scratch homes are made", e1 == nil && e2 == nil && e3 == nil, "") || !a.StartServer(ha) {
		return
	}
	s := &at4{a: a, ha: ha, he: he, hn: hn, runStart: len(a.R.Runner.Argvs())}
	s.work = filepath.Join(a.Dir, "work")
	if err := os.Mkdir(s.work, 0o700); !a.C.Check("the scratch directory for the file checks is made", err == nil, "") {
		return
	}
	var ok bool
	if s.design, ok = a.provision(ha, he, "alice", "dev", "design", filepath.Join(he.Dir, "design.creds"), false); !ok {
		return
	}
	if s.review, ok = a.provision(ha, he, "alice", "dev", "review", filepath.Join(he.Dir, "review.creds"), false); !ok {
		return
	}
	s.designEnv = a.SessionEnv(he, "dev", "design", "MASH_CREDS="+s.design.creds)
	s.reviewEnv = a.SessionEnv(he, "dev", "review", "MASH_CREDS="+s.review.creds)
	s.register()

	s.files()
	s.join()
	s.wide()
	s.mail()
	s.plain()
	s.deleted()
	s.finalScans()
}

// register arms the scans: every secret the scenario made. A file that cannot be read would leave its secret out of
// the scan without a word, so each registration is checked.
func (s *at4) register() {
	a := s.a
	for _, f := range []struct{ label, path string }{
		{"design seed", s.design.seed}, {"review seed", s.review.seed},
		{"design jwt", s.design.jwt}, {"review jwt", s.review.jwt},
		{"design creds", s.design.creds}, {"review creds", s.review.creds},
		{"design join", filepath.Join(s.ha.Dir, "alice.dev.design.jwt.join")},
		{"review join", filepath.Join(s.ha.Dir, "alice.dev.review.jwt.join")},
	} {
		a.C.Check("the scan is armed: "+f.label+" file was read and registered", a.watchFile(f.label, f.path), "")
	}
	for _, f := range secretFiles {
		a.C.Check("the scan is armed: the setup script's "+f.label+" ("+f.file+") was registered", a.hasSecret(f.label), "")
	}
	var seeds, jwts int
	for k := range a.secrets {
		switch {
		case strings.Contains(k, " seed "):
			seeds++
		case strings.Contains(k, " jwt "):
			jwts++
		}
	}
	a.C.Check("the scan is armed: seeds and user JWTs of both endpoints are registered", seeds >= 3 && jwts >= 3, fmt.Sprintf("%d seeds, %d JWTs", seeds, jwts))
}

// hasSecret reports whether any secret is registered under a label that starts with prefix.
func (a *AT) hasSecret(prefix string) bool {
	for k, v := range a.secrets {
		if strings.HasPrefix(k, prefix) && len(v) > 0 {
			return true
		}
	}
	return false
}

// run records a result for the scan of all output.
func (s *at4) run(label string, r Result) Result {
	s.res = append(s.res, labelled{label, r})
	return r
}

func (s *at4) ctl(label string, h Home, extra []string, args ...string) Result {
	return s.run(label, s.a.Ctl(label, h, append(s.a.Conn(), extra...), args...))
}

func (s *at4) mash(label string, h Home, env []string, args ...string) Result {
	return s.run(label, s.a.Mash(label, h, env, args...))
}

// dir makes a fresh private directory for one file check.
func (s *at4) dir(name string) (dir, tgt string) {
	s.nwork++
	dir = filepath.Join(s.work, fmt.Sprintf("%02d-%s", s.nwork, name))
	tgt = filepath.Join(dir, "targets")
	_ = os.MkdirAll(tgt, 0o700)
	return dir, tgt
}

// ---- check 2: files ---------------------------------------------------------------------------------------------

var refusedWordsRE = regexp.MustCompile(`(?i)already exists|exists|not a regular file|exposes|does not hold a seed|refus|is a symlink`)

// refuseOver plants the state, runs the command and judges the refusal.
func (s *at4) refuseOver(name string, run func() Result, want *regexp.Regexp, guarded []string, extraAbsent ...string) {
	a := s.a
	var before []fileGuard
	for _, p := range guarded {
		before = append(before, takeGuard(p))
	}
	for _, p := range extraAbsent {
		before = append(before, takeGuard(p))
	}
	r := run()
	var after []fileGuard
	for _, g := range before {
		after = append(after, takeGuard(g.Path))
	}
	ok, d := refusalHolds(r, before, after, want, a.secrets)
	a.C.Check("files: "+name+": refused (exit non-zero, a message that says why), the existing file and the symlink target byte-identical, no secret in the output", ok, d)
}

func (s *at4) files() {
	a, ha, he := s.a, s.ha, s.he
	states := []pathState{psFile, psSymlink, psDangling}
	fresh := func(label string) string { // a new key pair on the endpoint machine; the public key file
		seed := filepath.Join(s.work, label+".seed")
		r := s.mash("key-"+label, he, a.Conn(), "key", "create", "--out", seed)
		a.C.Check("files setup: mash key create makes a key for "+label, r.Code == 0, exitDetail(r))
		a.Watch("seed "+label, seed)
		return strings.TrimSuffix(seed, ".seed") + ".pub"
	}
	absent := func(addr string) {
		r := s.ctl("show-"+addr, ha, nil, "endpoint", "show", addr)
		a.C.Check("files: after the refusal no endpoint "+addr+" exists (nothing was issued)", r.Code != 0, exitDetail(r))
	}

	// The creds file mashctl endpoint create installs in the config folder.
	for i, st := range states {
		addr := "alice.dev.cf" + strconv.Itoa(i)
		path := filepath.Join(ha.Config, addr+".creds")
		_, tgt := s.dir("creds-" + addr)
		g, err := plant(st, path, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the creds path of "+addr, err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mashctl endpoint create "+addr+" over "+string(st)+" at the creds file in the config folder",
			func() Result { return s.ctl("creds-"+addr, ha, nil, "endpoint", "create", addr) }, refusedWordsRE, g, path+".pending")
		absent(addr)
		_ = os.Remove(path)
	}

	// The join file of create, with a public key and with a key made here.
	for i, st := range states {
		addr := "alice.dev.jf" + strconv.Itoa(i)
		dir, tgt := s.dir("join-" + addr)
		path := filepath.Join(dir, addr+".join")
		g, err := plant(st, path, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the join file path of "+addr, err == nil, errDetail(err)) {
			continue
		}
		pub := fresh("jf" + strconv.Itoa(i))
		s.refuseOver("mashctl endpoint create "+addr+" --public-key --out over "+string(st),
			func() Result {
				return s.ctl("join-"+addr, ha, nil, "endpoint", "create", addr, "--public-key", pub, "--out", path)
			}, refusedWordsRE, g)
		absent(addr)
	}
	{
		addr := "alice.dev.jfk"
		dir, tgt := s.dir("join-" + addr)
		path := filepath.Join(dir, addr+".join")
		g, err := plant(psSymlink, path, tgt)
		if a.C.Check("files setup: a symlink at the join file path of "+addr, err == nil, errDetail(err)) {
			s.refuseOver("mashctl endpoint create "+addr+" --out (no --public-key: the key is made here) over a symlink to a file",
				func() Result { return s.ctl("join-"+addr, ha, nil, "endpoint", "create", addr, "--out", path) }, refusedWordsRE, g, path+".pending")
			absent(addr)
		}
	}

	// The join file of reissue: an endpoint that exists keeps its working credential when the file is refused.
	for i, st := range states {
		dir, tgt := s.dir("reissue-" + strconv.Itoa(i))
		path := filepath.Join(dir, "design.join")
		g, err := plant(st, path, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the join file path of a reissue", err == nil, errDetail(err)) {
			continue
		}
		pub := fresh("ri" + strconv.Itoa(i))
		s.refuseOver("mashctl endpoint reissue alice.dev.design --public-key --out over "+string(st),
			func() Result {
				return s.ctl("reissue-"+strconv.Itoa(i), ha, nil, "endpoint", "reissue", "alice.dev.design", "--public-key", pub, "--out", path)
			}, refusedWordsRE, g)
	}
	r := s.mash("design-still-works", he, s.designEnv, "inbox")
	a.C.Check("files: after the refused reissues design's own credentials still work (nothing was rotated)", r.Code == 0, exitDetail(r))

	// The key pending file of a create that makes the key here.
	for _, c := range []struct {
		name string
		make func(path, tgtDir string) ([]string, error)
		want *regexp.Regexp
	}{
		{"a symlink", func(p, t string) ([]string, error) { return plant(psSymlink, p, t) }, regexp.MustCompile(`(?i)not a regular file`)},
		{"a dangling symlink", func(p, t string) ([]string, error) { return plant(psDangling, p, t) }, regexp.MustCompile(`(?i)not a regular file`)},
		{"a file that others can read", func(p, t string) ([]string, error) {
			err := os.WriteFile(p, []byte(plantedText), 0o644)
			if err == nil {
				err = os.Chmod(p, 0o644)
			}
			return []string{p}, err
		}, regexp.MustCompile(`(?i)exposes|mode`)},
		{"a file that holds no seed", func(p, t string) ([]string, error) { return plant(psFile, p, t) }, regexp.MustCompile(`(?i)does not hold a seed|seed`)},
	} {
		addr := "alice.dev.kp" + strconv.Itoa(len(c.name))
		dir, tgt := s.dir("pending-" + addr)
		out := filepath.Join(dir, addr+".join")
		g, err := c.make(out+".pending", tgt)
		if !a.C.Check("files setup: "+c.name+" at the key pending path of "+addr, err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mashctl endpoint create "+addr+" --out with "+c.name+" as the key pending file (never used as a key, never written through)",
			func() Result { return s.ctl("pending-"+addr, ha, nil, "endpoint", "create", addr, "--out", out) }, c.want, g, out)
		absent(addr)
	}

	// mash key create.
	for _, st := range states {
		dir, tgt := s.dir("key")
		seed := filepath.Join(dir, "k.seed")
		g, err := plant(st, seed, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the seed path of mash key create", err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mash key create --out over "+string(st), func() Result { return s.mash("key-over", he, a.Conn(), "key", "create", "--out", seed) },
			refusedWordsRE, g, filepath.Join(dir, "k.pub"))
	}
	for _, st := range []pathState{psFile, psSymlink} {
		dir, tgt := s.dir("keypub")
		seed := filepath.Join(dir, "k.seed")
		pub := filepath.Join(dir, "k.pub")
		g, err := plant(st, pub, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the public key path of mash key create", err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mash key create --out with "+string(st)+" at the public key file: no seed is written either",
			func() Result { return s.mash("key-over-pub", he, a.Conn(), "key", "create", "--out", seed) }, refusedWordsRE, g, seed)
	}
	{
		dir, tgt := s.dir("keypubout")
		seed := filepath.Join(dir, "k.seed")
		pubOut := filepath.Join(dir, "elsewhere.pub")
		g, err := plant(psSymlink, pubOut, tgt)
		if a.C.Check("files setup: a symlink at the --pub-out path of mash key create", err == nil, errDetail(err)) {
			s.refuseOver("mash key create --out --pub-out over a symlink: no seed is written either",
				func() Result {
					return s.mash("key-over-pubout", he, a.Conn(), "key", "create", "--out", seed, "--pub-out", pubOut)
				}, refusedWordsRE, g, seed)
		}
	}

	// mash credential create.
	for _, st := range states {
		dir, tgt := s.dir("cred")
		out := filepath.Join(dir, "d.creds")
		g, err := plant(st, out, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the creds path of mash credential create --out", err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mash credential create --out over "+string(st), func() Result {
			return s.mash("cred-over", he, a.Conn(), "credential", "create", "--seed", s.design.seed, "--jwt", s.design.jwt, "--out", out)
		}, refusedWordsRE, g)
	}
	for _, st := range []pathState{psFile, psSymlink} {
		_, tgt := s.dir("cred-default")
		path := filepath.Join(he.Config, "alice.dev.design.creds")
		_ = os.MkdirAll(he.Config, 0o700)
		g, err := plant(st, path, tgt)
		if !a.C.Check("files setup: "+string(st)+" at the default creds path of mash credential create (the config folder)", err == nil, errDetail(err)) {
			continue
		}
		s.refuseOver("mash credential create (no --out: the config folder) over "+string(st), func() Result {
			return s.mash("cred-default-over", he, a.Conn(), "credential", "create", "--seed", s.design.seed, "--jwt", s.design.jwt)
		}, refusedWordsRE, g)
		_ = os.Remove(path)
	}
	{ // --force replaces a symlink itself: it never writes through it
		dir, tgt := s.dir("cred-force")
		out := filepath.Join(dir, "d.creds")
		g, err := plant(psSymlink, out, tgt)
		if a.C.Check("files setup: a symlink at the creds path for --force", err == nil, errDetail(err)) {
			tg := takeGuard(g[1])
			r := s.mash("cred-force", he, a.Conn(), "credential", "create", "--seed", s.design.seed, "--jwt", s.design.jwt, "--out", out, "--force")
			after := takeGuard(g[1])
			now := takeGuard(out)
			a.C.Check("files: mash credential create --force over a symlink replaces the link and never writes through it (the target is byte-identical)",
				r.Code == 0 && tg.Diff(after) == "" && now.Kind == "file", exitDetail(r)+"; target: "+orNone(tg.Diff(after))+"; path is now a "+now.Kind)
		}
	}
}

// join probes for core's `mash join`. Its secret files (the join file it reads, the creds it writes) are not tested
// here until it exists: the check is pending, and when it exists the check fails until the real checks are written.
func (s *at4) join() {
	a := s.a
	r := s.mash("probe-join", s.he, a.Conn(), "join", "--help")
	exists := r.Code == 0
	name := "files: mash join refuses to write its credentials over an existing file or a symlink, and prints no secret"
	if exists {
		a.C.Check(name, false, "mash join exists in this build and AT-4 has no checks for it: write them (file, symlink, dangling symlink; no secret in output), then remove join-secrets from at4Pending")
		return
	}
	a.pend("join-secrets", name, false, "mash join --help: "+exitDetail(r))
}

// ---- check 1: the wide command set ------------------------------------------------------------------------------

func (s *at4) wide() {
	a, ha, he, hn := s.a, s.ha, s.he, s.hn
	join := filepath.Join(ha.Dir, "alice.dev.design.jwt.join")
	setup := filepath.Join(a.srvDir, fileSetupYAML)
	wpub := filepath.Join(s.work, "wide.pub")
	wseed := filepath.Join(s.work, "wide.seed")
	r := s.mash("wide-key", he, a.Conn(), "key", "create", "--out", wseed, "--json", "--verbose")
	a.C.Check("wide: mash key create --json --verbose succeeds", r.Code == 0, exitDetail(r))
	a.Watch("seed wide", wseed)
	wpub = strings.TrimSuffix(wseed, ".seed") + ".pub"

	type c struct {
		label string
		mash  bool
		env   []string
		args  []string
	}
	dEnv := s.designEnv
	cmds := []c{
		// mash, as design
		{"mash-status", true, dEnv, []string{"status"}}, {"mash-status-json", true, dEnv, []string{"status", "--json"}}, {"mash-status-verbose", true, dEnv, []string{"status", "--verbose"}},
		{"mash-doctor", true, dEnv, []string{"doctor"}}, {"mash-doctor-json", true, dEnv, []string{"doctor", "--json"}}, {"mash-doctor-verbose", true, dEnv, []string{"doctor", "--verbose"}},
		{"mash-who", true, dEnv, []string{"who"}}, {"mash-who-json", true, dEnv, []string{"who", "--json"}}, {"mash-who-verbose", true, dEnv, []string{"who", "--verbose"}},
		{"mash-send", true, dEnv, []string{"send", "alice.dev.review", "wide-set hello"}}, {"mash-send-json", true, dEnv, []string{"send", "alice.dev.review", "wide-set again", "--json", "--verbose"}},
		{"mash-inbox", true, dEnv, []string{"inbox"}}, {"mash-inbox-verbose", true, dEnv, []string{"inbox", "--verbose"}},
		{"mash-read-missing", true, dEnv, []string{"read", "9999"}}, {"mash-read-missing-json", true, dEnv, []string{"read", "9999", "--json", "--verbose"}},
		{"mash-version-json", true, dEnv, []string{"version", "--json"}},
		{"mash-key-dry", true, a.Conn(), []string{"key", "create", "--out", filepath.Join(s.work, "dry.seed"), "--dry-run", "--json", "--verbose"}},
		{"mash-cred-json", true, a.Conn(), []string{"credential", "create", "--seed", s.design.seed, "--jwt", s.design.jwt, "--out", filepath.Join(s.work, "wide.creds"), "--json", "--verbose"}},
		{"mash-cred-dry", true, a.Conn(), []string{"credential", "create", "--seed", s.design.seed, "--jwt", s.design.jwt, "--out", filepath.Join(s.work, "wide2.creds"), "--dry-run", "--json"}},
		// failure paths of mash: the wrong kind of file in an option
		{"mash-cred-swapped", true, a.Conn(), []string{"credential", "create", "--seed", s.design.jwt, "--jwt", s.design.seed, "--out", filepath.Join(s.work, "x1.creds"), "--verbose"}},
		{"mash-cred-swapped-json", true, a.Conn(), []string{"credential", "create", "--seed", s.design.jwt, "--jwt", s.design.seed, "--out", filepath.Join(s.work, "x2.creds"), "--json"}},
		{"mash-cred-join-as-seed", true, a.Conn(), []string{"credential", "create", "--seed", join, "--jwt", s.design.jwt, "--out", filepath.Join(s.work, "x3.creds")}},
		{"mash-cred-creds-as-jwt", true, a.Conn(), []string{"credential", "create", "--seed", s.design.seed, "--jwt", s.design.creds, "--out", filepath.Join(s.work, "x4.creds")}},
		{"mash-cred-creds-as-seed", true, a.Conn(), []string{"credential", "create", "--seed", s.design.creds, "--jwt", s.design.creds, "--out", filepath.Join(s.work, "x5.creds"), "--verbose"}},
		{"mash-cred-wrong-pair", true, a.Conn(), []string{"credential", "create", "--seed", s.design.seed, "--jwt", s.review.jwt, "--out", filepath.Join(s.work, "x6.creds"), "--verbose"}},
		{"mash-cred-missing", true, a.Conn(), []string{"credential", "create", "--seed", filepath.Join(s.work, "no-such.seed"), "--jwt", s.design.jwt, "--out", filepath.Join(s.work, "x7.creds")}},
		{"mash-key-missing-dir", true, a.Conn(), []string{"key", "create", "--out", filepath.Join(s.work, "no", "such", "dir", "k.seed"), "--json"}},
		{"mash-key-existing", true, a.Conn(), []string{"key", "create", "--out", s.design.seed, "--verbose"}},
		{"mash-key-existing-json", true, a.Conn(), []string{"key", "create", "--out", s.design.seed, "--json"}},
		{"mash-status-seed-as-creds", true, append(slices.Clone(dEnv), "MASH_CREDS="+s.design.seed), []string{"status", "--verbose"}},
		{"mash-inbox-join-as-creds", true, append(slices.Clone(dEnv), "MASH_CREDS="+join), []string{"inbox", "--verbose"}},
		{"mash-doctor-jwt-as-creds", true, append(slices.Clone(dEnv), "MASH_CREDS="+s.design.jwt), []string{"doctor", "--verbose"}},
		{"mash-who-creds-flag-seed", true, dEnv, []string{"who", "--creds", s.design.seed, "--verbose"}},
		{"mash-status-ca-is-creds", true, append(slices.Clone(dEnv), "MASH_CA="+s.design.creds), []string{"status", "--verbose"}},
		{"mash-inbox-ca-is-seed", true, append(slices.Clone(dEnv), "MASH_CA="+s.design.seed), []string{"inbox", "--verbose"}},
	}
	for _, x := range cmds {
		if x.mash {
			s.mash(x.label, he, x.env, x.args...)
		} else {
			s.ctl(x.label, ha, x.env, x.args...)
		}
	}
	// mashctl
	ctl := func(label string, args ...string) { s.ctl(label, ha, nil, args...) }
	ctl("ctl-status", "status")
	ctl("ctl-status-json", "status", "--json")
	ctl("ctl-status-verbose", "status", "--verbose")
	ctl("ctl-doctor", "doctor")
	ctl("ctl-doctor-json", "doctor", "--json")
	ctl("ctl-doctor-verbose", "doctor", "--verbose")
	ctl("ctl-limits", "limits", "show")
	ctl("ctl-limits-json", "limits", "show", "--json")
	ctl("ctl-limits-verbose", "limits", "show", "--verbose")
	ctl("ctl-init-dry", "init", "--config", setup, "--dry-run")
	ctl("ctl-init-dry-json", "init", "--config", setup, "--dry-run", "--json", "--verbose")
	ctl("ctl-list", "endpoint", "list")
	ctl("ctl-list-wide-json", "endpoint", "list", "--wide", "--json")
	ctl("ctl-list-verbose", "endpoint", "list", "--verbose")
	ctl("ctl-show", "endpoint", "show", "alice.dev.design")
	ctl("ctl-show-json", "endpoint", "show", "alice.dev.design", "--json")
	ctl("ctl-show-verbose", "endpoint", "show", "alice.dev.design", "--verbose")
	ctl("ctl-inbox", "endpoint", "inbox", "alice.dev.review")
	ctl("ctl-inbox-json", "endpoint", "inbox", "alice.dev.review", "--json", "--verbose")
	ctl("ctl-permissions", "endpoint", "permissions", "alice.dev.design")
	ctl("ctl-version-json", "version", "--json")
	ctl("ctl-create-w1", "endpoint", "create", "alice.dev.w1", "--public-key", wpub, "--out", filepath.Join(s.work, "w1.join"), "--json", "--verbose")
	a.watchFile("w1 join", filepath.Join(s.work, "w1.join"))
	ctl("ctl-create-dry", "endpoint", "create", "alice.dev.w2", "--dry-run", "--json", "--verbose")
	ctl("ctl-create-w3-installs-creds", "endpoint", "create", "alice.dev.w3", "--json", "--verbose")
	a.watchFile("w3 creds", filepath.Join(ha.Config, "alice.dev.w3.creds"))
	ctl("ctl-reissue-w3-json", "endpoint", "reissue", "alice.dev.w3", "--json", "--verbose")
	a.watchFile("w3 creds reissued", filepath.Join(ha.Config, "alice.dev.w3.creds"))
	ctl("ctl-create-w4-out-keymade", "endpoint", "create", "alice.dev.w4", "--out", filepath.Join(s.work, "w4.join"), "--verbose")
	a.watchFile("w4 join", filepath.Join(s.work, "w4.join"))
	ctl("ctl-delete-w3", "endpoint", "delete", "alice.dev.w3", "--yes", "--json", "--verbose")
	ctl("ctl-delete-w4", "endpoint", "delete", "alice.dev.w4", "--yes", "--verbose")
	// failure paths of mashctl, including a secret where something else is expected
	ctl("ctl-show-missing", "endpoint", "show", "alice.dev.nobody", "--json", "--verbose")
	ctl("ctl-create-existing", "endpoint", "create", "alice.dev.design", "--verbose", "--json")
	ctl("ctl-create-deleted", "endpoint", "create", "alice.dev.w3", "--verbose")
	ctl("ctl-create-seed-as-pubkey", "endpoint", "create", "alice.dev.s1", "--public-key", s.design.seed, "--out", filepath.Join(s.work, "s1.join"), "--verbose")
	ctl("ctl-create-join-as-pubkey", "endpoint", "create", "alice.dev.s2", "--public-key", join, "--out", filepath.Join(s.work, "s2.join"), "--json")
	ctl("ctl-create-creds-as-pubkey", "endpoint", "create", "alice.dev.s3", "--public-key", s.design.creds, "--out", filepath.Join(s.work, "s3.join"))
	ctl("ctl-create-jwt-as-pubkey", "endpoint", "create", "alice.dev.s4", "--public-key", s.design.jwt, "--out", filepath.Join(s.work, "s4.join"), "--verbose")
	ctl("ctl-reissue-seed-as-pubkey", "endpoint", "reissue", "alice.dev.design", "--public-key", s.design.seed, "--out", filepath.Join(s.work, "s5.join"), "--verbose")
	ctl("ctl-init-key-as-config", "init", "--config", filepath.Join(a.srvDir, fileAccountNK), "--dry-run", "--verbose")
	ctl("ctl-init-opkey-as-config-json", "init", "--config", filepath.Join(a.srvDir, fileOperatorNK), "--dry-run", "--json")
	ctl("ctl-init-creds-as-config", "init", "--config", filepath.Join(a.srvDir, fileAdminCreds), "--dry-run", "--verbose")
	ctl("ctl-init-cakey-as-config", "init", "--config", filepath.Join(a.srvDir, fileCAKey), "--dry-run")
	ctl("ctl-init-serverkey-as-config", "init", "--config", filepath.Join(a.srvDir, fileServerKey), "--dry-run", "--json")
	ctl("ctl-init-seed-as-config", "init", "--config", s.design.seed, "--dry-run", "--verbose")
	s.ctl("ctl-status-admin-creds-seed", ha, []string{"MASH_ADMIN_CREDS=" + s.design.seed}, "status", "--verbose")
	s.ctl("ctl-doctor-admin-creds-join", ha, []string{"MASH_ADMIN_CREDS=" + join}, "doctor", "--verbose", "--json")
	s.ctl("ctl-list-ca-is-key", ha, []string{"MASH_CA=" + filepath.Join(a.srvDir, fileCAKey)}, "endpoint", "list", "--verbose")
	s.ctl("ctl-list-ca-is-seed-json", ha, []string{"MASH_CA=" + s.design.seed}, "endpoint", "list", "--json", "--verbose")
	s.ctl("ctl-create-no-account-key", hn, []string{"MASH_ADMIN_CREDS=" + filepath.Join(a.srvDir, fileAdminCreds)}, "endpoint", "create", "alice.dev.nk", "--public-key", wpub, "--out", filepath.Join(s.work, "nk.join"), "--verbose")
	s.ctl("ctl-keys-dir-is-creds", ha, []string{"MASH_KEYS_DIR=" + filepath.Join(a.srvDir, "creds")}, "endpoint", "create", "alice.dev.nk2", "--public-key", wpub, "--out", filepath.Join(s.work, "nk2.join"), "--verbose")
	s.ctl("ctl-bad-server", ha, []string{"MASH_SERVER=tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT=2s"}, "endpoint", "list", "--verbose", "--json")
	s.ctl("ctl-wrong-ca", ha, []string{"MASH_CA=" + filepath.Join(a.srvDir, "server.pem")}, "status", "--verbose")

	n := 0
	var crashes []string
	for _, l := range s.res {
		n++
		if crashed(l.R) {
			crashes = append(crashes, l.Label+" exit "+strconv.Itoa(l.R.Code))
		}
	}
	a.C.Check("wide: "+strconv.Itoa(n)+" mash and mashctl commands (--json, --verbose, failure paths, secrets fed to the wrong option) ran and none crashed, hung or panicked", len(crashes) == 0 && n >= 90, strings.Join(crashes, "; "))
	s.a.C.Check("wide: the commands that must work did (status, doctor, who, inbox, send as design; create, reissue and delete by mashctl)",
		s.exitOf("mash-status") == 0 && s.exitOf("mash-who-json") == 0 && s.exitOf("mash-send") == 0 && s.exitOf("mash-inbox") == 0 &&
			s.exitOf("ctl-create-w1") == 0 && s.exitOf("ctl-reissue-w3-json") == 0 && s.exitOf("ctl-delete-w3") == 0 && s.exitOf("ctl-status-json") == 0 && s.exitOf("ctl-limits-json") == 0,
		fmt.Sprintf("status %d who %d send %d inbox %d create %d reissue %d delete %d ctl-status %d limits %d", s.exitOf("mash-status"), s.exitOf("mash-who-json"), s.exitOf("mash-send"), s.exitOf("mash-inbox"),
			s.exitOf("ctl-create-w1"), s.exitOf("ctl-reissue-w3-json"), s.exitOf("ctl-delete-w3"), s.exitOf("ctl-status-json"), s.exitOf("ctl-limits-json")))
	for _, label := range []string{"mash-cred-swapped", "mash-cred-join-as-seed", "mash-cred-creds-as-jwt", "mash-status-seed-as-creds", "ctl-create-seed-as-pubkey", "ctl-init-key-as-config", "ctl-init-creds-as-config", "ctl-init-cakey-as-config"} {
		a.C.Check("wide: feeding a secret to an option that expects something else is refused: "+label, s.exitOf(label) > 0, "exit "+strconv.Itoa(s.exitOf(label)))
	}
}

func (s *at4) exitOf(label string) int {
	for _, l := range s.res {
		if l.Label == label {
			return l.R.Code
		}
	}
	return -99
}

// ---- check 3: mail ----------------------------------------------------------------------------------------------

// at4Sent is one message published for the mail check.
type at4Sent struct {
	Name   string
	Marker string
	Seq    uint64
	Want   string // the trust label the stored message must carry
	Err    string
}

func (s *at4) mail() {
	a := s.a
	design, err := os.ReadFile(s.design.creds)
	if !a.C.Check("mail: the sender's creds are read", err == nil, "") {
		return
	}
	legit, err := sap.IdentityFromCreds(design)
	if !a.C.Check("mail: the sender's identity is built from its own creds file", err == nil, errDetail(err)) {
		return
	}
	skp, _ := nkeys.CreateUser()
	sseed, _ := skp.Seed()
	stranger, err := sap.NewIdentity(sseed, legit.Account)
	if !a.C.Check("mail: a stranger's key (in no directory record) is made, in the same account", err == nil, errDetail(err)) {
		return
	}
	// The recipient's session is up before anything is published, as in AT-001.
	p, _, err := a.StartSession(s.he, "dev", "review", "MASH_CREDS="+s.review.creds)
	if !a.C.Check("mail: the recipient's mash serve session starts and completes the MCP handshake", err == nil, errDetail(err)) {
		return
	}
	nc, err := nats.Connect(a.tlsURL(), nats.RootCAs(a.caPath()), nats.UserCredentials(s.design.creds), nats.CustomInboxPrefix("_INBOX.alice.alice_dev_design"),
		nats.MaxReconnects(0), nats.Timeout(5*time.Second))
	if !a.C.Check("mail: the harness connects with the sender's own credentials", err == nil, errDetail(err)) {
		return
	}
	defer nc.Close()
	to, _ := sap.ParseAddress("alice.dev.review")
	subject := to.Subjects().Mail
	senderKey := sap.KeyOf("alice.dev.design")
	rnd := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	publish := func(kind string) at4Sent {
		m := at4Sent{Name: kind, Marker: "at4-" + kind + "-" + rnd}
		payload := []byte("security boundary probe " + m.Marker)
		hdr, err := craftMail(kind, legit, stranger, senderKey, subject, payload, time.Now())
		if err != nil {
			m.Err = err.Error()
			return m
		}
		msg := nats.NewMsg(subject)
		msg.Data = payload
		if hdr != "" {
			msg.Header.Set(sap.HeaderName, hdr)
		}
		msg.Header.Set("Nats-Msg-Id", m.Marker)
		rep, err := nc.RequestMsg(msg, 5*time.Second)
		if err != nil {
			m.Err = err.Error()
			return m
		}
		var ack struct {
			Seq   uint64 `json:"seq"`
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
		}
		if json.Unmarshal(rep.Data, &ack) != nil || ack.Error != nil || ack.Seq == 0 {
			m.Err = "no stored acknowledgement: " + redact(string(rep.Data))
			return m
		}
		m.Seq = ack.Seq
		return m
	}
	var sent []at4Sent
	for _, c := range at4MailCases {
		m := publish(c.Name)
		m.Want = c.Trust
		sent = append(sent, m)
	}
	ctrl := publish("control")
	ctrl.Want = "verified"
	sent = append(sent, ctrl)
	for _, m := range sent {
		a.C.Check("mail: the "+m.Name+" message was published with the sender's creds and stored by the recipient's inbox stream", m.Err == "" && m.Seq > 0, orNone(m.Err)+" seq "+strconv.FormatUint(m.Seq, 10))
	}
	if ctrl.Seq == 0 {
		return
	}
	// The control message must reach the session; stream order means the earlier ones have been looked at by then.
	got := p.WaitFor(30*time.Second, func(out, _ string) bool { return strings.Contains(out, ctrl.Marker) })
	a.C.Check("mail: the properly signed control message is delivered to the recipient's session (it proves the crafted headers are well formed)", got, "")
	time.Sleep(2 * time.Second) // anything the session would still show for the others
	serveOut := p.Stdout()
	s.a.procCheck("while the recipient's mash serve session is live")
	p.Stop() // the CLI reads below are the same endpoint: they must not take its lease from a live session
	listText := ""
	var inboxCodes []int
	for _, args := range [][]string{{"inbox"}, {"inbox", "--limit", "100"}} {
		r := s.mash("review-"+strings.Join(args, "-"), s.he, s.reviewEnv, args...)
		listText += r.Stdout + "\n" + r.Stderr + "\n"
		inboxCodes = append(inboxCodes, r.Code)
	}
	readText := map[string]string{}
	readCodes := map[string]int{}
	for _, m := range sent {
		r := s.mash("review-read-"+m.Name, s.he, s.reviewEnv, "read", strconv.FormatUint(m.Seq, 10))
		readText[m.Name] = r.Stdout + "\n" + r.Stderr
		readCodes[m.Name] = r.Code
	}
	ir := s.ctl("ctl-inbox-review", s.ha, nil, "endpoint", "inbox", "alice.dev.review", "--json", "--limit", "100")
	a.C.Check("mail: mashctl endpoint inbox alice.dev.review --json succeeds", ir.Code == 0, exitDetail(ir))
	shownAnywhere := func(m at4Sent) (bool, string) {
		where := []string{}
		if strings.Contains(serveOut, m.Marker) {
			where = append(where, "the session")
		}
		if strings.Contains(listText, m.Marker) {
			where = append(where, "mash inbox")
		}
		if strings.Contains(readText[m.Name], m.Marker) {
			where = append(where, "mash read")
		}
		return len(where) > 0, strings.Join(where, ", ")
	}
	allDropped := true
	var shownDetail []string
	for _, m := range sent[:len(sent)-1] {
		// What holds today: stored as it is, with the right label, and never shown as verified.
		tr, found := storedTrust(ir.Stdout, m.Seq)
		a.C.Check("mail: mashctl endpoint inbox shows the stored "+m.Name+" message (#"+strconv.FormatUint(m.Seq, 10)+") with trust "+m.Want, found && tr == m.Want, "trust "+orNone(tr))
		bad := mislabelled(map[string]string{"the session": serveOut, "mash inbox": listText, "mash read": readText[m.Name]}, m.Marker, m.Want)
		a.C.Check("mail: the "+m.Name+" message is never presented to the recipient as verified, nor under any label but "+m.Want+" (session, mash inbox, mash read)", bad == "", bad)
		if shown, where := shownAnywhere(m); shown {
			allDropped = false
			shownDetail = append(shownDetail, m.Name+" shown in "+where)
		}
	}
	tr, found := storedTrust(ir.Stdout, ctrl.Seq)
	a.C.Check("mail: mashctl endpoint inbox shows the control message with trust verified", found && tr == "verified", "trust "+orNone(tr))
	cs, cd := controlEverywhere(ctrl.Marker, serveOut, listText, inboxCodes, readText[ctrl.Name], readCodes[ctrl.Name])
	a.C.Check("mail: the control message is shown to the recipient in all three places (the session, mash inbox and mash read, both commands exiting 0)", cs, cd)
	// What the specification adds: unverified mail is not shown at all (and is counted by doctor).
	dr := s.mash("review-doctor", s.he, s.reviewEnv, "doctor")
	counted := regexp.MustCompile(`refused\s+warn\s+\d+ unverified message`).MatchString(dr.Stdout)
	a.pend("unverified-refused", "mail: absent, claimed, unknown-signer and stale mail never reaches the recipient's session, mash inbox or mash read, and mash doctor counts what was refused",
		cs && allDropped && counted, orNone(strings.Join(shownDetail, "; "))+"; doctor counts the refusals: "+strconv.FormatBool(counted))
}

// ---- check 4: plain text ----------------------------------------------------------------------------------------

func (s *at4) plain() {
	a := s.a
	const remote = "nats://192.0.2.7:4222" // a documentation address: never a real server; the refusal comes first
	env := func(extra ...string) []string {
		return append(append(a.Conn(), "MASH_SERVER="+remote, "MASH_OPEN_TIMEOUT=5s"), extra...)
	}
	type call struct {
		name string
		run  func(extra []string) Result
	}
	calls := []call{
		{"mash inbox", func(x []string) Result {
			return a.Mash("plain-remote-inbox", s.he, append(slices.Clone(s.designEnv), x...), "inbox")
		}},
		{"mash status", func(x []string) Result {
			return a.Mash("plain-remote-status", s.he, append(slices.Clone(s.designEnv), x...), "status")
		}},
		{"mashctl endpoint list", func(x []string) Result {
			return a.Ctl("plain-remote-ctl", s.ha, append(env(), x...), "endpoint", "list")
		}},
		{"mashctl doctor", func(x []string) Result { return a.Ctl("plain-remote-ctl-doctor", s.ha, append(env(), x...), "doctor") }},
	}
	for _, c := range calls {
		t0 := time.Now()
		r := c.run([]string{"MASH_SERVER=" + remote, "MASH_OPEN_TIMEOUT=5s"})
		took := time.Since(t0)
		ok, d := plainRefused(r, took, 3*time.Second, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
		a.C.Check("plain text: "+c.name+" refuses a plain nats:// URL to a non-loopback address with no override, before connecting (no wait, no connection error)", ok, d)
		s.run("plain-"+c.name, r)
	}
	// The behaviour of R1-2: the override, its warning, and the old name refused. Written against spec/rules.json.
	cc, err := newConnCounter()
	if !a.C.Check("plain text: a counting listener is made on a loopback port", err == nil, errDetail(err)) {
		return
	}
	defer cc.Close()
	lurl := "nats://" + cc.Addr()
	var held = true
	var detail []string
	for _, c := range calls[:3] {
		var lenv []string
		lenv = []string{"MASH_SERVER=" + lurl, "MASH_OPEN_TIMEOUT=2s"}
		before := cc.Count()
		r := c.run(append(slices.Clone(lenv), "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1"))
		ok, d := dangerousWarned(r, cc.Count()-before)
		held = held && ok
		detail = append(detail, c.name+" with the override: "+d)
		before = cc.Count()
		r = c.run(append(slices.Clone(lenv), "MASH_INSECURE_PLAINTEXT=1"))
		ok, d = oldNameRefused(r, cc.Count()-before)
		held = held && ok
		detail = append(detail, c.name+" with the old name: "+d)
	}
	a.pend("plain-text-override", "plain text: with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 the connection is made and the exact warning is on stderr; MASH_INSECURE_PLAINTEXT is refused naming the new variable (mash inbox, mash status, mashctl endpoint list)",
		held, strings.Join(detail, "; "))
	// The no-override refusal also holds for a loopback address.
	for _, c := range calls[:3] {
		before := cc.Count()
		t0 := time.Now()
		r := c.run([]string{"MASH_SERVER=" + lurl, "MASH_OPEN_TIMEOUT=5s"})
		ok, d := plainRefused(r, time.Since(t0), 3*time.Second, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION")
		a.C.Check("plain text: "+c.name+" refuses a plain nats:// URL to loopback with no override, and no connection reaches the address", ok && cc.Count() == before, d+"; connections "+strconv.FormatInt(cc.Count()-before, 10))
	}
}

// ---- check 5: a deleted endpoint's credential -------------------------------------------------------------------

func (s *at4) deleted() {
	a := s.a
	tmp, ok := a.provision(s.ha, s.he, "alice", "dev", "temp", filepath.Join(s.he.Dir, "temp.creds"), false)
	if !ok {
		return
	}
	a.watchFile("temp creds", tmp.creds)
	// endpoint delete retires the creds file in the config directory: the revoked key is kept at a private path first.
	kept := filepath.Join(a.Dir, "temp-before-delete.creds")
	b, err := os.ReadFile(tmp.creds)
	if err == nil {
		err = os.WriteFile(kept, b, 0o600)
	}
	a.C.Check("deleted: the endpoint's creds file is copied to a private path before the delete", err == nil, "")
	a.watchFile("temp creds copy", kept)
	env := a.SessionEnv(s.he, "dev", "temp", "MASH_CREDS="+kept)
	direct := func() error {
		nc, err := nats.Connect(a.tlsURL(), nats.RootCAs(a.caPath()), nats.UserCredentials(kept), nats.MaxReconnects(0), nats.Timeout(5*time.Second))
		if err == nil {
			err = nc.Flush()
			nc.Close()
		}
		return err
	}
	r := s.mash("deleted-before-inbox", s.he, env, "inbox")
	a.C.Check("deleted: the control, before the delete, mash inbox with the credential is accepted", r.Code == 0, exitDetail(r))
	a.C.Check("deleted: the control, before the delete, a direct NATS connection with the credential is accepted", direct() == nil, "")
	r = s.ctl("deleted-delete", s.ha, nil, "endpoint", "delete", "alice.dev.temp", "--yes")
	a.C.Check("deleted: mashctl endpoint delete alice.dev.temp succeeds", r.Code == 0, exitDetail(r))
	var last Result
	deadline := time.Now().Add(40 * time.Second)
	for {
		last = s.mash("deleted-after-inbox", s.he, env, "inbox")
		if (last.Code != 0 && authRE.MatchString(last.Text())) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	a.C.Check("deleted: after the delete mash inbox with the credential is refused as not authorized (not a TLS or reachability failure)",
		last.Code != 0 && authRE.MatchString(last.Text()) && !regexp.MustCompile(`(?i)x509|certificate|no servers available|connection refused`).MatchString(last.Text()), exitDetail(last))
	derr := direct()
	a.C.Check("deleted: after the delete a direct NATS connection with the credential is refused with an authorization violation", derr != nil && strings.Contains(strings.ToLower(derr.Error()), "authorization"), errDetail(derr))
	// the same credential still names an address that cannot be made again
	r = s.ctl("deleted-recreate", s.ha, nil, "endpoint", "create", "alice.dev.temp", "--public-key", strings.TrimSuffix(tmp.seed, ".seed")+".pub", "--out", filepath.Join(s.work, "temp-again.join"))
	ok, d := recreateRefused(r)
	a.C.Check("deleted: the deleted address cannot be made again with the same key (refused because it was deleted or exists, not for another reason)", ok, d)
}

// ---- the scans of everything ------------------------------------------------------------------------------------

func (s *at4) finalScans() {
	a := s.a
	leaks := scanResults(s.res, a.secrets)
	a.C.Check("INV-001: no output of the "+strconv.Itoa(len(s.res))+" mash and mashctl commands of AT-4 holds a registered secret or a seed- or JWT-shaped string (checked on the captured results)", len(leaks) == 0, leakList(leaks))
	all := a.R.Runner.Argvs()
	argvs := all[min(s.runStart, len(all)):]
	al := scanArgvs(argvs, a.secrets)
	a.C.Check("argv: none of the "+strconv.Itoa(len(argvs))+" command lines of the run holds a registered secret or a seed- or JWT-shaped string (a secret on the command line shows in ps)", len(al) == 0, leakList(al))
	s.a.procCheck("at the end of the run")
	seeds := map[string][]byte{} // the seeds that live on the endpoint machine only
	for k, v := range a.secrets {
		if strings.HasPrefix(k, "design seed") || strings.HasPrefix(k, "review seed") || strings.HasPrefix(k, "seed ") {
			seeds[k] = v
		}
	}
	hits, err := scanFor(a.srvDir, seeds)
	a.C.Check("a search of the server directory for the endpoints' seeds finds nothing (the keys stay on the endpoint machine)", err == nil && len(hits) == 0, hitFiles(hits))
	hits, err = scanFor(s.ha.Dir, seeds)
	a.C.Check("a search of the mashctl host's whole home for the endpoints' seeds finds nothing", err == nil && len(hits) == 0, hitFiles(hits))
	a.C.Check("the scan covered "+strconv.Itoa(len(a.secrets))+" registered secrets (their labels are not recorded)", len(a.secrets) >= 20, "")
}
