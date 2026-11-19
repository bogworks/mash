// Package cliframe is the frame shared by the mash and mashctl command lines: the global options, accepted anywhere in
// the arguments, and the error report.
//
// The global options are --endpoint ADDR, --creds PATH, --server URL (repeatable), --ca PATH, --config-dir PATH,
// --verbose or -v, --json and --no-color. Each takes the form --flag value or --flag=value, before or after the verb.
// Extract strips them from the arguments and Global.Resolve applies the precedence flag, then environment, then
// config.json.
//
// Exit codes are only 0 (success), 1 (failure) and 2 (usage error). Report maps every error onto one of them.
package cliframe

import (
	"fmt"
	"strings"
)

// Spec is what one tool adds to the shared frame.
type Spec struct {
	Tool     string    // "mash" or "mashctl"
	CredsEnv string    // environment variable for --creds (mash MASH_CREDS, mashctl MASH_ADMIN_CREDS)
	CredsKey string    // config.json key for --creds (mash "creds", mashctl "admin_creds")
	Extra    []Setting // extra value-taking settings of this tool
}

// Setting is one extra value-taking setting: its flag, environment variable and config.json key.
type Setting struct {
	Flag string // like "--system-creds"
	Env  string
	Key  string
}

// Global holds the global options found on the command line.
type Global struct {
	Endpoint, Creds, CA, ConfigDir string
	Servers                        []string
	Verbose, JSON, NoColor         bool
	Extra                          map[string]string // by Setting.Flag, values given by flag
	Set                            map[string]bool   // flags ("--endpoint", ...) that were given; -v is "--verbose"
}

// Extract removes the global options from args wherever they appear and returns them with the remaining arguments in
// their original order. A bare "--" ends the scan: it and everything after it are returned untouched. A value-taking flag
// without a value, or whose value looks like a flag, is a usage error (*Error, code 2). The last occurrence wins for
// single-valued flags; --server accumulates and takes a comma list.
func Extract(spec Spec, args []string) (Global, []string, error) {
	g := Global{Extra: map[string]string{}, Set: map[string]bool{}}
	rest := make([]string, 0, len(args))
	extras := map[string]bool{}
	for _, s := range spec.Extra {
		extras[s.Flag] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch a {
		case "-v", "--verbose":
			g.Verbose, g.Set["--verbose"] = true, true
			continue
		case "--json":
			g.JSON, g.Set["--json"] = true, true
			continue
		case "--no-color":
			g.NoColor, g.Set["--no-color"] = true, true
			continue
		}
		name, inline, hasInline := strings.Cut(a, "=")
		switch {
		case name == "--endpoint" || name == "--creds" || name == "--server" || name == "--ca" ||
			name == "--config-dir" || extras[name]:
			v, err := flagValue(args, &i, name, inline, hasInline)
			if err != nil {
				return Global{}, nil, Usagef("%s", err.Error())
			}
			g.Set[name] = true
			switch name {
			case "--endpoint":
				g.Endpoint = v
			case "--creds":
				g.Creds = v
			case "--ca":
				g.CA = v
			case "--config-dir":
				g.ConfigDir = v
			case "--server":
				g.Servers = append(g.Servers, splitList(v)...)
			default:
				g.Extra[name] = v
			}
		default:
			rest = append(rest, a)
		}
	}
	return g, rest, nil
}

// flagValue reads the value of the value-taking flag name at args[*i]. With the inline form (--flag=VALUE) the value is
// taken as written. Otherwise the value is the next argument and *i moves onto it. The next argument may not start with
// '-' (a bare "-" is allowed), so that a flag left without its value does not swallow the next flag. The rule and the
// text match cmd/mash/flagvalue.go.
func flagValue(args []string, i *int, name, inline string, hasInline bool) (string, error) {
	if hasInline {
		if inline == "" {
			return "", fmt.Errorf("%s needs a value", name)
		}
		return inline, nil
	}
	if *i+1 >= len(args) || args[*i+1] == "" {
		return "", fmt.Errorf("%s needs a value", name)
	}
	next := args[*i+1]
	if next != "-" && strings.HasPrefix(next, "-") {
		shown, _, _ := strings.Cut(next, "=") // never echo what follows an '=': it may be a secret
		if len(shown) > 32 {
			shown = shown[:32]
		}
		hint := "VALUE"
		if !strings.Contains(next, "=") && len(next) <= 32 {
			hint = next
		}
		return "", fmt.Errorf("%s needs a value; %q looks like a flag (write %s=%s to use it as the value)", name, shown, name, hint)
	}
	*i++
	return next, nil
}

// splitList splits a comma list, trimming each item and dropping empty ones.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Resolved is the effective value of each setting after applying flag, environment and config.json.
type Resolved struct {
	Endpoint, Creds, CA, ConfigDir string
	Servers                        []string
	Verbose, JSON, NoColor         bool
	Extra                          map[string]string // by Setting.Flag
	// Source says where each setting came from, keyed by flag ("--endpoint", "--server", an extra's flag): "flag", the
	// environment variable name, "config.json", or "unset".
	Source map[string]string
}

// Resolve applies the precedence flag, then environment, then config.json to every value-taking setting. env looks up an
// environment variable (nil means none are set); file is the decoded config.json (nil means none). An empty environment
// value or an empty config.json value counts as unset. NoColor is also true when NO_COLOR is non-empty.
func (g Global) Resolve(spec Spec, env func(string) string, file map[string]any) Resolved {
	if env == nil {
		env = func(string) string { return "" }
	}
	r := Resolved{
		Verbose: g.Verbose,
		JSON:    g.JSON,
		NoColor: g.NoColor || env("NO_COLOR") != "",
		Extra:   map[string]string{},
		Source:  map[string]string{},
	}
	str := func(flag, flagVal, envName, key string) string {
		if flagVal != "" {
			r.Source[flag] = "flag"
			return flagVal
		}
		if envName != "" {
			if v := env(envName); v != "" {
				r.Source[flag] = envName
				return v
			}
		}
		if key != "" {
			if v, ok := file[key].(string); ok && v != "" {
				r.Source[flag] = "config.json"
				return v
			}
		}
		r.Source[flag] = "unset"
		return ""
	}
	r.Endpoint = str("--endpoint", g.Endpoint, "MASH_ENDPOINT", "endpoint")
	r.Creds = str("--creds", g.Creds, spec.CredsEnv, spec.CredsKey)
	r.CA = str("--ca", g.CA, "MASH_CA", "ca")
	r.ConfigDir = str("--config-dir", g.ConfigDir, "MASH_CONFIG_DIR", "")
	for _, s := range spec.Extra {
		r.Extra[s.Flag] = str(s.Flag, g.Extra[s.Flag], s.Env, s.Key)
	}

	switch {
	case len(g.Servers) > 0:
		r.Servers, r.Source["--server"] = g.Servers, "flag"
	default:
		r.Source["--server"] = "unset"
		if l := splitList(env("MASH_SERVER")); len(l) > 0 {
			r.Servers, r.Source["--server"] = l, "MASH_SERVER"
		} else if l := fileList(file["servers"]); len(l) > 0 {
			r.Servers, r.Source["--server"] = l, "config.json"
		}
	}
	return r
}

// fileList reads a config.json servers value: a JSON array of strings or a single (comma list) string.
func fileList(v any) []string {
	switch t := v.(type) {
	case string:
		return splitList(t)
	case []string:
		var out []string
		for _, s := range t {
			out = append(out, splitList(s)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, splitList(s)...)
			}
		}
		return out
	}
	return nil
}
