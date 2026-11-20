package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusAllOK(t *testing.T) {
	good := `{"servers":[{"server":"tls://a:1","ok":true},{"server":"tls://b:1","ok":true},{"server":"tls://c:1","ok":true}]}`
	if ok, d := statusAllOK(0, good, 3); !ok {
		t.Fatal(d)
	}
	down := `{"servers":[{"server":"tls://a:1","ok":true},{"server":"tls://b:1","ok":false,"reason":"the server did not answer"},{"server":"tls://c:1","ok":true}]}`
	if ok, d := statusAllOK(1, down, 3); ok || !strings.Contains(d, "tls://b:1 not ok: the server did not answer") {
		t.Fatal(ok, d)
	}
	for name, c := range map[string]struct {
		code int
		out  string
	}{
		"one server only": {0, `{"server":"tls://a:1","ok":true}`},
		"two entries":     {0, `{"servers":[{"server":"a","ok":true},{"server":"b","ok":true}]}`},
		"duplicate":       {0, `{"servers":[{"server":"a","ok":true},{"server":"a","ok":true},{"server":"c","ok":true}]}`},
		"not json":        {0, `nope`},
		"exit 1":          {1, good},
	} {
		if ok, _ := statusAllOK(c.code, c.out, 3); ok {
			t.Errorf("%s: passed", name)
		}
	}
}

func TestSetupScriptLine(t *testing.T) {
	b := []byte("echo hi\n")
	want := fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	l := setupScriptLine("/repo", "/repo/scripts/x.sh", b)
	if l != "setup script: scripts/x.sh "+want {
		t.Fatal(l)
	}
	if l := setupScriptLine("/repo", "/elsewhere/y.sh", b); l != "setup script: y.sh "+want {
		t.Fatal(l)
	}
	if strings.ContainsAny(setupScriptLine("/repo", "/repo/a|b.sh", b), "|\n") {
		t.Fatal("line holds a separator")
	}
}

func TestHasRecord(t *testing.T) {
	d := t.TempDir()
	if hasRecord(d) {
		t.Fatal("empty dir")
	}
	os.WriteFile(filepath.Join(d, "run.log"), nil, 0o600)
	os.Mkdir(filepath.Join(d, "record-x.md"), 0o700)
	if hasRecord(d) {
		t.Fatal("no record file")
	}
	os.WriteFile(filepath.Join(d, "record-AT-001.md"), []byte("x"), 0o600)
	if !hasRecord(d) {
		t.Fatal("record present")
	}
	if hasRecord(filepath.Join(d, "missing")) {
		t.Fatal("missing dir")
	}
}

func TestExportFailCode(t *testing.T) {
	if exportFailCode(0) != 1 || exportFailCode(4) != 4 || exportFailCode(1) != 1 {
		t.Fatal("exportFailCode")
	}
}

func TestInContainerSignals(t *testing.T) {
	for _, c := range []struct {
		env  bool
		rev  string
		want bool
	}{{true, "abc", true}, {true, "", false}, {false, "abc", false}, {false, "", false}} {
		if got := inContainerSignals(c.env, c.rev); got != c.want {
			t.Errorf("%v: %v", c, got)
		}
	}
}
