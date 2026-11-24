package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/spool"
)

// Compatibility gate for what a session is shown. The label a user sees, [verified], [forged] and so on, must be
// byte-identical across releases: a later release reads the spool this one wrote and must render it the same way. The
// golden is the output of v0.5.0 for the five shown spool entries pinned by internal/spool/testdata/compat. Forged
// and malformed mail is never shown (ADR 0019), so those entries are not in the golden; the golden test asserts that
// neither label nor body appears in the rendered output (the endpoint drops them before the spool: see classify). Regenerate it deliberately with UPDATE_GOLDEN=1; the default run only reads.
func compatEntries(t *testing.T, labels ...string) []spool.Entry {
	t.Helper()
	var in []spool.Entry
	for _, l := range labels {
		b, err := os.ReadFile(filepath.Join("..", "spool", "testdata", "compat", l+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var e spool.Entry
		if err := json.Unmarshal(b, &e); err != nil {
			t.Fatal(err)
		}
		in = append(in, e)
	}
	return in
}

func TestCompatRenderGolden(t *testing.T) {
	// The order of internal/spool/testdata/trust_labels.golden, without forged and malformed.
	in := compatEntries(t, "verified", "claimed", "absent", "unknown-signer", "stale")
	p := Presentation{Name: "ag.team.me", Owner: "team", Event: "PostToolUse", BatchMsgs: 10, BatchBytes: 6000}
	got, _ := Claude{}.Present(p, in)

	for _, bad := range []string{"[forged]", "[malformed]", "hello forged", "hello malformed"} {
		if strings.Contains(string(got), bad) {
			t.Errorf("%q is shown in the rendered output, against ADR 0019", bad)
		}
	}

	path := filepath.Join("testdata", "compat_render.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("rendered output changed\n got:\n%s\nwant:\n%s", got, want)
	}
}
