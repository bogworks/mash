package cmds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

type fakeDir struct {
	entries     []cmds.WhoEntry
	notVerified int
	err         error
	calls       int
}

func (f *fakeDir) Who(context.Context) ([]cmds.WhoEntry, int, error) {
	f.calls++
	return f.entries, f.notVerified, f.err
}

// directory is the one the specification's examples show, in an order that is not sorted.
func directory() *fakeDir {
	return &fakeDir{entries: []cmds.WhoEntry{
		{Address: "acme.demo.bob", Online: false, Role: "writer", Host: "myserver"},
		{Address: "acme.demo.alice", Online: true, Role: "reviewer", Host: "myserver", Description: "reviews pull requests",
			Capabilities: []string{"review"}, Labels: map[string]string{"team": "blue"}},
		{Address: "acme.demo.bobby", Online: true},
		{Address: "acme.lab.carol", Online: true},
		{Address: "acmeco.demo.dan", Online: false},
		{Address: "zed.demo.erin", Online: false},
	}}
}

func whoEnv(svc any) cmdcore.EnvFunc {
	return func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: svc}, nil }
}

func runWho(svc any, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Who, whoEnv(svc))(args, strings.NewReader(""), &o, &e, g)
	return code, o.String(), e.String()
}

func addresses(t *testing.T, out string) []string {
	t.Helper()
	var o struct{ Endpoints []struct{ Address string } }
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	var a []string
	for _, e := range o.Endpoints {
		a = append(a, e.Address)
	}
	return a
}

func TestWhoTable(t *testing.T) {
	f := &fakeDir{entries: directory().entries[:2]}
	code, out, errs := runWho(f, cliframe.Global{})
	want := "ADDRESS          STATE\nacme.demo.alice  online\nacme.demo.bob    offline\n"
	if code != 0 || out != want || errs != "" {
		t.Errorf("table: %d %q %q", code, out, errs)
	}
	code, out, _ = runWho(f, cliframe.Global{}, "--wide")
	want = "ADDRESS          STATE    ROLE      HOST      DESCRIPTION\n" +
		"acme.demo.alice  online   reviewer  myserver  reviews pull requests\n" +
		"acme.demo.bob    offline  writer    myserver  -\n"
	if code != 0 || out != want {
		t.Errorf("wide: %d %q", code, out)
	}
	// nothing recorded: - in every wide column
	_, out, _ = runWho(&fakeDir{entries: []cmds.WhoEntry{{Address: "a.b.c"}}}, cliframe.Global{}, "--wide")
	if out != "ADDRESS  STATE    ROLE  HOST  DESCRIPTION\na.b.c    offline  -     -     -\n" {
		t.Errorf("unrecorded: %q", out)
	}
}

func TestWhoEmptyTexts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"acme.lab.zed"}, "No endpoints under acme.lab.zed\n"},
		{[]string{"nobody"}, "No endpoints under nobody\n"},
		{[]string{"acme.*"}, ""}, // acme has endpoints
		{[]string{"nobody.*"}, "No endpoints under nobody\n"},
		{[]string{"--online", "zed"}, "No online endpoints under zed\n"},
		{[]string{"--offline", "acme.lab"}, "No offline endpoints under acme.lab\n"},
	} {
		_, out, _ := runWho(directory(), cliframe.Global{}, tc.args...)
		if tc.want == "" {
			if strings.HasPrefix(out, "No ") {
				t.Errorf("%v: %q", tc.args, out)
			}
			continue
		}
		if out != tc.want {
			t.Errorf("%v: %q, want %q", tc.args, out, tc.want)
		}
	}
	for _, tc := range []struct {
		dir  []cmds.WhoEntry
		args []string
		want string
	}{
		{nil, nil, "No endpoints\n"},
		{directory().entries[1:2], []string{"--offline"}, "No offline endpoints\n"},
		{directory().entries[:1], []string{"--online"}, "No online endpoints\n"},
	} {
		_, out, _ := runWho(&fakeDir{entries: tc.dir}, cliframe.Global{}, tc.args...)
		if out != tc.want {
			t.Errorf("%v: %q, want %q", tc.args, out, tc.want)
		}
	}
	// --json of an empty result is the object with an empty list
	if _, out, _ := runWho(&fakeDir{}, cliframe.Global{JSON: true}); out != `{"endpoints":[],"answers_not_verified":0}`+"\n" {
		t.Errorf("empty json: %q", out)
	}
}

