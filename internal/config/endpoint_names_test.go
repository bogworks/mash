package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The setting names of the hard cut: MASH_ENDPOINT / "endpoint", MASH_SERVER / "servers", MASH_CA / "ca".

func TestEndpointSettingSetsOwnerGroupName(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222","endpoint":"file.fg.fname"}`)
	c, err := load(envOf(nil), home)
	if err != nil || c.Owner != "file" || c.Group != "fg" || c.Name != "fname" || c.Source["endpoint"] != "config.json" {
		t.Fatalf("file: %+v %v", c, err)
	}
	c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "alice.dev.design"}), home)
	if err != nil || c.Owner != "alice" || c.Group != "dev" || c.Name != "design" || c.Source["endpoint"] != "MASH_ENDPOINT" ||
		c.FileValues["endpoint"] != "file.fg.fname" {
		t.Fatalf("env: %+v %v", c, err)
	}
	if c.Creds != filepath.Join(c.Dir, "alice.dev.design.creds") {
		t.Errorf("creds %q", c.Creds)
	}
}

func TestEndpointErrorsNameTheSetting(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222","endpoint":"Bad.g.a"}`)
	_, err := load(envOf(nil), home)
	if err == nil || !strings.Contains(err.Error(), `config.json "endpoint"`) || !strings.Contains(err.Error(), "must be lower-case") {
		t.Fatalf("file: %v", err)
	}
	_, err = load(envOf(map[string]string{"MASH_ENDPOINT": "x.y"}), home)
	if err == nil || !strings.Contains(err.Error(), "MASH_ENDPOINT") || !strings.Contains(err.Error(), "want exactly three") {
		t.Fatalf("env: %v", err)
	}
}

func TestServersSettingForms(t *testing.T) {
	for name, tc := range map[string]struct {
		file, env, want string
	}{
		"file string":        {`"servers":"tls://a:1"`, "", "tls://a:1"},
		"file comma string":  {`"servers":"tls://a:1, tls://b:2"`, "", "tls://a:1,tls://b:2"},
		"file array":         {`"servers":["tls://a:1","tls://b:2"]`, "", "tls://a:1,tls://b:2"},
		"file array commas":  {`"servers":["tls://a:1,tls://b:2","tls://c:3"]`, "", "tls://a:1,tls://b:2,tls://c:3"},
		"env comma list":     {``, "tls://a:1,tls://b:2", "tls://a:1,tls://b:2"},
		"env beats file":     {`"servers":["tls://f:1"]`, "tls://e:1", "tls://e:1"},
		"env empty items":    {``, " tls://a:1 ,, tls://b:2,", "tls://a:1,tls://b:2"},
		"file empty array":   {`"servers":[]`, "", ""},
		"nothing configured": {``, "", ""},
	} {
		home := t.TempDir()
		body := `{` + tc.file + `}`
		writeFile(t, home, body)
		c, err := load(envOf(map[string]string{"MASH_SERVER": tc.env}), home)
		if err != nil || c.URL != tc.want {
			t.Errorf("%s: URL %q, %v; want %q", name, c.URL, err, tc.want)
		}
	}
	home := t.TempDir()
	writeFile(t, home, `{"servers":["tls://f:1"]}`)
	c, _ := load(envOf(map[string]string{"MASH_SERVER": "tls://e:1"}), home)
	if c.Source["servers"] != "MASH_SERVER" || c.FileValues["servers"] != "tls://f:1" {
		t.Errorf("provenance %v %v", c.Source, c.FileValues)
	}
}

