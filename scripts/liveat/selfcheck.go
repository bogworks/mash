package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SelfCheck proves the harness itself with what exists today: the build, a scratch home, the server and its PID, the
// Runner, the environment builder, the leak scanner, the process control and the record writer. It uses no endpoint
// command of 0.5.5.
var SelfCheck = Scenario{
	ID: "selfcheck",
	Scenario: "The harness proves its own parts: build; scratch home; mashctl init --local --start and its recorded PID; mashctl list and audit; " +
		"a state snapshot that is stable when nothing changes; a child environment with no inherited variable; a deliberate leak that the scanner flags; " +
		"a process started and stopped by its PID; the record; the server stopped by its PID.",
	Body: func(a *AT) {
		h, err := a.Home("home")
		if !a.C.Check("a scratch home is made with mode 0700", err == nil, "") {
			return
		}
		st, err := os.Stat(h.Dir)
		a.C.Check("the scratch home is private", err == nil && st.Mode().Perm() == 0o700, modeDetail(st, err))
		rst, err := os.Stat(a.R.Dir)
		a.C.Check("the run directory is private", err == nil && rst.Mode().Perm() == 0o700, modeDetail(rst, err))
		a.C.Check("mash and mashctl were built into the run directory", fileExists(a.R.Mash) && fileExists(a.R.Mashctl), "")
		a.C.Check("the revision is recorded", strings.TrimSpace(a.R.Revision) != "", "")

		// The environment: nothing from the caller reaches a child.
		os.Setenv("MASH_URL", "nats://caller.invalid:1")
		os.Setenv("NATS_URL", "nats://caller.invalid:2")
		tools := map[string]string{}
		for _, n := range []string{"env", "echo", "cat"} {
			p, err := exec.LookPath(n)
			a.C.Check("the self-check tool "+n+" is on PATH", err == nil, "")
			if err != nil {
				return
			}
			tools[n] = p
		}
		env := a.R.Runner.Run("env", []string{tools["env"]}, a.Env(h), "")
		os.Unsetenv("MASH_URL")
		os.Unsetenv("NATS_URL")
		a.C.Check("a child sees HOME and MASH_CONFIG_DIR from the scratch home", strings.Contains(env.Stdout, "HOME="+h.Dir+"\n") && strings.Contains(env.Stdout, "MASH_CONFIG_DIR="+h.Config+"\n"), exitDetail(env))
		a.C.Check("a child sees no MASH_URL or NATS_URL of the caller", !strings.Contains(env.Stdout, "MASH_URL") && !strings.Contains(env.Stdout, "NATS_"), "")

		// The server.
		if !a.StartServer(h) {
			return
		}
		a.C.Check("the server's PID is recorded and alive", a.srvPID > 0 && alive(a.srvPID), "")
		r := a.Ctl("list", h, a.Conn(), "list")
		a.C.Check("mashctl list runs against the server", r.Code == 0, exitDetail(r))
		r = a.Ctl("audit", h, a.Conn(), "audit", "--json")
		a.C.Check("mashctl audit --json runs against the server", r.Code == 0 && strings.HasPrefix(strings.TrimSpace(r.Stdout), "{"), exitDetail(r))
		s1 := a.Snapshot("one", h, h)
		s2 := a.Snapshot("two", h, h)
		a.C.Check("two snapshots of an untouched server are byte-identical", len(s1.Diff(s2)) == 0 && len(s1.Bad) == 0, "differ: "+orNone(strings.Join(s1.Diff(s2), ", "))+"; failed: "+orNone(strings.Join(s1.Bad, ", ")))

		// The leak scanner, on a runner of its own so that its deliberate leak is not this run's.
		ldir := filepath.Join(a.Dir, "leaktest")
		if err := os.Mkdir(ldir, 0o700); err != nil {
			a.C.Check("the leak test directory is made", false, err.Error())
			return
		}
		lr := &Runner{Out: ldir, Cwd: ldir}
		fakeSeed := "SU" + strings.Repeat("A", 54) // not a key: the right shape only
		fakeJWT := "eyJ" + strings.Repeat("a", 12) + ".payload12345.sig"
		lr.Run("fake-seed", []string{tools["echo"], fakeSeed}, a.Env(h), "")
		lr.Run("fake-jwt", []string{tools["echo"], fakeJWT}, a.Env(h), "")
		lr.Run("clean", []string{tools["echo"], "nothing secret here"}, a.Env(h), "")
		leaks, err := scanDir(ldir)
		kinds := map[string]string{}
		for _, l := range leaks {
			kinds[l.Kind] = l.File
		}
		a.C.Check("the scanner flags a seed-shaped token in a captured file, naming the file", err == nil && strings.HasSuffix(kinds["seed"], "fake-seed.out"), "")
		a.C.Check("the scanner flags a JWT-shaped token in a captured file, naming the file", strings.HasSuffix(kinds["jwt"], "fake-jwt.out"), "")
		a.C.Check("the scanner leaves a clean file alone", len(leaks) == 2, "")
		a.C.Check("the runner reports both leaks as it runs", len(lr.Leaks()) == 2, "")
		for _, l := range leaks {
			if strings.Contains(l.File, fakeSeed) {
				a.C.Check("a leak names the file, never the match", false, "")
			}
		}

		// Process control: start a process, record its PID, stop it by that PID.
		p, err := a.R.StartProc("cat", []string{tools["cat"]}, a.Env(h))
		okStart := err == nil && p.PID > 0 && alive(p.PID)
		a.C.Check("a session process is started with a recorded PID", okStart, "")
		if okStart {
			pid := p.PID
			p.Stop()
			time.Sleep(100 * time.Millisecond)
			a.C.Check("it is stopped by that PID", !alive(pid), "")
		}

		// The server is stopped by its PID before the record is written.
		pid := a.srvPID
		a.StopServer()
		a.C.Check("the server is stopped by its recorded PID", pid > 0 && !alive(pid), "")
		a.C.Check("the port is free again", waitPortFree(a.R.Port, 3*time.Second) == nil, "")
	},
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
