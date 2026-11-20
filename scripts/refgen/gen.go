package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	banner      = "Generated from spec/*.json by scripts/refgen. Do not edit."
	nextRelease = "0.6.0"
	comingPage  = "coming-in-0-6-0.md"
	width       = 78
)

type metaSpec struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type arity struct {
	Minimum int  `json:"minimum"`
	Maximum *int `json:"maximum"`
}

type argSpec struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Required       bool     `json:"required"`
	Arity          arity    `json:"arity"`
	AcceptedValues []string `json:"acceptedValues"`
}

type optSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Aliases     []string   `json:"aliases"`
	Arguments   []argSpec  `json:"arguments"`
	Metadata    []metaSpec `json:"metadata"`
	Recursive   bool       `json:"recursive"`
}

type exitSpec struct {
	Code        int    `json:"code"`
	Description string `json:"description"`
}

type cmdSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Options     []optSpec  `json:"options"`
	Arguments   []argSpec  `json:"arguments"`
	Examples    []string   `json:"examples"`
	ExitCodes   []exitSpec `json:"exitCodes"`
	Metadata    []metaSpec `json:"metadata"`
	Commands    []*cmdSpec `json:"commands"`
}

type toolFile struct {
	Info struct {
		Title       string `json:"title"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"info"`
	Command *cmdSpec `json:"command"`
}

type outCase struct {
	Case   string `json:"case"`
	Text   string `json:"text"`
	Stream string `json:"stream"`
	Exit   int    `json:"exit"`
}

type paramSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Command     string          `json:"command"`
	Release     string          `json:"release"`
	Params      []paramSpec     `json:"params"`
	Structured  json.RawMessage `json:"structured"`
	Text        []outCase       `json:"text"`
	Errors      []outCase       `json:"errors"`
	Note        string          `json:"note"`
}

type mcpFile struct {
	Tools  []mcpTool   `json:"tools"`
	Server [][2]string `json:"server"`
}

type rulesFile struct {
	Rules []struct {
		Rule   string `json:"rule"`
		Detail string `json:"detail"`
	} `json:"rules"`
	Words []struct {
		Word    string `json:"word"`
		From    string `json:"from"`
		Meaning string `json:"meaning"`
	} `json:"words"`
	Coming []struct {
		Change  string `json:"change"`
		Meaning string `json:"meaning"`
	} `json:"coming_in_0_6_0"`
	Setup []struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		What    string `json:"what"`
	} `json:"setup"`
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (c *cmdSpec) meta(name string) (json.RawMessage, bool) {
	for _, m := range c.Metadata {
		if m.Name == name {
			return m.Value, true
		}
	}
	return nil, false
}

