package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/release"
)

// The unlinked step (the policy packages are not part of a release): no policy-engine package is linked into the
// released programs. internal/policy, the size constants, is allowed; every other package of the engine is refused
// wherever it appears in the dependencies of cmd/mash or cmd/mashctl. The same step refuses the synadia-agents.go
// caller and host packages (not linked before S5). internal/sapsdk and the SDK identity package are refused too, in
// every candidate below 0.6.0 (REQ-009): they are allowed only from the switch-on release, 0.6.0 or later. The SDK root
// package is always allowed.

// policyPrefix: every package whose path starts with it and is longer than it is refused (internal/policydoc,
// internal/policyfoo, internal/policy/sub). internal/policy itself is allowed.
const policyPrefix = "internal/policy"

// policyNamedElsewhere lists the engine packages whose names do not start with internal/policy. A new engine package
// whose name does not start with internal/policy must be added to this list, or the gate will not see it. Each matches
// the package and its subpackages: internal/listset/sub is refused; internal/listsetx and internal/xlistset are not.
var policyNamedElsewhere = []string{
	"internal/listset",
}

// sdkRefused lists, by whole import path, the synadia-agents.go packages no 0.5.x release links. The SDK root package
// is allowed. Each entry matches the package and its subpackages, by whole path: .../identity/x is refused, while
// .../identityx and .../synadia-agents.go itself are not.
var sdkRefused = []string{
	"github.com/bogworks/synadia-agents.go/caller",
	"github.com/bogworks/synadia-agents.go/host",
}

// switchOnRefused lists the packages refused in every candidate below the switch-on release: mash's own
// internal/sapsdk (module-relative) and the SDK identity package (full import path), each with its subpackages.
var switchOnRefused = []string{
	"internal/sapsdk",
	"github.com/bogworks/synadia-agents.go/identity",
}

var semverRE = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)

// switchedOn reports whether version is 0.6.0 or later; a pre-release or build suffix does not change the answer.
// An unparseable version is not switched on (fail closed).
func switchedOn(version string) bool {
	m := semverRE.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	major, err1 := strconv.Atoi(m[1])
	minor, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 0 || minor >= 6
}

// linkedPolicyPackages returns, sorted and without duplicates, the refused packages named in `go list -deps` output:
// engine packages of module as module-relative paths, SDK packages as full import paths. module is the
// candidate's own module path, without a trailing slash. version is the candidate's N.N.N: below 0.6.0 (or unparseable)
// internal/sapsdk and the SDK identity package are refused as well.
func linkedPolicyPackages(module, version, listOutput string) []string {
	refuseSwitchOn := !switchedOn(version)
	prefix := module + "/"
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(listOutput, "\n") {
		path := strings.TrimSpace(line)
		name := path
		if rel, ok := strings.CutPrefix(path, prefix); ok {
			name = rel
			if !isPolicyPackage(rel) && !(refuseSwitchOn && matchesAny(rel, switchOnRefused)) {
				continue
			}
		} else if !matchesAny(path, sdkRefused) && !(refuseSwitchOn && matchesAny(path, switchOnRefused)) {
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
	if matchesAny(pkg, switchOnRefused) {
		return "internal/sapsdk and the SDK identity package are not linked before 0.6.0"
	}
	if matchesAny(pkg, sdkRefused) {
		return "the SDK caller and host packages are not part of any 0.5.x release"
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
		links = append(links, targetLinks{t.GOOS + "/" + t.GOARCH, linkedPolicyPackages(module, o.Version, out)})
	}
	if s := describeLinked(links); s != "" {
		return fmt.Errorf("refused packages are linked into the released programs (the policy packages are not part of a release): %s; %s", s, describeReasons(links))
	}
	return nil
}
