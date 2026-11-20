package cliframe

import (
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// Shadow returns env and lookup wrapped so that each global flag given on the command line answers for the environment
// variable it overrides: --endpoint for MASH_ENDPOINT, --creds for spec.CredsEnv, --server (the values joined with
// commas) for MASH_SERVER, --ca for MASH_CA, --config-dir for MASH_CONFIG_DIR, and each extra setting's flag for its
// Env. A flag beats the environment variable. The wrapped lookup also answers config.FlagSourcePrefix+VAR with the
// flag's name, so config.Load can record "flag:--name" as the source.
func (g Global) Shadow(spec Spec, env func(string) string, lookup config.Lookup) (func(string) string, config.Lookup) {
	type val struct{ v, flag string }
	vals := map[string]val{}
	set := func(envName, flag, v string) {
		if envName != "" && v != "" {
			vals[envName] = val{v, flag}
		}
	}
	set("MASH_ENDPOINT", "--endpoint", g.Endpoint)
	set(spec.CredsEnv, "--creds", g.Creds)
	set("MASH_SERVER", "--server", strings.Join(g.Servers, ","))
	set("MASH_CA", "--ca", g.CA)
	set("MASH_CONFIG_DIR", "--config-dir", g.ConfigDir)
	for _, s := range spec.Extra {
		set(s.Env, s.Flag, g.Extra[s.Flag])
	}
	if len(vals) == 0 {
		return env, lookup
	}
	env2 := func(k string) string {
		if v, ok := vals[k]; ok {
			return v.v
		}
		return env(k)
	}
	lookup2 := func(k string) (string, bool) {
		if v, ok := vals[k]; ok {
			return v.v, true
		}
		if name, ok := strings.CutPrefix(k, config.FlagSourcePrefix); ok {
			if v, ok := vals[name]; ok {
				return v.flag, true
			}
		}
		return lookup(k)
	}
	return env2, lookup2
}

// ExtractOwning is Extract, except that a verb listed in own keeps the global flags that the verb itself parses: own maps
// a verb to those flags (for example "--json", or "--server" for mashctl adopt). Such a flag after the verb is left in
// the arguments exactly as written, in its place, with its value, and does not count as a global option; the verb sees
// its arguments as it did before the global options existed. The verb is the first argument that remains once the global
// options are removed. A "--json" written before the verb is a global option as usual, and is put after the verb for a
// verb that keeps it. For any other verb it is Extract.
func ExtractOwning(spec Spec, args []string, own map[string][]string) (Global, []string, error) {
	g, rest, err := Extract(spec, args)
	if err != nil || len(rest) == 0 || len(own[rest[0]]) == 0 {
		return g, rest, err
	}
	verb := rest[0]
	keeps := func(f string) bool {
		for _, k := range own[verb] {
			if k == f {
				return true
			}
		}
		return false
	}
	// Find the verb in args: skip the global options (and the values of those that take one) before it.
	vi := 0
	for ; vi < len(args); vi++ {
		a := args[vi]
		name, _, hasInline := strings.Cut(a, "=")
		switch {
		case a == DetailFlag || a == "--json" || a == "--no-color":
		case name == "--endpoint" || name == "--creds" || name == "--server" || name == "--ca" || name == "--config-dir" || spec.hasExtra(name):
			if !hasInline {
				vi++
			}
		default:
			goto found
		}
	}
found:
	const mark = "\x00own\x00"
	prot := make([]string, len(args))
	bare := false
	for i, a := range args {
		prot[i] = a
		if a == "--" {
			bare = true
		}
		if name, _, _ := strings.Cut(a, "="); i > vi && !bare && keeps(name) {
			prot[i] = mark + a
		}
	}
	g, rest, err = Extract(spec, prot)
	for i, a := range rest {
		rest[i] = strings.TrimPrefix(a, mark)
	}
	if err == nil && keeps("--json") && g.JSON {
		// A --json before the verb was stripped: the verb still parses it, so put it back after the verb.
		for i, a := range rest {
			if a == "--" {
				return g, append(append(append([]string{}, rest[:i]...), "--json"), rest[i:]...), nil
			}
		}
		rest = append(rest, "--json")
	}
	return g, rest, err
}

func (s Spec) hasExtra(flag string) bool {
	for _, e := range s.Extra {
		if e.Flag == flag {
			return true
		}
	}
	return false
}
