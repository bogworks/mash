package policyfile

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// edit replaces data[start:end] with text.
type edit struct {
	start, end int
	text       string
}

// splice applies non-overlapping edits. Edits at the same start apply longest first, so an insertion at the
// place a deletion starts lands after the deletion.
func splice(data []byte, edits []edit) []byte {
	es := append([]edit(nil), edits...)
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].start != es[j].start {
			return es[i].start > es[j].start
		}
		return es[i].end > es[j].end
	})
	out := append([]byte(nil), data...)
	for _, e := range es {
		out = append(out[:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	return out
}

// tree is the file's text with its parsed node tree: the nodes say where things are, the text is what gets edited.
type tree struct {
	path      string
	data      []byte
	top       *yaml.Node
	lineStart []int // lineStart[i] is the offset of line i+1
}

func (d *Doc) tree() (*tree, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(d.data, &root); err != nil {
		return nil, err
	}
	top := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		top = root.Content[0]
	}
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("policy file: not a mapping")
	}
	t := &tree{path: d.path, data: d.data, top: top, lineStart: []int{0}}
	for i, b := range d.data {
		if b == '\n' {
			t.lineStart = append(t.lineStart, i+1)
		}
	}
	if err := t.vet(); err != nil {
		return nil, err
	}
	return t, nil
}

// flowErr is the refusal for a node written in flow style, which edits cannot splice without touching its siblings.
func (t *tree) flowErr(what string, n *yaml.Node) error {
	return fmt.Errorf("policy file %s: line %d: %s is written in flow style ({...}); edit it by hand or rewrite it in block style", t.path, n.Line, what)
}

// unsupported is the refusal for a shape the editor does not splice.
func (t *tree) unsupported(n *yaml.Node, why string) error {
	return fmt.Errorf("policy file %s: line %d: %s; edit it by hand", t.path, n.Line, why)
}

// vet refuses files whose endpoints section has a shape the editor does not splice: a flow-style top level, anchors
// and aliases, and scalars that span lines. (Flow-style maps are refused where an edit would touch them, since a
// flow node shares its lines with its siblings; see the edit operations.)
func (t *tree) vet() error {
	if isFlow(t.top) {
		return t.flowErr("the top level of the file", t.top)
	}
	_, ev := get(t.top, "endpoints")
	if ev == nil {
		return nil
	}
	var walk func(n *yaml.Node) error
	walk = func(n *yaml.Node) error {
		switch {
		case n.Kind == yaml.AliasNode || n.Anchor != "":
			return t.unsupported(n, "endpoints uses a YAML anchor or alias")
		case (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && isFlow(n) && t.flowHasComment(n):
			return t.unsupported(n, "endpoints has a comment inside a {...} or [...]")
		case n.Kind == yaml.ScalarNode:
			if t.multiline(n) {
				return t.unsupported(n, "endpoints has a scalar that spans several lines")
			}
		}
		for _, c := range n.Content {
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(ev)
}

// multiline reports whether the quoted or plain scalar n spans more than one line.
func (t *tree) multiline(n *yaml.Node) bool {
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return false
	}
	o, end := t.nodeOff(n), t.tokenEnd(n)
	return end > o && t.lineOf(end-1) != t.lineOf(o)
}

// nodeEnd is the offset just past n's last token.
func (t *tree) nodeEnd(n *yaml.Node) int {
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		if isFlow(n) {
			return t.closeOff(n)
		}
		if len(n.Content) == 0 {
			return t.nodeOff(n)
		}
		last := n.Content[len(n.Content)-1]
		if n.Kind == yaml.MappingNode && isEmptyNull(last) {
			return t.tokenEnd(n.Content[len(n.Content)-2])
		}
		return t.nodeEnd(last)
	case yaml.ScalarNode:
		if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return t.lineOff(t.endLine(n) + 1)
		}
		return t.tokenEnd(n)
	}
	return t.nodeOff(n)
}

// soleOnLines reports whether data[start:end] is all there is on its lines but whitespace and a trailing comment.
func (t *tree) soleOnLines(start, end int) bool {
	if end > len(t.data) {
		end = len(t.data)
	}
	for _, b := range t.data[t.lineOff(t.lineOf(start)):start] {
		if b != ' ' && b != '\t' {
			return false
		}
	}
	rest := t.data[end:]
	if i := bytes.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	rest = bytes.TrimLeft(rest, " \t\r")
	return len(rest) == 0 || rest[0] == '#'
}

// flowHasComment reports whether a '#' that could start a comment sits inside the flow collection n.
func (t *tree) flowHasComment(n *yaml.Node) bool {
	o, end := t.nodeOff(n), t.closeOff(n)
	for i := o; i < end; i++ {
		if t.data[i] == '#' && i > 0 && strings.IndexByte(" \t\n", t.data[i-1]) >= 0 {
			return true
		}
	}
	return false
}

