package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSetupPort(t *testing.T) {
	for _, c := range []struct {
		name, out string
		want      int
		wantErr   bool
	}{
		{"plain", "checked: nats-server 2.10.7, nats 0.1.5, openssl\nport: 4222\nmade: CA\n", 4222, false},
		{"another port with a note", "checked: x\nport: 4224 (4222 is used by another server, left alone)\nmade: CA\n", 4224, false},
		{"no line", "checked: x\n", 0, true},
		{"not at the start of a line", "the port: 4222\n", 0, true},
		{"out of range", "port: 70000\n", 0, true},
		{"not a number", "port: abc\n", 0, true},
	} {
		got, err := parseSetupPort(c.out)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %d, %v; want %d (err %v)", c.name, got, err, c.want, c.wantErr)
		}
	}
}

func TestParsePIDFile(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     int
		wantErr  bool
	}{
		{"one line", "12345\n", 12345, false},
		{"no newline", "77", 77, false},
		{"first line only", "42\nother\n", 42, false},
		{"empty", "", 0, true},
		{"text", "nats-server\n", 0, true},
		{"pid 1 is never ours", "1\n", 0, true},
		{"negative", "-5\n", 0, true},
	} {
		got, err := parsePIDFile([]byte(c.in))
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %d, %v; want %d (err %v)", c.name, got, err, c.want, c.wantErr)
		}
	}
}

func TestSetupFileRender(t *testing.T) {
	f := SetupFile{
		Servers: []string{"tls://127.0.0.1:14222", "tls://127.0.0.1:14223", "tls://127.0.0.1:14224"},
		CA:      "/r/M1/tls/ca.pem", Owner: "alice", AdminCreds: "/r/M1/server/creds/mashadmin.creds",
		SystemCreds: "/r/M1/server/init-in/sys.creds", OperatorKey: "/r/M1/server/init-in/operator.nk", AccountKey: "/r/M1/server/init-in/account.nk",
	}
	want := `servers: ["tls://127.0.0.1:14222", "tls://127.0.0.1:14223", "tls://127.0.0.1:14224"]
ca: "/r/M1/tls/ca.pem"
owner: alice
admin_creds: "/r/M1/server/creds/mashadmin.creds"
system_creds: "/r/M1/server/init-in/sys.creds"
operator_key: "/r/M1/server/init-in/operator.nk"
account_key: "/r/M1/server/init-in/account.nk"
`
	if got := f.Render(); got != want {
		t.Errorf("setup file:\n%s\nwant:\n%s", got, want)
	}
	if got := yamlQuote(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("quote: %s", got)
	}
}

func TestSetupArgsAndScriptPath(t *testing.T) {
	r := &Run{Repo: "/repo"}
	if got := r.scriptPath(); got != "/repo/scripts/mash-local-nats-setup.sh" {
		t.Errorf("default script path %q", got)
	}
	r.SetupScript = "/elsewhere/setup.sh"
	got := strings.Join(r.setupArgs("/run/server", "--owner", "alice"), " ")
	if got != "bash /elsewhere/setup.sh --dir /run/server --owner alice" {
		t.Errorf("args %q", got)
	}
}

// The harness takes the system account and the resolver_preload block out of the script's own server.conf: the
// heredoc in the checked-in script must still have the shape templateParts reads.
func TestTemplatePartsReadsTheScriptsServerConf(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "mash-local-nats-setup.sh"))
	if err != nil {
		t.Skip("the setup script is not next to the harness: " + err.Error())
	}
	text := string(b)
	const open = "cat >server.conf <<CONF\n"
	i := strings.Index(text, open)
	if i < 0 {
		t.Fatal("the script has no server.conf heredoc")
	}
	body := text[i+len(open):]
	body = body[:strings.Index(body, "\nCONF\n")+1]
	body = strings.NewReplacer("$SYSPUB", "ADSYS", "$ACCPUB", "ADACC", "$SYSJWT", "jwt-one", "$ACCJWT", "jwt-two", "$DIR", "/x", "$PORT", "4222").Replace(body)
	sys, pre, err := templateParts(body)
	if err != nil || sys != "ADSYS" {
		t.Fatalf("templateParts on the script's server.conf: %q, %v", sys, err)
	}
	if !strings.Contains(pre, `ADSYS: "jwt-one"`) || !strings.Contains(pre, `ADACC: "jwt-two"`) || !strings.HasSuffix(pre, "}\n") {
		t.Errorf("preload block: %q", pre)
	}
}
