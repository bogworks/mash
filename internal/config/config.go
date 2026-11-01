// Package config loads mash's settings: ~/.config/mash/config.json, then the
// environment, which wins. An empty endpoint name means the endpoint manager
// runs inert (the plugin can be enabled in sessions that have no identity).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/store"
)

// The defaults of Config.OpenTimeout and Config.CallTimeout.
const (
	DefaultOpenTimeout = 20 * time.Second
	DefaultCallTimeout = 15 * time.Second
)

// Config is the resolved configuration for one process.
type Config struct {
	URL            string        // NATS URL
	Owner          string        // SAP owner token
	StateHome      string        // $XDG_STATE_HOME, else ~/.local/state: where serve and its hook keep the spool
	Name           string        // endpoint name (MASH_NAME); empty = inert
	Creds          string        // endpoint credentials file
	SettleMS       int           // quiet time before a batch is pushed
	BatchMsgs      int           // a batch up to this many messages is shown in full
	BatchBytes     int           // ... and up to this many body bytes
	IdleWake       time.Duration // hook mode: spooled mail no hook shows within this wakes the idle session (5s-30m)
	OpenTimeout    time.Duration // bound on opening a connection to the endpoint's data: the first requests (default 20s)
	CallTimeout    time.Duration // bound on one command's or tool call's requests once open (default 15s)
	AdminCreds     string        // admin credentials for `mash admin`
	TLSCA          string        // MASH_TLS_CA / "tls_ca": PEM file of the CA that signed the server's certificate (empty = system roots)
	TLSCert        string        // MASH_TLS_CERT / "tls_cert": PEM client certificate, for a server that requires one; set with TLSKey
	TLSKey         string        // MASH_TLS_KEY / "tls_key": PEM private key of TLSCert
	LaunchToken    string        // MASH_LAUNCH_TOKEN: names the launch of the agent this process serves; recorded in the lease, optional
	Host           string        // MASH_HOST / "host": the agent host adapter ("" = claude)
	SpoolFromStart bool          // MASH_SPOOL_FROM_START / "spool_from_start": spool mail from the first message, before any hook has run
	Dir            string        // ~/.config/mash
	Limits         store.Limits  // config.json "limits": inbox and bucket caps (defaults: store.DefaultLimits)
}

