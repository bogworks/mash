package sap

import (
	"math/rand"
	"strings"
	"testing"

	agents "github.com/bogworks/synadia-agents.go"
)

// oldSubjects is the v0.5.0 string construction, the oracle.
func oldSubjects(token, owner, name string) Subjects {
	s := func(verb string) string { return "agents." + verb + "." + token + "." + owner + "." + name }
	return Subjects{Prompt: s("prompt"), Status: s("status"), HB: s("hb"), Mail: s("mail")}
}

func TestCompatConstants(t *testing.T) {
	for _, c := range []struct{ name, got, v050, sdk string }{
		{"ServiceName", ServiceName, "agents", agents.ServiceName},
		{"QueueGroup", QueueGroup, "agents", agents.QueueGroup},
		{"ProtocolVersion", ProtocolVersion, "0.3", agents.ProtocolVersion},
	} {
		if c.got != c.v050 || c.got != c.sdk {
			t.Errorf("%s = %q, v0.5.0 %q, SDK %q", c.name, c.got, c.v050, c.sdk)
		}
	}
	if AgentToken != "cc" || AgentName != "claude-code" {
		t.Errorf("AgentToken %q, AgentName %q changed", AgentToken, AgentName)
	}
}

func TestCompatSubjectsIdentical(t *testing.T) {
	long := strings.Repeat("a", 32)
	legacyLong := "a" + strings.Repeat("b", 47)
	legacy := [][2]string{
		{"a", "b"}, {"alice", "mash-core"}, {"o1", "n2"}, {"a-b-c", "x9-y8"},
		{legacyLong, legacyLong}, {"homerange", "mash-core"},
	}
	before := subjectFallbacks.Load()
	for _, p := range legacy {
		want := oldSubjects("cc", p[0], p[1])
		if got := For(p[0], p[1]); got != want {
			t.Errorf("For(%q,%q) = %+v, want %+v", p[0], p[1], got, want)
		}
		id, err := Legacy(p[0], p[1])
		if err != nil {
			t.Fatalf("Legacy(%q,%q): %v", p[0], p[1], err)
		}
		if got := id.Subjects(); got != want {
			t.Errorf("legacy identity %q.%q Subjects = %+v, want %+v", p[0], p[1], got, want)
		}
	}
	newForm := [][3]string{
		{"a", "b", "c"}, {"claude-code", "randy", "mash-core"}, {"agent1", "own2", "n3"},
		{long, "y", "a"}, {"x", "y-z", "q9-9"}, {"codex", "homerange", "lane-1"},
		{"a", long, "b"}, {"a", "b", long},
	}
	for _, p := range newForm {
		id, err := New(p[0], p[1], p[2])
		if err != nil {
			t.Fatalf("New(%v): %v", p, err)
		}
		if got, want := id.Subjects(), oldSubjects(p[0], p[1], p[2]); got != want {
			t.Errorf("identity %v Subjects = %+v, want %+v", p, got, want)
		}
	}
	if n := subjectFallbacks.Load() - before; n != 0 {
		t.Errorf("%d fallbacks for valid names", n)
	}
}

// genToken makes a token within the new rule, deterministically.
func genToken(r *rand.Rand, max int) string {
	const letters, rest = "abcdefghijklmnopqrstuvwxyz", "abcdefghijklmnopqrstuvwxyz0123456789"
	n := 1 + r.Intn(max)
	b := []byte{letters[r.Intn(len(letters))]}
	for len(b) < n {
		if r.Intn(6) == 0 && b[len(b)-1] != '-' && len(b) < n-1 {
			b = append(b, '-')
			continue
		}
		b = append(b, rest[r.Intn(len(rest))])
	}
	return string(b)
}

func TestCompatSubjectsNeverFallBack(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	before := subjectFallbacks.Load()
	checked := 0
	for i := 0; i < 600; i++ {
		maxLen := MaxTokenLen
		if i%3 == 0 {
			maxLen = 6
		}
		a, o, n := genToken(r, maxLen), genToken(r, maxLen), genToken(r, maxLen)
		if id, err := New(a, o, n); err == nil {
			if got, want := id.Subjects(), oldSubjects(a, o, n); got != want {
				t.Fatalf("New(%q,%q,%q) Subjects = %+v, want %+v", a, o, n, got, want)
			}
			checked++
		}
		if id, err := Legacy(o, n); err == nil {
			if got, want := id.Subjects(), oldSubjects("cc", o, n); got != want {
				t.Fatalf("Legacy(%q,%q) Subjects = %+v, want %+v", o, n, got, want)
			}
			if got := For(o, n); got != oldSubjects("cc", o, n) {
				t.Fatalf("For(%q,%q) = %+v", o, n, got)
			}
			checked++
		}
	}
	if checked < 300 {
		t.Fatalf("only %d names checked", checked)
	}
	if n := subjectFallbacks.Load() - before; n != 0 {
		t.Errorf("%d fallbacks for names mash accepts", n)
	}
}

func TestCompatSubjectsFallbackStillBuilds(t *testing.T) {
	before := subjectFallbacks.Load()
	got := For("", "a.b")
	if want := oldSubjects("cc", "", "a.b"); got != want {
		t.Errorf("fallback = %+v, want %+v", got, want)
	}
	if subjectFallbacks.Load() != before+1 {
		t.Error("invalid names did not take the fallback")
	}
}
