package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// CheckRun is one GitHub check run on the candidate commit.
type CheckRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	App        struct {
		Slug string `json:"slug"`
	} `json:"app"`
}

// CIApp is the GitHub App whose check runs count: Actions. Another integration can write check runs under any
// name, so a name alone proves nothing: another app's run is ignored unless it carries a required check's name,
// which is refused as a spoof.
const CIApp = "github-actions"

// RequiredChecks are the check runs ci.yml produces that a candidate must have passed. test (macos) is advisory (ADR 0079):
// it is produced and visible but not required here. TODO(macos-gate): require it again, with pr-check's needs, when we
// confirm the macOS runner no longer pauses running jobs.
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

// Targets are the four platforms a release builds for, in the order SHA256SUMS lists them (sha256sum's glob order).
// Each platform carries two binaries, mash and mashctl (see Binaries).
var Targets = []Target{
	{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"},
}

// Target is one build target.
type Target struct{ GOOS, GOARCH string }

// AssetName is the release asset's name for the target.
func (t Target) AssetName() string { return "mash-" + t.GOOS + "-" + t.GOARCH }

// CtlAssetName is the release asset's name of mashctl for the target.
func (t Target) CtlAssetName() string { return "mashctl-" + t.GOOS + "-" + t.GOARCH }

// SBOMName is the CycloneDX SBOM asset of the target's mash binary.
func (t Target) SBOMName() string { return t.AssetName() + ".cdx.json" }

// Binary is one release binary: a program (one of Programs) built for a target.
type Binary struct {
	Program string
	Target
}

// AssetName is the release asset's name of the binary: <program>-<os>-<arch>.
func (b Binary) AssetName() string { return b.Program + "-" + b.GOOS + "-" + b.GOARCH }

// SBOMName is the CycloneDX SBOM asset of the binary.
func (b Binary) SBOMName() string { return b.AssetName() + ".cdx.json" }

// Binaries are the eight binaries a release carries: mash for every target, then mashctl for every target, in the
// order SHA256SUMS lists them.
func Binaries() []Binary {
	var out []Binary
	for _, p := range Programs {
		for _, t := range Targets {
			out = append(out, Binary{p, t})
		}
	}
	return out
}

// ProvenanceName is the provenance asset (the Sigstore bundle) covering the eight binaries, the two scripts and SHA256SUMS.
const ProvenanceName = "mash.intoto.jsonl"

// MigrationScriptName is the one-time 0.5.5 configuration migration script that releases up to and including
// MigrationScriptLastVersion carried as a third script (ADR 0152). From 0.5.10 on it is not a release asset (ADR 0181),
// and scripts/ no longer holds it; the published releases that carry it are still re-verified, so they are still held to it.
// Every version up to and including 0.5.9 is held to the third script, also those before 0.5.5, which shipped no scripts
// at all: the check does not tell them apart.
const MigrationScriptName = "mash-migrate-0.5.5.sh"

// MigrationScriptLastVersion is the last release that carries MigrationScriptName.
const MigrationScriptLastVersion = "0.5.9"

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// versionKey parses N.N.N strictly (no leading zeros, unlike tagKey).
func versionKey(v string) ([3]int, bool) {
	var k [3]int
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return k, false
	}
	for i := range k {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return k, false
		}
		k[i] = n
	}
	return k, true
}

// carriesMigrationScript reports whether the release of version (N.N.N, no leading v) carries MigrationScriptName: it
// parses and is at or below MigrationScriptLastVersion. An empty or unparsable version does not.
func carriesMigrationScript(version string) bool {
	a, ok := versionKey(version)
	last, _ := versionKey(MigrationScriptLastVersion)
	return ok && !less(last, a)
}

// ScriptNamesFor are the scripts the release of version (N.N.N) carries beside the binaries (ADR 0150), named exactly as
// the repository's scripts/ files are, in the order SHA256SUMS lists them (after the binaries, before the SBOMs). They
// are the repository's own files at the release commit, copied unchanged, and are subjects of the provenance attestation
// like the binaries. They have no SBOM. From 0.5.10 (and for an empty or unparsable version) they are install.sh and
// mash-local-nats-setup.sh; releases up to and including 0.5.9 carry MigrationScriptName as a third.
func ScriptNamesFor(version string) []string {
	n := []string{"install.sh", "mash-local-nats-setup.sh"}
	if carriesMigrationScript(version) {
		n = append(n, MigrationScriptName)
	}
	return n
}

// CIAssetNames are the assets release.yml makes that the gate cannot rebuild: the two scripts, each binary's SBOM in
// Binaries order, then the SLSA provenance. They are attached by name and listed in SHA256SUMS; the gate verifies their
// contents (the scripts are compared with the repository's, `release-check`; the rest by their attestations).
func CIAssetNames(version string) []string {
	n := ScriptNamesFor(version)
	for _, b := range Binaries() {
		n = append(n, b.SBOMName())
	}
	return append(n, ProvenanceName)
}

// SumsCINames are the CIAssetNames that SHA256SUMS lists: all of them, or, for a keyless release, all but the
// provenance bundle.
func SumsCINames(version string, keyless bool) []string {
	n := CIAssetNames(version)
	if keyless {
		n = n[:len(n)-1] // the provenance is last
	}
	return n
}

