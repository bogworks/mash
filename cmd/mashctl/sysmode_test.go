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
	for _, mode := range []os.FileMode{0o644, 0o640} {
		p := write("loose.creds", mode)
		e.env["MASH_SYS_CREDS"] = p
		out, errb, code := e.run("policy", "diff")
		if code != 1 || !strings.Contains(errb, "system account credentials") || !strings.Contains(errb, "chmod 600 "+p) {
			t.Fatalf("mode %04o: %d %q %q", mode, code, out, errb)
		}
		if strings.Contains(errb, string(src[:20])) {
			t.Fatal("the refusal printed creds content")
		}
		// the flag form is checked too
		if _, errb, code := e.run("policy", "diff", "--sys-creds", p); code != 1 || !strings.Contains(errb, "chmod 600 "+p) {
			t.Fatalf("--sys-creds mode %04o: %d %q", mode, code, errb)
		}
		// so is every other command that connects with them
		if dout, errb, code := e.run("policy", "drift", "--sys-creds", p); code == 0 || !strings.Contains(dout+errb, "chmod 600 "+p) {
			t.Fatalf("drift mode %04o: %d %q %q", mode, code, dout, errb)
		}
	}

	ok := write("ok.creds", 0o600)
	e.env["MASH_SYS_CREDS"] = ok
	if out, errb, code := e.run("policy", "diff"); code != 0 {
		t.Fatalf("0600: %d %q %q", code, out, errb)
	}

	link := filepath.Join(dir, "link.creds")
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	e.env["MASH_SYS_CREDS"] = link
	if out, errb, code := e.run("policy", "diff"); code != 0 {
		t.Fatalf("symlink to 0600: %d %q %q", code, out, errb)
	}

	missing := filepath.Join(dir, "nope.creds")
	e.env["MASH_SYS_CREDS"] = missing
	_, errb, code := e.run("policy", "diff")
	if code != 1 || !strings.Contains(errb, "system account credentials") || !strings.Contains(errb, "no such file") || strings.Contains(errb, "chmod") {
		t.Fatalf("missing: %d %q", code, errb)
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
