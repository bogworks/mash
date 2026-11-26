package cmds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/cmdcore/render"
)

type fakeSvc struct {
	to, text string
	o        cmds.SendOptions
	rc       cmds.SendReceipt
	err      error
	calls    int
}

func (f *fakeSvc) Send(_ context.Context, to, text string, o cmds.SendOptions) (cmds.SendReceipt, error) {
	f.calls++
	f.to, f.text, f.o = to, text, o
	return f.rc, f.err
}
func (f *fakeSvc) Version() (string, string, string) { return "0.5.6", "3f9a41c", "0.5.6" }

func envOf(f *fakeSvc, stdinPiped bool, files map[string]string) cmdcore.EnvFunc {
	return func(context.Context) (*cmdcore.Env, error) {
		return &cmdcore.Env{Svc: f, ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, errors.New("no such file " + p)
		}}, nil
	}
}

func runSend(f *fakeSvc, stdin string, files map[string]string, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Send, envOf(f, true, files))(args, strings.NewReader(stdin), &o, &e, g)
	return code, o.String(), e.String()
}

func TestSendArgv(t *testing.T) {
	one, two := 1, 2
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		files map[string]string
		to    string
		text  string
		o     cmds.SendOptions
	}{
		{"plain", []string{"a.b.c", "hi"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "task"}},
		{"urgent kind", []string{"a.b.c", "--urgent", "--kind", "question", "hi"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "question", Urgent: true}},
		{"equals forms", []string{"a.b.c", "--kind=fyi", "--reply-to=1", "--relay=2", "--id=k", "hi"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "fyi", ReplyTo: one, Relay: two, ID: "k"}},
		{"space forms after message", []string{"a.b.c", "hi", "--reply-to", "1", "--id", "k"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "task", ReplyTo: one, ID: "k"}},
		{"urgent=true", []string{"a.b.c", "--urgent=true", "hi"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "task", Urgent: true}},
		{"urgent=false", []string{"a.b.c", "--urgent=false", "hi"}, "", nil, "a.b.c", "hi", cmds.SendOptions{Kind: "task"}},
		{"file", []string{"a.b.c", "--file", "/m.txt"}, "", map[string]string{"/m.txt": "from file\n"}, "a.b.c", "from file", cmds.SendOptions{Kind: "task"}},
		{"file stdin", []string{"a.b.c", "--kind", "fyi", "--file", "-"}, "piped text\n", nil, "a.b.c", "piped text", cmds.SendOptions{Kind: "fyi"}},
		{"implicit stdin", []string{"a.b.c"}, "implicit", nil, "a.b.c", "implicit", cmds.SendOptions{Kind: "task"}},
		{"dashdash", []string{"a.b.c", "--", "--not-a-flag"}, "", nil, "a.b.c", "--not-a-flag", cmds.SendOptions{Kind: "task"}},
		{"lone dash reads stdin", []string{"a.b.c", "-"}, "from stdin\n", nil, "a.b.c", "from stdin", cmds.SendOptions{Kind: "task"}},
		{"lone dash after kind", []string{"a.b.c", "--kind", "fyi", "-"}, "x\r\n", nil, "a.b.c", "x", cmds.SendOptions{Kind: "fyi"}},
		{"dash-led text after dashdash", []string{"a.b.c", "--", "-x"}, "ignored", nil, "a.b.c", "-x", cmds.SendOptions{Kind: "task"}},
		{"lone dash after dashdash is stdin", []string{"a.b.c", "--", "-"}, "in\n", nil, "a.b.c", "in", cmds.SendOptions{Kind: "task"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{rc: cmds.SendReceipt{Number: 42}}
			code, out, errs := runSend(f, tc.stdin, tc.files, cliframe.Global{}, tc.args...)
			if code != 0 || errs != "" {
				t.Fatalf("code %d stderr %q", code, errs)
			}
			if out != "Mail sent (number 42 in a.b.c's inbox)\n" && !strings.HasSuffix(out, "mail sent (number 42 in a.b.c's inbox)\n") {
				t.Errorf("stdout %q", out)
			}
			if f.to != tc.to || f.text != tc.text || f.o != tc.o {
				t.Errorf("service got %q %q %+v, want %q %q %+v", f.to, f.text, f.o, tc.to, tc.text, tc.o)
			}
		})
	}
}

