package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateRemoved = flag.Bool("update-removed", false, "rewrite cmd/mashctl/testdata/removed_commands.golden.txt")

// The removed verbs run nothing, load no config, exit 2 and name the replacement. They are not aliases.
func TestRemovedCommands(t *testing.T) {
	var got strings.Builder
	noenv := func(string) string { return "" }
	for _, verb := range []string{"add", "reissue", "retire"} {
		for _, args := range [][]string{{verb}, {verb, "alice/dev/design", "--public-key", "x.pub"}, {verb, "--help"}, {verb, "--servers", "x"}} {
			var out, errb strings.Builder
			// an environment with no configuration: a command that loaded it would fail differently
			code := runEnv(args, noenv, &out, &errb)
			if code != 2 || out.String() != "" || strings.Count(errb.String(), "\n") != 1 {
				t.Errorf("%v: exit %d stdout %q stderr %q", args, code, out.String(), errb.String())
			}
			if args[0] == verb && len(args) == 1 {
				fmt.Fprintf(&got, "$ mashctl %s\n%sexit %d\n\n", verb, errb.String(), code)
			}
			if !strings.Contains(errb.String(), "mashctl: "+verb+" was removed. Use: mashctl endpoint ") {
				t.Errorf("%v: stderr %q", args, errb.String())
			}
		}
	}
	golden := filepath.Join("testdata", "removed_commands.golden.txt")
	if *updateRemoved {
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if got.String() != string(want) {
		t.Fatalf("%s differs (run with -update-removed after checking the diff)\n got: %s\nwant: %s", golden, got.String(), want)
	}
}
