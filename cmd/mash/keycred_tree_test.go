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

// treeExceptions are the checks a command of the key and credential nouns is excused from, as "noun create/check".
// There are none: every command passes every check.
var treeExceptions = map[string]string{}

// treeNoun is one noun of this file and the usage text of its only verb.
type treeNoun struct {
	noun  string
	usage string // the usage text of `mash NOUN create`
}

var treeNouns = []treeNoun{
	{"key", keyCreateUsage},
	{"credential", credentialCreateUsage},
}

func treeExcused(noun, check string) bool {
	_, ok := treeExceptions[noun+" create/"+check]
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
	for _, n := range treeNouns {
		t.Run(n.noun, func(t *testing.T) {
			dir, seedPath, jwtPath, _, _ := kcCredFixture(t, "tree")
			cfg := filepath.Join(dir, "cfg")
			// valid arguments for a run that succeeds, with its own output path
			good := func(out string) []string {
				if n.noun == "key" {
					return []string{n.noun, "create", "--out", filepath.Join(dir, out+".seed")}
				}
				return []string{n.noun, "create", "--seed", seedPath, "--jwt", jwtPath, "--out", filepath.Join(dir, out+".creds")}
			}

			// the usage line names no positional: these commands take none
			if !treeExcused(n.noun, "positional") {
				first, _, _ := strings.Cut(n.usage, "\n")
				if strings.Contains(first, "ADDRESS") || strings.Contains(first, "NAME") {
					t.Errorf("usage line %q names a positional", first)
				}
				if rc, _, _ := kcRun(t, cfg, append(good("pos"), "extra")...); rc != 2 {
					t.Errorf("one positional argument: exit %d, want 2", rc)
				}
			}

			// no aliases: a verb that is not listed exits 2 and names the valid verb
			if !treeExcused(n.noun, "aliases") {
				for _, v := range []string{"add", "new", "make", "generate", "ls", "list", "show", "delete", "rm", "remove", "build", "update"} {
					rc, out, errb := kcRun(t, cfg, n.noun, v)
					if rc != 2 || !strings.Contains(errb, "create") || out != "" {
						t.Errorf("%s %s: exit %d stdout %q stderr %q, want 2 naming create", n.noun, v, rc, out, errb)
					}
				}
			}

			// --help first: exit 0, with an example
			if !treeExcused(n.noun, "help") {
				rc, out, errb := kcRun(t, cfg, n.noun, "create", "--help")
				if rc != 0 || !strings.Contains(out, "Example") || errb != "" {
					t.Errorf("--help: exit %d stdout %q stderr %q", rc, out, errb)
				}
			}

			// --json parses, on success and on a refusal
			if !treeExcused(n.noun, "json") {
				args := append(good("json"), "--json")
				rc, out, errb := kcRun(t, cfg, args...)
				var v map[string]any
				if rc != 0 || json.Unmarshal([]byte(out), &v) != nil || v["outcome"] != "created" {
					t.Errorf("--json: exit %d stdout %q stderr %q", rc, out, errb)
				}
				rc, out, errb = kcRun(t, cfg, args...) // the output exists now
				v = nil
				if rc != 1 || json.Unmarshal([]byte(out), &v) != nil || v["outcome"] != "refused" {
					t.Errorf("--json refusal: exit %d stdout %q stderr %q", rc, out, errb)
				}
			}

			// --dry-run leaves the directory byte-identical
			if !treeExcused(n.noun, "dry_run") {
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
			if !treeExcused(n.noun, "unknown_flag") {
				if rc, _, _ := kcRun(t, cfg, append(good("flag"), "--no-such-flag")...); rc != 2 {
					t.Errorf("unknown flag: exit %d, want 2", rc)
				}
			}

			// a missing required flag exits 2
			if !treeExcused(n.noun, "required_flag") {
				if rc, _, _ := kcRun(t, cfg, n.noun, "create"); rc != 2 {
					t.Errorf("no flags: exit %d, want 2", rc)
				}
				if n.noun == "credential" {
					if rc, _, _ := kcRun(t, cfg, n.noun, "create", "--seed", seedPath); rc != 2 {
						t.Errorf("no --jwt: exit %d, want 2", rc)
					}
				}
			}
		})
	}
}