// off is the byte offset of a node position (1-based line, column counted in characters).
func (t *tree) off(line, col int) int {
	if line > len(t.lineStart) {
		return len(t.data)
	}
	o := t.lineStart[line-1]
	for i := 1; i < col && o < len(t.data); i++ {
		_, w := utf8.DecodeRune(t.data[o:])
		o += w
	}
	return o
}

func (t *tree) nodeOff(n *yaml.Node) int { return t.off(n.Line, n.Column) }

// lineOff is the offset where line starts, len(data) past the last line.
func (t *tree) lineOff(line int) int {
	if line > len(t.lineStart) {
		return len(t.data)
	}
	return t.lineStart[line-1]
}

// lineOf is the line number holding offset o.
func (t *tree) lineOf(o int) int {
	return sort.Search(len(t.lineStart), func(i int) bool { return t.lineStart[i] > o })
}

func get(m *yaml.Node, key string) (k, v *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

type entry struct{ key, val *yaml.Node }

func (t *tree) endpoint(name string) *entry {
	_, ev := get(t.top, "endpoints")
	if k, v := get(ev, name); k != nil {
		return &entry{k, v}
	}
	return nil
}

func isEmptyNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Value == ""
}

func isFlow(n *yaml.Node) bool { return n.Style&yaml.FlowStyle != 0 }

// closeOff is the offset just past the bracket closing the flow collection n.
func (t *tree) closeOff(n *yaml.Node) int {
	o := t.nodeOff(n)
	depth := 0
	for i := o; i < len(t.data); i++ {
		switch c := t.data[i]; c {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"':
			for i++; i < len(t.data) && t.data[i] != '"'; i++ {
				if t.data[i] == '\\' {
					i++
				}
			}
		case '\'':
			for i++; i < len(t.data); i++ {
				if t.data[i] == '\'' {
					if i+1 < len(t.data) && t.data[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		}
	}
	return len(t.data)
}

// endLine is the last line the node occupies.
func (t *tree) endLine(n *yaml.Node) int {
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		if isFlow(n) {
			return t.lineOf(t.closeOff(n) - 1)
		}
		if len(n.Content) == 0 {
			return n.Line
		}
		last := n.Content[len(n.Content)-1]
		if n.Kind == yaml.MappingNode && isEmptyNull(last) {
			return n.Content[len(n.Content)-2].Line
		}
		return t.endLine(last)
	case yaml.ScalarNode:
		if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return n.Line + strings.Count(strings.TrimRight(n.Value, "\n"), "\n") + 1
		}
	}
	return n.Line
}

// tokenEnd is the offset just past the scalar n.
func (t *tree) tokenEnd(n *yaml.Node) int {
	o := t.nodeOff(n)
	if o >= len(t.data) {
		return o
	}
	switch t.data[o] {
	case '"':
		for i := o + 1; i < len(t.data); i++ {
			if t.data[i] == '\\' {
				i++
			} else if t.data[i] == '"' {
				return i + 1
			}
		}
	case '\'':
		for i := o + 1; i < len(t.data); i++ {
			if t.data[i] == '\'' {
				if i+1 < len(t.data) && t.data[i+1] == '\'' {
					i++
					continue
				}
				return i + 1
			}
		}
	}
	return o + len(n.Value)
}

// lineSpan is the offsets of lines from..to inclusive, through each one's newline.
func (t *tree) lineSpan(from, to int) (int, int) { return t.lineOff(from), t.lineOff(to + 1) }

// insertAfterLine is an edit inserting text as whole lines after line.
func (t *tree) insertAfterLine(line int, text string) edit {
	o := t.lineOff(line + 1)
	if o == len(t.data) && len(t.data) > 0 && t.data[len(t.data)-1] != '\n' {
		text = "\n" + text
	}
	return edit{o, o, text}
}

func indentOf(k *yaml.Node) string { return strings.Repeat(" ", k.Column-1) }

// blockStep is how far nested block mappings are indented in the file, 2 when it cannot tell.
func blockStep(m *yaml.Node) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if v := m.Content[i+1]; v.Kind == yaml.MappingNode && !isFlow(v) && len(v.Content) > 0 {
			if s := v.Content[0].Column - m.Content[i].Column; s > 0 {
				return s
			}
		}
	}
	return 2
}

