package cmds_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// fakeBox is a Mailbox over a fixed inbox: it applies the query the way the service does (after, new, newest-limit).
type fakeBox struct {
	view  cmds.InboxView
	asked []cmds.InboxQuery
	err   error
}

func (f *fakeBox) Inbox(_ context.Context, q cmds.InboxQuery) (cmds.InboxView, error) {
	f.asked = append(f.asked, q)
	if f.err != nil {
		return cmds.InboxView{}, f.err
	}
	v := f.view
	var ms []cmds.MailItem
	for _, m := range v.Messages {
		if m.Number > q.After && (!q.OnlyNew || m.New) {
			ms = append(ms, m)
		}
	}
	if len(ms) > q.Limit {
		if q.After > 0 {
			ms = ms[:q.Limit]
		} else {
			ms = ms[len(ms)-q.Limit:]
		}
	}
	v.Messages = ms
	return v, nil
}

func (f *fakeBox) Message(_ context.Context, n int) (cmds.ReadView, error) {
	if f.err != nil {
		return cmds.ReadView{}, f.err
	}
	for i := range f.view.Messages {
		if f.view.Messages[i].Number == n {
			return cmds.ReadView{Endpoint: f.view.Endpoint, Message: &f.view.Messages[i]}, nil
		}
	}
	return cmds.ReadView{Endpoint: f.view.Endpoint}, nil
}

var specNow = time.Date(2026, 11, 19, 9, 44, 12, 0, time.UTC)

func ago(d time.Duration) time.Time { return specNow.Add(-d) }

// specBox is the inbox of the specification's examples: bob's two messages, 10 s and 8 s old.
func specBox() *fakeBox {
	return &fakeBox{view: cmds.InboxView{Endpoint: "acme.demo.alice", Total: 2, Waiting: 2, NewUrgent: 1, OldestWaiting: ago(10 * time.Second),
		WakeCount: 15, WakeAge: time.Hour, WakeAgeText: "1h",
		Messages: []cmds.MailItem{
			{Number: 1, From: "acme.demo.bob", Sent: ago(10 * time.Second), New: true, Kind: "task", Trust: "verified", Size: 14, Body: "lunch at noon?"},
			{Number: 2, From: "acme.demo.bob", Sent: ago(8 * time.Second), New: true, Urgent: true, Kind: "question", Trust: "verified", Size: 6, Body: "ready?"},
		}}}
}

func mailEnv(svc any) cmdcore.EnvFunc {
	return func(context.Context) (*cmdcore.Env, error) {
		return &cmdcore.Env{Svc: svc, Now: func() time.Time { return specNow }}, nil
	}
}

func runInbox(svc any, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Inbox, mailEnv(svc))(args, strings.NewReader(""), &o, &e, g)
	return code, o.String(), e.String()
}

func runRead(svc any, g cliframe.Global, args ...string) (code int, out, errs string) {
	var o, e bytes.Buffer
	code = cmdcore.CLI(cmds.Read, mailEnv(svc))(args, strings.NewReader(""), &o, &e, g)
	return code, o.String(), e.String()
}

// tablePart is the text from the table's header on.
func tablePart(s string) string {
	_, t, _ := strings.Cut(s, "\n\n")
	return strings.TrimRight(t, "\n")
}

