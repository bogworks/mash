package receipt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

func dig(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// goldenReceipt is a fixed passing receipt for linux/amd64 built the way the runner builds one.
func goldenReceipt() Receipt {
	commit := strings.Repeat("ab", 20)
	r := Receipt{
		Kind: KindAT011, Commit: commit, Result: ResultPassed,
		Environment:   "golden: a throwaway container created for the run and destroyed after it",
		InstallSHA256: dig("install.sh"), SumsSHA256: dig("SHA256SUMS"),
		PrevVersion: "0.5.10", UpgradePage: "0123456789abcdef0123456789abcdef01234567",
	}
	for _, p := range []string{"mash", "mashctl"} {
		r.Executables = append(r.Executables, Exec{Name: p, GOOS: "linux", GOARCH: "amd64", SHA256: dig("new " + p),
			Build: BuildConfig{Toolchain: release.Toolchain, Tags: []string{}, Flags: release.BuildFlags("0.6.0", commit)}})
	}
	for _, b := range release.Binaries() {
		r.Prior = append(r.Prior, Prior{Name: b.Program, GOOS: b.GOOS, GOARCH: b.GOARCH, SHA256: dig("old " + b.AssetName())})
	}
	return r
}

func TestProblemsCleanOnGolden(t *testing.T) {
	if ps := goldenReceipt().Problems(); len(ps) > 0 {
		t.Fatalf("golden receipt has problems: %v", ps)
	}
}

func TestProblemsFindsGaps(t *testing.T) {
	r := goldenReceipt()
	r.Commit, r.Environment, r.SumsSHA256, r.Prior = "short", "", "", nil
	if ps := r.Problems(); len(ps) != 4 {
		t.Fatalf("want 4 problems, got %v", ps)
	}
}

func TestMarshalTagsNeverNull(t *testing.T) {
	r := goldenReceipt()
	r.Executables[0].Build.Tags = nil
	b, err := Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"tags": []`)) || bytes.Contains(b, []byte("null")) {
		t.Fatalf("tags not an empty list:\n%s", b)
	}
}

// TestGolden holds the golden receipt the gate's test reads (scripts/releasecheck/testdata) to what this package
// writes. Set UPDATE_GOLDEN=1 to rewrite it.
func TestGolden(t *testing.T) {
	path := filepath.Join("..", "..", "releasecheck", "testdata", "at011-runner-golden.json")
	want, err := Marshal(goldenReceipt())
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("golden receipt is out of date; run with UPDATE_GOLDEN=1")
	}
}
