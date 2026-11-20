package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// treeExceptions are the checks a command of the endpoint noun is excused from, as "verb/check". There are none:
// every command of the noun passes every check.
var treeExceptions = map[string]string{}

// treeSample is what the tree test passes one verb of endpointVerbTable. A verb in the table with no sample fails the
// test: add one to treeSamples.
type treeSample struct {
	usage      string
	positional int    // most positional arguments it takes
	arg        string // what its usage line names as the positional argument, "" when it takes none
	// readArgs are the arguments after the verb of a read command that succeed on the seeded endpoint
	readArgs func(a sap.Address) []string
	// dryArgs are the arguments after the verb of a changing command, with --dry-run added by the test, that succeed
	dryArgs func(seeded sap.Address, pubFile string) []string
	// needsKey is set when the verb requires --public-key or --make-key, so the positional-count check supplies one
	needsKey bool
}

var treeSamples = map[string]treeSample{
	"create": {usage: endpointCreateUsage, positional: 1, arg: "ADDRESS", needsKey: true,
		dryArgs: func(_ sap.Address, pub string) []string { return []string{ad("treenew").String(), "--public-key", pub} }},
	"reissue": {usage: endpointReissueUsage, positional: 1, arg: "ADDRESS", needsKey: true,
		dryArgs: func(a sap.Address, pub string) []string { return []string{a.String(), "--public-key", pub} }},
	"delete": {usage: endpointDeleteUsage, positional: 1, arg: "ADDRESS",
		dryArgs: func(a sap.Address, _ string) []string { return []string{a.String()} }},
	"list": {usage: endpointListUsage, positional: 1, arg: "[prefix]",
		readArgs: func(sap.Address) []string { return nil }},
	"show": {usage: endpointShowUsage, positional: 1, arg: "<address>",
		readArgs: func(a sap.Address) []string { return []string{a.String()} }},
	"set": {usage: endpointSetUsage, positional: -1, arg: "<address>",
		dryArgs: func(a sap.Address, _ string) []string { return []string{a.String(), "label.tree=1", "role=x"} }},
	"unset": {usage: endpointUnsetUsage, positional: -1, arg: "<address>",
		dryArgs: func(a sap.Address, _ string) []string { return []string{a.String(), "label.tree", "role"} }},
	"suspend": {usage: endpointSuspendUsage, positional: 1, arg: "<address>",
		dryArgs: func(a sap.Address, _ string) []string { return []string{a.String()} }},
	"resume": {usage: endpointResumeUsage, positional: 1, arg: "<address>",
		dryArgs: func(a sap.Address, _ string) []string { return []string{a.String()} }},
	"inbox": {usage: endpointInboxUsage, positional: 1, arg: "<address>",
		readArgs: func(a sap.Address) []string { return []string{a.String()} }},
	"permissions": {usage: endpointPermissionsUsage, positional: 1, arg: "<address>",
		readArgs: func(a sap.Address) []string { return []string{a.String()} }},
}

func treeExcused(verb, check string) bool {
	_, ok := treeExceptions[verb+"/"+check]
	return ok
}

// usageLine is the first line of a usage text.
func usageLine(u string) string { l, _, _ := strings.Cut(u, "\n"); return l }

