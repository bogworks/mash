package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// whoShows reports whether the who table in out has a row for address with state in its second column.
func whoShows(out, address, state string) bool {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == address && f[1] == state {
			return true
		}
	}
	return false
}

// whoCell is the cell of the who table in out for the row of address, under the column named col ("" when there is none).
// The columns are found from the header, so a cell may hold spaces.
func whoCell(t *testing.T, out, address, col string) string {
	t.Helper()
	lines := strings.Split(out, "\n")
	head := lines[0]
	start := strings.Index(head, col)
	if start < 0 {
		t.Fatalf("no column %s in %q", col, head)
	}
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) > 0 && f[0] == address {
			rest := head[start+len(col):]
			i := strings.IndexFunc(rest, func(r rune) bool { return r != ' ' })
			if i < 0 {
				return strings.TrimSpace(l[min(start, len(l)):])
			}
			end := start + len(col) + i
			return strings.TrimSpace(l[min(start, len(l)):min(end, len(l))])
		}
	}
	return ""
}

type fixedDirectory []cmds.WhoEntry

func (d fixedDirectory) Who(context.Context) ([]cmds.WhoEntry, int, error) { return d, 0, nil }

// The text keeps every control character of a recorded role, host and description from reaching the terminal, and
// leaves Unicode alone.
func TestWhoTextEscapesRecordFields(t *testing.T) {
	entries := whoEntries([]endpoint.Presence{{Record: hostileRecord()}}, "acme")
	entries[0].Description = "\U0001F600 日本"
	env := &cmdcore.Env{Svc: fixedDirectory(entries)}
	r, err := cmds.Who.Run(context.Background(), env, cmds.WhoParams{Wide: true})
	if err != nil {
		t.Fatal(err)
	}
	got := r.Text(cmdcore.Opts{})
	if hasRawControl(got) {
		t.Fatalf("raw control in %q", got)
	}
	if !strings.Contains(got, `role\x1b[31m\x07\u009b`) || !strings.Contains(got, `host\x1b[31m`) || !strings.Contains(got, "\U0001F600 日本") {
		t.Fatalf("role, host or description wrong: %q", got)
	}
	// the JSON carries the record as it is
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"address":"acme.team1.ops1"`) {
		t.Fatalf("json %s", b)
	}
}

// The who command through the real binary's entry point: the table, --wide, a prefix, a full address that is not its
// near miss, the dotted forms only, and the --json object.
func TestWhoCLI(t *testing.T) {
	w := newWorld(t, "a", "b", "bb")
	if err := w.setMeta(t, "a", admin.MetaUpdate{Role: strp("reviewer"), Host: strp("myserver"), Description: strp("reviews pull requests"), AddCaps: []string{"review"}, Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatalf("set-meta: %v", err)
	}
	out, code := w.cli(t, "b", "who")
	if code != 0 || !reflect.DeepEqual(strings.Fields(strings.Split(out, "\n")[0]), []string{"ADDRESS", "STATE"}) || strings.Contains(out, "\t") {
		t.Fatalf("who: %d %q", code, out)
	}
	for _, n := range []string{"a", "b", "bb"} {
		if !whoShows(out, tTo(n), "online") && !whoShows(out, tTo(n), "offline") {
			t.Errorf("who lacks %s: %q", n, out)
		}
	}
	if i, j := strings.Index(out, tTo("a")), strings.Index(out, tTo("b")); i > j {
		t.Errorf("not sorted by address: %q", out)
	}
	// the columns are aligned: the state starts at one column in every row
	col := -1
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n")[1:] {
		f := strings.Fields(l)
		if c := strings.Index(l, f[1]); col >= 0 && c != col {
			t.Errorf("state column moved in %q", out)
		} else {
			col = c
		}
	}
	// --wide: the recorded role, host and description, - where nothing is recorded
	out, _ = w.cli(t, "b", "who", "--wide")
	if !reflect.DeepEqual(strings.Fields(strings.Split(out, "\n")[0]), []string{"ADDRESS", "STATE", "ROLE", "HOST", "DESCRIPTION"}) {
		t.Fatalf("wide header: %q", out)
	}
	for col, want := range map[string]string{"ROLE": "reviewer", "HOST": "myserver", "DESCRIPTION": "reviews pull requests"} {
		if got := whoCell(t, out, tTo("a"), col); got != want {
			t.Errorf("wide %s of a = %q, want %q (%q)", col, got, want, out)
		}
		if got := whoCell(t, out, tTo("b"), col); got != "-" {
			t.Errorf("wide %s of b = %q, want - (%q)", col, got, out)
		}
	}
	// prefixes
	names := func(out string) []string {
		var n []string
		for _, l := range strings.Split(out, "\n")[1:] {
			if f := strings.Fields(l); len(f) > 0 {
				n = append(n, f[0])
			}
		}
		return n
	}
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"o"}, []string{tTo("a"), tTo("b"), tTo("bb")}},
		{[]string{"o.*"}, []string{tTo("a"), tTo("b"), tTo("bb")}},
		{[]string{"o.dev"}, []string{tTo("a"), tTo("b"), tTo("bb")}},
		{[]string{"o.dev.*"}, []string{tTo("a"), tTo("b"), tTo("bb")}},
		{[]string{tTo("b")}, []string{tTo("b")}}, // never o.dev.bb
		{[]string{tTo("bb")}, []string{tTo("bb")}},
	} {
		out, code := w.cli(t, "b", append([]string{"who"}, tc.args...)...)
		if code != 0 || !reflect.DeepEqual(names(out), tc.want) {
			t.Errorf("who %v: %d %v (%q), want %v", tc.args, code, names(out), out, tc.want)
		}
	}
	// nothing under the prefix, and the near miss of a shorter prefix
	for args, want := range map[string]string{"o.lab": "No endpoints under o.lab\n", "o.de": "No endpoints under o.de\n", "z": "No endpoints under z\n"} {
		if out, code := w.cli(t, "b", "who", args); code != 0 || out != want {
			t.Errorf("who %s: %d %q, want %q", args, code, out, want)
		}
	}
	if out, code := w.cli(t, "b", "who", "o.dev.nobody"); code != 0 || out != "No endpoints under o.dev.nobody\n" {
		t.Errorf("who nobody: %d %q", code, out)
	}
	// the dotted forms only, and nothing but the owner, group and address forms
	for _, bad := range []string{"o_dev_b", "o/dev/b", "O.dev", "o.dev.b.x", "o.d*", "*", "o..dev", "o.dev.b.*"} {
		out, code := w.cli(t, "b", "who", bad)
		if code != 2 || out != "error: "+bad+" is not an owner, an owner.group or an address\nhint: mash who --help\n" {
			t.Errorf("who %s: %d %q", bad, code, out)
		}
	}
	if out, _ := w.cli(t, "b", "who", "Acme.Demo"); out != "error: Acme.Demo is not an owner, an owner.group or an address\nhint: mash who --help\n" {
		t.Errorf("capitals: %q", out)
	}
	if out, code := w.cli(t, "b", "who", "--online", "--offline"); code != 2 || out != "error: mash who takes --online or --offline, not both\nhint: mash who --help\n" {
		t.Errorf("conflict: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "--match", "x"); code != 2 || out != "error: unknown flag --match\n" {
		t.Errorf("old filter: %d %q", code, out)
	}
	for _, old := range []string{"--long", "--names", "--role", "--label"} {
		if _, code := w.cli(t, "b", "who", old); code != 2 {
			t.Errorf("who %s: exit %d, want 2", old, code)
		}
	}
	// --offline: only the endpoints that are not there; none online here (no serve is running)
	if out, code := w.cli(t, "b", "who", "--online"); code != 0 || out != "No online endpoints\n" {
		t.Errorf("--online: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "o.dev.b", "--offline"); code != 0 || out != "ADDRESS  STATE\no.dev.b  offline\n" {
		t.Errorf("--offline: %d %q", code, out)
	}
	// --json: an object
	out, code = w.cli(t, "b", "who", "--json", "--wide")
	var obj struct {
		Endpoints []map[string]any `json:"endpoints"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &obj) != nil || len(obj.Endpoints) != 3 || strings.HasPrefix(out, "[") {
		t.Fatalf("--json: %d %q", code, out)
	}
	e := obj.Endpoints[0]
	if len(e) != 7 || e["address"] != tTo("a") || e["role"] != "reviewer" || e["host"] != "myserver" || e["description"] != "reviews pull requests" ||
		!reflect.DeepEqual(e["capability"], []any{"review"}) || !reflect.DeepEqual(e["labels"], map[string]any{"team": "blue"}) {
		t.Errorf("first endpoint %v", e)
	}
	if e := obj.Endpoints[1]; e["role"] != nil || e["host"] != nil || e["description"] != nil ||
		!reflect.DeepEqual(e["capability"], []any{}) || !reflect.DeepEqual(e["labels"], map[string]any{}) {
		t.Errorf("unrecorded fields are null, capability [] and labels {}: %v", e)
	}
	// without --wide the structured result is the same, byte for byte
	if plain, code := w.cli(t, "b", "who", "--json"); code != 0 || plain != out {
		t.Errorf("--json without --wide differs: %d %q vs %q", code, plain, out)
	}
	if out, code := w.cli(t, "b", "who", "o.lab", "--json"); code != 0 || out != `{"endpoints":[],"answers_not_verified":0}`+"\n" {
		t.Errorf("empty --json: %d %q", code, out)
	}
	if out, code := w.cli(t, "b", "who", "o_dev_b", "--json"); code != 2 || out != `{"error":"o_dev_b is not an owner, an owner.group or an address","hint":"mash who --help"}`+"\n" {
		t.Errorf("bad prefix --json: %d %q", code, out)
	}
}