// SumsName is the checksums asset.
const SumsName = "SHA256SUMS"

// SumsSigName is the detached SSH signature of SHA256SUMS (ssh-keygen -Y sign, namespace SumsNamespace). The draft
// release.yml makes does not carry it: `release-check --assets` adds it. CheckAssets allows it, and the signing step
// judges it. A keyless release (at or above the cutover) carries none: CheckAssets refuses it there.
const SumsSigName = "SHA256SUMS.sig"

// SumsNamespace is the ssh-keygen -Y namespace of SHA256SUMS.sig. It is specific to mash, so a
// signature this key makes for any other purpose cannot be replayed as a mash release signature.
const SumsNamespace = "mash-release"

// ExpectedSums is the exact SHA256SUMS content for the given per-binary hashes (hex SHA-256 of each binary) and the
// hex SHA-256 of each CIAssetNames file (ci, by name): the binaries, the two scripts, the
// SBOMs and the provenance, in that order, as release.yml's draft job lists them. A name missing from ci is left out.
// A keyless release (at or above the cutover) lists the same names except the provenance bundle: it cannot list itself,
// as release.yml makes the sums before the job that attests.
func ExpectedSums(version string, hashes map[Binary]string, ci map[string]string, keyless bool) string {
	var b strings.Builder
	for _, bin := range Binaries() {
		fmt.Fprintf(&b, "%s  %s\n", hashes[bin], bin.AssetName())
	}
	for _, n := range SumsCINames(version, keyless) {
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
// of the candidate: exactly the eight binaries (mash and mashctl for each target), the two scripts, their eight SBOMs, the SLSA provenance and
// SHA256SUMS, nothing else; each binary's digest is the hash of our build for that target; the SBOMs and the provenance must be attached (their contents are verified separately, as
// they cannot be rebuilt); and SHA256SUMS is exactly the file those hashes and the attached digests produce. It names
// every problem. For a keyless release (at or above the cutover) SHA256SUMS omits the provenance bundle (which stays
// required to be attached), and a SHA256SUMS.sig is not allowed. An asset whose digest GitHub did not give (empty or
// absent) is reported as "digest not available" by name, still a refusal, and the empty value is never compared.
func CheckAssets(version string, have map[string]string, hashes map[Binary]string, keyless bool) error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }
	want := map[string]string{}
	ci := map[string]string{}
	for _, n := range CIAssetNames(version) {
		got, ok := have[n]
		hex, found := strings.CutPrefix(got, "sha256:")
		switch {
		case !ok:
			add("asset %s is missing from the release", n)
		case got == "": // GitHub gave none: say so, never compare it
			add("asset %s: digest not available", n)
		case !found || len(hex) != 64:
			add("asset %s has digest %q, not a sha256", n, got)
		default:
			ci[n] = hex
			want[n] = got
		}
	}
	want[SumsName] = "sha256:" + sha([]byte(ExpectedSums(version, hashes, ci, keyless)))
	for _, t := range Binaries() {
		h, ok := hashes[t]
		if !ok || len(h) != 64 {
			add("no usable build hash for %s (%s/%s)", t.Program, t.GOOS, t.GOARCH)
			continue
		}
		want[t.AssetName()] = "sha256:" + h
	}
	for name, digest := range want {
		got, ok := have[name]
		switch {
		case !ok:
			add("asset %s is missing from the release", name)
		case got == "": // GitHub gave none: say so, never compare it
			add("asset %s: digest not available", name)
		case got != digest:
			add("asset %s has digest %q, our build of the candidate gives %q", name, got, digest)
		}
	}
	for name := range have {
		if name == SumsSigName {
			if keyless {
				add("asset %s is attached: a keyless release carries no %s", name, SumsSigName)
			}
			continue // optional here: the signing step requires it and verifies it
		}
		if _, ok := want[name]; !ok {
			add("asset %s is not one the release should carry", name)
		}
	}
	return joined(problems)
}

// CheckScripts holds the release scripts to the repository: each of ScriptNamesFor(version) must be attached with exactly the
// digest of the bytes the repository holds at the release commit (content, by name; read with `git show <commit>:scripts/<name>`).
// A script that differs, or that is missing from either side, is named.
func CheckScripts(version string, have map[string]string, content map[string][]byte) error {
	var problems []string
	for _, n := range ScriptNamesFor(version) {
		b, ok := content[n]
		if !ok {
			problems = append(problems, fmt.Sprintf("no copy of scripts/%s at the release commit to compare %s with", n, n))
			continue
		}
		got, ok := have[n]
		switch want := "sha256:" + sha(b); {
		case !ok:
			problems = append(problems, fmt.Sprintf("script %s is missing from the release", n))
		case got == "": // GitHub gave none: say so, never compare it
			problems = append(problems, fmt.Sprintf("script %s: digest not available", n))
		case got != want:
			problems = append(problems, fmt.Sprintf("script %s has digest %q, scripts/%s at the release commit gives %q", n, got, n, want))
		}
	}
	return joined(problems)
}
