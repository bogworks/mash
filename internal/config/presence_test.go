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
	c, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_NAME": "a", "MASH_GROUP": "g", "MASH_OWNER": "", "MASH_URL": "", "MASH_CREDS": ""})
	if err != nil {
		t.Fatal(err)
	}
	if c.Owner != "o" || c.URL != "tls://x:4222" || c.Source["owner"] != "config.json" {
		t.Fatalf("%+v %v", c, c.Source)
	}
}

func TestInertConfigIgnoresEmptyGroup(t *testing.T) {
	if _, err := loadPresence(t, idFile+`}`, map[string]string{"MASH_GROUP": ""}); err != nil {
		t.Fatal(err)
	}
}
