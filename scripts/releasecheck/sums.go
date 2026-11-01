package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/release"
)

// Signing of SHA256SUMS (docs/decisions/0042). The --assets gate, which `publish` always runs, ends with this step and
// it has no off switch: a release is published only with a SHA256SUMS.sig that verifies against ci/release.allowed_signers
// as it stands at X. Nothing is signed until every line of SHA256SUMS has been compared with a binary this gate built
// itself from X (the same reproducible recipe release.yml uses), so a changed binary with a matching changed checksum
// is never signed.

// gateRecord is what one --assets gate run in this process established: the exact SHA256SUMS bytes it compared with
// its own fresh builds of X (and found signed by a release signer), and those builds' hashes. Publishing is bound to it,
// so a draft whose assets were replaced after the gate, even by another validly signed set, is never published.
type gateRecord struct {
	sums       []byte
	hashes     map[release.Target]string
	scriptHash string // sha256 of scripts/quickstart-synadia.sh at X
}

func (g *gateRecord) set(sums []byte, hashes map[release.Target]string, scriptHash string) {
	if g == nil {
		return
	}
	g.sums = append([]byte(nil), sums...)
	g.hashes = hashes
	g.scriptHash = scriptHash
}

func (g *gateRecord) ok() bool {
	return g != nil && g.sums != nil && g.hashes != nil && len(g.scriptHash) == 64
}

// signersPath is where X carries the keys allowed to sign a release's SHA256SUMS.
const signersPath = "ci/release.allowed_signers"

// signerPrincipal is the principal of the release key in signersPath.
const signerPrincipal = "release"

var sumsLine = regexp.MustCompile(`^([0-9a-f]{64}) [ *]([^ ]+)$`)

// checkSumEntries holds every line of SHA256SUMS to the hash of our own fresh build of X for that target (hashes, made
// by buildFresh from the tagged commit) and the quickstart-synadia.sh line to scriptHash (the script at X). A malformed line, a duplicate name, a name that is not mash-<os>-<arch> of a
// release target, a digest that differs from our build, a listed name that is not attached (or is attached with
// another digest) and an attached mash-* asset that is not listed are each refused; so is a target the sums omit.
func checkSumEntries(sums []byte, have map[string]string, hashes map[release.Target]string, scriptHash string) error {
	byName := map[string]release.Target{}
	for _, t := range release.Targets {
		byName[t.AssetName()] = t
	}
	listed := map[string]string{}
	for i, line := range strings.Split(string(sums), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := sumsLine.FindStringSubmatch(line)
		if m == nil {
			return fmt.Errorf("SHA256SUMS line %d is malformed (want \"<sha256>  <name>\"): %q", i+1, line)
		}
		hash, name := m[1], m[2]
		if _, dup := listed[name]; dup {
			return fmt.Errorf("SHA256SUMS lists %s more than once", name)
		}
		listed[name] = hash
		if name == release.ScriptName {
			if hash != scriptHash {
				return fmt.Errorf("SHA256SUMS lists %s as sha256:%s, the script at X is sha256:%s", name, hash, scriptHash)
			}
			continue
		}
		t, ok := byName[name]
		if !ok {
			return fmt.Errorf("SHA256SUMS lists %s, which is neither mash-<os>-<arch> of a release target nor %s", name, release.ScriptName)
		}
		if want := hashes[t]; hash != want {
			return fmt.Errorf("SHA256SUMS lists %s as sha256:%s, a fresh reproducible build of X gives sha256:%s", name, hash, want)
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := listed[n]; !ok {
			return fmt.Errorf("SHA256SUMS does not list %s", n)
		}
	}
	if _, ok := listed[release.ScriptName]; !ok {
		return fmt.Errorf("SHA256SUMS does not list %s", release.ScriptName)
	}
	names = append(names, release.ScriptName)
	for _, n := range names {
		digest, ok := have[n]
		switch {
		case !ok:
			return fmt.Errorf("SHA256SUMS lists %s, which is not attached to the release", n)
		case digest != "sha256:"+listed[n]:
			return fmt.Errorf("SHA256SUMS lists %s as sha256:%s, the attached asset's digest is %q", n, listed[n], digest)
		}
	}
	var extra []string
	for n := range have {
		if strings.HasPrefix(n, "mash-") {
			if _, ok := listed[n]; !ok {
				extra = append(extra, n)
			}
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("attached %s is not listed in SHA256SUMS", strings.Join(extra, ", "))
	}
	return nil
}

// signSums makes the detached, armored SSH signature of data: ssh-keygen -Y sign -n mash-release, with the key path
// resolveSigning gives (RELEASE_SIGNING_KEY: a .pub signs through ssh-agent, as the tag does; a private key file also
// works). Any ssh-keygen failure is the gate's (could not decide), never a refusal of the candidate.
func signSums(key string, data []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "release-check-sign-")
	if err != nil {
		return nil, op(err)
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, release.SumsName)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return nil, op(err)
	}
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-n", release.SumsNamespace, "-f", key, p).CombinedOutput(); err != nil {
		return nil, op(fmt.Errorf("ssh-keygen -Y sign with %s: %w: %s", key, err, strings.TrimSpace(string(out))))
	}
	sig, err := os.ReadFile(p + ".sig")
	return sig, op(err)
}