func TestInboxText(t *testing.T) {
	code, out, errs := runInbox(specBox(), cliframe.Global{})
	want := "2 messages for acme.demo.alice, 2 new, 1 urgent.\n\n" +
		"NUMBER  FROM  WHEN     STATE  URGENT  KIND      MESSAGE\n" +
		"1       bob   10s ago  new    no      task      lunch at noon?\n" +
		"2       bob   8s ago   new    yes     question  ready?\n"
	if code != 0 || out != want || errs != "" {
		t.Errorf("messages: %d %q %q", code, out, errs)
	}
	_, out, _ = runInbox(specBox(), cliframe.Global{}, "--wide")
	want = "2 messages for acme.demo.alice, 2 new, 1 urgent.\n\n" +
		"NUMBER  FROM           WHEN     STATE  URGENT  KIND      TRUST     SIZE  MESSAGE\n" +
		"1       acme.demo.bob  10s ago  new    no      task      verified  14 B  lunch at noon?\n" +
		"2       acme.demo.bob  8s ago   new    yes     question  verified  6 B   ready?\n"
	if out != want {
		t.Errorf("wide: %q", out)
	}
	// singular
	b := specBox()
	b.view.Total, b.view.Waiting, b.view.NewUrgent, b.view.Messages = 1, 1, 0, b.view.Messages[:1]
	if _, out, _ = runInbox(b, cliframe.Global{}); !strings.HasPrefix(out, "1 message for acme.demo.alice, 1 new, 0 urgent.\n\n") {
		t.Errorf("singular: %q", out)
	}
	// read messages are not counted as new or urgent
	b = specBox()
	b.view.Waiting, b.view.NewUrgent = 0, 0
	b.view.OldestWaiting = time.Time{}
	b.view.Messages[1].New, b.view.Messages[0].New = false, false
	_, out, _ = runInbox(b, cliframe.Global{})
	if !strings.HasPrefix(out, "2 messages for acme.demo.alice, 0 new, 0 urgent.\n\n") || !strings.Contains(out, "2       bob   8s ago   read   yes") {
		t.Errorf("read: %q", out)
	}
	// empty
	_, out, _ = runInbox(&fakeBox{view: cmds.InboxView{Endpoint: "acme.demo.alice"}}, cliframe.Global{})
	if out != "No messages for acme.demo.alice.\n" {
		t.Errorf("empty: %q", out)
	}
	// a listing that selects nothing out of a full inbox: the summary, no table
	_, out, _ = runInbox(specBox(), cliframe.Global{}, "--after", "2")
	if out != "2 messages for acme.demo.alice, 2 new, 1 urgent.\n" {
		t.Errorf("nothing after 2: %q", out)
	}
}

func TestInboxLongMessageAndOutsideGroup(t *testing.T) {
	long := "The build passed on all four runners, and the release notes are ready for review"
	b := &fakeBox{view: cmds.InboxView{Endpoint: "acme.demo.alice", Total: 1, Waiting: 1, OldestWaiting: ago(time.Minute), WakeCount: 15, WakeAge: time.Hour, WakeAgeText: "1h",
		Messages: []cmds.MailItem{{Number: 3, From: "acme.demo.bob", Sent: ago(time.Minute), New: true, Kind: "result", Trust: "verified", Body: long + "\nsecond line"}}}}
	_, out, _ := runInbox(b, cliframe.Global{})
	want := "NUMBER  FROM  WHEN    STATE  URGENT  KIND    MESSAGE\n3       bob   1m ago  new    no      result  The build passed on all four runn\u2026"
	if tablePart(out) != want {
		t.Errorf("long: %q", tablePart(out))
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if n := len([]rune(line)); n > 79 {
			t.Errorf("a line of %d columns: %q", n, line)
		}
	}
	// only the first line shows, and the JSON carries the same cut text
	_, js, _ := runInbox(b, cliframe.Global{JSON: true})
	if !strings.Contains(js, `"preview":"The build passed on all four runn\u2026"`) && !strings.Contains(js, `"preview":"The build passed on all four runn…"`) {
		t.Errorf("preview: %s", js)
	}
	// a wide terminal shows more of it
	b.view.Messages[0].Body = "short"
	b = &fakeBox{view: b.view}
	b.view.Messages = []cmds.MailItem{{Number: 3, From: "acme.ops.carol", Sent: ago(2 * time.Minute), New: true, Kind: "fyi", Trust: "verified", Body: "deploy finished"}}
	_, out, _ = runInbox(b, cliframe.Global{})
	want = "NUMBER  FROM            WHEN    STATE  URGENT  KIND  MESSAGE\n3       acme.ops.carol  2m ago  new    no      fyi   deploy finished"
	if tablePart(out) != want {
		t.Errorf("outside group: %q", tablePart(out))
	}
	// the same owner but another group is not "in your group"
	b.view.Messages[0].From = "acme.lab.carol"
	if _, out, _ = runInbox(b, cliframe.Global{}); !strings.Contains(out, "acme.lab.carol") {
		t.Errorf("another group: %q", out)
	}
	// wide keeps a long message whole
	b.view.Messages[0].Body = strings.Repeat("word ", 30)
	_, out, _ = runInbox(b, cliframe.Global{}, "--wide")
	if strings.Contains(out, "\u2026") {
		t.Errorf("wide cut a message: %q", out)
	}
}