func TestServersFileMustBeStringOrArrayOfStrings(t *testing.T) {
	for _, bad := range []string{`{"servers":5}`, `{"servers":[1]}`, `{"servers":{"a":1}}`, `{"servers":true}`} {
		if err := ValidateFile([]byte(bad)); err == nil || !strings.Contains(err.Error(), `"servers"`) {
			t.Errorf("ValidateFile(%s) = %v", bad, err)
		}
		home := t.TempDir()
		writeFile(t, home, bad)
		if _, err := load(envOf(nil), home); err == nil || !strings.Contains(err.Error(), `"servers"`) {
			t.Errorf("Load(%s) = %v", bad, err)
		}
	}
	for _, good := range []string{`{"servers":"a"}`, `{"servers":["a","b"]}`, `{"servers":[]}`, `{}`} {
		if err := ValidateFile([]byte(good)); err != nil {
			t.Errorf("ValidateFile(%s) = %v", good, err)
		}
	}
}

func TestCASetting(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1","ca":"ca.pem"}`)
	c, err := load(envOf(nil), home)
	if want := filepath.Join(home, ".config", "mash", "ca.pem"); err != nil || c.TLSCA != want || c.Source["ca"] != "config.json" {
		t.Fatalf("file: TLSCA %q (want %q) %v %v", c.TLSCA, want, err, c.Source)
	}
	c, err = load(envOf(map[string]string{"MASH_CA": "/e/ca.pem"}), home)
	if err != nil || c.TLSCA != "/e/ca.pem" || c.Source["ca"] != "MASH_CA" {
		t.Fatalf("env: %q %v %v", c.TLSCA, err, c.Source)
	}
}

