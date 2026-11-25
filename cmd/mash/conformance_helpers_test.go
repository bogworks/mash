package main

// Helpers of the conformance test (conformance_test.go): the specification read from spec/, the generated --help layout
// taken apart, and the MCP tool list compared with spec/mash-mcp.json.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type confArg struct {
	Name        string
	Required    bool
	Description string
}

type confOpt struct {
	Name        string
	Description string
	Aliases     []string
	Arguments   []confArg
}

type confCmd struct {
	Name        string
	Description string
	Arguments   []confArg
	Options     []confOpt
	Commands    []confCmd
	Examples    []string
	Metadata    []struct {
		Name  string
		Value json.RawMessage
	}
}

// meta is the string value of the metadata entry name ("" when there is none).
func (c confCmd) meta(name string) string {
	for _, m := range c.Metadata {
		if m.Name == name {
			var s string
			if json.Unmarshal(m.Value, &s) == nil {
				return s
			}
		}
	}
	return ""
}

type confParam struct {
	Name        string
	Type        string
	Required    bool
	Description string
	Enum        []string
}

type confTool struct {
	Name        string
	Description string
	Command     string
	Release     string
	Params      []confParam
}

type confSpec struct {
	Root  confCmd
	Tools []confTool
	Rules []struct{ Rule, Detail string }
}

// confSpecDir is the repository's spec/ directory, found from the test's working directory (go test runs in cmd/mash).
func confSpecDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join("..", "..", "spec")
	if _, err := os.Stat(filepath.Join(d, "mash.json")); err != nil {
		t.Fatalf("the specification is not at %s: %v", d, err)
	}
	return d
}

func loadConfSpec(t *testing.T) confSpec {
	t.Helper()
	dir := confSpecDir(t)
	read := func(name string, into any) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, into); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var s confSpec
	var m struct{ Command confCmd }
	read("mash.json", &m)
	s.Root = m.Command
	var tools struct{ Tools []confTool }
	read("mash-mcp.json", &tools)
	s.Tools = tools.Tools
	var rules struct {
		Rules []struct{ Rule, Detail string }
	}
	read("rules.json", &rules)
	s.Rules = rules.Rules
	return s
}

// confWalk calls f with every command under root and its path ("key create"), parents before children.
func confWalk(root confCmd, f func(path string, c confCmd)) {
	var rec func(prefix string, c confCmd)
	rec = func(prefix string, c confCmd) {
		path := strings.TrimSpace(prefix + " " + c.Name)
		f(path, c)
		for _, s := range c.Commands {
			rec(path, s)
		}
	}
	for _, c := range root.Commands {
		rec("", c)
	}
}

// confTrack names the releases the conformance test holds the build to: the commands of 0.5.9. sap is 0.6.0.
func confTrack(c confCmd) bool {
	r := c.meta("x-release")
	return r == "0.5.9"
}

func confFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// confEnv is an environment with no endpoint installed and nothing of the test process's own in it.
func confEnv(t *testing.T) map[string]string {
	d := t.TempDir()
	return map[string]string{"HOME": d, "MASH_CONFIG_DIR": d}
}

var confHeadings = map[string]bool{"Usage:": true, "Examples:": true, "Arguments:": true, "Options:": true, "Global options:": true}

// confLayout is a command's help taken apart: the lines before Usage:, then each heading's lines.
type confLayout struct {
	intro    []string
	order    []string
	sections map[string][]string
	stray    []string // column-0 lines inside a section that are not a heading
}

func confParseHelp(out string) confLayout {
	l := confLayout{sections: map[string][]string{}}
	cur := ""
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if confHeadings[line] {
			cur = line
			l.order = append(l.order, line)
			continue
		}
		switch {
		case cur == "":
			l.intro = append(l.intro, line)
		case line == "":
		case !strings.HasPrefix(line, " "):
			l.stray = append(l.stray, line)
		default:
			l.sections[cur] = append(l.sections[cur], line)
		}
	}
	return l
}

func confOptLabel(o confOpt) string {
	label := o.Name
	for _, a := range o.Aliases {
		label = a + ", " + label
	}
	for _, a := range o.Arguments {
		label += " <" + a.Name + ">"
	}
	return label
}

func confSynopsis(path string, c confCmd) string {
	s := "mash " + path
	for _, a := range c.Arguments {
		if a.Required {
			s += " <" + a.Name + ">"
		} else {
			s += " [<" + a.Name + ">]"
		}
	}
	if len(c.Options) > 0 {
		s += " [options]"
	}
	return s
}

