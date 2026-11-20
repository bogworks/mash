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
		ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", ""), ok("delivery", ""), ok("refused", "no unverified mail was refused")}}}
}

// refusedSignIn is the report of the specification's "A problem and a warning" case.
func refusedSignIn() *fakeHealth {
	return &fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", Checks: []cmds.HealthCheck{
		ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"),
		{Name: "sign-in", State: cmds.CheckFail, Detail: "the server refused these credentials", Reason: "the server refused its credentials", Fix: "mashctl endpoint reissue acme.demo.bob"},
		{Name: "delivery", State: cmds.CheckSkip, Detail: "needs sign-in"},
		{Name: "refused", State: cmds.CheckWarn, Detail: "3 unverified messages were refused"}}}}
}

// skippedAfterServer is the report of the specification's "A check that could not run" case.
func skippedAfterServer() *fakeHealth {
	return report("acme.demo.bob", false, ok("config", ""), ok("creds", ""),
		cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: "the server did not answer: tls://myserver:4222"},
		skip("sign-in", "server"), skip("delivery", "server"), ok("refused", "no unverified mail was refused"))
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

// noSessionEndpoint is what a session that names no endpoint reports: the config row fails and the rest cannot run.
func noSessionEndpoint() *fakeHealth {
	return report("", false, cmds.HealthCheck{Name: "config", State: cmds.CheckFail, Detail: cmds.ReasonNoSessionEndpoint, Fix: cmds.SessionEndpointFix},
		skip("creds", "config"), skip("server", "config"), skip("sign-in", "config"), skip("delivery", "config"), skip("refused", "config"))
}

