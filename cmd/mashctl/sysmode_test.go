//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/bootstrap"
)

// TestSysCredsModeIsChecked: system account credentials are held to the same mode check as admin and endpoint
// credentials (no group or other bits, a symlink is followed), and a missing file keeps its own error.
func TestSysCredsModeIsChecked(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	src, err := os.ReadFile(filepath.Join(e.pl.Dir, bootstrap.SysCreds))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, src, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// `policy drift` is the verb that reads only the system credentials (the others also need the admin connection):
	// it reports the account as UNKNOWN with the credentials error in its output.
	drift := func(args ...string) (string, int) {
		out, errb, code := e.run(append([]string{"policy", "drift"}, args...)...)
		return out + errb, code
	}
	for _, mode := range []os.FileMode{0o644, 0o640} {
		p := write("loose.creds", mode)
		e.env["MASH_SYS_CREDS"] = p
		all, code := drift()
		if code == 0 || !strings.Contains(all, "system account credentials") || !strings.Contains(all, "chmod 600 "+p) {
			t.Fatalf("mode %04o: %d %q", mode, code, all)
		}
		if strings.Contains(all, string(src[:20])) {
			t.Fatal("the refusal printed creds content")
		}
		// the flag form is checked too
		if all, code := drift("--sys-creds", p); code == 0 || !strings.Contains(all, "chmod 600 "+p) {
			t.Fatalf("--sys-creds mode %04o: %d %q", mode, code, all)
		}
	}

	ok := write("ok.creds", 0o600)
	e.env["MASH_SYS_CREDS"] = ok
	if all, _ := drift(); strings.Contains(all, "chmod") || strings.Contains(all, "system account credentials") {
		t.Fatalf("0600: %q", all)
	}

	link := filepath.Join(dir, "link.creds")
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_SYS_CREDS"] = link
	if all, _ := drift(); strings.Contains(all, "chmod") || strings.Contains(all, "system account credentials") {
		t.Fatalf("symlink to 0600: %q", all)
	}

	missing := filepath.Join(dir, "nope.creds")
	e.env["MASH_SYS_CREDS"] = missing
	all, code := drift()
	if code == 0 || !strings.Contains(all, "system account credentials") || !strings.Contains(all, "no such file") || strings.Contains(all, "chmod") {
		t.Fatalf("missing: %d %q", code, all)
	}
}

// TestReadCredsRefusesLooseModes: the reader the audit and reissue paths use (credsUserKey) refuses a loose file.
func TestReadCredsRefusesLooseModes(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	src, err := os.ReadFile(filepath.Join(e.pl.Dir, bootstrap.SysCreds))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "sys.creds")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credsUserKey(p); err != nil {
		t.Fatalf("0600: %v", err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := credsUserKey(p); err == nil || !strings.Contains(err.Error(), "chmod 600 "+p) {
		t.Fatalf("0644: %v", err)
	}
}
