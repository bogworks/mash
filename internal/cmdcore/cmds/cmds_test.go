package cmds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
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
		{"lone dash is text", []string{"a.b.c", "-"}, "", nil, "a.b.c", "-", cmds.SendOptions{Kind: "task"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSvc{rc: cmds.SendReceipt{Number: 42}}
			code, out, errs := runSend(f, tc.stdin, tc.files, cliframe.Global{}, tc.args...)
			if code != 0 || errs != "" {
				t.Fatalf("code %d stderr %q", code, errs)
			}
			if out != "Mail sent.\n" && !strings.HasSuffix(out, "mail sent.\n") {
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
		{"unknown option", []string{"a", "hi", "--bogus"}, "error: unknown option --bogus for mash send\n" + hint},
		{"unknown option with value", []string{"a", "hi", "--bogus=1"}, "error: unknown option --bogus for mash send\n" + hint},
		{"unknown short", []string{"a", "hi", "-z"}, "error: unknown option -z for mash send\n" + hint},
		{"no address", nil, "error: mash send needs <address>\n" + hint},
		{"flag without value", []string{"a", "hi", "--kind"}, "error: --kind needs a value\n" + hint},
		{"bad enum", []string{"a", "hi", "--kind", "loud"}, "error: --kind must be one of blocker, question, task, result, fyi\n" + hint},
		{"bad number", []string{"a", "hi", "--reply-to", "x"}, "error: --reply-to must be a whole number, not \"x\"\n" + hint},
		{"bad bool", []string{"a", "hi", "--urgent=maybe"}, "error: --urgent takes yes or no, not \"maybe\"\n" + hint},
		{"extra positional", []string{"a", "hi", "more"}, "error: unexpected argument \"more\" for mash send\n" + hint},
		{"both message and file", []string{"a", "hi", "--file", "/m"}, "error: mash send takes the message text or --file, not both\n" + hint},
		{"positional flag name", []string{"a", "--address", "x"}, "error: unknown option --address for mash send\n" + hint},
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
	code := cmdcore.CLI(cmds.Send, env)([]string{"a"}, nil, &o, &e, cliframe.Global{})
	if code != 2 || e.String() != "error: mash send needs the message text\nhint: mash send --help\n" {
		t.Errorf("no text: %d %q", code, e.String())
	}
	// empty piped text
	code, _, errs := runSend(&fakeSvc{}, "\n", nil, cliframe.Global{}, "a")
	if code != 2 || !strings.HasPrefix(errs, "error: mash send needs the message text") {
		t.Errorf("empty stdin: %d %q", code, errs)
	}
	// usage error in --json mode
	code, out, _ := runSend(&fakeSvc{}, "", nil, cliframe.Global{JSON: true}, "a", "hi", "--bogus")
	if code != 2 || out != `{"error":"unknown option --bogus for mash send","hint":"mash send --help"}`+"\n" {
		t.Errorf("json usage: %d %q", code, out)
	}
}

func TestSendFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"no such endpoint", fmt.Errorf("lookup: %w", cmds.ErrNoSuchEndpoint), "error: no endpoint has the address acme.demo.alise\nhint: mash who\n"},
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
	code, _, errs := runSend(&fakeSvc{}, "", nil, cliframe.Global{}, "a", "--file", "/nope")
	if code != 1 || !strings.HasPrefix(errs, "error: cannot read the message: no such file /nope") {
		t.Errorf("file: %d %q", code, errs)
	}
	var o, e bytes.Buffer
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil }
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a", "hi"}, nil, &o, &e, cliframe.Global{}); code != 1 {
		t.Errorf("no service: %d", code)
	}
	envErr := func(context.Context) (*cmdcore.Env, error) {
		return nil, cliframe.Failf("not set up").WithHint("mash setup")
	}
	e.Reset()
	if code := cmdcore.CLI(cmds.Send, envErr)([]string{"a", "hi"}, nil, &o, &e, cliframe.Global{}); code != 1 || e.String() != "error: not set up\nhint: mash setup\n" {
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
		{"sent", cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}, false, "Mail sent."},
		{"no kind", cmds.SendResult{}, false, "Mail sent."},
		{"urgent task", cmds.SendResult{Kind: "task", Urgent: true}, false, "Urgent mail sent."},
		{"question", cmds.SendResult{Kind: "question"}, false, "Question mail sent."},
		{"urgent question", cmds.SendResult{Kind: "question", Urgent: true}, false, "Urgent question mail sent."},
		{"duplicate", cmds.SendResult{Kind: "task", Duplicate: true}, false, "Mail already sent; nothing new stored."},
		{"verbose", cmds.SendResult{To: "acme.demo.alice", Number: 42, Kind: "task"}, true,
			"Mail sent.\nto:      acme.demo.alice\nnumber:  42\nkind:    task\nurgent:  no"},
		{"verbose urgent", cmds.SendResult{To: "acme.demo.alice", Number: 7, Kind: "fyi", Urgent: true}, true,
			"Urgent fyi mail sent.\nto:      acme.demo.alice\nnumber:  7\nkind:    fyi\nurgent:  yes"},
	} {
		if got := tc.r.Text(cmdcore.Opts{Verbose: tc.verbose}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
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
			reg.RunCLI(tc.cmd, tc.argv, strings.NewReader(""), &textOut, &errs, cliframe.Global{})

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
	if tool.Name != "send" || tool.Description != "Send a message to an endpoint." {
		t.Errorf("tool %q %q", tool.Name, tool.Description)
	}
	_, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"acme.demo.alise","message":"hi"}`))
	if err == nil || err.Error() != "error: no endpoint has the address acme.demo.alise\nhint: mash who" {
		t.Errorf("err = %v", err)
	}
	for _, bad := range []string{`{"address":"a"}`, `{"message":"m"}`, `{"address":"a","message":"m","kind":"loud"}`, `{"address":"a","message":"m","file":"x"}`, `{"address":"a","message":"m","reply_to":"x"}`, `[]`} {
		if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(bad)); err == nil || !strings.HasPrefix(err.Error(), "error: ") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	// the text-only mcp.Tool
	f.err = nil
	f.rc.Number = 5
	got, err := tool.MCP().Call(context.Background(), json.RawMessage(`{"address":"a","message":"m"}`))
	if err != nil || got != "Mail sent." {
		t.Errorf("MCP().Call = %q, %v", got, err)
	}
}

// TestSendSchema pins the tool's input schema to the published parameters.
func TestSendSchema(t *testing.T) {
	s := cmdcore.Schema[cmds.SendParams]()
	b, _ := json.Marshal(s)
	want := `{"additionalProperties":false,"properties":{` +
		`"address":{"description":"Who to send to, as owner.group.name.","type":"string"},` +
		`"id":{"description":"A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice.","type":"string"},` +
		`"kind":{"description":"What sort of message this is. blocker, question and task wake an idle recipient; result and fyi wait for its next turn. Default: task.","enum":["blocker","question","task","result","fyi"],"type":"string"},` +
		`"message":{"description":"The text.","type":"string"},` +
		`"relay":{"description":"The number of a message in your inbox that this passes on. Signed. (Today the MCP tool calls this 'via'.)","type":"integer"},` +
		`"reply_to":{"description":"The number of the message in your inbox that this answers. Signed, so the recipient can trust the link.","type":"integer"},` +
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
