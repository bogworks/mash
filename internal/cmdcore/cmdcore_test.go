package cmdcore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

type listResult []string

func (l listResult) Text(cmdcore.Opts) string { return strings.Join(l, ",") }

type demoParams struct {
	Name  string   `json:"name" arg:"name" required:"true" desc:"Who."`
	Tags  []string `json:"tags,omitempty" flag:"--tag" short:"-t" repeat:"true" desc:"Tags."`
	Count *int     `json:"count,omitempty" flag:"--count" desc:"How many."`
	N     int      `json:"n,omitempty" flag:"--n"`
	Loud  bool     `json:"loud,omitempty" flag:"--loud" short:"-l"`
	Skip  string   `json:"-" flag:"--skip"`
}

type demoResult struct {
	Got string `json:"got"`
}

func (d demoResult) Text(o cmdcore.Opts) string {
	return "got " + d.Got + map[bool]string{true: " (verbose)", false: ""}[o.Verbose]
}

func env(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil }

func TestParseAllForms(t *testing.T) {
	var seen demoParams
	c := cmdcore.Command[demoParams, demoResult]{Name: "demo", Run: func(_ context.Context, _ *cmdcore.Env, p demoParams) (demoResult, error) {
		seen = p
		return demoResult{Got: p.Name}, nil
	}}
	run := func(g cliframe.Global, args ...string) (int, string, string) {
		var o, e bytes.Buffer
		code := cmdcore.CLI(c, env)(args, nil, &o, &e, g)
		return code, o.String(), e.String()
	}
	code, out, _ := run(cliframe.Global{Verbose: true}, "x", "--tag", "a", "-t", "b", "--tag=c", "--count=3", "--n", "-4", "-l", "--skip", "s")
	if code != 0 || out != "got x (verbose)\n" {
		t.Fatalf("%d %q", code, out)
	}
	if !reflect.DeepEqual(seen.Tags, []string{"a", "b", "c"}) || seen.Count == nil || *seen.Count != 3 || seen.N != -4 || !seen.Loud || seen.Skip != "s" {
		t.Errorf("params %+v", seen)
	}
	code, out, _ = run(cliframe.Global{JSON: true}, "x")
	if code != 0 || out != `{"got":"x"}`+"\n" {
		t.Errorf("json %d %q", code, out)
	}
	if code, _, e := run(cliframe.Global{}, "x", "--count", "z"); code != 2 || !strings.Contains(e, "--count must be a whole number") {
		t.Errorf("%d %q", code, e)
	}
	if code, _, e := run(cliframe.Global{}); code != 2 || e != "error: mash demo needs <name>\nhint: mash demo --help\n" {
		t.Errorf("%d %q", code, e)
	}
	// a run error keeps its code
	c.Run = func(context.Context, *cmdcore.Env, demoParams) (demoResult, error) {
		return demoResult{}, errors.New("boom")
	}
	if code, _, e := run(cliframe.Global{}, "x"); code != 1 || e != "error: boom\n" {
		t.Errorf("%d %q", code, e)
	}
}

func TestArrayResultFallback(t *testing.T) {
	c := cmdcore.Command[struct{}, listResult]{Name: "list", Summary: "List.", Run: func(context.Context, *cmdcore.Env, struct{}) (listResult, error) {
		return listResult{"a", "b"}, nil
	}}
	text, structured, err := cmdcore.MCPTool(c, env).CallStructured(context.Background(), nil)
	if err != nil || text != "a,b" || structured != nil {
		t.Errorf("%q %v %v", text, structured, err)
	}
	var o, e bytes.Buffer
	if code := cmdcore.CLI(c, env)(nil, nil, &o, &e, cliframe.Global{JSON: true}); code != 0 || o.String() != `["a","b"]`+"\n" {
		t.Errorf("cli json %d %q", code, o.String())
	}
}

func TestSchemaFromTags(t *testing.T) {
	s := cmdcore.Schema[demoParams]()
	props := s["properties"].(map[string]any)
	if s["type"] != "object" || !reflect.DeepEqual(s["required"], []string{"name"}) {
		t.Errorf("schema %v", s)
	}
	if _, ok := props["skip"]; ok || len(props) != 5 {
		t.Errorf("props %v", props)
	}
	for k, want := range map[string]string{"name": "string", "tags": "array", "count": "integer", "n": "integer", "loud": "boolean"} {
		if got := props[k].(map[string]any)["type"]; got != want {
			t.Errorf("%s type %v want %s", k, got, want)
		}
	}
	if items := props["tags"].(map[string]any)["items"]; !reflect.DeepEqual(items, map[string]any{"type": "string"}) {
		t.Errorf("items %v", items)
	}
	if props["name"].(map[string]any)["description"] != "Who." {
		t.Error("description")
	}
	if _, ok := props["n"].(map[string]any)["description"]; ok {
		t.Error("empty description must be left out")
	}
}