// verifySums has ssh-keygen -Y verify check sig over data against the allowed-signers content (written to a temporary
// file), for principal "release" and namespace mash-release. A failure to run ssh-keygen at all is the gate's.
func verifySums(signers, data, sig []byte) error {
	dir, err := os.MkdirTemp("", "release-check-verify-")
	if err != nil {
		return op(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "signers"), signers, 0o600); err != nil {
		return op(err)
	}
	if err := os.WriteFile(filepath.Join(dir, release.SumsSigName), sig, 0o600); err != nil {
		return op(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", filepath.Join(dir, "signers"), "-I", signerPrincipal,
		"-n", release.SumsNamespace, "-s", filepath.Join(dir, release.SumsSigName))
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return op(fmt.Errorf("ssh-keygen -Y verify: %w", err))
	}
	return fmt.Errorf("SHA256SUMS.sig does not verify against %s as it is at X (namespace %s): %w: %s", signersPath, release.SumsNamespace, err, strings.TrimSpace(string(out)))
}

// downloadAsset reads one release asset's bytes with gh. gh fails for a name no asset has.
func downloadAsset(o Options, tag, name string) ([]byte, error) {
	b, err := gh(o, "release", "download", tag, "--repo", o.GHRepo, "--pattern", name, "--output", "-")
	return b, op(err)
}

// uploadSig attaches sig to the draft as SHA256SUMS.sig, replacing a stale one.
func uploadSig(o Options, tag string, sig []byte) error {
	dir, err := os.MkdirTemp("", "release-check-upload-")
	if err != nil {
		return op(err)
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, release.SumsSigName)
	if err := os.WriteFile(p, sig, 0o644); err != nil {
		return op(err)
	}
	_, err = gh(o, "release", "upload", tag, p, "--clobber", "--repo", o.GHRepo)
	return op(err)
}

// verifyReleaseSig reads SHA256SUMS and SHA256SUMS.sig back from the release and verifies them against the signers file
// at x. It is the last word before publishing and after an upload.
func verifyReleaseSig(o Options, tag, x string) error {
	signers, err := signersAt(o, x)
	if err != nil {
		return err
	}
	sums, err := downloadAsset(o, tag, release.SumsName)
	if err != nil {
		return err
	}
	sig, err := downloadAsset(o, tag, release.SumsSigName)
	if err != nil {
		var oe opError
		if errors.As(err, &oe) && strings.Contains(err.Error(), "no assets match") {
			return fmt.Errorf("%s is not attached to the release", release.SumsSigName)
		}
		return err
	}
	return verifySums(signers, sums, sig)
}