func TestInboxWakeLines(t *testing.T) {
	b := specBox()
	b.view.Total, b.view.Waiting, b.view.OldestWaiting = 3, 3, ago(72*time.Minute)
	_, out, _ := runInbox(b, cliframe.Global{})
	if !strings.HasPrefix(out, "3 messages for acme.demo.alice, 3 new, 1 urgent.\nThe oldest has waited 1h 12m.\n\nNUMBER  ") {
		t.Errorf("age: %q", out)
	}
	b = specBox()
	b.view.Total, b.view.Waiting = 17, 17
	_, out, _ = runInbox(b, cliframe.Global{})
	if !strings.HasPrefix(out, "17 messages for acme.demo.alice, 17 new, 1 urgent.\nMore than 15 are waiting.\n\nNUMBER  ") {
		t.Errorf("count: %q", out)
	}
	// both: the count line first
	b.view.OldestWaiting = ago(2 * time.Hour)
	_, out, _ = runInbox(b, cliframe.Global{})
	if !strings.HasPrefix(out, "17 messages for acme.demo.alice, 17 new, 1 urgent.\nMore than 15 are waiting.\nThe oldest has waited 2h.\n\nNUMBER  ") {
		t.Errorf("both: %q", out)
	}
	// exactly at the count, and exactly at the age, is not over; off is never over
	b = specBox()
	b.view.Waiting, b.view.WakeCount, b.view.OldestWaiting = 15, 15, ago(time.Hour)
	_, js, _ := runInbox(b, cliframe.Global{JSON: true})
	if !strings.Contains(js, `"over_wake_count":false`) || !strings.Contains(js, `"over_wake_age":false`) {
		t.Errorf("at the limits: %s", js)
	}
	b.view.Waiting, b.view.WakeCount, b.view.WakeAge, b.view.WakeAgeText, b.view.OldestWaiting = 500, 0, 0, "0", ago(100*time.Hour)
	_, js, _ = runInbox(b, cliframe.Global{JSON: true})
	for _, w := range []string{`"wake_count":0`, `"wake_age":"0"`, `"over_wake_count":false`, `"over_wake_age":false`, `"oldest_waiting":"4d 4h"`} {
		if !strings.Contains(js, w) {
			t.Errorf("off: missing %s in %s", w, js)
		}
	}
	// nothing waiting: null
	b = specBox()
	b.view.Waiting, b.view.OldestWaiting = 0, time.Time{}
	if _, js, _ = runInbox(b, cliframe.Global{JSON: true}); !strings.Contains(js, `"oldest_waiting":null`) {
		t.Errorf("none waiting: %s", js)
	}
}

func TestInboxOptions(t *testing.T) {
	b := specBox()
	runInbox(b, cliframe.Global{})
	runInbox(b, cliframe.Global{}, "--new", "--limit", "5", "--after", "1")
	runInbox(b, cliframe.Global{}, "--limit=100")
	want := []cmds.InboxQuery{{Limit: 20}, {OnlyNew: true, Limit: 5, After: 1}, {Limit: 100}}
	if !reflect.DeepEqual(b.asked, want) {
		t.Errorf("queries %+v, want %+v", b.asked, want)
	}
	_, out, _ := runInbox(b, cliframe.Global{}, "--after", "1")
	if strings.Contains(out, "\n1  ") || !strings.Contains(out, "\n2  ") {
		t.Errorf("--after 1: %q", out)
	}
	_, out, _ = runInbox(b, cliframe.Global{}, "--limit", "1")
	if strings.Contains(out, "\n1  ") || !strings.Contains(out, "\n2  ") { // newest last: the newest one
		t.Errorf("--limit 1: %q", out)
	}
	for _, bad := range []string{"0", "101", "-1", "1000", "x"} {
		n := len(b.asked)
		code, out, errs := runInbox(b, cliframe.Global{}, "--limit", bad)
		wantErr := "error: --limit must be a whole number from 1 to 100\nhint: mash inbox --help\n"
		if bad == "x" {
			wantErr = "error: --limit must be a whole number\nhint: mash inbox --help\n"
		}
		if code != 2 || out != "" || errs != wantErr || len(b.asked) != n {
			t.Errorf("--limit %s: %d %q %q", bad, code, out, errs)
		}
	}
	if code, _, errs := runInbox(b, cliframe.Global{}, "--after", "-1"); code != 2 || !strings.Contains(errs, "unknown option") && !strings.Contains(errs, "--after") {
		t.Errorf("--after -1: %d %q", code, errs)
	}
	if code, _, errs := runInbox(b, cliframe.Global{}, "--after", "x"); code != 2 || errs != "error: --after must be a whole number\nhint: mash inbox --help\n" {
		t.Errorf("--after x: %d %q", code, errs)
	}
	if code, _, errs := runInbox(b, cliframe.Global{}, "3"); code != 2 || !strings.Contains(errs, "unexpected argument") {
		t.Errorf("an argument: %d %q", code, errs)
	}
}

