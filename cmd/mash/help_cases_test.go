package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every x-output case of spec/mash.json named "mash <words> --help" is the command's help, word for word: the options
// that apply to the command, no others, and no Global options block.
func TestHelpCasesMatchSpec(t *testing.T) {
	spec := loadConfSpec(t)
	env := confEnv(t)
	n := 0
	confWalk(spec.Root, func(path string, c confCmd) {
		for _, c := range specCasesOf(t, c) {
			words, ok := strings.CutSuffix(c.Case, " --help")
			if !ok || !strings.HasPrefix(words, "mash") {
				continue
			}
			n++
			t.Run(c.Case, func(t *testing.T) {
				c.check(t, runMash(env, append(strings.Fields(strings.TrimPrefix(words, "mash")), "--help")...))
			})
		}
	})
	if n < 5 {
		t.Errorf("spec/mash.json has %d help cases, want at least send, serve, hook, prompt and key create", n)
	}
}

// specCasesOf are the x-output cases of c.
func specCasesOf(t *testing.T, c confCmd) []specCase {
	t.Helper()
	var out []specCase
	for _, m := range c.Metadata {
		if m.Name == "x-output" {
			if err := json.Unmarshal(m.Value, &out); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}
