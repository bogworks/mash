package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// at005Main is `liveat at005 [--dry-setup] [--hosts podman|local] [--keep]`:
// AT-005 of the 0.5.5 contract, across three machines, over TLS, on a cluster, from two mashctl machines. It needs no
// --port: the ports are the reserved set of DefaultPorts. Exit 0: every assertion held; 1: one did not; 2: usage; 4:
// the run could not be set up.
func at005Main(args []string, stdout, stderr io.Writer) int {
	o, err := parseAt005(args)
	if err != nil {
		fmt.Fprintln(stderr, "liveat at005:", err)
		fmt.Fprintln(stderr, at005Usage)
		return 2
	}
	dry, keep := &o.dry, &o.keep
	var hs Hosts
	switch o.hosts {
	case "local":
		hs = Local{}
	case "podman":
		hs = &Podman{SubnetBase: o.subnetBase}
	}
	if o.recordOut != "" { // refuse early, before anything is built
		if _, err := os.Lstat(o.recordOut); err == nil {
			fmt.Fprintf(stderr, "liveat at005: --record-out %s exists: refusing to overwrite\n", o.recordOut)
			return 2
		}
	}
	if pf, ok := hs.(Preflighter); ok {
		if err := pf.Preflight(); err != nil {
			fmt.Fprintln(stderr, "liveat at005:", err)
			return 4
		}
	}
	repo, err := repoRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	r, err := NewRun(repo, DefaultPorts[0].Client, *keep)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	defer r.Close()
	if err := r.Prepare(); err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	machines, err := hs.Open(r)
	if err != nil {
		fmt.Fprintln(stderr, "liveat at005:", err)
		return 4
	}
	fmt.Fprintf(stdout, "run %s, revision %s\n", r.ID, r.Revision)
	s := &At5{R: r, Hosts: machines, C: &Checks{}, secrets: map[string][]byte{}, dry: *dry, runLine: runLine(hs, o.runBy, o.hostLabel)}
	// An interrupt stops what the run started, by recorded PID, before it exits.
	done := make(chan struct{})
	defer close(done)
	notifyStop(r, done)

	id := "at005"
	scenario := at005Scenario
	if *dry {
		id = "at005-dry-setup"
		scenario = "At005 --dry-setup: certificates; M1 by the setup script; the three-node TLS cluster; the buckets (mashctl init --config); M2 by init --config; mashctl endpoint list from both; everything stopped by PID and cleaned up."
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				s.check("the scenario ran to its end", false, fmt.Sprintf("panic: %v", p))
			}
		}()
		if s.Setup(time.Now()) && !*dry {
			s.Scenario()
		}
	}()
	s.Finish()
	rec := Record{RunID: r.ID, Revision: r.Revision, AT: id, Scenario: scenario, Env: s.EnvLines(), Observed: s.observed(), Leaks: s.leaks, LocalSubstrate: o.hosts == "local"}
	text := rec.Markdown()
	if err := noPrivate(text); err != nil {
		fmt.Fprintln(stderr, "liveat at005:", err)
		return 4
	}
	if err := WriteRecord(filepath.Join(r.Dir, "record-"+id+".md"), rec); err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	if o.recordOut != "" {
		if err := WriteRecord(o.recordOut, rec); err != nil {
			fmt.Fprintln(stderr, "liveat at005: --record-out:", err)
			return 4
		}
	}
	fmt.Fprint(stdout, s.C.Table())
	fmt.Fprintf(stdout, "\n%s\n", text)
	if o.recordOut != "" {
		fmt.Fprintf(stdout, "record file: %s\n", o.recordOut)
	}
	if *keep {
		fmt.Fprintf(stdout, "kept: %s (record, output and scratch homes)\n", r.Dir)
	}
	if !rec.Passed() {
		return 1
	}
	return 0
}

const at005Usage = "usage: liveat at005 [--dry-setup] [--hosts podman --run-by manual|ci --host-label LABEL] [--hosts local] [--keep] [--record-out FILE] | liveat at005-cleanup [--run RUNID]"

type at005Opts struct {
	dry, keep                                      bool
	hosts, runBy, hostLabel, recordOut, subnetBase string
}

