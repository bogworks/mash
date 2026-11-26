// Command at011 is the AT-011 upgrade proof: it takes the previous release of mash and moves it, with mail in flight,
// to the candidate commit, and writes the receipt release-check reads (kind at-011), a log of every step and the
// evidence behind each verdict.
//
//	at011 run -commit <full sha> -version 0.6.0 -prev 0.5.10 -out <dir> -work <new absolute dir> -pack <objects.pack>
//	          -prev-sums-sha256 <hex> [-release-repo owner/name] [-repo <url|path>] [-venue <text>]
//	at011 finalize -out <dir> -statement <text> -container-removed=true|false
//
// run does everything inside the machine it runs on and must run in a throwaway one: a container (at011.sh starts it)
// or a VM guest. It makes accounts' worth of state under -work and never touches anything outside it and the output.
// finalize is run afterwards, by whoever destroyed the machine, to add the statement of the destruction to the receipt.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/release"
	"github.com/bogworks/mash/scripts/upgradeproof/receipt"
)

func main() { os.Exit(realMain(os.Args[1:])) }

func realMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: at011 run|finalize ...")
		return 2
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "finalize":
		return cmdFinalize(args[1:])
	}
	fmt.Fprintf(os.Stderr, "at011: unknown command %q\n", args[0])
	return 2
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c config
	fs.StringVar(&c.commit, "commit", "", "the candidate's full commit id (40 hex digits)")
	fs.StringVar(&c.version, "version", release.SwitchOnVersion, "the candidate's version, without the v")
	fs.StringVar(&c.prev, "prev", "0.5.10", "the previous release's version, without the v")
	fs.StringVar(&c.out, "out", "", "the output directory (receipt, log, evidence)")
	fs.StringVar(&c.pack, "pack", "", "a git pack of the candidate commit's objects (git pack-objects --revs)")
	fs.StringVar(&c.repo, "repo", "", "a repository to clone the candidate from, when there is no pack")
	fs.StringVar(&c.work, "work", "", "the working directory the run makes, an absolute path; it must not exist")
	fs.StringVar(&c.releaseRepo, "release-repo", defaultRepo, "the GitHub repository (owner/name) the previous release is read from")
	fs.StringVar(&c.prevSumsSHA, "prev-sums-sha256", "", "the sha256 of the previous release's SHA256SUMS, as verified by `gh attestation verify` on the launching host")
	fs.StringVar(&c.venue, "venue", "", "where the run happens, in words, for the receipt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(c.commit) != 40 || c.out == "" || c.prev != "0.5.10" || (c.pack == "" && c.repo == "") {
		fmt.Fprintln(os.Stderr, "at011 run: -commit (40 hex digits), -out, -pack or -repo are required; -prev is 0.5.10")
		return 2
	}
	if !filepath.IsAbs(c.work) {
		fmt.Fprintln(os.Stderr, "at011 run: -work is required and must be an absolute path (a folder that does not exist yet)")
		return 2
	}
	if !repoRE.MatchString(c.releaseRepo) {
		fmt.Fprintf(os.Stderr, "at011 run: -release-repo %q is not owner/name\n", c.releaseRepo)
		return 2
	}
	if _, err := os.Stat(c.work); err == nil {
		fmt.Fprintf(os.Stderr, "at011 run: %s exists; the run needs a machine and a folder made for it\n", c.work)
		return 2
	}
	goos, goarch, err := platform()
	if err != nil {
		fmt.Fprintln(os.Stderr, "at011:", err)
		return 2
	}
	if err := os.MkdirAll(filepath.Join(c.out, "evidence"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "at011:", err)
		return 2
	}
	lf, err := os.Create(filepath.Join(c.out, fmt.Sprintf("at011-%s-%s.log", goos, goarch)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "at011:", err)
		return 2
	}
	defer lf.Close()
	r := &runner{cfg: c, log: &logger{w: io.MultiWriter(lf, os.Stdout)}, goos: goos, goarch: goarch,
		pathVar: os.Getenv("PATH"), work: c.work, toolsDir: filepath.Join(c.work, "tools"),
		personas: map[string]*persona{}, sessions: map[string]*session{}, inv: map[string]*invSet{}, evid: map[string]any{}}
	if err := os.MkdirAll(filepath.Join(r.work, "runhome"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "at011:", err)
		return 2
	}
	if _, err := os.Lstat(r.noPluginRoot()); !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "at011: %s must not exist (it is the plugin root with no manifest): %v\n", r.noPluginRoot(), err)
		return 2
	}
	for _, n := range []string{"admin", "alice", "bob", "mallory"} {
		p := &persona{name: n, home: filepath.Join(r.work, n, "home"), cfg: filepath.Join(r.work, n, "cfg"), st: filepath.Join(r.work, n, "state")}
		if n != "admin" {
			p.addr = owner + "." + group + "." + n
		}
		r.personas[n] = p
	}
	return r.run1()
}

