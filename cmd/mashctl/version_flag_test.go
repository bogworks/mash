package main

import "testing"

// `mashctl --version` is `mashctl version`: the same output and exit code, and --json works the same way.
func TestVersionFlagIsVersionCommand(t *testing.T) {
	h := newFleetEnv(t)
	h.env = map[string]string{}
	for _, extra := range [][]string{nil, {"--json"}} {
		wantOut, wantErr, wantCode := h.run(append([]string{"version"}, extra...)...)
		gotOut, gotErr, gotCode := h.run(append([]string{"--version"}, extra...)...)
		if wantCode != 0 || wantOut == "" {
			t.Fatalf("version %v: %d %q %q", extra, wantCode, wantOut, wantErr)
		}
		if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
			t.Errorf("--version %v: %d %q %q; want %d %q %q", extra, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
		}
	}
}