// labelsFlow reports whether the file writes labels in flow style ({k: v}); true when it has no labels yet.
func (t *tree) labelsFlow() bool {
	_, ev := get(t.top, "endpoints")
	if ev != nil {
		for i := 1; i < len(ev.Content); i += 2 {
			if _, l := get(ev.Content[i], "labels"); l != nil && l.Kind == yaml.MappingNode && len(l.Content) > 0 {
				return isFlow(l)
			}
		}
	}
	return true
}

// labelLines renders `labels:` and its pairs as lines at indent.
func labelLines(indent, step string, flow bool, labels map[string]string, order []string) string {
	if flow {
		return indent + "labels: " + flowLabels(labels, order) + "\n"
	}
	var b strings.Builder
	b.WriteString(indent + "labels:\n")
	for _, k := range order {
		b.WriteString(indent + step + k + ": " + scalar(labels[k]) + "\n")
	}
	return b.String()
}

func entryText(name, indent, step string, flowEntry, flowLab bool, labels map[string]string) string {
	order := sortedKeys(labels)
	switch {
	case len(labels) == 0:
		return indent + name + ": {}\n"
	case flowEntry:
		return indent + name + ": {labels: " + flowLabels(labels, order) + "}\n"
	}
	return indent + name + ":\n" + labelLines(indent+step, step, flowLab, labels, order)
}

func (t *tree) addEndpoint(name string, labels map[string]string) ([]edit, error) {
	flowLab := t.labelsFlow()
	ek, ev := get(t.top, "endpoints")
	if ek == nil {
		pre := ""
		if n := len(t.data); n > 0 {
			if t.data[n-1] != '\n' {
				pre = "\n"
			}
			pre += "\n"
		}
		return []edit{{len(t.data), len(t.data), pre + "endpoints:\n" + entryText(name, "  ", "  ", false, flowLab, labels)}}, nil
	}
	const indent, step = "  ", "  "
	switch {
	case ev.Kind == yaml.MappingNode && isFlow(ev) && len(ev.Content) == 0, isEmptyNull(ev), ev.Kind == yaml.ScalarNode && ev.Tag == "!!null":
		// `endpoints: {}`, `endpoints:` or `endpoints: ~`: turn it into a block mapping with the new entry.
		var edits []edit
		line := ek.Line
		if !isEmptyNull(ev) {
			start, end := t.nodeOff(ev), len(t.data)
			if ev.Kind == yaml.MappingNode {
				end = t.closeOff(ev)
			} else {
				end = t.tokenEnd(ev)
			}
			for start > 0 && t.data[start-1] == ' ' {
				start--
			}
			edits = append(edits, edit{start, end, ""})
			line = t.lineOf(end - 1)
		}
		return append(edits, t.insertAfterLine(line, entryText(name, indent, step, false, flowLab, labels))), nil
	case ev.Kind == yaml.MappingNode && isFlow(ev):
		return nil, t.flowErr("endpoints", ev)
	case ev.Kind != yaml.MappingNode:
		return nil, t.unsupported(ev, "endpoints is not a mapping")
	}
	if last := ev.Content[len(ev.Content)-1]; !t.soleOnLines(t.nodeOff(ev.Content[0]), t.nodeEnd(last)) {
		return nil, t.unsupported(last, "the last endpoint shares its line with other content")
	}
	ind := indentOf(ev.Content[0])
	st := strings.Repeat(" ", blockStep(ev))
	flowEntry := false
	for i := 1; i < len(ev.Content); i += 2 {
		v := ev.Content[i]
		if v.Kind == yaml.MappingNode && !isFlow(v) && len(v.Content) > 0 {
			flowEntry = false
			break
		}
		if v.Kind == yaml.MappingNode && isFlow(v) && len(v.Content) > 0 {
			flowEntry = true
		}
	}
	return []edit{t.insertAfterLine(t.endLine(ev), entryText(name, ind, st, flowEntry, flowLab, labels))}, nil
}

func (t *tree) removeEndpoint(name string) ([]edit, error) {
	e := t.endpoint(name)
	if _, ev := get(t.top, "endpoints"); isFlow(ev) {
		return nil, t.flowErr("endpoints", ev)
	}
	end := t.tokenEnd(e.key)
	if !isEmptyNull(e.val) {
		end = t.nodeEnd(e.val)
	}
	if !t.soleOnLines(t.nodeOff(e.key), end) {
		return nil, t.unsupported(e.key, fmt.Sprintf("endpoint %q shares its line with other content", name))
	}
	from, to := t.lineSpan(e.key.Line, t.lineOf(end-1))
	return []edit{{from, to, ""}}, nil
}

