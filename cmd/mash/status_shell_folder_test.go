package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/spool"
)

// specDoctorShellFolder is the spec's text for a shell whose state folder cannot be created, with the example's server,
// endpoint and owner swapped for the test's own (the path keeps its shape: ~/.local/state/mash/spool/<owner>/<name>).
func specDoctorShellFolder(t *testing.T, w *world) string {
	t.Helper()
	return specDoctorShellFolderCase(t, w, "At a shell, when the state folder cannot be created")
}

// specDoctorShellFolderCase is the same for the named spec case.
func specDoctorShellFolderCase(t *testing.T, w *world, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "mash.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var find func(v any) string
	find = func(v any) string {
		switch x := v.(type) {
		case map[string]any:
			if x["case"] == name {
				s, _ := x["text"].(string)
				return s
			}
			for _, c := range x {
				if s := find(c); s != "" {
					return s
				}
			}
		case []any:
			for _, c := range x {
				if s := find(c); s != "" {
					return s
				}
			}
		}
		return ""
	}
	text := find(doc)
	if text == "" {
		t.Fatalf("the spec has no case %q", name)
	}
	const specPath = "~/.local/state/mash/spool/acme/acme.demo.bob"
	if !strings.Contains(text, specPath) {
		t.Fatalf("spec text %q does not show the spool folder %s", text, specPath)
	}
	text = strings.ReplaceAll(text, specPath, "~/.local/state/mash/spool/"+w.e.Owner+"/"+tAddr("a").Flat())
	return strings.Replace(text, "tls://myserver:4222", w.e.URL, 1)
}

// breakStateFolder puts a regular file where the state folder should be and returns the spool folder it breaks.
func breakStateFolder(t *testing.T, w *world) spool.Spool {
	t.Helper()
	sp := spoolFor(w, "a")
	// the state folder is three levels above the spool folder (<state>/spool/<owner>/<name>), whatever sets its place
	state := filepath.Dir(filepath.Dir(filepath.Dir(sp.Dir)))
	if filepath.Base(filepath.Dir(filepath.Dir(sp.Dir))) != "spool" {
		t.Fatalf("spool folder %s is not under <state>/spool/<owner>/<name>", sp.Dir)
	}
	if err := os.Rename(state, state+".moved"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	// mode 0700: a file with write and search bits still blocks the path
	if err := os.WriteFile(state, []byte("not a folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	return sp
}

// At a plain shell, with no session and so no record, mash status and mash doctor still see a state folder that cannot
// be created, by asking the system: here a regular file sits where the state folder should be. They say so only where
// mail waits for the spool (MASH_SPOOL_FROM_START, which the plugin sets, or MASH_SPOOL_REQUIRED): otherwise mail stays
// on the channel and is still delivered.
func TestShellStatusDoctorSeeAStateFolderThatCannotBeCreated(t *testing.T) {
	for _, setting := range []string{"MASH_SPOOL_FROM_START", "MASH_SPOOL_REQUIRED"} {
		t.Run(setting, func(t *testing.T) {
			w := tlsWorld(t, "a")
			w.env["a"][setting] = "1"
			t.Setenv("HOME", w.env["a"]["HOME"]) // Tilde reads it: the doctor row prints the folder as ~/...
			t.Run("normal folder is unchanged", func(t *testing.T) {
				if out, code := w.cli(t, "a", "status"); code != 0 || strings.Contains(out, cmds.ReasonMailHeld) {
					t.Errorf("status %d:\n%s", code, out)
				}
				if out, code := w.cli(t, "a", "doctor"); code != 0 || strings.Contains(out, cmds.CannotWrite) {
					t.Errorf("doctor %d:\n%s", code, out)
				}
			})
			breakStateFolder(t, w)
			t.Run("a file in the way", func(t *testing.T) {
				if out, code := w.cli(t, "a", "status"); code == 0 || !strings.Contains(out, cmds.ReasonMailHeld) {
					t.Errorf("status %d:\n%s", code, out)
				}
				out, code := w.cli(t, "a", "doctor")
				if want := specDoctorShellFolder(t, w) + "\n"; code != 1 || out != want {
					t.Errorf("doctor %d:\n%s\nwant the spec text:\n%s", code, out, want)
				}
			})
		})
	}
}

// With neither setting visible to the shell, mail stays on the channel and is delivered unless a session sets
// MASH_SPOOL_FROM_START (the plugin does), so a shell cannot tell: status stays ready and doctor warns instead of failing.
func TestShellDoctorWarnsOfAStateFolderThatCannotBeCreatedWhenNoSettingIsVisible(t *testing.T) {
	w := tlsWorld(t, "a")
	t.Setenv("HOME", w.env["a"]["HOME"])
	t.Run("writable folder keeps the skip row", func(t *testing.T) {
		out, code := w.cli(t, "a", "doctor")
		hasSkipRow := strings.Contains(out, "delivery     skip  checked only inside a Claude Code session") ||
			strings.Contains(out, "delivery  skip  checked only inside a Claude Code session")
		if code != 0 || strings.Contains(out, cmds.CannotWrite) || !hasSkipRow {
			t.Errorf("doctor %d:\n%s", code, out)
		}
	})
	breakStateFolder(t, w)
	out, code := w.cli(t, "a", "status")
	if code != 0 || strings.Contains(out, cmds.ReasonMailHeld) {
		t.Errorf("status %d:\n%s", code, out)
	}
	out, code = w.cli(t, "a", "doctor")
	want := specDoctorShellFolderCase(t, w, "At a shell, when the state folder cannot be created and no spool setting is visible") + "\n"
	if code != 0 || out != want {
		t.Errorf("doctor %d:\n%s\nwant the spec text:\n%s", code, out, want)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "delivery") && len(l) > 80 {
			t.Errorf("row over 80 columns: %q", l)
		}
	}
	out, code = w.cli(t, "a", "doctor", "--strict")
	if code != 1 {
		t.Errorf("doctor --strict %d:\n%s", code, out)
	}
	if out, _ := w.cli(t, "a", "doctor", "--json"); !strings.Contains(out, `"warnings": 1`) && !strings.Contains(out, `"warnings":1`) {
		t.Errorf("doctor --json does not count one warning:\n%s", out)
	}
}
