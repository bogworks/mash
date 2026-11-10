package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

const goodRuleFile = `{"version":1,"rules":[{"id":"no-cc","match":{"agent":{"is":"cc"}},"message":"the agent token cc is retired"}]}`

func ruleFileIn(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rule.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadWith(t *testing.T, fileBody string, env map[string]string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	if fileBody != "" {
		writeFile(t, home, fileBody)
	}
	return Load(lookupOf(env), home)
}

func TestNameRuleUnsetIsNoRule(t *testing.T) {
	c, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if c.NameRule != nil || c.NameRulePath != "" {
		t.Fatalf("a rule from nothing: %v %q", c.NameRule, c.NameRulePath)
	}
	if got := c.NameRule.StatusLine(c.NameRulePath); got != "name rule: none set" {
		t.Fatalf("status %q", got)
	}
}

func TestNameRuleFromEnvAndFile(t *testing.T) {
	p := ruleFileIn(t, goodRuleFile)
	c, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_NAME_RULE_FILE": p})
	if err != nil {
		t.Fatal(err)
	}
	if c.NameRule == nil || c.NameRulePath != p || c.NameRule.Count() != 1 {
		t.Fatalf("env: %v %q", c.NameRule, c.NameRulePath)
	}
	legacy, _ := sap.Legacy("o", "a")
	if c.NameRule.Check(legacy) == nil {
		t.Fatal("the loaded rule does not refuse cc")
	}

	// From the file key, with no name set (mashctl has none): still loaded.
	c, err = loadWith(t, idFile+`,"name_rule_file":"`+p+`"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.NameRule == nil || c.NameRulePath != p {
		t.Fatalf("file key: %v %q", c.NameRule, c.NameRulePath)
	}

	// The environment wins over the file key: a bad file key under a good env value loads.
	c, err = loadWith(t, idFile+`,"name_rule_file":"/nonexistent/rule.json"}`, map[string]string{"MASH_NAME_RULE_FILE": p})
	if err != nil {
		t.Fatal(err)
	}
	if c.NameRulePath != p {
		t.Fatalf("env did not win: %q", c.NameRulePath)
	}
}

func TestNameRuleFileKeyRelativeToConfigDir(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, idFile+`,"name_rule_file":"rule.json"}`)
	dir := filepath.Join(home, ".config", "mash")
	if err := os.WriteFile(filepath.Join(dir, "rule.json"), []byte(goodRuleFile), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(lookupOf(nil), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.NameRulePath != filepath.Join(dir, "rule.json") || c.NameRule == nil {
		t.Fatalf("%q %v", c.NameRulePath, c.NameRule)
	}
}

func TestNameRuleSetButEmptyIsInvalid(t *testing.T) {
	// A variable that is set but empty is invalid, not absent: Load sees it through a real Lookup.
	_, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_NAME_RULE_FILE": ""})
	if err == nil || !strings.Contains(err.Error(), "MASH_NAME_RULE_FILE") || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("env empty: %v", err)
	}
	// Also with no name set.
	if _, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME_RULE_FILE": ""}); err == nil {
		t.Fatal("env empty, inert, accepted")
	}
	for _, v := range []string{`""`, `null`, `5`, `true`, `["x"]`} {
		_, err := loadWith(t, idFile+`,"name_rule_file":`+v+`}`, nil)
		if err == nil || !strings.Contains(err.Error(), "name_rule_file") {
			t.Errorf("file key %s: %v", v, err)
		}
	}
	// An env value overrides an empty file key, as for every setting.
	p := ruleFileIn(t, goodRuleFile)
	if _, err := loadWith(t, idFile+`,"name_rule_file":""}`, map[string]string{"MASH_NAME_RULE_FILE": p}); err != nil {
		t.Fatalf("env over empty key: %v", err)
	}
}

func TestNameRuleBadFileRefusesLoadNamingTheFile(t *testing.T) {
	p := ruleFileIn(t, `{"version":1,"rules":[],"extra":1}`)
	for _, name := range []string{"a", ""} {
		env := map[string]string{"MASH_NAME_RULE_FILE": p}
		if name != "" {
			env["MASH_NAME"] = name
		}
		_, err := loadWith(t, idFile+`}`, env)
		if err == nil || !strings.Contains(err.Error(), p) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	if _, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME_RULE_FILE": filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Fatal("an unreadable file loaded")
	}
}

func TestNameRuleRequired(t *testing.T) {
	p := ruleFileIn(t, goodRuleFile)
	cases := []struct {
		name    string
		file    string
		env     map[string]string
		wantErr string
	}{
		{"required, no file", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "true"}, "name rule"},
		{"required in file key, no file", idFile + `,"name_rule_required":true}`, nil, "name rule"},
		{"required, file loaded", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "true", "MASH_NAME_RULE_FILE": p}, ""},
		{"false needs no file", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "false"}, ""},
		{"env false wins over file true", idFile + `,"name_rule_required":true}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "false"}, ""},
		{"env true wins over file false", idFile + `,"name_rule_required":false}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "true"}, "name rule"},
		{"env 1 is invalid", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "1"}, "MASH_NAME_RULE_REQUIRED"},
		{"env yes is invalid", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "yes"}, "MASH_NAME_RULE_REQUIRED"},
		{"env TRUE is invalid", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "TRUE"}, "MASH_NAME_RULE_REQUIRED"},
		{"env empty is invalid", idFile + `}`, map[string]string{"MASH_NAME_RULE_REQUIRED": ""}, "MASH_NAME_RULE_REQUIRED"},
		{"file string is invalid", idFile + `,"name_rule_required":"true"}`, nil, "name_rule_required"},
		{"file null is invalid", idFile + `,"name_rule_required":null}`, nil, "name_rule_required"},
		{"file number is invalid", idFile + `,"name_rule_required":1}`, nil, "name_rule_required"},
		{"env good over an invalid file value", idFile + `,"name_rule_required":"x"}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "false"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadWith(t, c.file, c.env)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestNameRuleRequiredLoadErrorWording(t *testing.T) {
	_, err := loadWith(t, idFile+`}`, map[string]string{"MASH_NAME_RULE_REQUIRED": "true"})
	if err == nil || err.Error() != ErrNameRuleRequired.Error() {
		t.Fatalf("got %v", err)
	}
}
