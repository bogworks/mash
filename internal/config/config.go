// Package config loads mash's settings: config.json in the config directory (see ConfigDir), then the
// environment, which wins. An empty endpoint name means the endpoint manager
// runs inert (the plugin can be enabled in sessions that have no identity).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/namerule"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// The defaults of Config.OpenTimeout and Config.CallTimeout.
const (
	DefaultOpenTimeout = 20 * time.Second
	DefaultCallTimeout = 15 * time.Second
)

// The defaults of the timeouts whose Config fields stay zero when unset.
const (
	DefaultCLITimeout   = 15 * time.Minute
	DefaultAdminTimeout = 30 * time.Second
)

// Config is the resolved configuration for one process.
type Config struct {
	URL             string        // NATS URL
	Owner           string        // SAP owner token
	StateDir        string        // $MASH_STATE_DIR, else $XDG_STATE_HOME/mash, else ~/.local/state/mash: holds spool/ and hook.log
	Name            string        // endpoint name (MASH_NAME); empty = inert
	AgentToken      string        // MASH_AGENT_TOKEN / "agent_token": the agent token of a three-token identity; empty = the legacy identity
	Creds           string        // endpoint credentials file
	SettleMS        int           // quiet time before a batch is pushed
	BatchMsgs       int           // a batch up to this many messages is shown in full
	BatchBytes      int           // ... and up to this many body bytes
	IdleWake        time.Duration // hook mode: spooled mail no hook shows within this wakes the idle session (5s-30m)
	RelayWindow     time.Duration // serve: a send within this of another sender's mail is traced as a relay candidate (1s-1h, default 60s)
	OpenTimeout     time.Duration // bound on opening a connection to the endpoint's data: the first requests (default 20s)
	CallTimeout     time.Duration // bound on one command's or tool call's requests once open (default 15s)
	LeaseRetry      time.Duration // serve: wait between attempts to take a held lease (zero = serve's default, 10s)
	TransientBudget time.Duration // serve: transient failures in a row for longer than this end the wait (zero = 15m)
	TransientMax    time.Duration // serve: the retry backoff doubles up to this (zero = 2m)
	CLITimeout      time.Duration // total deadline of one CLI command (zero = 15m)
	AdminTimeout    time.Duration // total deadline of one `mash admin` command (zero = 30s)
	AdminCreds      string        // admin credentials for `mash admin`
	TLSCA           string        // MASH_TLS_CA / "tls_ca": PEM file of the CA that signed the server's certificate (empty = system roots)
	TLSCert         string        // MASH_TLS_CERT / "tls_cert": PEM client certificate, for a server that requires one; set with TLSKey
	TLSKey          string        // MASH_TLS_KEY / "tls_key": PEM private key of TLSCert
	// InsecureLoopback is the one minimal escape from the TLS requirement of admin and system connections: it allows
	// plain text, and only to a loopback URL. It is a setting, not a command-line flag, and may be renamed when the
	// command-line settings are redesigned (its name now: "insecure_loopback").
	InsecureLoopback bool // MASH_INSECURE_LOOPBACK / "insecure_loopback": allow plain nats:// for admin and system connections to a loopback server (a local server `init --local` made); refused for any other host
	// RequireTLS makes every endpoint connection (serve, hook, the CLI commands) refuse a plain nats:// or ws:// URL to a
	// host that is not localhost or a loopback IP literal. Plain text to a loopback host is always allowed. Default off.
	RequireTLS     bool         // MASH_REQUIRE_TLS / "require_tls": refuse plain text from endpoint connections to a remote host (use tls://)
	LaunchToken    string       // MASH_LAUNCH_TOKEN: names the launch of the agent this process serves; recorded in the lease, optional
	Host           string       // MASH_HOST / "host": the agent host adapter ("" = claude)
	SpoolFromStart bool         // MASH_SPOOL_FROM_START / "spool_from_start": spool mail from the first message, before any hook has run
	SpoolRequired  bool         // MASH_SPOOL_REQUIRED / "spool_required": fail closed: mail the spool cannot take is held unacked and reported, never delivered by the channel (default off)
	Dir            string       // the config directory (ConfigDir); "" when none can be found
	Legacy         string       // $HOME/.config/mash ("" with no home): where policy.md and default creds are also looked for
	Limits         store.Limits // config.json "limits": inbox and bucket caps (defaults: store.DefaultLimits)

	// The name-acceptance rule (docs/name-rule.md). Load reads the file once; a file that cannot be read or is invalid
	// fails Load. NameRule is nil, and accepts everything, when no file is named.
	NameRule     *namerule.Rule // MASH_NAME_RULE_FILE / "name_rule_file": the loaded rule
	NameRulePath string         // the absolute path it was loaded from ("" = none)

	// Provenance of the settings that decide which server a command acts on, for a command to say what it is about to
	// act on (it informs; it changes no precedence). Keys: SourceKeys.
	File       string            // the config.json that was read ("" when there is none)
	Source     map[string]string // key -> where its value came from: an environment variable name (MASH_URL), "config.json", "default" or "unset"
	FileValues map[string]string // key -> what config.json itself said (only keys it set), to compare with the effective value
}