func TestInboxToolLimit(t *testing.T) {
	b := specBox()
	tool := cmdcore.MCPTool(cmds.Inbox, mailEnv(b))
	for _, c := range []struct{ args, want string }{
		{`{"limit":101}`, "error: limit must be a whole number from 1 to 100"},
		{`{"limit":0}`, "error: limit must be a whole number from 1 to 100"},
		{`{"after":-1}`, "error: after must be a whole number of 0 or more"},
		{`{"lines":true}`, "error: unknown argument lines"},
	} {
		_, _, err := tool.CallStructured(context.Background(), json.RawMessage(c.args))
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v", c.args, err)
		}
	}
	if _, _, err := tool.CallStructured(context.Background(), json.RawMessage(`{"limit":100,"after":0,"new":false,"wide":false}`)); err != nil {
		t.Error(err)
	}
	props := tool.Schema["properties"].(map[string]any)["limit"].(map[string]any)
	if props["minimum"] != 1 || props["maximum"] != 100 {
		t.Errorf("limit schema %v", props)
	}
}

func TestInboxFailure(t *testing.T) {
	code, out, errs := runInbox(&fakeBox{err: cmdcore.ServerNotAnswering("tls://x:4222")}, cliframe.Global{})
	if code != 1 || out != "" || errs != "error: the server did not answer: tls://x:4222\nhint: mash doctor\n" {
		t.Errorf("failure: %d %q %q", code, out, errs)
	}
	code, _, errs = runInbox(struct{}{}, cliframe.Global{})
	if code != 1 || !strings.HasPrefix(errs, "error: internal:") {
		t.Errorf("no service: %d %q", code, errs)
	}
}

func readBox() *fakeBox {
	return &fakeBox{view: cmds.InboxView{Endpoint: "acme.demo.alice", Messages: []cmds.MailItem{
		{Number: 2, From: "acme.demo.bob", Sent: ago(8 * time.Second), New: true, Urgent: true, Kind: "question", Trust: "verified", Body: "ready?"},
		{Number: 5, From: "acme.demo.alice", Sent: ago(time.Minute), Kind: "result", Trust: "verified", ReplyTo: 42, Body: "yes"},
		{Number: 6, From: "acme.demo.bob", Sent: ago(time.Hour), Kind: "fyi", Trust: "claimed", Relay: "acme.demo.carol #3", Body: "line one\nline two\n"},
	}}}
}

