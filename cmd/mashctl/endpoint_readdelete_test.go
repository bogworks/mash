package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
)

var updateEndpointGolden = flag.Bool("update-endpoint", false, "rewrite the cmd/mashctl/testdata/endpoint_*.golden.json files")

// scrub replaces what changes from run to run (user keys, times, the temporary directory) by fixed words.
func (h *fleetEnv) scrub(s string) string {
	s = strings.ReplaceAll(s, h.dir, "<DIR>")
	s = regexp.MustCompile(`U[A-Z2-7]{55}`).ReplaceAllString(s, "<USERKEY>")
	s = regexp.MustCompile(`"account": "A[A-Z2-7]{55}"`).ReplaceAllString(s, `"account": "<ACCOUNT>"`)
	s = regexp.MustCompile(`"(created|issued_at)": "[^"]*"`).ReplaceAllString(s, `"$1": "<TIME>"`)
	return s
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *updateEndpointGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (run with -update-endpoint after checking the diff)\n got: %s\nwant: %s", golden, got, want)
	}
}

func TestEndpointDeleteRemovesRecordRenamesCredsRevokesKey(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	h.seed(ad("c"))
	_, user := h.credsClaims(ad("c"))
	h.seed(ad("d"))
	if so, _, code := h.run("endpoint", "delete", "o.g.d", "--yes", "--json", "--detail"); code != 0 || !strings.Contains(so, `"deleted": true`) {
		t.Errorf("delete json %d %q", code, so)
	}
	out, errb, code := h.run("endpoint", "delete", "o.g.c", "--yes", "--detail")
	if code != 0 || !strings.HasPrefix(out, "Deleted endpoint o.g.c\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if out != "Deleted endpoint o.g.c\n" {
		t.Errorf("report: %q", out)
	}
	noSecrets(t, out, errb)
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	if _, err := os.Lstat(h.creds(ad("c"))); !os.IsNotExist(err) {
		t.Errorf("creds still at the old name: %v", err)
	}
	if _, err := os.Lstat(h.creds(ad("c")) + ".deleted"); err != nil {
		t.Errorf("renamed file: %v", err)
	}
	rec, found, err := h.adminOf().IssuedByKey(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if found && rec.RevokedAt == "" {
		t.Errorf("key not marked revoked: %+v", rec)
	}
}

func TestEndpointDeleteAcceptsDotted(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	h.seed(ad("b"))
	for _, ref := range []string{"o.g.a", "o.g.b"} {
		if out, errb, code := h.run("endpoint", "delete", ref, "--yes"); code != 0 {
			t.Fatalf("%s: %d %q %q", ref, code, out, errb)
		}
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
}

func TestEndpointDeleteDryRunChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	before, _ := os.ReadFile(h.creds(ad("c")))
	out, errb, code := h.run("endpoint", "delete", "o.g.c", "--dry-run")
	if code != 0 || out != "Would delete endpoint o.g.c and its empty inbox\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if r := h.records(); len(r) != 1 {
		t.Errorf("records %v", r)
	}
	after, err := os.ReadFile(h.creds(ad("c")))
	if err != nil || string(after) != string(before) {
		t.Errorf("creds file changed: %v", err)
	}
	if names := dirNames(t, h.dir); len(names) != 3 { // creds, keys, and what init made
		for _, n := range names {
			if strings.Contains(n, "retired") {
				t.Errorf("renamed file made: %v", names)
			}
		}
	}
	_, user := h.credsClaims(ad("c"))
	if rec, found, _ := h.adminOf().IssuedByKey(t.Context(), user); found && rec.RevokedAt != "" {
		t.Error("key revoked by a dry run")
	}
}

