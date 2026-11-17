package namerule

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newID(t *testing.T, o, g, i string) sap.Address {
	t.Helper()
	a, err := sap.NewAddress(o, g, i)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

const okRule = `{"id":"r","match":{"owner":{"is":"x"}},"message":"m"}`

func TestLoadRefusals(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"empty file", ``, "not valid JSON"},
		{"not json", `{`, "not valid JSON"},
		{"not an object", `[]`, "not valid JSON"},
		{"trailing content", `{"version":1,"rules":[]} {}`, "trailing content"},
		{"trailing garbage", `{"version":1,"rules":[]} x`, "trailing content"},
		{"unknown top-level key", `{"version":1,"rules":[],"extra":1}`, `unknown key "extra"`},
		{"unknown rule key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"}},"message":"m","why":1}]}`, `unknown key "why"`},
		{"unknown match key", `{"version":1,"rules":[{"id":"r","match":{"zone":{"is":"x"}},"message":"m"}]}`, `unknown key "zone"`},
		{"unknown token key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x","like":"y"}},"message":"m"}]}`, `unknown key "like"`},
		{"old agent key refused", `{"version":1,"rules":[{"id":"r","match":{"agent":{"is":"x"}},"message":"m"}]}`, `the keys are "owner", "group" and "instance"`},
		{"old name key refused", `{"version":1,"rules":[{"id":"r","match":{"name":{"is":"x"}},"message":"m"}]}`, `the keys are "owner", "group" and "instance"`},
		{"old keys with new ones refused", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"},"name":{"is":"y"}},"message":"m"}]}`, `key "name" is no longer read`},
		{"version missing", `{"rules":[]}`, "version is missing"},
		{"version string", `{"version":"1","rules":[]}`, "version is not an integer"},
		{"version fraction", `{"version":1.5,"rules":[]}`, "version is not an integer"},
		{"version null", `{"version":null,"rules":[]}`, "version is not an integer"},
		{"version zero", `{"version":0,"rules":[]}`, "unsupported version 0"},
		{"version two", `{"version":2,"rules":[]}`, "unsupported version 2"},
		{"version two with another shape", `{"version":2,"newkey":true}`, "unsupported version 2"},
		{"rules missing", `{"version":1}`, "rules is missing"},
		{"rules null", `{"version":1,"rules":null}`, "rules is not an array"},
		{"rules object", `{"version":1,"rules":{}}`, "rules is not an array"},
		{"rule not an object", `{"version":1,"rules":[1]}`, "rule 1"},
		{"id missing", `{"version":1,"rules":[{"match":{"owner":{"is":"x"}},"message":"m"}]}`, "id is missing or empty"},
		{"id empty", `{"version":1,"rules":[{"id":"","match":{"owner":{"is":"x"}},"message":"m"}]}`, "id is missing or empty"},
		{"message missing", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"}}}]}`, "message is missing or empty"},
		{"message empty", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"}},"message":""}]}`, "message is missing or empty"},
		{"duplicate id", `{"version":1,"rules":[` + okRule + `,` + okRule + `]}`, `duplicate id "r"`},
		{"match missing", `{"version":1,"rules":[{"id":"r","message":"m"}]}`, "match is missing"},
		{"match empty", `{"version":1,"rules":[{"id":"r","match":{},"message":"m"}]}`, "match is empty"},
		{"match null", `{"version":1,"rules":[{"id":"r","match":null,"message":"m"}]}`, "match is missing"},
		{"empty token object", `{"version":1,"rules":[{"id":"r","match":{"owner":{}},"message":"m"}]}`, "owner has neither is nor is_not"},
		{"null token object", `{"version":1,"rules":[{"id":"r","match":{"group":null},"message":"m"}]}`, "group has neither is nor is_not"},
		{"is and is_not", `{"version":1,"rules":[{"id":"r","match":{"instance":{"is":"a","is_not":"b"}},"message":"m"}]}`, "instance has both is and is_not"},
		{"bad RE2", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"("}},"message":"m"}]}`, "not valid RE2"},
		{"bad RE2 is_not", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is_not":"a**"}},"message":"m"}]}`, "not valid RE2"},
		{"lookahead is not RE2", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"(?=a)"}},"message":"m"}]}`, "not valid RE2"},
		{"unbalanced that wrapping would repair", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"a)(b"}},"message":"m"}]}`, "not valid RE2"},
		{"duplicate top key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"}},"message":"m"}],"rules":[]}`, `duplicate key "rules"`},
		{"duplicate version", `{"version":1,"version":1,"rules":[]}`, `duplicate key "version"`},
		{"duplicate rule key", `{"version":1,"rules":[{"id":"r","id":"s","match":{"owner":{"is":"x"}},"message":"m"}]}`, `duplicate key "id"`},
		{"duplicate match key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x"},"owner":{"is":"y"}},"message":"m"}]}`, `duplicate key "owner"`},
		{"duplicate token key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":"x","is":"y"}},"message":"m"}]}`, `duplicate key "is"`},
		{"case variant top key", `{"version":1,"Rules":[]}`, `unknown key "Rules"`},
		{"case variant version key", `{"Version":1,"rules":[]}`, `unknown key "Version"`},
		{"case variant rule key", `{"version":1,"rules":[{"ID":"r","match":{"owner":{"is":"x"}},"message":"m"}]}`, `unknown key "ID"`},
		{"case variant match key", `{"version":1,"rules":[{"id":"r","Match":{"owner":{"is":"x"}},"message":"m"}]}`, `unknown key "Match"`},
		{"case variant match member", `{"version":1,"rules":[{"id":"r","match":{"Owner":{"is":"x"}},"message":"m"}]}`, `unknown key "Owner"`},
		{"case variant token key", `{"version":1,"rules":[{"id":"r","match":{"owner":{"IS":"x"}},"message":"m"}]}`, `unknown key "IS"`},
		{"case variant is_not", `{"version":1,"rules":[{"id":"r","match":{"owner":{"Is_Not":"x"}},"message":"m"}]}`, `unknown key "Is_Not"`},
		{"pattern not a string", `{"version":1,"rules":[{"id":"r","match":{"owner":{"is":1}},"message":"m"}]}`, "owner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, c.body)
			r, err := Load(p)
			if err == nil {
				t.Fatalf("Load accepted %s", c.body)
			}
			if r != nil {
				t.Errorf("a refused file returned a rule")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q lacks %q", err, c.want)
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not name the file %s", err, p)
			}
		})
	}
}

func TestLoadUnreadableAndEmptyPath(t *testing.T) {
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty path: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.json")
	if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("unreadable file: %v", err)
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Errorf("a directory was read as a rule file")
	}
}

func TestUnsupportedVersionIsNamed(t *testing.T) {
	p := write(t, `{"version":7,"rules":[]}`)
	_, err := Load(p)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error %v is not ErrUnsupportedVersion", err)
	}
	if ErrUnsupportedVersion.Error() != "unsupported version" {
		t.Errorf("sentinel text %q", ErrUnsupportedVersion.Error())
	}
	if !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "7") {
		t.Errorf("error %q must name the file and the version", err)
	}
}

func TestSupportedVersionsTable(t *testing.T) {
	if got := SupportedVersions(); len(got) != 1 || got[0] != 1 {
		t.Errorf("supported versions = %v, want [1]", got)
	}
}

func TestLoadAcceptsEmptyRules(t *testing.T) {
	r, err := Load(write(t, `{"version":1,"rules":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.Count() != 0 {
		t.Errorf("count %d", r.Count())
	}
	if err := r.Check(newID(t, "acme", "dev", "peer")); err != nil {
		t.Errorf("an empty rule set refused: %v", err)
	}
}

