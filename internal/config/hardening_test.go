package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain text é ü 日本", "plain text é ü 日本"},
		{"a\x1b[2Kb", `a\x1b[2Kb`},
		{"a\rb\nc\td", `a\x0db\x0ac\x09d`},
		{"x\x7fy", `x\x7fy`},
		{"x\u009by", `x\u009by`},
		{"bad\xffbyte", `bad\xffbyte`},
	} {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := Clean(strings.Repeat("a", 1000))
	if long != strings.Repeat("a", CleanMax)+"..." {
		t.Fatalf("long: %d bytes", len(long))
	}
	if Clean(strings.Repeat("a", CleanMax)) != strings.Repeat("a", CleanMax) {
		t.Fatal("exactly the cap must be unchanged")
	}
}

func TestRedactURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"nats://user:pass@host:4222", "nats://***@host:4222"},
		{"tls://tok@host", "tls://***@host"},
		{"tls://host:4222", "tls://host:4222"},
		{"host:4222", "host:4222"},
		{"nats://u:p@ss@host/path@x", "nats://***@host/path@x"},
		{"nats://u:p%zz@host", "nats://***@host"},
		{"nats://host/a@b", "nats://host/a@b"},
		{"", ""},
		{"user:pass@h:4222", "***@h:4222"},
		{"tok@h", "***@h"},
		{"h:4222", "h:4222"},
		{"h:4222/a@b", "h:4222/a@b"},
		{"u:p@h?x=y@z", "***@h?x=y@z"},
		{"nats://u:p@h,u2:p2@h2", "nats://***@h,***@h2"},
		{"h1:4222, u:p@h2:4222,tok@h3", "h1:4222, ***@h2:4222,***@h3"},
	} {
		if got := RedactURL(c.in); got != c.want {
			t.Errorf("RedactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !HasUserinfo("u:p@h:4222") || !HasUserinfo("a:1,tok@b:2") || HasUserinfo("a:1,b:2") {
		t.Fatal("HasUserinfo scheme-less")
	}
	e := RedactErr(errors.New("dial u:p@h:4222: refused; dial tok@h2: refused"), "u:p@h:4222,tok@h2")
	if strings.Contains(e.Error(), "u:p") || strings.Contains(e.Error(), "tok") {
		t.Fatalf("RedactErr leaks: %v", e)
	}
	if e := RedactErr(errors.New("dial u:p@h:4222"), "u:p@h:4222"); strings.Contains(e.Error(), "u:p") {
		t.Fatalf("RedactErr leaks: %v", e)
	}
	if !HasUserinfo("nats://u:p@h") || !HasUserinfo("nats://u:p%zz@h") || HasUserinfo("tls://h:4222") {
		t.Fatal("HasUserinfo")
	}
}

func TestRelativePathsResolveAgainstConfigDir(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o","admin_creds":"admin.creds","tls_ca":"ca.pem","tls_cert":"c.pem","tls_key":"k.pem"}`)
	t.Chdir(t.TempDir())
	c, err := load(envOf(nil), home)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "mash")
	if c.AdminCreds != filepath.Join(dir, "admin.creds") || c.TLSCA != filepath.Join(dir, "ca.pem") ||
		c.TLSCert != filepath.Join(dir, "c.pem") || c.TLSKey != filepath.Join(dir, "k.pem") {
		t.Fatalf("got %q %q %q %q", c.AdminCreds, c.TLSCA, c.TLSCert, c.TLSKey)
	}
}

func TestAbsolutePathsKept(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o","admin_creds":"/etc/a.creds","tls_ca":"/etc/ca.pem"}`)
	c, err := load(envOf(nil), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminCreds != "/etc/a.creds" || c.TLSCA != "/etc/ca.pem" {
		t.Fatalf("got %q %q", c.AdminCreds, c.TLSCA)
	}
}

func TestWorldWritableConfigRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	for _, c := range []struct {
		mode    os.FileMode
		refused bool
	}{{0o600, false}, {0o644, false}, {0o664, false}, {0o666, true}, {0o602, true}} {
		home := t.TempDir()
		writeFile(t, home, `{"url":"tls://x:4222","owner":"o"}`)
		p := filepath.Join(home, ".config", "mash", "config.json")
		if err := os.Chmod(p, c.mode); err != nil {
			t.Fatal(err)
		}
		_, err := load(envOf(nil), home)
		if c.refused {
			if err == nil || !strings.Contains(err.Error(), "chmod go-w "+p) {
				t.Errorf("mode %04o: want a refusal with a chmod hint, got %v", c.mode, err)
			}
		} else if err != nil {
			t.Errorf("mode %04o: %v", c.mode, err)
		}
	}
}
