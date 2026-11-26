package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/scripts/upgradeproof/inventory"
)

// prereq is step 0: everything the proof needs before it touches mash. A failure here means the run could not be done.
func (r *runner) prereq(s *stepCtx) bool {
	okAll := true
	need := func(pass bool, name, f string, a ...any) {
		if !s.need(pass, name, f, a...) {
			okAll = false
		}
	}
	need(r.goos == "linux" || r.goos == "darwin", "platform", "%s/%s", r.goos, r.goarch)
	for _, c := range []string{"git", "go", "curl", "bash", "openssl", "ss"} {
		if r.goos == "darwin" && c == "ss" {
			continue
		}
		need(onPath(r.pathVar, c), c+" is on PATH", "")
	}
	if !okAll {
		return false
	}
	notes, err := installTools(r.toolsDir, r.goos, r.goarch)
	for _, n := range notes {
		s.note("%s", n)
	}
	need(err == nil, "download gh "+ghVersion+", nats-server "+natsServer+" and nats "+natsCLIVers, "%v", err)
	if !okAll {
		return false
	}
	// The previous release's own files.
	r.prevDir = filepath.Join(r.work, "prev")
	if err := os.MkdirAll(r.prevDir, 0o755); err != nil {
		need(false, "make the folder for the previous release's files", "%v", err)
		return false
	}
	// The previous release's files and its SHA256SUMS come from the same place, so the checksums authenticate nothing by
	// themselves. The launcher verified SHA256SUMS' attestation on its own host and hands over the digest it verified;
	// the file fetched here must have exactly that digest before anything is checked against it.
	base := relBase(r.cfg.releaseRepo)
	digestDetail := "digest " + strings.ToLower(r.cfg.prevSumsSHA)
	if !sumsDigestOK(r.cfg.prevSumsSHA) {
		digestDetail = fmt.Sprintf("none was given: run scripts/upgradeproof/at011.sh, which verifies the attestation of SHA256SUMS on its host with gh and passes the digest in; or run `gh attestation verify SHA256SUMS -R %s --signer-workflow %s/.github/workflows/release.yml --source-ref refs/tags/%s --source-digest <commit of the tag>` and pass the sha256 of that file", r.cfg.releaseRepo, r.cfg.releaseRepo, oldTag)
	}
	if !s.need(sumsDigestOK(r.cfg.prevSumsSHA), "the verified digest of "+oldTag+" SHA256SUMS was supplied (-prev-sums-sha256)", "%s", digestDetail) {
		return false
	}
	sums, err := get(base + "/SHA256SUMS")
	need(err == nil, "download "+oldTag+" SHA256SUMS from "+r.cfg.releaseRepo, "%v", err)
	if !okAll {
		return false
	}
	if got := sumBytes(sums); !s.need(got == strings.ToLower(r.cfg.prevSumsSHA), oldTag+" SHA256SUMS is the file the launcher verified",
		"fetched sha256 %s, verified sha256 %s: nothing fetched from the release is trusted", got, r.cfg.prevSumsSHA) {
		return false
	}
	s.note("%s SHA256SUMS sha256 %s: attestation-verified on the launcher host (gh attestation verify, repository %s); the guest checks only that it fetched that exact file", oldTag, r.cfg.prevSumsSHA, r.cfg.releaseRepo)
	r.evid["previous-release.json"] = map[string]string{"repository": r.cfg.releaseRepo, "tag": oldTag, "sha256sums_sha256": strings.ToLower(r.cfg.prevSumsSHA),
		"sha256sums_attestation": "verified on the launcher host with gh attestation verify, signer workflow release.yml, source ref refs/tags/" + oldTag}
	r.prevSums = string(sums)
	_ = os.WriteFile(filepath.Join(r.prevDir, "SHA256SUMS"), sums, 0o644)
	for _, n := range []string{"install.sh", "mash-local-nats-setup.sh"} {
		b, err := download(base, n, r.prevSums)
		need(err == nil, "download "+oldTag+" "+n+" and check it against SHA256SUMS", "%v", err)
		if err == nil {
			_ = os.WriteFile(filepath.Join(r.prevDir, n), b, 0o755)
		}
	}
	r.prevInstal = filepath.Join(r.prevDir, "install.sh")
	r.prevSetup = filepath.Join(r.prevDir, "mash-local-nats-setup.sh")
	for _, b := range release.Binaries() {
		_, ok := sumsLine(r.prevSums, b.AssetName())
		need(ok, oldTag+" SHA256SUMS lists "+b.AssetName(), "")
	}
	if !okAll {
		return false
	}
	// The candidate: source, the eight builds, the sums.
	r.src = filepath.Join(r.work, "src")
	err = r.checkoutSource(r.src)
	need(err == nil, "check out the candidate "+r.cfg.commit, "%v", err)
	if !okAll {
		return false
	}
	r.cand, err = r.buildCandidate(r.src, filepath.Join(r.work, "mirror", "v"+r.cfg.version))
	need(err == nil, "build the eight binaries with buildrelease and make the SHA256SUMS", "%v", err)
	if !okAll {
		return false
	}
	s.note("candidate SHA256SUMS sha256 %s, install.sh sha256 %s", r.cand.sumsSHA, r.cand.installS)
	r.mir, err = startMirror(filepath.Join(r.work, "mirror"), "/v"+r.cfg.version, filepath.Join(r.work, "mirror-ca.pem"))
	need(err == nil, "start the https mirror", "%v", err)
	if okAll {
		s.note("mirror at %s", r.mir.base)
	}
	return okAll
}

