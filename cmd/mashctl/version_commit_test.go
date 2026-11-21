package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// `mashctl version --json` carries the whole commit; the text form shows seven.
func TestVersionJSONCarriesTheWholeCommit(t *testing.T) {
	full := "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098"
	old := commit // the -ldflags value, which run reads
	commit = full
	defer func() { commit = old }()
	vars := map[string]string{"HOME": t.TempDir()}
	env := func(k string) string { return vars[k] }
	lookup := func(k string) (string, bool) { x, ok := vars[k]; return x, ok }
	var out, errb bytes.Buffer
	if code := run([]string{"version", "--json"}, env, lookup, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var o map[string]string
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || o["commit"] != full || len(o["commit"]) != 40 {
		t.Errorf("version --json: %q %v", out.String(), err)
	}
	out.Reset()
	if code := run([]string{"version"}, env, lookup, &out, &errb); code != 0 || !strings.HasSuffix(out.String(), " (3f9a41c)\n") {
		t.Errorf("version: %d %q", code, out.String())
	}
}
