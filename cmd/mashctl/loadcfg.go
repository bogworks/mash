package main

import (
	"errors"

	"github.com/bogworks/mash/internal/config"
)

var errAgentTokenSet = errors.New("mashctl: agent_token is set but mashctl cannot act on a three-token identity yet; unset agent_token (MASH_AGENT_TOKEN)")

// lookup is what config.Load reads: main passes os.LookupEnv so that a MASH_AGENT_TOKEN that is set but empty is seen.
// loadConfig is the one place mashctl loads its configuration. It fails closed: mashctl shares config.Load with mash,
// but would act on the legacy identity when agent_token is set, so every verb refuses instead. The refusal wins over
// any other error, including the tolerated ErrNoURL and ErrNoConfigDir, which callers treat as not fatal.
func loadConfig(env func(string) string, lookup config.Lookup) (config.Config, error) {
	return loadConfigFor(false, env, lookup)
}

// loadConfigFor is loadConfig; newForm is true for the three-token form of provision and deprovision (naming plan
// 1.6), which names its target itself and so runs with agent_token set.
func loadConfigFor(newForm bool, env func(string) string, lookup config.Lookup) (config.Config, error) {
	cfg, err := config.Load(lookup, env("HOME"))
	if cfg.AgentToken != "" && !newForm {
		return cfg, errAgentTokenSet
	}
	return cfg, err
}
