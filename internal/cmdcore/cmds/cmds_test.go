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
	"strings"
	"testing"

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
		{"bad number", []string{"a", "hi", "--reply-to", "x"}, "error: --reply-to must be a whole number\n" + hint},
		{"bad bool", []string{"a", "hi", "--urgent=maybe"}, "error: --urgent must be true or false\n" + hint},
		{"extra positional", []string{"a", "hi", "more"}, "error: unexpected argument \"more\" for mash send\n" + hint},
		{"both message and file", []string{"a", "hi", "--file", "/m"}, "error: mash send takes the message text or --file, not both\n" + hint},
		{"reply-to zero", []string{"a", "hi", "--reply-to", "0"}, "error: --reply-to must be a whole number of 1 or more\n" + hint},
		{"relay negative", []string{"a", "hi", "--relay=-3"}, "error: --relay must be a whole number of 1 or more\n" + hint},
		{"reply-to fraction", []string{"a", "hi", "--reply-to", "2.5"}, "error: --reply-to must be a whole number\n" + hint},
		{"empty address", []string{"", "hi"}, "error: mash send needs <address>\n" + hint},
		{"blank address", []string{"  ", "hi"}, "error: mash send needs <address>\n" + hint},
		{"kind twice", []string{"a", "hi", "--kind", "fyi", "--kind=task"}, "error: --kind was given twice\n" + hint},
		{"urgent twice", []string{"a", "hi", "--urgent", "--urgent"}, "error: --urgent was given twice\n" + hint},
		{"id twice", []string{"a", "hi", "--id", "x", "--id", "y"}, "error: --id was given twice\n" + hint},
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
	got, _, err := tool.MCP().CallStructured(context.Background(), json.RawMessage(`{"address":"a","message":"m"}`))
	if err != nil || got != "Mail sent." {
		t.Errorf("MCP().CallStructured = %q, %v", got, err)
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
		`"relay":{"description":"The number of a message in your inbox that this passes on. Signed. (Today the MCP tool calls this 'via'.)","minimum":1,"type":"integer"},` +
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
		code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a", "hi", "--urgent="+v)
		if code != 0 || f.o.Urgent != want {
			t.Errorf("--urgent=%s: code %d urgent %v stderr %q", v, code, f.o.Urgent, errs)
		}
	}
	for _, v := range []string{"1", "maybe", "", "y"} {
		code, _, errs := runSend(&fakeSvc{}, "", nil, cliframe.Global{}, "a", "hi", "--urgent="+v)
		if code != 2 || errs != "error: --urgent must be true or false\nhint: mash send --help\n" {
			t.Errorf("--urgent=%q: code %d stderr %q", v, code, errs)
		}
	}
}

func TestReplyToNumbers(t *testing.T) {
	f := &fakeSvc{}
	if code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a", "hi", "--reply-to", "1"); code != 0 || f.o.ReplyTo != 1 {
		t.Fatalf("code %d %q %+v", code, errs, f.o)
	}
	tool := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil))
	call := func(args string) error {
		_, _, err := tool.CallStructured(context.Background(), json.RawMessage(args))
		return err
	}
	f.o = cmds.SendOptions{}
	if err := call(`{"address":"a","message":"m","reply_to":2.0,"relay":3e0}`); err != nil || f.o.ReplyTo != 2 || f.o.Relay != 3 {
		t.Errorf("2.0 and 3e0 are integers: %v %+v", err, f.o)
	}
	for args, want := range map[string]string{
		`{"address":"a","message":"m","reply_to":2.5}`: "error: reply_to must be a whole number",
		`{"address":"a","message":"m","reply_to":0}`:   "error: reply_to must be a whole number of 1 or more",
		`{"address":"a","message":"m","relay":-1}`:     "error: relay must be a whole number of 1 or more",
		`{"address":"a","message":"m","relay":1e400}`:  "error: relay must be a whole number",
		`{"address":"a","message":"m","reply_to":"2"}`: "error: reply_to has the wrong type",
		`{"address":"","message":"m"}`:                 "error: address is required",
		`{"address":"a","message":"  "}`:               "error: message is required",
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
		return cmdcore.CLI(cmds.Send, env)([]string{"a", "hi"}, nil, &o, &e, cliframe.Global{})
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
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"a","message":"m"}`)); err == nil || !strings.HasPrefix(err.Error(), "error: internal: ") {
		t.Errorf("mcp nil env func: %v", err)
	}
	tool = cmdcore.MCPTool(cmds.Send, func(context.Context) (*cmdcore.Env, error) { return nil, nil })
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"address":"a","message":"m"}`)); err == nil || !strings.HasPrefix(err.Error(), "error: internal: ") {
		t.Errorf("mcp nil env: %v", err)
	}
	if _, err := cmds.Send.Run(context.Background(), nil, cmds.SendParams{Address: "a", Message: "m"}); err == nil {
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
	code := cmdcore.CLI(cmds.Send, env)([]string{"a"}, null, &o, &e, cliframe.Global{})
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
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a"}, r, &o, &e, cliframe.Global{}); code != 0 || f.text != "from a pipe" {
		t.Errorf("pipe: code %d text %q stderr %q", code, f.text, e.String())
	}
	file := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(file, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, _ := os.Open(file)
	defer in.Close()
	if code := cmdcore.CLI(cmds.Send, env)([]string{"a"}, in, &o, &e, cliframe.Global{}); code != 0 || f.text != "from a file" {
		t.Errorf("file: code %d text %q", code, f.text)
	}
}

