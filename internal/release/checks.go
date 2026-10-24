package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// CheckRun is one GitHub check run on the candidate commit.
type CheckRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// RequiredChecks are the check runs ci.yml produces that a candidate must have passed.
var RequiredChecks = []string{"test (ubuntu-latest)", "test (macos-latest)", "reproducible"}

func joined(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

// CheckCI requires every required check run to exist and have succeeded, all for commit x. A run for another
// commit, or two different results under one run id, means the response is not what was asked for and is
// refused. When a check ran more than once the latest run (highest id) decides.
func CheckCI(runs []CheckRun, x string) error {
	var problems []string
	byID := map[int64]CheckRun{}
	latest := map[string]CheckRun{}
	for _, r := range runs {
		if r.HeadSHA != x {
			problems = append(problems, fmt.Sprintf("CI check run %q (id %d) is for commit %q, not %s", r.Name, r.ID, r.HeadSHA, x))
			continue
		}
		if prev, ok := byID[r.ID]; ok && prev != r {
			problems = append(problems, fmt.Sprintf("CI check run id %d is reported twice with different results", r.ID))
			continue
		}
		byID[r.ID] = r
		if cur, ok := latest[r.Name]; !ok || r.ID > cur.ID {
			latest[r.Name] = r
		}
	}
	for _, name := range RequiredChecks {
		r, ok := latest[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("CI check %q: no run on this commit", name))
		case r.Status != "completed" || r.Conclusion != "success":
			problems = append(problems, fmt.Sprintf("CI check %q: status %q, conclusion %q", name, r.Status, r.Conclusion))
		}
	}
	return joined(problems)
}

// Targets are the four binaries a release carries, in the order SHA256SUMS lists them (sha256sum's glob order).
var Targets = []Target{
	{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"},
}

// Target is one build target.
type Target struct{ GOOS, GOARCH string }

// AssetName is the release asset's name for the target.
func (t Target) AssetName() string { return "mash-" + t.GOOS + "-" + t.GOARCH }

// SumsName is the checksums asset.
const SumsName = "SHA256SUMS"

// ExpectedSums is the exact SHA256SUMS content for the given per-target hashes (hex SHA-256 of each binary): the
// output of `sha256sum mash-*` in the dist directory.
func ExpectedSums(hashes map[Target]string) string {
	var b strings.Builder
	for _, t := range Targets {
		fmt.Fprintf(&b, "%s  %s\n", hashes[t], t.AssetName())
	}
	return b.String()
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CheckAssets judges a draft release's assets (name -> GitHub's "sha256:<hex>" digest) against our own fresh builds
// of the candidate: exactly the four binaries and SHA256SUMS, nothing else; each binary's digest is the hash of
// our build for that target; and SHA256SUMS is exactly the file those four hashes produce. It names every problem.
func CheckAssets(have map[string]string, hashes map[Target]string) error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	want := map[string]string{SumsName: "sha256:" + sha([]byte(ExpectedSums(hashes)))}
	for _, t := range Targets {
		h, ok := hashes[t]
		if !ok || len(h) != 64 {
			add("no usable build hash for %s/%s", t.GOOS, t.GOARCH)
			continue
		}
		want[t.AssetName()] = "sha256:" + h
	}
	for name, digest := range want {
		got, ok := have[name]
		switch {
		case !ok:
			add("asset %s is missing from the release", name)
		case got != digest:
			add("asset %s has digest %q, our build of the candidate gives %q", name, got, digest)
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			add("asset %s is not one the release should carry", name)
		}
	}
	return joined(problems)
}
