// Command at022 is the owner-boundary and forgery proof of AT-022 (REQ-021), run on the built `mash` binary of the
// candidate. Run it by scripts/ownerproof/at022.sh, inside a throwaway container, or directly in a throwaway VM guest
// (it starts a NATS server and mash processes, so never on a working host).
//
// Each item gets its own fresh server (operator mode, generated keys) with owner acme (endpoints alice, bob and
// carol) and owner zeta (endpoint mallory). Every endpoint's user JWT is checked to carry exactly the output of
// admin.Permissions. The sessions are the real binaries: `mash serve` (driven over MCP) and the mash commands prompt,
// who and send. The adversary's actions (raw NATS subscribe and publish by carol and mallory, forged registrations)
// are done by this runner with those users' own credentials. The tap that captures subjects is an observer identity
// of the test account and is not one of the five endpoints.
//
// Items 1, 2, 3 and 5 run on the candidate. Item 4 (the same-owner reply race) is recorded and never asserted closed;
// it runs on the candidate and on the last 0.5.x release (a published binary checked against that release's
// SHA256SUMS), and the two results must be equal.
//
//	at022 -src <clean clone at the commit> -commit <40 hex> -version 0.6.0 -oracle-version 0.5.10 -out <dir>
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/testenv"
)

const (
	ownerA = "acme"
	ownerB = "zeta"
	group  = "ops"
	// evil is the reply chunk an attacker publishes: a response chunk that, accepted, would show up in the output.
	evil = `{"type":"response","data":"EVIL"}`
	// downloadTimeout bounds one release download.
	downloadTimeout = 180 * time.Second
)

// releaseBase is the download URL prefix of the releases of repo (OWNER/REPO on GitHub).
func releaseBase(repo string) string { return "https://github.com/" + repo + "/releases/download/" }

var (
	repoRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// logger writes the human log to stderr and keeps it for run.log.
type logger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logger) f(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := time.Now().UTC().Format("15:04:05.000") + " " + fmt.Sprintf(format, a...) + "\n"
	l.buf.WriteString(line)
	fmt.Fprint(os.Stderr, line)
}

var lg = &logger{}

// Sub is one check inside an item.
type Sub struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Item is one observation of AT-022, reported on its own.
type Item struct {
	N     int    `json:"item"`
	Name  string `json:"name"`
	Build string `json:"build"`
	Pass  bool   `json:"pass"`
	// Error is set when the runner could not carry the item out (the item is then neither passed nor failed on its
	// merits, and the run fails).
	Error         string   `json:"runner_error,omitempty"`
	Checks        []Sub    `json:"checks"`
	Observations  []string `json:"observations,omitempty"`
	NotObservable []string `json:"not_observable_through_the_binary,omitempty"`
}

func (it *Item) check(name string, pass bool, detail string) bool {
	it.Checks = append(it.Checks, Sub{name, pass, detail})
	lg.f("  item %d %s: %s %s", it.N, map[bool]string{true: "ok  ", false: "FAIL"}[pass], name, detail)
	return pass
}

func (it *Item) note(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	it.Observations = append(it.Observations, s)
	lg.f("  item %d note: %s", it.N, s)
}

// finish sets Pass from the checks.
func (it *Item) finish() {
	it.Pass = it.Error == "" && len(it.Checks) > 0
	for _, c := range it.Checks {
		it.Pass = it.Pass && c.Pass
	}
	lg.f("item %d (%s, %s): %s", it.N, it.Name, it.Build, map[bool]string{true: "PASS", false: "FAIL"}[it.Pass])
}

// BuildInfo describes one binary under test.
type BuildInfo struct {
	Role        string `json:"role"` // candidate or oracle
	Version     string `json:"version"`
	Source      string `json:"source"`
	SHA256      string `json:"binary_sha256"`
	VersionText string `json:"mash_version_output,omitempty"`
}

// Record is the evidence file.
type Record struct {
	AT          string      `json:"at"`
	Req         string      `json:"req"`
	Venue       string      `json:"venue"`
	StartedAt   time.Time   `json:"started_at"`
	FinishedAt  time.Time   `json:"finished_at"`
	Environment string      `json:"environment_lifecycle"`
	Commit      string      `json:"candidate_commit"`
	Arch        string      `json:"arch"`
	ReleaseRepo string      `json:"oracle_release_repo"`
	Builds      []BuildInfo `json:"builds"`
	Setup       []Sub       `json:"setup"`
	Items       []Item      `json:"items"`
	Item4Cand   Item4       `json:"item4_candidate"`
	Item4Oracle Item4       `json:"item4_oracle"`
	Item4Equal  bool        `json:"item4_equal"`
	Item4Diff   []string    `json:"item4_differences,omitempty"`
	Pass        bool        `json:"pass"`
	Note        string      `json:"note"`
}

func main() { os.Exit(run()) }

