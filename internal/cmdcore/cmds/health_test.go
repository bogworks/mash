package cmds_test

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
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
)

type fakeHealth struct {
	rep cmds.HealthReport
	err error
}

func (f *fakeHealth) Health(context.Context) (cmds.HealthReport, error) { return f.rep, f.err }

func ok(name, detail string) cmds.HealthCheck {
	return cmds.HealthCheck{Name: name, State: cmds.CheckOK, Detail: detail}
}

// allWell is the report of the specification's "All well" case.
func allWell() *fakeHealth {
	return &fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", Checks: []cmds.HealthCheck{
		ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", ""), ok("refused", "no unverified mail was refused")}}}
}

// refusedSignIn is the report of the specification's "A problem and a warning" case.
func refusedSignIn() *fakeHealth {
	return &fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", Checks: []cmds.HealthCheck{
		ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"),
		{Name: "sign-in", State: cmds.CheckFail, Detail: "the server refused these credentials", Reason: "the server refused its credentials", Fix: "mashctl endpoint reissue acme.demo.bob"},
		{Name: "refused", State: cmds.CheckWarn, Detail: "3 unverified messages were refused"}}}}
}

func healthEnv(svc any) cmdcore.EnvFunc {
	return func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: svc}, nil }
}

func runStatus(svc any, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Status, healthEnv(svc))(args, strings.NewReader(""), &o, &e, g)
	return code, o.String(), e.String()
}

func runDoctor(svc any, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Doctor, healthEnv(svc))(args, strings.NewReader(""), &o, &e, g)
	return code, o.String(), e.String()
}

func report(endpoint string, online bool, checks ...cmds.HealthCheck) *fakeHealth {
	return &fakeHealth{rep: cmds.HealthReport{Endpoint: endpoint, Online: online, Checks: checks}}
}