func (t *tree) setLabels(name string, set map[string]string, unset []string) ([]edit, error) {
	e := t.endpoint(name)
	v := e.val
	if _, ev := get(t.top, "endpoints"); isFlow(ev) {
		return nil, t.flowErr("endpoints", ev)
	}
	lk, lv := get(v, "labels")

	// The labels as they will be: existing keys in file order minus unset, then new keys sorted.
	cur := map[string]*yaml.Node{}
	var order []string
	if lv != nil && lv.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(lv.Content); i += 2 {
			cur[lv.Content[i].Value] = lv.Content[i+1]
			order = append(order, lv.Content[i].Value)
		}
	} else if lv != nil && !isEmptyNull(lv) {
		return nil, t.unsupported(lv, fmt.Sprintf("endpoint %q: labels is not a mapping", name))
	}
	drop := map[string]bool{}
	for _, k := range unset {
		drop[k] = true
	}
	final := map[string]string{}
	var fin []string
	for _, k := range order {
		if !drop[k] {
			final[k] = cur[k].Value
			fin = append(fin, k)
		}
	}
	for _, k := range sortedKeys(set) {
		if _, ok := final[k]; !ok {
			fin = append(fin, k)
		}
		final[k] = set[k]
	}
	flowLab := t.labelsFlow()
	const step = "  "

	switch {
	case v.Kind == yaml.MappingNode && isFlow(v): // name: {labels: {...}} or name: {}
		// Only this node's own braces are replaced, and the endpoints map is block style, so no sibling is touched.
		if len(v.Content) > 2 || len(v.Content) == 2 && v.Content[0].Value != "labels" {
			return nil, t.unsupported(v, fmt.Sprintf("endpoint %q has keys other than labels", name))
		}
		text := "{}"
		if len(fin) > 0 {
			text = "{labels: " + flowLabels(final, fin) + "}"
		}
		return []edit{{t.nodeOff(v), t.closeOff(v), text}}, nil

	case isEmptyNull(v): // name:
		if len(fin) == 0 {
			return nil, nil
		}
		return []edit{t.insertAfterLine(e.key.Line, labelLines(indentOf(e.key)+step, step, flowLab, final, fin))}, nil

	case v.Kind != yaml.MappingNode:
		return nil, t.unsupported(v, fmt.Sprintf("endpoint %q: its value is not a mapping", name))
	}

	// name: followed by an indented block mapping.
	if lk == nil {
		if len(fin) == 0 {
			return nil, nil
		}
		st := strings.Repeat(" ", blockStep(v))
		return []edit{t.insertAfterLine(t.endLine(v), labelLines(indentOf(v.Content[0]), st, flowLab, final, fin))}, nil
	}
	if len(fin) == 0 {
		end := t.tokenEnd(lk)
		if !isEmptyNull(lv) {
			end = t.nodeEnd(lv)
		}
		if !t.soleOnLines(t.nodeOff(lk), end) {
			return nil, t.unsupported(lk, fmt.Sprintf("endpoint %q: labels shares its line with other content", name))
		}
		from, to := t.lineSpan(lk.Line, t.lineOf(end-1))
		return []edit{{from, to, ""}}, nil
	}
	if lv.Kind == yaml.MappingNode && isFlow(lv) {
		return []edit{{t.nodeOff(lv), t.closeOff(lv), flowLabels(final, fin)}}, nil
	}

	// Block labels: edit pair by pair.
	var edits []edit
	for i := 0; i+1 < len(lv.Content); i += 2 {
		k, val := lv.Content[i], lv.Content[i+1]
		if drop[k.Value] {
			end := t.tokenEnd(k)
			if !isEmptyNull(val) {
				end = t.nodeEnd(val)
			}
			if !t.soleOnLines(t.nodeOff(k), end) {
				return nil, t.unsupported(k, fmt.Sprintf("endpoint %q: label %q shares its line with other content", name, k.Value))
			}
			from, to := t.lineSpan(k.Line, t.lineOf(end-1))
			edits = append(edits, edit{from, to, ""})
		} else if nv, ok := set[k.Value]; ok && nv != val.Value {
			if isEmptyNull(val) {
				return nil, t.unsupported(k, fmt.Sprintf("endpoint %q: label %q has an empty value", name, k.Value))
			}
			edits = append(edits, edit{t.nodeOff(val), t.tokenEnd(val), scalar(nv)})
		}
	}
	var add strings.Builder
	ind := indentOf(lk) + step
	if len(lv.Content) > 0 {
		ind = indentOf(lv.Content[0])
	}
	for _, k := range sortedKeys(set) {
		if _, ok := cur[k]; !ok {
			add.WriteString(ind + k + ": " + scalar(set[k]) + "\n")
		}
	}
	if add.Len() > 0 {
		after := lk.Line
		if len(lv.Content) > 0 {
			after = t.endLine(lv)
		}
		edits = append(edits, t.insertAfterLine(after, add.String()))
	}
	return edits, nil
}
