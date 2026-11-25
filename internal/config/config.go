// Package config loads mash's settings: config.json in the config directory (see ConfigDir), then the
// environment, which wins. An empty endpoint name means the endpoint manager
// runs inert (the plugin can be enabled in sessions that have no identity).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/namerule"
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
	URL   string // the server address, or the comma-joined list of them (MASH_SERVER / "servers")
	Owner string // SAP owner token: the owner part of the endpoint address (from the one installed endpoint too)
	// ExplicitOwner is the owner mashctl acts for: the owner part of an endpoint that was set (--endpoint, MASH_ENDPOINT,
	// config.json "endpoint"), else config.json "owner". It is never taken from an installed endpoint's creds file.
	ExplicitOwner   string
	Home            string        // the home folder the configuration was loaded for ("" when unknown): paths are shown with ~ for it
	StateDir        string        // $MASH_STATE_DIR, else $XDG_STATE_HOME/mash, else ~/.local/state/mash: holds spool/ and hook.log
	Name            string        // the endpoint's instance token: the name part of the endpoint address; empty = inert
	Group           string        // the endpoint's group token: the group part of the endpoint address; no default
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
	// WakeCount is how many waiting messages are more than the session is left with (MASH_WAKE_COUNT / "wake_count": a
	// whole number, default 15, 0 = off). WakeAge is how long the oldest waiting message may wait
	// (MASH_WAKE_AGE / "wake_age": a whole number and m, h or d, or a Go duration, default 1h; 0 = off, WakeAge zero).
	// WakeAgeText is the age as it was written ("1h", "0"). The wake itself is serve's; mash inbox reports against them.
	WakeCount int
	WakeAge   time.Duration
	// WakeErr is why wake_count or wake_age is not valid (nil when both are); the setting is then at its default.
	// Only mash serve refuses it, every other command ignores it.
	WakeErr      error
	WakeAgeText  string
	AdminTimeout time.Duration // total deadline of one `mashctl` command (zero = 30s)
	AdminCreds   string        // admin credentials for `mashctl`
	// SigningKeyPub is config.json "signing_key_pub": the PUBLIC key of the account signing key `mashctl init` was given
	// (never a seed), "" when none was configured. A key store that has lost signing.nk and signing.pub is told apart
	// from one that never had a signing key by it.
	SigningKeyPub string
	SysCreds      string // config.json "system_creds": the system account's creds file, for `mashctl` (empty = none set)
	TLSCA         string // MASH_CA / "ca": PEM file of the CA that signed the server's certificate (empty = system roots)
	TLSCert       string // MASH_TLS_CERT / "tls_cert": PEM client certificate, for a server that requires one; set with TLSKey
	TLSKey        string // MASH_TLS_KEY / "tls_key": PEM private key of TLSCert
	// SkipEncryption is the one override of the TLS requirement (REQ-014): every connection mash and mashctl make
	// uses tls:// or wss://, to any host (loopback included), unless it is set. Then a plain nats:// or ws:// URL is
	// connected. It comes from the environment variable SkipEncryptionEnv only: no flag, no config.json key. Every
	// command prints SkipEncryptionWarning on standard error while it is set (WarnSkipEncryption).
	SkipEncryption bool   // MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1: allow plain-text connections to any host (default false)
	LaunchToken    string // MASH_LAUNCH_TOKEN: names the launch of the agent this process serves; recorded in the lease, optional
	Host           string // MASH_HOST / "host": the agent host adapter ("" = claude)
	SpoolFromStart bool   // MASH_SPOOL_FROM_START / "spool_from_start": spool mail from the first message, before any hook has run
	SpoolRequired  bool   // MASH_SPOOL_REQUIRED / "spool_required": fail closed: mail the spool cannot take is held unacked and reported, never delivered by the channel (default off)
	// AcceptUnverified delivers mail whose trust is not verified, with its label (MASH_ACCEPT_UNVERIFIED /
	// "accept_unverified" / mash serve --accept-unverified). Default off: such mail is dropped and counted, and neither
	// the session, the inbox listing nor mash read shows it.
	AcceptUnverified bool
	Dir              string       // the config directory (ConfigDir); "" when none can be found
	Limits           store.Limits // config.json "limits": inbox and bucket caps (defaults: store.DefaultLimits)

	// The name-acceptance rule (docs/name-rule.md). Load reads the file once; a file that cannot be read or is invalid
	// fails Load. NameRule is nil, and accepts everything, when no file is named.
	NameRule     *namerule.Rule // MASH_NAME_RULE_FILE / "name_rule_file": the loaded rule
	NameRulePath string         // the absolute path it was loaded from ("" = none)

	// Provenance of the settings that decide which server a command acts on, for a command to say what it is about to
	// act on (it informs; it changes no precedence). Keys: SourceKeys.
	File       string            // the config.json that was read ("" when there is none)
	Source     map[string]string // key -> where its value came from: an environment variable name (MASH_SERVER), "flag:--server", "config.json", "default" or "unset"
	FileValues map[string]string // key -> what config.json itself said (only keys it set), to compare with the effective value
}

