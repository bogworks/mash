package config

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func loadIdentity(t *testing.T, fileBody string, env map[string]string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	if fileBody != "" {
		writeFile(t, home, fileBody)
	}
	return load(envOf(env), home)
}

const idFile = `{"url":"tls://x:4222","owner":"o"`

func TestIdentityUnsetIsLegacy(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := sap.Legacy("o", "a")
	if !id.IsLegacy() || id.Subjects() != want.Subjects() || id.Flat() != "a" || id.Flat() != c.Name {
		t.Fatalf("got %+v", id)
	}
	if c.Source["agent_token"] != "unset" {
		t.Fatalf("source %q", c.Source["agent_token"])
	}
}

func TestIdentityFromFileAndEnv(t *testing.T) {
	c, err := loadIdentity(t, idFile+`,"agent_token":"fileagent"}`, map[string]string{"MASH_NAME": "a"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity()
	if err != nil || id.IsLegacy() || id.Agent != "fileagent" || id.Owner != "o" || id.Name != "a" {
		t.Fatalf("file: %+v %v", id, err)
	}
	if c.Source["agent_token"] != "config.json" || c.FileValues["agent_token"] != "fileagent" {
		t.Fatalf("file source %v %v", c.Source, c.FileValues)
	}

	c, err = loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": "envagent"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err = c.Identity(); err != nil || id.Agent != "envagent" || c.Source["agent_token"] != "MASH_AGENT_TOKEN" {
		t.Fatalf("env: %+v %v %v", id, err, c.Source)
	}

	c, err = loadIdentity(t, idFile+`,"agent_token":"fileagent"}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": "envagent"})
	if err != nil {
		t.Fatal(err)
	}
	if id, err = c.Identity(); err != nil || id.Agent != "envagent" || c.Source["agent_token"] != "MASH_AGENT_TOKEN" || c.FileValues["agent_token"] != "fileagent" {
		t.Fatalf("env wins: %+v %v %v", id, err, c.Source)
	}
}

func TestAgentTokenSetButEmptyInFileIsInvalid(t *testing.T) {
	_, err := loadIdentity(t, idFile+`,"agent_token":""}`, map[string]string{"MASH_NAME": "a"})
	if err == nil || !strings.Contains(err.Error(), "agent_token") {
		t.Fatalf("got %v", err)
	}
	// An env value overrides the empty file key, as for every setting.
	if _, err := loadIdentity(t, idFile+`,"agent_token":""}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": "ok"}); err != nil {
		t.Fatal(err)
	}
	// Inert: nothing is validated.
	if _, err := loadIdentity(t, idFile+`,"agent_token":""}`, nil); err != nil {
		t.Fatal(err)
	}
}

// Through the plain-func adapter (load), an empty value is absent. A real set-but-empty variable is an error: see
// TestEmptyAgentTokenEnvIsInvalidWhenNamed.
func TestAgentTokenEmptyEnvReadsAsUnset(t *testing.T) {
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_AGENT_TOKEN": ""})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := c.Identity(); err != nil || !id.IsLegacy() {
		t.Fatalf("%+v %v", id, err)
	}
}

func TestInvalidIdentityTokens(t *testing.T) {
	long := func(n int) string { return "a" + strings.Repeat("b", n-1) }
	for _, tc := range []struct{ agent, owner, name, want string }{
		{"Upper", "o", "a", "MASH_AGENT_TOKEN"},
		{"under_score", "o", "a", "MASH_AGENT_TOKEN"},
		{long(33), "o", "a", "MASH_AGENT_TOKEN"},
		{"cc", "o", "a", "MASH_AGENT_TOKEN"},
		{"ag", long(33), "a", "owner"},
		{"ag", "o", long(33), "MASH_NAME"},
		{"ag", "o", "n_x", "MASH_NAME"},
		{long(32), long(32), long(2), "MASH_AGENT_TOKEN, owner and MASH_NAME"},
	} {
		home := t.TempDir()
		writeFile(t, home, `{"url":"tls://x:4222","owner":"`+tc.owner+`"}`)
		_, err := load(envOf(map[string]string{"MASH_NAME": tc.name, "MASH_AGENT_TOKEN": tc.agent}), home)
		if err == nil || !strings.HasPrefix(err.Error(), "mash: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v", tc, err)
		}
	}
}

func TestLongNameLegacyOnly(t *testing.T) {
	name := "a" + strings.Repeat("b", 39) // 40 characters
	c, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": name})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := c.Identity(); err != nil || id.Flat() != name {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err := loadIdentity(t, idFile+`}`, map[string]string{"MASH_NAME": name, "MASH_AGENT_TOKEN": "ag"}); err == nil {
		t.Fatal("33..48 character name accepted with an agent token")
	}
}

func TestInertConfigHasNoIdentity(t *testing.T) {
	c, err := loadIdentity(t, idFile+`,"agent_token":"ag"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Identity(); err == nil || !strings.Contains(err.Error(), "no identity") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentTokenFileValueMustBeANonEmptyString(t *testing.T) {
	env := map[string]string{"MASH_NAME": "a"}
	for _, bad := range []string{`null`, `""`, `7`, `true`, `["x"]`} {
		_, err := loadIdentity(t, idFile+`,"agent_token":`+bad+`}`, env)
		if err == nil || !strings.Contains(err.Error(), "agent_token") {
			t.Errorf("agent_token %s: got %v", bad, err)
		}
	}
	c, err := loadIdentity(t, idFile+`,"agent_token":"good"}`, env)
	if err != nil || c.AgentToken != "good" {
		t.Fatalf("got %q, %v", c.AgentToken, err)
	}
	// Inert: nothing is validated.
	if _, err := loadIdentity(t, idFile+`,"agent_token":null}`, nil); err != nil {
		t.Fatal(err)
	}
}
