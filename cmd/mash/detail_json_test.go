package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// objectOf is the JSON object a command printed.
func objectOf(t *testing.T, r ran) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("not a JSON object: %+v: %v", r, err)
	}
	return m
}

// contains checks that --detail only adds: the fuller object holds every key of the usual one, with the same value
// (skip names the keys whose values differ from one run to the next).
func contains(t *testing.T, what string, usual, fuller map[string]any, skip ...string) {
	t.Helper()
next:
	for k, v := range usual {
		for _, s := range skip {
			if k == s {
				if _, ok := fuller[k]; !ok {
					t.Errorf("%s: the fuller object lacks %q", what, k)
				}
				continue next
			}
		}
		if fv, ok := fuller[k]; !ok || !reflect.DeepEqual(fv, v) {
			t.Errorf("%s: %q is %v in the fuller object, %v in the usual one", what, k, fv, v)
		}
	}
}

// labelsOf is the 'label: value' lines a --detail text adds after the usual text, as key (lower case, underscores) to value.
func labelsOf(usual, detail string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(detail, usual), "\n") {
		label, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.ReplaceAll(strings.TrimSpace(label), " ", "_")] = strings.TrimSpace(val)
	}
	return out
}

// send --detail adds to, kind and urgent to the text; with --json the fuller object holds them as keys too, and everything
// the usual one holds.
func TestSendDetailJSONContainsTheUsualObject(t *testing.T) {
	w := newWorld(t, "a", "b")
	args := []string{"send", tTo("b"), "--kind", "question", "--urgent", "hello?"}
	usual := runMash(w.env["a"], append(append([]string{}, args...), "--json")...)
	fuller := runMash(w.env["a"], append(append([]string{}, args...), "--json", "--detail")...)
	if usual.code != 0 || fuller.code != 0 {
		t.Fatalf("%+v %+v", usual, fuller)
	}
	u, f := objectOf(t, usual), objectOf(t, fuller)
	contains(t, "send", u, f, "number") // each send is a new message
	text := runMash(w.env["a"], append(append([]string{}, args...), "--detail")...)
	plain := runMash(w.env["a"], args...)
	labels := labelsOf(strings.TrimSpace(plain.out), strings.TrimSpace(text.out))
	if len(labels) == 0 {
		t.Fatalf("send --detail added no lines: %q", text.out)
	}
	for label, val := range labels {
		var want string
		switch v := f[label].(type) {
		case bool:
			want = map[bool]string{true: "yes", false: "no"}[v]
		case string:
			want = v
		default:
			t.Errorf("send --detail text has the label %q, the object no key for it: %v", label, f)
			continue
		}
		if want != val {
			t.Errorf("send %s: text %q, object %q", label, val, want)
		}
	}
	for _, k := range []string{"to", "kind", "urgent"} {
		if _, ok := f[k]; !ok {
			t.Errorf("no %q in %v", k, f)
		}
	}
}

// status --detail --json holds the usual four things with the same values, and the text's labels are keys of its session.
func TestStatusDetailJSONContainsTheUsualObject(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", nil)
	u := objectOf(t, runMash(env, "status", "--json"))
	f := objectOf(t, runMash(env, "status", "--json", "--detail"))
	contains(t, "status", u, f)
	for _, k := range []string{"reason_code", "session"} {
		if _, ok := f[k]; !ok {
			t.Errorf("no %q in %v", k, f)
		}
	}
	// a session's three labels are the keys of its object
	w2, _ := hooklessWorld(t, 0)
	eventually(t, 10*time.Second, "a session", func() bool { got, _ := shellSession(t, w2); return got != "null" })
	text, _ := w2.cli(t, "a", "status", "--detail")
	_, rest, _ := strings.Cut(text, "\n")
	labels := labelsOf("", rest)
	sess, _ := shellSession(t, w2)
	var m map[string]any
	if err := json.Unmarshal([]byte(sess), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"holds_endpoint", "hooks", "storage"} {
		if _, ok := labels[k]; !ok {
			t.Errorf("the text has no label for %q: %q", k, text)
		}
		if _, ok := m[k]; !ok {
			t.Errorf("the object has no %q: %s", k, sess)
		}
	}
}

// join --detail adds the servers and the creds path to the text: the usual object holds both keys already, and the fuller
// one holds the same.
func TestJoinDetailJSONContainsTheUsualObject(t *testing.T) {
	w := newJoinWorld(t)
	u := objectOf(t, w.run("join", "acme.demo.bob.join", "--dry-run", "--json"))
	f := objectOf(t, w.run("join", "acme.demo.bob.join", "--dry-run", "--json", "--detail"))
	contains(t, "join", u, f)
	for _, k := range []string{"servers", "creds"} {
		if _, ok := f[k]; !ok {
			t.Errorf("no %q in %v", k, f)
		}
	}
}

// A command with nothing more to say prints the same with --detail, in JSON as in text.
func TestDetailChangesNothingWhereThereIsNothingMore(t *testing.T) {
	w := newWorld(t, "a", "b")
	for _, args := range [][]string{{"version"}, {"who"}, {"inbox"}, {"doctor"}} {
		u := runMash(w.env["a"], append(append([]string{}, args...), "--json")...)
		f := runMash(w.env["a"], append(append([]string{}, args...), "--json", "--detail")...)
		if u.code != f.code || u.out != f.out {
			t.Errorf("%v: %+v vs %+v", args, u, f)
		}
	}
}
