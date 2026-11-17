package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// treeExceptions are the checks a command of the key and credential nouns is excused from, as "noun verb/check".
// There are none: every command passes every check.
var treeExceptions = map[string]string{}

// treeSample is the arguments the tree test uses for one command of keycredVerbTable, by "noun verb". A command in the
// table with no sample fails the test: add one here.
type treeSample struct {
	good    func(dir, seed, jwt, out string) []string // valid arguments for a run that succeeds, writing to out
	partial [][]string                                // arguments after the verb that lack a required flag; each exits 2
}

var treeSamples = map[string]treeSample{
	"key create": {
		good: func(dir, seed, jwt, out string) []string {
			return []string{"--out", filepath.Join(dir, out+".seed")}
		},
		partial: [][]string{{}},
	},
	"credential create": {
		good: func(dir, seed, jwt, out string) []string {
			return []string{"--seed", seed, "--jwt", jwt, "--out", filepath.Join(dir, out+".creds")}
		},
		partial: [][]string{{}, {"--seed", "x.seed"}},
	},
}

func treeExcused(noun, verb, check string) bool {
	_, ok := treeExceptions[noun+" "+verb+"/"+check]
	return ok
}

// treeSnapshot is every path under dir with its mode and, for a file, its bytes.
func treeSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := p + " " + info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(b)
		}
		paths = append(paths, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

func TestKeyCredentialCommandTree(t *testing.T) {
	if len(keycredVerbTable) == 0 {
		t.Fatal("keycredVerbTable is empty")
	}
	// every verb of a noun, from the table, for the no-alias check
	verbsOf := map[string][]string{}
	for _, v := range keycredVerbTable {
		verbsOf[v.noun] = append(verbsOf[v.noun], v.name)
	}
	for _, v := range keycredVerbTable {
		key := v.noun + " " + v.name
		t.Run(key, func(t *testing.T) {
			smp, ok := treeSamples[key]
			if !ok {
				t.Fatalf("keycredVerbTable has %q and treeSamples has no sample for it: add one to keycred_tree_test.go", key)
			}
			dir, seedPath, jwtPath, _, _ := kcCredFixture(t, "tree")
			cfg := filepath.Join(dir, "cfg")
			good := func(out string) []string {
				return append([]string{v.noun, v.name}, smp.good(dir, seedPath, jwtPath, out)...)
			}

			// the usage line names no positional: these commands take none
			if !treeExcused(v.noun, v.name, "positional") {
				first, _, _ := strings.Cut(v.usage, "\n")
				if strings.Contains(first, "ADDRESS") || strings.Contains(first, "NAME") {
					t.Errorf("usage line %q names a positional", first)
				}
				if rc, _, _ := kcRun(t, cfg, append(good("pos"), "extra")...); rc != 2 {
					t.Errorf("one positional argument: exit %d, want 2", rc)
				}
			}

			// no aliases: a verb that is not in the table exits 2 and names the valid verbs
			if !treeExcused(v.noun, v.name, "aliases") {
				for _, alias := range []string{"add", "new", "make", "generate", "ls", "list", "show", "delete", "rm", "remove", "build", "update"} {
					listed := false
					for _, n := range verbsOf[v.noun] {
						if n == alias {
							listed = true
						}
					}
					if listed {
						continue
					}
					rc, out, errb := kcRun(t, cfg, v.noun, alias)
					if rc != 2 || !strings.Contains(errb, v.name) || out != "" {
						t.Errorf("%s %s: exit %d stdout %q stderr %q, want 2 naming %s", v.noun, alias, rc, out, errb, v.name)
					}
				}
			}

			// --help first: exit 0, with an example
			if !treeExcused(v.noun, v.name, "help") {
				rc, out, errb := kcRun(t, cfg, v.noun, v.name, "--help")
				if rc != 0 || !strings.Contains(out, "Example") || errb != "" {
					t.Errorf("--help: exit %d stdout %q stderr %q", rc, out, errb)
				}
			}

			// --json parses, on success and on a refusal
			if !treeExcused(v.noun, v.name, "json") {
				args := append(good("json"), "--json")
				rc, out, errb := kcRun(t, cfg, args...)
				var doc map[string]any
				if rc != 0 || json.Unmarshal([]byte(out), &doc) != nil || doc["outcome"] != "created" {
					t.Errorf("--json: exit %d stdout %q stderr %q", rc, out, errb)
				}
				rc, out, errb = kcRun(t, cfg, args...) // the output exists now
				doc = nil
				if rc != 1 || json.Unmarshal([]byte(out), &doc) != nil || doc["outcome"] != "refused" {
					t.Errorf("--json refusal: exit %d stdout %q stderr %q", rc, out, errb)
				}
			}

			// --dry-run leaves the directory byte-identical
			if v.changes && !treeExcused(v.noun, v.name, "dry_run") {
				before := treeSnapshot(t, dir)
				rc, out, errb := kcRun(t, cfg, append(good("dry"), "--dry-run")...)
				if rc != 0 {
					t.Fatalf("--dry-run: exit %d stdout %q stderr %q", rc, out, errb)
				}
				if after := treeSnapshot(t, dir); after != before {
					t.Errorf("--dry-run changed the directory:\n%s\n---\n%s", before, after)
				}
			}

			// an unknown flag exits 2
			if !treeExcused(v.noun, v.name, "unknown_flag") {
				if rc, _, _ := kcRun(t, cfg, append(good("flag"), "--no-such-flag")...); rc != 2 {
					t.Errorf("unknown flag: exit %d, want 2", rc)
				}
			}

			// a missing required flag exits 2
			if !treeExcused(v.noun, v.name, "required_flag") {
				for _, p := range smp.partial {
					if rc, _, _ := kcRun(t, cfg, append([]string{v.noun, v.name}, p...)...); rc != 2 {
						t.Errorf("%v: exit %d, want 2", p, rc)
					}
				}
			}
		})
	}
}