func TestSendUsageErrors(t *testing.T) {
	hint := "hint: mash send --help\n"
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"a.b.c", "hi", "--bogus"}, "error: unknown flag --bogus\n"},
		{"unknown flag with value", []string{"a.b.c", "hi", "--bogus=1"}, "error: unknown flag --bogus\n"},
		{"unknown short", []string{"a.b.c", "hi", "-z"}, "error: unknown flag -z\n"},
		{"no address", nil, "error: mash send needs <address>\n" + hint},
		{"flag without value", []string{"a.b.c", "hi", "--kind"}, "error: --kind needs a value\n" + hint},
		{"bad enum", []string{"a.b.c", "hi", "--kind", "loud"}, "error: --kind must be one of blocker, question, task, result, fyi\n" + hint},
		{"bad number", []string{"a.b.c", "hi", "--reply-to", "x"}, "error: --reply-to must be a whole number\n" + hint},
		{"bad bool", []string{"a.b.c", "hi", "--urgent=maybe"}, "error: --urgent must be true or false\n" + hint},
		{"extra positional", []string{"a.b.c", "hi", "more"}, "error: unexpected argument \"more\" for mash send\n" + hint},
		{"both message and file", []string{"a.b.c", "hi", "--file", "/m"}, "error: mash send takes the message text or --file, not both\n" + hint},
		{"reply-to zero", []string{"a.b.c", "hi", "--reply-to", "0"}, "error: --reply-to must be a whole number of 1 or more\n" + hint},
		{"relay negative", []string{"a.b.c", "hi", "--relay=-3"}, "error: --relay must be a whole number of 1 or more\n" + hint},
		{"reply-to fraction", []string{"a.b.c", "hi", "--reply-to", "2.5"}, "error: --reply-to must be a whole number\n" + hint},
		{"dash and file", []string{"a.b.c", "-", "--file", "-"}, "error: mash send takes the message text or --file, not both\n" + hint},
		{"blank message", []string{"a.b.c", "   "}, "error: mash send needs the message text\n" + hint},
		{"empty address", []string{"", "hi"}, "error: mash send needs <address>\n" + hint},
		{"blank address", []string{"  ", "hi"}, "error: mash send needs <address>\n" + hint},
		{"kind twice", []string{"a.b.c", "hi", "--kind", "fyi", "--kind=task"}, "error: --kind was given twice\n" + hint},
		{"urgent twice", []string{"a.b.c", "hi", "--urgent", "--urgent"}, "error: --urgent was given twice\n" + hint},
		{"id twice", []string{"a.b.c", "hi", "--id", "x", "--id", "y"}, "error: --id was given twice\n" + hint},
		{"positional flag name", []string{"a.b.c", "--address", "x"}, "error: unknown flag --address\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{}
			code, out, errs := runSend(f, "", nil, cliframe.Global{}, tc.args...)
			if code != 2 || out != "" || errs != tc.want || f.calls != 0 {
				t.Errorf("code %d stdout %q stderr %q calls %d; want stderr %q", code, out, errs, f.calls, tc.want)
			}
		})
	}
	// no text and a terminal stdin
	var o, e bytes.Buffer
	f := &fakeSvc{}
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: f}, nil }
	code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c"}, nil, &o, &e, cliframe.Global{})
	if code != 2 || e.String() != "error: mash send needs the message text\nhint: mash send --help\n" {
		t.Errorf("no text: %d %q", code, e.String())
	}
	// empty piped text
	code, _, errs := runSend(&fakeSvc{}, "\n", nil, cliframe.Global{}, "a.b.c")
	if code != 2 || !strings.HasPrefix(errs, "error: mash send needs the message text") {
		t.Errorf("empty stdin: %d %q", code, errs)
	}
	// usage error in --json mode
	code, out, _ := runSend(&fakeSvc{}, "", nil, cliframe.Global{JSON: true}, "a.b.c", "hi", "--bogus")
	if code != 2 || out != `{"error":"unknown flag --bogus"}`+"\n" {
		t.Errorf("json usage: %d %q", code, out)
	}
}

func TestSendFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no such endpoint", fmt.Errorf("lookup: %w", cmds.ErrNoSuchEndpoint), "error: no endpoint acme.demo.alise\nhint: mash who\n"},
		{"recipient suspended", fmt.Errorf("send: %w", cmds.ErrRecipientSuspended), "error: acme.demo.alise is suspended or was deleted; nothing was sent\nhint: mash who\n"},
		{"not signed in", &cmds.SignInError{Addr: "acme.demo.bob"}, "error: acme.demo.bob cannot sign in: the server refused its credentials\nhint: mash doctor\n"},
		{"not signed in, no name", cmds.ErrNotSignedIn, "error: this endpoint cannot sign in: the server refused its credentials\nhint: mash doctor\n"},
		{"other", errors.New("boom"), "error: boom\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{err: tc.err}
			code, out, errs := runSend(f, "", nil, cliframe.Global{}, "acme.demo.alise", "hi")
			if code != 1 || out != "" || errs != tc.want {
				t.Errorf("code %d stdout %q stderr %q", code, out, errs)
			}
		})
	}
	// unreadable file and missing service
	code, _, errs := runSend(&fakeSvc{}, "", nil, cliframe.Global{}, "a.b.c", "--file", "/nope")
	if code != 1 || !strings.HasPrefix(errs, "error: cannot read the message: no such file /nope") {
		t.Errorf("file: %d %q", code, errs)
	}
	var o, e bytes.Buffer
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil }
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c", "hi"}, nil, &o, &e, cliframe.Global{}); code != 1 {
		t.Errorf("no service: %d", code)
	}
	envErr := func(context.Context) (*cmdcore.Env, error) {
		return nil, cliframe.Failf("not set up").WithHint("mash setup")
	}
	e.Reset()
	if code := cmdcore.CLI(cmds.Send, envErr)([]string{"a.b.c", "hi"}, nil, &o, &e, cliframe.Global{}); code != 1 || e.String() != "error: not set up\nhint: mash setup\n" {
		t.Errorf("env error: %d %q", code, e.String())
	}
}

