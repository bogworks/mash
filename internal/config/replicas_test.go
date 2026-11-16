package config

import (
	"strings"
	"testing"
)

func TestReplicasAndServersSettings(t *testing.T) {
	for _, c := range []struct {
		name, file string
		env        map[string]string
		replicas   string
		servers    int
		effective  int
		errHas     string
	}{
		{"defaults", `{"url":"u","owner":"o"}`, nil, "auto", 0, 1, ""},
		{"auto with three servers in the file", `{"url":"u","owner":"o","servers":3}`, nil, "auto", 3, 3, ""},
		{"replicas as a string", `{"url":"u","owner":"o","servers":3,"replicas":"1"}`, nil, "1", 3, 1, ""},
		{"replicas as a number", `{"url":"u","owner":"o","servers":3,"replicas":3}`, nil, "3", 3, 3, ""},
		{"env wins", `{"url":"u","owner":"o","servers":3,"replicas":"3"}`, map[string]string{"MASH_REPLICAS": "1", "MASH_SERVERS": "5"}, "1", 5, 1, ""},
		{"env alone", `{"url":"u","owner":"o"}`, map[string]string{"MASH_SERVERS": "3"}, "auto", 3, 3, ""},
		{"three on one declared server is refused when used", `{"url":"u","owner":"o","replicas":"3"}`, nil, "3", 0, 0, "needs at least 3 servers"},
		{"five is refused when used", `{"url":"u","owner":"o","servers":5,"replicas":5}`, nil, "5", 5, 0, "auto, 1 and 3"},
		{"an unusable MASH_SERVERS reads as none", `{"url":"u","owner":"o"}`, map[string]string{"MASH_SERVERS": "many"}, "auto", 0, 1, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, home, c.file)
			cfg, err := load(envOf(c.env), home)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Replicas != c.replicas || cfg.Servers != c.servers {
				t.Fatalf("replicas %q servers %d, want %q %d", cfg.Replicas, cfg.Servers, c.replicas, c.servers)
			}
			n, err := cfg.EffectiveReplicas()
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) {
					t.Fatalf("want an error with %q, got %d, %v", c.errHas, n, err)
				}
				return
			}
			if err != nil || n != c.effective {
				t.Fatalf("effective replicas %d, %v; want %d", n, err, c.effective)
			}
		})
	}
}

func TestReplicasFileValueMustBeAStringOrNumber(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"u","owner":"o","replicas":[3]}`)
	if _, err := load(envOf(nil), home); err == nil || !strings.Contains(err.Error(), `"replicas"`) {
		t.Fatalf("an array was accepted: %v", err)
	}
}
