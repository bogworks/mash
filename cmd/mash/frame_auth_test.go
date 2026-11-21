package main

import (
	"errors"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/testenv"
)

// A refused sign-in is worded the one way on every command that connects: the rules' "Not signed in" case.
func TestRefusedCredentialsFramedOnEveryCommand(t *testing.T) {
	w := tlsWorld(t, "a")
	other := testenv.Start(t)
	creds, _ := other.Mint(t, tAddr("a"))
	env := strictEnv(w, "a", map[string]string{"MASH_CREDS": creds})
	want := "error: " + tTo("a") + " cannot sign in: the server refused its credentials\nhint: mash doctor\n"
	for _, args := range [][]string{{"send", tTo("a"), "hi"}, {"inbox"}, {"read", "1"}, {"who"}} {
		r := runMash(env, args...)
		if r.code != 1 || r.out != "" || r.errs != want {
			t.Fatalf("mash %v: %+v", args, r)
		}
	}
	// --json: the same error as one JSON object on stdout, on every command that connects.
	for _, args := range [][]string{{"send", tTo("a"), "hi", "--json"}, {"inbox", "--json"}, {"read", "1", "--json"}, {"who", "--json"}} {
		r := runMash(env, args...)
		wantJSON := `{"error":"` + tTo("a") + ` cannot sign in: the server refused its credentials","hint":"mash doctor"}` + "\n"
		if r.code != 1 || r.out != wantJSON || r.errs != "" {
			t.Fatalf("mash %v: %+v", args, r)
		}
	}
}

// The MCP tools say it as the table's tools do: the hint is the last line.
func TestFramedMCPText(t *testing.T) {
	err := framed(tTo("a"), errors.New("nats: Authorization Violation"))
	got := cmdcore.ToolError(err).Error()
	if want := "error: " + tTo("a") + " cannot sign in: the server refused its credentials\nhint: mash doctor"; got != want {
		t.Fatalf("got %q", got)
	}
	plain := errors.New("boom")
	if framed("x", plain) != plain {
		t.Fatal("an unrelated error must pass through")
	}
}
