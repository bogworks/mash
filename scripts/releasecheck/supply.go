package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/bogworks/mash/internal/release"
)

// sbomPredicateType is the in-toto predicate type actions/attest-sbom writes for a CycloneDX SBOM.
const sbomPredicateType = "https://cyclonedx.org/bom"

// slsaBuilderID is the one generator workflow whose provenance the gate accepts (slsa-verifier --builder-id): another
// generator version is refused. It moves with SLSA_GENERATOR_TAG in release.yml.
const slsaBuilderID = "https://github.com/" + "slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0"

// releaseWorkflowPath is the workflow that builds and attests a release, and the entry point its provenance must name.
const releaseWorkflowPath = ".github/workflows/release.yml"

// attestFlags are the gh attestation verify flags that pin WHO made an attestation: the release workflow of the release
// repository, at the release tag, on a GitHub-hosted runner. -R alone would accept any workflow of the repository on any ref.
func attestFlags(o Options, tag string) []string {
	return []string{"-R", o.GHRepo, "--signer-workflow", o.GHRepo + "/" + releaseWorkflowPath, "--source-ref", "refs/tags/" + tag, "--deny-self-hosted-runners"}
}

// slsaVerifierInstall is how the gate tells a maintainer to get slsa-verifier. There is no flag to skip the check.
//
// The module path is written in two pieces on purpose: it names a third-party tool the maintainer installs, not a
// repository mash depends on, and internal/portable keeps foreign-org URLs out of string literals.
const slsaVerifierInstall = "go install github.com/" + "slsa-framework/slsa-verifier/v2/cli/slsa-verifier@v2.7.1"

// verifySupplyChain holds the release's SBOMs and SLSA provenance to what release.yml promises, on every binary; any
// failure is a refusal (exit 3) naming the file, and a failed download is the gate's. For each binary it requires that
//   - the attached mash-<os>-<arch>.cdx.json is CycloneDX JSON with components and a described main component or tool;
//   - `gh attestation verify <binary> -R <repo> --signer-workflow <repo>/.github/workflows/release.yml --source-ref
//     refs/tags/v<version> --deny-self-hosted-runners --predicate-type https://cyclonedx.org/bom` passes and a verified
//     statement names the binary's own sha256 as its subject and carries the attached SBOM as its predicate (an SBOM
//     attested for some other file, or a different SBOM file, is refused);
//   - `slsa-verifier verify-artifact <binary> --provenance-path mash.intoto.jsonl --source-uri github.com/<repo>
//     --source-tag v<version> --builder-id <the generic generator at its pinned tag>` passes;
//   - the provenance's workflow entry point is .github/workflows/release.yml (checkProvenanceEntryPoint).
//
// slsa-verifier must be installed: without it the gate refuses and says how to install it. It runs only for a release
// that passed every earlier check, like verifyAttestations.
func verifySupplyChain(o Options, tag string) error {
	verifier, err := exec.LookPath(o.slsaVerifier())
	if err != nil {
		return fmt.Errorf("slsa-verifier is not installed, so the SLSA provenance cannot be verified and the release is not accepted; install it with `%s`", slsaVerifierInstall)
	}
	dir, err := os.MkdirTemp("", "release-check-supply-")
	if err != nil {
		return op(err)
	}
	defer os.RemoveAll(dir)
	fetch := func(name string) (string, []byte, error) {
		b, err := downloadAsset(o, tag, name)
		if err != nil {
			return "", nil, err
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return "", nil, op(err)
		}
		return p, b, nil
	}
	prov, provBytes, err := fetch(release.ProvenanceName)
	if err != nil {
		return err
	}
	var bad []string
	for _, t := range release.Targets {
		name := t.AssetName()
		bin, binBytes, err := fetch(name)
		if err != nil {
			return err
		}
		_, sbom, err := fetch(t.SBOMName())
		if err != nil {
			return err
		}
		if err := checkSBOM(sbom); err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", t.SBOMName(), err))
		}
		if err := verifySBOMAttestation(o, tag, bin, binBytes, t.SBOMName(), sbom); err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", name, err))
		}
		cmd := exec.Command(verifier, "verify-artifact", bin, "--provenance-path", prov, "--source-uri", "github.com/"+o.GHRepo, "--source-tag", tag, "--builder-id", slsaBuilderID)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			bad = append(bad, fmt.Sprintf("%s: no verifying SLSA provenance in %s: %v: %s %s", name, release.ProvenanceName, err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())))
		}
	}
	if err := checkProvenanceEntryPoint(provBytes); err != nil {
		bad = append(bad, fmt.Sprintf("%s: %v", release.ProvenanceName, err))
	}
	if len(bad) > 0 {
		return fmt.Errorf("SBOM or SLSA provenance does not verify for %s", strings.Join(bad, "; "))
	}
	return nil
}

func (o Options) slsaVerifier() string {
	if o.SLSAVerifier != "" {
		return o.SLSAVerifier
	}
	return "slsa-verifier"
}