// leaseReport is the report of a session that does not hold its endpoint: every row is fine.
func leaseReport(lease, cause string) *fakeHealth {
	return &fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", Online: true, Lease: lease, LeaseCause: cause,
		Checks: []cmds.HealthCheck{ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", "")}}}
}

func skip(name, needs string) cmds.HealthCheck {
	return cmds.HealthCheck{Name: name, State: cmds.CheckSkip, Detail: "needs the " + needs}
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
	// the one parameter is detail, which the tool takes because status has a fuller answer
	sp := specTool(t, "status")["params"].([]any)
	if len(sp) != 1 || sp[0].(map[string]any)["name"] != cliframe.DetailParam || sp[0].(map[string]any)["description"] != cmdcore.DetailParamDescription {
		t.Errorf("spec params %v", sp)
	}
	tp := cmdcore.MCPTool(cmds.Status, nil).Schema["properties"].(map[string]any)
	if d, _ := tp[cliframe.DetailParam].(map[string]any); len(tp) != 1 || d["type"] != "boolean" || d["description"] != sp[0].(map[string]any)["description"] {
		t.Errorf("status tool params: %v", tp)
	}
	// the object
	st := runStatusObject(t, report("acme.demo.bob", true, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", "")))
	if !sameJSON(t, st, string(mustJSON(meta["x-json"]))) || !sameJSON(t, st, string(mustJSON(specTool(t, "status")["structured"]))) {
		t.Errorf("object %s, spec %v", st, meta["x-json"])
	}
	// the fuller object contains the usual one, and is the specification's
	inSession := func() *fakeHealth {
		f := report("acme.demo.bob", true, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"), ok("sign-in", ""))
		f.rep.Session = &cmds.SessionState{HoldsEndpoint: true, Hooks: cmds.HooksRan, Storage: cmds.StorageOK}
		return f
	}
	if _, js, _ := runStatus(inSession(), cliframe.Global{JSON: true, Detail: true}); !sameJSON(t, js+"\n", string(mustJSON(specTool(t, "status")["structured_detail"]))) || !sameJSON(t, js+"\n", string(mustJSON(meta["x-json-detail"]))) {
		t.Errorf("fuller object %s", js)
	}
	fails := map[string]*fakeHealth{
		"Not working (the first line on stdout, the hint on stderr)": report("acme.demo.bob", false, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"),
			cmds.HealthCheck{Name: "sign-in", State: cmds.CheckFail, Detail: "the server refused these credentials", Reason: "the server refused its credentials"}),
		"The server does not answer": report("acme.demo.bob", false, ok("config", ""), ok("creds", ""),
			cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: "the server did not answer: tls://myserver:4222"}),
		"In a session, when another session holds this endpoint": leaseReport(cmds.LeaseOther, ""),
		"In a session that a newer session replaced":             leaseReport(cmds.LeaseLost, ""),
		"In a session that could not take the endpoint":          leaseReport(cmds.LeaseFailed, "the server did not answer: tls://myserver:4222"),
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
		case "With --detail, inside a session":
			code, out, errs = runStatus(inSession(), cliframe.Global{Detail: true})
		case "With --detail, at a shell with no session":
			code, out, errs = runStatus(report("acme.demo.bob", false, ok("config", ""), ok("sign-in", "")), cliframe.Global{Detail: true})
		case "In a session that cannot store arriving mail on this machine":
			held := &fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", MailHeld: true, Checks: []cmds.HealthCheck{ok("config", ""), ok("sign-in", "")}}}
			code, out, errs = runStatus(held, cliframe.Global{})
		case "No endpoint chosen and several installed":
			// the specification's own text, then the hint when the addresses come in no particular order
			code, out, errs = runStatus(&fakeHealth{err: &config.SeveralEndpointsError{Addresses: []string{"acme.demo.bob", "acme.demo.alice"}}}, cliframe.Global{})
			wantErr := strings.SplitN(text, "\n", 2)[0]
			wantErr = "error: more than one endpoint is installed here: acme.demo.bob, acme.demo.alice"
			_ = wantErr
			code, out, errs = runStatus(&fakeHealth{err: &config.SeveralEndpointsError{Addresses: []string{"acme.demo.alice", "acme.demo.bob"}}}, cliframe.Global{})
			if code != 2 || out != "" || errs != text+"\n" || code != int(oc["exit"].(float64)) {
				t.Errorf("%s: %d %q %q, spec %q", oc["case"], code, out, errs, text)
			}
			_, _, errs = runStatus(&fakeHealth{err: &config.SeveralEndpointsError{Addresses: []string{"acme.demo.bob", "acme.demo.alice"}}}, cliframe.Global{})
			if !strings.HasSuffix(errs, "\nhint: mash status --endpoint acme.demo.alice\n") {
				t.Errorf("unsorted addresses: %q", errs)
			}
			continue
		case "No endpoint installed":
			code, out, errs = runStatus(&fakeHealth{err: cmds.ErrNoEndpointInstalled}, cliframe.Global{})
			if code != 2 || out != "" || errs != text+"\n" || code != int(oc["exit"].(float64)) {
				t.Errorf("%s: %d %q %q, spec %q", oc["case"], code, out, errs, text)
			}
			continue
		case "As an MCP tool in a session with no endpoint named":
			// exit 1, so an error result: the text block is the line, then the hint on the next line
			rep := noSessionEndpoint()
			reg := cmdcore.NewRegistry(healthEnv(rep), cmdcore.Register(cmds.Status))
			e, _ := reg.Lookup("status")
			_, _, err := e.Tool(reg.Env).CallStructured(context.Background(), nil)
			se, isRes := err.(interface{ Structured() any })
			if err == nil || !isRes || err.Error() != text {
				t.Fatalf("%s: %v, spec %q", oc["case"], err, text)
			}
			structured := se.Structured()
			if b, _ := json.Marshal(structured); string(b) != `{"endpoint":null,"ready":false,"online":false,"reason":"no endpoint is chosen for this session"}` {
				t.Errorf("%s: structured %s", oc["case"], b)
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
	// a session with no endpoint named: its own line and hint, the endpoint is null
	code, out, errs = runStatus(noSessionEndpoint(), cliframe.Global{})
	if code != 1 || out != "No endpoint is chosen for this session.\n" || errs != "hint: start the session with MASH_ENDPOINT=<address>\n" {
		t.Errorf("no endpoint: %d %q %q", code, out, errs)
	}
	// any other row that fails with no endpoint (a config that does not load): named "mash"
	code, out, errs = runStatus(report("", false, cmds.HealthCheck{Name: "config", State: cmds.CheckFail, Detail: "config.json is not valid"}), cliframe.Global{})
	if code != 1 || out != "mash: not ready: config.json is not valid\n" || errs != "hint: mash doctor\n" {
		t.Errorf("no endpoint, bad config: %d %q %q", code, out, errs)
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
	props := cmdcore.Schema[cmds.DoctorParams]()["properties"].(map[string]any)
	sp := specTool(t, "doctor")["params"].([]any)
	if len(props) != 1 || len(sp) != 1 || props["strict"] == nil || sp[0].(map[string]any)["name"] != "strict" || sp[0].(map[string]any)["type"] != "boolean" {
		t.Errorf("doctor takes only strict: %v", props)
	}
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		var f *fakeHealth
		switch oc["case"] {
		case "All well":
			f = allWell()
		case "A problem and a warning":
			f = refusedSignIn()
		case "A check that could not run":
			f = skippedAfterServer()
		case "Arriving mail cannot be stored on this machine":
			// the spec gives the delivery row alone, with the path under the detail column
			path := strings.TrimSpace(strings.SplitN(oc["text"].(string), "\n", 2)[1])
			_, out, _ := runDoctor(report("acme.demo.bob", false, ok("config", ""),
				cmds.HealthCheck{Name: "delivery", State: cmds.CheckFail, Detail: cmds.ReasonMailHeld + ": " + path}), cliframe.Global{})
			if !strings.Contains(out, oc["text"].(string)+"\n") {
				t.Errorf("%s: %q lacks %q", oc["case"], out, oc["text"])
			}
			_, js, _ := runDoctor(report("acme.demo.bob", false, cmds.HealthCheck{Name: "delivery", State: cmds.CheckFail, Detail: cmds.ReasonMailHeld + ": " + path}), cliframe.Global{JSON: true})
			if !strings.Contains(js, `"detail":"mail is held: its folder cannot be written: `+path+`","fix":null`) {
				t.Errorf("%s: json %s", oc["case"], js)
			}
			continue
		case "At a shell, where there is no session to deliver to":
			_, out, _ := runDoctor(report("acme.demo.bob", false, ok("config", ""),
				cmds.HealthCheck{Name: "delivery", State: cmds.CheckSkip, Detail: "needs a session"}), cliframe.Global{})
			if !strings.Contains(out, oc["text"].(string)+"\n") {
				t.Errorf("%s: %q lacks %q", oc["case"], out, oc["text"])
			}
			continue
		case "In a session that does not hold its endpoint", "In a session that a newer session replaced":
			// the spec gives the delivery row alone
			row := strings.TrimPrefix(oc["text"].(string), "delivery  FAIL  ")
			_, out, _ := runDoctor(report("acme.demo.bob", false, ok("config", ""),
				cmds.HealthCheck{Name: "delivery", State: cmds.CheckFail, Detail: row}), cliframe.Global{})
			if !strings.Contains(out, oc["text"].(string)+"\n") {
				t.Errorf("%s: %q lacks %q", oc["case"], out, oc["text"])
			}
			continue
		case "The server's certificate is not trusted", "The server does not offer encryption":
			// the spec gives the server row alone: the row of a report with that detail is word for word the spec's
			row := strings.TrimPrefix(oc["text"].(string), "server    FAIL  ")
			_, out, _ := runDoctor(report("acme.demo.bob", false, ok("config", ""), ok("creds", ""),
				cmds.HealthCheck{Name: "server", State: cmds.CheckFail, Detail: row}), cliframe.Global{})
			if !strings.Contains(out, oc["text"].(string)+"\n") {
				t.Errorf("%s: %q lacks %q", oc["case"], out, oc["text"])
			}
			continue
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
	// the object: the spec's x-json lists every row, each with the four keys
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
	if got.Endpoint != spec.Endpoint || got.Problems != spec.Problems || got.Warnings != spec.Warnings || !reflect.DeepEqual(got.Checks, spec.Checks) {
		t.Errorf("object %s, spec x-json %v", out, meta["x-json"])
	}
	// skipped rows are in the object with state skip and the detail, and are neither a problem nor a warning
	_, sk, _ := runDoctor(skippedAfterServer(), cliframe.Global{JSON: true})
	if !strings.Contains(sk, `"problems":1,"warnings":0`) || !strings.Contains(sk, `{"name":"sign-in","state":"skip","detail":"needs the server","fix":null}`) ||
		!strings.Contains(sk, `{"name":"refused","state":"ok","detail":"no unverified mail was refused","fix":null}`) {
		t.Errorf("skipped rows: %s", sk)
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

// A session that names no endpoint: doctor says so in the one line, with the fix as its hint, and the object lists every row.
func TestDoctorNoSessionEndpoint(t *testing.T) {
	code, out, errs := runDoctor(noSessionEndpoint(), cliframe.Global{})
	if code != 1 || out != "No endpoint is chosen for this session.\n" || errs != "hint: start the session with MASH_ENDPOINT=<address>\n" {
		t.Errorf("%d %q %q", code, out, errs)
	}
	_, out, _ = runDoctor(noSessionEndpoint(), cliframe.Global{JSON: true})
	if !strings.HasPrefix(out, `{"endpoint":null,"problems":1,"warnings":0,"checks":[{"name":"config","state":"fail",`) || strings.Count(out, `"name"`) != 6 {
		t.Errorf("object %s", out)
	}
	if code, out, errs := runDoctor(&fakeHealth{err: cmds.ErrNoEndpointInstalled}, cliframe.Global{}); code != 2 || out != "" ||
		errs != "error: no endpoint is installed here\nhint: mash join <file>\n" {
		t.Errorf("none installed: %d %q %q", code, out, errs)
	}
	if code, out, errs := runDoctor(&fakeHealth{err: cmds.ErrNoEndpointInstalled}, cliframe.Global{JSON: true}); code != 2 || errs != "" ||
		out != `{"error":"no endpoint is installed here","hint":"mash join <file>"}`+"\n" {
		t.Errorf("none installed, json: %d %q %q", code, out, errs)
	}
}

// What the command is called with is the caller's context, so a cancelled call stops what the checks wait on.
type ctxHealth struct{ got context.Context }

func (c *ctxHealth) Health(ctx context.Context) (cmds.HealthReport, error) {
	c.got = ctx
	return cmds.HealthReport{}, ctx.Err()
}

func TestStatusAndDoctorPassTheCallersContext(t *testing.T) {
	type key struct{}
	for _, name := range []string{"status", "doctor"} {
		f := &ctxHealth{}
		reg := cmdcore.NewRegistry(healthEnv(f), cmdcore.Register(cmds.Status), cmdcore.Register(cmds.Doctor))
		e, _ := reg.Lookup(name)
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, 1))
		cancel()
		if _, _, err := e.Tool(reg.Env).CallStructured(ctx, nil); err == nil {
			t.Errorf("%s: a cancelled call answered", name)
		}
		if f.got == nil || f.got.Value(key{}) != 1 || f.got.Err() == nil {
			t.Errorf("%s: the service got a context that is not the caller's: %v", name, f.got)
		}
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
		{"the first problem only", []cmds.HealthCheck{fail(""), fail("chmod 600 /x"), fail("mashctl x")}, "3 problems.", 1, ""},
		{"a skip is neither a problem nor a warning", []cmds.HealthCheck{fail("mashctl x"), skip("sign-in", "creds"), skip("refused", "creds")}, "1 problem.", 1, "ask whoever runs your mesh to run: mashctl x"},
		{"with no problem the first warning's fix", []cmds.HealthCheck{{Name: "refused", State: cmds.CheckWarn, Detail: "w", Fix: "mashctl y"}}, "1 warning.", 0, "ask whoever runs your mesh to run: mashctl y"},
		{"a problem outranks an earlier warning", []cmds.HealthCheck{{Name: "refused", State: cmds.CheckWarn, Detail: "w", Fix: "mashctl y"}, fail("chmod 600 /x")}, "1 problem, 1 warning.", 1, "chmod 600 /x"},
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
		{"doctor no endpoint", "doctor", noSessionEndpoint()},
		{"status no endpoint", "status", noSessionEndpoint()},
		{"doctor skipped rows", "doctor", skippedAfterServer()},
		{"doctor delivery fail", "doctor", report("a.b.c", false, ok("config", ""), cmds.HealthCheck{Name: "delivery", State: cmds.CheckFail, Detail: cmds.ReasonMailHeld + ": ~/.local/state/mash/a.b.c"})},
		{"status mail held", "status", &fakeHealth{rep: cmds.HealthReport{Endpoint: "a.b.c", MailHeld: true, Checks: allWell().rep.Checks}}},
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
			// a result that exits nonzero is a report marked an error: its text and structured result are the same
			if (err != nil) != (tcode != 0) {
				t.Fatalf("tool error %v, exit %d", err, tcode)
			}
			if err != nil {
				se, ok := err.(interface{ Structured() any })
				if !ok {
					t.Fatalf("tool error %v is not a report", err)
				}
				text, structured = err.Error(), se.Structured()
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

// A held folder: the delivery row is a problem with the path under the detail column; status says not ready, in the spec's words.
func TestDoctorDeliveryFailIsAProblem(t *testing.T) {
	f := report("acme.demo.bob", false, ok("sign-in", ""),
		cmds.HealthCheck{Name: "delivery", State: cmds.CheckFail, Detail: cmds.ReasonMailHeld + ": ~/.local/state/mash/acme.demo.bob"})
	code, out, errs := runDoctor(f, cliframe.Global{})
	want := "sign-in   ok\ndelivery  FAIL  mail is held: its folder cannot be written:\n                ~/.local/state/mash/acme.demo.bob\n\n1 problem.\n"
	if code != 1 || out != want || errs != "" {
		t.Errorf("%d %q %q", code, out, errs)
	}
	if strings.Contains(out, "spool") {
		t.Errorf("the word spool in %q", out)
	}
}

// A session that could not take its endpoint says why on the next line; the cause is in the JSON reason after a colon.
func TestStatusAndDoctorCouldNotTake(t *testing.T) {
	f := leaseReport(cmds.LeaseFailed, "the server did not answer: tls://myserver:4222")
	_, out, _ := runStatus(f, cliframe.Global{JSON: true})
	if out != `{"endpoint":"acme.demo.bob","ready":false,"online":false,"reason":"this session could not take this endpoint: the server did not answer: tls://myserver:4222"}`+"\n" {
		t.Errorf("status json: %s", out)
	}
	for _, c := range []struct{ lease, reason string }{{cmds.LeaseOther, "another session holds this endpoint"}, {cmds.LeaseLost, "this session lost this endpoint to a newer one"}} {
		_, out, _ = runStatus(leaseReport(c.lease, ""), cliframe.Global{JSON: true})
		if !strings.Contains(out, `"ready":false,"online":false,"reason":"`+c.reason+`"`) {
			t.Errorf("%s: %s", c.lease, out)
		}
	}
	_, out, _ = runDoctor(&fakeHealth{rep: cmds.HealthReport{Endpoint: "acme.demo.bob", Checks: []cmds.HealthCheck{ok("config", ""),
		{Name: "delivery", State: cmds.CheckFail, Detail: cmds.ReasonCouldNotTake + ": the server did not answer: tls://myserver:4222"}}}}, cliframe.Global{})
	if !strings.Contains(out, "delivery  FAIL  this session could not take this endpoint:\n                the server did not answer: tls://myserver:4222\n") {
		t.Errorf("doctor: %q", out)
	}
}

func TestDoctorStrict(t *testing.T) {
	warn := cmds.HealthCheck{Name: "refused", State: cmds.CheckWarn, Detail: "2 unverified messages were refused"}
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"--strict"}, 1}} {
		code, out, _ := runDoctor(report("a.b.c", false, ok("config", ""), warn), cliframe.Global{}, tc.args...)
		if code != tc.code || !strings.HasSuffix(out, "\n\n1 warning.\n") {
			t.Errorf("%v: %d %q", tc.args, code, out)
		}
	}
	if code, _, _ := runDoctor(report("a.b.c", false, ok("config", "")), cliframe.Global{}, "--strict"); code != 0 {
		t.Errorf("--strict with nothing wrong: %d", code)
	}
}

// A strict doctor with only a warning is an error result in a tool, with the report and the structured result kept; without
// strict the same report is a normal result.
func TestDoctorToolStrictMarksTheResultAnError(t *testing.T) {
	warn := cmds.HealthCheck{Name: "refused", State: cmds.CheckWarn, Detail: "2 unverified messages were refused", Fix: "mashctl endpoint reissue a.b.c"}
	reg := cmdcore.NewRegistry(healthEnv(report("a.b.c", false, ok("config", ""), warn)), cmdcore.Register(cmds.Doctor))
	e, _ := reg.Lookup("doctor")
	tool := e.Tool(reg.Env)
	text, structured, err := tool.CallStructured(context.Background(), json.RawMessage(`{}`))
	if err != nil || structured == nil || !strings.Contains(text, "1 warning.") {
		t.Fatalf("without strict: %q %v %v", text, structured, err)
	}
	_, _, err = tool.CallStructured(context.Background(), json.RawMessage(`{"strict":true}`))
	se, ok := err.(interface{ Structured() any })
	if err == nil || !ok {
		t.Fatalf("strict with a warning is an error result, got %v", err)
	}
	if !strings.Contains(err.Error(), "1 warning.\nhint: ") || strings.HasPrefix(err.Error(), "error:") {
		t.Errorf("the report is kept as it is: %q", err.Error())
	}
	b, _ := json.Marshal(se.Structured())
	if !strings.Contains(string(b), `"warnings":1`) {
		t.Errorf("structured %s", b)
	}
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"strict":false}`)); err != nil {
		t.Errorf("strict false: %v", err)
	}
}

func TestStatusDetailReasonCode(t *testing.T) {
	f := report("acme.demo.bob", false, ok("config", ""), ok("creds", ""), ok("server", "tls://myserver:4222"),
		cmds.HealthCheck{Name: "sign-in", State: cmds.CheckFail, Detail: "the server refused these credentials", Reason: "the server refused its credentials"})
	_, js, _ := runStatus(f, cliframe.Global{JSON: true, Detail: true})
	if !strings.Contains(js, `"reason_code":"creds_refused"`) || !strings.Contains(js, `"session":null`) {
		t.Errorf("%s", js)
	}
	if _, usual, _ := runStatus(f, cliframe.Global{JSON: true}); strings.Contains(usual, "reason_code") {
		t.Errorf("usual object has reason_code: %s", usual)
	}
	lr := leaseReport(cmds.LeaseLost, "")
	if _, js, _ = runStatus(lr, cliframe.Global{JSON: true, Detail: true}); !strings.Contains(js, `"reason_code":"endpoint_lost"`) {
		t.Errorf("%s", js)
	}
}