// The old names are simply not read: no environment variable, no config.json key.
func TestOldSettingNamesAreIgnored(t *testing.T) {
	home := t.TempDir()
	old := `{"url":"tls://old:1","owner":"oldowner","tls_ca":"/old/ca.pem"}`
	writeFile(t, home, old)
	if err := ValidateFile([]byte(old)); err != nil {
		t.Fatalf("an old key is an unknown key, ignored: %v", err)
	}
	c, err := load(envOf(map[string]string{
		"MASH_URL": "tls://envold:1", "MASH_OWNER": "o", "MASH_GROUP": "g", "MASH_NAME": "a", "MASH_TLS_CA": "/envold/ca.pem",
	}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "" || c.Owner != "" || c.Group != "" || c.Name != "" || c.TLSCA != "" || !c.Inert() {
		t.Fatalf("an old name was read: %+v", c)
	}
	for k, v := range c.Source {
		if strings.Contains(v, "MASH_URL") || strings.Contains(v, "MASH_OWNER") || strings.Contains(v, "TLS_CA") {
			t.Errorf("Source[%s] = %q", k, v)
		}
	}
}

func TestErrorTextsNameTheNewSettings(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want []string
	}{
		{ErrNoURL, []string{"no server: set MASH_SERVER, --server, or \"servers\" in config.json"}},
		{ErrNoOwner, []string{"no endpoint: set MASH_ENDPOINT, --endpoint, or install credentials for one endpoint"}},
		{ErrGroupMissing, []string{"MASH_ENDPOINT", "owner.group.name"}},
	} {
		for _, w := range tc.want {
			if !strings.Contains(tc.err.Error(), w) {
				t.Errorf("%q lacks %q", tc.err, w)
			}
		}
		for _, old := range []string{"MASH_URL", "MASH_OWNER", "MASH_GROUP", "MASH_NAME"} {
			if strings.Contains(tc.err.Error(), old) {
				t.Errorf("%q still names %s", tc.err, old)
			}
		}
	}
}

// installCreds writes an installed endpoint's creds file in the config directory under home, with mode m.
func installCreds(t *testing.T, home, name string, m os.FileMode) string {
	t.Helper()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("creds"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, m); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSingleInstalledEndpointIsChosen(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	creds := installCreds(t, home, "alice.dev.design.creds", 0o600)
	c, err := load(envOf(nil), home)
	if err != nil || c.Inert() || c.Owner != "alice" || c.Group != "dev" || c.Name != "design" {
		t.Fatalf("%+v %v", c, err)
	}
	if c.Creds != creds || c.Source["endpoint"] != "installed creds" {
		t.Errorf("creds %q source %v", c.Creds, c.Source)
	}
	if c.FileValues["endpoint"] != "" {
		t.Errorf("FileValues %v", c.FileValues)
	}
}

func TestNoInstalledEndpointIsInert(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	c, err := load(envOf(nil), home)
	if err != nil || !c.Inert() {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSeveralInstalledEndpointsIsATypedError(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1","admin_creds":"/a.creds"}`)
	installCreds(t, home, "bob.dev.review.creds", 0o600)
	installCreds(t, home, "alice.dev.design.creds", 0o600)
	c, err := load(envOf(nil), home)
	var se *SeveralEndpointsError
	if !errors.As(err, &se) || !errors.Is(err, ErrSeveralEndpoints) {
		t.Fatalf("got %v, want *SeveralEndpointsError", err)
	}
	if got, want := err.Error(), "more than one endpoint is installed here: alice.dev.design, bob.dev.review"; got != want {
		t.Errorf("message %q, want %q", got, want)
	}
	if strings.Join(se.Addresses, ",") != "alice.dev.design,bob.dev.review" {
		t.Errorf("Addresses %v", se.Addresses)
	}
	// The rest of the configuration is still resolved, so a caller that does not need an endpoint can go on.
	if c.URL != "tls://x:1" || c.AdminCreds != "/a.creds" || !c.Inert() {
		t.Errorf("partial config %+v", c)
	}
	// Naming one endpoint, by environment or by file, settles it.
	c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "bob.dev.review"}), home)
	if err != nil || c.Name != "review" {
		t.Fatalf("env: %+v %v", c, err)
	}
	writeFile(t, home, `{"servers":"tls://x:1","endpoint":"alice.dev.design"}`)
	if c, err = load(envOf(nil), home); err != nil || c.Name != "design" {
		t.Fatalf("file: %+v %v", c, err)
	}
}

func TestOnlySoundCredsFilesCountAsInstalled(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	dir := filepath.Join(home, ".config", "mash")
	installCreds(t, home, "alice.dev.design.creds", 0o600)
	installCreds(t, home, "bob.dev.loose.creds", 0o644)   // not 0600
	installCreds(t, home, "bob.dev.group.creds", 0o640)   // not 0600
	installCreds(t, home, "bob.dev.ro.creds", 0o400)      // not 0600
	installCreds(t, home, "Carol.dev.upper.creds", 0o600) // not [a-z0-9-]
	installCreds(t, home, "carol.dev.creds", 0o600)       // two parts
	installCreds(t, home, "carol.dev.x.y.creds", 0o600)   // four parts
	installCreds(t, home, "carol.dev.under_score.creds", 0o600)
	installCreds(t, home, "carol..empty.creds", 0o600)
	installCreds(t, home, "carol.dev.design.creds.bak", 0o600)
	installCreds(t, home, "carol.dev."+strings.Repeat("x", 64)+".creds", 0o600) // 64 chars
	if err := os.MkdirAll(filepath.Join(dir, "dave.dev.adir.creds"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "alice.dev.design.creds"), filepath.Join(dir, "erin.dev.link.creds")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "frank.dev.nested.creds"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := load(envOf(nil), home)
	if err != nil || c.Name != "design" || c.Owner != "alice" {
		t.Fatalf("want exactly alice.dev.design: %+v %v", c, err)
	}
	// A 63-character part counts.
	installCreds(t, home, "gina.dev."+strings.Repeat("x", 63)+".creds", 0o600)
	if _, err := load(envOf(nil), home); !errors.Is(err, ErrSeveralEndpoints) {
		t.Fatalf("two sound files: %v", err)
	}
}

// With an endpoint set (by flag, environment or file) the installed creds are not even looked at.
func TestInstalledEndpointsIgnoredWhenAnEndpointIsSet(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	installCreds(t, home, "a.b.c.creds", 0o600)
	installCreds(t, home, "d.e.f.creds", 0o600)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "g.h.i"}), home)
	if err != nil || c.Owner != "g" {
		t.Fatalf("%+v %v", c, err)
	}
}

