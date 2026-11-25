package main

import (
	"strings"
	"testing"
)

// `mash --version` is `mash version`: the same output and exit code, and --json works the same way.
func TestVersionFlagIsVersionCommand(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	for _, extra := range [][]string{nil, {"--json"}} {
		wantOut, wantErr, wantCode := statusOut(t, env, append([]string{"version"}, extra...)...)
		gotOut, gotErr, gotCode := statusOut(t, env, append([]string{"--version"}, extra...)...)
		if wantCode != 0 || wantOut == "" {
			t.Fatalf("version %v: %d %q %q", extra, wantCode, wantOut, wantErr)
		}
		if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
			t.Errorf("--version %v: %d %q %q; want %d %q %q", extra, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
		}
	}
	// the global option may come first, as for any command
	wantOut, _, _ := statusOut(t, env, "version", "--json")
	if gotOut, _, code := statusOut(t, env, "--json", "--version"); code != 0 || gotOut != wantOut {
		t.Errorf("--json --version: %d %q; want %q", code, gotOut, wantOut)
	}
}

// A malformed address names the owner.group.name form, never "instance".
func TestMalformedAddressSaysName(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	out, errb, code := statusOut(t, env, "--endpoint", "bad", "status")
	all := out + errb
	if code == 0 || !strings.Contains(all, "owner.group.name") || strings.Contains(all, "instance") {
		t.Errorf("%d %q", code, all)
	}
}
