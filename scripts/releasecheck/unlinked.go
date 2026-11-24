package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/release"
)

// The unlinked step (standing order 16): no policy-engine package is linked into the released programs. internal/policy,
// the size constants, is allowed; every other package of the engine is refused wherever it appears in the dependencies
// of cmd/mash or cmd/mashctl. The same step refuses the synadia-agents.go identity, caller and host packages and mash's
// internal/sapsdk (with subpackages) in 0.5.x; the SDK root package is allowed.

// policyPrefix: every package whose path starts with it and is longer than it is refused (internal/policydoc,
// internal/policyfoo, internal/policy/sub). internal/policy itself is allowed.
const policyPrefix = "internal/policy"

// policyNamedElsewhere lists the engine packages whose names do not start with internal/policy. A new engine package
// whose name does not start with internal/policy must be added to this list, or the gate will not see it. Each matches
// the package and its subpackages: internal/listset/sub is refused; internal/listsetx and internal/xissue are not.
var policyNamedElsewhere = []string{
	"internal/requesterreg",
	"internal/issue",
	"internal/reconcile",
	"internal/listset",
}

// sdkRefused lists, by whole import path, the synadia-agents.go packages no 0.5.x release links. The SDK root package
// is allowed. Each entry matches the package and its subpackages, by whole path: .../identity/x is refused, while
// .../identityx and .../synadia-agents.go itself are not.
var sdkRefused = []string{
	"github.com/bogworks/synadia-agents.go/identity",
	"github.com/bogworks/synadia-agents.go/caller",
	"github.com/bogworks/synadia-agents.go/host",
}

// moduleRefused lists, relative to the candidate's module, the new SDK adapter package (S4) no 0.5.x release links.
// It matches the package and its subpackages.
var moduleRefused = []string{
	"internal/sapsdk",
}

// linkedPolicyPackages returns, sorted and without duplicates, the refused packages named in `go list -deps` output:
// engine and SDK-adapter packages of module as module-relative paths, SDK packages as full import paths. module is the
// candidate's own module path, without a trailing slash.
func linkedPolicyPackages(module, listOutput string) []string {
	prefix := module + "/"
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(listOutput, "\n") {
		path := strings.TrimSpace(line)
		name := path
		if rel, ok := strings.CutPrefix(path, prefix); ok {
			name = rel
			if !isPolicyPackage(rel) && !matchesAny(rel, moduleRefused) {
				continue
			}
		} else if !matchesAny(path, sdkRefused) {
			continue
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// matchesAny reports whether path is one of list or a subpackage of one, by whole path.
func matchesAny(path string, list []string) bool {
	for _, p := range list {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// refusalReason says why a refused package may not be linked.
func refusalReason(pkg string) string {
	switch {
	case matchesAny(pkg, sdkRefused):
		return "the SDK identity, caller and host packages are not part of any 0.5.x release"
	case matchesAny(pkg, moduleRefused):
		return "the SAP SDK adapter is not part of any 0.5.x release"
	}
	return "the policy engine is not part of any 0.5.x release"
}

func isPolicyPackage(rel string) bool {
	if strings.HasPrefix(rel, policyPrefix) && len(rel) > len(policyPrefix) {
		return true
	}
	return matchesAny(rel, policyNamedElsewhere)
}

// targetLinks is the engine packages one release target links.
type targetLinks struct {
	target string // "goos/goarch"
	pkgs   []string
}

// describeLinked joins the per-target results into one line: each package, sorted, with the targets that link it in the
// order given, e.g. "internal/policyapply (darwin/arm64, linux/arm64)". It returns "" when nothing is linked.
func describeLinked(links []targetLinks) string {
	byPkg := map[string][]string{}
	for _, l := range links {
		for _, p := range l.pkgs {
			if ts := byPkg[p]; len(ts) == 0 || ts[len(ts)-1] != l.target {
				byPkg[p] = append(ts, l.target)
			}
		}
	}
	names := make([]string, 0, len(byPkg))
	for p := range byPkg {
		names = append(names, p)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, p := range names {
		parts[i] = p + " (" + strings.Join(byPkg[p], ", ") + ")"
	}
	return strings.Join(parts, ", ")
}

// describeReasons is the distinct reasons for the packages linked, in a fixed order.
func describeReasons(links []targetLinks) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range links {
		for _, p := range l.pkgs {
			if r := refusalReason(p); !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// modulePath is the candidate's module path, from `go list -m` under dir.
func modulePath(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m")
	cmd.Dir = dir
	cmd.Env = release.PinnedEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", op(fmt.Errorf("go list -m: %w: %s", err, stderrTail(stderr.Bytes())))
	}
	m := strings.TrimSpace(stdout.String())
	if m == "" {
		return "", op(fmt.Errorf("go list -m: empty module path"))
	}
	return m, nil
}

// goListDeps runs `go list -deps` of the two released programs under dir, in the release build environment for
// goos/goarch. The release build passes no -tags, so neither does this.
func goListDeps(ctx context.Context, dir, goos, goarch string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "./cmd/mash", "./cmd/mashctl")
	cmd.Dir = dir
	cmd.Env = release.BuildEnv(os.Environ(), goos, goarch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", op(fmt.Errorf("go list -deps (%s/%s): %w: %s", goos, goarch, err, stderrTail(stderr.Bytes())))
	}
	return stdout.String(), nil
}

// policyUnlinkedStep is the gate's step: go list runs in a fresh clone of x, once per release target, so it judges
// the candidate on every platform it ships for.
func policyUnlinkedStep(ctx context.Context, o Options, x string) error {
	if o.unlinked != nil {
		return o.unlinked(ctx, o, x)
	}
	src, work, err := freshClone(o.Repo, x)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	module, err := modulePath(ctx, src)
	if err != nil {
		return err
	}
	var links []targetLinks
	for _, t := range release.Targets {
		out, err := goListDeps(ctx, src, t.GOOS, t.GOARCH)
		if err != nil {
			return err
		}
		links = append(links, targetLinks{t.GOOS + "/" + t.GOARCH, linkedPolicyPackages(module, out)})
	}
	if s := describeLinked(links); s != "" {
		return fmt.Errorf("refused packages are linked into the released programs (standing order 16): %s; %s", s, describeReasons(links))
	}
	return nil
}
