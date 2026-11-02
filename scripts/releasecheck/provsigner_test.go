package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testGeneratorTag = "v2.1.0"
	testGeneratorSHA = "f7dd8c54c2067bafc12ca7a55595d5ee9b75204a"
	testGeneratorURI = generatorWorkflowURI + testGeneratorTag
)

// testReleaseYML is the part of release.yml the gate reads: the generator pin.
const testReleaseYML = "name: release\nenv:\n  SLSA_GENERATOR_TAG: " + testGeneratorTag + "\n  SLSA_GENERATOR_SHA: " + testGeneratorSHA + "\njobs: {}\n"

var (
	testKeyOnce sync.Once
	testKey     *ecdsa.PrivateKey
)

func utf8Ext(t testing.TB, v string) []byte {
	b, err := asn1.MarshalWithParams(v, "utf8")
	if err != nil {
		panic(err)
	}
	return b
}

// testCertDER builds a certificate; a nil value omits the extension, and the value is the extension's raw bytes.
func testCertDER(t testing.TB, digest, uri []byte) []byte {
	testKeyOnce.Do(func() {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	if digest != nil {
		tpl.ExtraExtensions = append(tpl.ExtraExtensions, pkix.Extension{Id: oidBuildSignerDigest, Value: digest})
	}
	if uri != nil {
		tpl.ExtraExtensions = append(tpl.ExtraExtensions, pkix.Extension{Id: oidBuildSignerURI, Value: uri})
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &testKey.PublicKey, testKey)
	if err != nil {
		panic(err)
	}
	return der
}

func goodCertDER(t testing.TB) []byte {
	return testCertDER(t, utf8Ext(t, testGeneratorSHA), utf8Ext(t, testGeneratorURI))
}

// bundleLineWithCert is a Sigstore bundle line carrying der as verificationMaterial.certificate.
func bundleLineWithCert(statement string, der []byte) string {
	return `{"mediaType":"application/vnd.dev.sigstore.bundle+json;version=0.2","verificationMaterial":{"certificate":{"rawBytes":"` + base64.StdEncoding.EncodeToString(der) + `"}},"dsseEnvelope":{"payload":"` + base64.StdEncoding.EncodeToString([]byte(statement)) + `","payloadType":"application/vnd.in-toto+json","signatures":[]}}` + "\n"
}

// chainLine is a bundle line with the older x509CertificateChain form.
func chainLine(der []byte) string {
	return `{"mediaType":"application/vnd.dev.sigstore.bundle+json;version=0.1","verificationMaterial":{"x509CertificateChain":{"certificates":[{"rawBytes":"` + base64.StdEncoding.EncodeToString(der) + `"}]}},"dsseEnvelope":{"payload":"e30=","payloadType":"application/vnd.in-toto+json","signatures":[]}}` + "\n"
}

// bareLineWithCert is a bare DSSE envelope whose signature carries a PEM certificate.
func bareLineWithCert(der []byte) string { return bareLineWithCertStatement("{}", der) }

func bareLineWithCertStatement(statement string, der []byte) string {
	p := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return `{"payloadType":"application/vnd.in-toto+json","payload":"` + base64.StdEncoding.EncodeToString([]byte(statement)) + `","signatures":[{"sig":"AA==","cert":` + jsonStr(p) + `}]}` + "\n"
}

func jsonStr(s string) string {
	return `"` + strings.NewReplacer("\n", `\n`, `"`, `\"`).Replace(s) + `"`
}

func writeProv(t testing.TB, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mash.intoto.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProvenanceSigner(t *testing.T) {
	good := goodCertDER(t)
	otherSHA := strings.Repeat("a", 40)
	raw := func(s string) []byte { return []byte(s) }
	cases := map[string]struct {
		body string
		want string // empty: accepted; else a substring of the refusal
	}{
		"bundle certificate matches":      {bundleLineWithCert("{}", good), ""},
		"bundle certificate chain":        {chainLine(good), ""},
		"bare envelope with a PEM cert":   {bareLineWithCert(good), ""},
		"two good lines of mixed shapes":  {bundleLineWithCert("{}", good) + bareLineWithCert(good) + chainLine(good), ""},
		"digest as a raw string":          {bundleLineWithCert("{}", testCertDER(t, raw(testGeneratorSHA), raw(testGeneratorURI))), ""},
		"raw digest with trailing space":  {bundleLineWithCert("{}", testCertDER(t, raw(testGeneratorSHA+"\n"), utf8Ext(t, testGeneratorURI))), ""},
		"digest differs":                  {bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, otherSHA), utf8Ext(t, testGeneratorURI))), otherSHA},
		"digest extension missing":        {bundleLineWithCert("{}", testCertDER(t, nil, utf8Ext(t, testGeneratorURI))), "no such extension"},
		"uri extension missing":           {bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, testGeneratorSHA), nil)), "Build Signer URI"},
		"wrong uri":                       {bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, testGeneratorSHA), utf8Ext(t, generatorWorkflowURI+"v2.0.0"))), "v2.0.0"},
		"no certificate":                  {dsseLine(`{}`), "no certificate to prove"},
		"bare envelope without cert":      {bareDSSELine(`{}`), "no certificate to prove"},
		"garbage line":                    {"nonsense\n", "not JSON"},
		"garbage certificate":             {bundleLineWithCert("{}", []byte("not a certificate")), "does not parse"},
		"one good and one bad line":       {bundleLineWithCert("{}", good) + bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, otherSHA), utf8Ext(t, testGeneratorURI))), "line 2"},
		"one good and one garbage line":   {bundleLineWithCert("{}", good) + "nonsense\n", "line 2"},
		"one good and one without a cert": {bundleLineWithCert("{}", good) + dsseLine(`{}`), "line 2"},
		"empty file":                      {"", "empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeProv(t, tc.body)
			err := checkProvenanceSignerDigest(p, testGeneratorSHA, testGeneratorURI)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && err == nil:
				t.Fatal("accepted")
			case tc.want != "" && (!strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "mash.intoto.jsonl")):
				t.Fatalf("err = %v, want one naming the file and %q", err, tc.want)
			}
		})
	}
	t.Run("a digest mismatch names both the found and the wanted commit", func(t *testing.T) {
		err := checkProvenanceSignerDigest(writeProv(t, bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, otherSHA), utf8Ext(t, testGeneratorURI)))), testGeneratorSHA, testGeneratorURI)
		if err == nil || !strings.Contains(err.Error(), otherSHA) || !strings.Contains(err.Error(), testGeneratorSHA) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a missing file is refused", func(t *testing.T) {
		if err := checkProvenanceSignerDigest(filepath.Join(t.TempDir(), "mash.intoto.jsonl"), testGeneratorSHA, testGeneratorURI); err == nil {
			t.Fatal("accepted")
		}
	})
}