type file struct {
	URL        string `json:"url"`
	Owner      string `json:"owner"`
	Creds      string `json:"creds"`
	SettleMS   *int   `json:"settle_ms"`
	BatchMsgs  *int   `json:"batch_msgs"`
	BatchBytes *int   `json:"batch_bytes"`
	IdleWake   string `json:"idle_wake"`

	OpenTimeout string `json:"open_timeout"`
	CallTimeout string `json:"call_timeout"`

	AdminCreds string `json:"admin_creds"`
	Host       string `json:"host"`

	TLSCA   string `json:"tls_ca"`
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	SpoolFromStart *bool `json:"spool_from_start"`

	Limits json.RawMessage `json:"limits"`
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

// ErrNoURL and ErrNoOwner are what Load returns once the whole configuration is resolved and a required field is
// still empty; any other error stopped loading before the environment was fully applied.
var (
	ErrNoURL   = errors.New("mash: no NATS url (config.json \"url\" or MASH_URL)")
	ErrNoOwner = errors.New("mash: no owner (config.json \"owner\" or MASH_OWNER)")
)

// ValidateFile reports whether raw is a config.json that Load would accept as far as the file itself goes: it must
// parse into the known fields with the right types, and "idle_wake" must be a valid duration. The environment is not
// consulted.
func ValidateFile(raw []byte) error {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if f.IdleWake != "" {
		if _, err := idleWake(f.IdleWake); err != nil {
			return fmt.Errorf("\"idle_wake\" %w", err)
		}
	}
	if _, err := ParseLimits(f.Limits); err != nil {
		return err
	}
	return nil
}

// Load resolves the configuration. env is os.Getenv in production.
func Load(env func(string) string, home string) (Config, error) {
	dir := filepath.Join(home, ".config", "mash")
	c := Config{SettleMS: 100, BatchMsgs: 10, BatchBytes: 16384, IdleWake: 45 * time.Second, OpenTimeout: DefaultOpenTimeout, CallTimeout: DefaultCallTimeout, Dir: dir, StateHome: env("XDG_STATE_HOME"), Limits: store.DefaultLimits()}
	if c.StateHome == "" {
		c.StateHome = filepath.Join(home, ".local", "state")
	}

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
	c.URL, c.Owner, c.Creds, c.AdminCreds, c.Host = f.URL, f.Owner, f.Creds, f.AdminCreds, f.Host
	c.TLSCA, c.TLSCert, c.TLSKey = f.TLSCA, f.TLSCert, f.TLSKey
	if f.SpoolFromStart != nil {
		c.SpoolFromStart = *f.SpoolFromStart
	}
	for _, p := range []struct {
		dst *int
		src *int
	}{{&c.SettleMS, f.SettleMS}, {&c.BatchMsgs, f.BatchMsgs}, {&c.BatchBytes, f.BatchBytes}} {
		if p.src != nil {
			*p.dst = *p.src
		}
	}

	if f.IdleWake != "" {
		d, err := idleWake(f.IdleWake)
		if err != nil {
			return c, fmt.Errorf("mash: config.json \"idle_wake\" %w", err)
		}
		c.IdleWake = d
	}

	if c.Limits, err = ParseLimits(f.Limits); err != nil {
		return c, err
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
	str(&c.Host, "MASH_HOST")
	str(&c.TLSCA, "MASH_TLS_CA")
	str(&c.TLSCert, "MASH_TLS_CERT")
	str(&c.TLSKey, "MASH_TLS_KEY")
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

	// Only "1" and "true" turn it on and "0" and "false" off: a typo must not silently keep the lossy startup path.
	switch v := env("MASH_SPOOL_FROM_START"); v {
	case "":
	case "1", "true":
		c.SpoolFromStart = true
	case "0", "false":
		c.SpoolFromStart = false
	default:
		return c, fmt.Errorf("mash: MASH_SPOOL_FROM_START=%q is not 1, true, 0 or false", v)
	}

	if v := env("MASH_IDLE_WAKE"); v != "" {
		d, err := idleWake(v)
		if err != nil {
			return c, fmt.Errorf("mash: MASH_IDLE_WAKE %w", err)
		}
		c.IdleWake = d
	}

	// Each timeout's effective raw value is chosen first (env, else file, else the default) and validated once, so an
	// invalid file value under a valid env override loads (codex M2 on #121); an invalid effective one names its source.
	for _, p := range []struct {
		dst          *time.Duration
		file, envKey string
		fileKey      string
	}{{&c.OpenTimeout, f.OpenTimeout, "MASH_OPEN_TIMEOUT", "open_timeout"}, {&c.CallTimeout, f.CallTimeout, "MASH_CALL_TIMEOUT", "call_timeout"}} {
		raw, from := env(p.envKey), p.envKey
		if raw == "" {
			raw, from = p.file, fmt.Sprintf("config.json %q", p.fileKey)
		}
		if raw == "" {
			continue
		}
		d, err := positiveDuration(raw)
		if err != nil {
			return c, fmt.Errorf("mash: %s %w", from, err)
		}
		*p.dst = d
	}

	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, errors.New("mash: tls_cert and tls_key (MASH_TLS_CERT and MASH_TLS_KEY) must be set together")
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
		return c, ErrNoURL
	}
	if c.Owner == "" {
		return c, ErrNoOwner
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

// idleWake parses an idle wake duration: a Go duration string from 5s to 30m.
func idleWake(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d < 5*time.Second || d > 30*time.Minute {
		return 0, fmt.Errorf("%q is not a duration from 5s to 30m (for example \"45s\" or \"2m\")", v)
	}
	return d, nil
}

// positiveDuration parses a timeout: a Go duration string above zero.
func positiveDuration(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not a duration above zero (for example \"20s\" or \"1m\")", v)
	}
	return d, nil
}

// PlainTextWarning returns a warning when url (or any entry of a comma-separated server list) uses nats:// or ws://
// to a host that is not loopback, and "" otherwise. SAP identity assumes a verified connection, which plain text is not.
func PlainTextWarning(url string) string {
	for _, e := range strings.Split(url, ",") {
		e = strings.TrimSpace(e)
		scheme, rest, ok := strings.Cut(e, "://")
		if !ok {
			continue
		}
		if s := strings.ToLower(scheme); s != "nats" && s != "ws" {
			continue
		}
		rest, _, _ = strings.Cut(rest, "/")
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest = rest[i+1:]
		}
		host := rest
		if h, _, err := net.SplitHostPort(rest); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); (ip != nil && ip.IsLoopback()) || strings.EqualFold(host, "localhost") {
			continue
		}
		return "is plain " + strings.ToLower(scheme) + ":// to a non-loopback host: SAP identity assumes a verified connection. Next: use tls:// with the server's certificate (tls_ca for a private CA)"
	}
	return ""
}
