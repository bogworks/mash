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

// treeCommand is one command of the endpoint noun and what the command-tree test expects of it.
type treeCommand struct {
	verb       string
	positional int  // most positional arguments it takes
	changes    bool // it changes state, so it has --dry-run
	reads      bool // it only reads, so it has --json
}

var treeCommands = []treeCommand{
	{verb: "create", positional: 1, changes: true},
	{verb: "reissue", positional: 1, changes: true},
	{verb: "delete", positional: 1, changes: true},
	{verb: "list", positional: 0, reads: true},
	{verb: "show", positional: 1, reads: true},
}

// treeReadArgs is the arguments of a read command that has one: the seeded endpoint.
func treeReadArgs(c treeCommand, a sap.Address) []string {
	if c.positional == 0 {
		return nil
	}
	return []string{slashForm(a)}
}

func treeExcused(verb, check string) bool {
	_, ok := treeExceptions[verb+"/"+check]
	return ok
}

// usageLine is the first line of a usage text.
func usageLine(u string) string { l, _, _ := strings.Cut(u, "\n"); return l }

func TestEndpointCommandTree(t *testing.T) {
	usages := map[string]string{"create": endpointCreateUsage, "reissue": endpointReissueUsage, "delete": endpointDeleteUsage,
		"list": endpointListUsage, "show": endpointShowUsage}
	h := newFleetEnv(t)
	pubDir := t.TempDir()
	_, pubFile, _ := epKey(t, pubDir, "tree")
	addr := ad("tree")
	h.seed(addr)

	// the verb list in the noun's own help names exactly these commands
	for _, c := range treeCommands {
		if !strings.Contains(endpointVerbs, c.verb) {
			t.Errorf("endpointVerbs %q lacks %s", endpointVerbs, c.verb)
		}
	}
	if got := len(strings.Split(endpointVerbs, ", ")); got != len(treeCommands) {
		t.Errorf("endpointVerbs names %d verbs, the table has %d", got, len(treeCommands))
	}

	// no aliases: a verb that is not listed exits 2
	for _, v := range []string{"add", "remove", "rm", "new", "ls", "get", "retire", "rotate", "update"} {
		if _, _, code := h.run("endpoint", v); code != 2 {
			t.Errorf("endpoint %s: exit %d, want 2", v, code)
		}
	}

	for _, c := range treeCommands {
		u := usages[c.verb]
		t.Run(c.verb, func(t *testing.T) {
			// at most one positional argument, and the usage line names it
			if !treeExcused(c.verb, "positional") {
				first := usageLine(u)
				if c.positional == 1 && !strings.Contains(first, "ADDRESS") {
					t.Errorf("usage line %q does not name ADDRESS", first)
				}
				if c.positional == 0 && strings.Contains(first, "ADDRESS") {
					t.Errorf("usage line %q names a positional the command does not take", first)
				}
				args := []string{"endpoint", c.verb}
				for i := 0; i <= c.positional; i++ {
					args = append(args, "x"+string(rune('a'+i))+"/dev/y")
				}
				if c.verb == "create" || c.verb == "reissue" {
					args = append(args, "--public-key", pubFile)
				}
				if _, _, code := h.run(args...); code != 2 {
					t.Errorf("%d positional arguments: exit %d, want 2", c.positional+1, code)
				}
			}
			// --help first: exit 0, with an example
			if !treeExcused(c.verb, "help") {
				out, errb, code := h.run("endpoint", c.verb, "--help")
				if code != 0 || !strings.Contains(out, "Example") || errb != "" {
					t.Errorf("--help: exit %d stdout %q stderr %q", code, out, errb)
				}
			}
			// an unknown flag exits 2
			if !treeExcused(c.verb, "unknown_flag") {
				if _, _, code := h.run("endpoint", c.verb, "--no-such-flag"); code != 2 {
					t.Errorf("unknown flag: exit %d, want 2", code)
				}
			}
			// a read command prints JSON that parses
			if c.reads && !treeExcused(c.verb, "json") {
				out, errb, code := h.run(append(append([]string{"endpoint", c.verb}, treeReadArgs(c, addr)...), "--json")...)
				var v map[string]any
				if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
					t.Errorf("--json: exit %d stdout %q stderr %q", code, out, errb)
				}
			}
			// a changing command with --dry-run changes nothing
			if c.changes && !treeExcused(c.verb, "dry_run") {
				var args []string
				switch c.verb {
				case "create":
					args = []string{"endpoint", "create", ad("treenew").String(), "--public-key", pubFile, "--dry-run"}
				case "reissue":
					args = []string{"endpoint", "reissue", addr.String(), "--public-key", pubFile, "--dry-run"}
				case "delete":
					args = []string{"endpoint", "delete", addr.String(), "--dry-run"}
				}
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