func TestSendText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		r       cmds.SendResult
		verbose bool
		want    string
	}{
		{"sent", cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}, false, "Mail sent (number 42 in acme.demo.alice's inbox)"},
		{"no kind", cmds.SendResult{Number: 3}, false, "Mail sent (number 3)"},
		{"urgent task", cmds.SendResult{Kind: "task", Urgent: true, Number: 5}, false, "Urgent mail sent (number 5)"},
		{"question", cmds.SendResult{Kind: "question", Number: 6}, false, "Question mail sent (number 6)"},
		{"urgent question", cmds.SendResult{Kind: "question", Urgent: true, Number: 43}, false, "Urgent question mail sent (number 43)"},
		{"duplicate", cmds.SendResult{Kind: "task", Duplicate: true, Number: 42}, false, "Mail already sent; nothing new stored"},
		{"retagged", cmds.SendResult{Kind: "fyi", Retagged: true, Number: 44}, false, "Mail sent as fyi (number 44): a reply marked question that asks nothing"},
		{"retagged urgent", cmds.SendResult{Kind: "fyi", Urgent: true, Retagged: true, Number: 45}, false, "Urgent mail sent as fyi (number 45): a reply marked question that asks nothing"},
		{"retagged verbose", cmds.SendResult{To: "acme.demo.alice", Number: 9, Kind: "fyi", Retagged: true}, true,
			"Mail sent as fyi (number 9): a reply marked question that asks nothing\nto:     acme.demo.alice\nkind:   fyi\nurgent: no"},
		{"duplicate beats retagged", cmds.SendResult{Kind: "fyi", Duplicate: true, Retagged: true}, false, "Mail already sent; nothing new stored"},
		{"verbose", cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}, true,
			"Mail sent (number 42 in acme.demo.alice's inbox)\nto:     acme.demo.alice\nkind:   task\nurgent: no"},
		{"verbose urgent", cmds.SendResult{To: "acme.demo.alice", Number: 7, Kind: "fyi", Urgent: true}, true,
			"Urgent fyi mail sent (number 7 in acme.demo.alice's inbox)\nto:     acme.demo.alice\nkind:   fyi\nurgent: yes"},
	} {
		if got := tc.r.Text(cmdcore.Opts{Detail: tc.verbose}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// noPluginSvc is a build run outside the plugin, at the version and commit of the specification's example.
type noPluginSvc struct{}

func (noPluginSvc) Version() (string, string, string) {
	return "0.5.9", "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098", ""
}

func TestVersion(t *testing.T) {
	f := &fakeSvc{}
	var o, e bytes.Buffer
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: f}, nil }
	if code := cmdcore.CLI(cmds.Version, env)(nil, nil, &o, &e, cliframe.Global{}); code != 0 || o.String() != "mash v0.5.6 (3f9a41c)\n" {
		t.Errorf("text: %d %q", code, o.String())
	}
	o.Reset()
	cmdcore.CLI(cmds.Version, env)(nil, nil, &o, &e, cliframe.Global{JSON: true})
	if o.String() != `{"version":"0.5.6","commit":"3f9a41c","plugin":"0.5.6"}`+"\n" {
		t.Errorf("json: %q", o.String())
	}
	// no plugin: "plugin" is null, never "" (the specification's case, word for word)
	noPlugin := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: noPluginSvc{}}, nil }
	o.Reset()
	cmdcore.CLI(cmds.Version, noPlugin)(nil, nil, &o, &e, cliframe.Global{JSON: true})
	var ind bytes.Buffer
	if err := json.Indent(&ind, bytes.TrimSpace(o.Bytes()), "", "  "); err != nil {
		t.Fatal(err)
	}
	_, vmeta := specCommand(t, "version")
	found := false
	for _, x := range vmeta["x-output"].([]any) {
		if oc := x.(map[string]any); oc["case"] == "--json, no plugin" {
			found = true
			if ind.String() != oc["text"].(string) {
				t.Errorf("json, no plugin: %q, spec %q", ind.String(), oc["text"])
			}
		}
	}
	if !found {
		t.Error("spec has no case \"--json, no plugin\"")
	}
	o.Reset()
	if code := cmdcore.CLI(cmds.Version, env)([]string{"extra"}, nil, &o, &e, cliframe.Global{}); code != 2 {
		t.Errorf("extra arg: %d", code)
	}
	bad := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil }
	if code := cmdcore.CLI(cmds.Version, bad)(nil, nil, &o, &e, cliframe.Global{}); code != 1 {
		t.Errorf("no svc: %d", code)
	}
}

// TestDoorsAgree drives the same input through the CLI (--json) and through the MCP tool and compares them.
func TestDoorsAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		argv []string
		args string // MCP arguments
		rc   cmds.SendReceipt
	}{
		{"send plain", "send", []string{"acme.demo.alice", "lunch?"}, `{"address":"acme.demo.alice","message":"lunch?"}`, cmds.SendReceipt{Number: 42}},
		{"send urgent question", "send", []string{"acme.demo.alice", "--urgent", "--kind", "question", "--reply-to", "2", "ready?"},
			`{"address":"acme.demo.alice","message":"ready?","kind":"question","urgent":true,"reply_to":2}`, cmds.SendReceipt{Number: 43}},
		{"send duplicate", "send", []string{"acme.demo.alice", "--id", "k", "x"}, `{"address":"acme.demo.alice","message":"x","id":"k"}`, cmds.SendReceipt{Number: 44, Duplicate: true}},
		{"send retagged", "send", []string{"acme.demo.alice", "--kind", "question", "--reply-to", "2", "ok"},
			`{"address":"acme.demo.alice","message":"ok","kind":"question","reply_to":2}`, cmds.SendReceipt{Number: 45, Kind: "fyi", Retagged: true}},
		{"send urgent retagged", "send", []string{"acme.demo.alice", "--urgent", "--kind", "question", "--reply-to", "2", "ok"},
			`{"address":"acme.demo.alice","message":"ok","kind":"question","urgent":true,"reply_to":2}`, cmds.SendReceipt{Number: 46, Kind: "fyi", Retagged: true}},
		{"version", "version", nil, `{}`, cmds.SendReceipt{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{rc: tc.rc}
			reg := cmdcore.NewRegistry(envOf(f, true, nil), cmdcore.Register(cmds.Send), cmdcore.Register(cmds.Version))
			var out, errs bytes.Buffer
			if code := reg.RunCLI(tc.cmd, tc.argv, strings.NewReader(""), &out, &errs, cliframe.Global{JSON: true}); code != 0 {
				t.Fatalf("cli: %d %s", code, errs.String())
			}
			cliSvc := *f
			var cliObj map[string]any
			if err := json.Unmarshal(out.Bytes(), &cliObj); err != nil {
				t.Fatal(err)
			}
			var textOut bytes.Buffer
			if code := reg.RunCLI(tc.cmd, tc.argv, strings.NewReader(""), &textOut, &errs, cliframe.Global{}); code != 0 {
				t.Fatalf("cli text run: exit %d %s", code, errs.String())
			}

			e, _ := reg.Lookup(tc.cmd)
			text, structured, err := e.Tool(reg.Env).CallStructured(context.Background(), json.RawMessage(tc.args))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(structured)
			var mcpObj map[string]any
			if err := json.Unmarshal(b, &mcpObj); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cliObj, mcpObj) {
				t.Errorf("--json %v != structuredContent %v", cliObj, mcpObj)
			}
			if text+"\n" != textOut.String() {
				t.Errorf("mcp text %q != cli stdout %q", text, textOut.String())
			}
			if tc.cmd == "send" && (f.to != cliSvc.to || f.text != cliSvc.text || f.o != cliSvc.o) {
				t.Errorf("service saw different input: mcp %+v, cli %+v", f, cliSvc)
			}
		})
	}
}