// verifyPublishable is the last check before a draft is published. rec is what the --assets gate of this very process
// established from fresh builds of X (never anything from an earlier run). It downloads SHA256SUMS and SHA256SUMS.sig
// from the release and requires: SHA256SUMS is byte-for-byte the one the gate checked; its signature verifies against
// the signers file at x; the release's assets as GitHub reports them right now (name -> "sha256:<hex>") include
// SHA256SUMS and SHA256SUMS.sig with digests equal to the bytes just downloaded; every mash-<os>-<arch> asset has the
// digest of the gate's fresh build of that target, and quickstart-synadia.sh the gate's hash of the script at X; and nothing is attached that the signed sums do not list. So a
// binary swapped or added after the gate, or the whole asset set replaced by another validly signed release's, is
// refused. GitHub has no compare-and-swap on assets, so a swap in the instant between this check and the publish call
// remains; the release is immutable once published, and that closes it.
func verifyPublishable(o Options, tag, x string, rec *gateRecord) error {
	if !rec.ok() {
		return gateError{errors.New("no --assets gate result from this run to bind the publish to")}
	}
	signers, err := signersAt(o, x)
	if err != nil {
		return err
	}
	sums, err := downloadAsset(o, tag, release.SumsName)
	if err != nil {
		return err
	}
	sig, err := downloadAsset(o, tag, release.SumsSigName)
	if err != nil {
		if strings.Contains(err.Error(), "no assets match") {
			return fmt.Errorf("%s is not attached to the release", release.SumsSigName)
		}
		return err
	}
	if !bytes.Equal(sums, rec.sums) {
		return fmt.Errorf("SHA256SUMS on the release is not the one the --assets gate checked against fresh builds of X; it changed after the gate")
	}
	if err := verifySums(signers, sums, sig); err != nil {
		return err
	}
	listed := map[string]string{}
	for i, line := range strings.Split(string(sums), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := sumsLine.FindStringSubmatch(line)
		if m == nil {
			return fmt.Errorf("SHA256SUMS line %d is malformed (want \"<sha256>  <name>\"): %q", i+1, line)
		}
		if _, dup := listed[m[2]]; dup {
			return fmt.Errorf("SHA256SUMS lists %s more than once", m[2])
		}
		listed[m[2]] = m[1]
	}
	have, err := releaseDigests(o, tag)
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{release.SumsName: sums, release.SumsSigName: sig} {
		sum := sha256.Sum256(data)
		want := "sha256:" + hex.EncodeToString(sum[:])
		got, ok := have[name]
		switch {
		case !ok:
			return fmt.Errorf("%s is no longer attached to the release", name)
		case got != want:
			return fmt.Errorf("%s changed after it was verified: the release reports %q, the bytes verified are %s", name, got, want)
		}
	}
	for _, t := range release.Targets {
		n := t.AssetName()
		if want := "sha256:" + rec.hashes[t]; have[n] != want {
			if _, ok := have[n]; !ok {
				return fmt.Errorf("%s is no longer attached to the release", n)
			}
			return fmt.Errorf("asset %s is not our fresh build of X: the release has %q, the --assets gate built %s", n, have[n], want)
		}
	}
	if want := "sha256:" + rec.scriptHash; have[release.ScriptName] != want {
		if _, ok := have[release.ScriptName]; !ok {
			return fmt.Errorf("%s is no longer attached to the release", release.ScriptName)
		}
		return fmt.Errorf("asset %s is not the script at X: the release has %q, the --assets gate hashed %s", release.ScriptName, have[release.ScriptName], want)
	}
	if listed[release.ScriptName] != rec.scriptHash {
		return fmt.Errorf("SHA256SUMS lists %s as sha256:%s, the script at X is sha256:%s", release.ScriptName, listed[release.ScriptName], rec.scriptHash)
	}
	names := make([]string, 0, len(listed))
	for n := range listed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		digest, ok := have[n]
		switch {
		case !ok:
			return fmt.Errorf("SHA256SUMS lists %s, which is no longer attached to the release", n)
		case digest != "sha256:"+listed[n]:
			return fmt.Errorf("asset %s changed after it was signed: SHA256SUMS lists sha256:%s, the release has %q", n, listed[n], digest)
		}
	}
	var extra []string
	for n := range have {
		if _, ok := listed[n]; !ok && n != release.SumsName && n != release.SumsSigName {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("attached %s is not listed in the signed SHA256SUMS", strings.Join(extra, ", "))
	}
	return nil
}

