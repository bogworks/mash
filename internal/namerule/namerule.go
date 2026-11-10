// Package namerule reads and applies mash's name-acceptance rule file, name-rule@1 (docs/name-rule.md, ADR 0077).
//
// A rule can only refuse an identity. mash gives no token a meaning of its own: the file the deployment supplies
// decides what is refused. The package imports only sap, so config can load a rule without an import cycle.
package namerule

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/sap"
)

// ErrUnsupportedVersion is wrapped by the error Load returns for a version this mash does not read. The error names
// the file and the version.
var ErrUnsupportedVersion = errors.New("unsupported version")

// reader parses the top-level keys of one version of the file (version included) into rules.
type reader func(top map[string]json.RawMessage) ([]rule, error)

// readers is the table of versions this mash reads. docs/name-rule.md: mash reads version n and n-1 for one
// compatibility window, then n-1 refuses start; today only version 1 exists, so only 1 is here. To add version 2, add
// its reader here (keeping version 1's for the window) and drop version 1's when the window ends.
var readers = map[int]reader{1: readV1}

// SupportedVersions lists the versions Load reads, in ascending order.
func SupportedVersions() []int {
	var vs []int
	for v := range readers {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

type matcher struct {
	key    string // agent, owner or name
	re     *regexp.Regexp
	negate bool // is_not
}

type rule struct {
	id, message string
	match       []matcher
}

// Rule is a loaded rule file. A nil *Rule is no rule: it accepts everything.
type Rule struct {
	rules  []rule
	digest string
}

// Refusal is the error Check returns: the rule that matched and its message.
type Refusal struct {
	RuleID  string
	Message string
	// Agent, Owner and Name are the tokens of the identity that was refused.
	Agent, Owner, Name string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("name rule %q refuses %s.%s.%s: %s", r.RuleID, r.Agent, r.Owner, r.Name, r.Message)
}

// Load reads the rule file at path. Any condition under "What refuses start" in docs/name-rule.md is an error that
// names the file.
func Load(path string) (*Rule, error) {
	fail := func(format string, a ...any) (*Rule, error) {
		return nil, fmt.Errorf("mash: name rule file %s: %s", path, fmt.Sprintf(format, a...))
	}
	if path == "" {
		return nil, errors.New("mash: name rule file path is empty: name a file, or leave the setting unset for no rule")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail("cannot be read: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil || top == nil {
		if err == nil {
			err = errors.New("not a JSON object")
		}
		return fail("not valid JSON: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fail("trailing content after the JSON value")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return fail("%v", err)
	}
	vraw, ok := top["version"]
	if !ok {
		for k := range top {
			if k != "rules" {
				return fail("unknown key %q at the top level", k)
			}
		}
		return fail("version is missing")
	}
	version, err := strconv.Atoi(string(bytes.TrimSpace(vraw))) // refuses 1.0, 1e0, "1" and null
	if err != nil {
		return fail("version is not an integer")
	}
	rd, ok := readers[version]
	if !ok {
		return nil, fmt.Errorf("mash: name rule file %s: %w %d (this mash reads %v)", path, ErrUnsupportedVersion, version, SupportedVersions())
	}
	rules, err := rd(top)
	if err != nil {
		return fail("%v", err)
	}
	sum := sha256.Sum256(raw)
	return &Rule{rules: rules, digest: hex.EncodeToString(sum[:])[:12]}, nil
}

// rejectDuplicateKeys walks the raw bytes token by token and refuses a key that repeats within one object, at every
// level: encoding/json would let the later one win silently.
func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		d, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch d {
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				k := kt.(string)
				if seen[k] {
					return fmt.Errorf("duplicate key %q", k)
				}
				seen[k] = true
				if err := walk(); err != nil {
					return err
				}
			}
		}
		_, err = dec.Token() // the closing delimiter
		return err
	}
	if err := walk(); err != nil {
		if strings.HasPrefix(err.Error(), "duplicate key") {
			return err
		}
		return fmt.Errorf("not valid JSON: %v", err)
	}
	return nil
}

// strictObject decodes the object in raw into its keys and refuses any key whose exact spelling is not in allowed:
// key names are case-sensitive, which encoding/json's struct matching is not.
func strictObject(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k := range m {
		if !contains(allowed, k) {
			return nil, fmt.Errorf("unknown key %q", k)
		}
	}
	return m, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

func readV1(top map[string]json.RawMessage) ([]rule, error) {
	for k := range top {
		if k != "version" && k != "rules" {
			return nil, fmt.Errorf("unknown key %q at the top level", k)
		}
	}
	rraw, ok := top["rules"]
	if !ok {
		return nil, errors.New("rules is missing")
	}
	if t := bytes.TrimSpace(rraw); len(t) == 0 || t[0] != '[' {
		return nil, errors.New("rules is not an array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rraw, &items); err != nil {
		return nil, fmt.Errorf("rules is not an array: %v", err)
	}
	var out []rule
	seen := map[string]bool{}
	for i, item := range items {
		n := i + 1
		if !isObject(item) {
			return nil, fmt.Errorf("rule %d is not an object", n)
		}
		f, err := strictObject(item, "id", "match", "message")
		if err != nil {
			return nil, fmt.Errorf("rule %d: %v", n, err)
		}
		idp, err := optString(f["id"])
		if err != nil {
			return nil, fmt.Errorf("rule %d: id is not a string", n)
		}
		if idp == nil || *idp == "" {
			return nil, fmt.Errorf("rule %d: id is missing or empty", n)
		}
		id := *idp
		if seen[id] {
			return nil, fmt.Errorf("rule %d: duplicate id %q", n, id)
		}
		seen[id] = true
		msg, err := optString(f["message"])
		if err != nil {
			return nil, fmt.Errorf("rule %q: message is not a string", id)
		}
		if msg == nil || *msg == "" {
			return nil, fmt.Errorf("rule %q: message is missing or empty", id)
		}
		ms, err := readMatch(id, f["match"])
		if err != nil {
			return nil, err
		}
		out = append(out, rule{id: id, message: *msg, match: ms})
	}
	return out, nil
}

// optString reads a JSON string; absent or null is nil.
func optString(raw json.RawMessage) (*string, error) {
	if isNull(raw) {
		return nil, nil
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func readMatch(id string, raw json.RawMessage) ([]matcher, error) {
	if isNull(raw) {
		return nil, fmt.Errorf("rule %q: match is missing", id)
	}
	if !isObject(raw) {
		return nil, fmt.Errorf("rule %q: match is not an object", id)
	}
	m, err := strictObject(raw, "agent", "owner", "name")
	if err != nil {
		return nil, fmt.Errorf("rule %q: match: %v", id, err)
	}
	var out []matcher
	for _, k := range []struct {
		key string
		raw json.RawMessage
	}{{"agent", m["agent"]}, {"owner", m["owner"]}, {"name", m["name"]}} {
		if k.raw == nil {
			continue
		}
		var tok struct{ Is, IsNot *string }
		if !isNull(k.raw) {
			if !isObject(k.raw) {
				return nil, fmt.Errorf("rule %q: match %s is not an object", id, k.key)
			}
			t, err := strictObject(k.raw, "is", "is_not")
			if err != nil {
				return nil, fmt.Errorf("rule %q: match %s: %v", id, k.key, err)
			}
			if tok.Is, err = optString(t["is"]); err != nil {
				return nil, fmt.Errorf("rule %q: match %s: is is not a string", id, k.key)
			}
			if tok.IsNot, err = optString(t["is_not"]); err != nil {
				return nil, fmt.Errorf("rule %q: match %s: is_not is not a string", id, k.key)
			}
		}
		switch {
		case tok.Is != nil && tok.IsNot != nil:
			return nil, fmt.Errorf("rule %q: match %s has both is and is_not", id, k.key)
		case tok.Is == nil && tok.IsNot == nil:
			return nil, fmt.Errorf("rule %q: match %s has neither is nor is_not", id, k.key)
		}
		pat, negate := "", false
		if tok.Is != nil {
			pat = *tok.Is
		} else {
			pat, negate = *tok.IsNot, true
		}
		// The pattern alone must be RE2: the wrapping could otherwise repair an unbalanced one such as a)(b.
		if _, err := regexp.Compile(pat); err != nil {
			return nil, fmt.Errorf("rule %q: match %s: pattern %q is not valid RE2: %v", id, k.key, pat, err)
		}
		re, err := regexp.Compile(`^(?:` + pat + `)$`)
		if err != nil {
			return nil, fmt.Errorf("rule %q: match %s: pattern %q is not valid RE2: %v", id, k.key, pat, err)
		}
		out = append(out, matcher{key: k.key, re: re, negate: negate})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rule %q: match is empty", id)
	}
	return out, nil
}

// Check returns a *Refusal naming the first rule that matches id, or nil when none does. A nil Rule accepts everything.
func (r *Rule) Check(id sap.EndpointIdentity) error {
	if r == nil {
		return nil
	}
	for _, ru := range r.rules {
		if ru.matches(id) {
			return &Refusal{RuleID: ru.id, Message: ru.message, Agent: id.Agent, Owner: id.Owner, Name: id.Name}
		}
	}
	return nil
}

func (ru rule) matches(id sap.EndpointIdentity) bool {
	for _, m := range ru.match {
		tok := id.Name
		switch m.key {
		case "agent":
			tok = id.Agent
		case "owner":
			tok = id.Owner
		}
		if m.re.MatchString(tok) == m.negate {
			return false
		}
	}
	return true
}

// Count is the number of rules; zero for a nil Rule.
func (r *Rule) Count() int {
	if r == nil {
		return 0
	}
	return len(r.rules)
}

// Digest is the first 12 hex digits of the SHA-256 of the file's bytes; "" for a nil Rule.
func (r *Rule) Digest() string {
	if r == nil {
		return ""
	}
	return r.digest
}

// StatusLine is the line `mash status` shows; path is the path as configured.
func (r *Rule) StatusLine(path string) string {
	if r == nil {
		return "name rule: none set"
	}
	return fmt.Sprintf("name rule: %s sha256:%s (%d rules)", path, r.digest, len(r.rules))
}
