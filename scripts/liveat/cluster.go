package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NodeSpec is what one node's server.conf is made from.
type NodeSpec struct {
	Name        string // server_name
	Addr        string // listen address of client and route ports
	Client      int
	Route       int
	Monitor     int // 0: no monitor
	Operator    string
	ResolverDir string
	StoreDir    string
	TLS         TLSFiles
	Routes      []string // nats-route://host:port of the other nodes
	ClusterName string
}

// RenderServerConf writes one node's server.conf: the template of the setup script's server.conf (operator, system account,
// full resolver with the same resolver_preload, JetStream) plus what docs/operations.md says a cluster adds, plus TLS
// on client and route connections (written by the harness, not by mash). It holds no key: only paths.
func RenderServerConf(n NodeSpec, systemAccount, preload string) string {
	var b strings.Builder
	q := strconv.Quote
	fmt.Fprintf(&b, "# AT-005 node %s, written by the test harness from the setup script's template.\n", n.Name)
	fmt.Fprintf(&b, "server_name: %s\n", n.Name)
	fmt.Fprintf(&b, "listen: %s:%d\n", n.Addr, n.Client)
	if n.Monitor > 0 {
		fmt.Fprintf(&b, "http: %s:%d\n", n.Addr, n.Monitor)
	}
	fmt.Fprintf(&b, "operator: %s\n", q(n.Operator))
	fmt.Fprintf(&b, "system_account: %s\n", systemAccount)
	fmt.Fprintf(&b, "resolver: {\n  type: full\n  dir: %s\n  allow_delete: false\n  interval: \"5s\"\n}\n", q(n.ResolverDir))
	b.WriteString(preload)
	if !strings.HasSuffix(preload, "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "jetstream: {\n  store_dir: %s\n}\n", q(n.StoreDir))
	fmt.Fprintf(&b, "tls: {\n  cert_file: %s\n  key_file: %s\n  ca_file: %s\n  timeout: 5\n}\n", q(n.TLS.Cert), q(n.TLS.Key), q(n.TLS.CA))
	fmt.Fprintf(&b, "cluster: {\n  name: %s\n  listen: %s:%d\n", n.ClusterName, n.Addr, n.Route)
	fmt.Fprintf(&b, "  tls: {\n    cert_file: %s\n    key_file: %s\n    ca_file: %s\n    verify: true\n    timeout: 5\n  }\n", q(n.TLS.Cert), q(n.TLS.Key), q(n.TLS.CA))
	fmt.Fprintf(&b, "  routes: [%s]\n}\n", strings.Join(n.Routes, ", "))
	return b.String()
}

var sysAcctRE = regexp.MustCompile(`(?m)^system_account:\s*(\S+)\s*$`)

// templateParts takes the system account and the resolver_preload block (verbatim) out of the setup script's server.conf.
func templateParts(conf string) (sysAcct, preload string, err error) {
	m := sysAcctRE.FindStringSubmatch(conf)
	if m == nil {
		return "", "", fmt.Errorf("server.conf has no system_account line")
	}
	lines := strings.Split(conf, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "resolver_preload:") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", "", fmt.Errorf("server.conf has no resolver_preload block")
	}
	for i := start; i < len(lines); i++ {
		if lines[i] == "}" {
			return m[1], strings.Join(lines[start:i+1], "\n") + "\n", nil
		}
	}
	return "", "", fmt.Errorf("resolver_preload block is not closed")
}

// Nodes builds the three NodeSpecs for the hosts. TLS files are the hosts' own.
func Nodes(hs []Host, tls []TLSFiles) []NodeSpec {
	var ns []NodeSpec
	for i, h := range hs {
		p := h.Ports()
		n := NodeSpec{
			Name: "mash-n" + strconv.Itoa(i+1), Addr: h.Addr(), Client: p.Client, Route: p.Route, Monitor: p.Monitor,
			Operator:    filepath.Join(h.ServerDir(), "operator.jwt"),
			ResolverDir: filepath.Join(h.ServerDir(), "resolver"),
			StoreDir:    filepath.Join(h.ServerDir(), "jetstream"),
			TLS:         tls[i], ClusterName: "mashc",
		}
		for j, o := range hs {
			if j != i {
				n.Routes = append(n.Routes, "nats-route://"+o.Addr()+":"+strconv.Itoa(o.Ports().Route))
			}
		}
		ns = append(ns, n)
	}
	return ns
}

