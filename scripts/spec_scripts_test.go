package scripts

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// spec/install.json and spec/mash-local-nats-setup.json give install.sh and mash-local-nats-setup.sh an x-output home.
// This test holds each script to its file without running it: every line of every x-output case must be in the script's
// source, so the text and the script cannot drift apart. A line of a case may carry a value that varies in the real
// output (the repository and tag, a port, a pid, a folder); the spec shows an example, and the table below says, for
// each case, which example stands for which shell expression in the source. Every other character must match exactly.

type specOut struct {
	Case   string `json:"case"`
	Text   string `json:"text"`
	Stream string `json:"stream"`
	Exit   int    `json:"exit"`
}

// readSpecOutputs returns the x-output cases of the root command of a spec file in ../spec.
func readSpecOutputs(t *testing.T, file string) []specOut {
	t.Helper()
	b, err := os.ReadFile("../spec/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Command struct {
			Metadata []struct {
				Name  string          `json:"name"`
				Value json.RawMessage `json:"value"`
			} `json:"metadata"`
		} `json:"command"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	for _, m := range f.Command.Metadata {
		if m.Name != "x-output" {
			continue
		}
		var cases []specOut
		if err := json.Unmarshal(m.Value, &cases); err != nil {
			t.Fatalf("%s: x-output: %v", file, err)
		}
		return cases
	}
	t.Fatalf("%s has no x-output", file)
	return nil
}

// sub says that the example in the spec's text stands for the expression in the script's source.
type sub struct{ example, source string }

// checkSpecAgainstScript checks every case of the spec file against the script. subs maps a case name to its
// substitutions, applied in order; a case with no entry has no varying value, and an entry for a case the spec does not
// have is an error, so the table cannot go stale.
func checkSpecAgainstScript(t *testing.T, specFile, script string, subs map[string][]sub) {
	t.Helper()
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	cases := readSpecOutputs(t, specFile)
	if len(cases) == 0 {
		t.Fatalf("%s: no x-output cases", specFile)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.Case] = true
		used := map[string]bool{}
		for _, line := range strings.Split(c.Text, "\n") {
			for _, s := range subs[c.Case] {
				if strings.Contains(line, s.example) {
					used[s.example] = true
				}
				line = strings.ReplaceAll(line, s.example, s.source)
			}
			// error: and hint: lines are printed by die, which adds the prefix; elsewhere the script echoes the whole line.
			body := strings.TrimPrefix(strings.TrimPrefix(line, "error: "), "hint: ")
			isDie := body != line
			var found []string
			for _, l := range lines {
				if strings.Contains(l, body) {
					found = append(found, l)
				}
			}
			if len(found) == 0 {
				t.Errorf("%s: case %q: %q is not in %s", specFile, c.Case, line, script)
				continue
			}
			if !streamMatches(found, c.Stream, isDie) {
				t.Errorf("%s: case %q: %q is not printed on %s in %s (found %q)", specFile, c.Case, line, c.Stream, script, found)
			}
		}
		for _, s := range subs[c.Case] {
			if !used[s.example] {
				t.Errorf("%s: case %q: the text has no example %q", specFile, c.Case, s.example)
			}
		}
	}
	for name := range subs {
		if !seen[name] {
			t.Errorf("%s has no case %q, which this test still substitutes for", specFile, name)
		}
	}
}

var dieCode = regexp.MustCompile(`\bdie (\d+) `)

// streamMatches reports whether one of the source lines prints to stream: a line with >&2 or a die call (stderr), or
// a plain echo or printf (stdout).
func streamMatches(found []string, stream string, isDie bool) bool {
	for _, l := range found {
		toErr := strings.Contains(l, ">&2") || strings.Contains(l, "die ") || strings.Contains(l, "die\t") || isDie
		if (stream == "stderr") == toErr {
			return true
		}
	}
	return false
}

func TestInstallSpecMatchesScript(t *testing.T) {
	checkSpecAgainstScript(t, "install.json", "install.sh", map[string][]sub{
		// the repository (MASH_REPO) and the tag vary with the release installed
		"Verified":                        {{"bogworks/mash v0.5.9", "$REPO $tag"}},
		"The tag's commit cannot be read": {{"bogworks/mash v0.5.9", "$REPO $tag"}},
	})
}

func TestSetupSpecMatchesScript(t *testing.T) {
	checkSpecAgainstScript(t, "mash-local-nats-setup.json", "mash-local-nats-setup.sh", map[string][]sub{
		// the folder nats-server was found in
		"nats-server found off PATH": {{"/usr/sbin/nats-server", "$_f"}},
		// the holder is named by pid or pids, the port is the one in use, the script is named as it was run
		"The port is held": {
			{"(pid 12)", "($_who)"},
			{"port 4222", "port $PORT"},
			{"mash-local-nats-setup.sh --remove", "$SELF --remove"},
		},
	})
}

// The die exit code in the script must be the spec's exit.
func TestSetupSpecExitCodes(t *testing.T) {
	src, err := os.ReadFile("mash-local-nats-setup.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range readSpecOutputs(t, "mash-local-nats-setup.json") {
		if c.Stream != "stderr" || c.Exit == 0 {
			continue
		}
		first := strings.SplitN(c.Text, "\n", 2)[0]
		body := strings.TrimPrefix(first, "error: ")
		if i := strings.Index(body, "port 4222"); i >= 0 {
			body = "port $PORT" + body[i+len("port 4222"):]
		}
		body, _, _ = strings.Cut(body, "(pid")
		ok := false
		for _, l := range strings.Split(string(src), "\n") {
			if m := dieCode.FindStringSubmatch(l); m != nil && strings.Contains(l, body) && m[1] == strconv.Itoa(c.Exit) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("case %q: no die %d line holds %q", c.Case, c.Exit, body)
		}
	}
}
