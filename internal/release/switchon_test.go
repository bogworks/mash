package release

import "testing"

func TestRequiresProofs(t *testing.T) {
	for v, want := range map[string]bool{
		"0.5.9": false, "0.5.10": false, "0.6.0": true, "0.6.1": true, "1.0.0": true, "0.10.0": true,
		"0.0.1": false, "": false, "0.6": false, "v0.6.0": false,
	} {
		if got := RequiresProofs(v); got != want {
			t.Errorf("RequiresProofs(%q) = %v, want %v", v, got, want)
		}
	}
}
