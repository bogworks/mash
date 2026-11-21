package main

import "testing"

// The slash form of an address is refused: exit 2, the specification's line on standard error, nothing on standard output.
func TestSlashFormIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, out, errb string
		args            []string
	}{
		{"who", "", "error: acme/demo is not an owner, an owner.group or an address\nhint: mash who --help\n",
			[]string{"who", "acme/demo"}},
		{"send", "", "error: acme/demo/bob is not an address; write it as acme.demo.bob\nhint: mash send --help\n",
			[]string{"send", "acme/demo/bob", "hi"}},
		{"who json", `{"error":"acme/demo is not an owner, an owner.group or an address","hint":"mash who --help"}` + "\n", "",
			[]string{"who", "acme/demo", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errb, code := runCapture(plainEnv(t, "tls://127.0.0.1:1"), tc.args...)
			if code != 2 || out != tc.out || errb != tc.errb {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit 2, stdout %q, stderr %q", code, out, errb, tc.out, tc.errb)
			}
		})
	}
}
