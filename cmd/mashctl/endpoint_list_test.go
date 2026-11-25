package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
)

func addr3(owner, group, name string) sap.Address {
	return sap.Address{Owner: owner, Group: group, Instance: name}
}

// seedTree is the endpoints acme.demo.alice, acme.demo.bob, acme.ops.carol and zed.demo.dave.
func seedTree(h *fleetEnv) {
	h.seedMeta(addr3("acme", "demo", "alice"), admin.Meta{Labels: map[string]string{"team": "blue"}}, nil)
	h.seed(addr3("acme", "demo", "bob"))
	h.seed(addr3("acme", "ops", "carol"))
	h.seed(addr3("zed", "demo", "dave"))
}

func TestEndpointListPrintsOwnerAndGroupOnEveryRow(t *testing.T) {
	h := newFleetEnv(t)
	seedTree(h)
	if _, _, code := h.run("endpoint", "suspend", "acme.ops.carol"); code != 0 {
		t.Fatal(code)
	}
	out, errb, code := h.run("endpoint", "list")
	want := "OWNER  GROUP  NAME   ADDRESS          STATE\n" +
		"acme   demo   alice  acme.demo.alice  offline\n" +
		"acme   demo   bob    acme.demo.bob    offline\n" +
		"acme   ops    carol  acme.ops.carol   suspended\n" +
		"zed    demo   dave   zed.demo.dave    offline\n"
	if code != 0 || out != want || errb != "" {
		t.Fatalf("%d %q %q\nwant %q", code, out, errb, want)
	}
	noSecrets(t, out, errb)
}

func TestEndpointListPrefix(t *testing.T) {
	h := newFleetEnv(t)
	seedTree(h)
	cases := []struct{ prefix, want string }{
		{"acme", "OWNER  GROUP  NAME   ADDRESS          STATE\nacme   demo   alice  acme.demo.alice  offline\nacme   demo   bob    acme.demo.bob    offline\nacme   ops    carol  acme.ops.carol   offline\n"},
		{"acme.demo", "OWNER  GROUP  NAME   ADDRESS          STATE\nacme   demo   alice  acme.demo.alice  offline\nacme   demo   bob    acme.demo.bob    offline\n"},
		{"acme.ops", "OWNER  GROUP  NAME   ADDRESS         STATE\nacme   ops    carol  acme.ops.carol  offline\n"},
		{"zed", "OWNER  GROUP  NAME  ADDRESS        STATE\nzed    demo   dave  zed.demo.dave  offline\n"},
		{"nobody", "No endpoints.\n"},
		{"acme.nothing", "No endpoints.\n"},
		{"ac", "No endpoints.\n"}, // a prefix is a whole owner, not the start of a word
	}
	for _, c := range cases {
		out, errb, code := h.run("endpoint", "list", c.prefix)
		if code != 0 || out != c.want || errb != "" {
			t.Errorf("list %s: %d %q %q\nwant %q", c.prefix, code, out, errb, c.want)
		}
	}
	// the JSON is filtered the same way
	out, _, code := h.run("endpoint", "list", "acme.demo", "--json")
	var j struct{ Endpoints []map[string]any }
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil || len(j.Endpoints) != 2 || j.Endpoints[0]["address"] != "acme.demo.alice" {
		t.Fatalf("%d %q", code, out)
	}
	for _, bad := range []string{"acme.demo.alice", "Acme", "a/b", "acme..demo", "."} {
		if out, errb, code := h.run("endpoint", "list", bad); code != 2 || out != "" || !strings.Contains(errb, "write acme or acme.demo") {
			t.Errorf("list %q: %d %q %q", bad, code, out, errb)
		}
	}
}

