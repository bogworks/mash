package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Parse reads a policy file strictly: unknown fields, duplicate mapping keys at any depth, more than one document
// and a document that is not a mapping are errors, and errors carry line numbers where the source has them. It then
// validates the file (see Check) and returns an error naming every Check error; warnings do not fail a parse. An
// absent default becomes "allow".
func Parse(data []byte) (*File, error) {
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("policy: empty file: expected a mapping with default, endpoints, rules and tests")
		}
		return nil, fmt.Errorf("policy: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("policy: line %d: more than one YAML document", extra.Line)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("policy: %w", err)
	}
	top := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		top = root.Content[0]
	}
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("policy: line %d: the file must be a mapping with default, endpoints, rules and tests", top.Line)
	}
	if err := dupKeys(top); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}

	var f File
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&f); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	if f.Default == "" {
		f.Default = Allow
	}
	if f.SubjectLabel == "" {
		f.SubjectLabel = DefaultSubjectLabel
	}
	f.lines = collectLines(top)
	if err := f.checked(); err != nil {
		return nil, err
	}
	return &f, nil
}

// dupKeys walks the node tree and rejects a mapping that repeats a key.
func dupKeys(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind == yaml.ScalarNode {
				if first, dup := seen[k.Value]; dup {
					return fmt.Errorf("line %d: duplicate key %q (first at line %d)", k.Line, k.Value, first)
				}
				seen[k.Value] = k.Line
			}
			if err := dupKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range n.Content {
		if err := dupKeys(c); err != nil {
			return err
		}
	}
	return nil
}

func collectLines(top *yaml.Node) fileLines {
	l := fileLines{endpoints: map[string]int{}}
	for i := 0; i+1 < len(top.Content); i += 2 {
		v := top.Content[i+1]
		switch top.Content[i].Value {
		case "rules":
			for _, c := range v.Content {
				l.rules = append(l.rules, c.Line)
			}
		case "tests":
			for _, c := range v.Content {
				l.tests = append(l.tests, c.Line)
			}
		case "endpoints":
			for j := 0; j+1 < len(v.Content); j += 2 {
				l.endpoints[v.Content[j].Value] = v.Content[j].Line
			}
		}
	}
	return l
}