func TestWhoPrefixes(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"", []string{"acme.demo.alice", "acme.demo.bob", "acme.demo.bobby", "acme.lab.carol", "acmeco.demo.dan", "zed.demo.erin"}},
		{"acme", []string{"acme.demo.alice", "acme.demo.bob", "acme.demo.bobby", "acme.lab.carol"}}, // never acmeco
		{"acme.*", []string{"acme.demo.alice", "acme.demo.bob", "acme.demo.bobby", "acme.lab.carol"}},
		{"acme.demo", []string{"acme.demo.alice", "acme.demo.bob", "acme.demo.bobby"}},
		{"acme.demo.*", []string{"acme.demo.alice", "acme.demo.bob", "acme.demo.bobby"}},
		{"acme.demo.bob", []string{"acme.demo.bob"}}, // never acme.demo.bobby
		{"acme.demo.bobby", []string{"acme.demo.bobby"}},
		{"acme.de", nil},
		{"acme.demo.bo", nil},
		{"ac", nil},
	} {
		args := []string{}
		if tc.prefix != "" {
			args = append(args, tc.prefix)
		}
		code, out, errs := runWho(directory(), cliframe.Global{JSON: true}, args...)
		if code != 0 || errs != "" || !reflect.DeepEqual(addresses(t, out), tc.want) {
			t.Errorf("prefix %q: %d %v %q", tc.prefix, code, addresses(t, out), errs)
		}
	}
	// --online / --offline
	_, out, _ := runWho(directory(), cliframe.Global{JSON: true}, "acme.demo", "--online")
	if got := addresses(t, out); !reflect.DeepEqual(got, []string{"acme.demo.alice", "acme.demo.bobby"}) {
		t.Errorf("--online: %v", got)
	}
	_, out, _ = runWho(directory(), cliframe.Global{JSON: true}, "--offline")
	if got := addresses(t, out); !reflect.DeepEqual(got, []string{"acme.demo.bob", "acmeco.demo.dan", "zed.demo.erin"}) {
		t.Errorf("--offline: %v", got)
	}
}

func TestWhoUsageErrors(t *testing.T) {
	hint := "hint: mash who --help\n"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"acme_demo_bob"}, "error: acme_demo_bob is not an owner, an owner.group or an address\n"},
		{[]string{"acme/demo"}, "error: acme/demo is not an owner, an owner.group or an address\n"},
		{[]string{"Acme.demo"}, "error: Acme.demo is not an owner, an owner.group or an address\n"},
		{[]string{"acme.demo.bob.x"}, "error: acme.demo.bob.x is not an owner, an owner.group or an address\n"},
		{[]string{"acme.demo.bob.*"}, "error: acme.demo.bob.* is not an owner, an owner.group or an address\n"},
		{[]string{"acme.d*"}, "error: acme.d* is not an owner, an owner.group or an address\n"},
		{[]string{"*"}, "error: * is not an owner, an owner.group or an address\n"},
		{[]string{"acme..demo"}, "error: acme..demo is not an owner, an owner.group or an address\n"},
		{[]string{"acme", "demo"}, "error: unexpected argument \"demo\" for mash who\n"},
		{[]string{"--online", "--offline"}, "error: mash who takes --online or --offline, not both\n"},
		{[]string{"--wide=maybe"}, "error: --wide must be true or false\n"},
	} {
		f := directory()
		code, out, errs := runWho(f, cliframe.Global{}, tc.args...)
		if code != 2 || out != "" || errs != tc.want+hint || f.calls != 0 {
			t.Errorf("%v: %d %q %q calls %d", tc.args, code, out, errs, f.calls)
		}
	}
	for _, flag := range []string{"--match", "--long"} { // an unknown flag: the same form as before the verb, no hint (ADR 0168)
		f := directory()
		code, out, errs := runWho(f, cliframe.Global{}, flag, "x")
		if code != 2 || out != "" || errs != "error: unknown flag "+flag+"\n" || f.calls != 0 {
			t.Errorf("%s: %d %q %q calls %d", flag, code, out, errs, f.calls)
		}
	}
	code, out, errs := runWho(directory(), cliframe.Global{JSON: true}, "acme_demo")
	if code != 2 || errs != "" || out != `{"error":"acme_demo is not an owner, an owner.group or an address","hint":"mash who --help"}`+"\n" {
		t.Errorf("json: %d %q %q", code, out, errs)
	}
	// a failure of the service is exit 1 with its own words
	code, _, errs = runWho(&fakeDir{err: errors.New("boom")}, cliframe.Global{})
	if code != 1 || !strings.Contains(errs, "boom") {
		t.Errorf("service failure: %d %q", code, errs)
	}
}

