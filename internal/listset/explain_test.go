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

// showSet is two owner groups in range r: o shares mail and heartbeat and keeps prompt and status closed, p is
// closed throughout. a allows an address of p and denies c; x allows a.
func showSet() ListSet {
	return ListSet{
		Range:    "r",
		Switches: concat(switches("r.o", Shared, Closed, Closed, Shared), switches("r.p", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{
			{Sender: "r.o.a", Allow: []string{"r.p.x"}, Deny: []string{"r.o.c"}},
			{Sender: "r.o.b"},
			{Sender: "r.o.c", Allow: []string{"r.o.a"}},
			{Sender: "r.p.x", Allow: []string{"r.o.a", "r.o.b"}},
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
		{"shared group, closed prompt and status", "r.o.b", "r.o.a", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
		{"deny within a shared group beats the owner line", "r.o.a", "r.o.c", [4]want{{false, dn}, {false, no}, {false, no}, {false, dn}}},
		{"deny is deny_tag on a shared verb and no_entry on a closed one", "r.o.a", "r.o.c", [4]want{{false, dn}, {false, no}, {false, no}, {false, dn}}},
		{"allow across groups", "r.o.a", "r.p.x", [4]want{{true, al}, {true, al}, {true, al}, {true, al}}},
		{"allow inside a group beats a closed verb", "r.o.c", "r.o.a", [4]want{{true, al}, {true, al}, {true, al}, {true, al}}},
		{"receiver outside the group, on no list", "r.o.b", "r.p.x", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"closed group, receiver in it, no entry", "r.p.x", "r.p.y", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"receiver the set does not name", "r.o.b", "r.o.new", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
		{"receiver in a group the set does not name", "r.o.a", "r.q.z", [4]want{{false, no}, {false, no}, {false, no}, {false, no}}},
		{"sender to itself", "r.o.b", "r.o.b", [4]want{{true, own}, {false, no}, {false, no}, {true, own}}},
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
		{"r.o.b", "r.o.a", 0, `owner group "r.o" shares mail`},
		{"r.o.b", "r.o.a", 3, `owner group "r.o" shares heartbeat`},
		{"r.o.a", "r.p.x", 0, `"r.p.x" is on the allow list`},
		{"r.o.a", "r.o.c", 0, `"r.o.c" is on the deny list`},
		{"r.o.b", "r.o.a", 1, `owner group "r.o" keeps prompt closed and "r.o.a" is on no list`},
		{"r.o.a", "r.o.c", 1, `owner group "r.o" keeps prompt closed and "r.o.c" is on no list`},
		{"r.o.b", "r.p.x", 0, `"r.p.x" is outside owner group "r.o" and on no list`},
	} {
		if got := detail(tc.sender, tc.receiver, tc.verb); got != tc.want {
			t.Errorf("detail = %q, want %q", got, tc.want)
		}
	}
}

func TestExplainOpenSubjects(t *testing.T) {
	ls := showSet()
	c := compiled(t, ls)
	e, err := Explain(ls, c, "r.o.b", "r.o.a")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"_INBOX.>", "$SRV.>", "$JS.API.INFO"} {
		if !slices.Contains(e.OpenSubjects.Publish, s) {
			t.Errorf("publish lacks %q: %v", s, e.OpenSubjects.Publish)
		}
	}
	wantList(t, "subscribe", e.OpenSubjects.Subscribe, "$SRV.>", "_INBOX.r_o_b.>")
	for _, s := range slices.Concat(e.OpenSubjects.Publish, e.OpenSubjects.Subscribe) {
		if strings.HasPrefix(s, "agents.") {
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
	bad.Senders[0].Allow = []string{"r.p.*"}
	for _, tc := range []struct {
		name             string
		ls               ListSet
		sender, receiver string
	}{
		{"unknown sender", ls, "r.o.nobody", "r.o.a"},
		{"sender in another range", ls, "q.o.a", "r.o.a"},
		{"receiver in another range", ls, "r.o.a", "q.o.a"},
		{"receiver not an address", ls, "r.o.a", "r.o"},
		{"receiver a wildcard", ls, "r.o.a", "r.o.*"},
		{"invalid list set", bad, "r.o.a", "r.o.b"},
	} {
		if _, err := Explain(tc.ls, c, tc.sender, tc.receiver); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	if _, err := Explain(ls, nil, "r.o.a", "r.o.b"); err == nil {
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
	other.Senders[1].Allow = []string{"r.o.a", "r.p.x"} // r.o.b gains an allow the lists do not give
	other.Senders[1].Deny = []string{"r.o.c"}
	c := compiled(t, other)
	if _, err := Explain(ls, c, "r.o.b", "r.p.x"); err == nil || !strings.Contains(err.Error(), "does not match the lists") {
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
	if v.Describes != DescribesListSet || v.Generation != 7 || v.Range != "r" || v.RawClientLimit != RawClientLimit {
		t.Errorf("header = %+v", v)
	}
	if len(v.Switches) != 8 || v.Switches[0] != (SwitchView{"r.o", Mail, Shared}) || v.Switches[7] != (SwitchView{"r.p", Heartbeat, Closed}) {
		t.Errorf("switches = %+v", v.Switches)
	}
	var senders []string
	for _, s := range v.Senders {
		senders = append(senders, s.Sender)
	}
	wantList(t, "senders", senders, "r.o.a", "r.o.b", "r.o.c", "r.p.x")
	a := v.Senders[0]
	wantList(t, "a allow", a.Allow, "r.p.x")
	wantList(t, "a deny", a.Deny, "r.o.c")
	wantList(t, "a mail reach", a.Reach[0].Receivers, "r.o.a", "r.o.b", "r.p.x")
	wantList(t, "a prompt reach", a.Reach[1].Receivers, "r.p.x")
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
		e, err := Explain(ls, c, "r.o.a", "r.o.c")
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
	for _, p := range [][2]string{{"r.o.b", "r.o.a"}, {"r.o.a", "r.o.c"}, {"r.o.a", "r.p.x"}, {"r.o.b", "r.q.z"}} {
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
		Range:    "r",
		Switches: switches("r.o", Shared, Closed, Closed, Shared),
		Senders: []SenderLists{
			{Sender: "r.o.a", Deny: []string{"r.o.c"}},
			{Sender: "r.o.b"},
			{Sender: "r.o.c"},
		},
	}
	c, err := Compile(ls)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, receiver := range []string{"r.o.b", "r.o.c"} {
		e, err := Explain(ls, c, "r.o.a", receiver)
		if err != nil {
			fmt.Println(err)
			return
		}
		for _, v := range e.Verbs {
			fmt.Printf("%s %s: %t %s\n", receiver, v.Verb, v.Allowed, v.Reason)
		}
	}
	// Output:
	// r.o.b mail: true owner_line
	// r.o.b prompt: false no_entry
	// r.o.b status: false no_entry
	// r.o.b heartbeat: true owner_line
	// r.o.c mail: false deny_tag
	// r.o.c prompt: false no_entry
	// r.o.c status: false no_entry
	// r.o.c heartbeat: false deny_tag
}
