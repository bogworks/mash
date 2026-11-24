// Command gate1demo renders the planned (next-release) output changes from the
// specification's own x-output-next cases, spec/next.json items and rules_next,
// as Markdown. See docs/spec-next.md.
//
//	go run ./scripts/gate1demo [-spec dir] [-out file]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	flagWidth = 69
	maxWidth  = 80
)

type outCase struct {
	Case    string `json:"case"`
	Text    string `json:"text"`
	Stream  string `json:"stream"`
	Exit    int    `json:"exit"`
	Issue   string `json:"issue"`
	Replace string `json:"replaces"`
	Note    string `json:"note"`
}

type meta struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type command struct {
	Name     string    `json:"name"`
	Metadata []meta    `json:"metadata"`
	Commands []command `json:"commands"`
}

type toolSpec struct {
	Command *command `json:"command"`
}

type nextItem struct {
	ID    string `json:"id"`
	Where string `json:"where"`
	outCase
}

func (n *nextItem) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID    string `json:"id"`
		Where string `json:"where"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	n.ID, n.Where = raw.ID, raw.Where
	return json.Unmarshal(b, &n.outCase)
}

type ruleNext struct {
	Rule     string `json:"rule"`
	Detail   string `json:"detail"`
	Replaces string `json:"replaces"`
}

type nextFile struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Items     []nextItem `json:"items"`
	RulesNext []ruleNext `json:"rules_next"`
}

type rulesFile struct {
	Rules []struct {
		Rule   string `json:"rule"`
		Detail string `json:"detail"`
	} `json:"rules"`
}

var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`S[UA][A-Z2-7]{40,}`),
	regexp.MustCompile(`-----BEGIN`),
	regexp.MustCompile(`(?i)(nkey|seed)\W{0,4}[A-Za-z0-9+/_=-]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`MASH_SETUP_TOKEN=`),
}

func checkSecrets(where, s string) error {
	for i, line := range strings.Split(s, "\n") {
		for _, re := range secretRes {
			if re.MatchString(line) {
				return fmt.Errorf("%s: line %d matches secrets pattern %q; refusing to render", where, i+1, re.String())
			}
		}
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func cases(c *command, name string) ([]outCase, error) {
	for _, m := range c.Metadata {
		if m.Name == name {
			var out []outCase
			if err := json.Unmarshal(m.Value, &out); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", c.Name, name, err)
			}
			return out, nil
		}
	}
	return nil, nil
}

type renderer struct {
	rel    string
	b      strings.Builder
	errs   []string
	nflag  int
	nitems int
}

func fence(b *strings.Builder, text string) {
	b.WriteString("```\n" + strings.TrimRight(text, "\n") + "\n```\n")
}

func streamLine(c outCase) string {
	s := c.Stream
	if s == "" {
		s = "stdout"
	}
	return fmt.Sprintf("stream: %s, exit: %d", s, c.Exit)
}

// description reports whether the case text is wholly in round brackets: the
// rules reserve those for a description, which is not output.
func description(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if len(t) >= 2 && t[0] == '(' && t[len(t)-1] == ')' {
		return t[1 : len(t)-1], true
	}
	return "", false
}

// body writes a case's text and its stream line, or, for a description, a
// plain line with no code block and no stream.
func body(b *strings.Builder, c outCase) {
	if d, ok := description(c.Text); ok {
		b.WriteString("Description (not output): " + d + "\n")
		return
	}
	fence(b, c.Text)
	b.WriteString(streamLine(c) + "\n")
}

func (r *renderer) widths(where, text string) {
	max := 0
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		w := utf8.RuneCountInString(line)
		if w > maxWidth {
			r.errs = append(r.errs, fmt.Sprintf("%s: line %d is %d columns (limit %d)", where, i+1, w, maxWidth))
		}
		if w > max {
			max = w
		}
	}
	if max > flagWidth {
		fmt.Fprintf(&r.b, "width: %d columns (over %d, flagged)\n", max, flagWidth)
		r.nflag++
	}
}

func (r *renderer) entry(where, issue string, c outCase, today *outCase, todayStream bool) {
	r.nitems++
	h := "## " + where
	if issue != "" {
		h += " (" + issue + ")"
	}
	r.b.WriteString("\n" + h + "\n\n")
	if c.Case != "" {
		r.b.WriteString("Case: " + c.Case + "\n\n")
	}
	if today != nil {
		r.b.WriteString("Today:\n\n")
		fence(&r.b, today.Text)
		if todayStream {
			r.b.WriteString(streamLine(*today) + "\n")
		}
		r.b.WriteString("\n")
	}
	r.b.WriteString(r.rel + ":\n\n")
	body(&r.b, c)
	r.widths(where, c.Text)
	if c.Note != "" {
		r.b.WriteString("\nNote: " + c.Note + "\n")
	}
}

