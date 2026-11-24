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
// of cmd/mash or cmd/mashctl.

const policyModule = "github.com/bogworks/mash/"

// policyPrefix: every package whose path starts with it and is longer than it is refused (internal/policydoc,
// internal/policyfoo, internal/policy/sub). internal/policy itself is allowed.
const policyPrefix = "internal/policy"

// policyNamedElsewhere lists the engine packages whose names do not start with internal/policy. A new engine package
// whose name does not start with internal/policy must be added to this list, or the gate will not see it. These match
// the whole path: internal/listsetx and internal/xissue are not refused.
var policyNamedElsewhere = []string{
	"internal/requesterreg",
	"internal/issue",
	"internal/reconcile",
	"internal/listset",
}

// linkedPolicyPackages returns, sorted and without duplicates, the engine packages named in `go list -deps` output.
func linkedPolicyPackages(listOutput string) []string {
	elsewhere := map[string]bool{}
	for _, p := range policyNamedElsewhere {
		elsewhere[p] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(listOutput, "\n") {
		line = strings.TrimSpace(line)
		rel, ok := strings.CutPrefix(line, policyModule)
		if !ok || seen[rel] {
			continue
		}
		if (strings.HasPrefix(rel, policyPrefix) && len(rel) > len(policyPrefix)) || elsewhere[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// goListDeps runs `go list -deps` of the two released programs under dir, with the gate's pinned Go settings.
func goListDeps(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "./cmd/mash", "./cmd/mashctl")
	cmd.Dir = dir
	cmd.Env = release.PinnedEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", op(fmt.Errorf("go list -deps: %w: %s", err, stderrTail(stderr.Bytes())))
	}
	return stdout.String(), nil
}

// policyUnlinkedStep is the gate's step: go list runs in a fresh clone of x, so it judges the candidate.
func policyUnlinkedStep(ctx context.Context, o Options, x string) error {
	if o.unlinked != nil {
		return o.unlinked(ctx, o, x)
	}
	src, work, err := freshClone(o.Repo, x)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	out, err := goListDeps(ctx, src)
	if err != nil {
		return err
	}
	if linked := linkedPolicyPackages(out); len(linked) > 0 {
		return fmt.Errorf("policy packages are linked into the released programs (standing order 16): %s", strings.Join(linked, ", "))
	}
	return nil
}