// A flag's value arrives as the environment variable's; the lookup says which flag it was.
func TestFlagProvenance(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://file:1","ca":"/file/ca.pem"}`)
	vals := map[string]string{"MASH_SERVER": "tls://flag:1", "MASH_ENDPOINT": "o.g.a", "MASH_CA": "/env/ca.pem",
		FlagSourcePrefix + "MASH_SERVER": "--server", FlagSourcePrefix + "MASH_ENDPOINT": "--endpoint"}
	c, err := Load(func(k string) (string, bool) { v, ok := vals[k]; return v, ok }, home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source["servers"] != "flag:--server" || c.Source["endpoint"] != "flag:--endpoint" || c.Source["ca"] != "MASH_CA" {
		t.Errorf("Source %v", c.Source)
	}
}

// An explicitly set creds path (MASH_CREDS or config.json "creds") stops the auto-pick: the address must not come from
// one installed file while the creds are another's.
func TestExplicitCredsPathSkipsTheAutoPick(t *testing.T) {
	home := t.TempDir()
	installCreds(t, home, "bob.dev.design.creds", 0o600)
	for name, tc := range map[string]struct {
		file string
		env  map[string]string
	}{
		"env":  {`{"servers":"tls://x:1"}`, map[string]string{"MASH_CREDS": "/elsewhere/alice.creds"}},
		"file": {`{"servers":"tls://x:1","creds":"/elsewhere/alice.creds"}`, nil},
	} {
		writeFile(t, home, tc.file)
		c, err := load(envOf(tc.env), home)
		if err != nil || c.Name != "" || c.Owner != "" || !c.Inert() || c.Source["endpoint"] != "unset" || c.Creds != "/elsewhere/alice.creds" {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
	// Several installed endpoints with an explicit creds path are no error either.
	installCreds(t, home, "carol.dev.review.creds", 0o600)
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	if c, err := load(envOf(map[string]string{"MASH_CREDS": "/e/a.creds"}), home); err != nil || !c.Inert() {
		t.Errorf("several: %+v %v", c, err)
	}
	// An explicit endpoint still wins and is not changed by the creds path.
	if c, err := load(envOf(map[string]string{"MASH_CREDS": "/e/a.creds", "MASH_ENDPOINT": "alice.dev.x"}), home); err != nil || c.Name != "x" || c.Creds != "/e/a.creds" {
		t.Errorf("explicit endpoint: %+v %v", c, err)
	}
}

// ExplicitOwner is the owner of a set endpoint, else config.json "owner", and never an installed endpoint's.
func TestExplicitOwnerNeverComesFromInstalledCreds(t *testing.T) {
	home := t.TempDir()
	installCreds(t, home, "bob.dev.design.creds", 0o600)
	writeFile(t, home, `{"servers":"tls://x:1"}`)
	c, err := load(envOf(nil), home)
	if err != nil || c.Owner != "bob" || c.ExplicitOwner != "" {
		t.Fatalf("auto-pick: %+v %v", c, err)
	}
	writeFile(t, home, `{"servers":"tls://x:1","owner":"acme"}`)
	if c, err = load(envOf(nil), home); err != nil || c.Owner != "bob" || c.ExplicitOwner != "acme" {
		t.Fatalf("owner key: %+v %v", c, err)
	}
	writeFile(t, home, `{"servers":"tls://x:1","owner":"acme","endpoint":"carol.dev.x"}`)
	if c, err = load(envOf(nil), home); err != nil || c.Owner != "carol" || c.ExplicitOwner != "carol" {
		t.Fatalf("file endpoint beats the owner key: %+v %v", c, err)
	}
	if c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "dave.dev.x"}), home); err != nil || c.ExplicitOwner != "dave" {
		t.Fatalf("env endpoint: %+v %v", c, err)
	}
}
