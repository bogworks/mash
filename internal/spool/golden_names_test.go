// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package spool

import (
	"path/filepath"
	"testing"
)

// The spool directory mash derives from (owner, name): <state>/spool/<owner>/<name>.
func TestGoldenSpoolDir(t *testing.T) {
	const n48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
	for _, c := range []struct{ state, owner, name, want string }{
		{"/s", "o", "a", "/s/spool/o/a"},
		{"/s", "acme-corp", "build-1", "/s/spool/acme-corp/build-1"},
		{"/s", n48, n48, "/s/spool/" + n48 + "/" + n48},
	} {
		if got := Dir(c.state, c.owner, c.name); got != filepath.FromSlash(c.want) {
			t.Errorf("Dir = %q, want %q", got, c.want)
		}
	}
}