func TestParsePrefix(t *testing.T) {
	for in, want := range map[string]string{"acme": "acme", "acme.demo": "acme.demo", "acme.demo.bob": "acme.demo.bob", "acme.*": "acme", "acme.demo.*": "acme.demo", "a-b.c1": "a-b.c1"} {
		if got, err := cmdcore.ParsePrefix(in, "h"); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "*", ".*", "acme.", ".acme", "acme.demo.bob.*", "acme.*.bob", "1acme", "acme_demo", "acme/demo", "ACME", "acme.demo.bob.x", "acme-", "a--b"} {
		if _, err := cmdcore.ParsePrefix(in, "h"); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	_, err := cmdcore.ParsePrefix("acme_demo", "mash who --help")
	var ce *cliframe.Error
	if !errors.As(err, &ce) || ce.Msg != "acme_demo is not an owner, an owner.group or an address" || ce.Hint != "mash who --help" || ce.Code != 2 {
		t.Errorf("error %#v", err)
	}
}

// The doors: --json is structuredContent, and the MCP text is the CLI's stdout, for every case of the parameters.
func TestWhoDoorsAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		args string
	}{
		{"all", nil, `{}`},
		{"wide", []string{"--wide"}, `{"wide":true}`},
		{"prefix", []string{"acme.demo"}, `{"prefix":"acme.demo"}`},
		{"star", []string{"acme.*"}, `{"prefix":"acme.*"}`},
		{"full address", []string{"acme.demo.bob"}, `{"prefix":"acme.demo.bob"}`},
		{"online", []string{"--online", "--wide"}, `{"online":true,"wide":true}`},
		{"offline", []string{"--offline"}, `{"offline":true}`},
		{"nothing", []string{"acme.zzz"}, `{"prefix":"acme.zzz"}`},
		{"empty prefix", nil, `{"prefix":""}`},
		{"role", []string{"--role", "REVIEW"}, `{"role":"REVIEW"}`},
		{"capability", []string{"--capability", "review"}, `{"capability":["review"]}`},
		{"capability twice", []string{"--capability", "review", "--capability", "x"}, `{"capability":["review","x"]}`},
		{"label", []string{"--label", "team=blue", "--label", "team"}, `{"label":["team=blue","team"]}`},
		{"all", []string{"--online", "--role", "rev", "--capability", "review", "--label", "team=blue", "acme"}, `{"online":true,"role":"rev","capability":["review"],"label":["team=blue"],"prefix":"acme"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := directory()
			reg := cmdcore.NewRegistry(whoEnv(f), cmdcore.Register(cmds.Who))
			var jout, text, errs bytes.Buffer
			if code := reg.RunCLI("who", tc.argv, strings.NewReader(""), &jout, &errs, cliframe.Global{JSON: true}); code != 0 {
				t.Fatalf("cli json: %d %s", code, errs.String())
			}
			if code := reg.RunCLI("who", tc.argv, strings.NewReader(""), &text, &errs, cliframe.Global{}); code != 0 {
				t.Fatalf("cli text: %d %s", code, errs.String())
			}
			e, _ := reg.Lookup("who")
			mtext, structured, err := e.Tool(reg.Env).CallStructured(context.Background(), json.RawMessage(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := structured.(cmds.WhoResult); !ok {
				t.Fatalf("structured is %T, want the result object", structured)
			}
			b, _ := json.Marshal(structured)
			// byte for byte: the CLI's --json line is the marshaled structured result and a newline, nothing else
			if jout.String() != string(b)+"\n" || !strings.HasPrefix(string(b), `{"endpoints":[`) {
				t.Errorf("--json %s != structuredContent %s", jout.String(), b)
			}
			if mtext+"\n" != text.String() {
				t.Errorf("mcp text %q != cli stdout %q", mtext, text.String())
			}
		})
	}
	// the old array form is gone; the bad arguments are errors with {error, hint}
	tool := cmdcore.MCPTool(cmds.Who, whoEnv(directory()))
	for _, bad := range []string{`{"format":"json"}`, `{"match":"x"}`, `{"label":[3]}`, `{"label":""}`, `{"label":"a="}`, `{"capability":[""]}`, `{"online":true,"offline":true}`, `{"prefix":"acme_demo"}`, `{"prefix":3}`, `[]`} {
		_, _, err := tool.CallStructured(context.Background(), json.RawMessage(bad))
		var se interface{ Structured() any }
		if err == nil || !errors.As(err, &se) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	_, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"online":true,"offline":true}`))
	if err == nil || err.Error() != "error: mash who takes --online or --offline, not both\nhint: mash who --help" {
		t.Errorf("conflict: %v", err)
	}
}

// The descriptions, examples, tool description, parameters, object and every output case are the specification's.
// Every endpoint carries every field in both doors, whatever wide says: capability a list and labels an object (empty,
// not null, when there are none), role, host and description null when absent. The doors are byte for byte the same.
func TestWhoJSONCarriesEveryField(t *testing.T) {
	want := `{"endpoints":[` +
		`{"address":"acme.demo.alice","online":true,"role":"reviewer","host":"myserver","description":"reviews pull requests","capability":["review"],"labels":{"team":"blue"}},` +
		`{"address":"acme.demo.bob","online":false,"role":"writer","host":"myserver","description":null,"capability":[],"labels":{}}],"answers_not_verified":0}`
	f := &fakeDir{entries: directory().entries[:2]}
	reg := cmdcore.NewRegistry(whoEnv(f), cmdcore.Register(cmds.Who))
	e, _ := reg.Lookup("who")
	for _, wide := range []bool{false, true} {
		argv, args := []string(nil), `{}`
		if wide {
			argv, args = []string{"--wide"}, `{"wide":true}`
		}
		var jout, errs bytes.Buffer
		if code := reg.RunCLI("who", argv, strings.NewReader(""), &jout, &errs, cliframe.Global{JSON: true}); code != 0 {
			t.Fatalf("cli: %d %s", code, errs.String())
		}
		_, structured, err := e.Tool(reg.Env).CallStructured(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(structured)
		if jout.String() != want+"\n" || string(b) != want || jout.String() != string(b)+"\n" {
			t.Errorf("wide=%v: cli %q, tool %s, want %s", wide, jout.String(), b, want)
		}
	}
	// the text alone changes with wide
	_, plain, _ := runWho(f, cliframe.Global{})
	_, wideText, _ := runWho(f, cliframe.Global{}, "--wide")
	if strings.Contains(plain, "ROLE") || !strings.Contains(wideText, "ROLE") {
		t.Errorf("wide changes the text only: %q %q", plain, wideText)
	}
}

func TestWhoMatchesSpec(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var o, e bytes.Buffer
	cmdcore.CLI(cmds.Who, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
	help := o.String()
	c, meta := specCommand(t, "who")
	want := []string{c["description"].(string)}
	for _, a := range append(anyList(c["arguments"]), anyList(c["options"])...) {
		want = append(want, a["description"].(string))
	}
	for _, w := range want {
		if !strings.Contains(norm(help), norm(w)) {
			t.Errorf("who --help lacks the spec's text %q", w)
		}
	}
	for _, ex := range c["examples"].([]any) {
		if !strings.Contains(help, "\n  "+ex.(string)+"\n") {
			t.Errorf("who --help lacks the spec's example %q", ex)
		}
	}
	if got := cmdcore.MCPTool(cmds.Who, nil).Description; got != specTool(t, "who")["description"] {
		t.Errorf("tool description %q", got)
	}
	if cmds.Who.Group != meta["x-group"] {
		t.Errorf("group %q, spec %v", cmds.Who.Group, meta["x-group"])
	}
	// the MCP parameters: exactly the spec's, with the spec's descriptions and types (wide is the spec's like the rest)
	props := cmdcore.Schema[cmds.WhoParams]()["properties"].(map[string]any)
	var specNames []string
	for _, p := range specTool(t, "who")["params"].([]any) {
		pm := p.(map[string]any)
		specNames = append(specNames, pm["name"].(string))
		got, ok := props[pm["name"].(string)].(map[string]any)
		wantType := map[string]string{"string": "string", "boolean": "boolean", "array of string": "array"}[pm["type"].(string)]
		if !ok || got["description"] != pm["description"] || got["type"] != wantType {
			t.Errorf("param %v: schema %v", pm["name"], got)
		}
	}
	var names []string
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	sort.Strings(specNames)
	if !reflect.DeepEqual(names, specNames) {
		t.Errorf("schema properties %v, spec %v", names, specNames)
	}
	// the object: what the command returns for the spec's example endpoint is the spec's x-json, field for field
	full := cmds.WhoEntry{Address: "acme.demo.alice", Online: true, Role: "reviewer", Host: "myserver", Description: "reviews pull requests",
		Capabilities: []string{"review"}, Labels: map[string]string{"team": "blue"}}
	x := string(mustJSON(meta["x-json"]))
	_, got, _ := runWho(&fakeDir{entries: []cmds.WhoEntry{full}}, cliframe.Global{JSON: true})
	if !sameJSON(t, got, x) {
		t.Errorf("object %s, spec x-json %s", got, x)
	}
	if !sameJSON(t, string(mustJSON(specTool(t, "who")["structured"])), x) {
		t.Error("spec/mash.json x-json and spec/mash-mcp.json structured differ")
	}
	// the output cases, word for word
	alice := cmds.WhoEntry{Address: "acme.demo.alice", Online: true, Role: "reviewer", Host: "myserver", Description: "reviews pull requests"}
	bob := cmds.WhoEntry{Address: "acme.demo.bob", Role: "writer", Host: "myserver"}
	both := []cmds.WhoEntry{bob, alice}
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		var argv []string
		dir := both
		notVerified := 0
		switch oc["case"] {
		case "Endpoints":
			dir = []cmds.WhoEntry{{Address: bob.Address}, {Address: alice.Address, Online: true}}
		case "With --wide":
			argv = []string{"--wide"}
		case "Nothing under the prefix":
			argv = []string{"acme.lab"}
		case "None at all":
			dir = nil
		case "None online":
			argv, dir = []string{"--online"}, both[:1]
		case "None offline under the prefix":
			argv = []string{"--offline", "acme.lab"}
		case "A prefix that is not part of an address":
			argv = []string{"Acme.Demo"}
		case "--json":
			code, out, _ := runWho(&fakeDir{entries: []cmds.WhoEntry{
				{Address: alice.Address, Online: true, Role: "reviewer", Host: "myserver", Description: "reviews pull requests", Capabilities: []string{"review"},
					Labels: map[string]string{"team": "blue"}},
				{Address: bob.Address}}}, cliframe.Global{JSON: true})
			var b bytes.Buffer
			if err := json.Indent(&b, []byte(strings.TrimSpace(out)), "", "  "); err != nil || b.String() != oc["text"].(string) || code != 0 {
				t.Errorf("who output %q: exit %d %q, spec %q", oc["case"], code, b.String(), oc["text"])
			}
			continue
		case "Answers that fail verification, the table (TO-12)":
			dir = []cmds.WhoEntry{{Address: alice.Address, Online: true}, {Address: bob.Address, Online: true}, {Address: "acme.demo.carol"}}
			notVerified = 1
		case "Answers that fail verification, the warning (TO-12)":
			code, out, errs := runWho(&fakeDir{entries: both, notVerified: 1}, cliframe.Global{})
			if code != 0 || errs != oc["text"].(string)+"\n" || strings.Contains(out, "warning") {
				t.Errorf("who output %q: exit %d, stderr %q, spec %q", oc["case"], code, errs, oc["text"])
			}
			continue
		case "A deleted endpoint here":
			continue // a stderr failure: cmd/mash/deleted_here_test.go (TestEverydayCommandsDeletedHereMatchSpec) compares it
		default:
			t.Errorf("unknown spec case %v", oc["case"])
			continue
		}
		code, out, errs := runWho(&fakeDir{entries: dir, notVerified: notVerified}, cliframe.Global{}, argv...)
		text := out
		if oc["stream"] == "stderr" {
			text = errs
		}
		if text != oc["text"].(string)+"\n" || code != int(oc["exit"].(float64)) {
			t.Errorf("who output %q: exit %d %q, spec %q", oc["case"], code, text, oc["text"])
		}
	}
	mt := specTool(t, "who")["text"].([]any)[0].(map[string]any)["text"]
	_, out, _ := runWho(&fakeDir{entries: []cmds.WhoEntry{{Address: "acme.demo.alice", Online: true}, {Address: "acme.demo.bob"}}}, cliframe.Global{})
	if out != mt.(string)+"\n" {
		t.Errorf("mcp text case %q, got %q", mt, out)
	}
}

func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		t.Fatalf("not JSON: %s / %s", a, b)
	}
	return reflect.DeepEqual(x, y)
}

