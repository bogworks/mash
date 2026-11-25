package main

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// stateOf is the state word `endpoint show` prints for o.g.<name>.
func (h *fleetEnv) stateOf(name string) string {
	h.t.Helper()
	out, errb, code := h.run("endpoint", "show", ad(name).String(), "--json")
	var j struct{ Online, Suspended bool }
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil {
		h.t.Fatalf("show %s: %d %q %q", name, code, out, errb)
	}
	switch { // the word the text shows: suspended wins over online
	case j.Suspended:
		return "suspended"
	case j.Online:
		return "online"
	}
	return "offline"
}

// holdLease writes the lease key a running session holds for o.g.<name>.
func (h *fleetEnv) holdLease(name string) {
	h.t.Helper()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := kv.Create(context.Background(), ad(name).Flat(), []byte(`{"host":"h","pid":"1"}`)); err != nil {
		h.t.Fatal(err)
	}
}

func TestEndpointSuspendResume(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	if got := h.stateOf("c"); got != "offline" {
		t.Fatalf("state before: %q", got)
	}
	out, errb, code := h.run("endpoint", "suspend", "o.g.c")
	if code != 0 || out != "Suspended endpoint o.g.c\n" || errb != "" {
		t.Fatalf("suspend: %d %q %q", code, out, errb)
	}
	if got := h.stateOf("c"); got != "suspended" {
		t.Fatalf("state after suspend: %q", got)
	}
	// the endpoint's inbox stream is parked (ADR 0057), and only that
	if st, err := h.adminOf().Suspended(context.Background(), ad("c")); err != nil || !st.Suspended || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(st.ID) {
		t.Fatalf("the inbox is not parked: %+v %v", st, err)
	}
	// suspend again: the same answer
	if out, _, code := h.run("endpoint", "suspend", "o.g.c"); code != 0 || out != "Suspended endpoint o.g.c\n" {
		t.Fatalf("suspend again: %d %q", code, out)
	}
	list, _, _ := h.run("endpoint", "list")
	if list != "OWNER  GROUP  NAME  ADDRESS  STATE\no      g      c     o.g.c    suspended\n" {
		t.Errorf("list: %q", list)
	}
	if out, errb, code := h.run("endpoint", "resume", "o.g.c"); code != 0 || out != "Resumed endpoint o.g.c\n" || errb != "" {
		t.Fatalf("resume: %d %q %q", code, out, errb)
	}
	if got := h.stateOf("c"); got != "offline" {
		t.Fatalf("state after resume: %q", got)
	}
	if st, err := h.adminOf().Suspended(context.Background(), ad("c")); err != nil || st.Suspended {
		t.Fatalf("the inbox is still parked: %+v %v", st, err)
	}
	// resume of an endpoint that is not suspended: the same answer
	if out, _, code := h.run("endpoint", "resume", "o.g.c"); code != 0 || out != "Resumed endpoint o.g.c\n" {
		t.Fatalf("resume again: %d %q", code, out)
	}
}

func TestEndpointStateOnline(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	h.seed(ad("c"))
	h.holdLease("c")
	if got := h.stateOf("c"); got != "online" {
		t.Fatalf("state with a lease: %q", got)
	}
	// suspended wins over online, and resume reports the state the endpoint is in
	if out, _, code := h.run("endpoint", "suspend", "o.g.c", "--json"); code != 0 || out != "{\n  \"address\": \"o.g.c\",\n  \"state\": \"suspended\"\n}\n" {
		t.Fatalf("suspend --json: %d %q", code, out)
	}
	if out, _, code := h.run("endpoint", "resume", "o.g.c", "--json"); code != 0 || out != "{\n  \"address\": \"o.g.c\",\n  \"state\": \"online\"\n}\n" {
		t.Fatalf("resume --json: %d %q", code, out)
	}
}

func TestEndpointShowSuspended(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	if _, _, code := h.run("endpoint", "suspend", "o.g.c"); code != 0 {
		t.Fatal(code)
	}
	out, errb, code := h.run("endpoint", "show", "o.g.c")
	if code != 0 || errb != "" || !strings.HasPrefix(out, "address:    o.g.c\nstate:      suspended\ncreated:    ") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestEndpointSuspendTakesNoDryRun(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	for _, verb := range []string{"suspend", "resume"} {
		out, errb, code := h.run("endpoint", verb, "o.g.c", "--dry-run")
		want := "error: unknown flag --dry-run\n"
		if code != 2 || out != "" || errb != want {
			t.Errorf("%s: %d %q %q", verb, code, out, errb)
		}
	}
	if h.stateOf("c") == "suspended" {
		t.Fatal("a refused run suspended the endpoint")
	}
}

func TestEndpointSuspendRefusals(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("c"))
	for _, verb := range []string{"suspend", "resume"} {
		// Any error is 1, never 3.
		if out, errb, code := h.run("endpoint", verb, "o.g.ghost"); code != 1 || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.Contains(errb, "no inbox stream") {
			t.Errorf("%s ghost: %d %q %q", verb, code, out, errb)
		}
		if out, errb, code := h.run("endpoint", verb); code != 2 || out != "" || !strings.HasPrefix(errb, "error: missing the endpoint address") || !strings.Contains(errb, "hint: mashctl endpoint "+verb+" --help") {
			t.Errorf("%s usage: %d %q %q", verb, code, out, errb)
		}
		// a flat key, a slash form and a wrong shape are refused with the form to give
		for _, arg := range []string{"o_g_c", "o/g/c", "O.g.c", seedShaped} {
			out, errb, code := h.run("endpoint", verb, arg)
			if code != 2 || out != "" || !strings.Contains(errb, "write owner.group.name") || strings.Contains(errb, seedShaped) {
				t.Errorf("%s %q: %d %q %q", verb, arg, code, out, errb)
			}
		}
		if _, _, code := h.run("endpoint", verb, "o.g.c", "o.g.d"); code != 2 {
			t.Errorf("%s two addresses: %d", verb, code)
		}
	}
	if h.stateOf("c") != "offline" {
		t.Fatal("a refused command changed the endpoint")
	}
}

