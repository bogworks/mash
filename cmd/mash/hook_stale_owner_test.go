package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// #816: an owner record left by a stopped serve (its Claude pid is gone) must not be reported to the model as "another
// Claude process of this same session holds the endpoint". The record has this session's id and another launch token,
// as a resume of the session after a restart has.
func TestHookStaleOwnerOfAStoppedProcessIsNotNamedAsAHolder(t *testing.T) {
	skipOnWindows(t)
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run true: %v", err)
	}
	dead := cmd.Process.Pid
	env := map[string]string{"HOME": t.TempDir(), "MASH_ENDPOINT": "o." + testGroup + ".a", "MASH_SERVER": "nats://127.0.0.1:1", "MASH_CREDS": "/none",
		"MASH_LAUNCH_TOKEN": "tok-new"}
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Own(fmt.Sprintf(`{"pid":%d,"session":"S-one","launch":"tok-old"}`, dead)); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"UserPromptSubmit", "PostToolUse"} {
		out := runHookAs(t, env, event, "S-one")
		if strings.Contains(out, "holds the endpoint") || strings.Contains(out, "pid "+strconv.Itoa(dead)) {
			t.Fatalf("%s named a stopped process as the holder:\n%s", event, out)
		}
		// No lease-wait state is live, so waitNotice adds nothing: the session is told nothing.
		if out != "" {
			t.Fatalf("%s said %q, want nothing", event, out)
		}
	}
}