func whoAddrs(t *testing.T, svc any, argv ...string) []string {
	t.Helper()
	code, out, errs := runWho(svc, cliframe.Global{JSON: true}, argv...)
	if code != 0 {
		t.Fatalf("who %v: %d %s", argv, code, errs)
	}
	return addresses(t, out)
}

func filterDir() *fakeDir {
	return &fakeDir{entries: []cmds.WhoEntry{
		{Address: "acme.demo.alice", Online: true, Role: "Code Reviewer", Capabilities: []string{"review", "test"}, Labels: map[string]string{"team": "blue", "tier": "1"}},
		{Address: "acme.demo.bob", Online: false, Role: "writer", Capabilities: []string{"review"}, Labels: map[string]string{"team": "red"}},
		{Address: "acme.lab.carol", Online: true, Role: "reviewer", Capabilities: []string{"reviewer"}},
		{Address: "acme.lab.dan", Online: true},
	}}
}

func TestWhoFilters(t *testing.T) {
	d := filterDir()
	for _, tc := range []struct {
		name string
		argv []string
		want []string
	}{
		{"role contains, any case", []string{"--role", "review"}, []string{"acme.demo.alice", "acme.lab.carol"}},
		{"role upper", []string{"--role", "WRITE"}, []string{"acme.demo.bob"}},
		{"capability is an exact word", []string{"--capability", "review"}, []string{"acme.demo.alice", "acme.demo.bob"}},
		{"capability is case sensitive and whole", []string{"--capability", "Review"}, nil},
		{"capabilities all", []string{"--capability", "review", "--capability", "test"}, []string{"acme.demo.alice"}},
		{"label present", []string{"--label", "team"}, []string{"acme.demo.alice", "acme.demo.bob"}},
		{"label value", []string{"--label", "team=blue"}, []string{"acme.demo.alice"}},
		{"label value exact", []string{"--label", "team=blu"}, nil},
		{"labels all", []string{"--label", "team=blue", "--label", "tier"}, []string{"acme.demo.alice"}},
		{"labels one missing", []string{"--label", "team=blue", "--label", "x"}, nil},
		{"with online", []string{"--capability", "review", "--online"}, []string{"acme.demo.alice"}},
		{"with offline", []string{"--capability", "review", "--offline"}, []string{"acme.demo.bob"}},
		{"with prefix", []string{"--role", "review", "acme.lab"}, []string{"acme.lab.carol"}},
		{"everything", []string{"--role", "rev", "--capability", "test", "--label", "tier=1", "--online", "acme.demo"}, []string{"acme.demo.alice"}},
		{"label value with =", []string{"--label", "team=blue=x"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := whoAddrs(t, d, tc.argv...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%v: %v, want %v", tc.argv, got, tc.want)
			}
		})
	}
	// nothing matched: the text says so, with the prefix and online/offline where given
	_, out, _ := runWho(d, cliframe.Global{}, "--capability", "nope")
	if out != "No endpoints\n" {
		t.Errorf("empty: %q", out)
	}
	_, out, _ = runWho(d, cliframe.Global{}, "--role", "nope", "--online", "acme.lab")
	if out != "No online endpoints under acme.lab\n" {
		t.Errorf("empty: %q", out)
	}
	// the tool: an array for the repeatable ones; a bare string is refused
	tool := cmdcore.MCPTool(cmds.Who, whoEnv(d))
	for args, want := range map[string]string{
		`{"capability":["review"],"label":["team=blue"]}`:        "acme.demo.alice",
		`{"capability":["review","test"],"label":["team=blue"]}`: "acme.demo.alice",
		`{"role":"WRITER"}`: "acme.demo.bob",
	} {
		_, st, err := tool.CallStructured(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		r := st.(cmds.WhoResult)
		if len(r.Endpoints) != 1 || r.Endpoints[0].Address != want {
			t.Errorf("%s: %+v", args, r.Endpoints)
		}
	}
	for _, args := range []string{`{"capability":"review"}`, `{"label":"team=blue"}`} {
		if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(args)); err == nil || !strings.Contains(err.Error(), "must be an array of strings") {
			t.Errorf("%s: %v", args, err)
		}
	}
	// bad filters are usage errors
	for _, argv := range [][]string{{"--role", " "}, {"--label", "="}, {"--label", "a="}, {"--label", "=b"}, {"--capability", " "}} {
		if code, _, errs := runWho(d, cliframe.Global{}, argv...); code != 2 || !strings.Contains(errs, "hint: mash who --help") {
			t.Errorf("%v: %d %q", argv, code, errs)
		}
	}
}