// SourceKeys are the keys of Config.Source and Config.FileValues, and the environment variable each one is read from.
var SourceKeys = map[string]string{
	"url": "MASH_URL", "owner": "MASH_OWNER", "agent_token": "MASH_AGENT_TOKEN", "admin_creds": "MASH_ADMIN_CREDS",
	"tls_ca": "MASH_TLS_CA", "tls_cert": "MASH_TLS_CERT", "tls_key": "MASH_TLS_KEY", "insecure_loopback": "MASH_INSECURE_LOOPBACK",
	"require_tls": "MASH_REQUIRE_TLS",
}

type file struct {
	URL        string          `json:"url"`
	Owner      string          `json:"owner"`
	AgentToken json.RawMessage `json:"agent_token"` // kept raw: null, "" and non-strings are present but invalid
	Creds      string          `json:"creds"`
	SettleMS   *int            `json:"settle_ms"`
	BatchMsgs  *int            `json:"batch_msgs"`
	BatchBytes *int            `json:"batch_bytes"`
	IdleWake   string          `json:"idle_wake"`

	RelayWindow string `json:"relay_window"`

	OpenTimeout string `json:"open_timeout"`
	CallTimeout string `json:"call_timeout"`

	LeaseRetry      string `json:"lease_retry"`
	TransientBudget string `json:"transient_budget"`
	TransientMax    string `json:"transient_max"`
	CLITimeout      string `json:"cli_timeout"`
	AdminTimeout    string `json:"admin_timeout"`

	AdminCreds string `json:"admin_creds"`
	Host       string `json:"host"`

	TLSCA   string `json:"tls_ca"`
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	SpoolFromStart *bool `json:"spool_from_start"`
	SpoolRequired  *bool `json:"spool_required"`

	InsecureLoopback *bool `json:"insecure_loopback"`
	RequireTLS       *bool `json:"require_tls"`

	Limits json.RawMessage `json:"limits"`

	NameRuleFile     json.RawMessage `json:"name_rule_file"`     // kept raw: null, "" and non-strings are present but invalid
	NameRuleRequired json.RawMessage `json:"name_rule_required"` // kept raw: only true and false are valid
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
	// ErrAgentTokenEmpty is Load's error for MASH_AGENT_TOKEN present but empty. Callers that must not treat a config
	// error as a no-op (hook) test for it with errors.Is.
	ErrAgentTokenEmpty = errors.New("mash: MASH_AGENT_TOKEN is set but empty: give an agent token, or unset it for the legacy identity")
	// ErrNameRuleRequired is Load's error for a required name rule with no file loaded.
	ErrNameRuleRequired = errors.New("mash: a name rule is required (name_rule_required or MASH_NAME_RULE_REQUIRED is true) but no rule file is set: set name_rule_file (MASH_NAME_RULE_FILE) to a name-rule file")
	ErrNoURL            = errors.New("mash: no NATS url (config.json \"url\" or MASH_URL)")
	ErrNoOwner          = errors.New("mash: no owner (config.json \"owner\" or MASH_OWNER)")
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
	if f.RelayWindow != "" {
		if _, err := relayWindow(f.RelayWindow); err != nil {
			return fmt.Errorf("\"relay_window\" %w", err)
		}
	}
	if _, err := ParseLimits(f.Limits); err != nil {
		return err
	}
	if _, _, err := nameRuleFileKey(f.NameRuleFile); err != nil {
		return err
	}
	if _, err := nameRuleRequiredKey(f.NameRuleRequired); err != nil {
		return err
	}
	return nil
}