// Every status case of the specification: the line on stdout, the hint on stderr, the exit code.
func TestStatusMatchesSpec(t *testing.T) {
	c, meta := specCommand(t, "status")
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var o, e bytes.Buffer
	cmdcore.CLI(cmds.Status, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
	help := o.String()
	if !strings.Contains(norm(help), norm(c["description"].(string))) {
		t.Errorf("status --help lacks the spec's description")
	}
	for _, ex := range c["examples"].([]any) {
		if !strings.Contains(help, "\n  "+ex.(string)+"\n") {
			t.Errorf("status --help lacks the spec's example %q", ex)
		}
	}
	if got := cmdcore.MCPTool(cmds.Status, nil).Description; got != specTool(t, "status")["description"] || got != c["description"] {
		t.Errorf("tool description %q", got)
	}
	if cmds.Status.Group != meta["x-group"] {
		t.Errorf("group %q, spec %v", cmds.Status.Group, meta["x-group"])
	}
	if props := cmdcore.Schema[cmds.StatusParams]()["properties"].(map[string]any); len(props) != 0 || len(specTool(t, "status")["params"].([]any)) != 0 {
		t.Errorf("status takes no parameters: %v", props)
	}
	// the object
	st := runStatusObject(t, report("acme.demo.bob", false, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", "")))
	if !sameJSON(t, st, string(mustJSON(meta["x-json"]))) || !sameJSON(t, st, string(mustJSON(specTool(t, "status")["structured"]))) {
		t.Errorf("object %s, spec %v", st, meta["x-json"])
	}
	fails := map[string]*fakeHealth{
		"Not working (the first line on stdout, the hint on stderr)": report("acme.demo.bob", false, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"),
			cmds.HealthCheck{Name: "sign-in", State: cmds.CheckFail, Detail: "the server refused these credentials", Reason: "the server refused its credentials"}),
		"The server does not answer": report("acme.demo.bob", false, ok("config", ""), ok("creds", ""),
			cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: "the server did not answer: tls://myserver:4222"}),
	}
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		text := oc["text"].(string)
		var code int
		var out, errs string
		switch oc["case"] {
		case "Working, nothing holding it open":
			code, out, errs = runStatus(report("acme.demo.bob", false, ok("config", ""), ok("sign-in", "")), cliframe.Global{})
		case "Working, an agent session is holding it open":
			code, out, errs = runStatus(report("acme.demo.bob", true, ok("config", ""), ok("sign-in", "")), cliframe.Global{})
		case "No endpoint chosen and several installed":
			code, out, errs = runStatus(&fakeHealth{err: &config.SeveralEndpointsError{Addresses: []string{"acme.demo.alice", "acme.demo.bob"}}}, cliframe.Global{})
			// the spec's text is the error, then a hint; the hint names the first address installed (the spec's example names the second)
			wantErr := strings.SplitN(text, "\n", 2)[0] + "\n"
			if code != 2 || out != "" || !strings.HasPrefix(errs, wantErr) || errs != wantErr+"hint: mash status --endpoint acme.demo.alice\n" {
				t.Errorf("%s: %d %q %q, spec %q", oc["case"], code, out, errs, text)
			}
			continue
		default:
			f, found := fails[oc["case"].(string)]
			if !found {
				t.Errorf("unknown spec case %v", oc["case"])
				continue
			}
			code, out, errs = runStatus(f, cliframe.Global{})
		}
		stdout, hint, _ := strings.Cut(text, "\nhint: ")
		wantErrs := ""
		if hint != "" {
			wantErrs = "hint: " + hint + "\n"
		}
		if out != stdout+"\n" || errs != wantErrs || code != int(oc["exit"].(float64)) {
			t.Errorf("%s: %d %q %q, spec %q (exit %v)", oc["case"], code, out, errs, text, oc["exit"])
		}
	}
}

func runStatusObject(t *testing.T, f *fakeHealth) string {
	t.Helper()
	code, out, _ := runStatus(f, cliframe.Global{JSON: true})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	return out
}

func TestStatusStates(t *testing.T) {
	// not ready, with each failing row; the first one that fails gives the reason; the refused row never does
	f := report("a.b.c", true, ok("config", ""),
		cmds.HealthCheck{Name: "creds", State: cmds.CheckFail, Detail: "the creds file is missing: /x"},
		cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: "the server did not answer: tls://h:1"})
	code, out, errs := runStatus(f, cliframe.Global{})
	if code != 1 || out != "a.b.c: not ready: the creds file is missing: /x\n" || errs != "hint: mash doctor\n" {
		t.Errorf("%d %q %q", code, out, errs)
	}
	code, out, _ = runStatus(f, cliframe.Global{JSON: true})
	if code != 1 || out != `{"endpoint":"a.b.c","ready":false,"online":false,"reason":"the creds file is missing: /x"}`+"\n" {
		t.Errorf("json: %d %q", code, out)
	}
	// warnings and the refused row do not make it not ready
	code, out, errs = runStatus(report("a.b.c", true, ok("config", ""), cmds.HealthCheck{Name: "server", State: cmds.CheckWarn, Detail: "plain"},
		cmds.HealthCheck{Name: "refused", State: cmds.CheckFail, Detail: "x"}), cliframe.Global{})
	if code != 0 || out != "a.b.c: ready, online\n" || errs != "" {
		t.Errorf("warn: %d %q %q", code, out, errs)
	}
	// no endpoint: named "mash", the endpoint is null
	code, out, _ = runStatus(report("", false, cmds.HealthCheck{Name: "config", State: cmds.CheckFail, Detail: "no endpoint is chosen for this session"}), cliframe.Global{})
	if code != 1 || out != "mash: not ready: no endpoint is chosen for this session\n" {
		t.Errorf("no endpoint: %d %q", code, out)
	}
	_, out, _ = runStatus(report("", false, cmds.HealthCheck{Name: "config", State: cmds.CheckFail, Detail: "d"}), cliframe.Global{JSON: true})
	if out != `{"endpoint":null,"ready":false,"online":false,"reason":"d"}`+"\n" {
		t.Errorf("no endpoint json: %q", out)
	}
	// a control character in a recorded name or reason does not reach the terminal
	_, out, _ = runStatus(report("a.b.c", false, cmds.HealthCheck{Name: "creds", State: cmds.CheckFail, Detail: "bad\x1b[31m"}), cliframe.Global{})
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("raw escape in %q", out)
	}
}

func TestStatusAndDoctorErrors(t *testing.T) {
	for _, cmd := range []string{"status", "doctor"} {
		run := map[string]func(any, cliframe.Global, ...string) (int, string, string){"status": runStatus, "doctor": runDoctor}[cmd]
		if code, out, errs := run(&fakeHealth{err: errors.New("boom")}, cliframe.Global{}); code != 1 || out != "" || errs != "error: boom\n" {
			t.Errorf("%s error: %d %q %q", cmd, code, out, errs)
		}
		if code, _, errs := run(allWell(), cliframe.Global{}, "extra"); code != 2 || !strings.HasSuffix(errs, "\nhint: mash "+cmd+" --help\n") {
			t.Errorf("%s extra: %d %q", cmd, code, errs)
		}
		if code, _, _ := run(nil, cliframe.Global{}); code != 1 {
			t.Errorf("%s no service: %d", cmd, code)
		}
		// --json of a several-installed error is the error object, exit 2
		code, out, errs := run(&fakeHealth{err: &config.SeveralEndpointsError{Addresses: []string{"a.b.c", "a.b.d"}}}, cliframe.Global{JSON: true})
		if code != 2 || errs != "" || out != `{"error":"more than one endpoint is installed here: a.b.c, a.b.d","hint":"mash `+cmd+` --endpoint a.b.c"}`+"\n" {
			t.Errorf("%s several, json: %d %q %q", cmd, code, out, errs)
		}
	}
}

