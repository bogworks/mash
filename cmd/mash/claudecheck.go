package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/host"
)

// claudeVersionTimeout bounds `claude --version`.
const claudeVersionTimeout = 3 * time.Second

// claudeVersionWaitDelay is how long after the timeout kills claude that Output still waits for its output pipe: a
// child that outlives claude and holds stdout open would otherwise keep the doctor waiting past the timeout.
const claudeVersionWaitDelay = 1 * time.Second

// The lookup and the run are variables so tests inject fakes: a test never depends on a claude on PATH.
var (
	// claudeLookPath is the path of the claude command, or an error when none is on PATH.
	claudeLookPath = func() (string, error) { return exec.LookPath("claude") }
	// claudeVersionOutput is what `claude --version` prints.
	claudeVersionOutput = func(ctx context.Context, path string) (string, error) {
		cmd := exec.CommandContext(ctx, path, "--version")
		cmd.WaitDelay = claudeVersionWaitDelay
		out, err := cmd.Output()
		return string(out), err
	}
)

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion is the first N.N.N in s.
func parseVersion(s string) (v [3]int, ok bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// claudeCheck is the claude row: the installed Claude Code against host.MinClaudeCode and host.TestedClaudeSeries.
// Doctor reports and never refuses; no fix is given, as none is a command of ours.
func claudeCheck(ctx context.Context) cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckClaude}
	path, err := claudeLookPath()
	if errors.Is(err, exec.ErrNotFound) {
		c.State, c.Detail = cmds.CheckSkip, "no claude command found"
		return c
	}
	if err != nil {
		c.State, c.Detail = cmds.CheckWarn, "the version of claude could not be read"
		return c
	}
	rctx, cancel := context.WithTimeout(ctx, claudeVersionTimeout)
	defer cancel()
	out, err := claudeVersionOutput(rctx, path)
	v, ok := parseVersion(out)
	if err != nil || !ok {
		c.State, c.Detail = cmds.CheckWarn, "the version of claude could not be read"
		return c
	}
	got := fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	min, _ := parseVersion(host.MinClaudeCode)
	series, _ := parseVersion(host.TestedClaudeSeries + ".0")
	switch {
	case v[0] < min[0] || (v[0] == min[0] && (v[1] < min[1] || (v[1] == min[1] && v[2] < min[2]))):
		c.State, c.Detail = cmds.CheckFail, got+" is below the tested "+host.MinClaudeCode
	case v[0] > series[0] || (v[0] == series[0] && v[1] > series[1]):
		c.State, c.Detail = cmds.CheckWarn, got+" is above the tested "+host.TestedClaudeSeries
	default:
		c.State, c.Detail = cmds.CheckOK, got
	}
	return c
}