func TestGeneratorPin(t *testing.T) {
	tag, sha, err := generatorPin([]byte(testReleaseYML))
	if err != nil || tag != testGeneratorTag || sha != testGeneratorSHA {
		t.Fatalf("got %q %q %v", tag, sha, err)
	}
	for name, yml := range map[string]string{
		"not yaml":    "a: [",
		"no env":      "jobs: {}\n",
		"no sha":      "env:\n  SLSA_GENERATOR_TAG: v2.1.0\n",
		"short sha":   "env:\n  SLSA_GENERATOR_TAG: v2.1.0\n  SLSA_GENERATOR_SHA: abc\n",
		"no tag":      "env:\n  SLSA_GENERATOR_SHA: " + testGeneratorSHA + "\n",
		"upper sha":   "env:\n  SLSA_GENERATOR_TAG: v2.1.0\n  SLSA_GENERATOR_SHA: " + strings.ToUpper(testGeneratorSHA) + "\n",
		"empty bytes": "",
	} {
		if _, _, err := generatorPin([]byte(yml)); err == nil {
			t.Errorf("%s: an unreadable pin was accepted", name)
		}
	}
}

// The real release.yml: its pin parses, and --builder-id names its tag, so there is one place to edit.
func TestGeneratorPin_realWorkflowAgreesWithBuilderID(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", releaseWorkflowPath))
	if err != nil {
		t.Fatal(err)
	}
	tag, _, err := generatorPin(b)
	if err != nil {
		t.Fatal(err)
	}
	if slsaBuilderID != generatorWorkflowURI+tag {
		t.Errorf("slsaBuilderID %q does not name SLSA_GENERATOR_TAG %s in release.yml", slsaBuilderID, tag)
	}
}

func TestProvenanceSignerCommand(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "release.yml")
	if err := os.WriteFile(wf, []byte(testReleaseYML), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if c := provenanceSignerCommand([]string{writeProv(t, bundleLineWithCert("{}", goodCertDER(t))), wf}, &out); c != 0 {
		t.Errorf("good provenance: exit %d", c)
	}
	if c := provenanceSignerCommand([]string{writeProv(t, bundleLineWithCert("{}", testCertDER(t, utf8Ext(t, strings.Repeat("b", 40)), utf8Ext(t, testGeneratorURI)))), wf}, &out); c != 3 {
		t.Errorf("wrong signer: exit %d, want 3", c)
	}
	if c := provenanceSignerCommand([]string{writeProv(t, ""), filepath.Join(dir, "missing.yml")}, &out); c != 3 {
		t.Errorf("unreadable pin: exit %d, want 3", c)
	}
	if c := provenanceSignerCommand([]string{"x"}, &out); c != 2 {
		t.Errorf("usage: exit %d, want 2", c)
	}
	if c := mainRun([]string{"provenance-signer"}, &out); c != 2 {
		t.Errorf("mainRun dispatch: exit %d, want 2", c)
	}
}