func TestSendTool(t *testing.T) {
	f := &fakeSvc{err: cmds.ErrNoSuchEndpoint}
	tool := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil))
	if tool.Name != "send" || tool.Description != specTool(t, "send")["description"] {
		t.Errorf("tool %q %q", tool.Name, tool.Description)
	}
	_, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"acme.demo.alise","message":"hi"}`))
	if err == nil || err.Error() != "error: no endpoint acme.demo.alise\nhint: mash who" {
		t.Errorf("err = %v", err)
	}
	for _, bad := range []string{`{"address":"a.b.c"}`, `{"message":"m"}`, `{"address":"a.b.c","message":"m","kind":"loud"}`, `{"address":"a.b.c","message":"m","file":"x"}`, `{"address":"a.b.c","message":"m","reply_to":"x"}`, `[]`} {
		if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(bad)); err == nil || !strings.HasPrefix(err.Error(), "error: ") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	// the text-only mcp.Tool
	f.err = nil
	f.rc.Number = 5
	got, _, err := tool.MCP().CallStructured(context.Background(), json.RawMessage(`{"address":"a.b.c","message":"m"}`))
	if err != nil || got != "Mail sent (number 5 in a.b.c's inbox)" {
		t.Errorf("MCP().CallStructured = %q, %v", got, err)
	}
}

// TestSendSchema pins the tool's input schema to the published parameters.
func TestSendSchema(t *testing.T) {
	s := cmdcore.Schema[cmds.SendParams]()
	b, _ := json.Marshal(s)
	want := `{"additionalProperties":false,"properties":{` +
		`"address":{"description":"The recipient: owner.group.name, as the who tool lists it.","type":"string"},` +
		`"id":{"description":"A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice.","type":"string"},` +
		`"kind":{"description":"What sort of message this is. blocker, question and task wake an idle recipient that has mash's wake channel on; otherwise, and for result and fyi, the message arrives with its next turn. Default: task. One exception: a reply (reply_to set) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so.","enum":["blocker","question","task","result","fyi"],"type":"string"},` +
		`"message":{"description":"The text of the mail.","type":"string"},` +
		`"relay":{"description":"The number of a message in your inbox that this passes on. Signed, so the recipient can trust the link.","minimum":1,"type":"integer"},` +
		`"reply_to":{"description":"The number of the message in your inbox that this answers. Signed, so the recipient can trust the link.","minimum":1,"type":"integer"},` +
		`"urgent":{"description":"Deliver at once, into a running turn where the agent host allows it.","type":"boolean"}},` +
		`"required":["address","message"],"type":"object"}`
	if string(b) != want {
		t.Errorf("schema\n%s\nwant\n%s", b, want)
	}
	v := cmdcore.Schema[cmds.VersionParams]()
	vb, _ := json.Marshal(v)
	if string(vb) != `{"additionalProperties":false,"properties":{},"required":[],"type":"object"}` {
		t.Errorf("version schema %s", vb)
	}
}

func TestBoolFlagValues(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "false": false, "yes": true, "no": false, "TRUE": true, "No": false} {
		f := &fakeSvc{}
		code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a.b.c", "hi", "--urgent="+v)
		if code != 0 || f.o.Urgent != want {
			t.Errorf("--urgent=%s: code %d urgent %v stderr %q", v, code, f.o.Urgent, errs)
		}
	}
	for _, v := range []string{"1", "maybe", "", "y"} {
		code, _, errs := runSend(&fakeSvc{}, "", nil, cliframe.Global{}, "a.b.c", "hi", "--urgent="+v)
		if code != 2 || errs != "error: --urgent must be true or false\nhint: mash send --help\n" {
			t.Errorf("--urgent=%q: code %d stderr %q", v, code, errs)
		}
	}
}

func TestReplyToNumbers(t *testing.T) {
	f := &fakeSvc{}
	if code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a.b.c", "hi", "--reply-to", "1"); code != 0 || f.o.ReplyTo != 1 {
		t.Fatalf("code %d %q %+v", code, errs, f.o)
	}
	tool := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil))
	call := func(args string) error {
		_, _, err := tool.CallStructured(context.Background(), json.RawMessage(args))
		return err
	}
	f.o = cmds.SendOptions{}
	if err := call(`{"address":"a.b.c","message":"m","reply_to":2.0,"relay":3e0}`); err != nil || f.o.ReplyTo != 2 || f.o.Relay != 3 {
		t.Errorf("2.0 and 3e0 are integers: %v %+v", err, f.o)
	}
	for args, want := range map[string]string{
		`{"address":"a.b.c","message":"m","reply_to":2.5}`: "error: reply_to must be a whole number",
		`{"address":"a.b.c","message":"m","reply_to":0}`:   "error: reply_to must be a whole number of 1 or more",
		`{"address":"a.b.c","message":"m","relay":-1}`:     "error: relay must be a whole number of 1 or more",
		`{"address":"a.b.c","message":"m","relay":1e400}`:  "error: relay must be a whole number",
		`{"address":"a.b.c","message":"m","reply_to":"2"}`: "error: reply_to has the wrong type",
		`{"address":"","message":"m"}`:                     "error: address is required",
		`{"address":"a.b.c","message":"  "}`:               "error: message is required",
	} {
		if err := call(args); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
}

func TestNilEnvAndService(t *testing.T) {
	var o, e bytes.Buffer
	run := func(env cmdcore.EnvFunc) int {
		o.Reset()
		e.Reset()
		return cmdcore.CLI(cmds.Send, env)([]string{"a.b.c", "hi"}, nil, &o, &e, cliframe.Global{})
	}
	for name, env := range map[string]cmdcore.EnvFunc{
		"nil func":    nil,
		"nil env":     func(context.Context) (*cmdcore.Env, error) { return nil, nil },
		"nil service": func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil },
	} {
		if code := run(env); code != 1 || !strings.HasPrefix(e.String(), "error: internal: ") || o.Len() != 0 {
			t.Errorf("%s: code %d stdout %q stderr %q", name, code, o.String(), e.String())
		}
	}
	tool := cmdcore.MCPTool(cmds.Send, nil)
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"a.b.c","message":"m"}`)); err == nil || !strings.HasPrefix(err.Error(), "error: internal: ") {
		t.Errorf("mcp nil env func: %v", err)
	}
	tool = cmdcore.MCPTool(cmds.Send, func(context.Context) (*cmdcore.Env, error) { return nil, nil })
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"a.b.c","message":"m"}`)); err == nil || !strings.HasPrefix(err.Error(), "error: internal: ") {
		t.Errorf("mcp nil env: %v", err)
	}
	if _, err := cmds.Send.Run(context.Background(), nil, cmds.SendParams{Address: "a.b.c", Message: "m"}); err == nil {
		t.Error("Run with a nil env must fail")
	}
	if _, err := cmds.Version.Run(context.Background(), nil, cmds.VersionParams{}); err == nil {
		t.Error("Version.Run with a nil env must fail")
	}
}

func TestImplicitStdinOnlyWhenPiped(t *testing.T) {
	// /dev/null is not a terminal but has nothing to send: it is not read as the text.
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	var o, e bytes.Buffer
	f := &fakeSvc{}
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: f}, nil }
	code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c"}, null, &o, &e, cliframe.Global{})
	if code != 2 || f.calls != 0 || !strings.HasPrefix(e.String(), "error: mash send needs the message text") {
		t.Errorf("/dev/null: code %d calls %d stderr %q", code, f.calls, e.String())
	}
	// a pipe and a regular file are read
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString("from a pipe\n")
	w.Close()
	e.Reset()
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c"}, r, &o, &e, cliframe.Global{}); code != 0 || f.text != "from a pipe" {
		t.Errorf("pipe: code %d text %q stderr %q", code, f.text, e.String())
	}
	file := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(file, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := os.Open(file)
	defer in.Close()
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c"}, in, &o, &e, cliframe.Global{}); code != 0 || f.text != "from a file" {
		t.Errorf("file: code %d text %q", code, f.text)
	}
}

func TestOneTrailingNewlineDropped(t *testing.T) {
	for in, want := range map[string]string{
		"a\n": "a", "a\r\n": "a", "a\n\n": "a\n", "a\r\n\r\n": "a\r\n", "a": "a", "a\r": "a\r", "a b\nc\n": "a b\nc",
	} {
		f := &fakeSvc{}
		if code, _, errs := runSend(f, in, nil, cliframe.Global{}, "a.b.c", "--file", "-"); code != 0 || f.text != want {
			t.Errorf("%q: code %d text %q, want %q (%s)", in, code, f.text, want, errs)
		}
	}
}

// TestJSONErrorsBothDoors: a failure is {error, hint} on stdout with the exit code (CLI --json) and an error result with
// the same object as structuredContent (MCP).
func TestJSONErrorsBothDoors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		argv     []string
		args     string
		err      error
		code     int
		wantJSON string
	}{
		{"no such endpoint", []string{"acme.demo.alise", "hi"}, `{"address":"acme.demo.alise","message":"hi"}`, cmds.ErrNoSuchEndpoint, 1,
			`{"error":"no endpoint acme.demo.alise","hint":"mash who"}`},
		{"not signed in", []string{"a.b.c", "hi"}, `{"address":"a.b.c","message":"hi"}`, &cmds.SignInError{Addr: "acme.demo.bob"}, 1,
			`{"error":"acme.demo.bob cannot sign in: the server refused its credentials","hint":"mash doctor"}`},
		{"server not answering", []string{"a.b.c", "hi"}, `{"address":"a.b.c","message":"hi"}`, cmdcore.ServerNotAnswering("tls://h:4222"), 1,
			`{"error":"the server did not answer: tls://h:4222","hint":"mash doctor"}`},
		{"no hint", []string{"a.b.c", "hi"}, `{"address":"a.b.c","message":"hi"}`, errors.New("boom"), 1, `{"error":"boom"}`},
		{"usage", []string{"a.b.c", "hi", "--reply-to", "0"}, `{"address":"a.b.c","message":"hi","reply_to":0}`, nil, 2,
			`{"error":"--reply-to must be a whole number of 1 or more","hint":"mash send --help"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{err: tc.err}
			code, out, errs := runSend(f, "", nil, cliframe.Global{JSON: true}, tc.argv...)
			if code != tc.code || out != tc.wantJSON+"\n" || errs != "" {
				t.Errorf("cli: code %d stdout %q stderr %q", code, out, errs)
			}
			// the MCP door: a usage error carries no hint (there is no command line to point at), the rest are the same object
			_, _, merr := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil)).MCP().CallStructured(context.Background(), json.RawMessage(tc.args))
			if merr == nil {
				t.Fatal("mcp: no error")
			}
			var se interface{ Structured() any }
			if !errors.As(merr, &se) {
				t.Fatalf("mcp error %T has no structured form", merr)
			}
			b, _ := json.Marshal(se.Structured())
			want := tc.wantJSON
			if tc.code == 2 {
				want = `{"error":"reply_to must be a whole number of 1 or more"}`
			}
			if string(b) != want {
				t.Errorf("mcp structured %s, want %s", b, want)
			}
		})
	}
}