// ErrNoConfigDir is what Load reports when a config file or default creds path is needed and neither a home nor an
// absolute XDG_CONFIG_HOME is available.
var ErrNoConfigDir = errors.New("mash: cannot find a config directory: set HOME or XDG_CONFIG_HOME, or use the MASH_* variables")

// explicitDir is the absolute directory named by the mash variable v, "" when it is unset. A relative value is an error
// naming the variable.
func explicitDir(env func(string) string, v string) (string, error) {
	d := env(v)
	if d == "" {
		return "", nil
	}
	if !filepath.IsAbs(d) {
		return "", fmt.Errorf("mash: %s=%q is not an absolute path", v, d)
	}
	return filepath.Clean(d), nil
}

// ConfigDir is the one place the whole binary looks for config.json, policy.md and default creds: an absolute
// $MASH_CONFIG_DIR when set (an explicit choice, with no legacy fallback; Load rejects a relative one), else $XDG_CONFIG_HOME/mash
// when XDG_CONFIG_HOME is absolute and that directory holds a config.json, else $HOME/.config/mash (where existing
// installs keep theirs). With no home it is the XDG directory if one is set, else "": never a relative path.
func ConfigDir(env func(string) string, home string) string {
	if d, _ := explicitDir(env, "MASH_CONFIG_DIR"); d != "" {
		return d
	}
	xdg := ""
	if x := env("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		xdg = filepath.Join(x, "mash")
		// Only a config.json that is absent falls back to the legacy directory. Any other error (permission denied,
		// I/O) keeps the XDG directory so Load's read fails loudly instead of silently using a different config.
		if _, err := os.Stat(filepath.Join(xdg, "config.json")); err == nil ||
			!(errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
			return xdg
		}
	}
	if home != "" {
		return filepath.Join(home, ".config", "mash")
	}
	return xdg
}

// Absent reports whether p is definitely not there: Lstat says not-exist or not-a-directory. Anything else, a symlink
// (even a dangling or inaccessible one) or a permission or I/O error, counts as present so the caller's read fails
// loudly instead of silently picking a file from another directory.
func Absent(p string) bool {
	_, err := os.Lstat(p)
	return err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR))
}

// FindFile is the path of name (policy.md, a default creds file): in the XDG mash directory when it is there, else in
// the legacy $HOME/.config/mash when it is there, else in ConfigDir, so messages and creation paths stay where they
// were. The XDG copy wins when both exist.
func FindFile(env func(string) string, home, name string) string {
	if d, _ := explicitDir(env, "MASH_CONFIG_DIR"); d != "" {
		return filepath.Join(d, name)
	}
	var dirs []string
	if x := env("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		dirs = append(dirs, filepath.Join(x, "mash"))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".config", "mash"))
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if !Absent(p) {
			return p
		}
	}
	if d := ConfigDir(env, home); d != "" {
		return filepath.Join(d, name)
	}
	return ""
}

// Find is FindFile for this Config: name in Dir if present, else in Legacy if present, else in Dir.
func (c Config) Find(name string) string {
	for _, d := range []string{c.Dir, c.Legacy} {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		if !Absent(p) {
			return p
		}
	}
	if c.Dir == "" {
		return ""
	}
	return filepath.Join(c.Dir, name)
}

// inDir resolves a relative path against dir; an empty or absolute path, or an empty dir, is returned as is.
func inDir(dir, p string) string {
	if p == "" || dir == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return p
	}
	return filepath.Join(dir, p)
}

