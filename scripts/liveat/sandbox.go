package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The sandbox of AT-001 to AT-003 (and selfcheck): tests run only in containers or VMs that the test makes and
// destroys, never on the machine that runs the harness. So `liveat at001` on a developer machine builds the node image,
// starts one throwaway rootless container of it (no network but loopback, no published port, the non-root user mash),
// puts a static `liveat` in it and runs `liveat <the same arguments> --inside` there: inside, the harness does what it
// always did (scratch homes, the setup script, `mashctl init --config`, nats-server on the container's loopback). The
// records and the captured output are copied back with `podman cp` into the host's run directory, scanned there for
// seeds and JWTs, and the container and the image are destroyed by their label (mash-at005=<run id>).

const (
	sandboxLiveat  = "/node/bin/liveat"
	sandboxExport  = "/node/liveat-export" // what the run inside leaves for the host: out/, record-*.md, run.log
	sandboxTimeout = 30 * time.Minute
)

// errInsideOutside is the refusal of --inside on a machine that is not a container.
const errInsideOutside = "liveat: --inside is refused outside a container (there is no " + containerEnvFile + "): it starts a NATS server, and tests never run on the machine that runs the harness; leave the flag out and the run goes into a sandbox container"

// sandboxArgs is the command line of the run inside the container: the same verb and port, plus --inside.
func sandboxArgs(cmd string, port int) []string {
	return []string{sandboxLiveat, cmd, "--port", strconv.Itoa(port), "--inside"}
}

// sandboxEnv is the whole environment of the run inside: scratch paths and what the host knows (the revision, the
// nats-server pin, the image's record lines). No variable of the caller reaches it.
func sandboxEnv(r *Run) []string {
	pin, _ := natsPin(r.Repo)
	return []string{
		"HOME=" + nodeHome,
		"TMPDIR=" + nodeHome + "/tmp",
		"LC_ALL=C",
		envInsideRevision + "=" + r.Revision,
		envInsideNatsPin + "=" + pin,
		envInsideImage + "=" + strings.Join(r.ImageLines, "|"),
	}
}

// sandboxMain is `liveat at001|at002|at003|all|selfcheck --port P [--keep]` on the host. Exit codes are the run
// inside's (0, 1, 2, 4); 4 also when the sandbox could not be made, 1 when the host's scan of what came back finds a
// seed or a JWT.
func sandboxMain(cmd string, port int, keep bool, stdout, stderr io.Writer, p *Podman) int {
	if err := p.Preflight(); err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		return 4
	}
	repo, err := repoRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	r, err := NewRun(repo, port, keep)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	r.Runner.Timeout = sandboxTimeout
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			r.Close()
			os.Exit(130)
		case <-done:
		}
	}()
	defer r.Close()
	if err := r.PrepareNatsOnly(); err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	h, err := p.OpenSandbox(r)
	if err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		return 4
	}
	fmt.Fprintf(stdout, "run %s, revision %s, sandbox container %s\n", r.ID, r.Revision, h.name)
	if code, err := h.installLiveat(r); err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		return code
	}
	res := h.Exec("inside", sandboxArgs(cmd, port), sandboxEnv(r))
	// What the container printed is redacted before it reaches this terminal or a CI log: a seed or a JWT in it is a
	// failure, and it is never shown.
	outText, errText2, printedSecret := shownOutput(res)
	fmt.Fprint(stdout, outText)
	fmt.Fprint(stderr, errText2)
	code := res.Code
	if printedSecret {
		fmt.Fprintln(stdout, "INV-001: the output of the run inside the sandbox held a seed or a JWT; it was removed from what is shown")
		code = 1
	}
	if code < 0 {
		fmt.Fprintln(stderr, "liveat: the run inside the sandbox did not finish")
		code = 4
	}
	// What came back: out/, record-*.md and run.log, never a home, a key or a server directory.
	back := filepath.Join(r.Dir, "sandbox-export")
	if _, err := h.c.do("cp", h.name+":"+sandboxExport, back); err != nil {
		fmt.Fprintln(stderr, "liveat: the records of the run inside could not be copied out:", err)
		if code == 0 {
			code = 4
		}
	} else {
		found, err := scanDir(back)
		if err != nil || len(found) > 0 || len(r.Runner.Leaks()) > 0 {
			fmt.Fprintln(stdout, "INV-001: the output copied out of the sandbox holds a seed or a JWT (or could not be scanned): "+leakNames(found)+" "+errText(err))
			code = 1
		}
		if keep {
			fmt.Fprintf(stdout, "records and output copied out: %s\n", back)
		}
	}
	// The container and image go now, not in a deferred close that only logs: one that cannot be removed holds keys
	// this run made, so it fails the run.
	if derr := r.Destroy(); derr != nil {
		fmt.Fprintln(stderr, "liveat: the sandbox container or image could not be removed:", derr)
		code = 1
	}
	if keep {
		fmt.Fprintf(stdout, "kept: %s (the sandbox container and image are still destroyed)\n", r.Dir)
	}
	return code
}

// installLiveat builds a static liveat from the checkout into the run directory and puts it in the sandbox container.
func (h *podHost) installLiveat(r *Run) (int, error) {
	out := filepath.Join(r.Dir, "liveat-linux")
	_, se, code := h.c.rt.Run(Cmd{Argv: []string{"go", "build", "-o", out, "./scripts/liveat"}, Dir: r.Repo, Env: append(goEnv(), "CGO_ENABLED=0")})
	if code != 0 {
		return 4, fmt.Errorf("CGO_ENABLED=0 go build ./scripts/liveat: exit %d: %s", code, strings.TrimSpace(se))
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return 4, err
	}
	if err := h.Put(sandboxLiveat, b, 0o755); err != nil {
		return 4, err
	}
	return 0, nil
}

// exportRun copies what the host should get from a run inside the sandbox into dst (made here, mode 0700): the
// captured command output (out/), the records and the run log. The scratch homes, the server directories and every key
// stay behind, so no seed or key file ever reaches the host.
func exportRun(r *Run, dst string) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	outDst := filepath.Join(dst, "out")
	if err := os.Mkdir(outDst, 0o700); err != nil {
		return err
	}
	copyFlat := func(from, to string) error {
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o600)
	}
	entries, err := os.ReadDir(r.Out)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := copyFlat(filepath.Join(r.Out, e.Name()), filepath.Join(outDst, e.Name())); err != nil {
				return err
			}
		}
	}
	top, err := os.ReadDir(r.Dir)
	if err != nil {
		return err
	}
	for _, e := range top {
		n := e.Name()
		if e.Type().IsRegular() && (n == "run.log" || strings.HasPrefix(n, "record-") && strings.HasSuffix(n, ".md")) {
			if err := copyFlat(filepath.Join(r.Dir, n), filepath.Join(dst, n)); err != nil {
				return err
			}
		}
	}
	return nil
}

// shownOutput is what the container printed with any seed or JWT removed, and whether it held one.
func shownOutput(res Result) (stdout, stderr string, leaked bool) {
	stdout, stderr = redact(res.Stdout), redact(res.Stderr)
	return stdout, stderr, stdout != res.Stdout || stderr != res.Stderr
}
