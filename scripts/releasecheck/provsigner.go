package main

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// The Fulcio certificate extensions that say which workflow, at which commit, signed the provenance. The OIDs are from
// the Sigstore OID registry (https://github.com/sigstore/fulcio/blob/main/docs/oid-info.md): 1.3.6.1.4.1.57264.1.9 is
// "Build Signer URI" and 1.3.6.1.4.1.57264.1.10 is "Build Signer Digest". (Issue #108 wrote .1.9 for the digest; the
// registry is authoritative.) Extensions 1.8 and later are DER-encoded UTF8Strings; the older raw-string form is also
// accepted, as certificateExtensionString does.
var (
	oidBuildSignerURI    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 9}
	oidBuildSignerDigest = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 10}
)

// generatorWorkflowURI is the workflow the Build Signer URI names, up to the "@refs/tags/<tag>".
const generatorWorkflowURI = "https://github.com/" + "slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/"

var gitSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// generatorPin reads SLSA_GENERATOR_TAG and SLSA_GENERATOR_SHA from the top-level env of a release.yml. There is no
// second place to edit: the gate reads release.yml at the release commit, and the dry run reads its own checkout.
func generatorPin(workflow []byte) (tag, sha string, err error) {
	var w struct {
		Env map[string]string `yaml:"env"`
	}
	if err := yaml.Unmarshal(workflow, &w); err != nil {
		return "", "", fmt.Errorf("%s is not readable: %w", releaseWorkflowPath, err)
	}
	tag, sha = w.Env["SLSA_GENERATOR_TAG"], w.Env["SLSA_GENERATOR_SHA"]
	if tag == "" || strings.ContainsAny(tag, " \t\n@") {
		return "", "", fmt.Errorf("%s has no usable env SLSA_GENERATOR_TAG (%q)", releaseWorkflowPath, tag)
	}
	if !gitSHARE.MatchString(sha) {
		return "", "", fmt.Errorf("%s has no usable env SLSA_GENERATOR_SHA (%q: want 40 lowercase hex characters)", releaseWorkflowPath, sha)
	}
	return tag, sha, nil
}

// checkProvenanceSignerWorkflow reads the generator pin from a release.yml and holds the provenance file to it.
func checkProvenanceSignerWorkflow(path string, workflow []byte) error {
	tag, sha, err := generatorPin(workflow)
	if err != nil {
		return err
	}
	return checkProvenanceSignerDigest(path, sha, generatorWorkflowURI+tag)
}

// checkProvenanceSignerDigest proves which generator commit signed the provenance. Every line of the file (one Sigstore
// bundle, or one bare DSSE envelope, per line) must carry the Fulcio certificate that signed it, and that certificate's
// Build Signer Digest must equal wantSHA and its Build Signer URI wantURI. A line without a parsable certificate, a
// missing extension or any mismatch is an error naming the file, the line and what was found against what was wanted;
// nothing is skipped. It does not verify the signature (slsa-verifier does); it reads what the certificate says.
func checkProvenanceSignerDigest(path, wantSHA, wantURI string) error {
	name := filepath.Base(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	n := 0
	for i, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n++
		cert, err := leafCertificate([]byte(ln))
		if err != nil {
			return fmt.Errorf("%s line %d: no certificate to prove the generator commit: %w", name, i+1, err)
		}
		gotSHA, err := certificateExtensionString(cert, oidBuildSignerDigest)
		if err != nil {
			return fmt.Errorf("%s line %d: Build Signer Digest (%s): %w", name, i+1, oidBuildSignerDigest, err)
		}
		if gotSHA != wantSHA {
			return fmt.Errorf("%s line %d: the certificate's Build Signer Digest is %q, not the recorded generator commit %q", name, i+1, gotSHA, wantSHA)
		}
		gotURI, err := certificateExtensionString(cert, oidBuildSignerURI)
		if err != nil {
			return fmt.Errorf("%s line %d: Build Signer URI (%s): %w", name, i+1, oidBuildSignerURI, err)
		}
		if gotURI != wantURI {
			return fmt.Errorf("%s line %d: the certificate's Build Signer URI is %q, not %q", name, i+1, gotURI, wantURI)
		}
	}
	if n == 0 {
		return fmt.Errorf("%s is empty: no certificate proves the generator commit", name)
	}
	return nil
}

