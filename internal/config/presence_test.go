package config

import (
	"strings"
	"testing"
)

// lookupOf reports a variable present iff it is a key of m, even when its value is "".
func lookupOf(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func loadPresence(t *testing.T, fileBody string, env map[string]string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	if fileBody != "" {
		writeFile(t, home, fileBody)
	}
	return Load(lookupOf(env), home)
}

func TestEmptyAgentTokenEnvIsInvalidWhenNamed(t *testing.T) {
	_, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": ""})
	if err == nil || !strings.Contains(err.Error(), "MASH_AGENT_TOKEN") || !strings.Contains(err.Error(), "set but empty") {
		t.Fatalf("got %v", err)
	}
	// An empty env value is an error even when config.json has a valid agent_token: it must not fall back silently.
	_, err = loadPresence(t, idFile+`,"agent_token":"fileagent"}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": ""})
	if err == nil || !strings.Contains(err.Error(), "set but empty") {
		t.Fatalf("with file token: got %v", err)
	}
}

func TestAgentTokenUnsetIsLegacyAndValidIsNewForm(t *testing.T) {
	c, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_NAME": "a"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := c.Identity(); err != nil || !id.IsLegacy() {
		t.Fatalf("unset: %+v %v", id, err)
	}
	c, err = loadPresence(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": "ag"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity()
	if err != nil || id.IsLegacy() || id.Agent != "ag" {
		t.Fatalf("valid: %+v %v", id, err)
	}
}

func TestEmptyOtherVariablesStillReadAsUnset(t *testing.T) {
	c, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_OWNER": "", "MASH_URL": "", "MASH_CREDS": ""})
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "o" || c.URL != "tls://x:4222" || c.Source["owner"] != "config.json" {
		t.Fatalf("%+v %v", c, c.Source)
	}
}

func TestInertConfigIgnoresEmptyAgentToken(t *testing.T) {
	if _, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_AGENT_TOKEN": ""}); err != nil {
		t.Fatal(err)
	}
}