// recordProc notes the session's PID, reported version, executable path and digest.
func (r *runner) recordProc(s *stepCtx, p *persona, ss *session) proc {
	ver, err := ss.reportedVersion()
	s.need(err == nil, p.name+"'s session answers the version tool", "%v", err)
	path, sum, ok := exeInfo(ss.pid)
	if !ok {
		// Where the OS does not show the running file, the digest is of the installed file at the start.
		path = p.bin("mash")
		sum, _ = fileSHA(path)
		s.note("%s: the OS does not show the running executable; its digest is the installed file's at start", p.name)
	}
	pr := proc{Persona: p.name, PID: ss.pid, Reported: ver, InitVer: ss.initVer, ExePath: path, ExeSHA256: sum,
		Started: time.Now().UTC().Format(time.RFC3339)}
	r.procs = append(r.procs, pr)
	s.note("%s session: pid %d, reports %s (initialize %s), executable %s, sha256 %s", p.name, pr.PID, pr.Reported, pr.InitVer, pr.ExePath, pr.ExeSHA256)
	return pr
}

func (r *runner) startSessionFor(s *stepCtx, p *persona, n int) (*session, bool) {
	ev := filepath.Join(r.cfg.out, "evidence")
	ss, err := startSession(p.name, p.bin("mash"), r.sessionEnv(p), filepath.Join(ev, fmt.Sprintf("serve-%s-%d.stderr", p.name, n)))
	if !s.need(err == nil, "open "+p.name+"'s session", "%v", err) {
		return nil, false
	}
	r.sessions[p.name] = ss
	return ss, true
}

// step1: install the previous release, the local server, mashctl init, two endpoints, two open sessions.
func (r *runner) step1(s *stepCtx) {
	for _, n := range []string{"admin", "alice", "bob", "mallory"} {
		p := r.personas[n]
		for _, d := range []string{p.home, p.cfg, p.st} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				s.need(false, "make "+d, "%v", err)
				return
			}
		}
	}
	for _, n := range []string{"admin", "alice", "bob", "mallory"} {
		p := r.personas[n]
		res := r.install(p, r.prevInstal, oldTag, "", true)
		s.ok(res.ok(), n+": install.sh "+oldTag+" with gh on PATH exits 0", "exit %d; what install.sh printed is in the log above", res.Code)
		if !res.ok() {
			s.note("install.sh printed (verbatim): %s", strings.ReplaceAll(strings.TrimSpace(res.Out), "\n", " | "))
			return
		}
		mashS, _ := fileSHA(p.bin("mash"))
		ctlS, _ := fileSHA(p.bin("mashctl"))
		wantM, _ := sumsLine(r.prevSums, "mash-"+r.goos+"-"+r.goarch)
		wantC, _ := sumsLine(r.prevSums, "mashctl-"+r.goos+"-"+r.goarch)
		s.ok(mashS == wantM && ctlS == wantC, n+": installed files are the published "+oldTag+" digests", "mash %s mashctl %s", mashS, ctlS)
		if n == "alice" {
			s.ok(!strings.Contains(res.Out, "warning:"), "install.sh "+oldTag+" with gh printed no warning (the attestation was verified)", "")
		}
	}
	for _, n := range []string{"admin", "alice"} {
		p := r.personas[n]
		v := r.run(r.env(p, false, false), p.home, time.Minute, p.bin("mash"), "version")
		s.ok(v.ok() && strings.Contains(v.Out, strings.TrimPrefix(oldTag, "v")), n+": mash version reports "+oldTag, "%s", strings.TrimSpace(v.Out))
	}
	// The local server.
	admin := r.personas["admin"]
	r.natsDir = filepath.Join(r.work, "nats")
	res := r.run(r.env(admin, false, true), r.work, setupTimeout, "bash", r.prevSetup, "--owner", owner, "--dir", r.natsDir)
	if !s.need(res.ok(), "mash-local-nats-setup.sh "+oldTag, "exit %d", res.Code) {
		return
	}
	yaml := filepath.Join(r.natsDir, "mash-setup.yaml")
	if m := regexp.MustCompile(`(?m)^mashctl init --config (\S+)`).FindStringSubmatch(res.Out); m != nil {
		yaml = m[1]
	}
	pidb, err := os.ReadFile(filepath.Join(r.natsDir, "server.pid"))
	if s.need(err == nil, "the setup script wrote server.pid", "%v", err) {
		fmt.Sscan(strings.TrimSpace(string(pidb)), &r.natsPID)
		s.note("recorded local server pid %d", r.natsPID)
	}
	res = r.mashctl("init", "--config", yaml)
	if !s.need(res.ok(), "mashctl init", "exit %d", res.Code) {
		return
	}
	for _, n := range []string{"alice", "bob", "mallory"} {
		p := r.personas[n]
		join := filepath.Join(r.work, n+".join")
		res = r.mashctl("endpoint", "create", p.addr, "--out", join)
		if !s.need(res.ok(), "create endpoint "+p.addr, "exit %d", res.Code) {
			return
		}
		res = r.run(r.env(p, false, false), p.home, time.Minute, p.bin("mash"), "join", join, "--config-dir", p.cfg)
		if !s.need(res.ok(), p.name+" joins", "exit %d", res.Code) {
			return
		}
	}
	for _, n := range []string{"alice", "bob"} {
		ss, ok := r.startSessionFor(s, r.personas[n], 1)
		if !ok {
			return
		}
		pr := r.recordProc(s, r.personas[n], ss)
		s.ok(pr.Reported == strings.TrimPrefix(oldTag, "v"), n+"'s open session reports "+oldTag, "reports %s", pr.Reported)
	}
	s.ok(r.natsPID > 0 && processAlive(r.natsPID), "the local server is running", "pid %d", r.natsPID)
}

