package testenv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// GitTimeout bounds every git subprocess a test runs through RunGit: a hang fails that one test with the output so
// far, instead of waiting for the whole package's test timeout.
const GitTimeout = 60 * time.Second

// RunGit runs git with args and returns its combined output. It gives up after GitTimeout.
func RunGit(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = 5 * time.Second // a killed git's children may hold the pipe open
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		err = fmt.Errorf("git %v timed out after %s: %w", args, GitTimeout, err)
	}
	return out, err
}

// HermeticGit makes the git a test package runs independent of the machine it runs on: no global or system config
// (credential helpers, url.insteadOf, hooksPath, signing), no prompts, no askpass, no ssh command, and a throwaway
// HOME so ~/.gitconfig, ~/.ssh and keychains are never read. Call it from TestMain before m.Run and run the
// returned cleanup after. Go's own caches are pinned first, since they default to paths under HOME.
func HermeticGit() (cleanup func(), err error) {
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if os.Getenv(k) != "" {
			continue
		}
		if out, e := exec.Command("go", "env", k).Output(); e == nil && len(out) > 1 {
			_ = os.Setenv(k, string(out[:len(out)-1]))
		}
	}
	home, err := os.MkdirTemp("", "hermetic-home-")
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]string{
		"HOME":                home,
		"XDG_CONFIG_HOME":     home + "/.config",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_ASKPASS":         "true",
		"SSH_ASKPASS":         "true",
		"GCM_INTERACTIVE":     "never",
	} {
		if err := os.Setenv(k, v); err != nil {
			return nil, err
		}
	}
	_ = os.Unsetenv("GIT_SSH_COMMAND")
	_ = os.Unsetenv("GIT_SSH")
	return func() { _ = os.RemoveAll(home) }, nil
}