// The who tool: the same parameters, the text is the CLI's stdout, the structured result is the object, and the old
// parameters are refused.
func TestWhoMCPTool(t *testing.T) {
	w := newWorld(t, "a", "b")
	if err := w.setMeta(t, "a", admin.MetaUpdate{Role: strp("reviewer"), Host: strp("myserver"), Description: strp("reviews pull requests"), AddCaps: []string{"review"}, Labels: map[string]string{"team": "blue"}}); err != nil {
		t.Fatalf("set-meta: %v", err)
	}
	s := startServe(t, w.env["b"])
	s.initialize(t)
	s.tools(t)
	var tool map[string]any
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "who" {
			tool = m
		}
	}
	props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if len(props) != 7 || props["prefix"] == nil || props["online"] == nil || props["offline"] == nil || props["wide"] == nil ||
		props["role"] == nil || props["capability"] == nil || props["label"] == nil {
		t.Fatalf("who schema: %v", props)
	}
	var cliOut string
	eventually(t, 5*time.Second, "b online", func() bool {
		cliOut, _ = w.cli(t, "a", "who")
		return whoShows(cliOut, tTo("b"), "online")
	})
	r := s.call(t, 40, "who", `{}`)
	sc, isObj := r["structuredContent"].(map[string]any)
	if !isObj || r["isError"] == true {
		t.Fatalf("structuredContent is not an object: %v", r)
	}
	eps, _ := sc["endpoints"].([]any)
	if len(eps) != 2 || eps[0].(map[string]any)["address"] != tTo("a") || eps[1].(map[string]any)["online"] != true {
		t.Fatalf("endpoints: %v", sc)
	}
	// the two doors give the same object, byte for byte (the tool's result is read back into the typed object, so
	// the field order is the one the CLI writes)
	cliJSON, _ := w.cli(t, "a", "who", "--json")
	raw, _ := json.Marshal(sc)
	var typed struct {
		Endpoints          []cmds.WhoEndpoint `json:"endpoints"`
		AnswersNotVerified *int               `json:"answers_not_verified"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(typed); string(b)+"\n" != cliJSON {
		t.Errorf("mcp %s != cli --json %s", b, cliJSON)
	}
	if e := eps[0].(map[string]any); !reflect.DeepEqual(e["capability"], []any{"review"}) || !reflect.DeepEqual(e["labels"], map[string]any{"team": "blue"}) {
		t.Errorf("mcp endpoint a lacks capability or labels: %v", e)
	}
	if txt := resultText(r); !strings.HasPrefix(txt, "ADDRESS  STATE\n") || !whoShows(txt, tTo("b"), "online") || strings.Contains(txt, "\t") {
		t.Fatalf("text: %q", txt)
	}
	// wide, a prefix and the filters through the tool
	wr := resultText(s.call(t, 41, "who", `{"wide":true,"prefix":"o.dev.*"}`))
	if got := whoCell(t, wr, tTo("a"), "DESCRIPTION"); got != "reviews pull requests" {
		t.Fatalf("wide text: %q", wr)
	}
	if got := resultText(s.call(t, 42, "who", `{"online":true}`)); strings.Contains(got, tTo("a")) || !whoShows(got, tTo("b"), "online") {
		t.Fatalf("online: %q", got)
	}
	if got := resultText(s.call(t, 43, "who", `{"prefix":"o.dev.b"}`)); strings.Contains(got, tTo("a")) {
		t.Fatalf("prefix: %q", got)
	}
	if got := resultText(s.call(t, 44, "who", `{"prefix":"o.lab"}`)); got != "No endpoints under o.lab" {
		t.Fatalf("empty: %q", got)
	}
	// the filters through the tool: the same endpoints as the command line, text and object
	for _, c := range []struct {
		args string
		argv []string
		want []string
	}{
		{`{"role":"REVIEW"}`, []string{"--role", "REVIEW"}, []string{tTo("a")}},
		{`{"capability":["review"]}`, []string{"--capability", "review"}, []string{tTo("a")}},
		{`{"capability":["review","x"]}`, []string{"--capability", "review", "--capability", "x"}, nil},
		{`{"label":["team=blue"],"online":false}`, []string{"--label", "team=blue"}, []string{tTo("a")}},
		{`{"label":["team","team=blue"],"capability":["review"]}`, []string{"--label", "team", "--label", "team=blue", "--capability", "review"}, []string{tTo("a")}},
		{`{"label":["team=red"]}`, []string{"--label", "team=red"}, nil},
		{`{"capability":["review"],"online":true}`, []string{"--capability", "review", "--online"}, nil},
	} {
		r := s.call(t, 60, "who", c.args)
		cli, code := w.cli(t, "a", append([]string{"who", "--json"}, c.argv...)...)
		if r["isError"] == true || code != 0 {
			t.Fatalf("who %s: %v / %d %s", c.args, r, code, cli)
		}
		var got []string
		sc, _ := r["structuredContent"].(map[string]any)
		for _, e := range sc["endpoints"].([]any) {
			got = append(got, e.(map[string]any)["address"].(string))
		}
		raw, _ := json.Marshal(sc)
		var typed struct {
			Endpoints          []cmds.WhoEndpoint `json:"endpoints"`
			AnswersNotVerified *int               `json:"answers_not_verified"`
		}
		_ = json.Unmarshal(raw, &typed)
		if b, _ := json.Marshal(typed); string(b)+"\n" != cli || !reflect.DeepEqual(got, c.want) {
			t.Errorf("who %s: tool %v, cli %q, want %v", c.args, got, cli, c.want)
		}
		if len(c.want) == 0 && resultText(r) != "No endpoints" && resultText(r) != "No online endpoints" {
			t.Errorf("who %s: empty text %q", c.args, resultText(r))
		}
	}
	// errors: {error, hint}, and the old parameters are not accepted
	for _, args := range []string{`{"prefix":"o_dev_b"}`, `{"online":true,"offline":true}`, `{"match":"x"}`, `{"format":"json"}`, `{"label":[3]}`, `{"label":"a="}`, `{"capability":"review"}`, `{"online":"yes"}`} {
		r := s.call(t, 45, "who", args)
		sc, _ := r["structuredContent"].(map[string]any)
		if r["isError"] != true || sc["error"] == nil {
			t.Errorf("who %s: %v", args, r)
		}
	}
	r = s.call(t, 46, "who", `{"prefix":"o_dev_b"}`)
	if sc, _ := r["structuredContent"].(map[string]any); sc["error"] != "o_dev_b is not an owner, an owner.group or an address" || sc["hint"] != "mash who --help" {
		t.Errorf("flat prefix: %v", r)
	}
}

// who lists only the caller's own owner, whatever the directory holds: the filter is the endpoint's, not the account's.
func TestWhoListsOnlyTheCallersOwner(t *testing.T) {
	ps := []endpoint.Presence{
		{Record: store.Record{Owner: "acme", Group: "demo", Instance: "alice"}, Online: true},
		{Record: store.Record{Owner: "acme", Group: "demo", Instance: "bob"}},
		{Record: store.Record{Owner: "other", Group: "demo", Instance: "carol"}, Online: true},
		{Record: store.Record{Owner: "acmeco", Group: "demo", Instance: "dan"}},
	}
	env := &cmdcore.Env{Svc: fixedDirectory(whoEntries(ps, "acme"))}
	for prefix, want := range map[string][]string{
		"":                 {"acme.demo.alice", "acme.demo.bob"},
		"acme":             {"acme.demo.alice", "acme.demo.bob"},
		"other":            nil,
		"other.demo":       nil,
		"other.*":          nil,
		"other.demo.carol": nil,
		"acmeco":           nil,
	} {
		r, err := cmds.Who.Run(context.Background(), env, cmds.WhoParams{Prefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range r.Endpoints {
			got = append(got, e.Address)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("prefix %q: %v, want %v", prefix, got, want)
		}
	}
}

// A wait for who is online that ran out is not a failure to connect.
func TestWhoRefreshFailureWording(t *testing.T) {
	cfg := config.Config{URL: "tls://myserver:4222"}
	err := refreshFailure(cfg, fmt.Errorf("wait: %w", context.DeadlineExceeded))
	var ce *cliframe.Error
	if !errors.As(err, &ce) || ce.Msg != "the directory could not be read: the endpoints did not answer in time" || ce.Hint != "mash doctor" || ce.Code != 1 {
		t.Errorf("timeout: %#v", err)
	}
	if strings.Contains(err.Error(), "server did not answer") {
		t.Errorf("reads like a connect failure: %v", err)
	}
	err = refreshFailure(cfg, errors.New("mash: presence probe: boom"))
	if !errors.As(err, &ce) || ce.Msg != "the directory could not be read: mash: presence probe: boom" || ce.Code != 1 {
		t.Errorf("other: %#v", err)
	}
}
