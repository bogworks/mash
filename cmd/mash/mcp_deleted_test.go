package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/testenv"
)

// #1396: the MCP tools say a deleted endpoint was deleted in the sentence the shell prints (#1055), as a tool error.
func deletedSentence() string {
	return "error: " + tTo("a") + ": " + cmdcore.ReasonEndpointDeleted + "\nhint: mash doctor"
}

// Path 1: serve started for an endpoint already deleted here. A serve that cannot open its endpoint exposes only
// version, status and doctor (there is no tool to fail), so the sentence is in its instructions and on stderr; the
// state a tool call meets after a wait that ended in that failure words it as the tools word errors.
func TestServeSaysTheEndpointWasDeletedHere(t *testing.T) {
	env, _ := deletedHereWorld(t, true)
	s := startServe(t, env)
	r := s.initialize(t)
	instr := r["result"].(map[string]any)["instructions"].(string)
	sentence := tTo("a") + ": " + cmdcore.ReasonEndpointDeleted
	if !strings.Contains(instr, sentence) {
		t.Errorf("instructions %q", instr)
	}
	if !strings.Contains(s.stderr(), sentence) {
		t.Errorf("stderr: %q", s.stderr())
	}
}

func TestToolsAfterAFailedStartSayTheEndpointWasDeleted(t *testing.T) {
	creds := filepath.Join(t.TempDir(), "bob.creds")
	if err := os.WriteFile(creds+".deleted", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Creds: creds, Name: "bob"}
	failed := startFailed(cfg, &endpoint.CredsMissingError{Path: creds})
	_, err := (&sessionState{failed: failed}).usable(tTo("a"))
	if err == nil || err.Error() != "error: "+cfg.DisplayName()+": "+cmdcore.ReasonEndpointDeleted+"\nhint: mash doctor" {
		t.Errorf("%v", err)
	}
}

// Without the set-aside file the instructions keep the old wording.
func TestServeKeepsTheMissingCredsWordingWithoutTheSetAsideFile(t *testing.T) {
	env, _ := deletedHereWorld(t, false)
	s := startServe(t, env)
	r := s.initialize(t)
	if instr := r["result"].(map[string]any)["instructions"].(string); strings.Contains(instr, "was deleted") {
		t.Errorf("%q", instr)
	}
}

// The spec's send tool has the case, word for word (the spec's example address standing for the world's endpoint a).
func TestMCPDeletedEndpointMatchesSpec(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "mash-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tools []struct {
			Name   string `json:"name"`
			Errors []struct{ Case, Text string }
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, tl := range doc.Tools {
		if tl.Name != "send" {
			continue
		}
		for _, c := range tl.Errors {
			if c.Case == "A deleted endpoint here" {
				if got := strings.Replace(c.Text, "acme.demo.bob", tTo("a"), 1); got != deletedSentence() {
					t.Errorf("spec %q, tools %q", got, deletedSentence())
				}
				return
			}
		}
	}
	t.Fatal(`spec/mash-mcp.json send has no case "A deleted endpoint here"`)
}

// Path 2: the endpoint was deleted while serve runs, so the server refuses the sign-in. When the configured creds
// file has been set aside, the failure is the sentence, for the shell (a refusing server, creds file set aside) and for
// the helper the tools use; otherwise it stays "cannot sign in".
func TestRefusedSignInOnADeletedEndpointSaysDeleted(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t)
	creds, _ := other.Mint(t, tAddr("a"))
	env := strictEnv(w, "a", map[string]string{"MASH_CREDS": creds})
	signIn := "error: " + tTo("a") + " cannot sign in: the server refused its credentials\nhint: mash doctor\n"
	if r := runMash(env, "inbox"); r.errs != signIn {
		t.Fatalf("before: %+v", r)
	}
	if err := os.WriteFile(creds+".deleted", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range deletedHereCommands {
		r := runMash(env, append(args, "--endpoint", tTo("a"))...)
		if r.code != 1 || r.out != "" || r.errs != deletedSentence()+"\n" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	cfg := config.Config{Creds: creds, Name: "bob"}
	auth := errors.New("nats: Authorization Violation")
	got := cmdcore.ToolError(framedIn(cfg, tTo("a"), auth)).Error()
	if want := deletedSentence(); got != want {
		t.Errorf("tool: %q", got)
	}
	if err := framedIn(config.Config{Creds: creds + ".other"}, tTo("a"), auth); strings.Contains(err.Error(), "deleted") {
		t.Errorf("no set-aside file: %v", err)
	}
	plain := errors.New("boom")
	if framedIn(cfg, tTo("a"), plain) != plain {
		t.Error("an unrelated error must pass through")
	}
}
