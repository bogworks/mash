package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

const refusal = "mashctl: agent_token is set but mashctl cannot act on a three-token identity yet; unset agent_token (MASH_AGENT_TOKEN)"

func tokenEnv(t *testing.T, extra map[string]string) func(string) string {
	t.Helper()
	m := map[string]string{"HOME": t.TempDir(), "MASH_URL": "nats://127.0.0.1:1", "MASH_OWNER": "o", "MASH_NAME": "a", "MASH_AGENT_TOKEN": "ag"}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

// Every verb that loads config refuses before it acts, whatever else the arguments or configuration say.
func TestMashctlRefusesAgentToken(t *testing.T) {
	for _, args := range [][]string{
		{"list"},
		{"init"},
		{"limits"},
		{"provision", "x"},
		{"policy", "explain", "a", "b"},
		{"policy", "check"},
		{"policy", "show"},
		{"policy", "apply", "--dry-run"},
		{"policy", "diff"},
		{"policy", "repair", "--dry-run"},
		{"policy", "drift"},
		{"adopt", "--server", "nats://127.0.0.1:1", "--operator-key", "/x", "--account-key", "/x", "--sys-creds", "/x", "--admin-creds", "/x"},
		{"add", "x"},
	} {
		var out, errb bytes.Buffer
		code := runEnv(args, tokenEnv(t, nil), &out, &errb)
		if code == 0 || !strings.Contains(errb.String(), refusal) {
			t.Errorf("%v: code %d stdout %q stderr %q", args, code, out.String(), errb.String())
		}
	}
}

// The loader itself, with the tolerated errors in play: the refusal wins, and an unset token changes nothing.
func TestLoadConfigRefusesAgentToken(t *testing.T) {
	for _, extra := range []map[string]string{nil, {"MASH_URL": ""}, {"MASH_OWNER": ""}, {"MASH_NAME": ""}} {
		if _, err := loadConfig(tokenEnv(t, extra), lk(tokenEnv(t, extra))); !errors.Is(err, errAgentTokenSet) || err.Error() != refusal {
			t.Errorf("%v: got %v", extra, err)
		}
		if _, err := loadCtlConfig(tokenEnv(t, extra), lk(tokenEnv(t, extra)), false); !errors.Is(err, errAgentTokenSet) {
			t.Errorf("loadCtlConfig %v: got %v", extra, err)
		}
	}
	if _, err := loadConfig(tokenEnv(t, map[string]string{"MASH_AGENT_TOKEN": ""}), lk(tokenEnv(t, map[string]string{"MASH_AGENT_TOKEN": ""}))); errors.Is(err, errAgentTokenSet) {
		t.Fatalf("unset token refused: %v", err)
	}
}

// With the process lookup set, a token that is set but empty is the config error, not a silent legacy identity.
func TestLoadConfigEmptyTokenIsAnError(t *testing.T) {
	env := tokenEnv(t, map[string]string{"MASH_AGENT_TOKEN": "x"})
	lookup := func(k string) (string, bool) {
		if k == "MASH_AGENT_TOKEN" {
			return "", true
		}
		return env(k), env(k) != ""
	}
	_, err := loadConfig(env, lookup)
	if err == nil || !strings.Contains(err.Error(), "set but empty") {
		t.Fatalf("got %v", err)
	}
}

// lk is the lookup tests pass when they inject only an env func: a variable is present iff it is not empty.
func lk(env func(string) string) config.Lookup { return config.LookupFromFunc(env) }

// runEnv is run with the lookup derived from env.
func runEnv(args []string, env func(string) string, stdout, stderr io.Writer) int {
	return run(args, env, config.LookupFromFunc(env), stdout, stderr)
}