func TestSendHelp(t *testing.T) {
	var o, e bytes.Buffer
	fr := cmdcore.Frame{Tool: "mash", Globals: []cmdcore.HelpOption{
		{Flag: "--endpoint", Value: "address", Desc: "Which endpoint to act as, as owner.group.name. Not needed when only one is installed here."},
		{Flag: "--verbose", Short: "-v", Desc: "Print more."},
		{Flag: "--help", Short: "-h", Desc: "Show help for the command."},
	}}
	var first string
	for _, args := range [][]string{{"--help"}, {"-h"}, {"acme.demo.alice", "--kind", "fyi", "-h"}, {"--bogus", "--help"}} {
		o.Reset()
		code := cmdcore.CLIWith(cmds.Send, envOf(&fakeSvc{}, true, nil), fr)(args, nil, &o, &e, cliframe.Global{})
		if code != 0 || e.Len() != 0 {
			t.Fatalf("%v: code %d stderr %q", args, code, e.String())
		}
		got := o.String()
		first = got
		for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if render.DisplayWidth(l) > 80 || l != strings.TrimRight(l, " ") {
				t.Errorf("line %q is wider than 80 columns or has trailing spaces", l)
			}
		}
		at := -1
		for _, h := range []string{"Send a message to an endpoint.\n\nUsage:\n  mash send <address> [<message>] [options]\n\nExamples:\n  mash send acme.demo.alice \"lunch at noon?\"\n",
			"\n\nArguments:\n  address  Who to send to, as owner.group.name.\n  message  The text. Leave it out to read the text from --file or from standard\n",
			"\n\nOptions:\n  --kind <kind>", "\n  --urgent", "\n  --reply-to <number>", "\n  --relay <number>", "\n  --id <key>", "\n  --file <path>",
			"\n  --endpoint <address>", "\n  -h, --help <"} {
			i := strings.Index(got, h)
			if h == "\n  -h, --help <" {
				h, i = "\n  -h, --help ", strings.Index(got, "\n  -h, --help ")
			}
			if i < at || i < 0 {
				t.Fatalf("%v: section %q missing or out of order in\n%s", args, h, got)
			}
			at = i
		}
		if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
			t.Errorf("help must end in one newline: %q", got[len(got)-3:])
		}
	}
	// wrapped descriptions line up under the first
	if !strings.Contains(first, "\n  --kind <kind>         What sort of message this is. blocker, question and task\n"+"                        wake an idle recipient that") {
		t.Errorf("--kind not aligned and wrapped:\n%s", first)
	}
	// no help after "--": it is the message
	f := &fakeSvc{}
	if code := cmdcore.CLIWith(cmds.Send, envOf(f, true, nil), fr)([]string{"a.b.c", "--", "--help"}, nil, &o, &e, cliframe.Global{}); code != 0 || f.text != "--help" {
		t.Errorf("after --: code %d text %q", code, f.text)
	}
	// the version help has no Arguments or Options section
	o.Reset()
	cmdcore.CLIWith(cmds.Version, envOf(&fakeSvc{}, true, nil), fr)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
	if strings.Contains(o.String(), "Arguments:") || strings.Contains(o.String(), "Global options:") || !strings.HasPrefix(o.String(), "Print the version. It can also be run as --version.\n\nUsage:\n  mash version\n\nExamples:\n  mash version\n  mash --version\n\nOptions:\n  -h, --help  Show help for the command.\n") {
		t.Errorf("version help:\n%s", o.String())
	}
}

