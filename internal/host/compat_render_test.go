package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/internal/spool"
)

// Compatibility gate for what a session is shown. The label a user sees, [verified], [forged] and so on, must be
// byte-identical across releases: a later release reads the spool this one wrote and must render it the same way. The
// golden is the output of v0.5.0 for the seven spool entries pinned by internal/spool/testdata/compat. Regenerate it
// deliberately with UPDATE_GOLDEN=1; the default run only reads.
func TestCompatRenderGolden(t *testing.T) {
	var in []spool.Entry
	// The order of internal/spool/testdata/trust_labels.golden.
	for _, l := range []string{"verified", "claimed", "absent", "forged", "malformed", "unknown-signer", "stale"} {
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
	p := Presentation{Name: "ag.team.me", Owner: "team", Event: "PostToolUse", BatchMsgs: 10, BatchBytes: 6000}
	got, _ := Claude{}.Present(p, in)

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