// Every doctor case of the specification, word for word.
func TestDoctorMatchesSpec(t *testing.T) {
	c, meta := specCommand(t, "doctor")
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var o, e bytes.Buffer
	cmdcore.CLI(cmds.Doctor, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
	help := o.String()
	if !strings.Contains(norm(help), norm(c["description"].(string))) {
		t.Errorf("doctor --help lacks the spec's description")
	}
	for _, ex := range c["examples"].([]any) {
		if !strings.Contains(help, "\n  "+ex.(string)+"\n") {
			t.Errorf("doctor --help lacks the spec's example %q", ex)
		}
	}
	if got := cmdcore.MCPTool(cmds.Doctor, nil).Description; got != specTool(t, "doctor")["description"] || got != c["description"] {
		t.Errorf("tool description %q", got)
	}
	if cmds.Doctor.Group != meta["x-group"] {
		t.Errorf("group %q, spec %v", cmds.Doctor.Group, meta["x-group"])
	}
	if props := cmdcore.Schema[cmds.DoctorParams]()["properties"].(map[string]any); len(props) != 0 || len(specTool(t, "doctor")["params"].([]any)) != 0 {
		t.Errorf("doctor takes no parameters: %v", props)
	}
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		var f *fakeHealth
		switch oc["case"] {
		case "All well":
			f = allWell()
		case "A problem and a warning":
			f = refusedSignIn()
		default:
			t.Errorf("unknown spec case %v", oc["case"])
			continue
		}
		stdout, hint, _ := strings.Cut(oc["text"].(string), "\nhint: ")
		wantErrs := ""
		if hint != "" {
			wantErrs = "hint: " + hint + "\n"
		}
		code, out, errs := runDoctor(f, cliframe.Global{})
		if out != stdout+"\n" || errs != wantErrs || code != int(oc["exit"].(float64)) {
			t.Errorf("%s: %d %q %q\nspec %q", oc["case"], code, out, errs, oc["text"])
		}
	}
	// the object: the spec's x-json lists the failing check; ours lists every row, and the failing one is the spec's
	_, out, _ := runDoctor(refusedSignIn(), cliframe.Global{JSON: true})
	var got, spec struct {
		Endpoint string
		Problems int
		Warnings int
		Checks   []map[string]any
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustJSON(meta["x-json"]), &spec); err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != spec.Endpoint || got.Problems != spec.Problems || got.Warnings != spec.Warnings || len(got.Checks) != 5 {
		t.Errorf("object %s, spec x-json %v", out, meta["x-json"])
	}
	if !reflect.DeepEqual(got.Checks[3], spec.Checks[0]) {
		t.Errorf("the failing check %v, spec %v", got.Checks[3], spec.Checks[0])
	}
	if !sameJSON(t, string(mustJSON(meta["x-json"])), string(mustJSON(specTool(t, "doctor")["structured"]))) {
		t.Error("spec/mash.json x-json and spec/mash-mcp.json structured differ")
	}
	if !reflect.DeepEqual(orderedKeys(t, []byte(out)), []string{"endpoint", "problems", "warnings", "checks"}) {
		t.Errorf("key order %v", orderedKeys(t, []byte(out)))
	}
	// every row has the four keys; null where nothing is said
	if !strings.Contains(out, `{"name":"config","state":"ok","detail":null,"fix":null}`) || !strings.Contains(out, `{"name":"server","state":"ok","detail":"tls://myserver:4222","fix":null}`) {
		t.Errorf("rows: %s", out)
	}
}