// An enum value is matched without regard to case, as kind.Parse always has, and reaches the service spelled as allowed.
func TestEnumIgnoresCase(t *testing.T) {
	f := &fakeSvc{}
	if code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a.b.c", "hi", "--kind", "Question"); code != 0 || f.o.Kind != "question" {
		t.Errorf("cli: code %d kind %q %s", code, f.o.Kind, errs)
	}
	if _, _, err := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil)).CallStructured(context.Background(), json.RawMessage(`{"address":"a.b.c","message":"m","kind":"FYI"}`)); err != nil || f.o.Kind != "fyi" {
		t.Errorf("mcp: %v kind %q", err, f.o.Kind)
	}
}

// specDoc reads a file of the specification (the single source for descriptions, outputs and objects).
func specDoc(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// specTool is the tool of spec/mash-mcp.json named name.
func specTool(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, x := range specDoc(t, "mash-mcp.json")["tools"].([]any) {
		if m := x.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("no tool %s in spec/mash-mcp.json", name)
	return nil
}

// specCommand is the command of spec/mash.json named name, with its metadata by name.
func specCommand(t *testing.T, name string) (map[string]any, map[string]any) {
	t.Helper()
	for _, x := range specDoc(t, "mash.json")["command"].(map[string]any)["commands"].([]any) {
		if m := x.(map[string]any); m["name"] == name {
			meta := map[string]any{}
			for _, e := range m["metadata"].([]any) {
				em := e.(map[string]any)
				meta[em["name"].(string)] = em["value"]
			}
			return m, meta
		}
	}
	t.Fatalf("no command %s in spec/mash.json", name)
	return nil, nil
}

// sendCasesComparedInCmdMash are the send x-output cases that are the process's error cases (stderr, exit code). This package
// cannot produce them; cmd/mash compares them, in the file named. An unlisted unhandled case still fails.
var sendCasesComparedInCmdMash = map[string]string{
	"An address in another form":                "cmd/mash/send_cmd_test.go",
	"Nothing arrives on standard input":         "cmd/mash/prerun_errors_test.go",
	"No such endpoint":                          "cmd/mash/send_cmd_test.go",
	"The recipient is suspended or was deleted": "cmd/mash/spec_failures_test.go (TestSendToSuspendedMatchesSpec)",
	"A deleted endpoint here":                   "cmd/mash/deleted_here_test.go (TestEverydayCommandsDeletedHereMatchSpec, reads the spec text)",
	"No credentials here":                       "cmd/mash/spec_failures_test.go (TestMissingCredsMatchesSpec, reads the spec text)",
	"Not signed in":                             "cmd/mash/frame_auth_test.go",
	"mash send --help":                          "cmd/mash/help_cases_test.go (TestHelpCasesMatchSpec)",
	"A send that signing refuses":               "internal/cmdcore/cmds/cmds_test.go (TestSigningRefusalMatchesSpec)",
	"A reply that signing refuses":              "internal/cmdcore/cmds/cmds_test.go (TestSigningRefusalMatchesSpec)",
}

// The descriptions, examples, tool description and objects are the specification's, word for word.
func TestMatchesSpec(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, tc := range []struct {
		name string
		help func() string
	}{
		{"send", func() string {
			var o, e bytes.Buffer
			cmdcore.CLI(cmds.Send, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
			return o.String()
		}},
		{"version", func() string {
			var o, e bytes.Buffer
			cmdcore.CLI(cmds.Version, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
			return o.String()
		}},
	} {
		c, meta := specCommand(t, tc.name)
		help := norm(tc.help())
		want := []string{c["description"].(string)}
		for _, a := range append(anyList(c["arguments"]), anyList(c["options"])...) {
			want = append(want, a["description"].(string))
		}
		for _, w := range want {
			if !strings.Contains(help, norm(w)) {
				t.Errorf("%s --help lacks the spec's text %q", tc.name, w)
			}
		}
		for _, ex := range c["examples"].([]any) {
			if !strings.Contains(tc.help(), "\n  "+ex.(string)+"\n") {
				t.Errorf("%s --help lacks the spec's example %q", tc.name, ex)
			}
		}
		mcp := specTool(t, tc.name)
		if mcp["description"] != toolDescription(t, tc.name) {
			t.Errorf("%s tool description %q, spec %q", tc.name, toolDescription(t, tc.name), mcp["description"])
		}
		// the object: the same fields (the specification's files are read as maps, so the order is not compared here)
		obj, _ := json.Marshal(mcp["structured"])
		var specKeys, gotKeys []string
		specKeys = orderedKeys(t, obj)
		switch tc.name {
		case "send":
			gotKeys = orderedKeys(t, mustJSON(cmds.SendResult{}))
		case "version":
			gotKeys = orderedKeys(t, mustJSON(cmds.VersionResult{}))
		}
		sort.Strings(specKeys)
		sort.Strings(gotKeys)
		if !reflect.DeepEqual(specKeys, gotKeys) {
			t.Errorf("%s object fields %v, spec %v", tc.name, gotKeys, specKeys)
		}
		xk := orderedKeys(t, mustJSON(meta["x-json"]))
		sort.Strings(xk)
		if !reflect.DeepEqual(xk, specKeys) {
			t.Errorf("%s: spec/mash.json x-json and spec/mash-mcp.json structured differ", tc.name)
		}
	}
	// the receipt texts: every x-output case of send, word for word
	_, smeta := specCommand(t, "send")
	for _, x := range smeta["x-output"].([]any) {
		c := x.(map[string]any)
		var r cmds.SendResult
		verbose := false
		switch c["case"] {
		case "Sent":
			r = cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}
		case "Sent, urgent, with a kind":
			r = cmds.SendResult{To: "acme.demo.alice", Number: 43, Kind: "question", Urgent: true}
		case "With --detail":
			r = cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}
			verbose = true
		case "A reply marked question that asks nothing":
			r = cmds.SendResult{To: "acme.demo.alice", Number: 44, Kind: "fyi", Retagged: true}
		case "Repeated --id":
			r = cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task", Duplicate: true}
		default:
			if _, ok := sendCasesComparedInCmdMash[c["case"].(string)]; ok {
				continue
			}
			t.Errorf("send: no test handles the spec case %q", c["case"])
			continue
		}
		if got := r.Text(cmdcore.Opts{Detail: verbose}); got != c["text"] {
			t.Errorf("send output %q: %q, spec %q", c["case"], got, c["text"])
		}
	}
	var mt struct {
		Text []struct{ Text string }
	}
	mb, _ := json.Marshal(specTool(t, "send"))
	_ = json.Unmarshal(mb, &mt)
	if len(mt.Text) == 0 || mt.Text[0].Text != (cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}).Text(cmdcore.Opts{}) {
		t.Errorf("mcp send text %v differs from the receipt", mt.Text)
	}
	// the MCP parameters
	props := cmdcore.Schema[cmds.SendParams]()["properties"].(map[string]any)
	for _, p := range specTool(t, "send")["params"].([]any) {
		pm := p.(map[string]any)
		got, ok := props[pm["name"].(string)].(map[string]any)
		if !ok || got["description"] != pm["description"] {
			t.Errorf("param %v: schema %v", pm["name"], got)
		}
	}
	if len(props) != len(specTool(t, "send")["params"].([]any)) {
		t.Errorf("schema has %d properties, spec %d params", len(props), len(specTool(t, "send")["params"].([]any)))
	}
}

func anyList(v any) []map[string]any {
	var out []map[string]any
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, x.(map[string]any))
	}
	return out
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// orderedKeys are the keys of a JSON object in the order written.
func orderedKeys(t *testing.T, b []byte) []string {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for d.More() {
		k, _ := d.Token()
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := d.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func toolDescription(t *testing.T, name string) string {
	t.Helper()
	switch name {
	case "send":
		return cmdcore.MCPTool(cmds.Send, nil).Description
	}
	return cmdcore.MCPTool(cmds.Version, nil).Description
}

// A flat, slashed, upper-case or short address is refused in both doors before anything is sent: exit 2 and nothing on
// stdout in text form; {"error","hint"} with --json; an MCP error with the same pair.
func TestSendRefusesOtherAddressForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"acme_demo_alice", "acme.demo.alice"},
		{"acme/demo/alice", "acme.demo.alice"},
		{"Acme.Demo.Alice", "acme.demo.alice"},
		{"ACME_DEMO_ALICE", "acme.demo.alice"},
		{"alice", "owner.group.name"},
		{"demo.alice", "owner.group.name"},
		{"a.b.c.d", "owner.group.name"},
		{"acme_demo", "owner.group.name"},
	} {
		msg := tc.in + " is not an address; write it as " + tc.want
		f := &fakeSvc{}
		code, out, errs := runSend(f, "", nil, cliframe.Global{}, tc.in, "hi")
		if code != 2 || out != "" || errs != "error: "+msg+"\nhint: mash send --help\n" || f.calls != 0 {
			t.Errorf("%s: code %d stdout %q stderr %q calls %d", tc.in, code, out, errs, f.calls)
		}
		code, out, errs = runSend(f, "", nil, cliframe.Global{JSON: true}, tc.in, "hi")
		wantJSON, _ := json.Marshal(map[string]string{"error": msg, "hint": "mash send --help"})
		if code != 2 || errs != "" || out != string(wantJSON)+"\n" {
			t.Errorf("%s --json: code %d stdout %q stderr %q", tc.in, code, out, errs)
		}
		args, _ := json.Marshal(map[string]string{"address": tc.in, "message": "hi"})
		_, structured, err := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil)).CallStructured(context.Background(), args)
		if err == nil || err.Error() != "error: "+msg+"\nhint: mash send --help" || structured != nil || f.calls != 0 {
			t.Errorf("%s mcp: %v %v calls %d", tc.in, err, structured, f.calls)
		}
		var se interface{ Structured() any }
		if !errors.As(err, &se) || !reflect.DeepEqual(se.Structured(), map[string]any{"error": msg, "hint": "mash send --help"}) {
			t.Errorf("%s mcp structured error: %v", tc.in, err)
		}
	}
}

