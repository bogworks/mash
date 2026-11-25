package cmdcore_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/mcp"
)

type badEnum struct {
	Num int `json:"num" flag:"--num" enum:"a,b"`
}
type badRequired struct {
	Req string `json:"req" flag:"--req" required:"yes"`
}
type badArgOpt struct {
	Pos string `json:"pos" arg:"pos,maybe"`
}
type argAndFlag struct {
	Both string `json:"both" arg:"both" flag:"--both"`
}
type repeatScalar struct {
	Rep string `json:"rep" flag:"--rep" repeat:"true"`
}
type repeatBool struct {
	RepB bool `json:"repb" flag:"--repb" repeat:"true"`
}
type shortNoFlag struct {
	Sh string `json:"sh" arg:"sh" short:"-s"`
}
type optBeforeReq struct {
	First  string `json:"first" arg:"first,optional"`
	Second string `json:"second" arg:"second"`
}
type dupArg struct {
	A string `json:"a" arg:"same"`
	B string `json:"b" arg:"same"`
}
type dupFlag struct {
	A string `flag:"--same"`
	B string `flag:"--same"`
}

func registerP[P any]() {
	cmdcore.Register(cmdcore.Command[P, listResult]{Name: "bad", Run: func(context.Context, *cmdcore.Env, P) (listResult, error) { return nil, nil }})
}

func TestBadTagsPanicAtRegistration(t *testing.T) {
	cases := []struct {
		name  string
		reg   func()
		field string
	}{
		{"enum on int", registerP[badEnum], "Num"},
		{"required value", registerP[badRequired], "Req"},
		{"arg option", registerP[badArgOpt], "Pos"},
		{"arg and flag", registerP[argAndFlag], "Both"},
		{"repeat on scalar", registerP[repeatScalar], "Rep"},
		{"repeat on bool", registerP[repeatBool], "RepB"},
		{"short without flag", registerP[shortNoFlag], "Sh"},
		{"optional before required", registerP[optBeforeReq], "Second"},
		{"duplicate arg", registerP[dupArg], "B"},
		{"duplicate flag", registerP[dupFlag], "B"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatal("no panic")
				}
				msg, _ := r.(string)
				if !strings.Contains(msg, "."+c.field+" ") {
					t.Errorf("message %q does not name field %s", msg, c.field)
				}
				if !strings.Contains(msg, "cmdcore_test.") {
					t.Errorf("message %q does not name the type", msg)
				}
			}()
			c.reg()
		})
	}
}

func TestRealCommandsStillRegister(t *testing.T) {
	cmdcore.Register(cmds.Send)
	cmdcore.Register(cmds.Version)
}

func TestToolErrorIsStructured(t *testing.T) {
	c := cmdcore.Command[demoParams, demoResult]{Name: "demo", Run: func(context.Context, *cmdcore.Env, demoParams) (demoResult, error) {
		return demoResult{}, cmdcore.NoSuchEndpoint("a.b.c")
	}}
	reg := cmdcore.NewRegistry(env, cmdcore.Register(c))
	tool := reg.MCPTools()[0]
	_, _, err := tool.CallStructured(context.Background(), []byte(`{"name":"x"}`))
	if err == nil {
		t.Fatal("no error")
	}
	if want := "error: no endpoint a.b.c\nhint: mash who"; err.Error() != want {
		t.Errorf("text %q", err.Error())
	}
	se, ok := err.(mcp.StructuredError)
	if !ok {
		t.Fatalf("%T is not a StructuredError", err)
	}
	want := map[string]any{"error": "no endpoint a.b.c", "hint": "mash who"}
	if !reflect.DeepEqual(se.Structured(), want) {
		t.Errorf("structured %v", se.Structured())
	}
	_, _, err = tool.CallStructured(context.Background(), []byte(`{}`))
	if got := err.(mcp.StructuredError).Structured(); !reflect.DeepEqual(got, map[string]any{"error": "name is required"}) {
		t.Errorf("no-hint structured %v", got)
	}
}
