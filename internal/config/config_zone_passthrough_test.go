package config

import (
	"strings"
	"testing"
)

// REQ-009 / AT-007: the owner part of the configured endpoint reaches Config.Owner as given, through config.json or
// MASH_ENDPOINT, and is not derived from the endpoint name, the host or the config directory.
func TestOwnerUsedAsGiven(t *testing.T) {
	owners := []string{"o", "acme", "a-b-c", "prod-ops", "o1", "same", strings.Repeat("o", 32)}
	for _, owner := range owners {
		for _, name := range []string{"a", "prod-ops-a", owner} {
			if !ValidName(name) || len(owner)+len(name) > 60 {
				continue
			}
			// config.json only: no MASH_ENDPOINT.
			home := t.TempDir()
			writeFile(t, home, `{"servers":"tls://x:4222","endpoint":"`+owner+`.g.`+name+`","host":"some-host-x"}`)
			c, err := load(envOf(map[string]string{"MASH_HOST": "other-host-y"}), home)
			if err != nil {
				t.Fatalf("file owner %q name %q: %v", owner, name, err)
			}
			if c.Owner != owner || c.Name != name {
				t.Fatalf("file owner %q name %q: got Owner %q Name %q", owner, name, c.Owner, c.Name)
			}
			// MASH_ENDPOINT only: no endpoint in the file.
			home = t.TempDir()
			writeFile(t, home, `{"servers":"tls://x:4222"}`)
			c, err = load(envOf(map[string]string{"MASH_ENDPOINT": owner + ".g." + name}), home)
			if err != nil {
				t.Fatalf("env owner %q name %q: %v", owner, name, err)
			}
			if c.Owner != owner {
				t.Fatalf("env owner %q name %q: got Owner %q", owner, name, c.Owner)
			}
			// Both: the environment wins, byte for byte, and neither is merged with the other.
			home = t.TempDir()
			writeFile(t, home, `{"servers":"tls://x:4222","endpoint":"from-file.h.zz"}`)
			c, err = load(envOf(map[string]string{"MASH_ENDPOINT": owner + ".g." + name}), home)
			if err != nil || c.Owner != owner || c.Group != "g" || c.Name != name {
				t.Fatalf("env over file: owner %q name %q: got %+v %v", owner, name, c, err)
			}
		}
	}
}

// With no endpoint configured mash does not make an owner up from the host or anything else: it is inert.
func TestOwnerNotDerived(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222","host":"prod-host"}`)
	c, err := load(envOf(map[string]string{"MASH_HOST": "prod-host", "MASH_ACCOUNT": "prod"}), home)
	if err != nil || !c.Inert() {
		t.Fatalf("got %+v, %v, want inert", c, err)
	}
	if c.Owner != "" {
		t.Fatalf("Owner %q was derived", c.Owner)
	}
}

// An owner that is not a valid name is refused and not repaired (lower-cased, trimmed or cut at a dash).
func TestOwnerValidatedNotRepaired(t *testing.T) {
	for _, owner := range []string{"Acme", " acme", "acme ", "a_b", "a--b", "acme.prod", strings.Repeat("o", 33)} {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://x:4222"}`)
		c, err := load(envOf(map[string]string{"MASH_ENDPOINT": owner + ".g." + "a"}), home)
		if err == nil {
			t.Fatalf("owner %q accepted as %q", owner, c.Owner)
		}
	}
}