// A retag is in the receipt of both doors: --json and structuredContent carry "retagged": true, and false otherwise.
func TestSendRetaggedFlag(t *testing.T) {
	for _, tc := range []struct {
		rc   cmds.SendReceipt
		want string
	}{
		{cmds.SendReceipt{Number: 42, Kind: "fyi", Retagged: true}, `{"to":"acme.demo.alice","number":42,"kind":"fyi","urgent":false,"duplicate":false,"retagged":true}`},
		{cmds.SendReceipt{Number: 42}, `{"to":"acme.demo.alice","number":42,"kind":"task","urgent":false,"duplicate":false,"retagged":false}`},
	} {
		f := &fakeSvc{rc: tc.rc}
		_, out, _ := runSend(f, "", nil, cliframe.Global{JSON: true}, "acme.demo.alice", "ok")
		if out != tc.want+"\n" {
			t.Errorf("--json %q, want %q", out, tc.want)
		}
		_, structured, err := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil)).CallStructured(context.Background(), json.RawMessage(`{"address":"acme.demo.alice","message":"ok"}`))
		if err != nil || string(mustJSON(structured)) != tc.want {
			t.Errorf("structured %s, %v; want %s", mustJSON(structured), err, tc.want)
		}
	}
}

// A pipe that is never written to does not block the command for ever: the read gives up at the deadline, and that is
// a failure (exit 1), not a usage error.
func TestImplicitStdinHasADeadline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // held open, never written
	for _, asJSON := range []bool{false, true} {
		var o, e bytes.Buffer
		f := &fakeSvc{}
		env := func(context.Context) (*cmdcore.Env, error) {
			return &cmdcore.Env{Svc: f, StdinTimeout: 100 * time.Millisecond}, nil
		}
		start := time.Now()
		code := cmdcore.CLI(cmds.Send, env)([]string{"a.b.c"}, r, &o, &e, cliframe.Global{JSON: asJSON})
		if time.Since(start) > 5*time.Second || code != 1 || f.calls != 0 {
			t.Fatalf("json=%v: code %d calls %d after %v", asJSON, code, f.calls, time.Since(start))
		}
		if asJSON && (e.String() != "" || o.String() != `{"error":"no message text arrived on standard input"}`+"\n") {
			t.Errorf("json: %q %q", o.String(), e.String())
		}
		if !asJSON && (o.String() != "" || e.String() != "error: no message text arrived on standard input\n") {
			t.Errorf("text: %q %q", o.String(), e.String())
		}
	}
}

