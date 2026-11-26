package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A logger writes the human-readable record of the run: each command with its exit status, each check with its verdict.
type logger struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *logger) printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format+"\n", a...)
}

// check is one named check of a step. Kind says, for a failed check, whether the release under test behaved wrongly
// ("candidate") or the proof could not carry the check out ("runner").
type check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Kind   string `json:"failure_kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// stepRecord is one step of AT-011 with the checks that made its verdict.
type stepRecord struct {
	N      string  `json:"step"`
	Title  string  `json:"title"`
	Passed bool    `json:"passed"`
	Checks []check `json:"checks"`
}

// stepCtx collects the checks of the step being run.
type stepCtx struct {
	r   *runner
	rec *stepRecord
}

// ok records a check that judges the release under test; a false verdict fails the step. It returns the verdict.
func (s *stepCtx) ok(pass bool, name, detailFormat string, a ...any) bool {
	return s.record(pass, "candidate", name, detailFormat, a...)
}

// need records a check of the proof's own working (a download, a start): when it fails the run could not carry out the
// step, which is a runner limitation and not a verdict on the release.
func (s *stepCtx) need(pass bool, name, detailFormat string, a ...any) bool {
	return s.record(pass, "runner", name, detailFormat, a...)
}

func (s *stepCtx) record(pass bool, kind, name, detailFormat string, a ...any) bool {
	d := fmt.Sprintf(detailFormat, a...)
	c := check{Name: name, Passed: pass, Detail: d}
	verdict := "PASS"
	if !pass {
		verdict = "FAIL"
		c.Kind = kind
	}
	s.rec.Checks = append(s.rec.Checks, c)
	if d != "" {
		s.r.log.printf("  %s: %s (%s)", verdict, name, d)
	} else {
		s.r.log.printf("  %s: %s", verdict, name)
	}
	return pass
}

// note records a line in the log that is not a check.
func (s *stepCtx) note(format string, a ...any) { s.r.log.printf("  note: "+format, a...) }

// step runs fn as step n and records its verdict: passed only when every check passed.
func (r *runner) step(n, title string, fn func(s *stepCtx)) bool {
	rec := &stepRecord{N: n, Title: title}
	s := &stepCtx{r: r, rec: rec}
	r.log.printf("")
	r.log.printf("== STEP %s: %s", n, title)
	fn(s)
	rec.Passed = len(rec.Checks) > 0
	for _, c := range rec.Checks {
		if !c.Passed {
			rec.Passed = false
		}
	}
	status, word := 1, "failed"
	if rec.Passed {
		status, word = 0, "passed"
	}
	r.log.printf("== STEP %s exit status: %d (%s)", n, status, word)
	r.steps = append(r.steps, rec)
	return rec.Passed
}

// cmdResult is what a command printed and how it ended.
type cmdResult struct {
	Out  string
	Code int
	Err  error // the command could not be started or timed out
}

func (c cmdResult) ok() bool { return c.Err == nil && c.Code == 0 }

// run starts name with args in dir under env, waits for it (at most timeout), logs "$ command", its output and "[exit=N]",
// and returns the result. Only the command line and its output are logged, never the environment.
func (r *runner) run(env []string, dir string, timeout time.Duration, name string, args ...string) cmdResult {
	return r.runIn(env, dir, "", timeout, name, args...)
}

func (r *runner) runIn(env []string, dir, stdin string, timeout time.Duration, name string, args ...string) cmdResult {
	r.log.printf("$ %s", shellLine(name, args))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	res := cmdResult{Out: buf.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Code = ee.ExitCode()
	} else if err != nil {
		res.Err = err
		res.Code = -1
	}
	if ctx.Err() != nil {
		res.Err = fmt.Errorf("timed out after %s", timeout)
	}
	if t := strings.TrimRight(res.Out, "\n"); t != "" {
		r.log.printf("%s", indent(t))
	}
	if res.Err != nil {
		r.log.printf("[error: %v]", res.Err)
	}
	r.log.printf("[exit=%d]", res.Code)
	return res
}

func shellLine(name string, args []string) string {
	parts := []string{name}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'$") {
			a = fmt.Sprintf("%q", a)
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func indent(s string) string {
	ls := strings.Split(s, "\n")
	for i, l := range ls {
		ls[i] = "    " + l
	}
	return strings.Join(ls, "\n")
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