// Answers that failed verification: the table is unchanged and exit is 0; one warning on stderr says how many, singular
// or plural, and nothing is printed when there are none (TO-12).
func TestWhoWarnsOnceHowManyAnswersFailedVerification(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, ""},
		{1, "warning: 1 discovery answer could not be verified\n"},
		{2, "warning: 2 discovery answers could not be verified\n"},
	} {
		d := directory()
		d.notVerified = tc.n
		code, out, errs := runWho(d, cliframe.Global{}, "acme.demo.alice")
		if code != 0 || errs != tc.want || !strings.HasPrefix(out, "ADDRESS") {
			t.Errorf("n=%d: exit %d, stderr %q, stdout %q", tc.n, code, errs, out)
		}
	}
}

// --json carries the count as an integer after endpoints, 0 when none, and prints no warning.
func TestWhoJSONAnswersNotVerified(t *testing.T) {
	for _, n := range []int{0, 3} {
		d := directory()
		d.notVerified = n
		code, out, errs := runWho(d, cliframe.Global{JSON: true})
		if code != 0 || errs != "" {
			t.Fatalf("n=%d: exit %d, stderr %q", n, code, errs)
		}
		var o map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &o); err != nil {
			t.Fatal(err)
		}
		if got := string(o["answers_not_verified"]); got != fmt.Sprint(n) {
			t.Errorf("n=%d: answers_not_verified = %q in %s", n, got, out)
		}
		if strings.Index(out, `"answers_not_verified"`) < strings.Index(out, `"endpoints"`) {
			t.Errorf("answers_not_verified comes before endpoints: %s", out)
		}
	}
}

// The MCP tool's structured result carries the same count, and its text has no warning line.
func TestWhoMCPAnswersNotVerified(t *testing.T) {
	d := directory()
	d.notVerified = 2
	text, structured, err := cmdcore.MCPTool(cmds.Who, whoEnv(d)).CallStructured(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(structured)
	var o struct {
		N *int `json:"answers_not_verified"`
	}
	if err := json.Unmarshal(b, &o); err != nil || o.N == nil || *o.N != 2 {
		t.Errorf("structured %s: %v", b, err)
	}
	if strings.Contains(text, "warning") {
		t.Errorf("text has a warning: %q", text)
	}
}