func TestEndpointDeleteMissingIsRefused(t *testing.T) {
	h := newFleetEnv(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.none", "--yes")
	if code != 1 || out != "" || errb != "error: no endpoint o.g.none\nhint: mashctl endpoint list\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, errb, code = h.run("endpoint", "delete", "o.g.none", "--yes", "--json")
	if code != 1 || errb != "" || out != "{\"error\":\"no endpoint o.g.none\",\"hint\":\"mashctl endpoint list\"}\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestEndpointDeleteRefusesAsRetireDoes(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	// a record whose user key is replaced between the read and the removal is refused, as retire refuses it
	_, retErr, retCode := h.run("endpoint", "delete", "o.g.zzz", "--yes")
	_, delErr, delCode := h.run("endpoint", "delete", "o.g.zzz", "--yes")
	if retCode != 1 || delCode != 1 {
		t.Fatalf("codes %d %d", retCode, delCode)
	}
	if !strings.Contains(retErr, "no endpoint o.g.zzz") || !strings.Contains(delErr, "no endpoint o.g.zzz") {
		t.Errorf("%q %q", retErr, delErr)
	}
	if r := h.records(); len(r) != 1 {
		t.Errorf("records %v", r)
	}
}

func TestEndpointVerbsUsage(t *testing.T) {
	h := newFleetEnv(t)
	for _, verb := range []string{"delete", "list", "show"} {
		for _, flag := range []string{"--help", "-h"} {
			out, errb, code := h.run("endpoint", verb, flag)
			if code != 0 || !strings.Contains(out, "Example:") || !strings.Contains(out, "Usage: mashctl endpoint "+verb) || errb != "" {
				t.Errorf("%s %s: %d %q %q", verb, flag, code, out, errb)
			}
		}
		_, errb, code := h.run("endpoint", verb, "--bogus")
		if code != 2 || errb != "error: unknown flag --bogus\n" {
			t.Errorf("%s --bogus: %d %q", verb, code, errb)
		}
	}
	// help comes before any config load
	var empty = map[string]string{}
	h.env, empty = empty, h.env
	if _, _, code := h.run("endpoint", "delete", "--help"); code != 0 {
		t.Errorf("help with no config: %d", code)
	}
	h.env = empty
}

func TestEndpointBadAddressExits2WithoutEcho(t *testing.T) {
	h := newFleetEnv(t)
	for _, verb := range []string{"delete", "show"} {
		out, errb, code := h.run("endpoint", verb, "Not A Valid/Addr!SECRETVALUE")
		if code != 2 || out != "" || strings.Contains(errb, "SECRETVALUE") {
			t.Errorf("%s: %d %q %q", verb, code, out, errb)
		}
	}
	if _, _, code := h.run("endpoint", "delete"); code != 2 {
		t.Errorf("no address: %d", code)
	}
}

func TestEndpointListZeroAndTwo(t *testing.T) {
	h := newFleetEnv(t)
	out, errb, code := h.run("endpoint", "list")
	if code != 0 || out != "No endpoints.\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, _, code = h.run("endpoint", "list", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var z struct{ Endpoints []any }
	if err := json.Unmarshal([]byte(out), &z); err != nil || z.Endpoints == nil || len(z.Endpoints) != 0 {
		t.Errorf("zero: %q %v", out, err)
	}
	h.seed(ad("b"))
	h.seed(ad("a"))
	out, errb, code = h.run("endpoint", "list")
	if code != 0 || out != "OWNER  GROUP  NAME  ADDRESS  STATE\no      g      a     o.g.a    offline\no      g      b     o.g.b    offline\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
}

func TestEndpointShowFoundAndNotFound(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	out, errb, code := h.run("endpoint", "show", "o.g.c")
	if code != 0 || !strings.HasPrefix(out, "address:    o.g.c\nstate:      offline\n") || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	out, errb, code = h.run("endpoint", "show", "o.g.nope")
	if code != 1 || out != "" || !strings.Contains(errb, "hint: mashctl endpoint list") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// the slash form is not an address of this command
	if out, _, code = h.run("endpoint", "show", "o/g/c"); code != 2 || out != "" {
		t.Fatalf("slash form: %d %q", code, out)
	}
}

func TestEndpointGoldens(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	h.seed(ad("a"))
	h.seed(ad("b"))

	out, errb, code := h.run("endpoint", "list", "--json")
	if code != 0 {
		t.Fatalf("list: %d %q", code, errb)
	}
	checkGolden(t, "endpoint_list.golden.json", h.scrub(out))

	out, errb, code = h.run("endpoint", "show", "o.g.a", "--json")
	if code != 0 {
		t.Fatalf("show: %d %q", code, errb)
	}
	checkGolden(t, "endpoint_show.golden.json", h.scrub(out))

	out, errb, code = h.run("endpoint", "delete", "o.g.a", "--yes", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("plan: %d %q", code, errb)
	}
	checkGolden(t, "endpoint_delete_planned.golden.json", h.scrub(out))

	out, errb, code = h.run("endpoint", "delete", "o.g.a", "--yes", "--json")
	if code != 0 {
		t.Fatalf("delete: %d %q", code, errb)
	}
	checkGolden(t, "endpoint_delete.golden.json", h.scrub(out))

	cliframe.IsTerminal = func(io.Writer) bool { return false } // the error object's golden is the compact form of a pipe
	out, errb, code = h.run("endpoint", "delete", "o.g.a", "--yes", "--json")
	if code != 1 || errb != "" {
		t.Fatalf("delete again: %d %q %q", code, out, errb)
	}
	checkGolden(t, "endpoint_delete_not_found.golden.json", h.scrub(out))
}
