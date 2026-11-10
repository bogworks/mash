package config

import (
	"errors"
	"strings"
	"testing"
)

// REQ-009 / AT-007: a configured owner reaches Config.Owner as given, through config.json or MASH_OWNER, and is not
// derived from the endpoint name, the host or the config directory.
func TestOwnerUsedAsGiven(t *testing.T) {
	owners := []string{"o", "acme", "a-b-c", "prod-ops", "o1", "same", strings.Repeat("o", 48)}
	for _, owner := range owners {
		for _, name := range []string{"a", "prod-ops-a", owner} {
			if !ValidName(name) {
				continue
			}
			// config.json only: no MASH_OWNER.
			home := t.TempDir()
			writeFile(t, home, `{"url":"tls://x:4222","owner":"`+owner+`","host":"some-host-x"}`)
			c, err := load(envOf(map[string]string{"MASH_NAME": name, "MASH_HOST": "other-host-y"}), home)
			if err != nil {
				t.Fatalf("file owner %q name %q: %v", owner, name, err)
			}
			if c.Owner != owner {
				t.Fatalf("file owner %q name %q: got Owner %q", owner, name, c.Owner)
			}
			// MASH_OWNER only: no owner in the file.
			home = t.TempDir()
			writeFile(t, home, `{"url":"tls://x:4222"}`)
			c, err = load(envOf(map[string]string{"MASH_NAME": name, "MASH_OWNER": owner}), home)
			if err != nil {
				t.Fatalf("env owner %q name %q: %v", owner, name, err)
			}
			if c.Owner != owner {
				t.Fatalf("env owner %q name %q: got Owner %q", owner, name, c.Owner)
			}
			// Both: the environment wins, byte for byte, and neither is merged with the other.
			home = t.TempDir()
			writeFile(t, home, `{"url":"tls://x:4222","owner":"from-file"}`)
			c, err = load(envOf(map[string]string{"MASH_NAME": name, "MASH_OWNER": owner}), home)
			if err != nil || c.Owner != owner {
				t.Fatalf("env over file: owner %q name %q: got %q %v", owner, name, c.Owner, err)
			}
		}
	}
}

// With no owner configured mash does not make one up from the name, the host or anything else: it refuses.
func TestOwnerNotDerived(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","host":"prod-host"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "prod-ops-a", "MASH_HOST": "prod-host", "MASH_ACCOUNT": "prod"}), home)
	if !errors.Is(err, ErrNoOwner) {
		t.Fatalf("got %v, want ErrNoOwner", err)
	}
	if c.Owner != "" {
		t.Fatalf("Owner %q was derived", c.Owner)
	}
}

// An owner that is not a valid name is refused and not repaired (lower-cased, trimmed or cut at a dash).
func TestOwnerValidatedNotRepaired(t *testing.T) {
	for _, owner := range []string{"Acme", " acme", "acme ", "a_b", "a--b", "acme.prod", strings.Repeat("o", 49)} {
		home := t.TempDir()
		writeFile(t, home, `{"url":"tls://x:4222"}`)
		c, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_OWNER": owner}), home)
		if err == nil {
			t.Fatalf("owner %q accepted as %q", owner, c.Owner)
		}
	}
}
