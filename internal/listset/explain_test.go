package listset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// showSet is two owner groups: o shares mail and heartbeat and keeps prompt and status closed, p is
// closed throughout. a allows an address of p and denies c; x allows a.
func showSet() ListSet {
	return ListSet{
		Switches: concat(switches("o", Shared, Closed, Closed, Shared), switches("p", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{
			{Sender: "o.a", Allow: []string{"p.x"}, Deny: []string{"o.c"}},
			{Sender: "o.b"},
			{Sender: "o.c", Allow: []string{"o.a"}},
			{Sender: "p.x", Allow: []string{"o.a", "o.b"}},
		},
	}
}

func compiled(t *testing.T, ls ListSet) *Compiled {
	t.Helper()
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type want struct {
	allowed bool
	reason  Reason
}

func TestExplain(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	own, no, al, dn := ReasonOwnerLine, ReasonNoEntry, ReasonAllowTag, ReasonDenyTag
	tests := []struct {
		name, sender, receiver string
		// mail, prompt, status, heartbeat
		w [4]want
	}{
		{"shared group, closed prompt and status", "o.b", "o.a", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
		{"deny within a shared group beats the owner line", "o.a", "o.c", [4]want{{false, dn}, {false, no}, {false, no}, {false, dn}}},
		{"deny is deny_tag on a shared verb and no_entry on a closed one", "o.a", "o.c", [4]want{{false, dn}, {false, no}, {false, no}, {false, dn}}},
		{"allow across groups", "o.a", "p.x", [4]want{{true, al}, {true, al}, {true, al}, {true, al}}},
		{"allow inside a group beats a closed verb", "o.c", "o.a", [4]want{{true, al}, {true, al}, {true, al}, {true, al}}},
		{"receiver outside the group, on no list", "o.b", "p.x", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"closed group, receiver in it, no entry", "p.x", "p.y", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"receiver the set does not name", "o.b", "o.new", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
		{"receiver in a group the set does not name", "o.a", "q.z", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"sender to itself", "o.b", "o.b", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Explain(ls, c, tc.sender, tc.receiver)
			if err != nil {
				t.Fatal(err)
			}
			if e.Describes != DescribesListSet || e.Sender != tc.sender || e.Receiver != tc.receiver || e.RawClientLimit != RawClientLimit {
				t.Errorf("header = %+v", e)
			}
			if len(e.Verbs) != len(Verbs) {
				t.Fatalf("verbs = %+v", e.Verbs)
			}
			for i, v := range Verbs {
				got := e.Verbs[i]
				if got.Verb != v || got.Allowed != tc.w[i].allowed || got.Reason != tc.w[i].reason || got.Detail == "" {
					t.Errorf("%s = %+v, want %+v", v, got, tc.w[i])
				}
			}
		})
	}
}

func TestExplainDetail(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	detail := func(sender, receiver string, i int) string {
		e, err := Explain(ls, c, sender, receiver)
		if err != nil {
			t.Fatal(err)
		}
		return e.Verbs[i].Detail
	}
	for _, tc := range []struct {
		sender, receiver string
		verb             int
		want             string
	}{
		{"o.b", "o.a", 0, `owner group "o" shares mail`},
		{"o.b", "o.a", 3, `owner group "o" shares heartbeat`},
		{"o.a", "p.x", 0, `"p.x" is on the allow list`},
		{"o.a", "o.c", 0, `"o.c" is on the deny list`},
		{"o.b", "o.a", 1, `owner group "o" keeps prompt closed and "o.a" is on no list`},
		{"o.a", "o.c", 1, `owner group "o" keeps prompt closed and "o.c" is on no list`},
		{"o.b", "p.x", 0, `"p.x" is outside owner group "o" and on no list`},
	} {
		if got := detail(tc.sender, tc.receiver, tc.verb); got != tc.want {
			t.Errorf("detail = %q, want %q", got, tc.want)
		}
	}
}

func TestExplainOpenSubjects(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	e, err := Explain(ls, c, "o.b", "o.a")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"_INBOX.>", "$SRV.>", "$JS.API.INFO", "mash.policy", "$KV.mash-leases.agent_o_b"} {
		if !slices.Contains(e.OpenSubjects.Publish, s) {
			t.Errorf("publish lacks %q: %v", s, e.OpenSubjects.Publish)
		}
	}
	wantList(t, "subscribe", e.OpenSubjects.Subscribe, "$SRV.>", "_INBOX.agent_o_b.>")
	for _, s := range slices.Concat(e.OpenSubjects.Publish, e.OpenSubjects.Subscribe) {
		if strings.HasPrefix(s, "agents.") && s != PolicySubject() {
			t.Errorf("open subject %q is an address subject", s)
		}
	}
	if !slices.IsSorted(e.OpenSubjects.Publish) {
		t.Error("publish not sorted")
	}
	if !strings.Contains(e.RawClientLimit, "a raw client can send data to another address") {
		t.Errorf("limit = %q", e.RawClientLimit)
	}
}

func TestExplainErrors(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	bad := showSet()
	bad.Senders[0].Allow = []string{"p.*"}
	for _, tc := range []struct {
		name             string
		ls               ListSet
		sender, receiver string
	}{
		{"unknown sender", ls, "o.nobody", "o.a"},
		{"sender a three-part address (the old form with a range)", ls, "q.o.a", "o.a"},
		{"receiver a three-part address (the old form with a range)", ls, "o.a", "q.o.a"},
		{"receiver not an address", ls, "o.a", "o"},
		{"receiver a wildcard", ls, "o.a", "o.*"},
		{"invalid list set", bad, "o.a", "o.b"},
	} {
		if _, err := Explain(tc.ls, c, tc.sender, tc.receiver); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	if _, err := Explain(ls, nil, "o.a", "o.b"); err == nil {
		t.Error("nil compiled: no error")
	}
	if _, err := Show(bad, c, 1); err == nil {
		t.Error("Show of an invalid list set: no error")
	}
	if _, err := Show(ls, nil, 1); err == nil {
		t.Error("Show with nil compiled: no error")
	}
}

func TestExplainRefusesCompiledThatDisagrees(t *testing.T) {
	ls := showSet()
	other := showSet()
	other.Senders[1].Allow = []string{"o.a", "p.x"} // o.b gains an allow the lists do not give
	other.Senders[1].Deny = []string{"o.c"}
	c := compiled(t, other)
	if _, err := Explain(ls, c, "o.b", "p.x"); err == nil || !strings.Contains(err.Error(), "does not match the lists") {
		t.Errorf("err = %v", err)
	}
}

func TestExplainAgreesWithShow(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	v, err := Show(ls, c, 3)
	if err != nil {
		t.Fatal(err)
	}
	m, err := validate(ls)
	if err != nil {
		t.Fatal(err)
	}
	named := universeNamed(m)
	n := 0
	for _, s := range v.Senders {
		for _, r := range named {
			e, err := Explain(ls, c, s.Sender, r)
			if err != nil {
				t.Fatal(err)
			}
			for i, verb := range Verbs {
				n++
				reach := s.Reach[i]
				if reach.Verb != verb || e.Verbs[i].Verb != verb {
					t.Fatalf("verb order: %v %v", reach.Verb, e.Verbs[i].Verb)
				}
				if got := slices.Contains(reach.Receivers, r); got != e.Verbs[i].Allowed {
					t.Errorf("%s %s %s: show reach %v, explain %v", s.Sender, verb, r, got, e.Verbs[i].Allowed)
				}
			}
		}
	}
	if n != 4*4*len(Verbs) {
		t.Errorf("compared %d", n)
	}
}

func TestShowContent(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	v, err := Show(ls, c, 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.Describes != DescribesListSet || v.Generation != 7 || v.RawClientLimit != RawClientLimit {
		t.Errorf("header = %+v", v)
	}
	if len(v.Switches) != 8 || v.Switches[0] != (SwitchView{"o", Mail, Shared}) || v.Switches[7] != (SwitchView{"p", Heartbeat, Closed}) {
		t.Errorf("switches = %+v", v.Switches)
	}
	var senders []string
	for _, s := range v.Senders {
		senders = append(senders, s.Sender)
	}
	wantList(t, "senders", senders, "o.a", "o.b", "o.c", "p.x")
	a := v.Senders[0]
	wantList(t, "a allow", a.Allow, "p.x")
	wantList(t, "a deny", a.Deny, "o.c")
	wantList(t, "a mail reach", a.Reach[0].Receivers, "o.a", "o.b", "p.x")
	wantList(t, "a prompt reach", a.Reach[1].Receivers, "p.x")
	wantList(t, "b allow", v.Senders[1].Allow)
}

func TestShowIsDeterministicAndNonNil(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	marshal := func(ls ListSet, c *Compiled) []byte {
		v, err := Show(ls, c, 2)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := marshal(ls, c)
	r := reorder(ls)
	if !bytes.Equal(first, marshal(r, compiled(t, r))) {
		t.Error("input order changes the view")
	}
	if bytes.Contains(first, []byte("null")) {
		t.Errorf("a slice marshals as null: %s", first)
	}
	ex := func(ls ListSet, c *Compiled) []byte {
		e, err := Explain(ls, c, "o.a", "o.c")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(e)
		return b
	}
	if !bytes.Equal(ex(ls, c), ex(r, compiled(t, r))) || bytes.Contains(ex(ls, c), []byte("null")) {
		t.Error("explanation differs by input order or holds null")
	}
}

func TestShowExplainGolden(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	v, err := Show(ls, c, 4)
	if err != nil {
		t.Fatal(err)
	}
	out := struct {
		Show     View          `json:"show"`
		Explains []Explanation `json:"explains"`
	}{Show: v, Explains: []Explanation{}}
	for _, p := range [][2]string{{"o.b", "o.a"}, {"o.a", "o.c"}, {"o.a", "p.x"}, {"o.b", "q.z"}} {
		e, err := Explain(ls, c, p[0], p[1])
		if err != nil {
			t.Fatal(err)
		}
		out.Explains = append(out.Explains, e)
	}
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "show_explain.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run Golden -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from %s (run with -update to rewrite):\n%s", path, got)
	}
}

func ExampleExplain() {
	ls := ListSet{
		Switches: switches("o", Shared, Closed, Closed, Shared),
		Senders: []SenderLists{
			{Sender: "o.a", Deny: []string{"o.c"}},
			{Sender: "o.b"},
			{Sender: "o.c"},
		},
	}
	c, err := Compile(ls)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, receiver := range []string{"o.b", "o.c"} {
		e, err := Explain(ls, c, "o.a", receiver)
		if err != nil {
			fmt.Println(err)
			return
		}
		for _, v := range e.Verbs {
			fmt.Printf("%s %s: %t %s\n", receiver, v.Verb, v.Allowed, v.Reason)
		}
	}
	// Output:
	// o.b mail: true owner_line
	// o.b prompt: false no_entry
	// o.b status: false no_entry
	// o.b heartbeat: true owner_line
	// o.c mail: false deny_tag
	// o.c prompt: false no_entry
	// o.c status: false no_entry
	// o.c heartbeat: false deny_tag
}
