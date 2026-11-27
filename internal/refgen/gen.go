package refgen

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
	banner = "Generated from spec/*.json by go run ./scripts/refgen. Do not edit."
	// nextRelease is only the old form of planning an entry (x-release at this value). It is a placeholder, not a
	// promise: no page names it, and the spec plans with x-plan instead.
	nextRelease = "0.6.0"
	// closeRelease is the one release after the current one that a command may
	// already carry: a command whose behaviour closed in it is documented with
	// the rest, and its page says "Release: 0.5.10".
	closeRelease = "0.5.10"
	comingPage   = "planned-not-yet-released.md"
	width        = 78
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
		Source      string `json:"x-source"`
		NowRelease  string `json:"x-now-release"`
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
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Command          string          `json:"command"`
	Release          string          `json:"release"`
	Params           []paramSpec     `json:"params"`
	Structured       json.RawMessage `json:"structured"`
	StructuredDetail json.RawMessage `json:"structured_detail"`
	Text             []outCase       `json:"text"`
	Errors           []outCase       `json:"errors"`
	Note             string          `json:"note"`
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

// metaString returns the string value of a metadata entry, "" when absent.
func (c *cmdSpec) metaString(name string) (string, error) {
	raw, ok := c.meta(name)
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return s, nil
}

// node is a command with its place in the tree.
type node struct {
	*cmdSpec
	path    []string
	release string // "" for an entry planned with x-plan
	plan    string // "", or now, next or later for a planned entry
	root    bool
	subs    []*node
}

func (n *node) page() string { return strings.Join(n.path, "-") + ".md" }
func (n *node) full() string { return strings.Join(n.path, " ") }

func validPlan(p string) bool { return p == "now" || p == "next" || p == "later" }

func buildTree(c *cmdSpec, path []string, current, inheritedRelease, inheritedPlan string, builtSet, root bool) (*node, error) {
	n := &node{cmdSpec: c, path: append(append([]string{}, path...), c.Name), release: inheritedRelease, plan: inheritedPlan, root: root}
	r, err := c.metaString("x-release")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", n.full(), err)
	}
	p, err := c.metaString("x-plan")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", n.full(), err)
	}
	_, hasPlan := c.meta("x-plan")
	if hasPlan {
		if root {
			return nil, fmt.Errorf("%s: the tool root is built and must not carry x-plan", n.full())
		}
		if !validPlan(p) {
			return nil, fmt.Errorf("%s: x-plan %q is not one of now, next, later", n.full(), p)
		}
		if r != "" {
			if r == current {
				return nil, fmt.Errorf("%s: a built entry (x-release %s) must not carry x-plan %s", n.full(), r, p)
			}
			return nil, fmt.Errorf("%s: an unbuilt entry (x-plan %s) must not carry x-release %s", n.full(), p, r)
		}
		if builtSet {
			return nil, fmt.Errorf("%s: a built entry (it inherits x-release %s) must not carry x-plan %s", n.full(), current, p)
		}
		n.release, n.plan, builtSet = "", p, false
	} else if r != "" {
		n.release, n.plan, builtSet = r, "", r == current || r == closeRelease
	}
	for _, s := range c.Commands {
		sub, err := buildTree(s, n.path, current, n.release, n.plan, builtSet, false)
		if err != nil {
			return nil, err
		}
		n.subs = append(n.subs, sub)
	}
	return n, nil
}

// Generate builds every reference page from the spec files in specDir. The
// result maps slash-separated paths, relative to the reference folder, to
// page content.
func Generate(specDir string) (map[string][]byte, error) {
	var tools []*node
	var current, source, now string
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
		if name == "mash" {
			source = f.Info.Source
		} else if f.Info.Source != source {
			return nil, fmt.Errorf("%s.json: x-source %q differs from %q", name, f.Info.Source, source)
		}
		if f.Info.NowRelease != "" {
			if now == "" {
				now = f.Info.NowRelease
			} else if f.Info.NowRelease != now {
				return nil, fmt.Errorf("%s.json: x-now-release %q differs from %q", name, f.Info.NowRelease, now)
			}
		}
		root, err := buildTree(f.Command, nil, current, current, "", false, true)
		if err != nil {
			return nil, err
		}
		tools = append(tools, root)
	}
	var mcp mcpFile
	if err := readJSON(filepath.Join(specDir, "mash-mcp.json"), &mcp); err != nil {
		return nil, err
	}
	var rules rulesFile
	if err := readJSON(filepath.Join(specDir, "rules.json"), &rules); err != nil {
		return nil, err
	}
	g := &gen{current: current, now: now, source: source, pages: map[string][]byte{}, mcp: mcp, rules: rules, tools: tools}
	return g.run()
}

