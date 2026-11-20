package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// putSpec is one `--put LOCALPATH:CONTAINERPATH[:mode]`: an extra file copied into the container before the script
// runs (for example an older release binary, at mode 0755). The mode is octal and defaults to 0644.
type putSpec struct {
	Local, Dest string
	Mode        os.FileMode
}

func parsePut(v string) (putSpec, error) {
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return putSpec{}, fmt.Errorf("--put %q: want LOCALPATH:CONTAINERPATH[:mode]", v)
	}
	ps := putSpec{Local: parts[0], Dest: parts[1], Mode: 0o644}
	if !filepath.IsAbs(ps.Local) || !filepath.IsAbs(ps.Dest) {
		return putSpec{}, fmt.Errorf("--put %q: both paths must be absolute", v)
	}
	if len(parts) == 3 {
		m, err := strconv.ParseUint(parts[2], 8, 32)
		if err != nil || m > 0o777 {
			return putSpec{}, fmt.Errorf("--put %q: the mode %q is not an octal permission", v, parts[2])
		}
		ps.Mode = os.FileMode(m)
	}
	return ps, nil
}

// putFlag collects the repeatable --put option.
type putFlag []putSpec

func (f *putFlag) String() string { return "" }
func (f *putFlag) Set(v string) error {
	ps, err := parsePut(v)
	if err != nil {
		return err
	}
	*f = append(*f, ps)
	return nil
}

// `liveat rehearse SCRIPT.sh [--port P]`: runs one shell script, as the non-root user mash, in ONE throwaway sandbox
// container (the machinery of sandbox.go: no network, label mash-at005=<run id>, destroyed at the end). It is for the
// steps of a known-issue workaround: the script starts the server with mash-local-nats-setup.sh, uses mash and mashctl
// from PATH, and what it prints is shown here with every seed and JWT removed (redact). Nothing else is copied back.

const rehearsePath = nodeHome + "/rehearse.sh"

// rehearseMain is the verb on the host. Exit: 0 when the sandbox ran the script (the script's own code is on the last
// line shown); 2 usage; 4 no sandbox; 1 when the output held a seed or a JWT.
func rehearseMain(args []string, stdout, stderr io.Writer, p *Podman) int {
	usage := "usage: liveat rehearse SCRIPT.sh [--port P] [--put LOCALPATH:CONTAINERPATH[:mode]]..."
	fs := flag.NewFlagSet("rehearse", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 4222, "")
	var puts putFlag
	fs.Var(&puts, "put", "")
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
	extras := make([][]byte, len(puts))
	for i, ps := range puts {
		if extras[i], err = os.ReadFile(ps.Local); err != nil {
			fmt.Fprintln(stderr, "liveat:", err)
			return 2
		}
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
	for i, ps := range puts {
		if err := h.Put(ps.Dest, extras[i], ps.Mode); err != nil {
			fmt.Fprintln(stderr, "liveat:", err)
			_ = r.Destroy()
			return 4
		}
		fmt.Fprintf(stdout, "put %s -> %s (mode %04o)\n", filepath.Base(ps.Local), ps.Dest, ps.Mode)
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