func TestReadText(t *testing.T) {
	code, out, errs := runRead(readBox(), cliframe.Global{}, "2")
	want := "number:   2\nfrom:     acme.demo.bob\nsent:     8s ago\ntrust:    verified\nkind:     question\nurgent:   yes\n\n> ready?\n"
	if code != 0 || out != want || errs != "" {
		t.Errorf("message: %d %q %q", code, out, errs)
	}
	_, out, _ = runRead(readBox(), cliframe.Global{}, "5")
	want = "number:   5\nfrom:     acme.demo.alice\nsent:     1m ago\ntrust:    verified\nkind:     result\nurgent:   no\nreply to: 42\n\n> yes\n"
	if out != want {
		t.Errorf("reply: %q", out)
	}
	_, out, _ = runRead(readBox(), cliframe.Global{}, "6")
	want = "number:   6\nfrom:     acme.demo.bob\nsent:     1h ago\ntrust:    claimed\nkind:     fyi\nurgent:   no\nrelay:    acme.demo.carol #3\n\n> line one\n> line two\n"
	if out != want {
		t.Errorf("relay and lines: %q", out)
	}
	code, out, errs = runRead(readBox(), cliframe.Global{}, "9")
	if code != 1 || out != "" || errs != "error: acme.demo.alice has no message 9\nhint: mash inbox\n" {
		t.Errorf("no such message: %d %q %q", code, out, errs)
	}
	code, out, _ = runRead(readBox(), cliframe.Global{JSON: true}, "9")
	if code != 1 || out != "{\"error\":\"acme.demo.alice has no message 9\",\"hint\":\"mash inbox\"}\n" {
		t.Errorf("no such message, json: %d %q", code, out)
	}
	for _, bad := range [][]string{{}, {"0"}, {"x"}, {"#2"}, {"2", "3"}} {
		if code, out, errs := runRead(readBox(), cliframe.Global{}, bad...); code != 2 || out != "" || !strings.HasPrefix(errs, "error: ") || !strings.HasSuffix(errs, "hint: mash read --help\n") {
			t.Errorf("%v: %d %q %q", bad, code, out, errs)
		}
	}
}

func TestReadJSON(t *testing.T) {
	_, out, _ := runRead(readBox(), cliframe.Global{JSON: true}, "2")
	want := `{"number":2,"from":"acme.demo.bob","sent":"2026-11-19T09:44:04Z","trust":"verified","kind":"question","urgent":true,"reply_to":null,"relay":null,"body":"ready?"}` + "\n"
	if out != want {
		t.Errorf("json %q", out)
	}
	_, out, _ = runRead(readBox(), cliframe.Global{JSON: true}, "5")
	if !strings.Contains(out, `"reply_to":42,"relay":null`) {
		t.Errorf("reply_to: %q", out)
	}
	_, out, _ = runRead(readBox(), cliframe.Global{JSON: true}, "6")
	if !strings.Contains(out, `"relay":"acme.demo.carol #3"`) || !strings.Contains(out, `"body":"line one\nline two\n"`) {
		t.Errorf("relay: %q", out)
	}
}

// Both doors return the same object, and the tool's text is the command line's.
func TestInboxAndReadDoorsAgree(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		cli   func(svc any, g cliframe.Global, args ...string) (int, string, string)
		tool  cmdcore.Tool
		boxes func() *fakeBox
		argv  []string
		json  string
	}{
		{"inbox", runInbox, cmdcore.MCPTool(cmds.Inbox, mailEnv(specBox())), specBox, nil, `{}`},
		{"inbox new", runInbox, cmdcore.MCPTool(cmds.Inbox, mailEnv(specBox())), specBox, []string{"--new", "--limit", "1"}, `{"new":true,"limit":1}`},
		{"inbox wide", runInbox, cmdcore.MCPTool(cmds.Inbox, mailEnv(specBox())), specBox, []string{"--wide"}, `{"wide":true}`},
		{"inbox after", runInbox, cmdcore.MCPTool(cmds.Inbox, mailEnv(specBox())), specBox, []string{"--after", "1"}, `{"after":1}`},
		{"read", runRead, cmdcore.MCPTool(cmds.Read, mailEnv(readBox())), readBox, []string{"5"}, `{"number":5}`},
		{"read relay", runRead, cmdcore.MCPTool(cmds.Read, mailEnv(readBox())), readBox, []string{"6"}, `{"number":6}`},
	} {
		_, jout, _ := c.cli(c.boxes(), cliframe.Global{JSON: true}, c.argv...)
		_, tout, _ := c.cli(c.boxes(), cliframe.Global{}, c.argv...)
		text, structured, err := c.tool.CallStructured(ctx, json.RawMessage(c.json))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		b, _ := json.Marshal(structured)
		if jout != string(b)+"\n" {
			t.Errorf("%s: cli --json %q, tool %s", c.name, jout, b)
		}
		if tout != text+"\n" {
			t.Errorf("%s: cli text %q, tool text %q", c.name, tout, text)
		}
	}
}