func TestDoctorSummaryAndHint(t *testing.T) {
	warn := cmds.HealthCheck{Name: "refused", State: cmds.CheckWarn, Detail: "2 unverified messages were refused"}
	fail := func(fix string) cmds.HealthCheck {
		return cmds.HealthCheck{Name: "creds", State: cmds.CheckFail, Detail: "the creds file is missing: /x", Fix: fix}
	}
	for _, tc := range []struct {
		name    string
		checks  []cmds.HealthCheck
		summary string
		code    int
		hint    string
	}{
		{"warnings only", []cmds.HealthCheck{ok("config", ""), warn}, "1 warning.", 0, ""},
		{"two warnings", []cmds.HealthCheck{warn, warn}, "2 warnings.", 0, ""},
		{"two problems", []cmds.HealthCheck{fail(""), fail("")}, "2 problems.", 1, ""},
		{"two problems and warnings", []cmds.HealthCheck{fail(""), fail(""), warn, warn}, "2 problems, 2 warnings.", 1, ""},
		{"mashctl fix", []cmds.HealthCheck{fail("mashctl endpoint reissue a.b.c")}, "1 problem.", 1, "ask whoever runs your mesh to run: mashctl endpoint reissue a.b.c"},
		{"other fix", []cmds.HealthCheck{fail("chmod 600 /x")}, "1 problem.", 1, "chmod 600 /x"},
		{"first fixable only", []cmds.HealthCheck{fail(""), fail("chmod 600 /x"), fail("mashctl x")}, "3 problems.", 1, "chmod 600 /x"},
		{"a warn's fix is no hint", []cmds.HealthCheck{{Name: "refused", State: cmds.CheckWarn, Detail: "w", Fix: "mashctl y"}}, "1 warning.", 0, ""},
	} {
		code, out, errs := runDoctor(report("a.b.c", false, tc.checks...), cliframe.Global{})
		if !strings.HasSuffix(out, "\n\n"+tc.summary+"\n") || code != tc.code {
			t.Errorf("%s: %d %q", tc.name, code, out)
		}
		want := ""
		if tc.hint != "" {
			want = "hint: " + tc.hint + "\n"
		}
		if errs != want {
			t.Errorf("%s: stderr %q, want %q", tc.name, errs, want)
		}
	}
	// the layout: name in 10 columns, state in 6, then the detail; no trailing spaces
	_, out, _ := runDoctor(report("a.b.c", false, ok("sign-in", ""), cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: "d"}), cliframe.Global{})
	if out != "sign-in   ok\nserver    FAIL  d\n\n1 problem.\n" {
		t.Errorf("layout %q", out)
	}
	// no rows at all still says No problems.
	if _, out, _ := runDoctor(report("", false), cliframe.Global{}); out != "\nNo problems.\n" {
		t.Errorf("empty %q", out)
	}
}

// The doors agree: --json is the structured result, and the MCP text is the stdout followed by the stderr hint.
func TestStatusAndDoctorDoorsAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  string
		f    *fakeHealth
	}{
		{"status ready", "status", allWell()},
		{"status online", "status", &fakeHealth{rep: cmds.HealthReport{Endpoint: "a.b.c", Online: true, Checks: allWell().rep.Checks}}},
		{"status not ready", "status", refusedSignIn()},
		{"doctor well", "doctor", allWell()},
		{"doctor problem", "doctor", refusedSignIn()},
		{"doctor no endpoint", "doctor", report("", false, cmds.HealthCheck{Name: "config", State: cmds.CheckFail, Detail: "no endpoint is chosen for this session", Fix: "start the session with MASH_ENDPOINT=<address>"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := cmdcore.NewRegistry(healthEnv(tc.f), cmdcore.Register(cmds.Status), cmdcore.Register(cmds.Doctor))
			var jo, jerr, to, terr bytes.Buffer
			jcode := reg.RunCLI(tc.cmd, nil, strings.NewReader(""), &jo, &jerr, cliframe.Global{JSON: true})
			tcode := reg.RunCLI(tc.cmd, nil, strings.NewReader(""), &to, &terr, cliframe.Global{})
			if jcode != tcode || jerr.Len() != 0 {
				t.Errorf("exit %d and %d, stderr with --json %q", jcode, tcode, jerr.String())
			}
			e, _ := reg.Lookup(tc.cmd)
			text, structured, err := e.Tool(reg.Env).CallStructured(context.Background(), nil)
			if err != nil { // a problem is a report, never an error
				t.Fatalf("tool error %v", err)
			}
			b, _ := json.Marshal(structured)
			if !sameJSON(t, string(b), jo.String()) {
				t.Errorf("structured %s, --json %s", b, jo.String())
			}
			want := strings.TrimSuffix(to.String(), "\n")
			if terr.Len() > 0 {
				want += "\n" + strings.TrimSuffix(terr.String(), "\n")
			}
			if text != want {
				t.Errorf("mcp text %q, cli %q", text, want)
			}
		})
	}
}
