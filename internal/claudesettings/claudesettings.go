// Package claudesettings reads Claude Code's managed settings, read-only, to say whether mash is on the channel
// allowlist. It never writes a file and never returns file contents: only key names and booleans leave it. Its
// reading is advice for diagnosis; /status in Claude Code is the authority.
//
// Every Claude Code path, rank rule and key name lives in this file, because they are Claude Code internals that can
// move. Where a rule cannot be read from the files, the answer is UNKNOWN or "present only", never a wrong OK.
package claudesettings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source names, shown to the user.
const (
	ServerName = "server-managed settings"
	FileName   = "local managed settings file"
	MergedName = "managed sources merged (managedSourcesBehavior: merge)"
)

// Paths and keys, with where each comes from.
const (
	// The server-managed cache: https://code.claude.com/docs/en/server-managed-settings#fetch-and-caching-behavior
	// ("the cache at ~/.claude/remote-settings.json"). Claude Code keeps it under CLAUDE_CONFIG_DIR when that is set;
	// this package reads only the default location.
	serverCacheRel = ".claude/remote-settings.json"

	// The endpoint-managed file and drop-in directory:
	// https://code.claude.com/docs/en/managed-settings#choose-a-delivery-mechanism and #split-a-file-based-policy-across-teams
	// (managed-settings.json first, then every *.json in managed-settings.d in alphabetical order; hidden files and
	// non-.json files are ignored).
	managedFile  = "managed-settings.json"
	managedDropD = "managed-settings.d"

	// The user settings file, whose env block may also set variables for the session.
	userSettingsRel = ".claude/settings.json"

	// Keys: https://code.claude.com/docs/en/settings-reference#settings-index (both are managed-scope keys).
	keyChannelsEnabled = "channelsEnabled"
	keyAllowedPlugins  = "allowedChannelPlugins"
	keyEnv             = "env"

	// The two control keys. Every other key is a "policy key", and the highest-ranked source with one wins:
	// https://code.claude.com/docs/en/managed-settings#how-claude-code-combines-managed-sources
	keyMergeBehavior = "managedSourcesBehavior"
	keyWSLInherits   = "wslInheritsWindowsSettings"

	// The env variable that limits an MCP server's environment to a baseline:
	// https://code.claude.com/docs/en/env-vars
	MCPAllowlistEnv = "CLAUDE_CODE_MCP_ALLOWLIST_ENV"
)

// Not read, because the docs name no file for them: the macOS configuration profile (com.anthropic.claudecode
// managed preferences, rank 2 between the server cache and the files) and the Windows HKLM/HKCU registry. A machine
// with an MDM policy can therefore rank a source above the file this package reads without it showing here.

// Source is one managed source, in Claude Code's rank order.
type Source struct {
	Name, Path string
	Present    bool     // a file exists (even one that cannot be read)
	PolicyKeys []string // key names only, sorted
	// ChannelsEnabled and AllowsMash are nil when the source does not set channelsEnabled / allowedChannelPlugins.
	ChannelsEnabled *bool
	AllowsMash      *bool
	Merge           bool   // managedSourcesBehavior is "merge"
	MCPAllowlistEnv string // this source's env value for CLAUDE_CODE_MCP_ALLOWLIST_ENV; "" when unset
	Err             error  // unreadable or unparseable; the rest of the source is then unreliable
}

// DefaultManagedDir is the endpoint-managed system directory for goos.
// https://code.claude.com/docs/en/managed-settings#choose-a-delivery-mechanism
func DefaultManagedDir(goos string) string {
	switch goos {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		return `C:\Program Files\ClaudeCode`
	}
	return "/etc/claude-code"
}

// Sources reads the managed sources in rank order: the server-managed cache, then the endpoint-managed files
// (managed-settings.json merged with managed-settings.d/*.json). An empty managedDir means the default for goos.
func Sources(home, goos, managedDir string) []Source {
	if managedDir == "" {
		managedDir = DefaultManagedDir(goos)
	}
	remote := Source{Name: ServerName, Path: filepath.Join(home, serverCacheRel)}
	if doc, present, err := readDoc(remote.Path); present {
		remote.Present = true
		fill(&remote, doc, err)
	}

	local := Source{Name: FileName, Path: filepath.Join(managedDir, managedFile)}
	paths := []string{local.Path}
	dropDir := filepath.Join(managedDir, managedDropD)
	ents, derr := os.ReadDir(dropDir)
	switch {
	case derr == nil:
		var names []string
		for _, e := range ents {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			paths = append(paths, filepath.Join(dropDir, n))
		}
	case !errors.Is(derr, fs.ErrNotExist):
		local.Present, local.Err = true, derr
	}
	merged := map[string]json.RawMessage{}
	for _, p := range paths {
		doc, present, err := readDoc(p)
		if !present {
			continue
		}
		local.Present = true
		if err != nil && local.Err == nil {
			local.Err = err
		}
		mergeDocs(merged, doc)
	}
	if local.Present {
		fill(&local, merged, local.Err)
	}
	return []Source{remote, local}
}