func (r *runner) run1() int {
	start := time.Now().UTC()
	r.log.printf("AT-011 upgrade proof: %s -> candidate %s (version %s) on %s/%s, %s, user uid %d",
		oldTag, r.cfg.commit, r.cfg.version, r.goos, r.goarch, start.Format(time.RFC3339), os.Getuid())
	r.log.printf("venue: %s", r.cfg.venue)
	defer r.cleanup()

	proceed := r.step("0", "prerequisites: tools, the previous release's files, the candidate's build and mirror", func(s *stepCtx) { r.prereq(s) })
	skip := func(n, title string) {
		r.log.printf("")
		r.log.printf("== STEP %s: %s", n, title)
		r.log.printf("  FAIL: not run: an earlier step could not be carried out")
		r.log.printf("== STEP %s exit status: 1 (failed)", n)
		r.steps = append(r.steps, &stepRecord{N: n, Title: title, Checks: []check{{Name: "not run: an earlier step could not be carried out", Kind: "runner"}}})
	}
	type stp struct {
		n, title string
		fn       func(*stepCtx)
		hard     bool // when it fails the later steps cannot run
	}
	list := []stp{
		{"1", "install the previous release and open Alice's and Bob's sessions", r.step1, true},
		{"2", "mail in Alice's inbox: delivered, pending, the ten shapes", r.step2, true},
		{"3", "build the candidate, install it for Alice without gh, follow the upgrade page, REQ-023", r.step3, false},
		{"4", "mixed period: Alice on the candidate, Bob on the previous release", r.step4, false},
		{"5", "Bob upgrades the same way", r.step5, false},
		{"6", "exactly-once dispositions, message by message", r.step6, true},
		{"7", "mashctl endpoint inbox against Alice before and after", r.step7, false},
		{"8", "the upgrade page and the release notes", r.step8, false},
	}
	for _, st := range list {
		if !proceed {
			skip(st.n, st.title)
			continue
		}
		if ok := r.step(st.n, st.title, st.fn); !ok && st.hard {
			proceed = false
		}
	}
	all := true
	for _, st := range r.steps {
		if !st.Passed {
			all = false
		}
	}
	var rec receipt.Receipt
	var recErr error
	r.step("9", "bind the proof's fields into the receipt", func(s *stepCtx) {
		if r.cand == nil {
			s.need(false, "the candidate was built", "step 0 did not complete")
			return
		}
		rec, recErr = r.receipt()
		if !s.need(recErr == nil, "gather the bound fields", "%v", recErr) {
			return
		}
		rec.Result = receipt.ResultPassed
		ps := rec.Problems()
		s.need(len(ps) == 0, "every field the gate binds is present and well formed", "%v", ps)
		s.need(all, "every step before this one passed", "a receipt says passed only then")
		s.note("candidate %s; install.sh %s; SHA256SUMS %s; previous version %s; upgrade page blob %s", rec.Commit, rec.InstallSHA256, rec.SumsSHA256, rec.PrevVersion, rec.UpgradePage)
	})
	pass := true
	for _, st := range r.steps {
		if !st.Passed {
			pass = false
		}
	}
	r.log.printf("")
	r.log.printf("== SUMMARY")
	for _, st := range r.steps {
		w := "PASS"
		if !st.Passed {
			w = "FAIL"
		}
		r.log.printf("| %s | %s | %s |", st.N, st.Title, w)
	}
	rec.Result = receipt.ResultFailed
	rec.ExitStatus = 1
	if pass {
		rec.Result, rec.ExitStatus = receipt.ResultPassed, 0
	}
	if r.cand != nil && recErr == nil {
		b, err := receipt.Marshal(rec)
		if err == nil {
			err = os.WriteFile(filepath.Join(r.cfg.out, receipt.FileName(r.goos, r.goarch)), b, 0o644)
		}
		if err != nil {
			r.log.printf("could not write the receipt: %v", err)
			pass = false
		}
	} else {
		r.log.printf("no receipt written: the fields it binds could not be gathered")
	}
	r.log.printf("RESULT: %s (candidate %s)", rec.Result, r.cfg.commit)
	r.writeEvidence(start)
	if pass {
		return 0
	}
	return 1
}

// receipt gathers the fields the gate binds for this platform.
func (r *runner) receipt() (receipt.Receipt, error) {
	rec := receipt.Receipt{Kind: receipt.KindAT011, Commit: r.cfg.commit, InstallSHA256: r.cand.installS, SumsSHA256: r.cand.sumsSHA,
		PrevVersion: r.cfg.prev,
		Environment: fmt.Sprintf("%s; this machine was created for this run and nothing outside it was used; the previous release was read from %s and its SHA256SUMS (sha256 %s) was attestation-verified on the launcher host",
			r.cfg.venue, r.cfg.releaseRepo, strings.ToLower(r.cfg.prevSumsSHA))}
	for _, n := range []string{"mash", "mashctl"} {
		h, ok := r.cand.hashes[release.Binary{Program: n, Target: release.Target{GOOS: r.goos, GOARCH: r.goarch}}]
		if !ok {
			return rec, fmt.Errorf("no build of %s for %s/%s", n, r.goos, r.goarch)
		}
		rec.Executables = append(rec.Executables, receipt.Exec{Name: n, GOOS: r.goos, GOARCH: r.goarch, SHA256: h,
			Build: receipt.BuildConfig{Toolchain: release.Toolchain, Tags: []string{}, Flags: release.BuildFlags(r.cfg.version, r.cfg.commit)}})
	}
	for _, b := range release.Binaries() {
		d, ok := sumsLine(r.prevSums, b.AssetName())
		if !ok {
			return rec, fmt.Errorf("the previous release's SHA256SUMS has no %s", b.AssetName())
		}
		rec.Prior = append(rec.Prior, receipt.Prior{Name: b.Program, GOOS: b.GOOS, GOARCH: b.GOARCH, SHA256: d})
	}
	res := r.run(r.baseEnv(), r.src, time.Minute, "git", "rev-parse", "--verify", r.cfg.commit+":docs/compatibility.md")
	if !res.ok() {
		return rec, fmt.Errorf("git rev-parse of the upgrade page: %s", res.Out)
	}
	rec.UpgradePage = strings.TrimSpace(res.Out)
	return rec, nil
}

