package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// provisionAliased provisions a three-token endpoint whose directory record carries an alias to the legacy endpoint
// aliasOwner/aliasName, with an unrestricted user: the window bridge does not carry the legacy-inbox grant yet (naming
// plan 1.4c), so delivery with provisioned credentials is not testable here. It returns the environment its mash
// commands run with.
func provisionAliased(t *testing.T, w *world, agent, owner, name, aliasOwner, aliasName string) map[string]string {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := w.e.MintRaw(t, id.Flat(), jwt.Permissions{})
	ctx := context.Background()
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.CreateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	rec := store.Record{Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent, Account: w.e.Account,
		User: user, MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339),
		Alias: &store.Alias{Owner: aliasOwner, Name: aliasName}}
	val, _ := json.Marshal(rec)
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": t.TempDir(), "MASH_NAME": id.Name, "MASH_OWNER": id.Owner, "MASH_AGENT_TOKEN": id.Agent,
		"MASH_URL": w.e.URL, "MASH_CREDS": creds, "MASH_SETTLE_MS": "20"}
}

func toolSchema(t *testing.T, s *session, name string) map[string]any {
	t.Helper()
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == name {
			return m
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

// The read and inbox tools take the qualified number: "L1", "#L1", "1", "#1" or the integer 1, and the mail of the
// legacy stream is shown as #L1.
func TestServeReadsLegacyStreamByQualifiedNumber(t *testing.T) {
	w := newWorld(t, "old", "snd")
	if out, code := w.cli(t, "snd", "send", "old", "queued for the old name"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	env := provisionAliased(t, w, "ag", "team", "new", w.e.Owner, "old")
	s := startServe(t, env)
	s.initialize(t)
	rd := toolSchema(t, s, "read")
	if d := rd["description"].(string); !strings.Contains(d, "L5") {
		t.Fatalf("description %q", d)
	}
	if typ := rd["inputSchema"].(map[string]any)["properties"].(map[string]any)["seq"].(map[string]any)["type"]; len(typ.([]any)) != 2 {
		t.Fatalf("seq type %v", typ)
	}
	ev := content(s.await(t, 10*time.Second, isChannel))
	if !strings.Contains(ev, "--- #L1 from snd [verified]") || !strings.Contains(ev, "> queued for the old name") {
		t.Fatalf("event %q", ev)
	}
	id := 10
	for _, seq := range []string{`"L1"`, `"#L1"`, `"l1"`} {
		id++
		r := s.call(t, id, "read", `{"seq":`+seq+`}`)
		txt := resultText(r)
		if seq == `"l1"` {
			if r["isError"] != true {
				t.Fatalf("read %s: %q", seq, txt)
			}
			continue
		}
		if r["isError"] == true || !strings.Contains(txt, "--- #L1 from snd [verified]") {
			t.Fatalf("read %s: %v %q", seq, r["isError"], txt)
		}
	}
	// The own stream is empty: bare 1 does not resolve to the legacy stream.
	for _, seq := range []string{`1`, `"1"`, `"#1"`} {
		id++
		r := s.call(t, id, "read", `{"seq":`+seq+`}`)
		if r["isError"] != true || strings.Contains(resultText(r), "queued for the old name") {
			t.Fatalf("read %s: %v %q", seq, r["isError"], resultText(r))
		}
	}
	id++
	if txt := resultText(s.call(t, id, "inbox", `{}`)); !strings.HasPrefix(txt, "#L1\tfrom snd [verified]\t") {
		t.Fatalf("inbox %q", txt)
	}
}

// A legacy-only endpoint's read tool is what it was: an integer seq and the same description. A string number still
// reads, and L5 is an error it can read.
func TestServeLegacyOnlyReadToolIsUnchanged(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", "a", "hello"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	rd := toolSchema(t, s, "read")
	if d := rd["description"].(string); d != "Read one message from your inbox by #seq." {
		t.Fatalf("description %q", d)
	}
	seq := rd["inputSchema"].(map[string]any)["properties"].(map[string]any)["seq"].(map[string]any)
	if seq["type"] != "integer" || seq["description"] != "message #seq" {
		t.Fatalf("seq schema %v", seq)
	}
	ev := content(s.await(t, 10*time.Second, isChannel))
	if !strings.Contains(ev, "--- #1 from b [verified]") {
		t.Fatalf("event %q", ev)
	}
	for i, arg := range []string{`1`, `"1"`, `"#1"`} {
		r := s.call(t, 20+i, "read", `{"seq":`+arg+`}`)
		if r["isError"] == true || !strings.Contains(resultText(r), "--- #1 from b [verified]") {
			t.Fatalf("read %s: %q", arg, resultText(r))
		}
	}
	r := s.call(t, 30, "read", `{"seq":"L1"}`)
	if r["isError"] != true || !strings.Contains(resultText(r), "no legacy stream on this endpoint") {
		t.Fatalf("read L1: %v %q", r["isError"], resultText(r))
	}
}

func sendProps(t *testing.T, s *session) map[string]any {
	t.Helper()
	return toolSchema(t, s, "send")["inputSchema"].(map[string]any)["properties"].(map[string]any)
}

// A legacy-only endpoint's send schema is what it was: in_reply_to and via are integers with today's text. The
// numbers still resolve as bare primary refs, as integers or strings.
func TestServeLegacyOnlySendSchemaIsUnchanged(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	p := sendProps(t, s)
	want := map[string]string{
		"in_reply_to": "optional #seq of the message this answers; sent signed, so recipients see 'answers #N' when it verifies",
		"via": "optional #seq of a message in your own inbox: declare that this message relays or quotes message #N of your inbox; " +
			"recipients see 'relayed from <sender> #N' when the declaration verifies; omit it for ordinary mail",
	}
	for f, d := range want {
		m := p[f].(map[string]any)
		if m["type"] != "integer" || m["description"] != d || len(m) != 2 {
			t.Fatalf("%s schema %v", f, m)
		}
	}
	for i, arg := range []string{`5`, `"5"`, `"#5"`} {
		if r := s.call(t, 40+i, "send", `{"to":"b","text":"x","in_reply_to":`+arg+`}`); r["isError"] == true {
			t.Fatalf("send in_reply_to %s: %q", arg, resultText(r))
		}
		// The inbox is empty: via resolves to primary #5, which is not there (not a parse or stream error).
		if r := s.call(t, 45+i, "send", `{"to":"b","text":"x","via":`+arg+`}`); r["isError"] != true || !strings.Contains(resultText(r), "no message #5 in your inbox") {
			t.Fatalf("send via %s: %q", arg, resultText(r))
		}
	}
}

// On a two-stream endpoint in_reply_to and via take an integer or a string; a legacy-stream ref is refused for each,
// and primary refs work as they did.
func TestServeSendRefusesLegacyStreamRefs(t *testing.T) {
	w := newWorld(t, "old", "snd")
	env := provisionAliased(t, w, "ag", "team", "new", w.e.Owner, "old")
	s := startServe(t, env)
	s.initialize(t)
	p := sendProps(t, s)
	for _, f := range []string{"in_reply_to", "via"} {
		if typ := p[f].(map[string]any)["type"]; len(typ.([]any)) != 2 {
			t.Fatalf("%s type %v", f, typ)
		}
	}
	id := 50
	for _, arg := range []string{`5`, `"5"`, `"#5"`} {
		for _, f := range []string{"in_reply_to", "via"} {
			id++
			r := s.call(t, id, "send", `{"to":"snd","text":"x","`+f+`":`+arg+`}`)
			// in_reply_to sends; via names primary #5, which is not in the (empty) inbox: resolved, not refused.
			if (f == "in_reply_to") == (r["isError"] == true) || (f == "via" && !strings.Contains(resultText(r), "no message #5 in your inbox")) {
				t.Fatalf("send %s=%s: %v %q", f, arg, r["isError"], resultText(r))
			}
		}
	}
	for _, arg := range []string{`"L5"`, `"#L5"`} {
		for _, f := range []string{"in_reply_to", "via"} {
			id++
			r := s.call(t, id, "send", `{"to":"snd","text":"x","`+f+`":`+arg+`}`)
			if r["isError"] != true || !strings.Contains(resultText(r), "in_reply_to/via cannot name a legacy-stream message (#L5) yet") {
				t.Fatalf("send %s=%s: %v %q", f, arg, r["isError"], resultText(r))
			}
		}
	}
}