// leafCertificate is the signing certificate of one provenance line: a Sigstore bundle carries it in
// verificationMaterial (certificate.rawBytes, or the first of x509CertificateChain.certificates), an older bare DSSE
// envelope in signatures[].cert (PEM).
func leafCertificate(line []byte) (*x509.Certificate, error) {
	var l struct {
		VerificationMaterial struct {
			Certificate struct {
				RawBytes string `json:"rawBytes"`
			} `json:"certificate"`
			Chain struct {
				Certificates []struct {
					RawBytes string `json:"rawBytes"`
				} `json:"certificates"`
			} `json:"x509CertificateChain"`
		} `json:"verificationMaterial"`
		Signatures []struct {
			Cert string `json:"cert"`
		} `json:"signatures"`
	}
	if err := json.Unmarshal(line, &l); err != nil {
		return nil, fmt.Errorf("the line is not JSON: %w", err)
	}
	vm := l.VerificationMaterial
	var der []byte
	switch {
	case vm.Certificate.RawBytes != "":
		d, err := decodeBase64(vm.Certificate.RawBytes)
		if err != nil {
			return nil, fmt.Errorf("verificationMaterial.certificate.rawBytes is not base64: %w", err)
		}
		der = d
	case len(vm.Chain.Certificates) > 0:
		d, err := decodeBase64(vm.Chain.Certificates[0].RawBytes)
		if err != nil {
			return nil, fmt.Errorf("verificationMaterial.x509CertificateChain.certificates[0].rawBytes is not base64: %w", err)
		}
		der = d
	default:
		for _, s := range l.Signatures {
			if s.Cert == "" {
				continue
			}
			if blk, _ := pem.Decode([]byte(s.Cert)); blk != nil {
				der = blk.Bytes
			} else if d, err := decodeBase64(s.Cert); err == nil {
				der = d
			} else {
				return nil, errors.New("signatures[].cert is neither PEM nor base64 DER")
			}
			break
		}
	}
	if len(der) == 0 {
		return nil, errors.New("the line carries no certificate (verificationMaterial.certificate, x509CertificateChain or signatures[].cert)")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("the certificate does not parse: %w", err)
	}
	return cert, nil
}

func decodeBase64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// certificateExtensionString is the string value of an extension: a DER UTF8String (what Fulcio writes for 1.8 and
// later), else the raw bytes, trimmed of whitespace.
func certificateExtensionString(cert *x509.Certificate, oid asn1.ObjectIdentifier) (string, error) {
	for _, e := range cert.Extensions {
		if !e.Id.Equal(oid) {
			continue
		}
		var s string
		if rest, err := asn1.Unmarshal(e.Value, &s); err == nil && len(rest) == 0 {
			return strings.TrimSpace(s), nil
		}
		return strings.TrimSpace(string(e.Value)), nil
	}
	return "", errors.New("the certificate has no such extension")
}

// provenanceSignerCommand is `releasecheck provenance-signer <mash.intoto.jsonl> <release.yml>`, for the dry-run
// workflow step: the same check the publish gate makes, against the pin in the given release.yml. Exit 0 ok, 3 refused.
func provenanceSignerCommand(args []string, out io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: releasecheck provenance-signer <mash.intoto.jsonl> <release.yml>")
		return 2
	}
	wf, err := os.ReadFile(args[1])
	if err == nil {
		err = checkProvenanceSignerWorkflow(args[0], wf)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "provenance-signer:", err)
		return 3
	}
	fmt.Fprintln(out, "ok: every line of", filepath.Base(args[0]), "was signed by the recorded generator commit")
	return 0
}