func TestEndpointCommandTree(t *testing.T) {
	if len(endpointVerbTable) == 0 {
		t.Fatal("endpointVerbTable is empty")
	}
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	pubDir := t.TempDir()
	_, pubFile, _ := epKey(t, pubDir, "tree")
	addr := ad("tree")
	h.seed(addr)

	// the noun's own help names every verb of the table, one line each, under 80 columns
	help, _, _ := h.run("endpoint", "--help")
	for _, v := range endpointVerbTable {
		if !strings.Contains(help, "\n  "+v.name+" ") {
			t.Errorf("endpoint --help does not list %s:\n%s", v.name, help)
		}
	}
	for _, l := range strings.Split(help, "\n") {
		if len(l) >= 80 {
			t.Errorf("endpoint --help has a line of %d columns: %q", len(l), l)
		}
	}

	// the verb list in the noun's own help names exactly the table's verbs
	listed := strings.Split(endpointVerbs, ", ")
	if len(listed) != len(endpointVerbTable) {
		t.Errorf("endpointVerbs names %d verbs, the table has %d", len(listed), len(endpointVerbTable))
	}
	inTable := map[string]bool{}
	for i, v := range endpointVerbTable {
		inTable[v.name] = true
		if i < len(listed) && listed[i] != v.name {
			t.Errorf("endpointVerbs names %q at position %d, the table has %q", listed[i], i+1, v.name)
		}
		if v.read == v.changes {
			t.Errorf("verb %s: read and changes must differ (read %v, changes %v)", v.name, v.read, v.changes)
		}
	}

	// no aliases: a verb that is not in the table exits 2
	for _, alias := range []string{"add", "remove", "rm", "new", "ls", "get", "retire", "rotate", "update"} {
		if inTable[alias] {
			continue
		}
		if _, _, code := h.run("endpoint", alias); code != 2 {
			t.Errorf("endpoint %s: exit %d, want 2", alias, code)
		}
	}

	for _, c := range endpointVerbTable {
		t.Run(c.name, func(t *testing.T) {
			smp, ok := treeSamples[c.name]
			if !ok {
				t.Fatalf("endpointVerbTable has %q and treeSamples has no sample for it: add one to endpoint_tree_test.go", c.name)
			}
			// at most one positional argument, and the usage line names it
			if !treeExcused(c.name, "positional") {
				first := usageLine(smp.usage)
				if smp.arg == "" || !strings.Contains(first, smp.arg) {
					t.Errorf("usage line %q does not name %q", first, smp.arg)
				}
				if smp.positional >= 0 {
					args := []string{"endpoint", c.name}
					for i := 0; i <= smp.positional; i++ {
						args = append(args, "x"+string(rune('a'+i))+"/dev/y")
					}
					if smp.needsKey {
						args = append(args, "--public-key", pubFile)
					}
					if _, _, code := h.run(args...); code != 2 {
						t.Errorf("%d positional arguments: exit %d, want 2", smp.positional+1, code)
					}
				}
			}
			// --help first: exit 0, with an example
			if !treeExcused(c.name, "help") {
				out, errb, code := h.run("endpoint", c.name, "--help")
				if code != 0 || !strings.Contains(out, "Example") || errb != "" {
					t.Errorf("--help: exit %d stdout %q stderr %q", code, out, errb)
				}
			}
			// an unknown flag exits 2
			if !treeExcused(c.name, "unknown_flag") {
				if _, _, code := h.run("endpoint", c.name, "--no-such-flag"); code != 2 {
					t.Errorf("unknown flag: exit %d, want 2", code)
				}
			}
			// a read command prints JSON that parses
			if c.read && !treeExcused(c.name, "json") {
				if smp.readArgs == nil {
					t.Fatalf("verb %s is a read command and its sample has no readArgs", c.name)
				}
				out, errb, code := h.run(append(append([]string{"endpoint", c.name}, smp.readArgs(addr)...), "--json")...)
				var v map[string]any
				if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
					t.Errorf("--json: exit %d stdout %q stderr %q", code, out, errb)
				}
			}
			// a changing command with --dry-run changes nothing
			if c.changes && !treeExcused(c.name, "dry_run") {
				if smp.dryArgs == nil {
					t.Fatalf("verb %s is a changing command and its sample has no dryArgs", c.name)
				}
				args := append(append([]string{"endpoint", c.name}, smp.dryArgs(addr, pubFile)...), "--dry-run")
				before := epSnapshot(t, h, h.dir)
				out, errb, code := h.run(args...)
				if code != 0 {
					t.Fatalf("--dry-run: exit %d stdout %q stderr %q", code, out, errb)
				}
				if after := epSnapshot(t, h, h.dir); after != before {
					t.Errorf("--dry-run changed state:\n%s\n---\n%s", before, after)
				}
			}
		})
	}
}

// A create killed between signing and the directory record leaves an issue record and no directory record. Running
// the create again with the same key finishes it, and running it once more makes nothing new.
func TestEndpointCreateAfterKill(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	dir := t.TempDir()
	pub, pubFile, _ := epKey(t, dir, "killed")
	a := ad("design")
	if err := h.adminOf().RecordIssued(context.Background(), store.Issued{PublicKey: pub, Address: a.Flat(), Owner: a.Owner,
		Kind: store.IssuedEndpoint, Tags: []string{}, Purpose: store.PurposeIssue}); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.record(a); ok {
		t.Fatal("the killed state has a directory record")
	}
	count := func() (recs, keys int) {
		for _, r := range h.records() {
			if strings.HasPrefix(r, a.String()+"//") {
				recs++
			}
		}
		iss, _, err := h.adminOf().Issued(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range iss {
			if i.Address == a.Flat() {
				keys++
			}
		}
		return
	}
	jf := filepath.Join(dir, "design.jwt")
	// run 1 finishes the killed create; run 2 names the same --jwt-out, which exists and is never overwritten, so it
	// is refused (exit 1) before anything is written; run 3 names a new file and signs again, as enroll.Create does for
	// a finished endpoint with the same key (exit 0). The directory record and the issued key stay single.
	for run, want := range []struct {
		file string
		code int
	}{{jf, 0}, {jf, 1}, {filepath.Join(dir, "again.jwt"), 0}} {
		out, errb, code := h.run("endpoint", "create", slashForm(a), "--public-key", pubFile, "--jwt-out", want.file)
		if code != want.code {
			t.Fatalf("run %d: exit %d, want %d; stdout %q stderr %q", run+1, code, want.code, out, errb)
		}
		if recs, keys := count(); recs != 1 || keys != 1 {
			t.Fatalf("run %d: %d directory records, %d issued keys, want 1 and 1", run+1, recs, keys)
		}
	}
	if r, ok := h.record(a); !ok || r.User != pub {
		t.Errorf("the record names %q, want %q", r.User, pub)
	}
}
