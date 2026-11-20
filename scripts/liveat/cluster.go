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

// RenderServerConf writes one node's server.conf: the template of `mashctl init --local` (operator, system account,
// full resolver with the same resolver_preload, JetStream) plus what docs/operations.md says a cluster adds, plus TLS
// on client and route connections (written by the harness, not by mash). It holds no key: only paths.
func RenderServerConf(n NodeSpec, systemAccount, preload string) string {
	var b strings.Builder
	q := strconv.Quote
	fmt.Fprintf(&b, "# AT-005 node %s, written by the test harness from the `mashctl init --local` template.\n", n.Name)
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

// templateParts takes the system account and the resolver_preload block (verbatim) out of an init --local server.conf.
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

// patchConfig sets keys in h's config.json (mode 0600) and removes others.
func (s *At5) patchConfig(h Host, set map[string]any, drop ...string) error {
	p := filepath.Join(h.Home().Config, "config.json")
	b, err := h.Get(p)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for k, v := range set {
		m[k] = v
	}
	for _, k := range drop {
		delete(m, k)
	}
	out, _ := json.MarshalIndent(m, "", "  ")
	return h.Put(p, append(out, '\n'), 0o600)
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

// InitM1 runs `mashctl init --local` on M1 without --start, then renders the three server.conf files from its output:
// the operator JWT is copied to M2 and M3, each node gets its own names, ports and directories.
func (s *At5) InitM1() bool {
	m1 := s.M(1)
	// T1b: `mashctl init --local` is removed. The live setup is rewritten later; until then the setup is a failed
	// check and nothing is run. (The code after this check is the part the rewrite keeps.)
	if !s.check("setup rewrite pending", false, "") {
		return false
	}
	b, err := m1.Get(filepath.Join(m1.ServerDir(), "server.conf"))
	if err != nil {
		s.check("M1: the template server.conf is readable", false, err.Error())
		return false
	}
	sys, preload, err := templateParts(string(b))
	if err != nil {
		s.check("M1: the template has a system account and a resolver_preload", false, err.Error())
		return false
	}
	opJWT, err := m1.Get(filepath.Join(m1.ServerDir(), "operator.jwt"))
	if err != nil {
		s.check("M1: operator.jwt is readable", false, err.Error())
		return false
	}
	nodes := Nodes(s.Hosts, s.TLS)
	for i, h := range s.Hosts {
		if i > 0 {
			if err := h.Put(nodes[i].Operator, opJWT, 0o600); err != nil {
				s.check("operator.jwt is copied to "+h.Role(), false, err.Error())
				return false
			}
		}
		conf := RenderServerConf(nodes[i], sys, preload)
		if err := h.Put(filepath.Join(h.ServerDir(), "server.conf"), []byte(conf), 0o600); err != nil {
			s.check("server.conf is written on "+h.Role(), false, err.Error())
			return false
		}
		s.Confs = append(s.Confs, conf)
	}
	return s.check("the template is edited for TLS, cluster and server_name and copied to M2 and M3 with their own ports and directories", true, "")
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

// InitBuckets points M1 at the cluster (config.json: every server's URL and ca) and runs
// `mashctl init` there, which creates the directory and lease buckets over the cluster. JetStream may need a few
// seconds to elect its meta group, so the command is retried.
func (s *At5) InitBuckets() bool {
	m1 := s.M(1)
	err := s.patchConfig(m1, map[string]any{"servers": s.urlList(), "ca": s.TLS[0].CA})
	if !s.check("M1: config.json names all three servers and ca", err == nil, errText(err)) {
		return false
	}
	// T1b: `mashctl init` with no setup file is removed (`mashctl init --config FILE` replaces it). The live setup is
	// rewritten later; until then the setup is a failed check and nothing is run.
	return s.check("setup rewrite pending", false, "")
}

// AdoptM2 sets M2 up as DEC-004 records: the operator signing seed, the account identity seed and sys.creds are copied
// from M1 by the harness (mode 0600) to a private directory on M2, `mashctl adopt ... --mint-admin` runs there against
// the server of M2, and the copies are removed by their exact paths afterwards.
func (s *At5) AdoptM2() bool {
	m1, m2 := s.M(1), s.M(2)
	in := filepath.Join(m2.Home().Dir, "adopt-in")
	files := map[string]string{
		"operator.nk": filepath.Join(m1.Home().Keys, "operator.nk"),
		"account.nk":  filepath.Join(m1.Home().Keys, "account.nk"),
		"sys.creds":   filepath.Join(m1.ServerDir(), "sys.creds"),
	}
	var copies []string
	cleanup := func() {
		for _, p := range copies {
			_ = m2.Remove(p)
		}
		_ = m2.Remove(in) // the directory this harness made, now empty
	}
	for name, src := range files {
		b, err := m1.Get(src)
		if err != nil {
			s.check("M1's "+name+" is readable for the copy", false, err.Error())
			cleanup()
			return false
		}
		dst := filepath.Join(in, name)
		if err := m2.Put(dst, b, 0o600); err != nil {
			s.check("the copy of "+name+" is made on M2", false, err.Error())
			cleanup()
			return false
		}
		clear(b)
		copies = append(copies, dst)
	}
	// T1b: `mashctl adopt` is removed (`mashctl init --config FILE` replaces it). The live setup is rewritten later;
	// until then the setup is a failed check and nothing is run.
	res := Result{Code: 1}
	cleanup()
	gone := true
	for _, p := range copies {
		if m2.Exists(p) {
			gone = false
		}
	}
	ok := s.check("setup rewrite pending", res.Code == 0, "")
	s.check("the copies of the seeds and sys.creds that the harness made on M2 are deleted by exact path", gone, "")
	if !ok {
		return false
	}
	m2cfg := s.patchConfig(m2, map[string]any{"servers": s.urlList(), "ca": s.TLS[1].CA})
	return s.check("M2: config.json names all three servers and ca", m2cfg == nil, errText(m2cfg))
}

func files2(dir, name string) string { return filepath.Join(dir, name) }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ClusterCheck runs `mashctl issued check` on h and checks the summary line.
func (s *At5) ClusterCheck(h Host, name string) bool {
	r := retry(40*time.Second, 2*time.Second, func() (Result, bool) {
		r := s.ctl(h, "issued-check", nil, "issued", "check")
		return r, r.Code == 0
	})
	sum := grepLine(r.Stdout, "servers answered")
	return s.check(name+": mashctl issued check reports all three servers answered", r.Code == 0 && strings.Contains(r.Stdout, "3 of 3 servers answered"), "exit "+strconv.Itoa(r.Code)+"; "+redact(sum))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Setup is everything before the scenario: certificates, M1 by init, the cluster, the buckets, M2 by adopt, and the
// first look from both machines.
func (s *At5) Setup(now time.Time) bool {
	if err := s.BuildCerts(now); err != nil {
		s.check("the harness makes its certificate authority and one certificate per node", false, err.Error())
		return false
	}
	s.check("the harness makes its certificate authority and one certificate per node, each chain verified with the SANs of the node's address", true, "")
	return s.InitM1() && s.StartServers() && s.InitBuckets() && s.AdoptM2() &&
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
