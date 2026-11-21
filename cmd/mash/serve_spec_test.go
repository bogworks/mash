package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// TestServeMatchesSpec runs every x-output case of serve in spec/mash.json: started by an agent host (stdin is a pipe)
// it speaks MCP and prints nothing on stderr; run by hand (stdin a terminal) it stops at once with the spec's text.
func TestServeMatchesSpec(t *testing.T) {
	spec := loadConfSpec(t)
	var cases []struct {
		Case   string
		Text   string
		Stream string
		Exit   int
	}
	confWalk(spec.Root, func(path string, c confCmd) {
		if path != "serve" {
			return
		}
		for _, m := range c.Metadata {
			if m.Name == "x-output" {
				if err := json.Unmarshal(m.Value, &cases); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	if len(cases) == 0 {
		t.Fatal("serve has no x-output in spec/mash.json")
	}
	env := confEnv(t)
	get := func(l *lockedBuf) string {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.b.String()
	}
	force := func(v bool) func() {
		old := stdinIsTerminal
		stdinIsTerminal = func(io.Reader) bool { return v }
		return func() { stdinIsTerminal = old }
	}
	for _, oc := range cases {
		t.Run(oc.Case, func(t *testing.T) {
			switch oc.Case {
			case "Started by an agent host":
				if oc.Exit != 0 || oc.Stream != "stdout" {
					t.Fatalf("spec case changed: %+v", oc)
				}
				defer force(false)()
				inR, inW := io.Pipe()
				var out, errb lockedBuf
				done := make(chan int, 1)
				go func() { done <- run([]string{"serve"}, envFrom(env), inR, &out, quiet(&errb)) }()
				if _, err := io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`+"\n"); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(5 * time.Second)
				for !strings.Contains(get(&out), `"id":1`) && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if !strings.Contains(get(&out), `"id":1`) {
					t.Fatalf("the MCP server did not answer initialize: %q", get(&out))
				}
				inW.Close() // the host closing standard input shuts it down
				select {
				case code := <-done:
					if code != oc.Exit {
						t.Errorf("exit %d, spec %d (stderr %q)", code, oc.Exit, get(&errb))
					}
				case <-time.After(5 * time.Second):
					t.Fatal("serve did not exit when stdin closed")
				}
				if s := get(&errb); s != "" {
					t.Errorf("stderr %q, want nothing", s)
				}
			case "Run by hand in a terminal":
				if oc.Exit != 2 || oc.Stream != "stderr" {
					t.Fatalf("spec case changed: %+v", oc)
				}
				defer force(true)()
				var out, errb bytes.Buffer
				code := run([]string{"serve"}, envFrom(env), strings.NewReader(""), &out, quiet(&errb))
				if code != oc.Exit || out.Len() != 0 || errb.String() != oc.Text+"\n" {
					t.Errorf("exit %d stdout %q stderr %q, spec exit %d stderr %q", code, out.String(), errb.String(), oc.Exit, oc.Text+"\n")
				}
			default:
				t.Errorf("serve case %q is not tested", oc.Case)
			}
		})
	}
}