// ExpandHome expands a leading "~" or "~/" in p to home. "~user/" is left alone, and so is p when home is unknown.
func ExpandHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// Lookup reads one variable and reports whether it is present, as os.LookupEnv does. A variable set to "" is present.
type Lookup func(string) (string, bool)

// LookupFromFunc adapts a plain env func: a variable is present iff its value is not empty. For callers (tests) that
// have no way to say a variable is set but empty.
func LookupFromFunc(env func(string) string) Lookup {
	return func(k string) (string, bool) { v := env(k); return v, v != "" }
}

// Load resolves the configuration. lookup is os.LookupEnv in production; home is $HOME, or the OS's idea of the user's
// home when that is unset ("" when unknown). Only MASH_AGENT_TOKEN distinguishes set-but-empty from unset (the former
// is an error once a name is set); an empty value of any other variable reads as unset.
func Load(lookup Lookup, home string) (Config, error) {
	env := func(k string) string { v, _ := lookup(k); return v }
	_, agentEnvSet := lookup("MASH_AGENT_TOKEN")
	if _, err := explicitDir(env, "MASH_CONFIG_DIR"); err != nil {
		return Config{}, err
	}
	state, err := explicitDir(env, "MASH_STATE_DIR")
	if err != nil {
		return Config{}, err
	}
	if state == "" {
		if x := env("XDG_STATE_HOME"); filepath.IsAbs(x) {
			state = filepath.Join(x, "mash")
		} else {
			state = filepath.Join(home, ".local", "state", "mash")
		}
	}
	dir := ConfigDir(env, home)
	legacy := ""
	if home != "" && env("MASH_CONFIG_DIR") == "" {
		legacy = filepath.Join(home, ".config", "mash")
	}
	c := Config{Legacy: legacy, SettleMS: 100, BatchMsgs: 10, BatchBytes: 16384, IdleWake: 45 * time.Second, RelayWindow: DefaultRelayWindow, OpenTimeout: DefaultOpenTimeout, CallTimeout: DefaultCallTimeout, Dir: dir, StateDir: state, Limits: store.DefaultLimits()}

	var f file
	var raw []byte
	if dir != "" { // no directory means no file: the pure-environment case still loads
		raw, err = os.ReadFile(filepath.Join(dir, "config.json"))
	} else {
		err = os.ErrNotExist
	}
	switch {
	case err == nil:
		if err := checkConfigMode(filepath.Join(dir, "config.json")); err != nil {
			return c, err
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return c, fmt.Errorf("mash: %s: %w", filepath.Join(dir, "config.json"), err)
		}
		c.File = filepath.Join(dir, "config.json")
	case !errors.Is(err, os.ErrNotExist):
		return c, fmt.Errorf("mash: reading config: %w", err)
	}
	// Relative paths in the file are relative to the config directory, not to wherever mash was started.
	f.AdminCreds = inDir(dir, ExpandHome(f.AdminCreds, home))
	f.TLSCA, f.TLSCert, f.TLSKey = inDir(dir, f.TLSCA), inDir(dir, f.TLSCert), inDir(dir, f.TLSKey)
	c.URL, c.Owner, c.Creds, c.AdminCreds, c.Host = f.URL, f.Owner, f.Creds, f.AdminCreds, f.Host
	c.TLSCA, c.TLSCert, c.TLSKey = f.TLSCA, f.TLSCert, f.TLSKey
	if f.SpoolFromStart != nil {
		c.SpoolFromStart = *f.SpoolFromStart
	}
	if f.SpoolRequired != nil {
		c.SpoolRequired = *f.SpoolRequired
	}
	if f.InsecureLoopback != nil {
		c.InsecureLoopback = *f.InsecureLoopback
	}
	if f.RequireTLS != nil {
		c.RequireTLS = *f.RequireTLS
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

	if f.RelayWindow != "" {
		d, err := relayWindow(f.RelayWindow)
		if err != nil {
			return c, fmt.Errorf("mash: config.json \"relay_window\" %w", err)
		}
		c.RelayWindow = d
	}

	if c.Limits, err = ParseLimits(f.Limits); err != nil {
		return c, err
	}

	fileAgentSet := false
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
	str(&c.AgentToken, "MASH_AGENT_TOKEN")
	if f.AgentToken != nil {
		if env("MASH_AGENT_TOKEN") == "" {
			c.AgentToken = agentTokenOf(f.AgentToken)
		}
		fileAgentSet = true
	}
	c.Creds, c.AdminCreds = ExpandHome(c.Creds, home), ExpandHome(c.AdminCreds, home)
	c.recordSources(f, env, home)
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

	// Only "1" and "true" turn it on and "0" and "false" off: a typo must not silently keep mail on the channel path.
	switch v := env("MASH_SPOOL_REQUIRED"); v {
	case "":
	case "1", "true":
		c.SpoolRequired = true
	case "0", "false":
		c.SpoolRequired = false
	default:
		return c, fmt.Errorf("mash: MASH_SPOOL_REQUIRED=%q is not 1, true, 0 or false", v)
	}

	switch v := env("MASH_INSECURE_LOOPBACK"); v {
	case "":
	case "1", "true":
		c.InsecureLoopback = true
	case "0", "false":
		c.InsecureLoopback = false
	default:
		return c, fmt.Errorf("mash: MASH_INSECURE_LOOPBACK=%q is not 1, true, 0 or false", v)
	}

	switch v := env("MASH_REQUIRE_TLS"); v {
	case "":
	case "1", "true":
		c.RequireTLS = true
	case "0", "false":
		c.RequireTLS = false
	default:
		return c, fmt.Errorf("mash: MASH_REQUIRE_TLS=%q is not 1, true, 0 or false", v)
	}

	if v := env("MASH_IDLE_WAKE"); v != "" {
		d, err := idleWake(v)
		if err != nil {
			return c, fmt.Errorf("mash: MASH_IDLE_WAKE %w", err)
		}
		c.IdleWake = d
	}

	if v := env("MASH_RELAY_WINDOW"); v != "" {
		d, err := relayWindow(v)
		if err != nil {
			return c, fmt.Errorf("mash: MASH_RELAY_WINDOW %w", err)
		}
		c.RelayWindow = d
	}

	// Each timeout's effective raw value is chosen first (env, else file, else the default) and validated once, so an
	// invalid file value under a valid env override loads (codex M2 on #121); an invalid effective one names its source.
	for _, p := range []struct {
		dst          *time.Duration
		file, envKey string
		fileKey      string
	}{{&c.OpenTimeout, f.OpenTimeout, "MASH_OPEN_TIMEOUT", "open_timeout"}, {&c.CallTimeout, f.CallTimeout, "MASH_CALL_TIMEOUT", "call_timeout"},
		{&c.LeaseRetry, f.LeaseRetry, "MASH_LEASE_RETRY", "lease_retry"}, {&c.TransientBudget, f.TransientBudget, "MASH_TRANSIENT_BUDGET", "transient_budget"},
		{&c.TransientMax, f.TransientMax, "MASH_TRANSIENT_MAX", "transient_max"}, {&c.CLITimeout, f.CLITimeout, "MASH_CLI_TIMEOUT", "cli_timeout"},
		{&c.AdminTimeout, f.AdminTimeout, "MASH_ADMIN_TIMEOUT", "admin_timeout"}} {
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
	// The name rule is loaded for every invocation, with or without an endpoint name: mashctl has none.
	if err := c.loadNameRule(lookup, f, dir, home); err != nil {
		return c, err
	}
	if c.Name == "" {
		return c, nil
	}
	// A key that is present but empty is invalid, not absent: in config.json (unless the environment overrides it) and
	// as MASH_AGENT_TOKEN itself, which must not fall back to the file or to the legacy identity.
	if agentEnvSet && env("MASH_AGENT_TOKEN") == "" {
		return c, ErrAgentTokenEmpty
	}
	if fileAgentSet && c.AgentToken == "" {
		return c, errors.New("mash: config.json \"agent_token\" must be a non-empty string: give an agent token, or remove the key for the legacy identity")
	}
	if c.AgentToken == "" && !ValidName(c.Name) {
		return c, fmt.Errorf("mash: MASH_NAME %q: use 1-48 lower-case letters, digits and single hyphens, starting with a letter", c.Name)
	}
	if c.URL == "" {
		if dir == "" {
			return c, ErrNoConfigDir
		}
		return c, ErrNoURL
	}
	if c.Owner == "" {
		if dir == "" {
			return c, ErrNoConfigDir
		}
		return c, ErrNoOwner
	}
	if c.AgentToken == "" && !ValidName(c.Owner) {
		return c, fmt.Errorf("mash: owner %q: use 1-48 lower-case letters, digits and single hyphens", c.Owner)
	}
	if c.AgentToken != "" {
		if _, err := c.Identity(); err != nil {
			return c, fmt.Errorf("mash: %s: %w", identitySetting(err), err)
		}
	}
	if c.Creds == "" {
		if dir == "" {
			return c, ErrNoConfigDir
		}
		c.Creds = c.Find(c.CredsName() + ".creds")
	}
	return c, nil
}

// Identity is the endpoint's identity: the legacy one of Owner and Name when AgentToken is empty, else the three-token
// identity of AgentToken, Owner and Name. The error of sap is returned as is; it names the rule that failed.
func (c Config) Identity() (sap.EndpointIdentity, error) {
	if c.Name == "" {
		return sap.EndpointIdentity{}, errors.New("mash: no identity: no endpoint name is configured (MASH_NAME)")
	}
	if c.AgentToken == "" {
		return sap.Legacy(c.Owner, c.Name)
	}
	return sap.New(c.AgentToken, c.Owner, c.Name)
}

// CredsName is the base name of the endpoint's default creds file: the name for a legacy endpoint (as always), the
// flat identity (agent_owner_name) for a three-token one, so two identities that share a name token do not share a file.
func (c Config) CredsName() string {
	if c.AgentToken != "" {
		if id, err := c.Identity(); err == nil {
			return id.Flat()
		}
	}
	return c.Name
}

// identitySetting names the setting a sap identity error is about.
func identitySetting(err error) string {
	m := err.Error()
	switch {
	case strings.HasPrefix(m, "sap: agent token"), strings.HasPrefix(m, "sap: agent "):
		return "MASH_AGENT_TOKEN (config.json \"agent_token\")"
	case strings.HasPrefix(m, "sap: owner"):
		return "owner (config.json \"owner\" or MASH_OWNER)"
	case strings.HasPrefix(m, "sap: name"):
		return "MASH_NAME"
	}
	return "MASH_AGENT_TOKEN, owner and MASH_NAME"
}

// recordSources fills Source and FileValues from the file f and the environment, as Load applied them: an environment
// variable that is set and not empty wins, else a value the file set, else the default.
func (c *Config) recordSources(f file, env func(string) string, home string) {
	c.Source, c.FileValues = map[string]string{}, map[string]string{}
	fileVals := map[string]string{"url": f.URL, "owner": f.Owner, "agent_token": agentTokenOf(f.AgentToken), "admin_creds": ExpandHome(f.AdminCreds, home),
		"tls_ca": f.TLSCA, "tls_cert": f.TLSCert, "tls_key": f.TLSKey}
	if f.InsecureLoopback != nil {
		fileVals["insecure_loopback"] = strconv.FormatBool(*f.InsecureLoopback)
	}
	if f.RequireTLS != nil {
		fileVals["require_tls"] = strconv.FormatBool(*f.RequireTLS)
	}
	for key, ev := range SourceKeys {
		fv := fileVals[key]
		if fv != "" {
			c.FileValues[key] = fv
		}
		switch {
		case env(ev) != "":
			c.Source[key] = ev
		case fv != "":
			c.Source[key] = "config.json"
		case key == "insecure_loopback" || key == "require_tls":
			c.Source[key] = "default"
		default:
			c.Source[key] = "unset"
		}
	}
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

// DefaultRelayWindow is Config.RelayWindow when nothing sets it.
const DefaultRelayWindow = 60 * time.Second

// relayWindow parses the relay trace window: a Go duration string from 1s to 1h.
func relayWindow(v string) (time.Duration, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Second || d > time.Hour {
		return 0, fmt.Errorf("%q is not a duration from 1s to 1h (for example \"60s\" or \"5m\")", v)
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
// (an entry with no scheme is nats://) to a host that is not loopback, and "" otherwise. SAP identity assumes a
// verified connection, which plain text is not. The warning names the host, never the URL's userinfo.
func PlainTextWarning(url string) string {
	scheme, host, ok := natsauth.PlainRemote(url)
	if !ok {
		return ""
	}
	return fmt.Sprintf("is plain %s:// to a non-loopback host (%s): SAP identity assumes a verified connection. Next: use tls:// with the server's certificate (tls_ca for a private CA); set MASH_REQUIRE_TLS=1 to make mash refuse it", scheme, host)
}

// RejectedArg is what an error may print of a command-line argument it rejects without having validated it: the
// flag's name when the argument is a flag (up to any "="), else only the word "argument". A positional value is never
// printed, because a mistyped one may be a secret (a seed pasted into the wrong place).
func RejectedArg(a string) string {
	if !strings.HasPrefix(a, "-") {
		return "argument"
	}
	name, _, _ := strings.Cut(a, "=")
	if len(name) > 32 {
		name = name[:32]
	}
	return "option " + strconv.Quote(name)
}

// agentTokenOf is the value of a present agent_token key: the string, or "" for null, an empty string or any other
// JSON type, which Load then refuses.
func agentTokenOf(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

// nameRuleFileKey is the value of a present name_rule_file key: set is false when the key is absent. A key that is
// present must be a non-empty string.
func nameRuleFileKey(raw json.RawMessage) (path string, set bool, err error) {
	if raw == nil {
		return "", false, nil
	}
	var v string
	if json.Unmarshal(raw, &v) != nil || v == "" {
		return "", true, errors.New("mash: config.json \"name_rule_file\" must be a non-empty string: give the path of a name-rule file, or remove the key for no rule")
	}
	return v, true, nil
}

// nameRuleRequiredKey is the value of a present name_rule_required key (false when absent): only the JSON booleans.
func nameRuleRequiredKey(raw json.RawMessage) (bool, error) {
	switch strings.TrimSpace(string(raw)) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, errors.New("mash: config.json \"name_rule_required\" must be true or false")
}

// loadNameRule resolves name_rule_file and name_rule_required (the environment wins over the file key for each, and a
// value that is set but empty is invalid, not absent), loads the rule file and applies the requirement.
func (c *Config) loadNameRule(lookup Lookup, f file, dir, home string) error {
	path := ""
	if v, set := lookup("MASH_NAME_RULE_FILE"); set {
		if v == "" {
			return errors.New("mash: MASH_NAME_RULE_FILE is set but empty: give the path of a name-rule file, or unset it for no rule")
		}
		path = ExpandHome(v, home)
	} else {
		v, set, err := nameRuleFileKey(f.NameRuleFile)
		if err != nil {
			return err
		}
		if set {
			path = inDir(dir, ExpandHome(v, home))
		}
	}
	required := false
	if v, set := lookup("MASH_NAME_RULE_REQUIRED"); set {
		switch v {
		case "true":
			required = true
		case "false":
		default:
			return fmt.Errorf("mash: MASH_NAME_RULE_REQUIRED=%q is not true or false", v)
		}
	} else {
		var err error
		if required, err = nameRuleRequiredKey(f.NameRuleRequired); err != nil {
			return err
		}
	}
	if path == "" {
		if required {
			return ErrNameRuleRequired
		}
		return nil
	}
	// Both forms end as one absolute path, so two endpoints that print the same path loaded the same file: the
	// environment's relative path is taken from the current directory, the file key's from the config directory.
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("mash: name rule file %s: %w", path, err)
	}
	r, err := namerule.Load(abs)
	if err != nil {
		return err
	}
	c.NameRule, c.NameRulePath = r, abs
	return nil
}