func TestInboxReadMatchSpec(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, name := range []string{"inbox", "read"} {
		var o, e bytes.Buffer
		if name == "inbox" {
			cmdcore.CLI(cmds.Inbox, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
		} else {
			cmdcore.CLI(cmds.Read, nil)([]string{"--help"}, nil, &o, &e, cliframe.Global{})
		}
		help := o.String()
		c, meta := specCommand(t, name)
		want := []string{c["description"].(string)}
		for _, a := range append(anyList(c["arguments"]), anyList(c["options"])...) {
			want = append(want, a["description"].(string))
		}
		for _, w := range want {
			if !strings.Contains(norm(help), norm(w)) {
				t.Errorf("%s --help lacks the spec's text %q", name, w)
			}
		}
		for _, ex := range c["examples"].([]any) {
			if !strings.Contains(help, "\n  "+ex.(string)+"\n") {
				t.Errorf("%s --help lacks the spec's example %q", name, ex)
			}
		}
		var tool cmdcore.Tool
		var group string
		var props map[string]any
		var x string
		switch name {
		case "inbox":
			tool, group, props = cmdcore.MCPTool(cmds.Inbox, nil), cmds.Inbox.Group, cmdcore.Schema[cmds.InboxParams]()["properties"].(map[string]any)
			_, x, _ = runInbox(specBox(), cliframe.Global{JSON: true})
		case "read":
			tool, group, props = cmdcore.MCPTool(cmds.Read, nil), cmds.Read.Group, cmdcore.Schema[cmds.ReadParams]()["properties"].(map[string]any)
			_, x, _ = runRead(readBox(), cliframe.Global{JSON: true}, "2")
		}
		if tool.Description != specTool(t, name)["description"] {
			t.Errorf("%s: tool description %q", name, tool.Description)
		}
		if group != meta["x-group"] {
			t.Errorf("%s: group %q, spec %v", name, group, meta["x-group"])
		}
		var specNames, names []string
		for _, p := range specTool(t, name)["params"].([]any) {
			pm := p.(map[string]any)
			specNames = append(specNames, pm["name"].(string))
			got, ok := props[pm["name"].(string)].(map[string]any)
			if !ok || got["description"] != pm["description"] || got["type"] != map[string]string{"string": "string", "boolean": "boolean", "integer": "integer"}[pm["type"].(string)] {
				t.Errorf("%s param %v: schema %v", name, pm["name"], got)
			}
		}
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		sort.Strings(specNames)
		if !reflect.DeepEqual(names, specNames) {
			t.Errorf("%s: schema properties %v, spec %v", name, names, specNames)
		}
		if req := tool.Schema["required"].([]string); (name == "read") != (len(req) == 1) {
			t.Errorf("%s: required %v", name, req)
		}
		sx := string(mustJSON(meta["x-json"]))
		if !sameJSON(t, string(mustJSON(specTool(t, name)["structured"])), sx) {
			t.Errorf("%s: spec/mash.json x-json and spec/mash-mcp.json structured differ", name)
		}
		// the object for the spec's example, field for field (the inbox example lists one of its messages)
		if name == "inbox" {
			var got, spec map[string]any
			json.Unmarshal([]byte(x), &got)
			json.Unmarshal([]byte(sx), &spec)
			msgs := got["messages"].([]any)
			got["messages"] = msgs[:1]
			if !reflect.DeepEqual(got, spec) {
				t.Errorf("inbox object %v, spec x-json %v", got, spec)
			}
		} else if !sameJSON(t, x, sx) {
			t.Errorf("read object %s, spec x-json %s", x, sx)
		}
	}
}

func TestInboxAndReadMatchesSpec(t *testing.T) {
	_, meta := specCommand(t, "inbox")
	long := "The build passed on all four runners, and the rest of the line"
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		want := oc["text"].(string)
		b := specBox()
		var argv []string
		var got string
		switch oc["case"] {
		case "Messages":
			_, got, _ = runInbox(b, cliframe.Global{})
			got = strings.TrimRight(got, "\n")
		case "With --wide":
			_, got, _ = runInbox(b, cliframe.Global{}, "--wide")
			got = strings.TrimRight(got, "\n")
		case "Empty":
			_, got, _ = runInbox(&fakeBox{view: cmds.InboxView{Endpoint: "acme.demo.alice"}}, cliframe.Global{})
			got = strings.TrimRight(got, "\n")
		case "A long message, or one of several lines":
			b.view.Messages = []cmds.MailItem{{Number: 3, From: "acme.demo.bob", Sent: ago(time.Minute), New: true, Kind: "result", Trust: "verified", Body: long + "\nmore"}}
			_, got, _ = runInbox(b, cliframe.Global{}, argv...)
			got = tablePart(got)
		case "The oldest has waited longer than the wake age":
			b.view.Total, b.view.Waiting, b.view.NewUrgent, b.view.OldestWaiting = 3, 3, 0, ago(72*time.Minute)
			b.view.Messages = []cmds.MailItem{{Number: 3, From: "acme.demo.bob", Sent: ago(72 * time.Minute), New: true, Kind: "task", Trust: "verified", Body: "x"}}
			_, got, _ = runInbox(b, cliframe.Global{})
			got, _, _ = strings.Cut(got, "NUMBER")
			want, _, _ = strings.Cut(want, "NUMBER")
		case "More waiting than the wake count":
			b.view.Total, b.view.Waiting, b.view.NewUrgent = 17, 17, 0
			b.view.Messages = []cmds.MailItem{{Number: 3, From: "acme.demo.bob", Sent: ago(time.Minute), New: true, Kind: "task", Trust: "verified", Body: "x"}}
			_, got, _ = runInbox(b, cliframe.Global{})
			got, _, _ = strings.Cut(got, "NUMBER")
			want, _, _ = strings.Cut(want, "NUMBER")
		case "A sender outside your group":
			b.view.Messages = []cmds.MailItem{{Number: 3, From: "acme.ops.carol", Sent: ago(2 * time.Minute), New: true, Kind: "fyi", Trust: "verified", Body: "deploy finished"}}
			_, got, _ = runInbox(b, cliframe.Global{})
			got = tablePart(got)
		default:
			t.Errorf("unknown spec case %v", oc["case"])
			continue
		}
		if oc["case"] == "A long message, or one of several lines" {
			want = strings.Replace(want, "The build passed on all four runn\u2026", "The build passed on all four runn\u2026", 1)
		}
		if got != want {
			t.Errorf("inbox output %q:\n got %q\nwant %q", oc["case"], got, want)
		}
	}
	_, meta = specCommand(t, "read")
	for _, x := range meta["x-output"].([]any) {
		oc := x.(map[string]any)
		var code int
		var out, errs string
		switch oc["case"] {
		case "A message":
			code, out, errs = runRead(readBox(), cliframe.Global{}, "2")
		case "An answer to something you sent":
			code, out, errs = runRead(readBox(), cliframe.Global{}, "5")
		case "No such message":
			code, out, errs = runRead(readBox(), cliframe.Global{}, "9")
		case "A message that was refused":
			continue // the refusal of unverified mail is slice S7: its tests compare this case
		default:
			t.Errorf("unknown spec case %v", oc["case"])
			continue
		}
		text := out
		if oc["stream"] == "stderr" {
			text = errs
		}
		if text != oc["text"].(string)+"\n" || code != int(oc["exit"].(float64)) {
			t.Errorf("read output %q: exit %d %q, spec %q", oc["case"], code, text, oc["text"])
		}
	}
}

func TestReadServiceFailure(t *testing.T) {
	code, _, errs := runRead(&fakeBox{err: errors.New("boom")}, cliframe.Global{}, "1")
	if code != 1 || errs != "error: boom\n" {
		t.Errorf("%d %q", code, errs)
	}
}

func TestReadQuotesEveryLineEmptyOnesToo(t *testing.T) {
	r := cmds.ReadResult{Number: 1, From: "a.b.c", Trust: "verified", Kind: "fyi", Body: "one\n\nthree\n"}
	if got := r.Text(cmdcore.Opts{}); !strings.HasSuffix(got, "\n\n> one\n> \n> three") {
		t.Errorf("text: %q", got)
	}
}

// Every line of a read's text starts with "> ", an empty one too, as the hook quotes it (rules.json).
func TestReadQuotesEveryLineWithASpace(t *testing.T) {
	out := cmds.ReadResult{Number: 1, Body: "one\n\nthree\n"}.Text(cmdcore.Opts{})
	_, body, _ := strings.Cut(out, "\n\n")
	if want := "> one\n> \n> three"; body != want {
		t.Fatalf("body %q, want %q", body, want)
	}
}