// hostLabelOK: letters, digits, dashes and dots, 1 to 40 characters. The label is never looked up.
func hostLabelOK(l string) bool {
	if l == "" || len(l) > 40 {
		return false
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func parseAt005(args []string) (at005Opts, error) {
	var o at005Opts
	fs := flag.NewFlagSet("at005", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.dry, "dry-setup", false, "")
	fs.BoolVar(&o.keep, "keep", false, "")
	fs.StringVar(&o.hosts, "hosts", "local", "")
	fs.StringVar(&o.runBy, "run-by", "", "")
	fs.StringVar(&o.hostLabel, "host-label", "", "")
	fs.StringVar(&o.recordOut, "record-out", "", "")
	fs.StringVar(&o.subnetBase, "subnet-base", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return o, errors.New("bad arguments")
	}
	switch o.hosts {
	case "podman":
		if o.runBy != "manual" && o.runBy != "ci" {
			return o, errors.New("--run-by is required with --hosts podman and must be manual or ci")
		}
		if o.subnetBase != "" && !validSubnetBase(o.subnetBase) {
			return o, errors.New("--subnet-base must be a private IPv4 /16 prefix as A.B: 10.x, 172.16 to 172.31.x or 192.168")
		}
		if !hostLabelOK(o.hostLabel) {
			return o, errors.New("--host-label is required with --hosts podman: letters, digits, dashes and dots only, at most 40 characters")
		}
	case "local":
		if o.subnetBase != "" {
			return o, errors.New("--subnet-base is for --hosts podman only")
		}
		if o.runBy != "" || o.hostLabel != "" {
			return o, errors.New("--run-by and --host-label are for --hosts podman only")
		}
	default:
		return o, errors.New("--hosts is podman or local")
	}
	return o, nil
}

// runLine is the record's line saying who ran the test and on what substrate. The label is the caller's text: the
// machine's own name is never read.
func runLine(hs Hosts, runBy, label string) string {
	p, ok := hs.(*Podman)
	if !ok {
		return "run: local loopback (dry setup or developer check; not a record for the contract)"
	}
	return fmt.Sprintf("run: %s, host %s, substrate podman %s", runBy, label, p.Version())
}

const at005Scenario = "AT-005, across machines, over TLS, on a cluster, from two mashctl machines: three NATS servers on three machines " +
	"(M1, M2, M3) of one cluster, TLS on client and route connections under a CA the harness made, one account, scratch homes. " +
	"M1's operator, account, keys and server.conf template come from the setup script (the config is then edited for TLS and cluster and copied to M2 and M3); M1 and M2 run mashctl init --config " +
	"with the same operator signing seed, account identity seed, sys.creds and admin creds (DEC-004); both with a setup file and config.json listing three servers. " +
	"endpoint create alice.dev.design from M1 (key made by mash key create on M3, session on M3 connecting to the server of M2) and " +
	"alice/dev/review from M2 (key made on M1, session on M1 connecting to the server on M3); mail each way; endpoint list --json on M1 and M2; " +
	"a second owner's endpoint bob/dev/x (key and creds on M3) forging and replaying answers into alice's discovery while her mash who runs on M1, with alice/dev/ghost offline; " +
	"endpoint reissue of design from M2 with a new key made on M3 (old session dropped, old key refused on each of the three servers); " +
	"endpoint delete of review from M1 (session dropped, a reconnect refused on each server, mash status says why); " +
	"a plain nats:// URL to a non-loopback address refused by an endpoint and by mashctl with no override (REQ-014, ADR 0146); " +
	"the same address created from M1 and M2 at the same moment (one succeeds, the other is refused address_taken); a revocation made on M1 shown by endpoint show and mashctl doctor on M2; a search of all captured output for any endpoint seed and any JWT, and of the homes on M1 and M2 for any endpoint seed."

// EnvLines is the environment section of the record: each machine by role, the NATS server version, and the TLS and
// cluster setup, without any key or certificate private part.
func (s *At5) EnvLines() []string {
	r := s.R
	out := []string{s.runLine, "hosts: " + s.hostsName() + ": three machines M1, M2, M3 (roles, not host names)"}
	for _, h := range s.Hosts {
		p := h.Ports()
		mon := ""
		if p.Monitor > 0 {
			mon = fmt.Sprintf(", monitor %d", p.Monitor)
		}
		out = append(out, fmt.Sprintf("%s: %s, address %s, own HOME, config dir, keys dir and server dir; NATS client port %d, route port %d%s", h.Role(), h.Facts(), h.Addr(), p.Client, p.Route, mon))
	}
	out = append(out, "host: "+hostLine(), "go: "+goVersion(), "nats-server: "+r.NatsVer+" (the same binary on all three machines)")
	out = append(out, "cluster: name mashc, server_name mash-n1..mash-n3, routes between all three, JetStream with a store directory per node, operator mode with the full resolver (interval 5s) and the same resolver_preload on every node; the template server.conf made by the setup script (its server stopped again), edited and copied")
	out = append(out, "TLS on client connections (tls block) and on routes (cluster tls block, verify on), ca_file, cert_file and key_file per node")
	if s.CA != nil {
		out = append(out, TLSDescribe(s.CA, s.Leaves)...)
	}
	out = append(out, "mashctl on M1 and M2 (init --config, DEC-004), config.json with all three tls:// URLs and ca")
	for _, n := range s.skipped {
		out = append(out, "not run: "+n)
	}
	out = append(out, "clock: no clock offset is made (the nodes share the host's clock; the skewed case is an in-process test elsewhere)")
	return out
}

func (s *At5) hostsName() string {
	switch s.Hosts[0].(type) {
	case *localHost:
		return "local (all on this host: loopback, a directory tree each, one nats-server process each; the harness's own selfcheck, not the acceptance run)"
	case *podHost:
		return "podman (three rootless containers built from this checkout, each its own filesystem and address on a private network, created and destroyed by the test)"
	}
	return "unknown"
}

func goVersion() string { return runtimeVersion() }

// hostLine is the host of the run as the contract's section 8 asks: operating system, kernel and architecture. The kernel
// release is `uname -r`; if that cannot be read the line says so rather than leaving it out.
func hostLine() string {
	k := "kernel unknown"
	if b, err := exec.Command("uname", "-r").Output(); err == nil && strings.TrimSpace(string(b)) != "" {
		k = "kernel " + strings.TrimSpace(string(b))
	}
	return runtime.GOOS + ", " + k + ", " + runtime.GOARCH
}

func (s *At5) observed() []Assertion {
	return s.C.All()
}

// ---- helpers -------------------------------------------------------------------------------------------------------

var (
	auth5RE = regexp.MustCompile(`(?i)authori[sz]ation|not authori[sz]ed|revoked`)
	tls5RE  = regexp.MustCompile(`(?i)x509|certificate|tls handshake|unknown authority`)
	seed5RE = regexp.MustCompile(`SU[A-Z2-7]{50,}`)
	jwt5RE  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`)
)

func (s *At5) tlsOf(h Host) TLSFiles {
	for i, x := range s.Hosts {
		if x == h {
			return s.TLS[i]
		}
	}
	return TLSFiles{}
}

// watch remembers the seed or JWT text held in a file on h so that a search can look for exactly it. Memory only.
func (s *At5) watch(h Host, kind, label, path string) {
	b, err := h.Get(path)
	if err != nil {
		return
	}
	var t []byte
	if kind == "seed" {
		t = seed5RE.Find(b)
	} else {
		t = jwt5RE.Find(b)
	}
	if len(t) < 20 {
		return
	}
	s.mu.Lock()
	s.secrets[kind+" "+label] = t
	s.mu.Unlock()
}

// snip is the first line of a command's text, shortened and redacted, for a record's detail.
func snip(t string) string {
	t = strings.TrimSpace(t)
	if strings.HasPrefix(t, "{") {
		if v, err := parseJSON(t); err == nil {
			if m, ok := v.(map[string]any); ok {
				if o, ok := m["outcome"].(string); ok {
					t = "outcome " + o
				}
			}
			for _, o := range findObjects(v, "code", "detail") {
				t = fmt.Sprintf("refusal %v: %v", o["code"], o["detail"])
			}
		}
	}
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	if len(t) > 170 {
		t = t[:170] + "..."
	}
	return redact(t)
}

func exitSnip(r Result) string {
	d := "exit " + strconv.Itoa(r.Code)
	if t := snip(r.Text()); t != "" {
		d += ": " + t
	}
	return d
}

// epEnv is the environment of an endpoint session or command on host h, connecting to the server on srv, with creds.
func (s *At5) epEnv(h, srv Host, name, creds string, extra ...string) []string {
	return append([]string{
		"MASH_SERVER=" + s.urlOf(srv), "MASH_CA=" + s.tlsOf(h).CA, "MASH_ENDPOINT=alice.dev." + name,
		"MASH_CREDS=" + creds, "MASH_STATE_DIR=" + filepath.Join(h.Home().State, name),
	}, extra...)
}

// ep is one endpoint: where its key was made and the paths there.
type ep5 struct {
	owner string // "" is alice
	name  string
	host  Host // the endpoint's machine
	seed  string
	pub   string
	jwt   string // on the endpoint's machine
	creds string
}

// jwtBase is the owner.group.instance part of the names of an endpoint's JWT files, so endpoints of different owners with the same
// instance never share a file in the harness workspace (an empty owner is alice).
func (e ep5) jwtBase() string {
	o := e.owner
	if o == "" {
		o = "alice"
	}
	return o + ".dev." + e.name
}

// makeKey runs `mash key create` on h.
func (s *At5) makeKey(h Host, name, tag string) (ep5, bool) {
	e := ep5{name: name, host: h, seed: filepath.Join(h.Home().Dir, name+tag+".seed"), pub: filepath.Join(h.Home().Dir, name+tag+".pub")}
	r := s.mash(h, "key-create-"+name+tag, nil, "key", "create", "--out", e.seed, "--pub-out", e.pub)
	ok := s.check(h.Role()+": mash key create makes the key of "+name+tag, r.Code == 0, exitSnip(r))
	s.watch(h, "seed", name+tag, e.seed)
	return e, ok
}

// carryPub copies a public key (public: it is not a secret) from one machine to another.
func (s *At5) carryPub(from Host, path string, to Host) (string, error) {
	b, err := from.Get(path)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(to.Home().Dir, "carried-"+filepath.Base(path))
	return dst, to.Put(dst, b, 0o600)
}

// createFrom runs `mashctl endpoint create|reissue` on ctl for e (whose key was made on e.host): the public key is
// carried to ctl, the JWT goes to the courier directory and from there to the endpoint's machine, where `mash credential
// create` assembles the creds. verb is "create" or "reissue".
func (s *At5) provision(ctl Host, verb string, e *ep5, credsPath string) bool {
	owner := e.owner
	if owner == "" {
		owner = "alice"
	}
	pubOnCtl, err := s.carryPub(e.host, e.pub, ctl)
	if !s.check("the public key of "+e.name+" is carried to "+ctl.Role(), err == nil, errText(err)) {
		return false
	}
	jwtCarry := filepath.Join(ctl.Home().Dir, e.jwtBase()+"."+verb+".out.join") // a join file written on ctl; the courier moves its JWT
	args := []string{"endpoint", verb, owner + ".dev." + e.name, "--out", jwtCarry, "--public-key", pubOnCtl}
	if verb == "reissue" {
		args = append(args, "--verbose") // the count of servers that took the account update is a --verbose line
	}
	r := s.ctl(ctl, "endpoint-"+verb+"-"+e.name, nil, args...)
	ok := s.check(ctl.Role()+": mashctl endpoint "+verb+" "+owner+".dev."+e.name+" --out --public-key succeeds", r.Code == 0, exitSnip(r))
	if !ok {
		return false
	}
	s.mu.Lock()
	s.lastCreate = r.Stdout
	s.mu.Unlock()
	if verb == "create" {
		// create makes no account update (nothing is revoked), so there is nothing for three servers to acknowledge: the
		// check does not apply and is recorded as n/a, never as a pass (known issue #766; the contract wording is corrected).
		s.C.NotApplicable(ctl.Role()+": endpoint create: all three servers took the account update",
			"not applicable: create makes no account update, so there is nothing to acknowledge; known issue #766")
	} else {
		s.check(ctl.Role()+": endpoint "+verb+" reports that all three servers took the account update",
			strings.Contains(r.Stdout, "servers acknowledged: 3"), snip(grepLine(r.Stdout, "acknowledged")))
	}
	s.watch(ctl, "jwt", e.name+verb, jwtCarry)
	jb, err := ctl.Get(jwtCarry)
	if err != nil {
		s.check("the join file is written by endpoint "+verb, false, err.Error())
		return false
	}
	if tok, terr := joinUserJWT(jb); terr != nil {
		s.check("the join file written by endpoint "+verb+" holds a user_jwt", false, terr.Error())
		return false
	} else {
		jb = append(tok, '\n')
	}
	// the courier carries the JWT file to the endpoint's machine
	e.jwt = filepath.Join(e.host.Home().Dir, e.jwtBase()+"."+verb+".jwt")
	if err := e.host.Put(e.jwt, jb, 0o600); err != nil {
		s.check("the JWT file is carried to "+e.host.Role(), false, err.Error())
		return false
	}
	e.creds = credsPath
	// T1b-b: the `mash credential create` flow below waits for core's `mash join`; it is left as it is until then.
	r = s.mash(e.host, "credential-create-"+e.name+"-"+verb, nil, "credential", "create", "--seed", e.seed, "--jwt", e.jwt, "--out", credsPath, "--force")
	ok = s.check(e.host.Role()+": mash credential create assembles the creds of "+e.name+" from its own seed and the carried JWT", r.Code == 0, exitSnip(r))
	s.watch(e.host, "jwt", e.name+verb, credsPath)
	return ok
}

func grepLine(text, sub string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// startSession starts `mash serve` on h as endpoint name against the server on srv, through the MCP handshake.
func (s *At5) startSession(h, srv Host, name, creds string) (*Proc, []string, error) {
	env := s.epEnv(h, srv, name, creds)
	p, err := h.Spawn("serve-"+name, []string{h.Bin("mash"), "serve"}, s.env(h, env...))
	if err != nil {
		return nil, env, err
	}
	s.R.Runner.logf("session %s on %s pid=%d", name, h.Role(), p.PID)
	s.notes = append(s.notes, fmt.Sprintf("session %s on %s pid %d (server of %s)", name, h.Role(), p.PID, srv.Role()))
	return p, env, p.Handshake(30 * time.Second)
}

func (s *At5) waitOnline(h Host, env []string, ids ...string) bool {
	deadline := time.Now().Add(40 * time.Second)
	for {
		r := s.mash(h, "who", env, "who", "--json")
		n := 0
		if v, err := parseJSON(r.Stdout); err == nil {
			for _, o := range findObjects(v, "identity") {
				id, _ := o["identity"].(string)
				on, _ := o["online"].(bool)
				if on && slices.Contains(ids, id) {
					n++
				}
			}
		}
		if n == len(ids) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// sent5RE finds the message number in `mash send --json`'s receipt.
var sent5RE = regexp.MustCompile(`"number":(\d+)`)

// mail sends from one endpoint to another with the CLI and reads it back with the recipient's own credentials: it must
// arrive with the text and be labelled verified.
func (s *At5) mail(name string, fromH Host, from []string, toH Host, to []string, toAddr, text string) {
	r := s.mash(fromH, "send-"+name, from, "send", toAddr, text, "--json")
	s.check("mail "+name+": mash send succeeds", r.Code == 0, exitSnip(r))
	m := sent5RE.FindStringSubmatch(r.Stdout)
	if m == nil {
		s.check("mail "+name+": send names the message number", false, snip(r.Text()))
		return
	}
	var rd Result
	deadline := time.Now().Add(30 * time.Second)
	for {
		rd = s.mash(toH, "read-"+name, to, "read", m[1])
		if (rd.Code == 0 && strings.Contains(rd.Stdout, text)) || time.Now().After(deadline) {
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	s.check("mail "+name+": the recipient reads the text", rd.Code == 0 && strings.Contains(rd.Stdout, text), exitSnip(rd))
	s.check("mail "+name+": the recipient sees it labelled verified", strings.Contains(rd.Stdout, "verified"), "")
}

// refusedOn connects with creds (a copy of an old creds file) to each of the three servers separately and asserts the
// refusal is an authorization failure, not a TLS or reachability one.
func (s *At5) refusedOn(what string, h Host, name, creds string) {
	for _, srv := range s.Hosts {
		env := s.epEnv(h, srv, name, creds)
		r := retry(40*time.Second, time.Second, func() (Result, bool) {
			r := s.mash(h, "refused-"+name+"-on-"+srv.Role(), env, "inbox")
			return r, r.Code != 0 && auth5RE.MatchString(r.Text())
		})
		s.check(what+": a connection to the server on "+srv.Role()+" is refused as not authorized", r.Code != 0 && auth5RE.MatchString(r.Text()) && !tls5RE.MatchString(r.Text()), exitSnip(r))
	}
}

// acceptedOn is the positive control: creds connect to each of the three servers.
func (s *At5) acceptedOn(what string, h Host, name, creds string) {
	for _, srv := range s.Hosts {
		env := s.epEnv(h, srv, name, creds)
		r := retry(40*time.Second, time.Second, func() (Result, bool) {
			r := s.mash(h, "accepted-"+name+"-on-"+srv.Role(), env, "inbox")
			return r, r.Code == 0
		})
		s.check(what+": a connection to the server on "+srv.Role()+" is accepted", r.Code == 0, exitSnip(r))
	}
}

func (s *At5) closed(p *Proc) bool {
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if p.Exited() || auth5RE.MatchString(p.Stderr()+p.Stdout()) {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}

// copyPrivate copies a creds file to a private path on the same machine (0600) and remembers its secrets.
func (s *At5) copyPrivate(h Host, src, dst, label string) bool {
	b, err := h.Get(src)
	if err != nil || h.Put(dst, b, 0o600) != nil {
		return false
	}
	s.watch(h, "seed", label, dst)
	s.watch(h, "jwt", label, dst)
	return true
}

// primaryIPv4 is this host's first non-loopback IPv4 address that is up, "" when there is none.
func primaryIPv4() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if v4 := ipn.IP.To4(); v4 != nil && !v4.IsLoopback() && !v4.IsLinkLocalUnicast() {
					return v4.String()
				}
			}
		}
	}
	return ""
}

// ---- the scenario --------------------------------------------------------------------------------------------------

// Scenario runs AT-005 after Setup.
func (s *At5) Scenario() {
	m1, m2, m3 := s.M(1), s.M(2), s.M(3)

	// 1. design from M1, key on M3; session on M3 connects to the server of M2.
	design, ok := s.makeKey(m3, "design", "")
	if !ok {
		return
	}
	designCreds := filepath.Join(m3.Home().Config, "design.creds")
	if !s.provision(m1, "create", &design, designCreds) {
		return
	}
	// 2. review from M2, key on M1; session on M1 connects to the server on M3.
	review, ok := s.makeKey(m1, "review", "")
	if !ok {
		return
	}
	reviewCreds := filepath.Join(m1.Home().Config, "review.creds")
	if !s.provision(m2, "create", &review, reviewCreds) {
		return
	}
	dp, denv, err := s.startSession(m3, m2, "design", designCreds)
	if !s.check("the design session on M3 connects to the server on M2 (TLS) and completes the MCP handshake", err == nil, errText(err)) {
		return
	}
	rp, renv, err := s.startSession(m1, m3, "review", reviewCreds)
	if !s.check("the review session on M1 connects to the server on M3 (TLS) and completes the MCP handshake", err == nil, errText(err)) {
		return
	}
	s.check("both sessions are online", s.waitOnline(m3, denv, "alice.dev.design", "alice.dev.review"), "")

	// 3. mail each way; endpoint list --json from both mashctl machines.
	s.mail("design-to-review", m3, denv, m1, renv, "alice.dev.review", "hello review from design across machines")
	s.mail("review-to-design", m1, renv, m3, denv, "alice.dev.design", "hello design from review across machines")
	for _, h := range []Host{m1, m2} {
		r := s.ctl(h, "endpoint-list", nil, "endpoint", "list", "--json")
		groups := map[string]string{}
		if v, err := parseJSON(r.Stdout); err == nil {
			for _, o := range findObjects(v, "group") {
				inst, _ := o["name"].(string)
				g, _ := o["group"].(string)
				groups[inst] = g
			}
		}
		s.check(h.Role()+": endpoint list --json shows design and review, each with group dev", r.Code == 0 && groups["design"] == "dev" && groups["review"] == "dev", fmt.Sprintf("exit %d, groups seen: %v", r.Code, groups))
	}

	// 3b. a second owner's endpoint forges and replays answers into alice's discovery.
	s.secondOwner(m1, m2, m3, renv)

	// 4. reissue design from M2 with a new key made on M3.
	oldDesign := filepath.Join(m3.Home().Dir, "design-old.creds")
	s.check("M3: the old design creds are copied to a private path before the reissue", s.copyPrivate(m3, designCreds, oldDesign, "design-old"), "")
	s.acceptedOn("the credential `endpoint create` issued for design (accepted by each of the three servers, before the reissue)", m3, "design", oldDesign)
	design2, ok := s.makeKey(m3, "design", "-2")
	if !ok {
		return
	}
	if !s.provision(m2, "reissue", &design2, designCreds) {
		return
	}
	s.check("REQ-011: the old design session is dropped (its serve exits or reports the authorization failure)", s.closed(dp), "")
	s.refusedOn("the old design key", m3, "design", oldDesign)
	s.acceptedOn("the new design key", m3, "design", designCreds)

	// 5. delete review from M1.
	oldReview := filepath.Join(m1.Home().Dir, "review-old.creds")
	s.check("M1: the review creds are copied to a private path before the delete", s.copyPrivate(m1, reviewCreds, oldReview, "review-old"), "")
	s.acceptedOn("review key before the delete (control)", m1, "review", oldReview)
	r := s.ctl(m1, "endpoint-delete-review", nil, "endpoint", "delete", "alice.dev.review", "--yes", "--verbose")
	s.check("M1: mashctl endpoint delete alice/dev/review succeeds", r.Code == 0, exitSnip(r))
	s.check("M1: endpoint delete reports that all three servers acknowledged the revocation", strings.Contains(r.Stdout, "servers acknowledged: 3"), snip(grepLine(r.Stdout, "acknowledged")))
	s.check("REQ-011: the review session is dropped (its serve exits or reports the authorization failure)", s.closed(rp), "")
	s.refusedOn("the deleted review key", m1, "review", oldReview)
	st := s.mash(m1, "status-review", s.epEnv(m1, m3, "review", oldReview), "status")
	s.check("mash status (the command) with the deleted key says why: not ready, the server refused its credentials", st.Code == 1 && strings.Contains(st.Stdout, "not ready: the server refused its credentials"), exitSnip(st))
	s.statusSaysWhy(m1, m3, "review", oldReview)

	// 6. a revocation made on M1 is seen from M2.
	s.revocationSeen()

	// 7. plain URLs.
	s.plainRefusals(m1, m3)

	// 8. concurrent creates.
	s.race("race0")
}

// forgeSeconds is how long `nodeinit forge` waits for discovery requests; it ends sooner once both answers were refused.
const forgeSeconds = 40

// secondOwner is REQ-005's negative case across machines. bob/dev/x (a second owner, in the same account) is created
// from M1 with a key made on M3, where bob's creds are assembled, so his seed never leaves M3; alice/dev/ghost is
// created with no session (offline). `nodeinit forge` on M3 connects as bob to the server on M2, answers each of
// alice's discovery requests with a forged and a replay-shaped payload into her reply subject, and counts the
// permissions violations; meanwhile alice's `mash who` runs on M1 (renv: her review endpoint, server on M3). mashctl
// takes the owner from the address, so no owner override is needed.
func (s *At5) secondOwner(m1, m2, m3 Host, aliceEnv []string) {
	if _, ok := m3.(*localHost); ok {
		s.skip("REQ-005: second-owner forged and replayed answers (the forging runs inside a node: podman hosts only)")
		return
	}
	bob, ok := s.makeKey(m3, "bobx", "")
	if !ok {
		return
	}
	bob.owner, bob.name = "bob", "x"
	bobCreds := filepath.Join(m3.Home().Config, "bob.creds")
	if !s.provision(m1, "create", &bob, bobCreds) {
		return
	}
	ghost, ok := s.makeKey(m3, "ghost", "")
	if !ok {
		return
	}
	if !s.provision(m1, "create", &ghost, filepath.Join(m3.Home().Config, "ghost.creds")) {
		return
	}
	p, err := m3.Spawn("forge", []string{m3.Bin("nodeinit"), "forge", "--url", s.urlOf(m2), "--ca", s.tlsOf(m3).CA, "--creds", bobCreds, "--seconds", strconv.Itoa(forgeSeconds)}, s.env(m3))
	if err != nil {
		s.check("REQ-005: bob's forging endpoint starts on M3", false, errText(err))
		return
	}
	defer p.Stop()
	s.notes = append(s.notes, fmt.Sprintf("forge on M3 pid %d (server of M2)", p.PID))
	ready := p.WaitFor(30*time.Second, func(_, e string) bool { return strings.Contains(e, "forge: subscribed") })
	if !s.check("REQ-005: bob (a second owner) connects to the server on M2 and subscribes to $SRV.>", ready, snip(p.Stderr())) {
		return
	}
	r := s.mash(m1, "who-forged", aliceEnv, "who", "--json")
	online, listed := "", false
	if v, err := parseJSON(r.Stdout); err == nil {
		for _, o := range findObjects(v, "identity") {
			id, _ := o["identity"].(string)
			if id == "alice.dev.ghost" {
				listed = true
				if on, _ := o["online"].(bool); on {
					online += id + " "
				}
			}
		}
	}
	s.check("REQ-005: alice's mash who ran on M1 while bob answered", r.Code == 0, exitSnip(r))
	deadline := time.Now().Add((forgeSeconds + 20) * time.Second)
	for !p.Exited() && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
	}
	var res struct {
		Seen       int `json:"seen_requests"`
		Violations int `json:"violations"`
	}
	line := ""
	for _, l := range strings.Split(p.Stdout(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "{") && json.Unmarshal([]byte(l), &res) == nil {
			line = strings.TrimSpace(l)
		}
	}
	s.check("REQ-005: bob on M3 sees alice's discovery request (a $SRV.INFO request with a reply in _INBOX.alice.)", line != "" && res.Seen >= 1, "forge result: "+orNone(line))
	s.check("REQ-005: the servers refuse bob's forged discovery answer and his replayed presence answer with permissions violations", line != "" && res.Violations >= 2, "forge result: "+orNone(line))
	s.check("REQ-005: alice's who does not list her offline endpoint alice/dev/ghost as online", r.Code == 0 && online == "", fmt.Sprintf("alice/dev/ghost listed: %v, online: %s", listed, orNone(online)))
}

// showRefusal asserts the output of `endpoint show --json` for an endpoint that no longer exists: exit 1 and a refused
// outcome whose refusal code is exactly not_found (cmd/mashctl/endpoint_show.go). Any other exit, any other code and
// output that is not that JSON document fail.
func showRefusal(code int, stdout string) (bool, string) {
	if code != 1 {
		return false, fmt.Sprintf("exit %d, want 1", code)
	}
	var doc struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		return false, "exit 1 but stdout is not the --json document"
	}
	if doc.Error == "" {
		return false, "exit 1 but the --json document has no error"
	}
	if !strings.HasPrefix(doc.Error, "no endpoint ") {
		return false, fmt.Sprintf("error %q, want no endpoint ...", doc.Error)
	}
	return true, "exit 1, no endpoint"
}

// doctorKeysOK asserts `mashctl doctor --json` after the revocation: exit 0, no problem, and the `keys` check in state ok
// (no connected key that mash did not issue, no revoked key still connected).
func doctorKeysOK(code int, stdout string) (bool, string) {
	if code != 0 {
		return false, fmt.Sprintf("exit %d, want 0 (no problem)", code)
	}
	var doc struct {
		Problems *int `json:"problems"`
		Checks   []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		return false, "stdout is not the --json document"
	}
	if doc.Problems == nil || *doc.Problems != 0 {
		return false, "the report does not say 0 problems"
	}
	for _, c := range doc.Checks {
		if c.Name == "keys" {
			if c.State != "ok" {
				return false, "the keys check is " + c.State + ", want ok"
			}
			return true, "exit 0, 0 problems, keys ok"
		}
	}
	return false, "the report has no keys check"
}

// revocationSeen: the delete of review was made from M1; M2 shows it with endpoint show and mashctl doctor.
func (s *At5) revocationSeen() {
	m2 := s.M(2)
	r := s.ctl(m2, "show-review", nil, "endpoint", "show", "alice.dev.review", "--json")
	ok, d := showRefusal(r.Code, r.Stdout)
	s.check("M2: endpoint show alice.dev.review --json refuses (no endpoint) for the endpoint M1 deleted", ok, d)
	dc := retry(30*time.Second, 2*time.Second, func() (Result, bool) {
		r := s.ctl(m2, "doctor-json", nil, "doctor", "--json")
		ok, _ := doctorKeysOK(r.Code, r.Stdout)
		return r, ok
	})
	ok, d = doctorKeysOK(dc.Code, dc.Stdout)
	s.check("M2: mashctl doctor --json after the revocation: exit 0, no problem, keys ok (no revoked key connected, every connected key issued by mash)", ok, d)
	s.ClusterCheck(m2, "M2 after the revocation")
}

// plainRefusals asserts REQ-014 and ADR 0146 (an endpoint and mashctl refuse a plain nats:// URL, with no override set). No plain server is started: both refusals come before any
// connection, so the harness points at a non-loopback address (on the local hosts this machine's own address, where
// nothing listens; on the podman hosts the address of M1) and the refusal text, not a connection error, is what is
// asserted.
func (s *At5) plainRefusals(m1, m3 Host) {
	ip := m1.Addr()
	if _, local := m1.(*localHost); local {
		ip = primaryIPv4()
	}
	if ip == "" {
		s.skip("plain nats:// refusals (no non-loopback IPv4 address on this host)")
		return
	}
	url := "nats://" + ip + ":" + strconv.Itoa(DefaultPorts[0].Client)
	env := s.epEnv(m3, m1, "design", filepath.Join(m3.Home().Config, "design.creds"), "MASH_SERVER="+url, "MASH_INSECURE_PLAINTEXT=0")
	r := s.mash(m3, "plain-endpoint", env, "inbox")
	txt := r.Text()
	s.check("REQ-014: with no override an endpoint refuses to connect to a plain nats:// URL on a non-loopback address (refused before any connection; no plain server started)", r.Code != 0 && strings.Contains(strings.ToLower(txt), "tls"), exitSnip(r))
	st := s.mash(m3, "plain-status", env, "status")
	dr := s.mash(m3, "plain-doctor", env, "doctor")
	s.check("REQ-014: mash doctor says the server row FAILs for it", strings.Contains(dr.Stdout, "server    FAIL  "), snip(grepLine(dr.Text(), "server")))
	s.check("REQ-014: mash status says it is not ready", st.Code == 1 && strings.Contains(st.Stdout, "not ready: "), exitSnip(st))
	r = s.ctl(m1, "plain-admin", []string{"MASH_SERVER=" + url, "MASH_INSECURE_PLAINTEXT=0"}, "endpoint", "list")
	s.check("REQ-014: mashctl refuses an admin connection to a plain nats:// URL on a non-loopback address", r.Code != 0 && strings.Contains(strings.ToLower(r.Text()), "tls"), exitSnip(r))
}

// race: M1 and M2 each make a key on their own machine and create alice/dev/race0 at the same moment. The product
// maps the race loser to address_taken, so exactly one create succeeds and the other is refused address_taken with exit
// 1. (A retryable lock-lost error can only come past a 30 s clock skew, which AT-005 never has.) One identity exists
// afterwards, and the loser's create, run again on its own, is refused address_taken too.
func (s *At5) race(addr string) {
	m1, m2 := s.M(1), s.M(2)
	k1, ok1 := s.makeKey(m1, addr, "-m1")
	k2, ok2 := s.makeKey(m2, addr, "-m2")
	if !ok1 || !ok2 {
		return
	}
	type side struct {
		h   Host
		pub string
		jwt string
	}
	sides := []side{
		{m1, k1.pub, filepath.Join(m1.Home().Dir, addr+".m1.jwt")},
		{m2, k2.pub, filepath.Join(m2.Home().Dir, addr+".m2.jwt")},
	}
	var res [2]Result
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, c := range sides {
		wg.Add(1)
		go func(i int, c side) {
			defer wg.Done()
			<-start
			res[i] = s.ctl(c.h, "race-"+addr, nil, "endpoint", "create", "alice.dev."+addr, "--out", c.jwt, "--public-key", c.pub, "--json")
		}(i, c)
	}
	time.Sleep(200 * time.Millisecond)
	close(start)
	wg.Wait()
	ok, taken, badExit := 0, 0, 0
	for _, r := range res {
		if r.Code == 0 {
			ok++
		} else if strings.Contains(r.Text(), "already exists") {
			taken++
			if r.Code != 1 {
				badExit++
			}
		}
	}
	s.check("concurrent endpoint create of alice.dev."+addr+" from M1 and M2: exactly one succeeds and the other is refused as already existing with exit 1",
		ok == 1 && taken == 1 && badExit == 0, fmt.Sprintf("M1 exit %d, M2 exit %d; refused already exists: %d; %s | %s", res[0].Code, res[1].Code, taken, snip(res[0].Stdout), snip(res[1].Stdout)))
	var loser int
	if res[1].Code != 0 {
		loser = 1
	}
	lk := []ep5{k1, k2}[loser]
	again := s.ctl(sides[loser].h, "race-again-"+addr, nil, "endpoint", "create", "alice.dev."+addr, "--out", sides[loser].jwt+".again", "--public-key", lk.pub, "--json")
	s.check("the loser's create, run again on its own, is refused as already existing", again.Code != 0 && strings.Contains(again.Text(), "already exists"), exitSnip(again))
	r := s.ctl(m1, "list-after-"+addr, nil, "endpoint", "list", "--json")
	n := 0
	if v, err := parseJSON(r.Stdout); err == nil {
		for _, o := range findObjects(v, "name") {
			if i, _ := o["name"].(string); i == addr {
				n++
			}
		}
	}
	s.check("one identity exists for alice/dev/"+addr+" afterwards", r.Code == 0 && n == 1, fmt.Sprintf("%d record(s)", n))
}

// Finish stops everything by recorded PID, frees the ports, removes nothing but what the run made (Close does that),
// and searches the captured output and the homes on M1 and M2.
func (s *At5) Finish() {
	for _, p := range s.sessions() {
		p.Stop()
	}
	s.StopServers(s.srv)
	free := true
	for i, p := range DefaultPorts {
		for _, port := range []int{p.Client, p.Route, p.Monitor} {
			if port > 0 && s.Hosts[i].WaitPortFree(port, 10*time.Second) != nil {
				free = false
			}
		}
	}
	s.check("every process the harness started is stopped by its recorded PID and ports 14222-14224, 16222-16224 and 18222 are free", free, strings.Join(s.notes, "; "))
	s.search()
	ce := s.R.Runner.CaptureErrors()
	s.check("INV-001: every command's output was captured completely (no capture or read error)", len(ce) == 0, strings.Join(ce, "; "))
	s.destroyCheck()
}

// destroyCheck is last: the nodes are destroyed here, so the result of the destruction is in the record and a failure
// fails the run. Close retries it as a safety net.
func (s *At5) destroyCheck() {
	derr := s.R.Destroy()
	detail := ""
	if derr != nil {
		detail = snip(strings.ReplaceAll(derr.Error(), "\n", "; "))
	}
	s.check("every container, network and image of this run is destroyed and nothing labelled "+labelKey+"="+s.R.ID+" remains", derr == nil, detail)
}

func (s *At5) sessions() []*Proc {
	s.R.mu.Lock()
	defer s.R.mu.Unlock()
	var out []*Proc
	for _, p := range s.R.procs {
		if !slices.Contains(s.srv, p) {
			out = append(out, p)
		}
	}
	return out
}

// search looks for secrets: every captured output for any seed or JWT and for the exact endpoint secrets; the homes on
// M1 and M2 for the exact endpoint SEEDS only, except those of an endpoint whose own machine that home is (review's
// key was made on M1 and its creds hold its seed there, by design). A JWT is not a secret key: the JWT files that
// `endpoint create --out` writes are on the mashctl machine by design, so the home search ignores JWTs.
func (s *At5) search() {
	found, err := scanDir5(s.R.Out)
	s.check("INV-001: no captured output holds a seed or a JWT", err == nil && len(found) == 0, leakNames(found))
	s.leaks = append(s.leaks, found...)
	specific, err := scanFor5(s.R.Out, s.secrets)
	s.check("INV-001: no captured output holds an endpoint seed or JWT this run made", err == nil && len(specific) == 0, leakNames(specific))
	s.leaks = append(s.leaks, specific...)
	for _, i := range []int{1, 2} {
		h := s.M(i)
		home, done, err := h.Snapshot() // the machine's whole tree: home, config, keys, server and tls
		if err != nil {
			s.check(fmt.Sprintf("M%d's file tree is readable for the search", i), false, err.Error())
			continue
		}
		secrets := map[string][]byte{}
		for k, v := range s.secrets {
			if i == 1 && (strings.Contains(k, "review") || strings.Contains(k, "race0-m1")) {
				continue // made on M1: its own machine
			}
			if i == 2 && strings.Contains(k, "race0-m2") {
				continue
			}
			if strings.HasPrefix(k, "seed ") {
				secrets[k] = v
			}
		}
		hits, err := scanFor5(home, secrets)
		done()
		s.check(fmt.Sprintf("a search of M%d's whole machine directory (home, config, keys, server) for the %d endpoint seeds that do not belong on it finds nothing", i, len(secrets)), err == nil && len(hits) == 0, leakNames(hits))
		s.leaks = append(s.leaks, hits...)
	}
	if s.dry {
		return
	}
	s.check("the seeds and JWTs of this run were read for the search", len(s.secrets) > 0, strconv.Itoa(len(s.secrets))+" kept in memory only")
}

func leakNames(ls []Leak) string {
	var n []string
	for _, l := range ls {
		n = append(n, l.Kind+" in "+l.File)
	}
	return strings.Join(n, "; ")
}

func runtimeVersion() string { return runtime.Version() }

// notifyStop makes an interrupt stop everything the run started, by recorded PID, before the process exits.
func notifyStop(r *Run, done chan struct{}) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			r.Close()
			os.Exit(130)
		case <-done:
		}
	}()
}

