package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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
	App        struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

// CIApp is the GitHub App whose check runs count: Actions. Another integration can write check runs under any
// name, so a name alone proves nothing: another app's run is ignored unless it carries a required check's name,
// which is refused as a spoof.
const CIApp = "github-actions"

// RequiredChecks are the check runs ci.yml produces that a candidate must have passed. test (macos) is advisory (ADR 0079):
// it is produced and visible but not required here. TODO(macos-gate): require it again, with pr-check's needs, when x-lead
// confirms the macOS runner no longer pauses running jobs.
var RequiredChecks = []string{"test (linux)", "reproducible"}

func joined(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

// CheckCI requires every required check run to exist and have succeeded, all for commit x. A run for another
// commit, or two different results under one run id, means the response is not what was asked for and is
// refused. When a check ran more than once the latest run (highest id) decides. Only CIApp's runs count: a run
// from another app is ignored unless it carries a required check's name, which is refused as a spoof.
func CheckCI(runs []CheckRun, x string) error {
	var problems []string
	byID := map[int64]CheckRun{}
	latest := map[string]CheckRun{}
	for _, r := range runs {
		if r.HeadSHA != x {
			problems = append(problems, fmt.Sprintf("CI check run %q (id %d) is for commit %q, not %s", r.Name, r.ID, r.HeadSHA, x))
			continue
		}
		if r.App.Slug != CIApp {
			if !slices.Contains(RequiredChecks, r.Name) {
				continue
			}
			problems = append(problems, fmt.Sprintf("CI check run %q (id %d) comes from app %q, not %s", r.Name, r.ID, r.App.Slug, CIApp))
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

// SBOMName is the CycloneDX SBOM asset of the target's binary.
func (t Target) SBOMName() string { return t.AssetName() + ".cdx.json" }

// ProvenanceName is the SLSA Build L3 provenance asset covering the four binaries.
const ProvenanceName = "mash.intoto.jsonl"

// CIAssetNames are the assets release.yml makes that the gate cannot rebuild: each target's SBOM, in target order,
// then the SLSA provenance. They are attached by name and listed in SHA256SUMS; the gate verifies their contents.
func CIAssetNames() []string {
	var n []string
	for _, t := range Targets {
		n = append(n, t.SBOMName())
	}
	return append(n, ProvenanceName)
}

// SumsName is the checksums asset.
const SumsName = "SHA256SUMS"

// ScriptName is the quickstart script's release asset name; the asset is the tagged scripts/quickstart-synadia.sh.
const ScriptName = "quickstart-synadia.sh"

// SumsSigName is the detached SSH signature of SHA256SUMS (ssh-keygen -Y sign, namespace SumsNamespace). The draft
// release.yml makes does not carry it: `release-check --assets` adds it. CheckAssets allows it, and the signing step
// judges it.
const SumsSigName = "SHA256SUMS.sig"

// SumsNamespace is the ssh-keygen -Y namespace of SHA256SUMS.sig. It is specific to mash, so a
// signature this key makes for any other purpose cannot be replayed as a mash release signature.
const SumsNamespace = "mash-release"

// ExpectedSums is the exact SHA256SUMS content for the given per-target hashes (hex SHA-256 of each binary), the
// quickstart script's hash and the hex SHA-256 of each CIAssetNames file (ci, by name): the binaries, the script, the
// SBOMs and the provenance, in that order, as release.yml's draft job lists them. A name missing from ci is left out.
func ExpectedSums(hashes map[Target]string, scriptHash string, ci map[string]string) string {
	var b strings.Builder
	for _, t := range Targets {
		fmt.Fprintf(&b, "%s  %s\n", hashes[t], t.AssetName())
	}
	fmt.Fprintf(&b, "%s  %s\n", scriptHash, ScriptName)
	for _, n := range CIAssetNames() {
		if h, ok := ci[n]; ok {
			fmt.Fprintf(&b, "%s  %s\n", h, n)
		}
	}
	return b.String()
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CheckAssets judges a draft release's assets (name -> GitHub's "sha256:<hex>" digest) against our own fresh builds
// of the candidate: exactly the four binaries, their four SBOMs, the SLSA provenance, the quickstart script and
// SHA256SUMS, nothing else; each binary's digest is the hash of our build for that target, the script's is scriptHash
// (the tagged source's); the SBOMs and the provenance must be attached (their contents are verified separately, as
// they cannot be rebuilt); and SHA256SUMS is exactly the file those hashes and the attached digests produce. It names
// every problem.
func CheckAssets(have map[string]string, hashes map[Target]string, scriptHash string) error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	want := map[string]string{}
	ci := map[string]string{}
	for _, n := range CIAssetNames() {
		got, ok := have[n]
		hex, found := strings.CutPrefix(got, "sha256:")
		switch {
		case !ok:
			add("asset %s is missing from the release", n)
		case !found || len(hex) != 64:
			add("asset %s has digest %q, not a sha256", n, got)
		default:
			ci[n] = hex
			want[n] = got
		}
	}
	want[SumsName] = "sha256:" + sha([]byte(ExpectedSums(hashes, scriptHash, ci)))
	if len(scriptHash) != 64 {
		add("no usable hash for %s", ScriptName)
	} else {
		want[ScriptName] = "sha256:" + scriptHash
	}
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
		if name == SumsSigName {
			continue // optional here: the signing step requires it and verifies it
		}
		if _, ok := want[name]; !ok {
			add("asset %s is not one the release should carry", name)
		}
	}
	return joined(problems)
}
