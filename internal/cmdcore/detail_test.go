package cmdcore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

type fullResult struct {
	Name string `json:"name"`
}

func (f fullResult) Text(cmdcore.Opts) string { return f.Name + ": ok" }
func (f fullResult) DetailText(cmdcore.Opts) string {
	return "size:  big\ncolor: red"
}
func (f fullResult) DetailValue() any {
	return struct {
		fullResult
		Size string `json:"size"`
	}{f, "big"}
}

type fullParams struct {
	Name string `json:"name" arg:"name" required:"true" desc:"Who."`
}

var fullCmd = cmdcore.Command[fullParams, fullResult]{Name: "full", Run: func(_ context.Context, _ *cmdcore.Env, p fullParams) (fullResult, error) {
	return fullResult(p), nil
}}

func runFull(g cliframe.Global, args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := cmdcore.CLI(fullCmd, env)(args, nil, &o, &e, g)
	return code, o.String(), e.String()
}

func TestDetailerCLI(t *testing.T) {
	if _, out, _ := runFull(cliframe.Global{}, "x"); out != "x: ok\n" {
		t.Errorf("usual %q", out)
	}
	if _, out, _ := runFull(cliframe.Global{Detail: true}, "x"); out != "x: ok\nsize:  big\ncolor: red\n" {
		t.Errorf("detail %q", out)
	}
	if _, out, _ := runFull(cliframe.Global{JSON: true}, "x"); out != `{"name":"x"}`+"\n" {
		t.Errorf("json %q", out)
	}
	_, out, _ := runFull(cliframe.Global{JSON: true, Detail: true}, "x")
	var usual, fuller map[string]any
	_ = json.Unmarshal([]byte(`{"name":"x"}`), &usual)
	if err := json.Unmarshal([]byte(out), &fuller); err != nil || fuller["size"] != "big" || fuller["name"] != usual["name"] {
		t.Errorf("fuller object %q %v", out, err)
	}
}

// A result with nothing more to say is unchanged by the option, in text and in JSON.
func TestDetailWithNothingMoreChangesNothing(t *testing.T) {
	c := cmdcore.Command[demoParams, plainResult]{Name: "plain", Run: func(context.Context, *cmdcore.Env, demoParams) (plainResult, error) {
		return plainResult{"x"}, nil
	}}
	for _, js := range []bool{false, true} {
		var a, b, e bytes.Buffer
		cmdcore.CLI(c, env)([]string{"x"}, nil, &a, &e, cliframe.Global{JSON: js})
		cmdcore.CLI(c, env)([]string{"x"}, nil, &b, &e, cliframe.Global{JSON: js, Detail: true})
		if a.String() != b.String() {
			t.Errorf("json=%v: %q vs %q", js, a.String(), b.String())
		}
	}
}

type plainResult struct {
	Got string `json:"got"`
}

func (p plainResult) Text(cmdcore.Opts) string { return p.Got }

func TestOldVerboseOptionIsRefused(t *testing.T) {
	for _, old := range []string{"--verbose", "-v"} {
		code, _, e := runFull(cliframe.Global{}, "x", old)
		if code != 2 || !strings.Contains(e, "unknown flag "+old) {
			t.Errorf("%s: %d %q", old, code, e)
		}
	}
}

func TestDetailerMCP(t *testing.T) {
	tool := cmdcore.MCPTool(fullCmd, env)
	props := tool.Schema["properties"].(map[string]any)
	d, ok := props[cliframe.DetailParam].(map[string]any)
	if !ok || d["type"] != "boolean" || d["description"] != "Return the fuller object. Default false: the usual, minimal one." {
		t.Fatalf("schema %v", props)
	}
	if rq := tool.Schema["required"].([]string); len(rq) != 1 || rq[0] != "name" {
		t.Errorf("required %v", rq)
	}
	text, st, err := tool.CallStructured(context.Background(), json.RawMessage(`{"name":"x"}`))
	if err != nil || text != "x: ok" {
		t.Fatalf("%q %v", text, err)
	}
	if b, _ := json.Marshal(st); string(b) != `{"name":"x"}` {
		t.Errorf("usual %s", b)
	}
	text, st, err = tool.CallStructured(context.Background(), json.RawMessage(`{"name":"x","detail":true}`))
	if err != nil || text != "x: ok\nsize:  big\ncolor: red" {
		t.Fatalf("%q %v", text, err)
	}
	if b, _ := json.Marshal(st); string(b) != `{"name":"x","size":"big"}` {
		t.Errorf("fuller %s", b)
	}
	if _, _, err = tool.CallStructured(context.Background(), json.RawMessage(`{"name":"x","detail":"yes"}`)); err == nil {
		t.Error("a non-boolean detail is refused")
	}
	// a command with no fuller answer has no detail param
	plain := cmdcore.MCPTool(cmdcore.Command[demoParams, plainResult]{Name: "plain"}, env)
	if _, has := plain.Schema["properties"].(map[string]any)[cliframe.DetailParam]; has {
		t.Error("plain command lists detail")
	}
}
