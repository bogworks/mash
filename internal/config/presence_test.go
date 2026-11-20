package config

import "testing"

// lookupOf reports a variable present iff it is a key of m, even when its value is "".
func lookupOf(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func loadPresence(t *testing.T, fileBody string, env map[string]string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	if fileBody != "" {
		writeFile(t, home, fileBody)
	}
	return Load(lookupOf(env), home)
}

func TestEmptyOtherVariablesStillReadAsUnset(t *testing.T) {
	c, err := loadPresence(t, `{"servers":"tls://x:4222","endpoint":"o.g.a"}`, map[string]string{"MASH_SERVER": "", "MASH_ENDPOINT": "", "MASH_CREDS": ""})
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "o" || c.URL != "tls://x:4222" || c.Source["endpoint"] != "config.json" {
		t.Fatalf("%+v %v", c, c.Source)
	}
}

func TestEmptyEndpointReadsAsUnset(t *testing.T) {
	if c, err := loadPresence(t, `{"servers":"tls://x:4222"}`, map[string]string{"MASH_ENDPOINT": ""}); err != nil || !c.Inert() {
		t.Fatalf("%+v %v", c, err)
	}
}