// releaseDigests is the release's assets as GitHub reports them: name -> "sha256:<hex>".
func releaseDigests(o Options, tag string) (map[string]string, error) {
	b, err := gh(o, "release", "view", tag, "--repo", o.GHRepo, "--json", "assets")
	if err != nil {
		return nil, op(err)
	}
	var resp struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, gateError{fmt.Errorf("could not read the release's assets: %w", err)}
	}
	have := map[string]string{}
	for _, a := range resp.Assets {
		have[a.Name] = a.Digest
	}
	return have, nil
}

// signersAt is ci/release.allowed_signers as X carries it, never a file from the home directory or the working tree.
func signersAt(o Options, x string) ([]byte, error) {
	b, err := gitBytes(o.Repo, "show", x+":"+signersPath)
	if err != nil {
		return nil, fmt.Errorf("the release signers file %s is not at %s: %w", signersPath, x, err)
	}
	return b, nil
}

func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// signedSums makes sure the release carries a SHA256SUMS.sig that verifies, and returns an error if it cannot. Every
// line of SHA256SUMS is checked against our fresh builds first, whatever happens next. Then:
//   - a SHA256SUMS.sig that is attached must verify (it is reused, so a rerun needs no key); one that does not is refused;
//   - none attached on a published release is refused: a published release is never amended;
//   - none attached on a draft is made with the release key (RELEASE_SIGNING_KEY), verified against the signers file at X, uploaded and
//     read back.
func signedSums(o Options, tag, x string, draft bool, have map[string]string, hashes map[release.Target]string, scriptHash string, rec *gateRecord) error {
	// The sums are verified against the committed file at X, which consumers trust; RELEASE_SIGNERS is the maintainer's
	// own file for the tag and is deliberately not used here.
	signers, err := signersAt(o, x)
	if err != nil {
		return err
	}
	if _, ok := have[release.SumsName]; !ok {
		return errors.New("SHA256SUMS is not attached to the release (release.yml builds it)")
	}
	sums, err := downloadAsset(o, tag, release.SumsName)
	if err != nil {
		return err
	}
	if err := checkSumEntries(sums, have, hashes, scriptHash); err != nil {
		return err
	}
	if _, ok := have[release.SumsSigName]; ok {
		sig, err := downloadAsset(o, tag, release.SumsSigName)
		if err != nil {
			return err
		}
		if err := verifySums(signers, sums, sig); err != nil { // verifies: reuse it; does not: refuse, never replace
			return err
		}
		rec.set(sums, hashes, scriptHash)
		return nil
	}
	if !draft {
		return errors.New("SHA256SUMS.sig is not attached to the published release")
	}
	home, _ := os.UserHomeDir()
	key, _, err := resolveSigning(os.Getenv, home, fileExists, "") // lazily: a rerun that reuses a signature needs no key
	if err != nil {
		return op(err) // no key is the gate's failure (4), not a refusal of the candidate
	}
	sig, err := signSums(key, sums)
	if err != nil {
		return err
	}
	if err := verifySums(signers, sums, sig); err != nil {
		return fmt.Errorf("the key %s is not one %s lists at X: %w", key, signersPath, err)
	}
	if err := uploadSig(o, tag, sig); err != nil {
		return err
	}
	if err := verifyReleaseSig(o, tag, x); err != nil {
		return err
	}
	rec.set(sums, hashes, scriptHash)
	return nil
}

// verifyAttestations downloads each of the four binaries from the release into a temporary directory and runs
// `gh attestation verify <file> -R <owner/name>` on it (the repository is the release repository, o.GHRepo). A binary
// with no attestation, or one that does not verify, is refused naming it (exit 3); a download that fails is the
// gate's (could not decide). It keeps a draft that release.yml's attest job never saw (an older one, or one made by
// hand) from being published without provenance. Attestations need a public repository.
func verifyAttestations(o Options, tag string) error {
	dir, err := os.MkdirTemp("", "release-check-attest-")
	if err != nil {
		return op(err)
	}
	defer os.RemoveAll(dir)
	var bad []string
	for _, t := range release.Targets {
		name := "mash-" + t.GOOS + "-" + t.GOARCH
		b, err := downloadAsset(o, tag, name)
		if err != nil {
			return err
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return op(err)
		}
		if _, err := gh(o, "attestation", "verify", p, "-R", o.GHRepo); err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("no verifying build provenance attestation for %s", strings.Join(bad, "; "))
	}
	return nil
}
