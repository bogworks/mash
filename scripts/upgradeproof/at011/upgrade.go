package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/scripts/upgradeproof/inventory"
)

const ghMissingWarning = "warning: gh not found: checked checksums only, not verified"

// The actions of an upgrade, which the runner knows how to carry out. Every release's notes and the upgrade page give
// them in the order of upgradeActions.
const (
	actCheck   = "check: mash version and mash doctor, mashctl doctor"
	actInstall = "install both programs"
	actPlugins = "update the plugin"
	actRestart = "restart each session"
	actAfter   = "check again"
)

// upgradeActions is the canonical order of the five actions of an upgrade; it names no version.
var upgradeActions = []string{actCheck, actInstall, actPlugins, actRestart, actAfter}

var numberedRE = regexp.MustCompile(`(?m)^\s*\d+[.)]\s+(.+)$`)

// pageSteps is the numbered steps of the section "Upgrading from <prev>" of the page, or none. The section runs to the
// next heading of its level or above; other upgrade sections (an older version's) are not read.
func pageSteps(page, prev string) []string {
	head := regexp.MustCompile(`(?i)upgrading from ` + regexp.QuoteMeta(prev) + `(?:$|[^0-9.]|\.(?:$|[^0-9]))`)
	in, level := false, 0
	var sect []string
	for _, l := range strings.Split(page, "\n") {
		if strings.HasPrefix(l, "#") {
			n := len(l) - len(strings.TrimLeft(l, "#"))
			if !in && head.MatchString(l) {
				in, level = true, n
				continue
			}
			if in && n <= level {
				in = false
			}
		}
		if in {
			sect = append(sect, l)
		}
	}
	var out []string
	for _, m := range numberedRE.FindAllStringSubmatch(strings.Join(sect, "\n"), -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

var headingRE = regexp.MustCompile(`^(#+)\s+(.*)$`)

// notesSteps is the numbered steps of the first section of a release notes document whose heading starts with
// "Upgrading" (any case). The section runs to the next heading of its level or above. No such section gives none.
func notesSteps(notes string) []string {
	in, level := false, 0
	var sect []string
	for _, l := range strings.Split(notes, "\n") {
		if m := headingRE.FindStringSubmatch(l); m != nil {
			if in && len(m[1]) <= level {
				break
			}
			if !in && strings.HasPrefix(strings.ToLower(strings.TrimSpace(m[2])), "upgrading") {
				in, level = true, len(m[1])
				continue
			}
		}
		if in {
			sect = append(sect, l)
		}
	}
	var out []string
	for _, m := range numberedRE.FindAllStringSubmatch(strings.Join(sect, "\n"), -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// orderFrom maps each page step to the action it asks for. ok is false when a step is not one the runner knows.
func orderFrom(steps []string) (order []string, unknown []string) {
	for _, st := range steps {
		// A step is named by its first sentence; the rest explains (step 3 of 0.6.0 says "installs" of the plugin).
		l := strings.ToLower(st)
		if i := strings.Index(l, ". "); i >= 0 {
			l = l[:i]
		}
		switch {
		case strings.Contains(l, "restart"):
			order = append(order, actRestart)
		case strings.Contains(l, "install"):
			order = append(order, actInstall)
		case strings.Contains(l, "plugin"):
			order = append(order, actPlugins)
		case strings.Contains(l, "doctor") || strings.Contains(l, "version") || strings.Contains(l, "check"):
			if len(order) > 0 && order[len(order)-1] != actPlugins && containsAct(order, actInstall) {
				order = append(order, actAfter)
			} else {
				order = append(order, actCheck)
			}
		default:
			unknown = append(unknown, st)
		}
	}
	return order, unknown
}

// orderProblem says what is wrong with the order the page gives, or "" when it is exactly the five actions of an
// upgrade, each once, in order, and every step is one the runner knows.
func orderProblem(order, unknown []string) string {
	var bad []string
	if len(unknown) > 0 {
		bad = append(bad, fmt.Sprintf("steps the runner does not understand: %q", unknown))
	}
	var missing []string
	for _, a := range upgradeActions {
		if !containsAct(order, a) {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		bad = append(bad, fmt.Sprintf("missing: %s", strings.Join(missing, "; ")))
	}
	if len(unknown) == 0 && len(missing) == 0 && strings.Join(order, "|") != strings.Join(upgradeActions, "|") {
		bad = append(bad, fmt.Sprintf("the actions are repeated or out of order: got %s; want %s", strings.Join(order, "; "), strings.Join(upgradeActions, "; ")))
	}
	return strings.Join(bad, "; ")
}

func containsAct(order []string, a string) bool {
	for _, o := range order {
		if o == a {
			return true
		}
	}
	return false
}

// upgrade carries one persona through the upgrade page's order. During the mixed period the other persona is untouched.
// The REQ-023 checks sit between the install and the restart.
func (r *runner) upgrade(s *stepCtx, p *persona, order []string, mailFrom *persona, token string, withAdmin bool) bool {
	old := r.sessions[p.name]
	var before proc
	for _, pr := range r.procs {
		if pr.Persona == p.name {
			before = pr
		}
	}
	oldVer := r.cfg.prev
	newVer := r.cfg.version
	for _, act := range order {
		s.note("upgrade page order: %s", act)
		switch act {
		case actCheck, actAfter:
			bin := p.bin("mash")
			v := r.run(r.env(p, false, false), p.home, time.Minute, bin, "version")
			d := r.run(r.env(p, false, false), p.home, doctorTimeout, bin, "doctor")
			ctl := r.mashctl("doctor")
			if act == actCheck {
				s.ok(strings.Contains(v.Out, oldVer), p.name+": mash version reports "+oldVer+" before the upgrade", "%s", strings.TrimSpace(v.Out))
				s.ok(d.ok(), p.name+": mash doctor exits 0 before the upgrade", "exit %d", d.Code)
				s.ok(ctl.ok(), "mashctl doctor exits 0 before the upgrade", "exit %d", ctl.Code)
			} else {
				s.ok(strings.Contains(v.Out, newVer), p.name+": mash version reports "+newVer+" after the upgrade", "%s", strings.TrimSpace(v.Out))
				s.ok(d.ok(), p.name+": mash doctor exits 0 after the upgrade", "exit %d", d.Code)
				s.ok(ctl.ok(), "mashctl doctor exits 0 after the upgrade", "exit %d", ctl.Code)
			}
		case actInstall:
			targets := []*persona{p}
			if withAdmin {
				targets = append(targets, r.personas["admin"])
			}
			for _, t := range targets {
				mark := len(r.mir.requests())
				res := r.install(t, filepath.Join(r.cand.dir, "install.sh"), "v"+newVer, r.mir.base, false)
				s.ok(res.ok(), t.name+": install.sh v"+newVer+" without gh exits 0", "exit %d", res.Code)
				s.ok(strings.Contains(res.Out, ghMissingWarning), t.name+": install.sh printed the warning that gh was not found", "")
				got := map[string]string{}
				for _, n := range []string{"mash", "mashctl"} {
					got[n], _ = fileSHA(t.bin(n))
					want := r.cand.hashes[release.Binary{Program: n, Target: release.Target{GOOS: r.goos, GOARCH: r.goarch}}]
					s.ok(got[n] == want, t.name+": installed "+n+" is the candidate build", "installed %s, built %s", got[n], want)
				}
				fetched := r.mir.requestedFiles(mark)
				s.ok(contains(fetched, "SHA256SUMS"), t.name+": install.sh fetched SHA256SUMS from the mirror", "fetched %v", fetched)
			}
			r.checkServedSums(s)
		case actPlugins:
			s.note("update the plugin: there is no Claude Code in this guest, so the plugin step does not apply (recorded, not skipped silently)")
		case actRestart:
			// REQ-023: the open session, before it is restarted.
			s.ok(old.alive(), p.name+" REQ-023: the open session is alive after the install", "pid %d", before.PID)
			ver, err := old.reportedVersion()
			s.ok(err == nil && ver == oldVer, p.name+" REQ-023: the open session still reports "+oldVer, "reports %q %v", ver, err)
			path, sum, ok := exeInfo(before.PID)
			if ok {
				s.ok(sum == before.ExeSHA256, p.name+" REQ-023: the open session's executable is unchanged", "path %s, sha256 %s, recorded %s", path, sum, before.ExeSHA256)
			} else {
				s.note("%s: the OS does not show the running executable, so its digest cannot be compared", p.name)
			}
			if mailFrom != nil {
				if r.mailEntry(s, mailFrom, p, token, "sent after the install, before the restart "+token, "step3", inventory.Pending) {
					time.Sleep(3 * time.Second)
					// The hook of the session (the new program) shows what the open session spooled.
					err := r.drain(p)
					s.ok(err == nil, p.name+" REQ-023: the open session still receives mail (its hook shows it)", "%v", err)
					got := r.invOf(p.addr).Tally.Shown(inventory.ID{Seq: seqOf(r, p.addr, token), Sender: mailFrom.addr, Nonce: token})
					s.ok(got == 1, p.name+" REQ-023: "+token+" arrived while the old session was open", "shown %d times", got)
				}
			}
			exited, status := old.stop(30 * time.Second)
			s.ok(exited && status == 0, p.name+": the old session ends cleanly when its input closes", "exited %v status %d", exited, status)
			ss, ok := r.startSessionFor(s, p, 2)
			if !ok {
				return false
			}
			pr := r.recordProc(s, p, ss)
			s.ok(pr.Reported == newVer, p.name+" REQ-023: after the restart the session reports "+newVer, "reports %s", pr.Reported)
			s.ok(pr.PID != before.PID, p.name+": the restarted session is a new process", "pid %d, was %d", pr.PID, before.PID)
			time.Sleep(6 * time.Second) // the new session scans the stored mail before it delivers
		}
	}
	return true
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// checkServedSums fetches SHA256SUMS from the mirror like a client would and holds it to the candidate's digests.
func (r *runner) checkServedSums(s *stepCtx) {
	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(r.mir.caPath)
	pool.AppendCertsFromPEM(pem)
	c := &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	resp, err := c.Get(r.mir.base + "/SHA256SUMS")
	if !s.need(err == nil, "fetch SHA256SUMS from the mirror", "%v", err) {
		return
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	served := b.String()
	s.ok(sumBytes([]byte(served)) == r.cand.sumsSHA, "the served SHA256SUMS is the one made for the candidate", "sha256 %s", sumBytes([]byte(served)))
	for _, n := range []string{"mash", "mashctl"} {
		name := fmt.Sprintf("%s-%s-%s", n, r.goos, r.goarch)
		d, ok := sumsLine(served, name)
		want := r.cand.hashes[release.Binary{Program: n, Target: release.Target{GOOS: r.goos, GOARCH: r.goarch}}]
		s.ok(ok && d == want, "the served SHA256SUMS lists the candidate digest of "+name, "served %s built %s", d, want)
	}
	d, ok := sumsLine(served, "install.sh")
	s.ok(ok && d == r.cand.installS, "the served SHA256SUMS lists the candidate's install.sh", "served %s", d)
}
