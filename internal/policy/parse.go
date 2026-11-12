package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

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
	if err := checkKeys(top); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}

	if err := refuseZones(top); err != nil {
		return nil, err
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
	f.lines = collectLines(top)
	if err := f.checked(); err != nil {
		return nil, err
	}
	return &f, nil
}

// keyKinds names what a non-string scalar key reads as, by its resolved YAML tag.
var keyKinds = map[string]string{
	"!!null": "null", "!!int": "int", "!!float": "float", "!!bool": "bool",
	"!!timestamp": "timestamp", "!!merge": "merge",
}

// plainStringKey reports an error unless the key node is a scalar that reads as a string. A quoted scalar always
// does, so a name such as "null" stays available.
func plainStringKey(k *yaml.Node) error {
	var text, kind string
	switch k.Kind {
	case yaml.ScalarNode:
		tag := k.ShortTag()
		if tag == "!!str" {
			return nil
		}
		text, kind = k.Value, keyKinds[tag]
		if kind == "" {
			kind = strings.TrimPrefix(tag, "!!")
		}
	case yaml.MappingNode:
		text, kind = "a mapping", "mapping"
	case yaml.SequenceNode:
		text, kind = "a sequence", "sequence"
	default:
		text, kind = "an alias", "alias"
	}
	return fmt.Errorf("line %d: map key %s is not a plain string (it reads as %s); quote it, for example \"null\"", k.Line, text, kind)
}

// checkKeys walks the node tree and rejects a mapping key that is not a plain string and a mapping that repeats a key.
func checkKeys(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if err := plainStringKey(k); err != nil {
				return err
			}
			if k.Kind == yaml.ScalarNode {
				if first, dup := seen[k.Value]; dup {
					return fmt.Errorf("line %d: duplicate key %q (first at line %d)", k.Line, k.Value, first)
				}
				seen[k.Value] = k.Line
			}
			if err := checkKeys(n.Content[i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range n.Content {
		if n.Kind == yaml.SequenceNode && c.Kind == yaml.ScalarNode && c.ShortTag() == "!!null" {
			line := c.Line
			if line <= 0 {
				line = n.Line
			}
			return fmt.Errorf("line %d: a list item is null; remove it or give it a value", line)
		}
		if err := checkKeys(c); err != nil {
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

// ZoneRemoved is the refusal of a policy file that still carries a zone. Zones are gone (ADR 0091), so a file that
// names one is refused rather than read as if the zone were a plain label that no longer does anything.
const ZoneRemoved = "zones were removed (ADR 0091): the address is the fence, and the server enforces no zone"

// refuseZones refuses a file that carries a zone: the top-level subject_label (it named the label whose value was the
// zone), a zone or zones section or field at the top or on an endpoint, and a label or selector key `zone` (the
// default zone label). Every occurrence is named with its line, and the message says what to remove.
func refuseZones(top *yaml.Node) error {
	var found []string
	add := func(line int, what string) { found = append(found, fmt.Sprintf("line %d: %s", line, what)) }
	zoneKey := func(k string) bool { return k == "zone" || k == "zones" }
	labelKeys := func(m *yaml.Node, where string) {
		if m == nil || m.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == "zone" {
				add(m.Content[i].Line, where+" has a label named zone: remove it (it was the default zone label)")
			}
		}
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		switch {
		case k.Value == "subject_label":
			add(k.Line, "subject_label names the label whose value was the zone: remove it")
		case zoneKey(k.Value):
			add(k.Line, k.Value+" is a zone section: remove it")
		case k.Value == "endpoints" && v.Kind == yaml.MappingNode:
			for j := 0; j+1 < len(v.Content); j += 2 {
				ep := v.Content[j+1]
				if ep.Kind != yaml.MappingNode {
					continue
				}
				for l := 0; l+1 < len(ep.Content); l += 2 {
					ek := ep.Content[l]
					switch {
					case zoneKey(ek.Value):
						add(ek.Line, "endpoint "+v.Content[j].Value+" has a "+ek.Value+" field: remove it")
					case ek.Value == "labels":
						labelKeys(ep.Content[l+1], "endpoint "+v.Content[j].Value)
					}
				}
			}
		case k.Value == "rules" && v.Kind == yaml.SequenceNode:
			for _, r := range v.Content {
				if r.Kind != yaml.MappingNode {
					continue
				}
				for l := 0; l+1 < len(r.Content); l += 2 {
					switch r.Content[l].Value {
					case "from", "to":
						labelKeys(r.Content[l+1], "a rule's "+r.Content[l].Value+" selector")
					}
				}
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return fmt.Errorf("policy: %s; remove what names a zone from the file (%s)", ZoneRemoved, strings.Join(found, "; "))
}