// TO-8: a send, or a stored-mail reply (send --reply-to), that signing refuses prints the spec's one line on standard error
// with no hint, exits 1, and the MCP tool gives the same words as its error. The words are the endpoint's (internal/endpoint).
func TestSigningRefusalMatchesSpec(t *testing.T) {
	_, meta := specCommand(t, "send")
	cause := errors.New("not sent: could not sign: your name is too long")
	for _, x := range meta["x-output"].([]any) {
		c := x.(map[string]any)
		var args []string
		switch c["case"] {
		case "A send that signing refuses":
			args = []string{"acme.demo.bob", "ready?"}
		case "A reply that signing refuses":
			args = []string{"acme.demo.bob", "yes", "--reply-to", "2"}
		default:
			continue
		}
		code, out, errs := runSend(&fakeSvc{err: cause}, "", nil, cliframe.Global{}, args...)
		if code != 1 || out != "" || errs != c["text"].(string)+"\n" || c["stream"] != "stderr" || int(c["exit"].(float64)) != 1 {
			t.Errorf("%v: %d %q %q, spec %q", c["case"], code, out, errs, c["text"])
		}
	}
	for _, e := range specTool(t, "send")["errors"].([]any) {
		m := e.(map[string]any)
		if m["case"] == "A send that signing refuses" && m["text"] != "error: "+cause.Error() {
			t.Errorf("mcp send error %q", m["text"])
		}
	}
}