func loadRule(t *testing.T, body string) *Rule {
	t.Helper()
	r, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCheck(t *testing.T) {
	noMash := `{"id":"no-mash","match":{"owner":{"is":"mash"}},"message":"the owner mash is reserved"}`
	window := `{"id":"group-ci","match":{"owner":{"is_not":"cc"},"group":{"is":"ci"}},"message":"the group ci is reserved"}`
	body := func(rs ...string) string {
		return `{"version":1,"rules":[` + strings.Join(rs, ",") + `]}`
	}
	addr := func(o, g, i string) func(*testing.T) sap.Address {
		return func(t *testing.T) sap.Address { return newID(t, o, g, i) }
	}
	cases := []struct {
		name    string
		rules   []string
		id      func(*testing.T) sap.Address
		refused string // rule id, "" = accepted
		message string
	}{
		{"owner token refused", []string{noMash}, addr("mash", "acme", "peer"), "no-mash", "the owner mash is reserved"},
		{"owner token accepted when other", []string{noMash}, addr("mashx", "acme", "peer"), "", ""},
		{"owner rule does not read the group", []string{noMash}, addr("acme", "mash", "peer"), "", ""},
		{"owner rule does not read the instance", []string{noMash}, addr("acme", "peer", "mash"), "", ""},
		{"group key", []string{window}, addr("acme", "ci", "peer"), "group-ci", "the group ci is reserved"},
		{"group key accepts another group", []string{window}, addr("acme", "dev", "peer"), "", ""},
		{"is_not on the owner slot", []string{window}, addr("cc", "ci", "peer"), "", ""},
		{"instance key", []string{`{"id":"i","match":{"instance":{"is":"peer"}},"message":"m"}`}, addr("acme", "dev", "peer"), "i", "m"},
		{"instance key does not read the group", []string{`{"id":"i","match":{"instance":{"is":"dev"}},"message":"m"}`}, addr("acme", "dev", "peer"), "", ""},
		{"all keys must hold: one fails", []string{`{"id":"both","match":{"owner":{"is":"claude"},"group":{"is":"acme"}},"message":"m"}`}, addr("claude", "bogs", "peer"), "", ""},
		{"all keys must hold: all hold", []string{`{"id":"both","match":{"owner":{"is":"claude"},"group":{"is":"acme"},"instance":{"is":"pe.*"}},"message":"m"}`}, addr("claude", "acme", "peer"), "both", "m"},
		{"whole token: a|b does not match ab", []string{`{"id":"ab","match":{"instance":{"is":"a|b"}},"message":"m"}`}, addr("claude", "acme", "ab"), "", ""},
		{"whole token: a|b matches a", []string{`{"id":"ab","match":{"instance":{"is":"a|b"}},"message":"m"}`}, addr("claude", "acme", "a"), "ab", "m"},
		{"whole token: a|b matches b", []string{`{"id":"ab","match":{"instance":{"is":"a|b"}},"message":"m"}`}, addr("claude", "acme", "b"), "ab", "m"},
		{"whole token: prefix does not match", []string{`{"id":"p","match":{"instance":{"is":"peer"}},"message":"m"}`}, addr("claude", "acme", "peer2"), "", ""},
		{"whole token: suffix does not match", []string{`{"id":"p","match":{"instance":{"is":"eer"}},"message":"m"}`}, addr("claude", "acme", "peer"), "", ""},
		{"is_not with alternation: member holds nothing", []string{`{"id":"only-ab","match":{"instance":{"is_not":"a|b"}},"message":"m"}`}, addr("claude", "acme", "a"), "", ""},
		{"is_not with alternation: non-member is refused", []string{`{"id":"only-ab","match":{"instance":{"is_not":"a|b"}},"message":"m"}`}, addr("claude", "acme", "ab"), "only-ab", "m"},
		{"is_not with alternation: other is refused", []string{`{"id":"only-ab","match":{"instance":{"is_not":"a|b"}},"message":"m"}`}, addr("claude", "acme", "c"), "only-ab", "m"},
		{"first matching rule is reported", []string{noMash, `{"id":"second","match":{"group":{"is":"acme"}},"message":"two"}`}, addr("mash", "acme", "peer"), "no-mash", "the owner mash is reserved"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := loadRule(t, body(c.rules...))
			err := r.Check(c.id(t))
			if c.refused == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var ref *Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("error %v is not a *Refusal", err)
			}
			if ref.RuleID != c.refused || ref.Message != c.message {
				t.Errorf("refusal = %+v, want rule %q message %q", ref, c.refused, c.message)
			}
			if !strings.Contains(err.Error(), `"`+c.refused+`"`) || !strings.Contains(err.Error(), c.message) {
				t.Errorf("error text %q must name the rule and carry its message", err)
			}
		})
	}
}

func TestNilRuleAcceptsEverything(t *testing.T) {
	var r *Rule
	if err := r.Check(newID(t, "acme", "dev", "peer")); err != nil {
		t.Errorf("nil rule refused: %v", err)
	}
	if r.Count() != 0 || r.Digest() != "" {
		t.Errorf("nil rule: count %d digest %q", r.Count(), r.Digest())
	}
	if got := r.StatusLine(""); got != "name rule: none set" {
		t.Errorf("status = %q", got)
	}
}

func TestDigestCountStatusLine(t *testing.T) {
	body := `{"version":1,"rules":[` + okRule + `]}`
	p := write(t, body)
	r, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// sha256 of the bytes, first 12 hex.
	want := sha12(body)
	if r.Digest() != want || len(r.Digest()) != 12 {
		t.Errorf("digest %q, want %q", r.Digest(), want)
	}
	if r.Count() != 1 {
		t.Errorf("count %d", r.Count())
	}
	if got, want := r.StatusLine(p), "name rule: "+p+" sha256:"+want+" (1 rules)"; got != want {
		t.Errorf("status = %q, want %q", got, want)
	}
}

func sha12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