func TestEndpointPermissions(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	// no server and no config: it reads nothing
	h.env = map[string]string{"HOME": t.TempDir()}
	out, errb, code := h.run("endpoint", "permissions", "acme.demo.bob")
	if code != 0 || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	a, _ := sap.NewAddress("acme", "demo", "bob")
	var wb bytes.Buffer
	enc := json.NewEncoder(&wb) // < > & stay readable, as in every --json object
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(permissionsJSON(admin.Permissions(a)))
	want := wb.Bytes()
	if out != string(want) {
		t.Fatalf("permissions: %q, want %q", out, want)
	}
	var p map[string]map[string][]string
	if err := json.Unmarshal([]byte(out), &p); err != nil || len(p) != 2 || len(p["pub"]["allow"]) == 0 || len(p["sub"]["allow"]) == 0 {
		t.Fatalf("not the permissions object: %q", out)
	}
	real := admin.Permissions(a)
	if !slices.Equal(p["pub"]["allow"], real.Pub.Allow) || !slices.Equal(p["sub"]["allow"], real.Sub.Allow) {
		t.Errorf("allow lists differ from the code's permissions: %q", out)
	}
	for _, dir := range []string{"pub", "sub"} {
		if d, ok := p[dir]["deny"]; !ok || d == nil || len(d) != 0 {
			t.Errorf("%s deny is %v: want present and empty", dir, d)
		}
	}
	if !strings.Contains(out, `"deny": []`) {
		t.Errorf("deny is not printed as []: %q", out)
	}
	if o2, _, code := h.run("endpoint", "permissions", "acme.demo.bob", "--json"); code != 0 || o2 != out {
		t.Errorf("--json: %d %q", code, o2)
	}
	// the --nsc form is gone
	if out, errb, code := h.run("endpoint", "permissions", "acme.demo.bob", "--nsc"); code != 2 || out != "" || !strings.Contains(errb, `unknown flag --nsc`) {
		t.Errorf("--nsc: %d %q %q", code, out, errb)
	}
	for _, arg := range []string{"bob", "acme_demo_bob", seedShaped} {
		if out, errb, code := h.run("endpoint", "permissions", arg); code != 2 || out != "" || strings.Contains(errb, seedShaped) || !strings.Contains(errb, "write owner.group.name") {
			t.Errorf("%q: %d %q %q", arg, code, out, errb)
		}
	}
}

func TestVersionOutput(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	h.env = map[string]string{}
	commit = "3f9a41c8d2e07b6a5c4d3e2f1a0b9c8d7e6f5a4b"
	t.Cleanup(func() { commit = "" })
	out, errb, code := h.run("version")
	re := regexp.MustCompile(`^mashctl v\S+ \(3f9a41c\)\n$`)
	if code != 0 || errb != "" || !re.MatchString(out) {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, errb, code = h.run("version", "--json")
	var j struct{ Version, Commit string }
	if code != 0 || errb != "" || json.Unmarshal([]byte(out), &j) != nil || j.Commit != "3f9a41c8d2e07b6a5c4d3e2f1a0b9c8d7e6f5a4b" || j.Version == "" || strings.HasPrefix(j.Version, "v") {
		t.Fatalf("--json: %d %q %q", code, out, errb)
	}
	// a build with nothing stamped
	commit = ""
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = old })
	if out, errb, code := h.run("version"); code != 0 || errb != "" || out != "mashctl v0.0.0-dev (unknown)\n" {
		t.Errorf("unstamped: %d %q %q", code, out, errb)
	}
	if out, errb, code := h.run("version", "--json"); code != 0 || errb != "" || !strings.Contains(out, `"commit": "unknown"`) {
		t.Errorf("unstamped --json: %d %q %q", code, out, errb)
	}
	if shortCommit("") != "unknown" || shortCommit("abc") != "abc" || shortCommit("3f9a41c8d2e0+dirty") != "3f9a41c+dirty" {
		t.Error("shortCommit")
	}
	if out, errb, code := h.run("version", "--bogus"); code != 2 || out != "" || !strings.HasPrefix(errb, "error: unknown flag") {
		t.Errorf("--bogus: %d %q %q", code, out, errb)
	}
	if out, _, code := h.run("version", "--help"); code != 0 || !strings.HasPrefix(out, "usage: mashctl version") {
		t.Errorf("--help: %d %q", code, out)
	}
}

// the endpoint list reads the state through the same admin call as show
func TestEndpointStateOfMissingInbox(t *testing.T) {
	e := testenv.Start(t)
	a, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.EndpointState(context.Background(), ad("nobody"), true)
	if err != nil || st.Online || st.Suspended || st.Waiting != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}