func TestOneTrailingNewlineDropped(t *testing.T) {
	for in, want := range map[string]string{
		"a\n": "a", "a\r\n": "a", "a\n\n": "a\n", "a\r\n\r\n": "a\r\n", "a": "a", "a\r": "a\r", "a b\nc\n": "a b\nc",
	} {
		f := &fakeSvc{}
		if code, _, errs := runSend(f, in, nil, cliframe.Global{}, "a", "--file", "-"); code != 0 || f.text != want {
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
			`{"error":"no endpoint has the address acme.demo.alise","hint":"mash who"}`},
		{"not signed in", []string{"a", "hi"}, `{"address":"a","message":"hi"}`, &cmds.SignInError{Addr: "acme.demo.bob"}, 1,
			`{"error":"acme.demo.bob cannot sign in: the server refused its credentials","hint":"mash doctor"}`},
		{"server not answering", []string{"a", "hi"}, `{"address":"a","message":"hi"}`, cmdcore.ServerNotAnswering("tls://h:4222"), 1,
			`{"error":"the server did not answer: tls://h:4222","hint":"mash doctor"}`},
		{"no hint", []string{"a", "hi"}, `{"address":"a","message":"hi"}`, errors.New("boom"), 1, `{"error":"boom"}`},
		{"usage", []string{"a", "hi", "--reply-to", "0"}, `{"address":"a","message":"hi","reply_to":0}`, nil, 2,
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
			"\n\nGlobal options:\n  --endpoint <address>", "\n  -v, --verbose", "\n  -h, --help <"} {
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
	if !strings.Contains(first, "\n  --kind <kind>        What sort of message this is. blocker, question and task\n"+"                       wake an idle recipient;") {
		t.Errorf("--kind not aligned and wrapped:\n%s", first)
	}
	// no help after "--": it is the message
	f := &fakeSvc{}
	if code := cmdcore.CLIWith(cmds.Send, envOf(f, true, nil), fr)([]string{"a", "--", "--help"}, nil, &o, &e, cliframe.Global{}); code != 0 || f.text != "--help" {
		t.Errorf("after --: code %d text %q", code, f.text)
	}
	// the version help has no Arguments or Options section
	o.Reset()
	cmdcore.CLIWith(cmds.Version, envOf(&fakeSvc{}, true, nil), fr)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
	if strings.Contains(o.String(), "Arguments:") || strings.Contains(o.String(), "\nOptions:") || !strings.HasPrefix(o.String(), "Print the version.\n\nUsage:\n  mash version\n\nExamples:\n  mash version\n\nGlobal options:\n") {
		t.Errorf("version help:\n%s", o.String())
	}
}

// An enum value is matched without regard to case, as kind.Parse always has, and reaches the service spelled as allowed.
func TestEnumIgnoresCase(t *testing.T) {
	f := &fakeSvc{}
	if code, _, errs := runSend(f, "", nil, cliframe.Global{}, "a", "hi", "--kind", "Question"); code != 0 || f.o.Kind != "question" {
		t.Errorf("cli: code %d kind %q %s", code, f.o.Kind, errs)
	}
	if _, _, err := cmdcore.MCPTool(cmds.Send, envOf(f, false, nil)).CallStructured(context.Background(), json.RawMessage(`{"address":"a","message":"m","kind":"FYI"}`)); err != nil || f.o.Kind != "fyi" {
		t.Errorf("mcp: %v kind %q", err, f.o.Kind)
	}
}
