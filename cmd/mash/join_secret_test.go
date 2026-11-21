//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/bogworks/mash/internal/joinfile"
)

func othersText(f string) string {
	return fmt.Sprintf("error: %s can be read by others; a join file that holds a seed must be readable by you alone\nhint: chmod 600 %s\n", f, f)
}

func notPlainText(f string) string {
	return fmt.Sprintf("error: %s is not a plain file owned by you; nothing was installed\nhint: copy it into a file of your own, then: chmod 600 <that file>\n", f)
}

func TestJoinFileWithASeedMustBeOwnerOnly(t *testing.T) {
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666} {
		w := newJoinWorld(t)
		os.Chmod("acme.demo.bob.join", mode)
		r := w.run("join", "acme.demo.bob.join")
		if r.code != 1 || r.out != "" || r.errs != othersText("acme.demo.bob.join") {
			t.Errorf("mode %o: %+v", mode, r)
		}
		if _, err := os.Stat(w.cfg); err == nil {
			t.Errorf("mode %o: the folder was made", mode)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		w := newJoinWorld(t)
		os.Chmod("acme.demo.bob.join", mode)
		if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
			t.Errorf("mode %o: %+v", mode, r)
		}
	}
}

func TestJoinFileThatIsNotPlain(t *testing.T) {
	w := newJoinWorld(t)
	os.Rename("acme.demo.bob.join", "real.join")
	os.Symlink("real.join", "link.join")
	os.Mkdir("dir.join", 0o700)
	if err := syscall.Mkfifo("fifo.join", 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"link.join", "dir.join", "fifo.join"} {
		r := w.run("join", f)
		if r.code != 1 || r.out != "" || r.errs != notPlainText(f) {
			t.Errorf("%s: %+v", f, r)
		}
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
	if r := w.run("join", "real.join"); r.code != 0 {
		t.Errorf("the plain file: %+v", r)
	}
}

func TestJoinFileOfAnotherOwnerIsRefused(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file to another user needs root")
	}
	w := newJoinWorld(t)
	os.Chown("acme.demo.bob.join", 12345, 12345)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 1 || r.errs != notPlainText("acme.demo.bob.join") {
		t.Errorf("%+v", r)
	}
}

func TestJoinFileWithOnlyAUserJWTIsNotChecked(t *testing.T) {
	w := newJoinWorld(t)
	writeJoin(t, "jwt.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
	os.Chmod("jwt.join", 0o644)
	os.WriteFile("bob.seed", w.m.seed, 0o600)
	if r := w.run("join", "jwt.join", "--key", "bob.seed"); r.code != 0 {
		t.Errorf("%+v", r)
	}
	// through a symlink too: there is no seed in the file
	w = newJoinWorld(t)
	writeJoin(t, "jwt.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
	os.Chmod("jwt.join", 0o644)
	os.WriteFile("bob.seed", w.m.seed, 0o600)
	os.Symlink("jwt.join", "jwt-link.join")
	if r := w.run("join", "jwt-link.join", "--key", "bob.seed"); r.code != 0 {
		t.Errorf("%+v", r)
	}
}

func TestJoinKeyFileMustBeOwnerOnly(t *testing.T) {
	w := newJoinWorld(t)
	writeJoin(t, "jwt.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
	os.WriteFile("bob.seed", w.m.seed, 0o600)
	os.Chmod("bob.seed", 0o644)
	r := w.run("join", "jwt.join", "--key", "bob.seed")
	if r.code != 1 || r.out != "" || r.errs != othersText("bob.seed") {
		t.Errorf("0644: %+v", r)
	}
	os.Symlink("bob.seed", "bob-link.seed")
	os.Chmod("bob.seed", 0o600)
	r = w.run("join", "jwt.join", "--key", "bob-link.seed")
	if r.code != 1 || r.errs != notPlainText("bob-link.seed") {
		t.Errorf("symlink: %+v", r)
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
	if r := w.run("join", "jwt.join", "--key", "bob.seed"); r.code != 0 {
		t.Errorf("0600: %+v", r)
	}
}

// A folder that holds an endpoint and has no ca.pem was set up without a certificate authority: a join file that brings
// one is refused, with or without --replace, and one that brings none is fine. An empty folder takes the first one.
func TestJoinRefusesACAIntoAFolderWithEndpointsAndNone(t *testing.T) {
	want := "error: ~/.config/mash was set up with a different ca;\n       nothing was changed\nhint: mash join %s --config-dir <another folder>\n"
	for _, replace := range []bool{false, true} {
		w := newJoinWorld(t)
		os.MkdirAll(w.cfg, 0o700)
		os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"servers":["tls://myserver:4222"]}`), 0o600)
		os.WriteFile(w.credsFile(), w.m.creds, 0o600)
		before := treeSnapshot(t, w.cfg)
		args := []string{"join", "acme.demo.bob.join"}
		if replace {
			args = append(args, "--replace")
		}
		r := w.run(args...)
		if r.code != 1 || r.out != "" || r.errs != fmt.Sprintf(want, "acme.demo.bob.join") {
			t.Errorf("%v: %+v", args, r)
		}
		if treeSnapshot(t, w.cfg) != before {
			t.Errorf("%v changed the folder", args)
		}
		// no CA in the join file: fine
		w.joinFor("none.join", w.m, "acme.demo.bob", nil)
		if r := w.run(append(args[:1:1], "none.join", "--replace")...); r.code != 0 {
			t.Errorf("no CA, replace: %+v", r)
		}
	}
	// the first join into an empty folder sets the CA
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err != nil {
		t.Error("the first join did not set the CA")
	}
	// a folder with no endpoint yet and no ca.pem takes a CA
	w = newJoinWorld(t)
	os.MkdirAll(w.cfg, 0o700)
	os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"servers":["tls://myserver:4222"]}`), 0o600)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Errorf("no endpoint yet: %+v", r)
	}
}

// Only an installed endpoint (owner.group.name.creds, mode 0600) makes a folder one that was set up without a CA. An
// admin.creds, or a creds file that is not 0600, is not one, so the first join still brings its CA.
func TestJoinTakesACAIntoAFolderWithoutAnInstalledEndpoint(t *testing.T) {
	for _, c := range []struct {
		name string
		mode os.FileMode
	}{
		{"admin.creds", 0o600},
		{"acme.demo.carol.creds", 0o644},
	} {
		w := newJoinWorld(t)
		os.MkdirAll(w.cfg, 0o700)
		p := filepath.Join(w.cfg, c.name)
		if err := os.WriteFile(p, w.m.creds, c.mode); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, c.mode)
		r := w.run("join", "acme.demo.bob.join")
		if r.code != 0 {
			t.Errorf("%s %v: %+v", c.name, c.mode, r)
		}
		if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err != nil {
			t.Errorf("%s %v: the join did not write ca.pem", c.name, c.mode)
		}
	}
}