func lookup(cs []outCase, name string) *outCase {
	for i := range cs {
		if cs[i].Case == name {
			return &cs[i]
		}
	}
	return nil
}

func (r *renderer) walk(c *command, path []string) error {
	path = append(append([]string{}, path...), c.Name)
	full := strings.Join(path, " ")
	next, err := cases(c, "x-output-next")
	if err != nil {
		return err
	}
	if len(next) > 0 {
		cur, err := cases(c, "x-output")
		if err != nil {
			return err
		}
		for _, n := range next {
			var t *outCase
			if n.Replace != "" {
				if t = lookup(cur, n.Replace); t == nil {
					return fmt.Errorf("%s: case %q replaces %q, which is not a case in x-output", full, n.Case, n.Replace)
				}
			}
			r.entry(full+" / "+n.Case, n.Issue, n, t, true)
		}
	}
	for i := range c.Commands {
		if err := r.walk(&c.Commands[i], path); err != nil {
			return err
		}
	}
	return nil
}

var specVersion = regexp.MustCompile(`^(\d+\.\d+\.\d+)-spec\.\d+$`)

// release returns the release a spec version such as "0.5.9-spec.0" plans.
func release(v string) (string, error) {
	m := specVersion.FindStringSubmatch(v)
	if m == nil {
		return "", fmt.Errorf("info.version %q must look like 0.5.9-spec.0", v)
	}
	return m[1], nil
}

// render builds the Markdown for the spec in dir. It returns an error, and no
// output, when a secrets pattern matches or a line exceeds the hard width.
func render(dir string) (string, error) {
	var nf nextFile
	if err := readJSON(filepath.Join(dir, "next.json"), &nf); err != nil {
		return "", err
	}
	rel, err := release(nf.Info.Version)
	if err != nil {
		return "", fmt.Errorf("next.json: %w", err)
	}
	r := &renderer{rel: rel}
	r.b.WriteString("# Gate 1 demo: planned " + rel + " output\n\nGenerated from the specification (x-output-next, spec/next.json). Not yet implemented.\n")
	for _, f := range []string{"mash.json", "mashctl.json"} {
		var t toolSpec
		if err := readJSON(filepath.Join(dir, f), &t); err != nil {
			return "", err
		}
		if t.Command == nil {
			return "", fmt.Errorf("%s: no command", f)
		}
		if err := r.walk(t.Command, nil); err != nil {
			return "", err
		}
	}
	for _, it := range nf.Items {
		where := it.ID
		if it.Where != "" {
			where += " [" + it.Where + "]"
		}
		var t *outCase
		if it.Replace != "" {
			t = &outCase{Text: it.Replace}
		}
		r.entry(where, it.Issue, it.outCase, t, false)
	}
	var rf rulesFile
	if len(nf.RulesNext) > 0 {
		if err := readJSON(filepath.Join(dir, "rules.json"), &rf); err != nil {
			return "", err
		}
	}
	for _, rn := range nf.RulesNext {
		r.nitems++
		r.b.WriteString("\n## rule: " + rn.Rule + "\n\n")
		if rn.Replaces != "" {
			found := false
			for _, o := range rf.Rules {
				if o.Rule == rn.Replaces {
					r.b.WriteString("Today: " + o.Rule + "\n\n")
					fence(&r.b, o.Detail)
					r.b.WriteString("\n")
					found = true
				}
			}
			if !found {
				return "", fmt.Errorf("rule %q replaces %q, which is not a rule in rules.json", rn.Rule, rn.Replaces)
			}
		}
		r.b.WriteString(rel + ":\n\n")
		fence(&r.b, rn.Detail)
	}
	out := r.b.String()
	if err := checkSecrets("spec", out); err != nil {
		return "", err
	}
	if len(r.errs) > 0 {
		return "", fmt.Errorf("%s", strings.Join(r.errs, "\n"))
	}
	return out, nil
}

func main() {
	spec := flag.String("spec", "spec", "directory holding mash.json, mashctl.json, next.json, rules.json")
	out := flag.String("out", "", "write the Markdown to this file instead of stdout")
	flag.Parse()
	md, err := render(*spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gate1demo:", err)
		os.Exit(1)
	}
	if *out == "" {
		fmt.Print(md)
		return
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gate1demo:", err)
		os.Exit(1)
	}
}