func TestEndpointListWide(t *testing.T) {
	h := newFleetEnv(t)
	seedTree(h)
	// two messages waiting for bob
	js, err := jetstream.New(h.e.Conn(t, h.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := js.Publish(context.Background(), addr3("acme", "demo", "bob").Subjects().Mail, []byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	out, errb, code := h.run("endpoint", "list", "acme.demo", "--wide")
	if code != 0 || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	re := regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\d{4}-\d\d-\d\d \d\d:\d\d)\s+(\d+)\s+(\S+)$`)
	if len(lines) != 3 || lines[0] != "OWNER  GROUP  ADDRESS          STATE    CREATED           WAITING  LABELS" {
		t.Fatalf("%q", out)
	}
	alice, bob := re.FindStringSubmatch(lines[1]), re.FindStringSubmatch(lines[2])
	if alice == nil || bob == nil {
		t.Fatalf("rows do not match %s:\n%s", re, out)
	}
	if alice[3] != "acme.demo.alice" || alice[6] != "0" || alice[7] != "team=blue" || bob[1] != "acme" || bob[2] != "demo" || bob[3] != "acme.demo.bob" || bob[6] != "2" || bob[7] != "-" {
		t.Errorf("alice %q bob %q", alice, bob)
	}
	if !strings.HasPrefix(lines[2], "acme   demo   acme.demo.bob") {
		t.Errorf("owner and group are printed on every row in --wide too: %q", lines[2])
	}
	// without --wide the waiting count is not read, and the columns are the four
	if out, _, _ := h.run("endpoint", "list", "acme.demo"); strings.Contains(out, "CREATED") || strings.Contains(out, "WAITING") {
		t.Errorf("plain list has wide columns: %q", out)
	}
}

func TestEndpointListEmptyAndJSON(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	h := newFleetEnv(t)
	out, errb, code := h.run("endpoint", "list")
	if code != 0 || out != "No endpoints.\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, _, code = h.run("endpoint", "list", "--json")
	if code != 0 || out != "{\n  \"endpoints\": []\n}\n" {
		t.Fatalf("%d %q", code, out)
	}
	h.seed(addr3("acme", "demo", "alice"))
	out, _, code = h.run("endpoint", "list", "--json")
	want := "{\n  \"endpoints\": [\n    {\n      \"address\": \"acme.demo.alice\",\n      \"owner\": \"acme\",\n      \"group\": \"demo\",\n      \"name\": \"alice\",\n      \"online\": false,\n      \"suspended\": false\n    }\n  ]\n}\n"
	if code != 0 || out != want {
		t.Fatalf("%d %q", code, out)
	}
}

func TestEndpointShowFields(t *testing.T) {
	h := newFleetEnv(t)
	a := addr3("acme", "demo", "bob")
	h.seedMeta(a, admin.Meta{Labels: map[string]string{"team": "blue", "env": "dev"}}, nil)
	rec, _ := h.record(a)
	out, errb, code := h.run("endpoint", "show", "acme.demo.bob")
	re := regexp.MustCompile(`^address:    acme\.demo\.bob\nstate:      offline\ncreated:    \d{4}-\d\d-\d\d \d\d:\d\d UTC\nwaiting:    0 messages\nlabels:     env=dev,team=blue\npublic key: ` + rec.User + `\n$`)
	if code != 0 || errb != "" || !re.MatchString(out) {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// optional fields show only when set
	if _, _, code := h.run("endpoint", "set", "acme.demo.bob", "role=reviewer", "host=laptop", "description=checks diffs", "capability=review"); code != 0 {
		t.Fatal(code)
	}
	out, _, _ = h.run("endpoint", "show", "acme.demo.bob")
	for _, want := range []string{"\nrole:        reviewer\n", "\nhost:        laptop\n", "\ndescription: checks diffs\n", "\ncapability:  review\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	// the JSON
	out, _, code = h.run("endpoint", "show", "acme.demo.bob", "--json")
	var j struct {
		Address   string            `json:"address"`
		Online    bool              `json:"online"`
		Suspended bool              `json:"suspended"`
		Created   string            `json:"created"`
		Waiting   int               `json:"waiting"`
		Labels    map[string]string `json:"labels"`
		PublicKey string            `json:"public_key"`
		Role      string            `json:"role"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil || j.Address != "acme.demo.bob" || j.Online || j.Suspended || j.Waiting != 0 || j.Labels["team"] != "blue" ||
		j.PublicKey != rec.User || j.Role != "reviewer" || !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(j.Created) {
		t.Fatalf("%d %q", code, out)
	}
	// the endpoint is not there
	if out, errb, code := h.run("endpoint", "show", "acme.demo.nobody"); code != 1 || out != "" || errb != "error: no endpoint acme.demo.nobody\nhint: mashctl endpoint list\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestEndpointShowWaiting(t *testing.T) {
	h := newFleetEnv(t)
	a := addr3("acme", "demo", "bob")
	h.seed(a)
	js, err := jetstream.New(h.e.Conn(t, h.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.Publish(context.Background(), a.Subjects().Mail, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	out, _, code := h.run("endpoint", "show", "acme.demo.bob")
	if code != 0 || !strings.Contains(out, "\nwaiting:    1 message\n") {
		t.Fatalf("%d %q", code, out)
	}
}
