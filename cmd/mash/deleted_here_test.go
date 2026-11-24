package main

import (
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore"
)

// #1055 (REQ-008): send, inbox, read and who say the endpoint was deleted when mashctl set its creds file aside as
// <creds>.deleted, in the sentence status and doctor print; with no set-aside file the missing-credentials error stays.
var deletedHereCommands = [][]string{
	{"send", "acme.demo.bob", "hi"},
	{"inbox"},
	{"read", "1"},
	{"who"},
}

func deletedHereWorld(t *testing.T, aside bool) (map[string]string, *world) {
	t.Helper()
	w := tlsWorld(t, "a")
	creds := w.env["a"]["MASH_CREDS"]
	if aside {
		if err := os.Rename(creds, creds+".deleted"); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(creds); err != nil {
		t.Fatal(err)
	}
	return strictEnv(w, "a", nil), w
}

func TestEverydayCommandsSayTheEndpointWasDeletedHere(t *testing.T) {
	env, _ := deletedHereWorld(t, true)
	sentence := cmdcore.ReasonEndpointDeleted
	for _, args := range deletedHereCommands {
		r := runMash(env, append(args, "--endpoint", tTo("a"))...)
		if r.code != 1 || r.out != "" || r.errs != "error: "+sentence+"\nhint: mash doctor\n" {
			t.Errorf("%v: %+v", args, r)
		}
		for _, l := range strings.Split(r.errs, "\n") {
			if len(l) > 80 {
				t.Errorf("%v: line over 80 columns: %q", args, l)
			}
		}
		j := runMash(env, append(args, "--endpoint", tTo("a"), "--json")...)
		if j.code != 1 || j.errs != "" || j.out != `{"error":"`+sentence+`","hint":"mash doctor"}`+"\n" {
			t.Errorf("%v --json: %+v", args, j)
		}
	}
}

func TestEverydayCommandsKeepNoCredentialsWithoutTheSetAsideFile(t *testing.T) {
	env, _ := deletedHereWorld(t, false)
	for _, args := range deletedHereCommands {
		r := runMash(env, append(args, "--endpoint", tTo("a"))...)
		if r.code != 1 || r.out != "" || !strings.Contains(r.errs, " has no credentials here: ") ||
			strings.Contains(r.errs, "deleted") || !strings.HasSuffix(r.errs, "\nhint: mash doctor\n") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

// status, doctor and the four commands print one sentence.
func TestStatusDoctorAndEverydayCommandsShareTheDeletedSentence(t *testing.T) {
	env, _ := deletedHereWorld(t, true)
	for _, args := range append([][]string{{"status"}, {"doctor"}}, deletedHereCommands...) {
		r := runMash(env, append(args, "--endpoint", tTo("a"))...)
		if !strings.Contains(r.out+r.errs, cmdcore.ReasonEndpointDeleted) {
			t.Errorf("%v: %+v lacks %q", args, r, cmdcore.ReasonEndpointDeleted)
		}
	}
}