// step2: stored mail in Alice's inbox (and some in Bob's): delivered, pending and the ten shapes, plus the X candidates.
func (r *runner) step2(s *stepCtx) {
	alice, bob, mallory := r.personas["alice"], r.personas["bob"], r.personas["mallory"]
	time.Sleep(3 * time.Second)
	// Delivered before the upgrade: mail the session was shown.
	for _, t := range []string{"d01", "d02", "d03"} {
		if !r.mailEntry(s, bob, alice, t, "from Bob, shown before the upgrade "+t, "step2", inventory.Delivered) {
			return
		}
	}
	if !r.mailEntry(s, alice, bob, "e01", "from Alice, shown before the upgrade e01", "step2", inventory.Delivered) {
		return
	}
	time.Sleep(3 * time.Second)
	err := r.drain(alice)
	s.need(err == nil, "Alice's hook shows the first mail", "%v", err)
	err = r.drain(bob)
	s.need(err == nil, "Bob's hook shows the first mail", "%v", err)
	for _, t := range []string{"d01", "d02", "d03"} {
		s.need(r.invOf(alice.addr).Tally.Shown(inventory.ID{Seq: seqOf(r, alice.addr, t), Sender: bob.addr, Nonce: t}) == 1, "Alice was shown "+t+" once before the upgrade", "")
	}
	// The ten shapes and the four candidates, stored as raw messages.
	if !r.inject(s, alice, mallory) {
		return
	}
	// Mail in flight: stored and spooled, the hook not yet run.
	for _, t := range []string{"p01", "p02"} {
		if !r.mailEntry(s, bob, alice, t, "from Bob, still waiting "+t, "step2", inventory.Pending) {
			return
		}
	}
	if !r.mailEntry(s, alice, bob, "q01", "from Alice, still waiting q01", "step2", inventory.Pending) {
		return
	}
	time.Sleep(3 * time.Second)
	rows, raw, err := r.adminInbox(alice.addr)
	if !s.need(err == nil, "mashctl lists Alice's inbox before the upgrade", "%v", err) {
		return
	}
	r.before = rows
	r.evid["alice-inbox-before.json"] = rawOrText(raw)
	s.need(len(rows) == len(r.invOf(alice.addr).Entries), "the listing holds every inventoried message", "%d listed, %d inventoried", len(rows), len(r.invOf(alice.addr).Entries))
	s.need(len(inventory.Check(r.invOf(alice.addr).Entries)) == 0, "Alice's inventory is complete", "%v", inventory.Check(r.invOf(alice.addr).Entries))
	s.need(len(inventory.Check(r.invOf(bob.addr).Entries)) == 0, "Bob's inventory is complete", "%v", inventory.Check(r.invOf(bob.addr).Entries))
}

func seqOf(r *runner, addr, token string) uint64 {
	for _, e := range r.invOf(addr).Entries {
		if e.ID.Nonce == token {
			return e.ID.Seq
		}
	}
	return 0
}

func rawOrText(s string) any {
	return strings.TrimSpace(s)
}

func processAlive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }
