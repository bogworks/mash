package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Toolchain is the Go toolchain every release is built with; go.mod's go directive and the workflows pin the same value.
const Toolchain = "go1.27.3"

// keepGo are the Go variables that locate caches and modules; they cannot change what the compiler emits.
var keepGo = map[string]bool{
	"GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "GOPROXY": true, "GOSUMDB": true, "GONOSUMDB": true,
	"GONOPROXY": true, "GOPRIVATE": true, "GOINSECURE": true, "GOTMPDIR": true, "GOVCS": true,
}

// buildEnv is the environment of a release build: the caller's environment without any Go or cgo setting that
// could change the output (toolchain, GOROOT, GOFLAGS, GOEXPERIMENT, GOAMD64, CGO_*, a go env file or a
// workspace), then the pinned settings. Two machines with different ambient Go configuration build the same bytes.
func buildEnv(base []string, goos, goarch string) []string {
	var out []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if (strings.HasPrefix(k, "GO") || strings.HasPrefix(k, "CGO_")) && !keepGo[k] {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GOTOOLCHAIN="+Toolchain, "GOENV=off", "GOWORK=off", "GOFLAGS=", "CGO_ENABLED=0")
	if goos != "" {
		out = append(out, "GOOS="+goos)
	}
	if goarch != "" {
		out = append(out, "GOARCH="+goarch)
	}
	return out
}

// ToolError is a failure of git itself (not a checkout, a corrupt repository, git missing) as opposed to a finding
// about the source: the caller could not decide.
type ToolError struct{ Err error }

func (e *ToolError) Error() string { return e.Err.Error() }
func (e *ToolError) Unwrap() error { return e.Err }

// gitEnv is the environment for the git calls that attest a tree: no variable may point git at another repository,
// index or object store, and no user or system configuration may change what it reports.
func gitEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG") || k == "GIT_DIR" || k == "GIT_WORK_TREE" || k == "GIT_INDEX_FILE" ||
			k == "GIT_COMMON_DIR" || k == "GIT_OBJECT_DIRECTORY" || k == "GIT_ALTERNATE_OBJECT_DIRECTORIES" || k == "GIT_NAMESPACE" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

// gitOut runs git in dir under gitEnv and returns stdout; a failure names the command and carries git's stderr.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", &ToolError{fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))}
	}
	return stdout.String(), nil
}

// CheckSource refuses a source tree that is not exactly the candidate: HEAD must be the full commit X, the
// working tree clean, and the output outside the tree. A release binary stamped X must be built from X.
func CheckSource(srcDir, commit, out string) error {
	if len(commit) != 40 || strings.Trim(commit, "0123456789abcdef") != "" {
		return fmt.Errorf("commit %q is not a full 40-hex SHA", commit)
	}
	head, err := gitOut(srcDir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(head); got != commit {
		return fmt.Errorf("source HEAD is %s, not the candidate %s", got, commit)
	}
	// Every untracked and every ignored file counts: a stray .go file, hidden by a global ignore rule or by
	// status.showUntrackedFiles=no in someone's configuration, would be compiled into a binary stamped with X.
	st, err := gitOut(srcDir, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(st)) > 0 {
		return fmt.Errorf("source tree is not clean:\n%s", st)
	}
	absSrc, err := resolvedAbs(srcDir)
	if err != nil {
		return err
	}
	absOut, err := resolvedAbs(out)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absSrc, absOut)
	if err != nil {
		return err
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("output %s is inside the source tree %s", out, srcDir)
	}
	return nil
}

// BuildFlags are the release build flags. They carry no build tags.
func BuildFlags(version, commit string) []string {
	return []string{
		"-trimpath", "-buildvcs=false", "-mod=readonly",
		"-ldflags", fmt.Sprintf("-X github.com/bogworks/mash/internal/endpoint.Version=%s -X main.commit=%s", version, commit),
	}
}

// Build compiles cmd/mash from srcDir into out for goos/goarch ("" means the host's) with the release flags,
// CGO off and the pinned toolchain, and returns the binary's SHA-256. The same source built in any directory
// gives the same hash.
func Build(ctx context.Context, srcDir, out, goos, goarch, version, commit string) (string, error) {
	abs, err := resolvedAbs(out) // the same resolution CheckSource judged: the filesystem's, not a lexical clean
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	// The module cache is an input too: its files must be the ones go.sum names, or an edited dependency would
	// be compiled in unseen.
	for _, sub := range [][]string{{"mod", "download"}, {"mod", "verify"}} { // fetch what is missing, then check all of it
		c := exec.CommandContext(ctx, "go", sub...)
		c.Dir = srcDir
		c.Env = buildEnv(os.Environ(), goos, goarch)
		if b, err := c.CombinedOutput(); err != nil {
			return "", fmt.Errorf("go %s: %w\n%s", strings.Join(sub, " "), err, b)
		}
	}
	args := append([]string{"build"}, BuildFlags(version, commit)...)
	args = append(args, "-o", abs, "./cmd/mash")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = srcDir
	cmd.Env = buildEnv(os.Environ(), goos, goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, b)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// PinnedEnv is the environment for running the project's tests from a candidate checkout: the caller's
// environment with the same ambient Go configuration removed and the same toolchain pinned as a release build
// (no GOFLAGS, workspace, go env file, GOEXPERIMENT or arch tuning), so the tests that run are the tests in X.
func PinnedEnv(base []string) []string { return buildEnv(base, "", "") }

// resolvedAbs is the absolute path with every symlink resolved, dangling ones included, the way the filesystem
// does it: components are taken one at a time, a link's target is expanded in place (so a ".." after a link applies
// to what the link leads to, not to the link's own directory), and what does not exist yet hangs off the resolved
// prefix. A loop, a permission error or any other failure is an error, never a guess.
func resolvedAbs(p string) (string, error) {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		p = wd + string(filepath.Separator) + p
	}
	pending := components(p)
	cur := string(filepath.Separator)
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, part)
		fi, err := os.Lstat(next)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) { // not there yet, so not a link: the rest is plain names
				for _, rest := range pending {
					if rest == ".." {
						return "", fmt.Errorf("cannot resolve %s: %q is missing and the path goes back up through it", p, next)
					}
				}
				return filepath.Join(append([]string{next}, pending...)...), nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if links++; links > 40 {
			return "", fmt.Errorf("too many levels of symbolic links resolving %s", p)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			cur = string(filepath.Separator)
		}
		pending = append(components(target), pending...)
	}
	return cur, nil
}

func components(p string) []string {
	var out []string
	for _, c := range strings.Split(p, string(filepath.Separator)) {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}
