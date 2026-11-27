package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/release"
)

// The gate step of REQ-019 (receipts.go holds the check itself). 0.6.0 is the SDK move, so the switch-on release is
// version 0.6.0 (release.SwitchOnVersion). From it the gate requires the AT-011 and AT-012 receipts and refuses a
// candidate without them. Below it the step runs only when --receipts is given, and then holds the AT-011 receipts
// alone (the 0.5.x candidate that carries S1 to S3); with no --receipts the gate is exactly what it was.

// receiptsStepName is the step the gate prints.
const receiptsStepName = "the upgrade proofs are bound to the candidate (REQ-019)"

// receiptsNoneGiven is the one problem of a switch-on gate run with no --receipts.
const receiptsNoneGiven = "receipts: none given: the proofs of AT-011 and AT-012 are required from " + release.SwitchOnVersion

// upgradePagePath is the upgrade page whose git blob id a receipt binds. No document names one, so this is the page
// that carries the "Upgrading from ..." section (docs/compatibility.md); a candidate without the file has no page to
// follow, and that is a problem, not a pass.
const upgradePagePath = "docs/compatibility.md"

// receiptsLine is the release line whose rules apply to this run, and whether the step runs at all.
func receiptsLine(o Options) (line proofLine, run bool) {
	if release.RequiresProofs(o.Version) {
		return lineSwitchOn, true
	}
	if o.Receipts != "" {
		return lineAT011, true
	}
	return 0, false
}

// receiptsStep runs the receipt check and records its outcome in r. A failure to compute what the receipts must say
// is a gate error (exit 4); every mismatch, and a missing or unreadable receipt set, is a refusal (exit 3) and every
// problem is reported.
func receiptsStep(ctx context.Context, o Options, x string, r *report) {
	line, run := receiptsLine(o)
	if !run {
		return
	}
	if o.Receipts == "" { // the switch-on release, no receipts given: refused before anything is built
		r.fail(receiptsStepName, []string{receiptsNoneGiven})
		return
	}
	rs, err := loadReceipts(o.Receipts)
	if err != nil { // fails closed: receipts that cannot be read prove nothing
		r.fail(receiptsStepName, []string{err.Error()})
		return
	}
	expect := o.expect
	if expect == nil {
		expect = realExpected
	}
	exp, err := expect(ctx, o, x)
	if err != nil {
		r.step(receiptsStepName, op(err))
		return
	}
	if ps := checkReceipts(exp, rs, line); len(ps) > 0 {
		r.fail(receiptsStepName, ps)
		return
	}
	r.step(receiptsStepName, nil)
}

// fail records a refused step with every one of its problems, each as its own line.
func (r *report) fail(name string, problems []string) {
	fmt.Fprintf(r.out, "FAIL  %s\n", name)
	r.problems = append(r.problems, problems...)
	r.refused += len(problems)
}

// realExpected computes what a receipt must say from the gate's own builds of x: the eight executables (mash and
// mashctl on each of the four platforms), install.sh and the SHA256SUMS the gate would make from those builds, the
// previous published release and each of its binaries, the upgrade page's blob id at x, and the mail corpus.
func realExpected(ctx context.Context, o Options, x string) (Expected, error) {
	exp := Expected{Commit: x, NewerVersion: o.Version, CorpusSHA: compatCorpusDigest()}
	for _, t := range release.Targets {
		exp.Platforms = append(exp.Platforms, Platform{t.GOOS, t.GOARCH})
	}

	hashes := map[release.Binary]string{}
	var berrs []error
	for _, b := range release.Binaries() {
		h, err := buildFresh(ctx, o, x, b.Program, b.GOOS, b.GOARCH, "")
		if err != nil {
			berrs = append(berrs, fmt.Errorf("%s %s/%s: %w", b.Program, b.GOOS, b.GOARCH, err))
			continue
		}
		hashes[b] = h
		exp.Executables = append(exp.Executables, Exec{Name: b.Program, GOOS: b.GOOS, GOARCH: b.GOARCH, SHA256: h,
			Build: BuildConfig{Toolchain: release.Toolchain, Tags: []string{}, Flags: release.BuildFlags(o.Version, x)}})
	}
	if err := errors.Join(berrs...); err != nil {
		return Expected{}, fmt.Errorf("the gate's own builds of the candidate: %w", err)
	}

	// install.sh and SHA256SUMS. The sums are what the gate generates: our binaries and the repository's scripts at x
	// (the SBOMs and provenance exist only on the draft, so they are not listed).
	kl, err := keyless(o, x, "v"+o.Version)
	if err != nil {
		return Expected{}, err
	}
	ci := map[string]string{}
	for _, n := range release.ScriptNamesFor(o.Version) {
		b, err := gitBytes(o.Repo, "show", x+":scripts/"+n)
		if err != nil {
			return Expected{}, fmt.Errorf("scripts/%s at the candidate: %w", n, err)
		}
		ci[n] = sha256hex(b)
	}
	exp.InstallSHA = ci["install.sh"]
	exp.SumsSHA = sha256hex([]byte(release.ExpectedSums(o.Version, hashes, ci, kl)))

	// The previous published release and the digest of each of its binaries.
	// The previous release is the highest earlier non-draft published one, so a draft or a recorded skipped release is
	// passed over (the rest of the gate allows recorded skips).
	releases, err := listReleases(o)
	if err != nil {
		return Expected{}, err
	}
	if pv := previousRelease("v"+o.Version, releases); pv != "" {
		prev := "v" + pv
		exp.PrevVersion = strings.TrimPrefix(prev, "v")
		exp.OlderVersion = exp.PrevVersion
		have, err := releaseDigests(o, prev)
		if err != nil {
			return Expected{}, err
		}
		for _, b := range release.Binaries() {
			if d, ok := strings.CutPrefix(have[b.AssetName()], "sha256:"); ok {
				exp.Prior = append(exp.Prior, Prior{Name: b.Program, GOOS: b.GOOS, GOARCH: b.GOARCH, SHA256: d})
			} // a binary with no digest stays out: Expected.validate refuses it
		}
	}

	// The upgrade page's git blob id at x; no such file is left empty, which validate refuses.
	if _, has, err := fileAt(o.Repo, x, upgradePagePath); err != nil {
		return Expected{}, err
	} else if has {
		id, err := git(o.Repo, "rev-parse", "--verify", x+":"+upgradePagePath)
		if err != nil {
			return Expected{}, op(err)
		}
		exp.UpgradePage = strings.TrimSpace(id)
	}
	return exp, nil
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