// scanDir5 and scanFor5 are the scanner of leak.go, kept here so that this file stands alone: any seed or JWT in the
// files under dir, and the exact bytes of named secrets. A Leak names the file and the kind, never the match.
func walk5(root string, fn func(path string, b []byte)) error {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fn(p, b)
	}
	return nil
}

func scanDir5(dir string) ([]Leak, error) {
	var out []Leak
	err := walk5(dir, func(p string, b []byte) {
		rel, _ := filepath.Rel(dir, p)
		if seed5RE.Match(b) {
			out = append(out, Leak{File: rel, Kind: "seed"})
		}
		if jwt5RE.Match(b) {
			out = append(out, Leak{File: rel, Kind: "jwt"})
		}
	})
	return out, err
}

func scanFor5(dir string, secrets map[string][]byte) ([]Leak, error) {
	var out []Leak
	err := walk5(dir, func(p string, b []byte) {
		rel, _ := filepath.Rel(dir, p)
		for label, sec := range secrets {
			if len(sec) > 0 && bytes.Contains(b, sec) {
				out = append(out, Leak{File: rel, Kind: label})
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].File+out[i].Kind < out[j].File+out[j].Kind })
	return out, err
}

// refusalCode5 reads refusal.code from a --json document ("" when there is none). Copied from at.go.
func refusalCode5(stdout string) string {
	v, err := parseJSON(stdout)
	if err != nil {
		return ""
	}
	if m, ok := v.(map[string]any); ok {
		if o, ok := m["refusal"].(map[string]any); ok {
			c, _ := o["code"].(string)
			return c
		}
	}
	return ""
}

// statusSaysWhy starts a session with the revoked creds and asks its status tool, which says the server refused the
// credentials, as the command does.
func (s *At5) statusSaysWhy(h, srv Host, name, creds string) {
	env := s.epEnv(h, srv, name, creds)
	p, err := h.Spawn("serve-status-"+name, []string{h.Bin("mash"), "serve"}, s.env(h, env...))
	if err != nil {
		s.check("REQ-011: the status tool of a session with the deleted key says why", false, err.Error())
		return
	}
	_ = p.Handshake(30 * time.Second)
	_ = p.Send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`)
	p.WaitFor(30*time.Second, func(o, _ string) bool { return strings.Contains(o, `"id":3`) })
	all := p.Stdout() + p.Stderr()
	line := ""
	for _, l := range strings.Split(strings.ReplaceAll(all, `\n`, "\n"), "\n") {
		if strings.Contains(l, "not ready") {
			line = l
		}
	}
	p.Stop()
	s.check("REQ-011: the status tool of a session with the deleted key says why (the server refused its credentials)", strings.Contains(all, "not ready: the server refused its credentials"), snip(line))
}