func run() int {
	src := flag.String("src", "", "a clean git clone checked out at -commit (the candidate source)")
	commit := flag.String("commit", "", "the candidate commit, 40 hex digits")
	version := flag.String("version", "0.6.0", "the candidate's version")
	oracleVer := flag.String("oracle-version", "0.5.10", "the oracle release, the last 0.5.x")
	outDir := flag.String("out", "", "directory for record.json and run.log")
	work := flag.String("work", "", "scratch directory for servers, state and binaries (default: a new temp directory)")
	repo := flag.String("release-repo", "bogworks/mash", "the GitHub repository OWNER/REPO whose releases hold the oracle")
	venue := flag.String("venue", "", "a line describing where this runs (container image, VM), for the record")
	flag.Parse()
	if *src == "" || *outDir == "" || !commitRE.MatchString(*commit) || !versionRE.MatchString(*version) || !versionRE.MatchString(*oracleVer) || !repoRE.MatchString(*repo) {
		fmt.Fprintln(os.Stderr, "usage: at022 -src <clone> -commit <40 hex> -version X.Y.Z -oracle-version X.Y.Z -out <dir> [-work dir] [-release-repo OWNER/REPO] [-venue text]")
		return 2
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	wd := *work
	if wd == "" {
		d, err := os.MkdirTemp("", "at022-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		wd = d
	} else if err := os.MkdirAll(wd, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	rec := &Record{AT: "AT-022", Req: "REQ-021", Venue: *venue, StartedAt: time.Now().UTC(), Commit: *commit, ReleaseRepo: *repo, Arch: runtime.GOARCH,
		Environment: "each item gets its own NATS server (operator mode, generated keys) and state directories under " + wd +
			", created at the start of the item and stopped at its end; the launcher creates and destroys the container",
		Note: "item 4 is recorded and compared, never asserted closed (REQ-021)"}
	lg.f("AT-022 owner-boundary proof: candidate %s version %s, oracle v%s, %s/%s", *commit, *version, *oracleVer, runtime.GOOS, runtime.GOARCH)
	lg.f("venue: %s", *venue)

	ok := execute(rec, *src, *commit, *version, *oracleVer, *repo, wd)
	rec.FinishedAt = time.Now().UTC()
	rec.Pass = ok && len(rec.Items) > 0
	lg.f("RESULT: %s", map[bool]string{true: "PASS", false: "FAIL"}[rec.Pass])
	b, _ := json.MarshalIndent(rec, "", "  ")
	werr := false
	for _, w := range []struct {
		name string
		data []byte
	}{{"record.json", append(b, '\n')}, {"run.log", lg.buf.Bytes()}} {
		if err := os.WriteFile(filepath.Join(*outDir, w.name), w.data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "at022: writing %s: %v\n", w.name, err)
			werr = true
		}
	}
	switch {
	case werr:
		return 3
	case rec.Pass:
		return 0
	}
	return 1
}

func (r *Record) setup(name string, pass bool, detail string) bool {
	r.Setup = append(r.Setup, Sub{name, pass, detail})
	lg.f("%s %s %s", map[bool]string{true: "ok  ", false: "FAIL"}[pass], name, detail)
	return pass
}

// execute fetches both binaries and runs the items. It returns whether every item held and item 4 is equal.
func execute(rec *Record, src, commit, version, oracleVer, repo, wd string) bool {
	arch := runtime.GOARCH
	oracleBin, oracleSum, err := fetchOracle(repo, oracleVer, arch, filepath.Join(wd, "oracle"))
	if !rec.setup("oracle v"+oracleVer+" binary downloaded and matches the release's SHA256SUMS", err == nil, errText(err)) {
		return false
	}
	lg.f("oracle mash-%s-%s sha256 %s (matches SHA256SUMS)", runtime.GOOS, arch, oracleSum)
	candBin := filepath.Join(wd, "candidate", "mash")
	candSum, err := buildCandidate(src, commit, version, arch, candBin)
	if !rec.setup("candidate "+version+" built by scripts/buildrelease at "+commit[:12], err == nil, errText(err)) {
		return false
	}
	lg.f("candidate mash sha256 %s", candSum)
	cand := BuildInfo{Role: "candidate", Version: version, Source: "scripts/buildrelease at " + commit, SHA256: candSum}
	orc := BuildInfo{Role: "oracle", Version: oracleVer, Source: releaseBase(repo) + "v" + oracleVer + "/mash-" + runtime.GOOS + "-" + arch, SHA256: oracleSum}
	cand.VersionText = versionText(candBin)
	orc.VersionText = versionText(oracleBin)
	rec.Builds = []BuildInfo{cand, orc}

	ok := true
	runItem := func(num int, name string, b BuildInfo, bin string, fn func(w *world, it *Item)) {
		it := Item{N: num, Name: name, Build: b.Role + " " + b.Version}
		lg.f("=== item %d: %s (%s) ===", num, name, it.Build)
		w, err := newWorld(filepath.Join(wd, fmt.Sprintf("%s-item%d", b.Role, num)), bin, b.Version, rec)
		if err != nil {
			it.Error = err.Error()
			lg.f("item %d stopped: %v", num, err)
		} else {
			func() {
				defer w.close()
				fn(w, &it)
			}()
		}
		it.finish()
		if !it.Pass {
			ok = false
		}
		rec.Items = append(rec.Items, it)
	}
	runItem(1, "the owner boundary, against captured subjects", cand, candBin, item1)
	runItem(2, "registration forgery", cand, candBin, item2)
	runItem(3, "guessed-subject forgery", cand, candBin, item3)
	// Item 4 on each build, in a fresh server of its own, recorded and compared.
	var r4 [2]Item4
	for i, b := range []struct {
		info BuildInfo
		bin  string
	}{{cand, candBin}, {orc, oracleBin}} {
		runItem(4, "same-owner reply race, recorded and not asserted closed", b.info, b.bin, func(w *world, it *Item) { r4[i] = item4(w, it, b.info.Role+" "+b.info.Version) })
	}
	rec.Item4Cand, rec.Item4Oracle = r4[0], r4[1]
	d := DiffItem4(r4[0], r4[1])
	rec.Item4Equal = r4[0].Ran && r4[1].Ran && len(d) == 0
	rec.Item4Diff = d
	if !rec.Item4Equal {
		ok = false
	}
	lg.f("item 4 on the candidate: exit %d, stdout %q, carol violations %d", r4[0].PromptExit, r4[0].PromptStdout, r4[0].CarolViolations)
	lg.f("item 4 on the oracle:    exit %d, stdout %q, carol violations %d", r4[1].PromptExit, r4[1].PromptStdout, r4[1].CarolViolations)
	lg.f("item 4 results equal: %v %s", rec.Item4Equal, strings.Join(d, "; "))
	runItem(5, "the authorised flow, both ways", cand, candBin, item5)
	return ok
}

func versionText(bin string) string {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- binaries -----------------------------------------------------------------------------------------------------

func httpGet(url string) ([]byte, error) {
	c := &http.Client{Timeout: downloadTimeout}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// fetchOracle downloads the release's SHA256SUMS and mash-<os>-<arch> for the OS it runs on, and checks the binary against the sums.
func fetchOracle(repo, ver, arch, dir string) (path, sum string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	base := releaseBase(repo) + "v" + ver + "/"
	sums, err := httpGet(base + "SHA256SUMS")
	if err != nil {
		return "", "", err
	}
	m, err := ParseSums(string(sums))
	if err != nil {
		return "", "", err
	}
	name := "mash-" + runtime.GOOS + "-" + arch
	want := m[name]
	if want == "" {
		return "", "", fmt.Errorf("SHA256SUMS of v%s has no %s", ver, name)
	}
	bin, err := httpGet(base + name)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256(bin)
	got := hex.EncodeToString(h[:])
	if got != want {
		return "", "", fmt.Errorf("%s digest %s does not match SHA256SUMS %s", name, got, want)
	}
	path = filepath.Join(dir, "mash")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		return "", "", err
	}
	return path, got, nil
}

// buildCandidate runs scripts/buildrelease in src (a clean clone at commit) and checks the digest it printed.
func buildCandidate(src, commit, version, arch, out string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	cmd := exec.Command("go", "run", "./scripts/buildrelease", "-src", ".", "-out", out, "-goos", runtime.GOOS, "-goarch", arch,
		"-version", version, "-commit", commit)
	cmd.Dir = src
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("buildrelease: %v: %s", err, strings.TrimSpace(e.String()))
	}
	f := strings.Fields(o.String())
	if len(f) < 1 {
		return "", errors.New("buildrelease printed no digest")
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	if got := hex.EncodeToString(h[:]); got != f[0] {
		return "", fmt.Errorf("buildrelease printed %s, the file is %s", f[0], got)
	}
	return f[0], nil
}

// --- the world of one item ----------------------------------------------------------------------------------------

// world is one item's server and endpoints.
type world struct {
	dir, bin, version string
	e                 *testenv.Env
	stop              func()
	admin             *nats.Conn // the observer identity of the test account, not an endpoint
	addr              map[string]sap.Address
	creds             map[string]string
	closers           []func()
	sessions          []*session
}

var labels = []struct{ label, owner string }{{"alice", ownerA}, {"bob", ownerA}, {"carol", ownerA}, {"mallory", ownerB}}

// newWorld starts the two-owner server and provisions the four endpoints, each with the output of admin.Permissions,
// and records whether every user JWT carries exactly that.
func newWorld(dir, bin, version string, rec *Record) (*world, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	e, stop, err := testenv.Launch(filepath.Join(dir, "srv"))
	if err != nil {
		return nil, err
	}
	w := &world{dir: dir, bin: bin, version: version, e: e, stop: stop, addr: map[string]sap.Address{}, creds: map[string]string{}}
	e.Owner = ownerA
	anc, err := nats.Connect(e.URL, natsauth.UserCredentials(e.AdminCreds), nats.Timeout(15*time.Second))
	if err != nil {
		stop()
		return nil, err
	}
	w.admin = anc
	ctx := context.Background()
	adms := map[string]*admin.Admin{}
	for _, o := range []string{ownerA, ownerB} {
		a, err := admin.New(anc, o)
		if err != nil {
			w.close()
			return nil, err
		}
		adms[o] = a
	}
	if err := adms[ownerA].Init(ctx); err != nil {
		w.close()
		return nil, err
	}
	for _, l := range labels {
		a, err := sap.NewAddress(l.owner, group, l.label)
		if err != nil {
			w.close()
			return nil, err
		}
		creds, pub, err := e.MintEndpoint(a)
		if err != nil {
			w.close()
			return nil, err
		}
		if _, err := adms[l.owner].Provision(ctx, a, pub, e.Account, "", admin.Meta{}); err != nil {
			w.close()
			return nil, fmt.Errorf("provision %s: %w", a, err)
		}
		w.addr[l.label], w.creds[l.label] = a, creds
		if problem := exactPermissions(creds, a); problem != "" {
			w.close()
			rec.setup("permissions of "+a.String()+" are exactly admin.Permissions", false, problem)
			return nil, errors.New(problem)
		}
	}
	rec.setup("every endpoint JWT carries exactly admin.Permissions ("+filepath.Base(dir)+")", true, "alice, bob, carol of acme and mallory of zeta")
	lg.f("server %s, endpoints %s %s %s %s", e.URL, w.addr["alice"], w.addr["bob"], w.addr["carol"], w.addr["mallory"])
	return w, nil
}

// exactPermissions decodes the user JWT of a creds file and returns "" only when its permissions are exactly the output
// of admin.Permissions(a).
func exactPermissions(credsPath string, a sap.Address) string {
	raw, err := os.ReadFile(credsPath)
	if err != nil {
		return err.Error()
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return err.Error()
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return err.Error()
	}
	return SamePermissions(a.String(), &uc.Permissions, admin.Permissions(a), uc.Resp)
}

func (w *world) close() {
	for i := len(w.sessions) - 1; i >= 0; i-- {
		w.sessions[i].stop()
	}
	for i := len(w.closers) - 1; i >= 0; i-- {
		w.closers[i]()
	}
	if w.admin != nil {
		w.admin.Close()
	}
	if w.stop != nil {
		w.stop()
	}
}

func (w *world) home(label string) string { return filepath.Join(w.dir, "home", label) }
func (w *world) to(label string) string   { return w.addr[label].String() }

// env is the environment a mash process runs in as label.
func (w *world) env(label string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + w.home(label), "MASH_STATE_DIR=" + filepath.Join(w.home(label), "state"),
		"MASH_ENDPOINT=" + w.to(label), "MASH_SERVER=" + w.e.URL, "MASH_CREDS=" + w.creds[label],
		"MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1"}
}

// conn is a raw NATS connection as label, on that endpoint's own credentials and reply prefix, which records the
// errors the server reports (permission violations among them).
type conn struct {
	nc   *nats.Conn
	errs *Errors
}

func (w *world) connect(label string) (*conn, error) {
	c := &conn{errs: &Errors{}}
	nc, err := nats.Connect(w.e.URL, natsauth.UserCredentials(w.creds[label]), nats.Timeout(15*time.Second),
		nats.CustomInboxPrefix(w.addr[label].InboxPrefix()),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { c.errs.Add(err.Error()) }))
	if err != nil {
		return nil, err
	}
	w.closers = append(w.closers, nc.Close)
	c.nc = nc
	return c, nil
}

// violated waits up to d for a violation of kind that names subject.
func (c *conn) violated(kind, subject string, d time.Duration) bool {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		if Violated(c.errs.All(), kind, subject) {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

// settle makes sure the server has handled everything sent so far and that any violation has been delivered.
func (c *conn) settle() error {
	if err := c.nc.Flush(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

// --- running mash ---------------------------------------------------------------------------------------------------

// proc is a mash command running in the background.
type proc struct {
	cmd         *exec.Cmd
	out, errb   *syncBuf
	done        chan struct{}
	pid         int
	startedWhen time.Time
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// start runs the binary as label with args.
func (w *world) start(label string, args ...string) (*proc, error) {
	if err := os.MkdirAll(w.home(label), 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(w.bin, args...)
	cmd.Env = w.env(label)
	p := &proc{cmd: cmd, out: &syncBuf{}, errb: &syncBuf{}, done: make(chan struct{}), startedWhen: time.Now()}
	cmd.Stdout, cmd.Stderr = p.out, p.errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p.pid = cmd.Process.Pid
	go func() { _ = cmd.Wait(); close(p.done) }()
	return p, nil
}

// wait returns the standard output, standard error and exit status once the command ends; at d it kills that PID only.
func (p *proc) wait(d time.Duration) (stdout, stderr string, exit int, err error) {
	select {
	case <-p.done:
	case <-time.After(d):
		_ = syscall.Kill(p.pid, syscall.SIGKILL) // this command's own recorded PID
		<-p.done
		err = fmt.Errorf("timed out after %s", d)
	}
	return p.out.String(), p.errb.String(), p.cmd.ProcessState.ExitCode(), err
}

// run is start and wait.
func (w *world) run(label string, d time.Duration, args ...string) (string, string, int, error) {
	p, err := w.start(label, args...)
	if err != nil {
		return "", "", -1, err
	}
	return p.wait(d)
}

// who runs `mash who --json` as label.
func (w *world) who(label string) (WhoDoc, string, error) {
	out, errOut, code, err := w.run(label, 60*time.Second, "who", "--json")
	if err != nil {
		return WhoDoc{}, errOut, err
	}
	if code != 0 {
		return WhoDoc{}, errOut, fmt.Errorf("mash who exited %d: %s", code, firstLine(errOut+out))
	}
	d, err := ParseWho([]byte(out))
	return d, errOut, err
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "warning: ") {
			return l
		}
	}
	return strings.TrimSpace(s)
}

// session is one `mash serve` process, driven over MCP on its stdin and stdout.
type session struct {
	label  string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	pid    int
	done   chan struct{}
	errf   *os.File
	nextID int
	once   sync.Once
}

// serve starts label's `mash serve` session, completes the MCP handshake and waits until its prompt endpoint is
// registered.
func (w *world) serve(label string) (*session, error) {
	if err := os.MkdirAll(w.home(label), 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(w.bin, "serve")
	cmd.Env = w.env(label)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	ef, err := os.Create(filepath.Join(w.dir, "serve-"+label+".stderr"))
	if err != nil {
		return nil, err
	}
	cmd.Stderr = ef
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &session{label: label, cmd: cmd, stdin: in, lines: make(chan string, 256), pid: cmd.Process.Pid, done: make(chan struct{}), errf: ef, nextID: 10}
	w.sessions = append(w.sessions, s)
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
		close(s.lines)
	}()
	go func() { _ = cmd.Wait(); close(s.done) }()
	if err := s.handshake(); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	subj, err := w.addr[label].Subjects()
	if err != nil {
		return nil, err
	}
	if !w.waitRegistered(subj.Prompt, true, 20*time.Second) {
		return nil, fmt.Errorf("%s's prompt endpoint never registered", label)
	}
	lg.f("%s's session is up (pid %d)", label, s.pid)
	return s, nil
}

func (s *session) send(line string) error { _, err := io.WriteString(s.stdin, line+"\n"); return err }

// await reads stdout lines until pred accepts one.
func (s *session) await(d time.Duration, pred func(map[string]any) bool) (map[string]any, bool) {
	t := time.After(d)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				return nil, false
			}
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && pred(m) {
				return m, true
			}
		case <-t:
			return nil, false
		}
	}
}

func idIs(id int) func(map[string]any) bool {
	return func(m map[string]any) bool { v, ok := m["id"].(float64); return ok && int(v) == id }
}

func (s *session) handshake() error {
	if err := s.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"experimental":{"claude/channel":{}}},"clientInfo":{"name":"at022","version":"1"}}}`); err != nil {
		return err
	}
	if _, ok := s.await(20*time.Second, idIs(1)); !ok {
		return errors.New("no initialize reply from mash serve")
	}
	if err := s.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		return err
	}
	if err := s.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); err != nil {
		return err
	}
	if _, ok := s.await(20*time.Second, idIs(2)); !ok {
		return errors.New("no tools/list reply from mash serve")
	}
	return nil
}

// prompt waits for a prompt event (a channel event that carries a request id) and returns that id.
func (s *session) prompt(d time.Duration) (string, bool) {
	var id string
	_, ok := s.await(d, func(m map[string]any) bool {
		if m["method"] != "notifications/claude/channel" {
			return false
		}
		p, _ := m["params"].(map[string]any)
		meta, _ := p["meta"].(map[string]any)
		id, _ = meta["request_id"].(string)
		return id != ""
	})
	return id, ok
}

// call runs an MCP tool and returns the raw JSON of its result.
func (s *session) call(tool string, args map[string]any) (string, error) {
	s.nextID++
	id := s.nextID
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err := s.send(string(b)); err != nil {
		return "", err
	}
	m, ok := s.await(15*time.Second, idIs(id))
	if !ok {
		return "", fmt.Errorf("no answer to the %s tool call", tool)
	}
	r, _ := json.Marshal(m["result"])
	return string(r), nil
}

// reply answers a prompt through the session's reply tool.
func (s *session) reply(reqID, text string) error {
	_, err := s.call("reply", map[string]any{"request_id": reqID, "text": text})
	return err
}

// stop ends the process by its recorded PID: stdin closed first, then SIGTERM, then SIGKILL, each to that PID only.
func (s *session) stop() {
	s.once.Do(func() {
		_ = s.stdin.Close()
		for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
			if sig != 0 {
				_ = syscall.Kill(s.pid, sig)
			}
			select {
			case <-s.done:
				s.errf.Close()
				return
			case <-time.After(5 * time.Second):
			}
		}
		s.errf.Close()
	})
}

// registered reports whether some endpoint answers $SRV.INFO with the prompt subject. It asks from the observer
// identity, whose own reply subjects are unrestricted.
func (w *world) registered(subject string) bool {
	inbox := "_INBOX." + ownerA + ".probe." + randHex() // an endpoint may publish only to the inboxes of its own owner
	sub, err := w.admin.SubscribeSync(inbox)
	if err != nil {
		return false
	}
	defer sub.Unsubscribe()
	if w.admin.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil) != nil {
		return false
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			return false
		}
		var info micro.Info
		if json.Unmarshal(m.Data, &info) != nil {
			continue
		}
		for _, ep := range info.Endpoints {
			if ep.Name == "prompt" && ep.Subject == subject {
				return true
			}
		}
	}
	return false
}

func (w *world) waitRegistered(subject string, want bool, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if w.registered(subject) == want {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func randHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// --- the tap --------------------------------------------------------------------------------------------------------

type msg struct {
	Subject, Reply string
	Data           []byte
}

// tap records every message on the subjects of interest, as seen by the observer identity.
type tap struct {
	mu   sync.Mutex
	msgs []msg
	nc   *nats.Conn
}

func (w *world) tap(subjects ...string) (*tap, error) {
	t := &tap{nc: w.admin}
	for _, s := range subjects {
		if _, err := w.admin.Subscribe(s, func(m *nats.Msg) {
			t.mu.Lock()
			t.msgs = append(t.msgs, msg{m.Subject, m.Reply, append([]byte(nil), m.Data...)})
			t.mu.Unlock()
		}); err != nil {
			return nil, err
		}
	}
	return t, w.admin.Flush()
}

func (t *tap) all() []msg {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]msg(nil), t.msgs...)
}

// requestsFrom returns the discovery and prompt requests whose reply subject is under asker's own prefix.
func (t *tap) requestsFrom(asker sap.Address) (discovery, prompt []msg) {
	for _, m := range t.all() {
		if !strings.HasPrefix(m.Reply, asker.InboxPrefix()+".") {
			continue
		}
		if strings.HasPrefix(m.Subject, "$SRV.INFO") {
			discovery = append(discovery, m)
		} else if strings.HasPrefix(m.Subject, "agents.prompt.") {
			prompt = append(prompt, m)
		}
	}
	return
}

func (t *tap) contains(s string) bool {
	for _, m := range t.all() {
		if bytes.Contains(m.Data, []byte(s)) {
			return true
		}
	}
	return false
}

func waitFor(d time.Duration, ok func() bool) bool {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		if ok() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
	}
}

// --- items ----------------------------------------------------------------------------------------------------------

// item1: alice's prompt to bob is held in flight; the tap captures every reply subject alice's commands used; mallory
// (owner zeta) and carol (same owner) attack each captured subject with their own credentials.
func item1(w *world, it *Item) {
	it.NotObservable = append(it.NotObservable,
		"the reply subjects the caller 'says it used' (the constructor's ReplySubjects list) are internal; the set is taken from the server's view only (the tap)",
		"what alice's own connection receives: alice is the `mash prompt` command, so 'alice receives nothing from mallory' is observed as: no message from mallory on any reply subject at the server (tap on _INBOX.>) and the genuine answer is the command's whole output")
	bob, err := w.serve("bob")
	if err != nil {
		it.Error = err.Error()
		return
	}
	tp, err := w.tap("$SRV.INFO.>", "agents.prompt.>", "_INBOX.>")
	if err != nil {
		it.Error = err.Error()
		return
	}
	pr, err := w.start("alice", "prompt", w.to("bob"), "ping")
	if err != nil {
		it.Error = err.Error()
		return
	}
	var captured []string
	if !waitFor(20*time.Second, func() bool {
		d, p := tp.requestsFrom(w.addr["alice"])
		if len(d) >= 1 && len(p) >= 1 {
			captured = captured[:0]
			for _, m := range append(d, p...) {
				captured = append(captured, m.Reply)
			}
			return true
		}
		return false
	}) {
		it.Error = "the tap saw no discovery request and prompt request from alice"
		return
	}
	reqID, ok := bob.prompt(20 * time.Second)
	if !ok {
		it.Error = "bob's session never received alice's prompt"
		return
	}
	it.note("captured %d reply subjects used by alice's commands: %s", len(captured), strings.Join(captured, ", "))
	mallory, err1 := w.connect("mallory")
	carol, err2 := w.connect("carol")
	if err1 != nil || err2 != nil {
		it.Error = fmt.Sprint("connecting mallory and carol: ", err1, err2)
		return
	}
	if _, err := mallory.nc.SubscribeSync("$SRV.>"); err != nil {
		it.Error = err.Error()
		return
	}
	if err := mallory.settle(); err != nil {
		it.Error = err.Error()
		return
	}
	it.check("mallory's own $SRV.> subscription is allowed", len(Violations(mallory.errs.All())) == 0, fmt.Sprint(Violations(mallory.errs.All())))
	it.check("at least one discovery and one prompt reply subject captured", len(captured) >= 2, fmt.Sprintf("%d subjects", len(captured)))
	for _, s := range captured {
		if _, err := mallory.nc.SubscribeSync(s); err != nil {
			it.Error = err.Error()
			return
		}
		it.check("mallory's subscribe to "+s+" is refused, naming the subject", mallory.violated("subscription", s, 5*time.Second), "")
		_ = mallory.nc.Publish(s, []byte(evil))
		_ = mallory.nc.Publish(s, nil)
		it.check("mallory's publish to "+s+" is refused, naming the subject", mallory.violated("publish", s, 5*time.Second), "")
		if _, err := carol.nc.SubscribeSync(s); err != nil {
			it.Error = err.Error()
			return
		}
		it.check("carol's subscribe to "+s+" is refused, naming the subject", carol.violated("subscription", s, 5*time.Second), "")
	}
	if err := bob.reply(reqID, "genuine"); err != nil {
		it.Error = err.Error()
		return
	}
	out, errOut, code, err := pr.wait(30 * time.Second)
	if err != nil {
		it.Error = "alice's prompt did not finish: " + err.Error()
		return
	}
	it.check("alice's prompt finished with the genuine answer, unchanged by the attempts", code == 0 && strings.TrimSpace(out) == "genuine", fmt.Sprintf("exit %d, stdout %q, stderr %q", code, out, NormalStderr(errOut)))
	_ = tp.nc.Flush()
	time.Sleep(200 * time.Millisecond)
	it.check("nothing from mallory reached any subscriber of a reply subject", !tp.contains("EVIL"), "")
}

// genuineRegistration runs bob's real service just long enough to read the registration it answers a discovery with,
// and stops it again: the bytes the forgeries are made from and the valid answer that is replayed.
func genuineRegistration(w *world) ([]byte, error) {
	bob, err := w.serve("bob")
	if err != nil {
		return nil, err
	}
	asker, err := w.connect("alice")
	if err != nil {
		return nil, err
	}
	inbox := w.addr["alice"].InboxPrefix() + ".genuine." + randHex()
	sub, err := asker.nc.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := asker.nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil); err != nil {
		return nil, err
	}
	want, _ := w.addr["bob"].Subjects()
	var found []byte
	for end := time.Now().Add(5 * time.Second); found == nil && time.Now().Before(end); {
		m, err := sub.NextMsg(time.Until(end))
		if err != nil {
			break
		}
		if s, err := PromptSubjectOf(m.Data); err == nil && s == want.Prompt {
			found = m.Data
		}
	}
	bob.stop()
	if found == nil {
		return nil, errors.New("bob's service gave no registration")
	}
	if !w.waitRegistered(want.Prompt, false, 20*time.Second) {
		return nil, errors.New("bob's registration is still answered after his session stopped")
	}
	return found, nil
}

// answerer is carol's connection watching the discovery subject: for every request from alice it publishes the
// forged answers on the reply subject and, in the ahead mode, then bob's genuine answer from bob's own connection.
type answerer struct {
	carol, bob *conn
	sub        *nats.Subscription
	stop       chan struct{}
	done       chan struct{}
	mu         sync.Mutex
	requests   int
}

const racePause = 40 * time.Millisecond

func (w *world) answer(invalid []Answer, valid []byte) (*answerer, error) {
	c, err := w.connect("carol")
	if err != nil {
		return nil, err
	}
	b, err := w.connect("bob")
	if err != nil {
		return nil, err
	}
	a := &answerer{carol: c, bob: b, stop: make(chan struct{}), done: make(chan struct{})}
	sub, err := c.nc.SubscribeSync("$SRV.INFO." + sap.ServiceName)
	if err != nil {
		return nil, err
	}
	a.sub = sub
	if err := c.nc.Flush(); err != nil {
		return nil, err
	}
	prefix := w.addr["alice"].InboxPrefix() + "."
	go func() {
		defer close(a.done)
		for {
			m, err := sub.NextMsg(100 * time.Millisecond)
			select {
			case <-a.stop:
				return
			default:
			}
			if err != nil || !strings.HasPrefix(m.Reply, prefix) {
				continue
			}
			a.mu.Lock()
			a.requests++
			a.mu.Unlock()
			for _, f := range invalid {
				_ = c.nc.Publish(m.Reply, f.Data)
				_ = c.nc.Flush()
			}
			if valid != nil {
				time.Sleep(racePause)
				_ = b.nc.Publish(m.Reply, valid)
				_ = b.nc.Flush()
			}
		}
	}()
	return a, nil
}

func (a *answerer) close() {
	close(a.stop)
	<-a.done
	_ = a.sub.Unsubscribe()
}

func (a *answerer) seen() int { a.mu.Lock(); defer a.mu.Unlock(); return a.requests }

// item2: carol (a same-owner peer, whose publish on _INBOX.acme.> the broker allows) publishes forged registrations
// on the discovery reply subject she observes; alice's `mash who` rejects and counts each and does not attribute it.
func item2(w *world, it *Item) {
	it.NotObservable = append(it.NotObservable,
		"the caller's internal counter: answers_not_verified is read from `mash who --json`, one discovery per command, so the count is per command and the cumulative total of the constructor is not visible",
		"which answer the caller rejected for which reason (unsigned, bad signature or wrong key): only the count and bob's online state are visible")
	genuine, err := genuineRegistration(w)
	if err != nil {
		it.Error = err.Error()
		return
	}
	carolID, err := sapsdk.IdentityFromCredsAt(w.creds["carol"], mustRead(w.creds["carol"]))
	if err != nil {
		it.Error = err.Error()
		return
	}
	bobSubj, _ := w.addr["bob"].Subjects()
	forged, err := Forgeries(genuine, bobSubj.Prompt, carolID)
	if err != nil {
		it.Error = err.Error()
		return
	}
	// the real verification code agrees with how each forgery was made
	for _, f := range forged {
		var info struct {
			Metadata map[string]string `json:"metadata"`
		}
		_ = json.Unmarshal(f.Data, &info)
		md := info.Metadata
		valid := sapsdk.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], bobSubj.Prompt, md["id_sig"])
		if want := f.Name == "wrong-key"; valid != want {
			it.Error = fmt.Sprintf("forgery %s: id_sig verifies=%v, want %v", f.Name, valid, want)
			return
		}
	}
	bobAddr := w.to("bob")
	type cs struct {
		name    string
		invalid []Answer
		valid   []byte
		online  bool
		want    int
	}
	var cases []cs
	for _, f := range forged {
		cases = append(cases, cs{"only/" + f.Name, []Answer{f}, nil, false, 1})
	}
	for _, f := range forged {
		cases = append(cases, cs{"ahead-of-valid/" + f.Name, []Answer{f}, genuine, true, 1})
	}
	cases = append(cases, cs{"all-three-ahead-of-valid", forged, genuine, true, len(forged)})
	for _, c := range cases {
		a, err := w.answer(c.invalid, c.valid)
		if err != nil {
			it.Error = err.Error()
			return
		}
		doc, errOut, err := w.who("alice")
		a.close()
		if err != nil {
			it.Error = c.name + ": " + err.Error()
			return
		}
		listed, online := doc.Online(bobAddr)
		count := doc.Count()
		attrib := "bob is shown offline"
		if c.online {
			attrib = "bob is shown online by the valid answer"
		}
		it.check(c.name+": not attributed to bob", listed && online == c.online,
			fmt.Sprintf("bob listed=%v online=%v (want online=%v: %s); carol saw %d request(s)", listed, online, c.online, attrib, a.seen()))
		it.check(c.name+": counted in answers_not_verified", doc.NotVerified != nil && *doc.NotVerified == c.want,
			fmt.Sprintf("answers_not_verified %s (want %d; absent means the build does not print the count); stderr %q", count, c.want, NormalStderr(errOut)))
		it.note("%s: stderr warning present: %v", c.name, strings.Contains(errOut, "could not be verified"))
	}
}

// item3: AT-016's observer cases through the binary. Carol sees alice's discovery request on $SRV.> and floods the
// guesses at the reply subject for 3 s, with the evil chunk, an empty message and a replay of bob's genuine
// registration. Alice's command must accept none of it. In the prompt phase carol also floods every reply subject she
// saw in the discovery phase, so a candidate that reuses one for its prompt sees her chunk accepted. Last, every reply
// subject she saw must have the inbox layout and none may repeat.
func item3(w *world, it *Item) {
	it.NotObservable = append(it.NotObservable,
		"the caller's list of reply subjects it used (ReplySubjects) is internal; the observer attacks the subjects the server shows it")
	genuine, err := genuineRegistration(w)
	if err != nil {
		it.Error = err.Error()
		return
	}
	// discovery: bob is offline; carol replays his genuine registration on every guess, so a guess that hit alice's
	// reply subject would make him look online.
	attacks, seen, stopObs, err := w.observe(genuine, nil)
	if err != nil {
		it.Error = err.Error()
		return
	}
	doc, errOut, err := w.who("alice")
	stopObs()
	if err != nil {
		it.Error = "discovery: " + err.Error()
		return
	}
	listed, online := doc.Online(w.to("bob"))
	it.check("discovery: no guessed reply subject was accepted (bob stays offline)", listed && !online, fmt.Sprintf("bob listed=%v online=%v, answers_not_verified %s, stderr %q", listed, online, doc.Count(), NormalStderr(errOut)))
	it.check("discovery: the observer saw alice's discovery request and attacked", attacks() > 0, fmt.Sprintf("%d attacked request(s)", attacks()))

	discovery := seen()
	// prompt: bob serves and holds the answer 3.5 s; carol floods the guesses, and the discovery reply subjects
	// themselves, during that time.
	bob, err := w.serve("bob")
	if err != nil {
		it.Error = err.Error()
		return
	}
	attacks, seen, stopObs, err = w.observe(nil, discovery)
	if err != nil {
		it.Error = err.Error()
		return
	}
	pr, err := w.start("alice", "prompt", w.to("bob"), "ping")
	if err != nil {
		stopObs()
		it.Error = err.Error()
		return
	}
	reqID, ok := bob.prompt(20 * time.Second)
	if !ok {
		stopObs()
		it.Error = "bob's session never received alice's prompt"
		return
	}
	time.Sleep(3500 * time.Millisecond)
	if err := bob.reply(reqID, "genuine"); err != nil {
		stopObs()
		it.Error = err.Error()
		return
	}
	out, errOut, code, err := pr.wait(30 * time.Second)
	stopObs()
	if err != nil {
		it.Error = "alice's prompt did not finish: " + err.Error()
		return
	}
	it.check("prompt: the genuine answer arrived and nothing the observer sent was accepted", code == 0 && strings.TrimSpace(out) == "genuine" && !strings.Contains(out, "EVIL"),
		fmt.Sprintf("exit %d, stdout %q, stderr %q", code, out, NormalStderr(errOut)))
	it.check("prompt: the observer saw a discovery request and attacked", attacks() > 0, fmt.Sprintf("%d attacked request(s)", attacks()))
	all := append(append([]string(nil), discovery...), seen()...)
	rerr := CheckReplySubjects(w.addr["alice"].InboxPrefix(), all)
	detail := fmt.Sprintf("%d reply subject(s) seen", len(all))
	if rerr != nil {
		detail += ": " + rerr.Error()
	}
	it.check("reply subjects: every reply subject the observer saw has the inbox layout and none was reused", rerr == nil, detail)
}

// observe starts carol, who watches $SRV.> and, for each discovery request from alice, floods the guesses of Guesses
// for up to 3 s with the evil chunk and an empty message, and with a replay of replay when it is not nil. On each
// attacked request it floods the subjects in also the same way, which are reply subjects from an earlier phase: the
// observed subject itself is never attacked, but an earlier one a candidate reuses is. It records every reply
// subject it sees from alice, in order. It returns the number of attacked requests, the recorded subjects and a
// function that stops it.
func (w *world) observe(replay []byte, also []string) (attacks func() int, seen func() []string, stop func(), err error) {
	c, err := w.connect("carol")
	if err != nil {
		return nil, nil, nil, err
	}
	spy, err := c.nc.SubscribeSync("$SRV.>")
	if err != nil {
		return nil, nil, nil, err
	}
	if err := c.nc.Flush(); err != nil {
		return nil, nil, nil, err
	}
	quit := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	attacked := 0
	var subjects []string
	prefix := w.addr["alice"].InboxPrefix() + "."
	go func() {
		defer close(done)
		for {
			m, err := spy.NextMsg(100 * time.Millisecond)
			select {
			case <-quit:
				return
			default:
			}
			if err != nil || !strings.HasPrefix(m.Subject, "$SRV.INFO") || !strings.HasPrefix(m.Reply, prefix) {
				continue
			}
			mu.Lock()
			attacked++
			subjects = append(subjects, m.Reply)
			mu.Unlock()
			guesses := append(Guesses(m.Reply), also...)
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
				select {
				case <-quit:
					return
				default:
				}
				for _, g := range guesses {
					_ = c.nc.Publish(g, []byte(evil))
					_ = c.nc.Publish(g, nil)
					if replay != nil {
						_ = c.nc.Publish(g, replay)
					}
				}
				_ = c.nc.Flush()
			}
		}
	}()
	return func() int { mu.Lock(); defer mu.Unlock(); return attacked },
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), subjects...) },
		func() { close(quit); <-done; _ = spy.Unsubscribe() }, nil
}

// item4 records the same-owner reply race on one build: carol is given a live prompt reply subject (her grants do not
// let her see one) and publishes a reply chunk on it. The result is returned, never asserted.
func item4(w *world, it *Item, build string) Item4 {
	r := Item4{Build: build}
	it.NotObservable = append(it.NotObservable,
		"whether the caller 'accepted' carol's chunk as the target's answer is seen only as what `mash prompt` prints and its exit status")
	bob, err := w.serve("bob")
	if err != nil {
		it.Error = err.Error()
		return r
	}
	tp, err := w.tap("agents.prompt.>")
	if err != nil {
		it.Error = err.Error()
		return r
	}
	pr, err := w.start("alice", "prompt", w.to("bob"), "ping")
	if err != nil {
		it.Error = err.Error()
		return r
	}
	var live string
	if !waitFor(20*time.Second, func() bool {
		if _, p := tp.requestsFrom(w.addr["alice"]); len(p) > 0 {
			live = p[0].Reply
			return true
		}
		return false
	}) {
		it.Error = "the tap saw no prompt request from alice"
		return r
	}
	reqID, ok := bob.prompt(20 * time.Second)
	if !ok {
		it.Error = "bob's session never received alice's prompt"
		return r
	}
	carol, err := w.connect("carol")
	if err != nil {
		it.Error = err.Error()
		return r
	}
	_ = carol.nc.Publish(live, []byte(`{"type":"response","data":"CAROL"}`))
	_ = carol.nc.Flush()
	time.Sleep(300 * time.Millisecond)
	if err := bob.reply(reqID, "genuine"); err != nil {
		it.Error = err.Error()
		return r
	}
	out, errOut, code, err := pr.wait(30 * time.Second)
	if err != nil {
		it.Error = "alice's prompt did not finish: " + err.Error()
		return r
	}
	r.Ran, r.PromptExit, r.PromptStdout, r.PromptStderr, r.Subject = true, code, out, NormalStderr(errOut), live
	r.CarolViolations = len(Violations(carol.errs.All()))
	it.check("recorded: carol published a chunk on the live reply subject and the prompt ended", true,
		fmt.Sprintf("exit %d, stdout %q, stderr %q, carol's violations %d", code, out, r.PromptStderr, r.CarolViolations))
	it.note("RECORDED on %s: carol's chunk on %s; `mash prompt` printed %q, exit %d, carol's violations %d", build, live, out, code, r.CarolViolations)
	return r
}

// item5: the authorised flow. Alice and bob each run a session; each mails, finds and prompts the other.
func item5(w *world, it *Item) {
	it.NotObservable = append(it.NotObservable,
		"a permission violation on the commands' own connections is seen only in their standard error and exit status; the server's per-connection error list of a `mash` process is not reachable")
	sessions := map[string]*session{}
	for _, l := range []string{"alice", "bob"} {
		s, err := w.serve(l)
		if err != nil {
			it.Error = err.Error()
			return
		}
		sessions[l] = s
	}
	for _, d := range []struct{ from, to string }{{"alice", "bob"}, {"bob", "alice"}} {
		name := d.from + "->" + d.to
		body := "hello from " + d.from
		out, errOut, code, err := w.run(d.from, 60*time.Second, "send", w.to(d.to), body)
		if err != nil {
			it.Error = name + ": send: " + err.Error()
			return
		}
		it.check(name+": mail sent", code == 0, fmt.Sprintf("exit %d, stdout %q, stderr %q", code, firstLine(out), NormalStderr(errOut)))
		got := false
		var last string
		for end := time.Now().Add(20 * time.Second); !got && time.Now().Before(end); time.Sleep(500 * time.Millisecond) {
			last, err = sessions[d.to].call("inbox", map[string]any{})
			got = err == nil && strings.Contains(last, body)
		}
		it.check(name+": the mail is in the recipient's inbox", got, truncate(last, 200))

		doc, errOut, err := w.who(d.from)
		if err != nil {
			it.Error = name + ": who: " + err.Error()
			return
		}
		listed, online := doc.Online(w.to(d.to))
		it.check(name+": discovery finds the other online, nothing unverified", listed && online && (doc.NotVerified == nil || *doc.NotVerified == 0),
			fmt.Sprintf("listed=%v online=%v answers_not_verified %s stderr %q", listed, online, doc.Count(), NormalStderr(errOut)))

		pr, err := w.start(d.from, "prompt", w.to(d.to), "ping")
		if err != nil {
			it.Error = err.Error()
			return
		}
		reqID, ok := sessions[d.to].prompt(20 * time.Second)
		if !ok {
			it.check(name+": the recipient's session received the prompt", false, "no prompt event")
			pr.wait(5 * time.Second)
			continue
		}
		answer := "pong from " + d.to
		if err := sessions[d.to].reply(reqID, answer); err != nil {
			it.Error = err.Error()
			return
		}
		out, errOut, code, err = pr.wait(30 * time.Second)
		if err != nil {
			it.Error = name + ": prompt: " + err.Error()
			return
		}
		it.check(name+": the prompt is answered", code == 0 && strings.TrimSpace(out) == answer, fmt.Sprintf("exit %d, stdout %q, stderr %q", code, out, NormalStderr(errOut)))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func mustRead(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
