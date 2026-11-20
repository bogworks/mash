// Command liveat runs the live acceptance tests AT-001 to AT-003 and AT-4 (the security boundaries, at004) of the 0.5.5 endpoint-model contract against a build
// of this checkout.
//
//	go run ./scripts/liveat at001|at002|at003|at004|all|selfcheck --port P [--keep] [--strict] [--inside]
//
// On the machine you run it on it starts nothing: it builds a node image (podman.go, Containerfile.at005), starts ONE
// throwaway rootless container of it, copies itself in and runs `liveat <the same arguments> --inside` there as a
// non-root user, copies the records and captured output back, scans them, and destroys the container and the image
// (sandbox.go). --inside, which is what actually starts a server, is refused outside a container. The rest of this
// comment describes the run inside the container.
//
// It builds mash and mashctl into a private run directory (mode 0700), starts one local NATS server per scenario with
// scripts/mash-local-nats-setup.sh and `mashctl init --config` (the script picks port 4222 or the next free one; P is only checked free before it starts: there is no default), runs the scenario
// with a scratch home and an environment built from scratch values, keeps the output of every command under
// <run>/out, fails the run if any of it holds a seed or a JWT (INV-001), prints the run record of contract section 8
// (and keeps it as <run>/record-<at>.md), stops every process it started by its recorded PID, and removes the run
// directory unless --keep is given. Exit 0: every assertion held (with pending checks, the result line says PASS WITH PENDING, not a clean pass); 1: one did not; 2: usage; 3: --strict and a check is pending; 4: the run could not be set up.
//
// The CLI forms of contract 0.5.5 (mashctl endpoint ..., mash key create, mash join) are coded as the
// contract writes them; a build without them fails the scenario with that fact.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() { os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr)) }

func realMain(args []string, stdout, stderr io.Writer) (code int) {
	usage := "usage: liveat at001|at002|at003|at004|all|selfcheck --port P [--keep] [--strict: a pending check fails the run, exit 3] [--inside: only inside a container; the default runs in a sandbox container] | liveat at005 [--dry-setup] [--hosts podman --run-by manual|ci --host-label LABEL [--subnet-base A.B] | --hosts local] [--keep] [--record-out FILE] | liveat at005-cleanup [--run RUNID]"
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if args[0] == "at005-cleanup" {
		return at005Cleanup(args[1:], stdout, stderr, nil, nil)
	}
	if args[0] == "at005" { // the reserved ports are fixed: no --port
		return at005Main(args[1:], stdout, stderr)
	}
	cmd := args[0]
	fs := flag.NewFlagSet("liveat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 0, "")
	keep := fs.Bool("keep", false, "")
	inside := fs.Bool("inside", false, "")
	strict := fs.Bool("strict", false, "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	var scs []Scenario
	switch cmd {
	case "at001":
		scs = []Scenario{AT001}
	case "at002":
		scs = []Scenario{AT002}
	case "at003":
		scs = []Scenario{AT003}
	case "at004":
		scs = []Scenario{AT004}
	case "all":
		scs = []Scenario{AT001, AT002, AT003, AT004}
	case "selfcheck":
		scs = []Scenario{SelfCheck}
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if *port == 0 {
		fmt.Fprintln(stderr, "liveat: --port is required (the port the operator reserved for this run; there is no default)")
		return 2
	}
	if *inside && !insideContainer() {
		fmt.Fprintln(stderr, errInsideOutside)
		return 2
	}
	if !*inside { // the default: nothing starts on this machine, the run goes into a sandbox container
		return sandboxMain(cmd, *port, *keep, *strict, stdout, stderr, &Podman{})
	}
	r, err := NewRun("", *port, *keep)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	// An interrupt stops what the run started, by recorded PID, before it exits.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; r.Close(); os.Exit(130) }()
	defer r.Close()
	defer func() { // after the records are written, before Close: what the host copies out
		if err := exportRun(r, sandboxExport); err != nil {
			fmt.Fprintln(stderr, "liveat: export:", err)
			code = exportFailCode(code)
		}
	}()
	if err := r.PrepareInside(os.Getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	fmt.Fprintf(stdout, "run %s, revision %s\n", r.ID, r.Revision)
	failed := 0
	var pending []string
	for _, sc := range scs {
		fmt.Fprintf(stdout, "\n== %s\n", sc.ID)
		rec, err := runScenario(r, sc)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 4
		}
		fmt.Fprint(stdout, rec.table)
		fmt.Fprintf(stdout, "\n%s\n", rec.markdown)
		if !rec.passed {
			failed++
		}
		pending = append(pending, rec.pending...)
	}
	if *keep {
		fmt.Fprintf(stdout, "kept: %s (records, output and scratch homes)\n", r.Dir)
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "%d scenario(s) FAILED\n", failed)
		return 1
	}
	return strictExit(*strict, pending, stdout)
}

// strictExit is the exit code of a run with no failed assertion: 0, or 3 under --strict when any check is pending (a
// pending check is not a pass: the final release gate runs `liveat all --strict`).
func strictExit(strict bool, pending []string, stdout io.Writer) int {
	if strict && len(pending) > 0 {
		fmt.Fprintf(stdout, "pending checks fail under --strict: %s\n", strings.Join(pending, "; "))
		return 3
	}
	return 0
}

type scenarioResult struct {
	table, markdown string
	passed          bool
	pending         []string // the names of the pending checks
}

// runScenario runs one scenario with a fresh server, writes its record and returns what to print. A panic in a
// scenario is a failed assertion, not a crash that leaves a server running.
func runScenario(r *Run, sc Scenario) (res scenarioResult, err error) {
	a, err := NewAT(r, sc.ID)
	if err != nil {
		return res, err
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				a.C.Check("the scenario ran to its end", false, fmt.Sprintf("panic: %v", p))
			}
		}()
		sc.Body(a)
	}()
	rec := a.Finish(sc)
	if err := WriteRecord(filepath.Join(r.Dir, "record-"+sc.ID+".md"), rec); err != nil {
		return res, err
	}
	return scenarioResult{table: a.C.Table(), markdown: rec.Markdown(), passed: rec.Passed(), pending: pendingNames(a.C)}, nil
}

// repoRoot is the top of the checkout the harness is run in.
func repoRoot() (string, error) {
	c := exec.Command("git", "rev-parse", "--show-toplevel")
	c.Env = goEnv()
	b, err := c.Output()
	if err != nil {
		return "", errors.New("liveat: run it from inside the mash checkout")
	}
	return strings.TrimSpace(string(b)), nil
}

// exportFailCode is the exit code of a run inside whose export failed: the host cannot get the records, so the run
// cannot pass; a code that was already non-zero stays.
func exportFailCode(code int) int {
	if code == 0 {
		return 1
	}
	return code
}
