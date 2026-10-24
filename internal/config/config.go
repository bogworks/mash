// Package config loads mash's settings: ~/.config/mash/config.json, then the
// environment, which wins. An empty endpoint name means the endpoint manager
// runs inert (the plugin can be enabled in sessions that have no identity).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Config is the resolved configuration for one process.
type Config struct {
	URL         string // NATS URL
	Owner       string // SAP owner token
	Name        string // endpoint name (MASH_NAME); empty = inert
	Creds       string // endpoint credentials file
	SettleMS    int    // quiet time before a batch is pushed
	BatchMsgs   int    // a batch up to this many messages is shown in full
	BatchBytes  int    // ... and up to this many body bytes
	AdminCreds  string // admin credentials for `mash admin`
	LaunchToken string // MASH_LAUNCH_TOKEN: names the launch of the agent this process serves; recorded in the lease, optional
	Dir         string // ~/.config/mash
}

type file struct {
	URL        string `json:"url"`
	Owner      string `json:"owner"`
	Creds      string `json:"creds"`
	SettleMS   *int   `json:"settle_ms"`
	BatchMsgs  *int   `json:"batch_msgs"`
	BatchBytes *int   `json:"batch_bytes"`
	AdminCreds string `json:"admin_creds"`
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidName reports whether s is a valid endpoint or owner name:
// lower-case letters, digits and single hyphens, starting with a letter,
// 1-48 characters.
func ValidName(s string) bool {
	return len(s) >= 1 && len(s) <= 48 && nameRE.MatchString(s)
}

// Inert reports whether no endpoint name is configured.
func (c Config) Inert() bool { return c.Name == "" }

// Load resolves the configuration. env is os.Getenv in production.
func Load(env func(string) string, home string) (Config, error) {
	dir := filepath.Join(home, ".config", "mash")
	c := Config{SettleMS: 100, BatchMsgs: 10, BatchBytes: 16384, Dir: dir}

	var f file
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &f); err != nil {
			return c, fmt.Errorf("mash: %s: %w", filepath.Join(dir, "config.json"), err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return c, fmt.Errorf("mash: reading config: %w", err)
	}
	c.URL, c.Owner, c.Creds, c.AdminCreds = f.URL, f.Owner, f.Creds, f.AdminCreds
	for _, p := range []struct {
		dst *int
		src *int
	}{{&c.SettleMS, f.SettleMS}, {&c.BatchMsgs, f.BatchMsgs}, {&c.BatchBytes, f.BatchBytes}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}

	str := func(dst *string, key string) {
		if v := env(key); v != "" {
			*dst = v
		}
	}
	str(&c.URL, "MASH_URL")
	str(&c.Owner, "MASH_OWNER")
	str(&c.Name, "MASH_NAME")
	str(&c.Creds, "MASH_CREDS")
	str(&c.AdminCreds, "MASH_ADMIN_CREDS")
	str(&c.LaunchToken, "MASH_LAUNCH_TOKEN")
	for _, p := range []struct {
		dst *int
		key string
	}{{&c.SettleMS, "MASH_SETTLE_MS"}, {&c.BatchMsgs, "MASH_BATCH_MSGS"}, {&c.BatchBytes, "MASH_BATCH_BYTES"}} {
		if v := env(p.key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return c, fmt.Errorf("mash: %s=%q is not a non-negative integer", p.key, v)
			}
			*p.dst = n
		}
	}

	if !validLaunchToken(c.LaunchToken) {
		return c, fmt.Errorf("mash: MASH_LAUNCH_TOKEN: use at most 128 letters, digits, '.', '_' or '-'")
	}
	if c.Name == "" {
		return c, nil
	}
	if !ValidName(c.Name) {
		return c, fmt.Errorf("mash: MASH_NAME %q: use 1-48 lower-case letters, digits and single hyphens, starting with a letter", c.Name)
	}
	if c.URL == "" {
		return c, errors.New("mash: no NATS url (config.json \"url\" or MASH_URL)")
	}
	if c.Owner == "" {
		return c, errors.New("mash: no owner (config.json \"owner\" or MASH_OWNER)")
	}
	if !ValidName(c.Owner) {
		return c, fmt.Errorf("mash: owner %q: use 1-48 lower-case letters, digits and single hyphens", c.Owner)
	}
	if c.Creds == "" {
		c.Creds = filepath.Join(dir, c.Name+".creds")
	}
	return c, nil
}

// validLaunchToken: empty, or up to 128 of [A-Za-z0-9._-] (it goes into a JSON lease value and a process environment).
func validLaunchToken(t string) bool {
	if len(t) > 128 {
		return false
	}
	for _, r := range t {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
