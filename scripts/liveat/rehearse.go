package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// `liveat rehearse SCRIPT.sh [--port P]`: runs one shell script, as the non-root user mash, in ONE throwaway sandbox
// container (the machinery of sandbox.go: no network, label mash-at005=<run id>, destroyed at the end). It is for the
// steps of a known-issue workaround: the script starts the server with mash-local-nats-setup.sh, uses mash and mashctl
// from PATH, and what it prints is shown here with every seed and JWT removed (redact). Nothing else is copied back.

const rehearsePath = nodeHome + "/rehearse.sh"

// rehearseMain is the verb on the host. Exit: 0 when the sandbox ran the script (the script's own code is on the last
// line shown); 2 usage; 4 no sandbox; 1 when the output held a seed or a JWT.
func rehearseMain(args []string, stdout, stderr io.Writer, p *Podman) int {
	usage := "usage: liveat rehearse SCRIPT.sh [--port P]"
	fs := flag.NewFlagSet("rehearse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 4222, "")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") { // the script comes first, then flags
		fmt.Fprintln(stderr, usage)
		return 2
	}
	script := args[0]
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	body, err := os.ReadFile(script)
	if err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		return 2
	}
	if err := p.Preflight(); err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		return 4
	}
	repo, err := repoRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 4
	}
	r, err := NewRun(repo, *port, false)
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
	fmt.Fprintf(stdout, "run %s, revision %s, sandbox container %s, script %s\n", r.ID, r.Revision, h.name, filepath.Base(script))
	if err := h.Put(rehearsePath, body, 0o755); err != nil {
		fmt.Fprintln(stderr, "liveat:", err)
		_ = r.Destroy()
		return 4
	}
	res := h.Exec("rehearse", []string{"bash", rehearsePath}, []string{"HOME=" + nodeHome, "TMPDIR=" + nodeHome + "/tmp", "LC_ALL=C"})
	outText, errText, leaked := shownOutput(res)
	fmt.Fprint(stdout, "--- stdout\n"+outText)
	fmt.Fprint(stdout, "--- stderr\n"+errText)
	fmt.Fprintf(stdout, "--- exit code %d\n", res.Code)
	code := 0
	if leaked {
		fmt.Fprintln(stdout, "INV-001: the output held a seed or a JWT; it was removed from what is shown")
		code = 1
	}
	if res.Code < 0 {
		fmt.Fprintln(stderr, "liveat: the script did not finish")
		code = 4
	}
	if derr := r.Destroy(); derr != nil {
		fmt.Fprintln(stderr, "liveat: the sandbox container or image could not be removed:", derr)
		code = 1
	}
	return code
}