func (c *cmdSpec) metaString(name string) string {
	raw, ok := c.meta(name)
	if !ok {
		return ""
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// node is a command with its place in the tree.
type node struct {
	*cmdSpec
	path    []string
	release string
	root    bool
	subs    []*node
}

func (n *node) page() string { return strings.Join(n.path, "-") + ".md" }
func (n *node) full() string { return strings.Join(n.path, " ") }

func buildTree(c *cmdSpec, path []string, inherited string, root bool) *node {
	n := &node{cmdSpec: c, path: append(append([]string{}, path...), c.Name), release: inherited, root: root}
	if r := c.metaString("x-release"); r != "" {
		n.release = r
	}
	for _, s := range c.Commands {
		n.subs = append(n.subs, buildTree(s, n.path, n.release, false))
	}
	return n
}

// Generate builds every reference page from the spec files in specDir. The
// result maps slash-separated paths, relative to the reference folder, to
// page content.
func Generate(specDir string) (map[string][]byte, error) {
	var tools []*node
	var current string
	for _, name := range []string{"mash", "mashctl"} {
		var f toolFile
		if err := readJSON(filepath.Join(specDir, name+".json"), &f); err != nil {
			return nil, err
		}
		if f.Command == nil {
			return nil, fmt.Errorf("%s.json: no command", name)
		}
		v, _, _ := strings.Cut(f.Info.Version, "-")
		if current == "" {
			current = v
		} else if v != current {
			return nil, fmt.Errorf("%s.json: version %s differs from %s", name, v, current)
		}
		tools = append(tools, buildTree(f.Command, nil, current, true))
	}
	var mcp mcpFile
	if err := readJSON(filepath.Join(specDir, "mash-mcp.json"), &mcp); err != nil {
		return nil, err
	}
	var rules rulesFile
	if err := readJSON(filepath.Join(specDir, "rules.json"), &rules); err != nil {
		return nil, err
	}
	g := &gen{current: current, pages: map[string][]byte{}, mcp: mcp, rules: rules, tools: tools}
	return g.run()
}

type gen struct {
	current string
	pages   map[string][]byte
	mcp     mcpFile
	rules   rulesFile
	tools   []*node
	mcpHas  map[string]bool
	coming  []*node
	comingT []mcpTool
}

func (g *gen) put(name string, b *bld) {
	g.pages[name] = []byte(b.String())
}

func (g *gen) run() (map[string][]byte, error) {
	g.mcpHas = map[string]bool{}
	for _, t := range g.mcp.Tools {
		switch t.Release {
		case g.current:
			g.mcpHas[t.Name] = true
		case nextRelease:
			g.comingT = append(g.comingT, t)
		default:
			return nil, fmt.Errorf("mcp tool %s: release %q is neither %s nor %s", t.Name, t.Release, g.current, nextRelease)
		}
	}
	for _, root := range g.tools {
		if err := g.tree(root); err != nil {
			return nil, err
		}
		g.toolIndex(root)
	}
	g.mcpPages()
	g.glossary()
	g.rulesPage()
	g.setupPage()
	g.comingPageOut()
	g.readme()
	return g.pages, nil
}

// tree writes a page for every command of the current release and collects
// the commands that are planned for the next one.
func (g *gen) tree(n *node) error {
	switch n.release {
	case g.current:
		if !n.root {
			g.cmdPage(n)
		}
		for _, s := range n.subs {
			if err := g.tree(s); err != nil {
				return err
			}
		}
	case nextRelease:
		g.coming = append(g.coming, n)
	default:
		return fmt.Errorf("%s: release %q is neither %s nor %s", n.full(), n.release, g.current, nextRelease)
	}
	return nil
}

// ---- markdown helpers ----

type bld struct{ strings.Builder }

func newPage(title string) *bld {
	b := &bld{}
	b.WriteString(banner + "\n\n# " + title + "\n")
	return b
}

func (b *bld) blank() { b.WriteString("\n") }

func (b *bld) heading(level int, text string) {
	b.WriteString(strings.Repeat("#", level) + " " + text + "\n\n")
}

func (b *bld) para(text string) {
	b.WriteString(wrap(text, width, "", "") + "\n\n")
}

func (b *bld) fence(info, text string) {
	f := "```"
	for strings.Contains(text, f) {
		f += "`"
	}
	b.WriteString(f + info + "\n" + text + "\n" + f + "\n\n")
}

func (b *bld) table(head []string, rows [][]string) {
	b.WriteString("| " + strings.Join(head, " | ") + " |\n")
	sep := make([]string, len(head))
	for i := range sep {
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			c = strings.ReplaceAll(c, "|", `\|`)
			c = strings.Join(strings.Fields(c), " ")
			cells[i] = c
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	b.blank()
}

// finish trims the page to exactly one trailing newline.
func (b *bld) String() string {
	return strings.TrimRight(b.Builder.String(), "\n") + "\n"
}

// wrap breaks text at word boundaries so lines stay within w columns. The
// first line starts with first, the others with rest.
func wrap(text string, w int, first, rest string) string {
	var lines []string
	cur := first
	empty := true
	for _, word := range strings.Fields(text) {
		if !empty && len(cur)+1+len(word) > w {
			lines = append(lines, cur)
			cur = rest
			empty = true
		}
		if !empty {
			cur += " "
		}
		cur += word
		empty = false
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n")
}

func code(s string) string { return "`" + s + "`" }

func jsonIndent(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}

// ---- command pages ----

func argSyntax(a argSpec) string {
	s := a.Name
	if a.Arity.Maximum == nil || *a.Arity.Maximum > 1 {
		s += "..."
	}
	if a.Required {
		return "<" + s + ">"
	}
	return "[" + s + "]"
}

func usage(n *node) string {
	u := n.full()
	if len(n.subs) > 0 {
		return u + " <command> [options]"
	}
	for _, a := range n.Arguments {
		u += " " + argSyntax(a)
	}
	return u + " [options]"
}

func optArg(o optSpec) string {
	var parts []string
	for _, a := range o.Arguments {
		s := code("<" + a.Name + ">")
		if len(a.AcceptedValues) > 0 {
			s += ": " + strings.Join(a.AcceptedValues, ", ")
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func optName(o optSpec) string {
	names := []string{code(o.Name)}
	for _, a := range o.Aliases {
		names = append(names, code(a))
	}
	return strings.Join(names, ", ")
}

func (g *gen) optionsTable(b *bld, opts []optSpec) {
	hasEnv := false
	for _, o := range opts {
		for _, m := range o.Metadata {
			if m.Name == "x-env" {
				hasEnv = true
			}
		}
	}
	head := []string{"Option", "Argument", "Description"}
	if hasEnv {
		head = append(head, "Environment")
	}
	var rows [][]string
	for _, o := range opts {
		r := []string{optName(o), optArg(o), o.Description}
		if hasEnv {
			env := ""
			for _, m := range o.Metadata {
				if m.Name == "x-env" {
					var s string
					_ = json.Unmarshal(m.Value, &s)
					env = s
				}
			}
			r = append(r, env)
		}
		rows = append(rows, r)
	}
	b.table(head, rows)
}

func (g *gen) argsTable(b *bld, args []argSpec) {
	var rows [][]string
	for _, a := range args {
		req := "no"
		if a.Required {
			req = "yes"
		}
		rows = append(rows, []string{code(a.Name), req, a.Description})
	}
	b.table([]string{"Argument", "Required", "Description"}, rows)
}

func (g *gen) cases(b *bld, lvl int, cs []outCase) {
	for _, c := range cs {
		b.heading(lvl, c.Case)
		b.WriteString(fmt.Sprintf("Stream: %s. Exit code: %d.\n\n", c.Stream, c.Exit))
		b.fence("text", c.Text)
	}
}

// body writes the sections of one command, with headings at level lvl.
func (g *gen) body(b *bld, n *node, lvl int) {
	b.para(n.Description)
	meta := fmt.Sprintf("Release: %s.", n.release)
	if grp := n.metaString("x-group"); grp != "" {
		meta += " Group: " + grp + "."
	}
	b.para(meta)
	if long := n.metaString("x-long"); long != "" {
		b.para(long)
	}
	b.heading(lvl, "Usage")
	b.fence("text", usage(n))
	if len(n.Options) > 0 {
		b.heading(lvl, "Options")
		g.optionsTable(b, n.Options)
	}
	if !n.root {
		b.para("The global options of " + code(n.path[0]) + " also apply: see [" + n.path[0] + "](" + n.path[0] + ".md#global-options).")
	}
	if len(n.Arguments) > 0 {
		b.heading(lvl, "Arguments")
		g.argsTable(b, n.Arguments)
	}
	if len(n.Examples) > 0 {
		b.heading(lvl, "Examples")
		b.fence("text", strings.Join(n.Examples, "\n"))
	}
	if len(n.ExitCodes) > 0 {
		b.heading(lvl, "Exit codes")
		var rows [][]string
		for _, e := range n.ExitCodes {
			rows = append(rows, []string{fmt.Sprint(e.Code), e.Description})
		}
		b.table([]string{"Code", "Meaning"}, rows)
	}
	if raw, ok := n.meta("x-output"); ok {
		var cs []outCase
		if json.Unmarshal(raw, &cs) == nil && len(cs) > 0 {
			b.heading(lvl, "Output")
			g.cases(b, lvl+1, cs)
		}
	}
	if raw, ok := n.meta("x-json"); ok {
		b.heading(lvl, "JSON")
		b.para("With " + code("--json") + " the result is this shape. The values are an example.")
		b.fence("json", jsonIndent(raw))
	}
	if raw, ok := n.meta("x-mcp"); ok {
		var m struct {
			Tool *string `json:"tool"`
			Why  string  `json:"why"`
		}
		if json.Unmarshal(raw, &m) == nil {
			b.heading(lvl, "MCP tool")
			switch {
			case m.Tool != nil && g.mcpHas[*m.Tool]:
				b.para("[" + code(*m.Tool) + "](mcp-" + *m.Tool + ".md)")
			case m.Tool != nil:
				b.para(code(*m.Tool))
			default:
				b.para("None. " + m.Why)
			}
			if m.Tool != nil && m.Why != "" {
				b.para(m.Why)
			}
		}
	}
	if s := n.metaString("x-notes"); s != "" {
		b.heading(lvl, "Notes")
		b.para(s)
	}
	if s := n.metaString("x-today"); s != "" {
		b.heading(lvl, "What it replaces")
		b.para(s)
	}
}

func (g *gen) cmdPage(n *node) {
	b := newPage(n.full())
	b.blank()
	g.body(b, n, 2)
	if len(n.subs) > 0 {
		b.heading(2, "Commands")
		g.subList(b, n, g.current)
	}
	g.put(n.page(), b)
}

// subList lists the subcommands of n that belong to release rel.
func (g *gen) subList(b *bld, n *node, rel string) {
	var rows [][]string
	for _, s := range n.subs {
		if s.release != rel {
			continue
		}
		rows = append(rows, []string{"[" + code(s.full()) + "](" + s.page() + ")", s.Description})
	}
	b.table([]string{"Command", "Description"}, rows)
}

type removed struct {
	What string `json:"what"`
	Why  string `json:"why"`
}

func (g *gen) toolIndex(n *node) {
	b := newPage(n.full())
	b.blank()
	b.para(n.Description)
	b.heading(2, "Usage")
	b.fence("text", n.full()+" <command> [options]")
	var global []optSpec
	for _, o := range n.Options {
		if o.Recursive {
			global = append(global, o)
		}
	}
	if len(global) > 0 {
		b.heading(2, "Global options")
		b.para("These options work with every " + code(n.Name) + " command.")
		g.optionsTable(b, global)
	}
	var local []optSpec
	for _, o := range n.Options {
		if !o.Recursive {
			local = append(local, o)
		}
	}
	if len(local) > 0 {
		b.heading(2, "Options")
		g.optionsTable(b, local)
	}
	b.heading(2, "Commands")
	var groups []string
	byGroup := map[string][]*node{}
	for _, s := range n.subs {
		if s.release != g.current {
			continue
		}
		grp := s.metaString("x-group")
		if _, ok := byGroup[grp]; !ok {
			groups = append(groups, grp)
		}
		byGroup[grp] = append(byGroup[grp], s)
	}
	for _, grp := range groups {
		if grp != "" {
			b.heading(3, grp)
		}
		var rows [][]string
		for _, s := range byGroup[grp] {
			rows = append(rows, []string{"[" + code(s.full()) + "](" + s.page() + ")", s.Description})
		}
		b.table([]string{"Command", "Description"}, rows)
	}
	if len(n.ExitCodes) > 0 {
		b.heading(2, "Exit codes")
		var rows [][]string
		for _, e := range n.ExitCodes {
			rows = append(rows, []string{fmt.Sprint(e.Code), e.Description})
		}
		b.table([]string{"Code", "Meaning"}, rows)
	}
	if raw, ok := n.meta("x-removed"); ok {
		var rs []removed
		if json.Unmarshal(raw, &rs) == nil && len(rs) > 0 {
			b.heading(2, "Removed in "+g.current)
			var rows [][]string
			for _, r := range rs {
				rows = append(rows, []string{code(r.What), r.Why})
			}
			b.table([]string{"Removed", "Why"}, rows)
		}
	}
	if len(g.coming) > 0 {
		var names []string
		for _, c := range g.coming {
			if c.path[0] == n.Name {
				names = append(names, code(c.full()))
			}
		}
		if len(names) > 0 {
			b.para("Planned for " + nextRelease + ": " + strings.Join(names, ", ") + ". See [coming in " + nextRelease + "](" + comingPage + ").")
		}
	}
	g.put(n.page(), b)
}

// ---- MCP ----

func (g *gen) mcpPages() {
	idx := newPage("MCP tools")
	idx.blank()
	idx.para("The tools mash serves over MCP, and the notes on the server itself.")
	var rows [][]string
	rows = append(rows, []string{"[server](mcp-server.md)", "How the MCP server is started and which protocol revisions it speaks."})
	var tools []mcpTool
	for _, t := range g.mcp.Tools {
		if t.Release == g.current {
			tools = append(tools, t)
		}
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	for _, t := range tools {
		rows = append(rows, []string{"[" + code(t.Name) + "](mcp-" + t.Name + ".md)", firstSentence(t.Description)})
		g.mcpTool(t)
	}
	idx.table([]string{"Page", "Description"}, rows)
	g.put("mcp.md", idx)

	s := newPage("MCP server")
	s.blank()
	var srows [][]string
	for _, kv := range g.mcp.Server {
		srows = append(srows, []string{kv[0], kv[1]})
	}
	s.table([]string{"Topic", "Detail"}, srows)
	g.put("mcp-server.md", s)
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

func (g *gen) mcpTool(t mcpTool) {
	b := newPage("MCP tool " + t.Name)
	b.blank()
	g.mcpBody(b, t, 2)
	g.put("mcp-"+t.Name+".md", b)
}

func (g *gen) mcpBody(b *bld, t mcpTool, lvl int) {
	b.para(t.Description)
	b.para(fmt.Sprintf("Release: %s. Command line equivalent: %s.", t.Release, code(t.Command)))
	if len(t.Params) > 0 {
		b.heading(lvl, "Parameters")
		var rows [][]string
		for _, p := range t.Params {
			req := "no"
			if p.Required {
				req = "yes"
			}
			desc := p.Description
			if len(p.Enum) > 0 {
				desc += " One of: " + strings.Join(p.Enum, ", ") + "."
			}
			rows = append(rows, []string{code(p.Name), p.Type, req, desc})
		}
		b.table([]string{"Name", "Type", "Required", "Description"}, rows)
	}
	if len(t.Structured) > 0 {
		b.heading(lvl, "Structured result")
		b.para("The structured content of a successful result. The values are an example.")
		b.fence("json", jsonIndent(t.Structured))
	}
	if len(t.Text) > 0 {
		b.heading(lvl, "Text result")
		g.cases(b, lvl+1, t.Text)
	}
	if len(t.Errors) > 0 {
		b.heading(lvl, "Errors")
		g.cases(b, lvl+1, t.Errors)
	}
	if t.Note != "" {
		b.heading(lvl, "Notes")
		b.para(t.Note)
	}
}

// ---- glossary, rules, setup ----

func (g *gen) glossary() {
	b := newPage("Glossary")
	b.blank()
	b.para("The words mash uses, and what each one means.")
	ws := g.rules.Words
	sort.SliceStable(ws, func(i, j int) bool {
		return strings.ToLower(ws[i].Word) < strings.ToLower(ws[j].Word)
	})
	var rows [][]string
	for _, w := range ws {
		rows = append(rows, []string{code(w.Word), w.From, w.Meaning})
	}
	b.table([]string{"Word", "From", "Meaning"}, rows)
	g.put("glossary.md", b)
}

func (g *gen) rulesPage() {
	b := newPage("Rules")
	b.blank()
	b.para("The rules every mash command and tool follows.")
	for i, r := range g.rules.Rules {
		b.WriteString(wrap(sentence(r.Rule)+" "+r.Detail, width, fmt.Sprintf("%d. ", i+1), "   ") + "\n\n")
	}
	g.put("rules.md", b)
}

func (g *gen) setupPage() {
	b := newPage("Setup scripts")
	b.blank()
	b.para("The scripts that install mash and set up a local NATS server.")
	for _, s := range g.rules.Setup {
		b.heading(2, s.Name)
		b.para(s.What)
		b.fence("sh", s.Command)
	}
	g.put("setup.md", b)
}

// ---- coming in 0.6.0 ----

func (g *gen) comingPageOut() {
	b := newPage("Coming in " + nextRelease)
	b.blank()
	b.para("What changes in " + nextRelease + ". None of it is in " + g.current + ".")
	if len(g.rules.Coming) > 0 {
		b.heading(2, "Changes")
		for _, c := range g.rules.Coming {
			b.WriteString(wrap(sentence(c.Change)+" "+c.Meaning, width, "- ", "  ") + "\n")
		}
		b.blank()
	}
	if len(g.coming) > 0 {
		b.heading(2, "Commands")
		for _, n := range g.coming {
			g.comingNode(b, n, 3)
		}
	}
	if len(g.comingT) > 0 {
		b.heading(2, "MCP tools")
		for _, t := range g.comingT {
			b.heading(3, code(t.Name))
			g.mcpBody(b, t, 4)
		}
	}
	g.put(comingPage, b)
}

func (g *gen) comingNode(b *bld, n *node, lvl int) {
	b.heading(lvl, code(n.full()))
	g.body(b, n, lvl+1)
	for _, s := range n.subs {
		g.comingNode(b, s, lvl+1)
	}
}

// ---- README ----

func (g *gen) readme() {
	b := newPage("Reference")
	b.blank()
	b.para("The mash and mashctl command reference for release " + g.current + ", generated from the spec files in spec/.")
	b.para("Regenerate: " + code("go run ./scripts/refgen") + ". A test fails when these pages differ from the spec.")
	b.heading(2, "Commands")
	for _, t := range g.tools {
		b.WriteString(wrap("["+code(t.full())+"]("+t.page()+"): "+t.Description, width, "- ", "  ") + "\n")
		g.readmeTree(b, t, 1)
	}
	b.blank()
	b.heading(2, "MCP tools")
	b.WriteString("- [MCP tools](mcp.md)\n- [Server](mcp-server.md)\n")
	var names []string
	for n := range g.mcpHas {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString("  - [" + code(n) + "](mcp-" + n + ".md)\n")
	}
	b.blank()
	b.heading(2, "Concepts")
	b.WriteString("- [Glossary](glossary.md)\n- [Rules](rules.md)\n- [Setup scripts](setup.md)\n")
	b.WriteString("- [Coming in " + nextRelease + "](" + comingPage + ")\n")
	g.put("README.md", b)
}

func (g *gen) readmeTree(b *bld, n *node, depth int) {
	for _, s := range n.subs {
		if s.release != g.current {
			continue
		}
		ind := strings.Repeat("  ", depth)
		b.WriteString(wrap("["+code(s.full())+"]("+s.page()+"): "+s.Description, width, ind+"- ", ind+"  ") + "\n")
		g.readmeTree(b, s, depth+1)
	}
}

// sentence ends s with a full stop unless it already ends in punctuation.
func sentence(s string) string {
	if s == "" || strings.ContainsRune(".!?:", rune(s[len(s)-1])) {
		return s
	}
	return s + "."
}