// mergeDocs folds b into a the way Claude Code merges managed files: lists combine without duplicates, objects
// merge key by key, and any other value is replaced by the later file's.
// https://code.claude.com/docs/en/managed-settings#split-a-file-based-policy-across-teams
func mergeDocs(a, b map[string]json.RawMessage) {
	for k, bv := range b {
		av, ok := a[k]
		if !ok {
			a[k] = bv
			continue
		}
		var al, bl []json.RawMessage
		if json.Unmarshal(av, &al) == nil && json.Unmarshal(bv, &bl) == nil {
			seen := map[string]bool{}
			var out []json.RawMessage
			for _, v := range append(al, bl...) {
				c := compact(v)
				if !seen[c] {
					seen[c] = true
					out = append(out, v)
				}
			}
			a[k], _ = json.Marshal(out)
			continue
		}
		var ao, bo map[string]json.RawMessage
		if json.Unmarshal(av, &ao) == nil && json.Unmarshal(bv, &bo) == nil && ao != nil && bo != nil {
			mergeDocs(ao, bo)
			a[k], _ = json.Marshal(ao)
			continue
		}
		a[k] = bv
	}
}

func compact(v json.RawMessage) string {
	var x any
	if json.Unmarshal(v, &x) != nil {
		return string(v)
	}
	b, _ := json.Marshal(x)
	return string(b)
}

// readDoc reads one settings file as a JSON object. present is false only when the file does not exist; an
// unreadable or unparseable file is present with an error, because Claude Code counts it as present too.
func readDoc(path string) (doc map[string]json.RawMessage, present bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, true, errors.New("cannot read " + path + ": " + underlying(err))
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc == nil {
		return nil, true, errors.New(path + " is not a JSON object")
	}
	return doc, true, nil
}

// underlying drops the path os errors carry, so the message names the file once and never echoes contents.
func underlying(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

func fill(s *Source, doc map[string]json.RawMessage, err error) {
	if err != nil {
		s.Err = err
		return
	}
	for k, v := range doc {
		if k != keyMergeBehavior && k != keyWSLInherits && string(v) != "null" {
			s.PolicyKeys = append(s.PolicyKeys, k)
		}
	}
	sort.Strings(s.PolicyKeys)
	if v, ok := doc[keyMergeBehavior]; ok {
		var m string
		s.Merge = json.Unmarshal(v, &m) == nil && m == "merge"
	}
	if v, ok := doc[keyChannelsEnabled]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			s.ChannelsEnabled = &b
		} else {
			s.Err = errors.New(s.Path + ": " + keyChannelsEnabled + " is not a boolean")
		}
	}
	if v, ok := doc[keyAllowedPlugins]; ok {
		var list []struct{ Marketplace, Plugin string }
		if json.Unmarshal(v, &list) != nil {
			s.Err = errors.New(s.Path + ": " + keyAllowedPlugins + " is not a list of {marketplace, plugin}")
		} else {
			ok := false
			for _, e := range list {
				ok = ok || (e.Marketplace == "mash" && e.Plugin == "mash")
			}
			s.AllowsMash = &ok
		}
	}
	s.MCPAllowlistEnv = envValue(doc)
}

// envValue is the CLAUDE_CODE_MCP_ALLOWLIST_ENV value in doc's env block, or "".
func envValue(doc map[string]json.RawMessage) string {
	var env map[string]any
	if json.Unmarshal(doc[keyEnv], &env) != nil {
		return ""
	}
	switch v := env[MCPAllowlistEnv].(type) {
	case string:
		return v
	case bool, float64:
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

// Effective picks the source Claude Code applies. By default it is the first source with a policy key and the rest
// are shadowed; when that source sets managedSourcesBehavior "merge", every source with a policy key contributes and
// the winner is a synthesised source carrying the merged view: channelsEnabled and allowedChannelPlugins each come
// whole from the highest-ranked source that sets them (allowedChannelPlugins is a restriction allowlist, not a union).
// https://code.claude.com/docs/en/managed-settings#compose-every-managed-source
// winner is nil when no source has a policy key. A source with Err and no policy keys read is skipped here; callers
// check Err on the sources ranked above the winner.
func Effective(srcs []Source) (winner *Source, shadowed []Source) {
	first := -1
	for i := range srcs {
		if len(srcs[i].PolicyKeys) > 0 {
			first = i
			break
		}
	}
	if first < 0 {
		return nil, srcs
	}
	if !srcs[first].Merge {
		w := srcs[first]
		return &w, append(append([]Source{}, srcs[:first]...), srcs[first+1:]...)
	}
	m := Source{Name: MergedName, Path: srcs[first].Path, Present: true, Merge: true}
	for _, s := range srcs[first:] {
		if len(s.PolicyKeys) == 0 {
			shadowed = append(shadowed, s)
			continue
		}
		if m.ChannelsEnabled == nil {
			m.ChannelsEnabled = s.ChannelsEnabled
		}
		if m.AllowsMash == nil {
			m.AllowsMash = s.AllowsMash
		}
		m.PolicyKeys = append(m.PolicyKeys, s.PolicyKeys...)
	}
	shadowed = append(shadowed, srcs[:first]...)
	return &m, shadowed
}

// StrippedEnv reports whether Claude Code limits an MCP server's environment: CLAUDE_CODE_MCP_ALLOWLIST_ENV is "1"
// in the env of a managed source (env merges per variable across admin sources, the higher rank winning), else in
// <home>/.claude/settings.json, else in getenv (serve's own environment). where names which one set it.
func StrippedEnv(home string, srcs []Source, getenv func(string) string) (stripped bool, where string) {
	for _, s := range srcs {
		if s.MCPAllowlistEnv != "" {
			return s.MCPAllowlistEnv == "1", s.Name
		}
	}
	if doc, _, err := readDoc(filepath.Join(home, userSettingsRel)); err == nil {
		if v := envValue(doc); v != "" {
			return v == "1", "~/" + userSettingsRel
		}
	}
	if getenv != nil && getenv(MCPAllowlistEnv) == "1" {
		return true, "the environment"
	}
	return false, ""
}