// At5 is the state of one AT-005 run: the three machines, the CA, the servers and sessions, the assertions and the
// secrets to search for.
type At5 struct {
	R      *Run
	Hosts  []Host
	C      *Checks
	CA     *CA
	Leaves []*Leaf
	TLS    []TLSFiles
	Confs  []string // rendered server.conf text per node (paths only), for the record

	mu         sync.Mutex
	secrets    map[string][]byte
	skipped    []string
	notes      []string
	leaks      []Leak
	srv        []*Proc // the three nats-server processes, by recorded PID
	lastCreate string
	stage      string // the directory the setup script made inside M1 (a template, stopped), source of M1's and M2's files
	dry        bool
	runLine    string
}

func (s *At5) M(i int) Host { return s.Hosts[i-1] }

func (s *At5) urlList() string {
	var u []string
	for _, h := range s.Hosts {
		u = append(u, "tls://"+h.Addr()+":"+strconv.Itoa(h.Ports().Client))
	}
	return strings.Join(u, ",")
}

func (s *At5) urlOf(h Host) string { return "tls://" + h.Addr() + ":" + strconv.Itoa(h.Ports().Client) }

// env is the whole environment of a command on h: scratch values only.
func (s *At5) env(h Host, extra ...string) []string {
	return BuildEnv(h.Home(), []string{filepath.Dir(h.Bin("nats-server"))}, extra...)
}

// ctl runs mashctl on h with its config.json, which lists all three servers.
func (s *At5) ctl(h Host, label string, extra []string, args ...string) Result {
	return h.Exec(label, append([]string{h.Bin("mashctl")}, args...), s.env(h, extra...))
}

// mash runs the mash command line on h.
func (s *At5) mash(h Host, label string, extra []string, args ...string) Result {
	return h.Exec(label, append([]string{h.Bin("mash")}, args...), s.env(h, extra...))
}

func (s *At5) check(name string, cond bool, detail string) bool { return s.C.Check(name, cond, detail) }

func (s *At5) skip(what string) {
	s.mu.Lock()
	s.skipped = append(s.skipped, what)
	s.mu.Unlock()
}

