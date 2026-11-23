package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// A folder that holds something different is refused in one form for each of the six things init never replaces: the
// text rendering wraps the line at 80 columns (the folder here is ~/.config/mash, as in the specification), and the
// --json message stays on one line.
func TestInitRefusesWhatTheFolderHoldsDifferentlyInOneForm(t *testing.T) {
	e := newApplyEnv(t)
	for _, tc := range []struct {
		what  string
		setup func(a *initer)
	}{
		{"operator key", func(a *initer) {
			ks, err := ctlkeys.Open(a.env["MASH_KEYS_DIR"], "")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ks.InitOperator(); err != nil {
				t.Fatal(err)
			}
		}},
		{"account key", func(a *initer) {
			ks, err := ctlkeys.Open(a.env["MASH_KEYS_DIR"], "")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ks.InitAccount(); err != nil {
				t.Fatal(err)
			}
		}},
		{"server", func(a *initer) {
			writeHeldConfig(t, a, map[string]any{"servers": []string{"tls://other.example:4222"}})
		}},
		{"system creds", func(a *initer) {
			sd := filepath.Join(a.env["MASH_CONFIG_DIR"], "server")
			if err := os.MkdirAll(sd, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sd, "sys.creds"), []byte("other"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"admin creds", func(a *initer) { writeHeldConfig(t, a, map[string]any{"admin_creds": "/elsewhere/admin.creds"}) }},
		{"owner", func(a *initer) { writeHeldConfig(t, a, map[string]any{"owner": "carol"}) }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			a := newIniter(t, e)
			a.env["MASH_CONFIG_DIR"] = filepath.Join(a.root, ".config", "mash")
			tc.setup(a)
			before := tree(t, a.root)
			line := "~/.config/mash was set up with a different " + tc.what + "; nothing was changed"
			hint := "mashctl init --config <file> --config-dir <a new folder>"

			_, errb, code := a.init()
			want := "error: " + line + "\nhint: " + hint + "\n"
			if len("error: "+line) > 80 { // only a line over 80 columns wraps
				first, second, _ := strings.Cut(line, "; ")
				want = "error: " + first + ";\n       " + second + "\nhint: " + hint + "\n"
			}
			if code != 1 || errb != want {
				t.Errorf("text: exit %d, stderr %q\nwant %q", code, errb, want)
			}
			for _, l := range strings.Split(errb, "\n") {
				if len([]rune(l)) > 80 {
					t.Errorf("line over 80 columns: %q", l)
				}
			}
			sameTree(t, before, tree(t, a.root))

			out, errb, code := a.init("--json")
			var obj map[string]string
			if err := json.Unmarshal([]byte(out), &obj); err != nil || strings.Count(out, "\n") != 1 || obj["error"] != line || obj["hint"] != hint || errb != "" || code != 1 {
				t.Errorf("json: exit %d, stdout %q, stderr %q (%v)\nwant error %q", code, out, errb, err, line)
			}
			sameTree(t, before, tree(t, a.root))
		})
	}
}

// writeHeldConfig puts a config.json in the folder: the settings init would write, with the given ones different.
func writeHeldConfig(t *testing.T, a *initer, differ map[string]any) {
	t.Helper()
	held := map[string]any{
		"servers": []string{a.src.srv.ClientURL()}, "owner": "alice", "admin_creds": a.admin,
		"system_creds": filepath.Join(a.env["MASH_CONFIG_DIR"], "server", "sys.creds"),
	}
	for k, v := range differ {
		held[k] = v
	}
	b, err := json.Marshal(held)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.env["MASH_CONFIG_DIR"], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