// cleanup ends what the run started, by recorded PIDs only: open sessions (their input closed), then the local server.
func (r *runner) cleanup() {
	r.log.printf("")
	r.log.printf("== CLEANUP (recorded processes only)")
	for n, ss := range r.sessions {
		if ss.alive() {
			exited, st := ss.stop(20 * time.Second)
			r.log.printf("  session %s (pid %d) stopped: exited by itself %v, status %d", n, ss.pid, exited, st)
		}
	}
	if r.natsPID > 0 && processAlive(r.natsPID) {
		r.log.printf("  local server (recorded pid %d): SIGTERM", r.natsPID)
		_ = syscall.Kill(r.natsPID, syscall.SIGTERM)
	}
	if r.mir != nil {
		r.mir.close()
	}
}

func (r *runner) writeEvidence(start time.Time) {
	ev := filepath.Join(r.cfg.out, "evidence")
	_ = writeJSON(filepath.Join(ev, "steps.json"), r.steps)
	_ = writeJSON(filepath.Join(ev, "sessions.json"), r.procs)
	for name, v := range r.evid {
		_ = writeJSON(filepath.Join(ev, name), v)
	}
	if r.mir != nil {
		_ = writeJSON(filepath.Join(ev, "mirror-requests.json"), r.mir.requests())
	}
	_ = writeJSON(filepath.Join(ev, "run.json"), map[string]any{
		"commit": r.cfg.commit, "version": r.cfg.version, "previous": r.cfg.prev, "venue": r.cfg.venue,
		"previous_release_repository": r.cfg.releaseRepo, "previous_sha256sums_sha256": r.cfg.prevSumsSHA,
		"goos": runtime.GOOS, "goarch": runtime.GOARCH, "started": start.Format(time.RFC3339), "ended": time.Now().UTC().Format(time.RFC3339),
	})
}

// finalizeReceipt adds the statement about the environment to a receipt. A receipt that says passed stays so only when
// the environment is gone and the receipt is still well formed; otherwise it is failed.
func finalizeReceipt(rec receipt.Receipt, statement string, removed bool) receipt.Receipt {
	rec.Environment += "; " + statement
	if !removed {
		rec.Environment += "; THE ENVIRONMENT STILL EXISTS: the proof did not run in a machine destroyed after it, so this receipt is failed"
	}
	if rec.Result == receipt.ResultPassed && (!removed || len(rec.Problems()) > 0) {
		rec.Result, rec.ExitStatus = receipt.ResultFailed, 1
	}
	return rec
}

func cmdFinalize(args []string) int {
	fs := flag.NewFlagSet("finalize", flag.ContinueOnError)
	out := fs.String("out", "", "the output directory of the run")
	statement := fs.String("statement", "", "what became of the environment, in words")
	removed := fs.Bool("container-removed", false, "the throwaway environment is gone (required: true or false); false fails the receipt")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "container-removed" })
	if *out == "" || *statement == "" || !given {
		fmt.Fprintln(os.Stderr, "at011 finalize: -out, -statement and -container-removed=true|false are required")
		return 2
	}
	files, _ := filepath.Glob(filepath.Join(*out, "at-011-*.json"))
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "at011 finalize: no receipt in", *out)
		return 1
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "at011 finalize:", err)
			return 1
		}
		rec, err := receipt.Unmarshal(b)
		if err != nil {
			fmt.Fprintln(os.Stderr, "at011 finalize:", err)
			return 1
		}
		rec = finalizeReceipt(rec, *statement, *removed)
		nb, err := receipt.Marshal(rec)
		if err == nil {
			err = os.WriteFile(f, nb, 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "at011 finalize:", err)
			return 1
		}
		fmt.Printf("%s: result %s\n", f, rec.Result)
	}
	logs, _ := filepath.Glob(filepath.Join(*out, "at011-*.log"))
	for _, l := range logs {
		if f, err := os.OpenFile(l, os.O_APPEND|os.O_WRONLY, 0); err == nil {
			w := bufio.NewWriter(f)
			fmt.Fprintf(w, "\n== ENVIRONMENT: %s\n", *statement)
			_ = w.Flush()
			f.Close()
		}
	}
	return 0
}
