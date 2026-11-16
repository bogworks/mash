package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
)

// credRun runs `mashctl credential ARGS` with no environment; the backend must not be opened.
func credRun(t *testing.T, stdin string, args ...string) (rc int, stdout, stderr string) {
	t.Helper()
	old, oldIn := openCredBackend, credStdin
	openCredBackend = func(config.Config, func(string) string, int) (*credBackend, error) {
		t.Fatal("the backend was opened")
		return nil, nil
	}
	credStdin = strings.NewReader(stdin)
	t.Cleanup(func() { openCredBackend, credStdin = old, oldIn })
	var so, se bytes.Buffer
	env := func(string) string { return "" }
	rc = run(append([]string{"credential"}, args...), env, config.LookupFromFunc(env), &so, &se)
	return rc, so.String(), se.String()
}

func TestCredentialIssueUsageErrors(t *testing.T) {
	_, pub, _ := credential.NewKey()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no address", []string{"issue", "--key", pub}, "--address is required"},
		{"no key", []string{"issue", "--address", "acme.x"}, "--key is required"},
		{"unknown flag", []string{"issue", "--address", "acme.x", "--key", pub, "--bogus"}, "unknown flag --bogus"},
		{"request and address", []string{"issue", "--request", "-", "--address", "acme.x"}, "--request conflicts with --address"},
		{"request and key", []string{"issue", "--request", "-", "--key", pub}, "--request conflicts with --key"},
		{"request and endpoint", []string{"issue", "--request", "-", "--endpoint", "agent.acme.x"}, "--request conflicts with --endpoint"},
		{"request and purpose", []string{"issue", "--request", "-", "--purpose", "rotation"}, "--request conflicts with --purpose"},
		{"flag without value", []string{"issue", "--address"}, "--address needs a value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, so, se := credRun(t, "", tc.args...)
			if rc != 2 || so != "" || !strings.Contains(se, tc.want) || !strings.Contains(se, "usage: mashctl credential issue") {
				t.Fatalf("rc %d stdout %q stderr %q, want usage error with %q", rc, so, se, tc.want)
			}
		})
	}
	if rc, _, se := credRun(t, "", "issue", "--address", "acme.x", "--key", pub, "--servers", "zero"); rc != 2 || !strings.Contains(se, "--servers") {
		t.Fatalf("bad --servers: rc %d %q", rc, se)
	}
}

func TestCredentialSeedIsRefusedAndNeverEchoed(t *testing.T) {
	seed, pub, _ := credential.NewKey()
	s := string(seed)
	body, _ := json.Marshal(map[string]string{"address": "acme.x", "endpoint": "agent.acme.x", "public_key": s})
	for _, tc := range []struct {
		name  string
		stdin string
		args  []string
	}{
		{"--key", "", []string{"issue", "--address", "acme.x", "--key", s}},
		{"--address", "", []string{"issue", "--address", s, "--key", pub}},
		{"--endpoint", "", []string{"issue", "--address", "acme.x", "--key", pub, "--endpoint", "agent." + s}},
		{"--request stdin", string(body), []string{"issue", "--request", "-"}},
	} {
		for _, asJSON := range []bool{false, true} {
			args := tc.args
			if asJSON {
				args = append(append([]string{}, args...), "--json")
			}
			rc, so, se := credRun(t, tc.stdin, args...)
			if rc != 1 || strings.Contains(so+se, s) {
				t.Fatalf("%s json=%v: rc %d, seed echoed or not refused: %q %q", tc.name, asJSON, rc, so, se)
			}
			if asJSON {
				var res credential.Result
				if err := json.Unmarshal([]byte(so), &res); err != nil || res.Outcome != credential.OutcomeRefused || res.Refusal == nil || res.Refusal.Code != credential.CodeSeed || res.ExitCode() != 1 {
					t.Fatalf("%s json: %q %v", tc.name, so, err)
				}
				if se != "" {
					t.Fatalf("%s json: stderr %q", tc.name, se)
				}
			} else if !strings.Contains(se, "refused: seed_in_request") || !strings.Contains(se, "exposed") {
				t.Fatalf("%s: %q", tc.name, se)
			}
		}
	}
	// show and revoke refuse a seed as the target too.
	for _, verb := range []string{"show", "revoke"} {
		rc, so, se := credRun(t, "", verb, s)
		if rc != 2 || strings.Contains(so+se, s) || !strings.Contains(se, "exposed") {
			t.Fatalf("%s: rc %d %q %q", verb, rc, so, se)
		}
	}
}

func TestCredentialRequestFileMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	if err := os.WriteFile(p, []byte(`{"address":"acme.x","bogus":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, _, se := credRun(t, "", "issue", "--request", p)
	if rc != 1 || !strings.Contains(se, "refused: malformed") {
		t.Fatalf("rc %d %q", rc, se)
	}
	if rc, _, _ := credRun(t, "", "issue", "--request", filepath.Join(t.TempDir(), "none")); rc != 6 {
		t.Fatalf("missing request file: rc %d", rc)
	}
}

func TestCredentialShowAndRevokeUsage(t *testing.T) {
	_, pub, _ := credential.NewKey()
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"show"}, "a target is required"},
		{[]string{"show", "notakey"}, "neither a user public key nor an address"},
		{[]string{"revoke"}, "a target is required"},
		{[]string{"revoke", "acme.x"}, "not a NATS user public key"},
		{[]string{"revoke", apub}, "not a NATS user public key"},
		{[]string{"revoke", pub, "--servers", "0"}, "--servers"},
	} {
		rc, so, se := credRun(t, "", tc.args...)
		if rc != 2 || so != "" || !strings.Contains(se, tc.want) {
			t.Fatalf("%v: rc %d %q %q", tc.args, rc, so, se)
		}
	}
}

func TestCredentialHelpAndUnknown(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"issue", "--help"}, {"show", "--help"}, {"revoke", "--help"}, {"key", "--help"}, {"install", "-h"}} {
		rc, so, se := credRun(t, "", args...)
		if rc != 0 || !strings.Contains(so, "usage: mashctl credential") || se != "" {
			t.Fatalf("%v: rc %d %q %q", args, rc, so, se)
		}
	}
	if rc, _, se := credRun(t, ""); rc != 2 || !strings.Contains(se, "usage: mashctl credential") {
		t.Fatalf("no verb: rc %d %q", rc, se)
	}
	if rc, _, _ := credRun(t, "", "frobnicate"); rc != 2 {
		t.Fatalf("unknown verb: rc %d", rc)
	}
}

func TestCredentialKeyAndInstallThroughMashctl(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "ep.seed")
	rc, so, se := credRun(t, "", "key", "--out", seed)
	if rc != 0 || !strings.Contains(so, "public key: U") || se != "" {
		t.Fatalf("rc %d %q %q", rc, so, se)
	}
	if fi, err := os.Stat(seed); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("seed file: %v %v", fi, err)
	}
	if rc, _, _ := credRun(t, "", "key", "--out", seed); rc == 0 {
		t.Fatal("key overwrote the seed")
	}
}

func TestCredentialIssueExitCodesFollowTheResult(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		code    int
	}{{credential.OutcomeIssued, 0}, {credential.OutcomeRefused, 1}, {credential.OutcomeError, 6}} {
		if got := (credential.Result{Outcome: tc.outcome}).ExitCode(); got != tc.code {
			t.Errorf("%s: %d", tc.outcome, got)
		}
	}
	// emitIssue: the JWT alone goes to --out (0600, truncated on a second run); the text omits it.
	_, pub, _ := credential.NewKey()
	res := credential.Result{Outcome: credential.OutcomeIssued, Address: "acme.x", PublicKey: pub, JWT: "JWTTEXT", Tags: []string{"a", "b"}, Generation: 4}
	out := filepath.Join(t.TempDir(), "x.jwt")
	for range 2 {
		var so, se bytes.Buffer
		if rc := emitIssue(res, issueOpts{out: out, force: true}, &so, &se); rc != 0 {
			t.Fatalf("rc %d %s", rc, se.String())
		}
		if strings.Contains(so.String(), "JWTTEXT") || !strings.Contains(so.String(), "issued: acme.x, key "+pub+", generation 4\ntags: a b\njwt written to "+out) {
			t.Fatalf("stdout %q", so.String())
		}
		if b, _ := os.ReadFile(out); string(b) != "JWTTEXT\n" {
			t.Fatalf("file %q", b)
		}
		if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", fi.Mode().Perm())
		}
	}
	// Without force an existing --out is not replaced, even if the early check was raced.
	if err := os.WriteFile(out, []byte("KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if rc := emitIssue(res, issueOpts{out: out}, &so, &se); rc != 6 {
		t.Fatalf("no force: rc %d", rc)
	}
	if b, _ := os.ReadFile(out); string(b) != "KEEP\n" {
		t.Fatalf("file replaced: %q", b)
	}
	so.Reset()
	se.Reset()
	if rc := emitIssue(res, issueOpts{}, &so, &se); rc != 0 || !strings.HasSuffix(so.String(), "\nJWTTEXT\n") || se.Len() != 0 {
		t.Fatalf("stdout form: rc %d %q %q", rc, so.String(), se.String())
	}
	so.Reset()
	if rc := emitIssue(res, issueOpts{json: true}, &so, &se); rc != 0 {
		t.Fatal(rc)
	}
	var back credential.Result
	if err := json.Unmarshal(so.Bytes(), &back); err != nil || back.JWT != "JWTTEXT" || bytes.Count(so.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("json %q %v", so.String(), err)
	}
	so.Reset()
	ref := credential.Result{Outcome: credential.OutcomeRefused, Refusal: &credential.Refusal{Code: "no_list_set", Detail: "none"}}
	if rc := emitIssue(ref, issueOpts{}, &so, &se); rc != 1 || so.Len() != 0 || !strings.Contains(se.String(), "refused: no_list_set: none") {
		t.Fatalf("refused: rc %d %q %q", rc, so.String(), se.String())
	}
}

func TestCredentialIssueOutExistingNeedsForce(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.jwt")
	if err := os.WriteFile(out, []byte("KEEP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, _, se := credRun(t, "", "issue", "--address", "acme.x", "--key", "UAAAA", "--out", out)
	if rc != 2 || !strings.Contains(se, out+" exists; pass --force to overwrite") {
		t.Fatalf("rc %d %q", rc, se)
	}
	if b, _ := os.ReadFile(out); string(b) != "KEEP\n" {
		t.Fatalf("file changed: %q", b)
	}
	// --force parses and gets past the early check.
	if o, _, err := parseIssueArgs([]string{"--address", "acme.x", "--key", "UAAAA", "--out", out, "--force"}); err != nil || !o.force {
		t.Fatalf("%+v %v", o, err)
	}
}