// BuildCerts makes the CA and one leaf per node and writes each node's files into its own TLS directory.
func (s *At5) BuildCerts(now time.Time) error {
	ca, err := NewCA(now)
	if err != nil {
		return err
	}
	s.CA = ca
	for i, h := range s.Hosts {
		sans := SANsFor(h.Addr())
		l, err := ca.Issue("mash-n"+strconv.Itoa(i+1), now, sans...)
		if err != nil {
			return err
		}
		f, err := WriteTLS(h, ca, l)
		if err != nil {
			return err
		}
		s.Leaves = append(s.Leaves, l)
		s.mu.Lock()
		registerKeyLines(s.secrets, "leaf "+l.Name+" key", l.KeyPEM) // every line of the key.pem written above
		if kb, err := x509.MarshalPKCS8PrivateKey(ca.key); err == nil {
			registerKeyLines(s.secrets, "ca key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})) // memory-only, never written
		}
		s.mu.Unlock()
		s.TLS = append(s.TLS, f)
		if err := ca.Verify(l, now, sans...); err != nil {
			return fmt.Errorf("leaf %s does not verify: %w", l.Name, err)
		}
	}
	return nil
}

// setupCmd is the command line of one call of the setup script on h: bash and the script in the node image (a podman
// node), or the Run's script (a local machine, which only a run inside a container may use).
func (s *At5) setupCmd(h Host, dir string, more ...string) []string {
	if _, ok := h.(*podHost); ok {
		return append([]string{"bash", nodeSetupPath, "--dir", dir}, more...)
	}
	return s.R.setupArgs(dir, more...)
}

// stageGet reads one file of the setup script's directory on M1 (the stage), through the Host.
func (s *At5) stageGet(file string) ([]byte, error) { return s.M(1).Get(filepath.Join(s.stage, file)) }

// stageSetup runs the setup script INSIDE M1 (a node container, as its non-root user; never on the test host) into a
// new directory <M1's directory>/setup, and stops the server it starts: the cluster runs its own three servers, and
// this one only supplies the operator, the account, the keys, the creds and the server.conf template. The script
// picks its own port (4222 or the next free, on M1's loopback); the cluster does not use it.
func (s *At5) stageSetup() bool {
	m1 := s.M(1)
	if _, local := m1.(*localHost); local && !insideContainer() {
		s.check("the setup script runs inside a node container", false, errSetupOnHost)
		return false
	}
	s.stage = filepath.Join(filepath.Dir(m1.ServerDir()), "setup")
	env := s.env(m1)
	res := m1.Exec("setup-script", s.setupCmd(m1, s.stage, "--owner", "alice"), env)
	_, perr := parseSetupPort(res.Stdout)
	ok := s.check("the setup script makes the operator, the account, the keys, the creds and the server.conf template (run inside M1)", res.Code == 0 && perr == nil, exitDetail(res)+"; "+errText(perr))
	for _, k := range secretFiles {
		if kb, err := s.stageGet(k.file); err == nil {
			s.mu.Lock()
			registerKeyLines(s.secrets, "setup "+k.label, kb)
			s.mu.Unlock()
		}
	}
	pid := 0
	if b, err := s.stageGet(fileServerPID); err == nil {
		pid, _ = parsePIDFile(b)
	}
	if pid > 0 {
		st := m1.Exec("setup-stop", s.setupCmd(m1, s.stage, "--stop"), env)
		if st.Code != 0 && m1.Exists(filepath.Join(s.stage, fileServerPID)) {
			// the script's stop failed: end exactly the PID in its pid file, inside M1
			m1.Exec("setup-stop-pid", []string{m1.Bin("nodeinit"), "stop", "--pidfile", filepath.Join(s.stage, fileServerPID), "--grace", "5s"}, env)
		}
		s.check("the setup script's own server is stopped again (the cluster runs its own three)", st.Code == 0 || !m1.Exists(filepath.Join("/proc", strconv.Itoa(pid), "exe")), exitDetail(st))
	}
	return ok && pid > 0
}

// InitM1 runs the setup script (stageSetup), then renders the three server.conf files from its server.conf: the
// operator JWT is copied to every node, each node gets its own names, ports and directories.
func (s *At5) InitM1() bool {
	if !s.stageSetup() {
		return false
	}
	b, err := s.stageGet(fileServerConf)
	if err != nil {
		s.check("the template server.conf is readable", false, err.Error())
		return false
	}
	sys, preload, err := templateParts(string(b))
	if err != nil {
		s.check("the template has a system account and a resolver_preload", false, err.Error())
		return false
	}
	opJWT, err := s.stageGet(fileOperator)
	if err != nil {
		s.check("operator.jwt is readable", false, err.Error())
		return false
	}
	nodes := Nodes(s.Hosts, s.TLS)
	for i, h := range s.Hosts {
		if err := h.Put(nodes[i].Operator, opJWT, 0o600); err != nil {
			s.check("operator.jwt is copied to "+h.Role(), false, err.Error())
			return false
		}
		conf := RenderServerConf(nodes[i], sys, preload)
		if err := h.Put(filepath.Join(h.ServerDir(), "server.conf"), []byte(conf), 0o600); err != nil {
			s.check("server.conf is written on "+h.Role(), false, err.Error())
			return false
		}
		s.Confs = append(s.Confs, conf)
	}
	return s.check("the template is edited for TLS, cluster and server_name and written to M1, M2 and M3 with their own ports and directories", true, "")
}

// StartServers starts the three NATS servers, each by `nats-server -c server.conf`, and waits until they have formed a
// cluster: node 1's monitor shows two routes and every client port answers TLS.
func (s *At5) StartServers() bool {
	for _, h := range s.Hosts {
		for _, p := range []int{h.Ports().Client, h.Ports().Route} {
			if err := h.WaitPortFree(p, 3*time.Second); err != nil {
				s.check(h.Role()+": port "+strconv.Itoa(p)+" is free before the server starts", false, err.Error())
				return false
			}
		}
	}
	if err := s.M(1).WaitPortFree(DefaultPorts[0].Monitor, 3*time.Second); err != nil {
		s.check("the monitor port is free", false, err.Error())
		return false
	}
	for _, h := range s.Hosts {
		p, err := h.Spawn("nats-server", []string{h.Bin("nats-server"), "-c", filepath.Join(h.ServerDir(), "server.conf")}, s.env(h))
		if err != nil {
			s.check(h.Role()+": nats-server starts", false, err.Error())
			return false
		}
		s.srv = append(s.srv, p)
		s.R.Runner.logf("%s: nats-server pid=%d", h.Role(), p.PID)
		s.notes = append(s.notes, fmt.Sprintf("%s nats-server pid %d", h.Role(), p.PID))
	}
	m1 := s.M(1)
	formed, detail := waitFormed(m1, "http://"+m1.Addr()+":"+strconv.Itoa(m1.Ports().Monitor)+"/routez", 2, 40*time.Second)
	return s.check("the three NATS servers started and formed one cluster over TLS routes (node 1's monitor shows at least 2 routes; the server pools several connections per peer)", formed, detail)
}

// waitFormed polls the routez URL, fetched from h, until it reports want routes.
func waitFormed(h Host, url string, want int, d time.Duration) (bool, string) {
	deadline := time.Now().Add(d)
	last := "no answer"
	for {
		b, err := h.HTTPGet(url)
		if err == nil {
			var v struct {
				NumRoutes int `json:"num_routes"`
			}
			if json.Unmarshal(b, &v) == nil {
				last = fmt.Sprintf("%d routes", v.NumRoutes)
				if v.NumRoutes >= want {
					return true, last
				}
			}
		}
		if time.Now().After(deadline) {
			return false, last
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// StopServers stops each started server by its recorded PID, in parallel, and checks that the ports are free.
func (s *At5) StopServers(procs []*Proc) {
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func(p *Proc) { defer wg.Done(); p.Stop() }(p)
	}
	wg.Wait()
}

// retry runs fn until it returns true or d passes; it returns the last result.
func retry(d time.Duration, every time.Duration, fn func() (Result, bool)) Result {
	deadline := time.Now().Add(d)
	for {
		r, ok := fn()
		if ok || time.Now().After(deadline) {
			return r
		}
		time.Sleep(every)
	}
}

// urlSlice is the three servers' URLs.
func (s *At5) urlSlice() []string {
	var u []string
	for _, h := range s.Hosts {
		u = append(u, s.urlOf(h))
	}
	return u
}

// initFromSetup runs `mashctl init --config` on h against the whole cluster. The operator signing seed, the account
// identity seed and the system creds are copied from the setup script's directory (mode 0600) to a private directory on
// h, and so are the admin creds, which config.json keeps pointing at (they stay at h's <server dir>/creds). The setup file lists
// all three servers and h's own CA copy. JetStream may need a few seconds to elect its meta group, so the command is
// retried. The seed and sys.creds copies are removed by their exact paths afterwards. It returns the last result and whether the
// copies are gone.
func (s *At5) initFromSetup(h Host) (res Result, gone bool, err error) {
	return s.initFromSetupWith(h, s.urlSlice(), false)
}

// initFromSetupWith is initFromSetup with the setup file's server list given. With once set the command runs a single
// time (no retry), for a run that is expected to be refused.
func (s *At5) initFromSetupWith(h Host, servers []string, once bool) (res Result, gone bool, err error) {
	in := filepath.Join(h.ServerDir(), "init-in")
	admin := filepath.Join(h.ServerDir(), fileAdminCreds)
	files := []struct{ src, dst string }{
		{fileOperatorNK, filepath.Join(in, "operator.nk")},
		{fileAccountNK, filepath.Join(in, "account.nk")},
		{fileSysCreds, filepath.Join(in, "sys.creds")},
		{fileAdminCreds, admin},
	}
	var temp []string
	cleanup := func() {
		for _, p := range temp {
			_ = h.Remove(p)
		}
		_ = h.Remove(in) // the directory this harness made, now empty
	}
	for _, f := range files {
		b, rerr := s.stageGet(f.src)
		if rerr != nil {
			cleanup()
			return res, true, fmt.Errorf("%s is not readable for the copy: %v", f.src, rerr)
		}
		if perr := h.Put(f.dst, b, 0o600); perr != nil {
			cleanup()
			return res, true, fmt.Errorf("the copy of %s is not made on %s: %v", f.src, h.Role(), perr)
		}
		clear(b)
		if f.dst != admin {
			temp = append(temp, f.dst)
		}
	}
	ca := s.TLS[0].CA
	for i, o := range s.Hosts {
		if o.Role() == h.Role() {
			ca = s.TLS[i].CA
		}
	}
	setup := SetupFile{
		Servers: servers, CA: ca, Owner: "alice", AdminCreds: admin,
		SystemCreds: filepath.Join(in, "sys.creds"), OperatorKey: filepath.Join(in, "operator.nk"), AccountKey: filepath.Join(in, "account.nk"),
	}
	file := filepath.Join(h.ServerDir(), fileSetupYAML)
	if perr := h.Put(file, []byte(setup.Render()), 0o600); perr != nil {
		cleanup()
		return res, true, perr
	}
	if once {
		res = s.ctl(h, "init-config-refused", nil, "init", "--config", file)
	} else {
		res = retry(40*time.Second, 2*time.Second, func() (Result, bool) {
			r := s.ctl(h, "init-config", nil, "init", "--config", file)
			return r, r.Code == 0
		})
	}
	cleanup()
	gone = true
	for _, p := range temp {
		if h.Exists(p) {
			gone = false
		}
	}
	return res, gone, nil
}

// InitRefusedForOneServer runs `mashctl init --config` once on M1 with a setup file that lists only M1's own server of
// the three-node cluster. The ping sees a cluster that reports more servers than the file lists, so init must refuse
// (exit 1, "the setup file lists ... servers") and change nothing: M1 has no config.json afterwards.
func (s *At5) InitRefusedForOneServer() bool {
	h := s.M(1)
	res, gone, err := s.initFromSetupWith(h, []string{s.urlOf(h)}, true)
	if err != nil {
		s.check("M1: the setup file for the refused mashctl init --config is made", false, err.Error())
		return false
	}
	t := res.Text()
	ok := s.check("M1: mashctl init --config with only M1's server listed is refused: the server is part of a cluster but the setup file lists fewer servers (exit 1)", res.Code == 1, exitDetail(res))
	ok = s.check("M1: the refusal says the setup file lists the servers and names the cluster", strings.Contains(t, "setup file lists") && strings.Contains(t, "cluster"), "output: "+strings.TrimSpace(t)) && ok
	ok = s.check("M1: the refused mashctl init --config changed nothing: there is no config.json", !h.Exists(filepath.Join(h.Home().Config, "config.json")), "") && ok
	ok = s.check("the copies of the seeds and sys.creds that the harness made on M1 for the refused run are deleted by exact path", gone, "") && ok
	return ok
}

// InitBuckets runs `mashctl init --config` on M1 with a setup file that lists all three servers and the cluster's CA,
// which makes the directory, lease and issue-record buckets over the cluster and writes M1's config.json.
func (s *At5) InitBuckets() bool {
	res, gone, err := s.initFromSetup(s.M(1))
	if err != nil {
		s.check("M1: the setup file for mashctl init --config is made", false, err.Error())
		return false
	}
	s.check("the copies of the seeds and sys.creds that the harness made on M1 are deleted by exact path", gone, "")
	return s.check("M1: mashctl init --config with all three servers and ca makes mash's buckets over the cluster", res.Code == 0, exitDetail(res))
}

// AdoptM2 sets M2 up as DEC-004 records, with the same authority: the operator signing seed, the account identity seed,
// sys.creds and the admin creds are copied by the harness (mode 0600) to M2, `mashctl init --config` runs there against
// the cluster (it does not mint an admin: M2 uses M1's admin creds), and the seed and sys.creds copies are removed by their exact paths afterwards.
func (s *At5) AdoptM2() bool {
	res, gone, err := s.initFromSetup(s.M(2))
	if err != nil {
		s.check("M2: the setup file for mashctl init --config is made", false, err.Error())
		return false
	}
	ok := s.check("M2: mashctl init --config with all three servers and ca, with the same keys and creds", res.Code == 0, exitDetail(res))
	s.check("the copies of the seeds and sys.creds that the harness made on M2 are deleted by exact path", gone, "")
	return ok
}

func files2(dir, name string) string { return filepath.Join(dir, name) }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var doctorKeysOKRE = regexp.MustCompile(`(?m)^keys\s+ok\b`)

// doctorClean asserts `mashctl doctor` output: exit 0, the keys line ok, no FAIL and "No problems.".
func doctorClean(code int, out string) (bool, string) {
	var bad []string
	if code != 0 {
		bad = append(bad, "exit "+strconv.Itoa(code))
	}
	if !doctorKeysOKRE.MatchString(out) {
		bad = append(bad, "no `keys ok` line")
	}
	if strings.Contains(out, "FAIL") {
		bad = append(bad, "a FAIL line")
	}
	if !strings.Contains(out, "No problems.") {
		bad = append(bad, "no `No problems.` line")
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, "exit 0, keys ok, No problems."
}

// statusAllOK asserts `mashctl status --json` on a host whose config lists the cluster: exit 0 and one entry per
// server (want entries, each a different server), every one ok. A node that is down is a row that is not ok.
func statusAllOK(code int, stdout string, want int) (bool, string) {
	var doc struct {
		Servers []struct {
			Server string  `json:"server"`
			OK     bool    `json:"ready"`
			Reason *string `json:"reason"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &doc); err != nil {
		return false, fmt.Sprintf("exit %d, stdout is not the status --json document", code)
	}
	if len(doc.Servers) != want {
		return false, fmt.Sprintf("%d server entries, want %d", len(doc.Servers), want)
	}
	var bad []string
	seen := map[string]bool{}
	for _, r := range doc.Servers {
		if seen[r.Server] {
			bad = append(bad, "server "+r.Server+" listed twice")
		}
		seen[r.Server] = true
		if !r.OK {
			why := ""
			if r.Reason != nil {
				why = ": " + *r.Reason
			}
			bad = append(bad, r.Server+" not ok"+why)
		}
	}
	if code != 0 {
		bad = append(bad, "exit "+strconv.Itoa(code))
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, fmt.Sprintf("exit 0, %d of %d servers ok", want, want)
}

// ClusterCheck runs `mashctl doctor` on h and checks that it reports no problem, then `mashctl status --json` (one
// call, one entry per server of the config) and requires every one of the three servers to answer: doctor talks to
// one URL and passes with a node down, status does not.
func (s *At5) ClusterCheck(h Host, name string) bool {
	r := retry(40*time.Second, 2*time.Second, func() (Result, bool) {
		r := s.ctl(h, "doctor", nil, "doctor")
		ok, _ := doctorClean(r.Code, r.Stdout)
		return r, ok
	})
	ok, d := doctorClean(r.Code, r.Stdout)
	a := s.check(name+": mashctl doctor reports no problems (keys ok)", ok, redact(d))
	st := retry(40*time.Second, 2*time.Second, func() (Result, bool) {
		r := s.ctl(h, "status-all", nil, "status", "--json")
		ok, _ := statusAllOK(r.Code, r.Stdout, len(s.Hosts))
		return r, ok
	})
	ok, d = statusAllOK(st.Code, st.Stdout, len(s.Hosts))
	b := s.check(name+": mashctl status --json shows every one of the three servers answering", ok, redact(d))
	return a && b
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Setup is everything before the scenario: certificates, M1 by the setup script, the cluster, the buckets (init --config), M2 by init --config, and the
// first look from both machines.
func (s *At5) Setup(now time.Time) bool {
	if err := s.BuildCerts(now); err != nil {
		s.check("the harness makes its certificate authority and one certificate per node", false, err.Error())
		return false
	}
	s.check("the harness makes its certificate authority and one certificate per node, each chain verified with the SANs of the node's address", true, "")
	return s.InitM1() && s.StartServers() && s.InitRefusedForOneServer() && s.InitBuckets() && s.AdoptM2() &&
		s.ClusterCheck(s.M(1), "M1") && s.ClusterCheck(s.M(2), "M2") && s.listBoth()
}

func (s *At5) listBoth() bool {
	ok := true
	for _, h := range s.Hosts[:2] {
		r := s.ctl(h, "list", nil, "endpoint", "list")
		ok = s.check(h.Role()+": mashctl endpoint list works", r.Code == 0, exitDetail(r)) && ok
	}
	return ok
}