type gen struct {
	current string
	now     string // x-now-release, the version Now plans name
	source  string
	pages   map[string][]byte
	mcp     mcpFile
	rules   rulesFile
	tools   []*node
	mcpHas  map[string]bool
	coming  []*node
	comingT []mcpTool
	err     error
}

// fail records the first problem found in the spec text of a command or tool;
// Generate returns it instead of the pages.
func (g *gen) fail(where, field string, err error) {
	if g.err == nil {
		g.err = fmt.Errorf("%s: %s: %w", where, field, err)
	}
}

// metaStr is metaString that records an error.
func (g *gen) metaStr(n *node, name string) string {
	s, err := n.metaString(name)
	if err != nil {
		g.fail(n.full(), name, err)
	}
	return s
}

func (g *gen) put(name string, b *bld) {
	g.pages[name] = []byte(b.String())
}

func (g *gen) run() (map[string][]byte, error) {
	g.mcpHas = map[string]bool{}
	for _, t := range g.mcp.Tools {
		switch t.Release {
		case g.current, closeRelease:
			g.mcpHas[t.Name] = true
		case nextRelease:
			g.comingT = append(g.comingT, t)
		default:
			return nil, fmt.Errorf("mcp tool %s: release %q is neither %s, %s nor %s", t.Name, t.Release, g.current, closeRelease, nextRelease)
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
	if g.err != nil {
		return nil, g.err
	}
	return g.pages, nil
}

// tree writes a page for every command of the current release and collects
// the commands that are planned for the next one.
func (g *gen) tree(n *node) error {
	if n.plan != "" {
		if err := g.checkPlanned(n); err != nil {
			return err
		}
		g.coming = append(g.coming, n)
		return nil
	}
	switch n.release {
	case g.current, closeRelease:
		if !n.root {
			g.cmdPage(n)
		}
		for _, s := range n.subs {
			if err := g.tree(s); err != nil {
				return err
			}
		}
	case nextRelease: // the old form of planning an entry
		if err := g.checkPlanned(n); err != nil {
			return err
		}
		g.coming = append(g.coming, n)
	default:
		return fmt.Errorf("%s: release %q is neither %s, %s nor %s", n.full(), n.release, g.current, closeRelease, nextRelease)
	}
	return nil
}

// checkPlanned applies to n and every descendant the rules tree applies at
// the top level, since tree stops at a planned parent: x-plan now needs
// x-now-release, and an entry with no plan must name the current release or
// the next one.
func (g *gen) checkPlanned(n *node) error {
	if n.plan == "now" && g.now == "" {
		return fmt.Errorf("%s: x-plan now needs x-now-release in the tool file's info", n.full())
	}
	if n.plan == "" && n.release != g.current && n.release != nextRelease {
		return fmt.Errorf("%s: release %q is neither %s nor %s", n.full(), n.release, g.current, nextRelease)
	}
	for _, s := range n.subs {
		if s.plan == "" && g.released(s.release) {
			return fmt.Errorf("%s: a built entry (x-release %s) cannot sit under a planned parent", s.full(), s.release)
		}
		if err := g.checkPlanned(s); err != nil {
			return err
		}
	}
	return nil
}

// planLabel is the text that says when a planned entry is due: "Planned for
// <x-now-release>" for now, "Planned: next" or "Planned: later", and for the
// old form "Planned for <release>".
func (g *gen) planLabel(n *node) string {
	switch n.plan {
	case "now":
		return "Planned for " + g.now
	case "next", "later":
		return "Planned: " + n.plan
	}
	return "Planned for " + n.release
}

// ---- markdown helpers ----

// Text that comes from the spec is passed through esc before it goes into a
// paragraph, a table cell or a heading; the generator's own markup (code
// spans, links) is added around it. Fenced blocks take spec text verbatim.

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
			c = strings.Join(strings.Fields(c), " ")
			c = strings.ReplaceAll(c, "|", `\|`)
			cells[i] = escLead(c)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	b.blank()
}

// finish trims the page to exactly one trailing newline.
func (b *bld) String() string {
	return strings.TrimRight(b.Builder.String(), "\n") + "\n"
}

// lineHazard reports whether a word would change the meaning of the line if
// it came first: a heading, quote, list item, fence or setext underline.
func lineHazard(w string) bool {
	switch {
	case strings.HasPrefix(w, "#"), strings.HasPrefix(w, ">"),
		strings.HasPrefix(w, "```"), strings.HasPrefix(w, "~~~"):
		return true
	case w == "-" || w == "+" || w == "*":
		return true
	case strings.Trim(w, "-") == "" || strings.Trim(w, "=") == "":
		return true
	}
	n := len(w) - 1
	if n < 1 || (w[n] != '.' && w[n] != ')') {
		return false
	}
	for _, r := range w[:n] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// escLead backslash-escapes a line-start hazard at the start of s.
func escLead(s string) string {
	w, _, _ := strings.Cut(s, " ")
	if w == "" || !lineHazard(w) {
		return s
	}
	if n := len(w) - 1; n >= 1 && (w[n] == '.' || w[n] == ')') && w[0] >= '0' && w[0] <= '9' {
		return w[:n] + `\` + s[n:]
	}
	return `\` + s
}

// wrap breaks text at word boundaries so lines stay within w columns. The
// first line starts with first, the others with rest. A word that would
// start a continuation line as a heading, quote, list item or the like stays
// on the previous line; at the start of the text it is backslash-escaped.
func wrap(text string, w int, first, rest string) string {
	var lines []string
	cur := first
	empty := true
	for i, word := range strings.Fields(text) {
		if i == 0 {
			word = escLead(word)
		}
		if !empty && len(cur)+1+len(word) > w && !lineHazard(word) {
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

// esc makes spec text safe to put in a paragraph, table cell or heading:
// it escapes <, >, & and backslashes before punctuation, and * and _ where
// they could open emphasis. Inline code spans in the text stay verbatim.
func esc(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c == '`' {
			j := i
			for j < len(s) && s[j] == '`' {
				j++
			}
			run := s[i:j]
			if k := indexRun(s[j:], len(run)); k >= 0 {
				end := j + k + len(run)
				b.WriteString(s[i:end])
				i = end
				continue
			}
			b.WriteString(strings.Repeat("\\`", len(run)))
			i = j
			continue
		}
		switch c {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '*':
			b.WriteString(`\*`)
		case '_':
			prev := i > 0 && isWordByte(s[i-1])
			next := i+1 < len(s) && isWordByte(s[i+1])
			if prev && next {
				b.WriteByte(c)
			} else {
				b.WriteString(`\_`)
			}
		case '\\':
			if i+1 < len(s) && s[i+1] < 0x80 && strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", s[i+1]) >= 0 {
				b.WriteString(`\\`)
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}

func isWordByte(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// indexRun finds the first run of exactly n backticks in s.
func indexRun(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return i
		}
		i = j
	}
	return -1
}

// code renders s as an inline code span, with a fence of backticks longer
// than the longest run inside s, and padding spaces when s starts or ends with
// a backtick.
func code(s string) string {
	longest, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	f := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return f + " " + s + " " + f
	}
	return f + s + f
}

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
			s += ": " + esc(strings.Join(a.AcceptedValues, ", "))
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

func (g *gen) optionsTable(b *bld, n *node, opts []optSpec) {
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
		r := []string{optName(o), optArg(o), esc(o.Description)}
		if hasEnv {
			env := ""
			for _, m := range o.Metadata {
				if m.Name == "x-env" {
					var s string
					if err := json.Unmarshal(m.Value, &s); err != nil {
						g.fail(n.full(), "x-env of option "+o.Name, err)
					}
					env = esc(s)
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
		rows = append(rows, []string{code(a.Name), req, esc(a.Description)})
	}
	b.table([]string{"Argument", "Required", "Description"}, rows)
}

func (g *gen) cases(b *bld, lvl int, cs []outCase) {
	for _, c := range cs {
		b.heading(lvl, esc(c.Case))
		b.WriteString(fmt.Sprintf("Stream: %s. Exit code: %d.\n\n", esc(c.Stream), c.Exit))
		b.fence("text", c.Text)
	}
}

// mcpCases writes the cases of an MCP tool result: the text of the result
// and whether isError is set. A tool result has no stream and no exit code.
func (g *gen) mcpCases(b *bld, lvl int, cs []outCase, isError bool) {
	for _, c := range cs {
		b.heading(lvl, esc(c.Case))
		if isError {
			b.para("The call returns this text with " + code("isError") + " set.")
		} else {
			b.para("The call returns this text.")
		}
		b.fence("text", c.Text)
	}
}

// body writes the sections of one command, with headings at level lvl.
func (g *gen) body(b *bld, n *node, lvl int) {
	b.para(esc(n.Description))
	meta := fmt.Sprintf("Release: %s.", esc(n.release))
	if n.plan != "" {
		meta = esc(g.planLabel(n)) + "."
	}
	if grp := g.metaStr(n, "x-group"); grp != "" {
		meta += " Group: " + esc(grp) + "."
	}
	b.para(meta)
	if long := g.metaStr(n, "x-long"); long != "" {
		b.para(esc(long))
	}
	b.heading(lvl, "Usage")
	b.fence("text", usage(n))
	if len(n.Options) > 0 {
		b.heading(lvl, "Options")
		g.optionsTable(b, n, n.Options)
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
			rows = append(rows, []string{fmt.Sprint(e.Code), esc(e.Description)})
		}
		b.table([]string{"Code", "Meaning"}, rows)
	}
	if raw, ok := n.meta("x-output"); ok {
		var cs []outCase
		if err := json.Unmarshal(raw, &cs); err != nil {
			g.fail(n.full(), "x-output", err)
		} else if len(cs) > 0 {
			b.heading(lvl, "Output")
			g.cases(b, lvl+1, cs)
		}
	}
	if raw, ok := n.meta("x-json"); ok {
		b.heading(lvl, "JSON")
		b.para("With " + code("--json") + " the result is this shape. The values are an example.")
		b.fence("json", jsonIndent(raw))
	}
	if raw, ok := n.meta("x-json-detail"); ok {
		b.heading(lvl, "JSON with --detail")
		b.para("With " + code("--json --detail") + " the result is this fuller shape, which contains the usual one. The values are an example.")
		b.fence("json", jsonIndent(raw))
	}
	if raw, ok := n.meta("x-mcp"); ok {
		var m struct {
			Tool *string `json:"tool"`
			Why  string  `json:"why"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			g.fail(n.full(), "x-mcp", err)
		} else {
			b.heading(lvl, "MCP tool")
			switch {
			case m.Tool != nil && g.mcpHas[*m.Tool]:
				b.para("[" + code(*m.Tool) + "](mcp-" + *m.Tool + ".md)")
			case m.Tool != nil:
				b.para(code(*m.Tool))
			default:
				b.para("None. " + esc(m.Why))
			}
			if m.Tool != nil && m.Why != "" {
				b.para(esc(m.Why))
			}
		}
	}
	if s := g.metaStr(n, "x-notes"); s != "" {
		b.heading(lvl, "Notes")
		b.para(esc(s))
	}
	if s := g.metaStr(n, "x-today"); s != "" {
		b.heading(lvl, "What it replaces")
		b.para(esc(s))
	}
}

func (g *gen) cmdPage(n *node) {
	b := newPage(esc(n.full()))
	b.blank()
	g.body(b, n, 2)
	if len(n.subs) > 0 {
		b.heading(2, "Commands")
		g.subList(b, n)
	}
	g.put(n.page(), b)
}

// released reports whether release r is documented: the current release, or
// closeRelease.
func (g *gen) released(r string) bool { return r == g.current || r == closeRelease }

// subList lists the subcommands of n that are released.
func (g *gen) subList(b *bld, n *node) {
	var rows [][]string
	for _, s := range n.subs {
		if !g.released(s.release) {
			continue
		}
		rows = append(rows, []string{"[" + code(s.full()) + "](" + s.page() + ")", esc(s.Description)})
	}
	b.table([]string{"Command", "Description"}, rows)
}

type removed struct {
	What string `json:"what"`
	Why  string `json:"why"`
}

func (g *gen) toolIndex(n *node) {
	b := newPage(esc(n.full()))
	b.blank()
	b.para(esc(n.Description))
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
		g.optionsTable(b, n, global)
	}
	var local []optSpec
	for _, o := range n.Options {
		if !o.Recursive {
			local = append(local, o)
		}
	}
	if len(local) > 0 {
		b.heading(2, "Options")
		g.optionsTable(b, n, local)
	}
	b.heading(2, "Commands")
	var groups []string
	byGroup := map[string][]*node{}
	for _, s := range n.subs {
		if !g.released(s.release) {
			continue
		}
		grp := g.metaStr(s, "x-group")
		if _, ok := byGroup[grp]; !ok {
			groups = append(groups, grp)
		}
		byGroup[grp] = append(byGroup[grp], s)
	}
	for _, grp := range groups {
		if grp != "" {
			b.heading(3, esc(grp))
		}
		var rows [][]string
		for _, s := range byGroup[grp] {
			rows = append(rows, []string{"[" + code(s.full()) + "](" + s.page() + ")", esc(s.Description)})
		}
		b.table([]string{"Command", "Description"}, rows)
	}
	if len(n.ExitCodes) > 0 {
		b.heading(2, "Exit codes")
		var rows [][]string
		for _, e := range n.ExitCodes {
			rows = append(rows, []string{fmt.Sprint(e.Code), esc(e.Description)})
		}
		b.table([]string{"Code", "Meaning"}, rows)
	}
	if raw, ok := n.meta("x-removed"); ok {
		var rs []removed
		if err := json.Unmarshal(raw, &rs); err != nil {
			g.fail(n.full(), "x-removed", err)
		} else if len(rs) > 0 {
			b.heading(2, "Removed in "+g.current)
			var rows [][]string
			for _, r := range rs {
				rows = append(rows, []string{code(r.What), esc(r.Why)})
			}
			b.table([]string{"Removed", "Why"}, rows)
		}
	}
	if len(g.coming) > 0 {
		var labels []string
		byLabel := map[string][]string{}
		for _, c := range g.coming {
			if c.path[0] == n.Name {
				l := g.planLabel(c)
				if _, ok := byLabel[l]; !ok {
					labels = append(labels, l)
				}
				byLabel[l] = append(byLabel[l], code(c.full()))
			}
		}
		for _, l := range labels {
			b.para(esc(l) + ": " + strings.Join(byLabel[l], ", ") + ". See [planned, not yet released](" + comingPage + ").")
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
		if g.released(t.Release) {
			tools = append(tools, t)
		}
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	for _, t := range tools {
		rows = append(rows, []string{"[" + code(t.Name) + "](mcp-" + t.Name + ".md)", esc(firstSentence(t.Description))})
		g.mcpTool(t)
	}
	idx.table([]string{"Page", "Description"}, rows)
	g.put("mcp.md", idx)

	s := newPage("MCP server")
	s.blank()
	var srows [][]string
	for _, kv := range g.mcp.Server {
		srows = append(srows, []string{esc(kv[0]), esc(kv[1])})
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
	b := newPage("MCP tool " + esc(t.Name))
	b.blank()
	g.mcpBody(b, t, 2)
	g.put("mcp-"+t.Name+".md", b)
}

func (g *gen) mcpBody(b *bld, t mcpTool, lvl int) {
	b.para(esc(t.Description))
	b.para(fmt.Sprintf("Release: %s. Command line equivalent: %s.", esc(t.Release), code(t.Command)))
	if len(t.Params) > 0 {
		b.heading(lvl, "Parameters")
		var rows [][]string
		for _, p := range t.Params {
			req := "no"
			if p.Required {
				req = "yes"
			}
			desc := esc(p.Description)
			if len(p.Enum) > 0 {
				desc += " One of: " + esc(strings.Join(p.Enum, ", ")) + "."
			}
			rows = append(rows, []string{code(p.Name), esc(p.Type), req, desc})
		}
		b.table([]string{"Name", "Type", "Required", "Description"}, rows)
	}
	if len(t.Structured) > 0 {
		b.heading(lvl, "Structured result")
		b.para("The structured content of a successful result. The values are an example.")
		b.fence("json", jsonIndent(t.Structured))
	}
	if d := bytes.TrimSpace(t.StructuredDetail); len(d) > 0 && string(d) != "null" {
		b.heading(lvl, "Structured result with --detail")
		b.para("With the " + code("detail") + " parameter set, the structured content is this fuller shape, which contains the usual one. The values are an example.")
		b.fence("json", jsonIndent(t.StructuredDetail))
	}
	if len(t.Text) > 0 {
		b.heading(lvl, "Text result")
		g.mcpCases(b, lvl+1, t.Text, false)
	}
	if len(t.Errors) > 0 {
		b.heading(lvl, "Errors")
		g.mcpCases(b, lvl+1, t.Errors, true)
	}
	if t.Note != "" {
		b.heading(lvl, "Notes")
		b.para(esc(t.Note))
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
		rows = append(rows, []string{code(w.Word), esc(w.From), esc(w.Meaning)})
	}
	b.table([]string{"Word", "From", "Meaning"}, rows)
	g.put("glossary.md", b)
}

func (g *gen) rulesPage() {
	b := newPage("Rules")
	b.blank()
	b.para("The rules every mash command and tool follows.")
	for i, r := range g.rules.Rules {
		b.WriteString(wrap(esc(sentence(r.Rule)+" "+r.Detail), width, fmt.Sprintf("%d. ", i+1), "   ") + "\n\n")
	}
	g.put("rules.md", b)
}

func (g *gen) setupPage() {
	b := newPage("Setup scripts")
	b.blank()
	b.para("The scripts that install mash and set up a local NATS server.")
	for _, s := range g.rules.Setup {
		b.heading(2, esc(s.Name))
		b.para(esc(s.What))
		b.fence("sh", s.Command)
	}
	g.put("setup.md", b)
}

// ---- planned, not yet released ----

func (g *gen) comingPageOut() {
	b := newPage("Planned, not yet released")
	b.blank()
	b.para("What is planned and not yet released. None of it is in " + g.current + ".")
	if len(g.rules.Coming) > 0 {
		b.heading(2, "Changes")
		for _, c := range g.rules.Coming {
			b.WriteString(wrap(esc(sentence(c.Change)+" "+c.Meaning), width, "- ", "  ") + "\n")
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
	if g.source != "" {
		b.para("Source: " + g.source + ".")
	}
	b.para("Regenerate: " + code("go run ./scripts/refgen") + ". A test fails when these pages differ from the spec.")
	b.heading(2, "Commands")
	for _, t := range g.tools {
		b.WriteString(wrap("["+code(t.full())+"]("+t.page()+"): "+esc(t.Description), width, "- ", "  ") + "\n")
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
	b.WriteString("- [Planned, not yet released](" + comingPage + ")\n")
	g.put("README.md", b)
}

func (g *gen) readmeTree(b *bld, n *node, depth int) {
	for _, s := range n.subs {
		if !g.released(s.release) {
			continue
		}
		ind := strings.Repeat("  ", depth)
		b.WriteString(wrap("["+code(s.full())+"]("+s.page()+"): "+esc(s.Description), width, ind+"- ", ind+"  ") + "\n")
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