// confHelpProblems compares the output of `mash <path> --help` with the specification: the layout of rules.json
// ("--help has one layout"), word for word, in 80 columns. It returns one line per difference: check, expected, got.
func confHelpProblems(spec confSpec, env map[string]string, path string, c confCmd) []string {
	var p []string
	r := runMash(env, append(strings.Fields(path), "--help")...)
	if r.code != 0 || r.errs != "" {
		return []string{fmt.Sprintf("exit: want 0 and nothing on stderr, got %d and %q", r.code, r.errs)}
	}
	for i, line := range strings.Split(r.out, "\n") {
		if n := len([]rune(line)); n > 80 {
			p = append(p, fmt.Sprintf("width: line %d is %d columns, want at most 80: %q", i+1, n, line))
		}
	}
	l := confParseHelp(r.out)
	if got := confFlat(strings.Join(l.intro, " ")); got != confFlat(c.Description) {
		p = append(p, fmt.Sprintf("description: want %q first, got %q", c.Description, got))
	}
	want := []string{"Usage:"}
	if len(c.Examples) > 0 {
		want = append(want, "Examples:")
	}
	if len(c.Arguments) > 0 {
		want = append(want, "Arguments:")
	}
	if len(c.Options) > 0 {
		want = append(want, "Options:")
	}
	want = append(want, "Global options:")
	if strings.Join(l.order, ",") != strings.Join(want, ",") {
		p = append(p, fmt.Sprintf("headings: want %v in this order, got %v", want, l.order))
	}
	for _, s := range l.stray {
		p = append(p, fmt.Sprintf("layout: %q is not under a heading; the layout has nothing else", s))
	}
	if len(c.Commands) == 0 {
		if got, want := confFlat(strings.Join(l.sections["Usage:"], " ")), confSynopsis(path, c); got != want {
			p = append(p, fmt.Sprintf("usage: want %q, got %q", want, got))
		}
	}
	have := map[string]bool{}
	for _, line := range l.sections["Examples:"] {
		have[strings.TrimSpace(line)] = true
	}
	for _, ex := range c.Examples {
		if !have[ex] {
			p = append(p, fmt.Sprintf("example: want %q in Examples:, got %q", ex, l.sections["Examples:"]))
		}
	}
	args := confFlat(strings.Join(l.sections["Arguments:"], " "))
	for _, a := range c.Arguments {
		if !strings.Contains(args, a.Name+" "+confFlat(a.Description)) {
			p = append(p, fmt.Sprintf("argument: want %q described as %q in Arguments:", a.Name, a.Description))
		}
	}
	opts := confFlat(strings.Join(l.sections["Options:"], " "))
	for _, o := range c.Options {
		if w := confOptLabel(o) + " " + confFlat(o.Description); !strings.Contains(opts, w) {
			p = append(p, fmt.Sprintf("option: want %q in Options:", w))
		}
	}
	globals := confFlat(strings.Join(l.sections["Global options:"], " "))
	for _, o := range spec.Root.Options {
		if w := confOptLabel(o) + " " + confFlat(o.Description); !strings.Contains(globals, w) {
			p = append(p, fmt.Sprintf("global option: want %q in Global options:", w))
		}
	}
	return p
}

// confTopLevel takes the tool's own help apart: the group headings in order, and the commands listed under each (the
// word after "mash " on an indented line).
func confTopLevel(out string) (order []string, groups map[string][]string) {
	groups = map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if h, ok := strings.CutSuffix(line, ":"); ok && confGroupName(h) {
			cur = h
			order = append(order, h)
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			cur = ""
		}
		if cur != "" && strings.HasPrefix(line, "  mash ") {
			groups[cur] = append(groups[cur], strings.Fields(line)[1])
		}
	}
	return order, groups
}

func confGroupName(s string) bool {
	switch s {
	case "Everyday", "Setup", "Integration", "Raw SAP":
		return true
	}
	return false
}

// confToolProblems compares one tools/list entry with the specification's tool: description, then each parameter's
// name, type, required, enum and description, exact.
func confToolProblems(want confTool, got map[string]any) []string {
	var p []string
	if d, _ := got["description"].(string); d != want.Description {
		p = append(p, fmt.Sprintf("description: want %q, got %q", want.Description, d))
	}
	schema, _ := got["inputSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	req := map[string]bool{}
	for _, r := range anySlice(schema["required"]) {
		req[fmt.Sprint(r)] = true
	}
	seen := map[string]bool{}
	for _, w := range want.Params {
		seen[w.Name] = true
		g, ok := props[w.Name].(map[string]any)
		if !ok {
			p = append(p, fmt.Sprintf("param %s: want it (%s), not offered; offered %v", w.Name, w.Type, confKeys(props)))
			continue
		}
		gotType := fmt.Sprint(g["type"])
		if items, ok := g["items"].(map[string]any); ok && g["type"] == "array" {
			gotType = "array of " + fmt.Sprint(items["type"])
		}
		if gotType != w.Type {
			p = append(p, fmt.Sprintf("param %s type: want %q, got %v", w.Name, w.Type, gotType))
		}
		if req[w.Name] != w.Required {
			p = append(p, fmt.Sprintf("param %s required: want %v, got %v", w.Name, w.Required, req[w.Name]))
		}
		if d, _ := g["description"].(string); d != w.Description {
			p = append(p, fmt.Sprintf("param %s description: want %q, got %q", w.Name, w.Description, d))
		}
		var enum []string
		for _, e := range anySlice(g["enum"]) {
			enum = append(enum, fmt.Sprint(e))
		}
		if strings.Join(enum, ",") != strings.Join(w.Enum, ",") {
			p = append(p, fmt.Sprintf("param %s enum: want %v, got %v", w.Name, w.Enum, enum))
		}
	}
	for _, k := range confKeys(props) {
		if !seen[k] {
			p = append(p, fmt.Sprintf("param %s: offered, but the specification has no such parameter", k))
		}
	}
	for r := range req {
		if props[r] == nil {
			p = append(p, fmt.Sprintf("required %s: not a property", r))
		}
	}
	return p
}

func anySlice(v any) []any { s, _ := v.([]any); return s }

func confKeys(m map[string]any) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}