// SourceKeys are the keys of Config.Source and Config.FileValues, and the environment variable each one is read from.
var SourceKeys = map[string]string{
	"endpoint": "MASH_ENDPOINT", "servers": "MASH_SERVER", "admin_creds": "MASH_ADMIN_CREDS",
	"ca": "MASH_CA", "tls_cert": "MASH_TLS_CERT", "tls_key": "MASH_TLS_KEY",
}

// FlagSourcePrefix is the prefix of the Lookup keys with which a Lookup that carries global flag values (see
// cliframe.Global.Shadow) says which flag supplied the environment variable named after the prefix: looking up
// FlagSourcePrefix+"MASH_SERVER" answers "--server" when the value of MASH_SERVER came from that flag. The NUL byte
// keeps it out of any real environment.
const FlagSourcePrefix = "\x00flag:"

type file struct {
	Endpoint   string          `json:"endpoint"` // owner.group.name
	Owner      string          `json:"owner"`    // read by mashctl only (until init --config lands): the owner it acts for
	Servers    json.RawMessage `json:"servers"`  // a JSON array of strings, or one string (a comma list)
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

	WakeCount json.RawMessage `json:"wake_count"` // kept raw: a whole number only
	WakeAge   json.RawMessage `json:"wake_age"`   // kept raw: a string such as "1h", or the number 0

	AdminCreds string `json:"admin_creds"`
	SysCreds   string `json:"system_creds"`
	SigningPub string `json:"signing_key_pub"` // the public key of the configured signing key; written by mashctl init
	Host       string `json:"host"`

	CA      string `json:"ca"`
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`

	SpoolFromStart *bool `json:"spool_from_start"`
	SpoolRequired  *bool `json:"spool_required"`

	AcceptUnverified *bool `json:"accept_unverified"`

	OldInsecurePlaintext json.RawMessage `json:"insecure_plaintext"` // read only to refuse it: the key is gone (SkipEncryptionEnv)

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

// CodeGroupMissing is the code of the refusal for an endpoint with no group.
const CodeGroupMissing = "group_missing"

// ErrNoURL and ErrNoOwner are what Load returns once the whole configuration is resolved and a required field is
// still empty; any other error stopped loading before the environment was fully applied.
var (
	// ErrNameRuleRequired is Load's error for a required name rule with no file loaded.
	ErrNameRuleRequired = errors.New("mash: a name rule is required (name_rule_required or MASH_NAME_RULE_REQUIRED is true) but no rule file is set: set name_rule_file (MASH_NAME_RULE_FILE) to a name-rule file")
	ErrNoURL            = errors.New("mash: no server: set MASH_SERVER, --server, or \"servers\" in config.json")
	ErrNoOwner          = errors.New("mash: no endpoint: set MASH_ENDPOINT, --endpoint, or install credentials for one endpoint")
	// ErrGroupMissing is Load's error, and Identity's, for an endpoint name with no group. Compare with errors.Is. Its
	// text names the endpoint setting; the CLI reports CodeGroupMissing.
	ErrGroupMissing = errors.New("mash: no endpoint: set MASH_ENDPOINT, --endpoint, or install credentials for one endpoint (an endpoint address is owner.group.name, for example alice.dev.design)")
)

// SeveralEndpointsError is Load's error when no endpoint is set and more than one endpoint's creds file is installed
// in the config directory, so none can be chosen. Addresses are the installed endpoints, sorted. Match it with
// errors.As.
type SeveralEndpointsError struct{ Addresses []string }

func (e *SeveralEndpointsError) Error() string {
	return "more than one endpoint is installed here: " + strings.Join(e.Addresses, ", ")
}

// ErrSeveralEndpoints is the documented name of the error: use errors.As with a *SeveralEndpointsError, or
// errors.Is(err, ErrSeveralEndpoints).
var ErrSeveralEndpoints = &SeveralEndpointsError{}

// Is makes errors.Is(err, ErrSeveralEndpoints) true for any *SeveralEndpointsError.
func (e *SeveralEndpointsError) Is(target error) bool { return target == ErrSeveralEndpoints }

// credsFileRE is an installed endpoint's creds file: owner.group.name.creds, each part 1-63 of [a-z0-9-].
var credsFileRE = regexp.MustCompile(`^([a-z0-9-]{1,63})\.([a-z0-9-]{1,63})\.([a-z0-9-]{1,63})\.creds$`)

// installedEndpoints lists, sorted, the addresses of the regular files directly in dir named owner.group.name.creds that
// are owned by the current user and have mode 0600; any other file does not count.
func installedEndpoints(dir string) []string {
	return InstalledEndpoints(dir)
}

// InstalledEndpoints is installedEndpoints for callers outside the package (mash join).
func InstalledEndpoints(dir string) []string {
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		m := credsFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, e.Name()))
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".creds"))
	}
	sort.Strings(out)
	return out
}

// serverList reads the config.json "servers" value: a JSON array of strings or one string; each string may be a comma
// list. Empty items are dropped.
func serverList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return splitServers(one), nil
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		var out []string
		for _, m := range many {
			out = append(out, splitServers(m)...)
		}
		return out, nil
	}
	return nil, errors.New("mash: config.json \"servers\" must be a JSON array of strings or one string (a comma list)")
}

// splitServers splits a comma list of server addresses, trimming each and dropping empty items.
func splitServers(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SkipEncryptionEnv is the one way to allow a plain-text (nats:// or ws://) connection, for mash and mashctl alike. Its
// only value is 1 (empty and 0 are off). There is no flag and no config.json key.
const SkipEncryptionEnv = "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"

// SkipEncryptionWarning is the line every command of mash and mashctl prints on standard error, once, at start,
// while SkipEncryptionEnv is on.
const SkipEncryptionWarning = "warning: no encryption to the server (" + SkipEncryptionEnv + ")"

// SkipEncryptionError is a refusal about the encryption override (a removed name, a bad value). It is a command-line
// or configuration error: the commands exit 2 on it.
type SkipEncryptionError struct{ msg, hint string }

func (e *SkipEncryptionError) Error() string { return "mash: " + e.msg }

// Msg is the error without its "mash: " prefix.
func (e *SkipEncryptionError) Msg() string { return e.msg }

// Hint is the one-line fix for the refusal. It never points at a command, since every command refuses until the fix is made.
func (e *SkipEncryptionError) Hint() string { return e.hint }

// SkipEncryptionFrom reads SkipEncryptionEnv: "" and "0" are off, "1" is on, anything else is an error naming it. The
// removed name MASH_INSECURE_PLAINTEXT is refused when it is set at all.
func SkipEncryptionFrom(lookup Lookup) (bool, error) {
	if old, _ := lookup("MASH_INSECURE_PLAINTEXT"); old != "" {
		return false, &SkipEncryptionError{msg: "MASH_INSECURE_PLAINTEXT is no longer read; set " + SkipEncryptionEnv + "=1 instead", hint: "unset MASH_INSECURE_PLAINTEXT; use " + SkipEncryptionEnv + "=1"}
	}
	v, _ := lookup(SkipEncryptionEnv)
	switch v {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, &SkipEncryptionError{msg: fmt.Sprintf("%s=%q is not 1 (set it to 1 to turn encryption off, or unset it)", SkipEncryptionEnv, v), hint: "set " + SkipEncryptionEnv + "=1 or unset it"}
}

// WarnSkipEncryption prints SkipEncryptionWarning to stderr when SkipEncryptionEnv is on. A command calls it once,
// first, before it does anything else.
func WarnSkipEncryption(lookup Lookup, stderr io.Writer) {
	// Asked of the variable itself, not of SkipEncryptionFrom: a command that is refused (the removed name set beside it)
	// still says encryption is off, since the warning is first on every command, errors included.
	if v, _ := lookup(SkipEncryptionEnv); v == "1" {
		fmt.Fprintln(stderr, SkipEncryptionWarning)
	}
}

// CheckSkipEncryption is the one check of the encryption override, made by each tool at its entry point before any
// command dispatches: the removed variable, a bad value, and the removed config.json key insecure_plaintext are
// refused. The file is read only for that key (a missing, unreadable or malformed file is the commands' business); the
// directory is the one Load uses. It returns nil when there is nothing to refuse.
func CheckSkipEncryption(lookup Lookup, home string) *SkipEncryptionError {
	var se *SkipEncryptionError
	if _, err := SkipEncryptionFrom(lookup); errors.As(err, &se) {
		return se
	}
	env := func(k string) string { v, _ := lookup(k); return v }
	if dir := ConfigDir(env, home); dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
			var f file
			if json.Unmarshal(raw, &f) == nil && errors.As(refuseOldKey(f), &se) {
				return se
			}
		}
	}
	return nil
}

// refuseOldKey refuses the removed config.json key insecure_plaintext.
func refuseOldKey(f file) error {
	if f.OldInsecurePlaintext != nil {
		return &SkipEncryptionError{msg: "insecure_plaintext in config.json is no longer read; set " + SkipEncryptionEnv + "=1 instead", hint: "remove insecure_plaintext from config.json; use " + SkipEncryptionEnv + "=1"}
	}
	return nil
}

// ValidateFile reports whether raw is a config.json that Load would accept as far as the file itself goes: it must
// parse into the known fields with the right types, and "idle_wake" must be a valid duration. The environment is not
// consulted.
func ValidateFile(raw []byte) error {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if err := refuseOldKey(f); err != nil {
		return err
	}
	if _, err := serverList(f.Servers); err != nil {
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

// ConfigDir is the one place the whole binary looks for config.json, policy.md and default creds. In order:
//  1. an absolute $MASH_CONFIG_DIR (Load rejects a relative one);
//  2. an absolute $XDG_CONFIG_HOME/mash;
//  3. $HOME/.config/mash.
//
// A relative XDG_CONFIG_HOME is ignored. With no home it is the XDG directory if one is set, else "": never a
// relative path. No other location is read.
func ConfigDir(env func(string) string, home string) string {
	if d, _ := explicitDir(env, "MASH_CONFIG_DIR"); d != "" {
		return d
	}
	if x := env("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "mash")
	}
	if home != "" {
		return filepath.Join(home, ".config", "mash")
	}
	return ""
}

// Absent reports whether p is definitely not there: Lstat says not-exist or not-a-directory. Anything else, a symlink
// (even a dangling or inaccessible one) or a permission or I/O error, counts as present so the caller's read fails
// loudly instead of silently picking a file from another directory.
func Absent(p string) bool {
	_, err := os.Lstat(p)
	return err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR))
}

// Find is the path of name (policy.md, a default creds file) in Dir; "" when there is no Dir.
func (c Config) Find(name string) string {
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

// HomeDir is the home directory: $HOME as env gives it, else the one the system names, else "" when neither is known.
func HomeDir(env func(string) string) string {
	if h := env("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// Tilde writes the home directory at the start of p as "~", the inverse of ExpandHome. p is returned as it is when it
// is not under the home directory or the home is unknown.
func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return TildeIn(p, home)
}

// TildeIn is Tilde for a home directory the caller already knows.
func TildeIn(p, home string) string {
	if home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return p
}

// ExpandHome expands an initial "~" or "~/" in p to home. "~user/" is left alone, and so is p when home is unknown.
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
// home when that is unset ("" when unknown). An empty value of any variable reads as unset.
func Load(lookup Lookup, home string) (Config, error) { return loadPicking(lookup, home, true) }

// LoadNamedOnly is Load without the one-installed-endpoint pick: the endpoint is the one MASH_ENDPOINT (or --endpoint)
// or config.json "endpoint" names, and with none named the Config is inert (Name empty, Source["endpoint"] "unset"),
// whatever creds are installed. A session (mash serve, mash hook) uses it: it acts only for an endpoint it was told.
// The commands you type use Load.
func LoadNamedOnly(lookup Lookup, home string) (Config, error) {
	return loadPicking(lookup, home, false)
}

func loadPicking(lookup Lookup, home string, autoPick bool) (Config, error) {
	env := func(k string) string { v, _ := lookup(k); return v }
	skip, err := SkipEncryptionFrom(lookup)
	if err != nil {
		return Config{}, err
	}
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
	c := Config{WakeCount: DefaultWakeCount, WakeAge: DefaultWakeAge, WakeAgeText: DefaultWakeAgeText, SettleMS: 100, BatchMsgs: 10, BatchBytes: 16384, IdleWake: 45 * time.Second, RelayWindow: DefaultRelayWindow, OpenTimeout: DefaultOpenTimeout, CallTimeout: DefaultCallTimeout, Dir: dir, Home: home, StateDir: state, Limits: store.DefaultLimits()}

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
		if err := refuseOldKey(f); err != nil {
			return c, err
		}
		c.File = filepath.Join(dir, "config.json")
	case !errors.Is(err, os.ErrNotExist):
		return c, fmt.Errorf("mash: reading config: %w", err)
	}
	// Relative paths in the file are relative to the config directory, not to wherever mash was started.
	f.AdminCreds = inDir(dir, ExpandHome(f.AdminCreds, home))
	c.SysCreds = inDir(dir, ExpandHome(f.SysCreds, home))
	c.SigningKeyPub = f.SigningPub
	f.CA, f.TLSCert, f.TLSKey = inDir(dir, f.CA), inDir(dir, f.TLSCert), inDir(dir, f.TLSKey)
	fileServers, err := serverList(f.Servers)
	if err != nil {
		return c, err
	}
	c.URL, c.Creds, c.AdminCreds, c.Host = strings.Join(fileServers, ","), f.Creds, f.AdminCreds, f.Host
	c.TLSCA, c.TLSCert, c.TLSKey = f.CA, f.TLSCert, f.TLSKey
	if f.SpoolFromStart != nil {
		c.SpoolFromStart = *f.SpoolFromStart
	}
	if f.SpoolRequired != nil {
		c.SpoolRequired = *f.SpoolRequired
	}
	if f.AcceptUnverified != nil {
		c.AcceptUnverified = *f.AcceptUnverified
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

	str := func(dst *string, key string) {
		if v := env(key); v != "" {
			*dst = v
		}
	}
	if l := splitServers(env("MASH_SERVER")); len(l) > 0 {
		c.URL = strings.Join(l, ",")
	}
	str(&c.Creds, "MASH_CREDS")
	str(&c.AdminCreds, "MASH_ADMIN_CREDS")
	c.Creds, c.AdminCreds = ExpandHome(c.Creds, home), ExpandHome(c.AdminCreds, home)
	// The endpoint: MASH_ENDPOINT, else config.json "endpoint", else the one installed endpoint (the creds file named for
	// it in the config directory). An address that does not parse fails Load after the rest is resolved.
	endpoint, endpointSetting := env("MASH_ENDPOINT"), "MASH_ENDPOINT"
	if endpoint == "" {
		endpoint, endpointSetting = f.Endpoint, `config.json "endpoint"`
	}
	var endpointErr error
	autoPicked := false
	c.ExplicitOwner = f.Owner
	switch {
	case endpoint != "":
		if a, err := sap.ParseAddress(endpoint); err != nil {
			endpointErr = fmt.Errorf("mash: %s: %w", endpointSetting, err)
		} else {
			c.Owner, c.Group, c.Name = a.Owner, a.Group, a.Instance
			c.ExplicitOwner = a.Owner
		}
	case c.Creds != "":
		// A creds path was set (--creds, MASH_CREDS, "creds"): installed creds must not pick the address, or one
		// endpoint's address would be signed with another's creds.
	case !autoPick:
	default:
		switch found := installedEndpoints(dir); len(found) {
		case 0:
		case 1:
			autoPicked = true
			if a, err := sap.ParseAddress(found[0]); err != nil {
				endpointErr = fmt.Errorf("mash: the installed endpoint %s: %w", found[0], err)
			} else {
				c.Owner, c.Group, c.Name = a.Owner, a.Group, a.Instance
			}
		default:
			endpointErr = &SeveralEndpointsError{Addresses: found}
		}
	}
	c.recordSources(f, fileServers, lookup, home, autoPicked)
	str(&c.LaunchToken, "MASH_LAUNCH_TOKEN")
	str(&c.Host, "MASH_HOST")
	str(&c.TLSCA, "MASH_CA")
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

	switch v := env("MASH_ACCEPT_UNVERIFIED"); v {
	case "":
	case "1", "true":
		c.AcceptUnverified = true
	case "0", "false":
		c.AcceptUnverified = false
	default:
		return c, fmt.Errorf("mash: MASH_ACCEPT_UNVERIFIED=%q is not 1, true, 0 or false", v)
	}

	c.SkipEncryption = skip

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

	// The wake settings are serve's: a bad one is kept in WakeErr (the defaults stay), so only serve refuses it.
	c.WakeErr = c.loadWake(f, env)

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
	if endpointErr != nil {
		return c, endpointErr
	}
	if c.Name == "" {
		return c, nil
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
	if c.Group == "" {
		return c, ErrGroupMissing
	}
	if _, err := c.Identity(); err != nil {
		return c, fmt.Errorf("mash: %s: %w", endpointSetting, err)
	}
	if c.Creds == "" {
		if dir == "" {
			return c, ErrNoConfigDir
		}
		c.Creds = c.Find(c.CredsName() + ".creds")
	}
	return c, nil
}

// Identity is the endpoint's address: Owner, Group and Name (the instance). It returns ErrGroupMissing when Name is
// set and Group is not (there is no default group). The error of sap is returned as is; it names the rule that failed.
func (c Config) Identity() (sap.Address, error) {
	if c.Name == "" {
		return sap.Address{}, errors.New("mash: no identity: no endpoint is configured (MASH_ENDPOINT)")
	}
	if c.Group == "" {
		return sap.Address{}, ErrGroupMissing
	}
	return sap.NewAddress(c.Owner, c.Group, c.Name)
}

// CredsName is the base name of the endpoint's default creds file: owner.group.instance, so two endpoints that share an
// instance token do not share a file.
func (c Config) CredsName() string {
	if id, err := c.Identity(); err == nil {
		return id.String()
	}
	return c.Name
}

// recordSources fills Source and FileValues from the file f and the environment, as Load applied them: an environment
// variable that is set and not empty wins (recorded as "flag:--name" when a global flag supplied it, see
// FlagSourcePrefix), else a value the file set, else the one installed endpoint, else the default.
func (c *Config) recordSources(f file, fileServers []string, lookup Lookup, home string, autoPicked bool) {
	env := func(k string) string { v, _ := lookup(k); return v }
	c.Source, c.FileValues = map[string]string{}, map[string]string{}
	fileVals := map[string]string{"endpoint": f.Endpoint, "servers": strings.Join(fileServers, ","), "admin_creds": ExpandHome(f.AdminCreds, home),
		"ca": f.CA, "tls_cert": f.TLSCert, "tls_key": f.TLSKey}
	for key, ev := range SourceKeys {
		fv := fileVals[key]
		if fv != "" {
			c.FileValues[key] = fv
		}
		switch {
		case env(ev) != "":
			c.Source[key] = ev
			if fl, ok := lookup(FlagSourcePrefix + ev); ok && fl != "" {
				c.Source[key] = "flag:" + fl
			}
		case fv != "":
			c.Source[key] = "config.json"
		case key == "endpoint" && autoPicked:
			c.Source[key] = "installed creds"
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

// The defaults of the wake settings.
const (
	DefaultWakeCount   = 15
	DefaultWakeAge     = time.Hour
	DefaultWakeAgeText = "1h"
)

// wakeUnits are the units of wake_age.
var wakeUnits = map[byte]time.Duration{'m': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour}

// loadWake reads wake_count and wake_age: the environment wins over config.json, and the effective value is validated
// once, so an invalid file value under a valid environment value loads and an invalid effective one names its source.
func (c *Config) loadWake(f file, env func(string) string) error {
	// Both are read: a bad wake_count does not stop a valid wake_age from being kept; the first error is reported.
	cerr := c.loadWakeCount(f, env)
	if aerr := c.loadWakeAge(f, env); cerr == nil {
		return aerr
	}
	return cerr
}

func (c *Config) loadWakeCount(f file, env func(string) string) error {
	count, from := env("MASH_WAKE_COUNT"), "MASH_WAKE_COUNT"
	if count == "" && len(f.WakeCount) > 0 {
		count, from = strings.TrimSpace(string(f.WakeCount)), `config.json "wake_count"`
		if count == "" {
			count = "null"
		}
	}
	if count != "" {
		n, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil || n < 0 || strings.TrimSpace(count) != strconv.Itoa(n) {
			return fmt.Errorf("mash: %s %s is not a whole number of messages, 0 or more (0 turns the wake off)", from, quoteSetting(count))
		}
		c.WakeCount = n
	}
	return nil
}

func (c *Config) loadWakeAge(f file, env func(string) string) error {
	age, from := env("MASH_WAKE_AGE"), "MASH_WAKE_AGE"
	if age == "" && len(f.WakeAge) > 0 {
		from = `config.json "wake_age"`
		raw := strings.TrimSpace(string(f.WakeAge))
		var s string
		switch {
		case json.Unmarshal(f.WakeAge, &s) == nil:
			age = s
			if age == "" {
				age = `""`
			}
		case raw == "0":
			age = "0"
		default:
			return fmt.Errorf("mash: %s %s is not a duration (for example \"30m\", \"1h\" or \"2d\") or 0", from, raw)
		}
	}
	if age != "" {
		d, err := wakeAge(age)
		if err != nil {
			return fmt.Errorf("mash: %s %w", from, err)
		}
		c.WakeAge, c.WakeAgeText = d, strings.TrimSpace(age)
	}
	return nil
}

// ValidWakeCount reports whether v is a wake count: any non-negative whole number (the rule of MASH_WAKE_COUNT, wake_count
// and serve's --wake-count; no upper limit).
func ValidWakeCount(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= 0 && strconv.Itoa(n) == v
}

// ValidWakeAge reports whether v is a wake age: 0, or a whole number and m, h or d any positive length (the rule of
// MASH_WAKE_AGE, wake_age and serve's --wake-age).
func ValidWakeAge(v string) bool {
	_, err := wakeAge(v)
	return err == nil
}

func quoteSetting(v string) string {
	if strings.HasPrefix(strings.TrimSpace(v), `"`) {
		return strings.TrimSpace(v)
	}
	return strconv.Quote(strings.TrimSpace(v))
}

// wakeAge parses the wake age: 0 (off), a whole number and m, h or d (minutes, hours, days), or a Go duration such as
// 90s or 1h30m; any positive length.
func wakeAge(v string) (time.Duration, error) {
	bad := fmt.Errorf("%q is not a duration (for example \"30m\", \"1h\" or \"2d\") or 0", v)
	t := strings.TrimSpace(v)
	if t == "0" {
		return 0, nil
	}
	if len(t) >= 2 {
		if unit := wakeUnits[t[len(t)-1]]; unit != 0 {
			digits := t[:len(t)-1]
			if n, err := strconv.Atoi(digits); err == nil && n >= 1 && strconv.Itoa(n) == digits && time.Duration(n) <= math.MaxInt64/unit {
				return time.Duration(n) * unit, nil
			}
		}
	}
	if d, err := time.ParseDuration(t); err == nil && d > 0 {
		return d, nil
	}
	return 0, bad
}
