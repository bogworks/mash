package endpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The planned goldens are the target text (style A), not what main prints. They are asserted against real output by W,
// not by this stage; here they are only checked for form until W promotes them: every line at most 80 columns, and no
// empty line except the one blank separator of TO-12.
func TestPlannedGoldensForm(t *testing.T) {
	for _, name := range []string{"TO-10", "TO-11", "TO-12"} {
		b, err := os.ReadFile(filepath.Join("testdata", "planned", name+".next.golden"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if !strings.HasSuffix(s, "\n") {
			t.Errorf("%s: no final newline", name)
		}
		blanks := 0
		for _, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
			if utf8.RuneCountInString(l) > 80 {
				t.Errorf("%s: %q is wider than 80 columns", name, l)
			}
			if l == "" {
				blanks++
			}
		}
		want := 0
		if name == "TO-12" {
			want = 1
		}
		if blanks != want {
			t.Errorf("%s: %d empty lines, want %d", name, blanks, want)
		}
	}
}
