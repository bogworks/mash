package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
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

// The spec's send, inbox, read and who tools have the case, word for word (the spec's example address standing for
// the world's endpoint a).
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
	for _, name := range []string{"send", "inbox", "read", "who"} {
		found := false
		for _, tl := range doc.Tools {
			if tl.Name != name {
				continue
			}
			for _, c := range tl.Errors {
				if c.Case == "A deleted endpoint here" {
					found = true
					if got := strings.Replace(c.Text, "acme.demo.bob", tTo("a"), 1); got != deletedSentence() {
						t.Errorf("%s: spec %q, tools %q", name, got, deletedSentence())
					}
				}
			}
		}
		if !found {
			t.Errorf(`spec/mash-mcp.json %s has no case "A deleted endpoint here"`, name)
		}
	}
}

// authFailingSvc is the mcpSvc with the failure the server's refusal of a sign-in would be injected at the one place
// each method frames its error (mcpSvc.frame): a refused sign-in cannot be produced mid-call against the test server.
type authFailingSvc struct {
	*mcpSvc
	ep *endpoint.Endpoint
}

var errAuth = errors.New("nats: Authorization Violation")

func (f authFailingSvc) Inbox(context.Context, cmds.InboxQuery) (cmds.InboxView, error) {
	return cmds.InboxView{}, f.frame(f.ep, errAuth)
}
func (f authFailingSvc) Message(context.Context, int) (cmds.ReadView, error) {
	return cmds.ReadView{}, f.frame(f.ep, errAuth)
}
func (f authFailingSvc) Send(context.Context, string, string, cmds.SendOptions) (cmds.SendReceipt, error) {
	return cmds.SendReceipt{}, f.frame(f.ep, errAuth)
}
func (f authFailingSvc) Who(context.Context) ([]cmds.WhoEntry, int, error) {
	return nil, 0, f.frame(f.ep, fmt.Errorf("who: asking who is online: %w", errAuth))
}

// The four table tools word a refused sign-in as the shell does: the deleted sentence once the creds file was set
// aside, "cannot sign in" before, through the tool table's own tool.
func TestTableToolsFrameADeletedEndpoint(t *testing.T) {
	w := newWorld(t, "a")
	ep := w.openEP(t, "a")
	env := w.env["a"]
	cfg, err := config.Load(config.LookupFromFunc(envFrom(env)), env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	var st atomic.Pointer[sessionState]
	st.Store(&sessionState{ep: ep})
	svc := &mcpSvc{state: &st, name: cfg.DisplayName(), callTimeout: time.Second, cfg: cfg}
	f := authFailingSvc{svc, ep}
	table := newCommands(func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: f}, nil })
	signIn := "error: " + tTo("a") + " cannot sign in: the server refused its credentials\nhint: mash doctor"
	args := map[string]string{"send": `{"address":"` + tTo("b") + `","message":"hi"}`, "inbox": `{}`, "read": `{"number":1}`, "who": `{}`}
	for _, phase := range []string{"before", "after"} {
		if phase == "after" {
			if err := os.Rename(cfg.Creds, cfg.Creds+".deleted"); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"send", "inbox", "read", "who"} {
			c, _ := table.Lookup(name)
			_, _, err := c.Tool(table.Env).MCP().CallStructured(context.Background(), json.RawMessage(args[name]))
			want := signIn
			if phase == "after" {
				want = deletedSentence()
			}
			if err == nil || err.Error() != want {
				t.Errorf("%s %s: %v, want %q", name, phase, err, want)
			}
		}
	}
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
	if err := os.Rename(creds, creds+".deleted"); err != nil {
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

func TestDeletedHereNeedsTheCredsFileGone(t *testing.T) {
	creds := filepath.Join(t.TempDir(), "bob.creds")
	if err := os.WriteFile(creds+".deleted", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("nats: Authorization Violation")
	if d := deletedHere("bob", creds, refused); d == nil || !strings.HasSuffix(d.Msg, cmdcore.ReasonEndpointDeleted) {
		t.Errorf("marker + absent creds: %v", d)
	}
	if err := os.WriteFile(creds, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if d := deletedHere("bob", creds, refused); d != nil {
		t.Errorf("marker + present creds read as deleted: %v", d)
	}
}

func TestOnlyTheDeletedFailureIsTakenAsDeleted(t *testing.T) {
	other := cliframe.Failf("bob: something else broke")
	if isEndpointDeleted(other) || isEndpointDeleted(errors.New("x")) {
		t.Error("a non-deleted error matched")
	}
	if !isEndpointDeleted(cmdcore.EndpointDeleted("bob")) {
		t.Error("the deleted failure did not match")
	}
	cfg := config.Config{Name: "bob"}
	if got := leaseCause(cfg, other); strings.Contains(got, cmdcore.ReasonEndpointDeleted) {
		t.Errorf("leaseCause: %q", got)
	}
	_, err := (&sessionState{failed: other}).usable("bob")
	if err == nil || strings.Contains(err.Error(), cmdcore.ReasonEndpointDeleted) {
		t.Errorf("usable: %v", err)
	}
	if got := leaseCause(cfg, cmdcore.EndpointDeleted("bob")); got != cmdcore.ReasonEndpointDeleted {
		t.Errorf("leaseCause deleted: %q", got)
	}
}