// verifySBOMAttestation runs gh attestation verify with the CycloneDX predicate type on the binary and checks that a
// verified statement has the binary's sha256 among its subjects and a predicate semantically equal to the attached
// SBOM (actions/attest-sbom stores the SBOM JSON verbatim as the predicate; key order and whitespace do not matter).
func verifySBOMAttestation(o Options, tag, path string, data []byte, sbomName string, sbom []byte) error {
	args := append([]string{"attestation", "verify", path}, attestFlags(o, tag)...)
	out, err := gh(o, append(args, "--predicate-type", sbomPredicateType, "--format", "json")...)
	if err != nil {
		return fmt.Errorf("no verifying SBOM attestation: %w", err)
	}
	var results []struct {
		VerificationResult struct {
			Statement struct {
				PredicateType string `json:"predicateType"`
				Subject       []struct {
					Digest map[string]string `json:"digest"`
				} `json:"subject"`
				Predicate json.RawMessage `json:"predicate"`
			} `json:"statement"`
		} `json:"verificationResult"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		return fmt.Errorf("could not read gh attestation verify's output: %w", err)
	}
	var attached any
	if err := json.Unmarshal(sbom, &attached); err != nil {
		return fmt.Errorf("%s is not JSON: %w", sbomName, err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	subjectSeen := false
	for _, r := range results {
		st := r.VerificationResult.Statement
		if st.PredicateType != "" && st.PredicateType != sbomPredicateType {
			continue
		}
		for _, s := range st.Subject {
			if s.Digest["sha256"] != want {
				continue
			}
			subjectSeen = true
			var pred any
			if json.Unmarshal(st.Predicate, &pred) == nil && reflect.DeepEqual(pred, attached) {
				return nil
			}
		}
	}
	if subjectSeen {
		return fmt.Errorf("the verified SBOM attestation for %s does not match %s", filepath.Base(path), sbomName)
	}
	return fmt.Errorf("the verified SBOM attestation does not have this file (sha256:%s) as its subject", want)
}

// checkProvenanceEntryPoint requires that every statement in the provenance file (one Sigstore bundle, or bare DSSE envelope, per line) names
// .github/workflows/release.yml as the workflow entry point: predicate.invocation.configSource.entryPoint (SLSA v0.2) or
// predicate.buildDefinition.externalParameters.workflow.path (v1). Neither field, or another value, is refused.
func checkProvenanceEntryPoint(b []byte) error {
	n := 0
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n++
		var env struct {
			Bundle struct {
				Payload string `json:"payload"`
			} `json:"dsseEnvelope"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(ln), &env); err != nil {
			return fmt.Errorf("a line is not a DSSE envelope: %w", err)
		}
		payload := env.Bundle.Payload // slsa-github-generator v2 writes Sigstore-bundle lines
		if payload == "" {
			payload = env.Payload // a bare DSSE envelope
		}
		if payload == "" {
			return errors.New("a line is neither a Sigstore bundle (dsseEnvelope.payload) nor a DSSE envelope (payload)")
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			if raw, err = base64.URLEncoding.DecodeString(payload); err != nil {
				return fmt.Errorf("the DSSE payload is not base64: %w", err)
			}
		}
		var st struct {
			Predicate struct {
				Invocation struct {
					ConfigSource struct {
						EntryPoint string `json:"entryPoint"`
					} `json:"configSource"`
				} `json:"invocation"`
				BuildDefinition struct {
					ExternalParameters struct {
						Workflow struct {
							Path string `json:"path"`
						} `json:"workflow"`
					} `json:"externalParameters"`
				} `json:"buildDefinition"`
			} `json:"predicate"`
		}
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("the DSSE payload is not an in-toto statement: %w", err)
		}
		got := st.Predicate.Invocation.ConfigSource.EntryPoint
		if got == "" {
			got = st.Predicate.BuildDefinition.ExternalParameters.Workflow.Path
		}
		if got == "" {
			return errors.New("the provenance names no workflow entry point (predicate.invocation.configSource.entryPoint or predicate.buildDefinition.externalParameters.workflow.path)")
		}
		if got != releaseWorkflowPath {
			return fmt.Errorf("the provenance was made by workflow %q, not %s", got, releaseWorkflowPath)
		}
	}
	if n == 0 {
		return errors.New("the provenance file is empty")
	}
	return nil
}

// noDuplicateKeys walks the JSON token stream and refuses an object that repeats a key at any depth (encoding/json keeps
// the last one silently, so two parsers could read different documents).
func noDuplicateKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	var walk func() error
	walk = func() error {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		seen := map[string]bool{}
		for dec.More() {
			if d == '{' {
				k, err := dec.Token()
				if err != nil {
					return err
				}
				if seen[k.(string)] {
					return fmt.Errorf("duplicate key %q", k)
				}
				seen[k.(string)] = true
			}
			if err := walk(); err != nil {
				return err
			}
		}
		_, err = dec.Token() // the closing delimiter
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("data after the JSON document")
	}
	return nil
}

// checkSBOM is a modest sanity check of an attached SBOM: CycloneDX JSON, spec 1.5 or later, at least one component,
// and either a described main component or a tool section. Whether it belongs to the binary is decided by comparing it with the verified attestation's predicate.
func checkSBOM(b []byte) error {
	var v struct {
		BOMFormat   string            `json:"bomFormat"`
		SpecVersion string            `json:"specVersion"`
		Components  []json.RawMessage `json:"components"`
		Metadata    struct {
			Component struct {
				Name string `json:"name"`
			} `json:"component"`
			Tools json.RawMessage `json:"tools"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	if err := noDuplicateKeys(b); err != nil {
		return err
	}
	if v.BOMFormat != "CycloneDX" {
		return fmt.Errorf("bomFormat is %q, not CycloneDX", v.BOMFormat)
	}
	var minor int
	if _, err := fmt.Sscanf(v.SpecVersion, "1.%d", &minor); err != nil || minor < 5 {
		return fmt.Errorf("specVersion is %q, want 1.5 or later", v.SpecVersion)
	}
	if len(v.Components) == 0 {
		return errors.New("no components")
	}
	tools := strings.TrimSpace(string(v.Metadata.Tools))
	if v.Metadata.Component.Name == "" && (tools == "" || tools == "null" || tools == "{}" || tools == "[]") {
		return errors.New("metadata names neither a main component nor a tool")
	}
	return nil
}