func TestMCPDecode(t *testing.T) {
	var seen demoParams
	c := cmdcore.Command[demoParams, demoResult]{Name: "demo", Run: func(_ context.Context, _ *cmdcore.Env, p demoParams) (demoResult, error) {
		seen = p
		return demoResult{Got: p.Name}, nil
	}}
	tool := cmdcore.MCPTool(c, env)
	text, st, err := tool.CallStructured(context.Background(), json.RawMessage(`{"name":"x","tags":["a","b"],"count":2,"loud":true}`))
	if err != nil || text != "got x" || st.(demoResult).Got != "x" {
		t.Fatalf("%q %v %v", text, st, err)
	}
	if !reflect.DeepEqual(seen.Tags, []string{"a", "b"}) || *seen.Count != 2 || !seen.Loud {
		t.Errorf("%+v", seen)
	}
	for in, want := range map[string]string{
		`{}`:                         "error: name is required",
		``:                           "error: name is required",
		`null`:                       "error: name is required",
		`{"name":"x","bogus":1}`:     "error: unknown argument bogus",
		`{"name":"x","count":"two"}`: "error: count has the wrong type",
		`"str"`:                      "error: the arguments must be a JSON object",
	} {
		if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(in)); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: %v, want prefix %q", in, err, want)
		}
	}
	// an error with a hint becomes two lines
	c.Run = func(context.Context, *cmdcore.Env, demoParams) (demoResult, error) {
		return demoResult{}, cmdcore.NoSuchEndpoint("a.b.c")
	}
	if _, _, err := cmdcore.MCPTool(c, env).CallStructured(context.Background(), json.RawMessage(`{"name":"x"}`)); err.Error() != "error: no endpoint has the address a.b.c\nhint: mash who" {
		t.Errorf("%v", err)
	}
	// env failure
	bad := func(context.Context) (*cmdcore.Env, error) { return nil, errors.New("down") }
	if _, _, err := cmdcore.MCPTool(c, bad).CallStructured(context.Background(), json.RawMessage(`{"name":"x"}`)); err == nil || err.Error() != "error: down" {
		t.Errorf("%v", err)
	}
}

// Unknown keys are reported before missing ones, with the valid names as the hint.
func TestMCPUnknownBeforeRequired(t *testing.T) {
	c := cmdcore.Command[demoParams, demoResult]{Name: "demo", Run: func(context.Context, *cmdcore.Env, demoParams) (demoResult, error) {
		return demoResult{}, nil
	}}
	_, _, err := cmdcore.MCPTool(c, env).CallStructured(context.Background(), json.RawMessage(`{"to":"x","text":"y"}`))
	if err == nil || !strings.HasPrefix(err.Error(), "error: unknown argument text\nhint: the arguments are name, ") {
		t.Fatalf("%v", err)
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		err        *cliframe.Error
		msg, hint  string
		wantReport string
	}{
		{cmdcore.NoSuchEndpoint("acme.demo.alise"), "no endpoint has the address acme.demo.alise", "mash who", "error: no endpoint has the address acme.demo.alise\nhint: mash who\n"},
		{cmdcore.NotSignedIn("acme.demo.bob"), "acme.demo.bob cannot sign in: the server refused its credentials", "mash doctor", "error: acme.demo.bob cannot sign in: the server refused its credentials\nhint: mash doctor\n"},
	} {
		if tc.err.Msg != tc.msg || tc.err.Hint != tc.hint || tc.err.Code != 1 {
			t.Errorf("%+v", tc.err)
		}
		var o, e bytes.Buffer
		if code := cliframe.Report(&o, &e, tc.err, false); code != 1 || e.String() != tc.wantReport {
			t.Errorf("%d %q", code, e.String())
		}
	}
}

func TestRegistry(t *testing.T) {
	a := cmdcore.Command[struct{}, demoResult]{Name: "zeta", Summary: "Z.", Run: func(context.Context, *cmdcore.Env, struct{}) (demoResult, error) {
		return demoResult{Got: "z"}, nil
	}}
	b := a
	b.Name, b.Summary = "alpha", "A."
	reg := cmdcore.NewRegistry(env, cmdcore.Register(a), cmdcore.Register(b))
	if !reflect.DeepEqual(reg.Names(), []string{"alpha", "zeta"}) {
		t.Errorf("names %v", reg.Names())
	}
	tools := reg.Tools()
	if len(tools) != 2 || tools[0].Name != "zeta" || tools[1].Description != "A." {
		t.Errorf("tools %+v", tools)
	}
	if m := reg.MCPTools(); len(m) != 2 || m[0].Name != "zeta" {
		t.Errorf("mcp tools %+v", m)
	}
	e, ok := reg.Lookup("alpha")
	if !ok || e.CLIName() != "alpha" || e.Summary() != "A." {
		t.Error("lookup")
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Error("lookup nope")
	}
	var o, er bytes.Buffer
	if code := reg.RunCLI("zeta", nil, nil, &o, &er, cliframe.Global{}); code != 0 || o.String() != "got z\n" {
		t.Errorf("%d %q", code, o.String())
	}
	if code := reg.RunCLI("nope", nil, nil, &o, &er, cliframe.Global{}); code != 2 {
		t.Errorf("unknown: %d", code)
	}
}

func TestUnsupportedFieldPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want a panic for a float field")
		}
	}()
	cmdcore.Schema[struct {
		F float64 `json:"f"`
	}]()
}

// A read of a refused sequence says "message 12 was refused".
func TestRefusedMessageText(t *testing.T) {
	if got := cmdcore.RefusedMessage(12).Error(); got != "message 12 was refused: it was not verified" {
		t.Fatalf("got %q", got)
	}
}
