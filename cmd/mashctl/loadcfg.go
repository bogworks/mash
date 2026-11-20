package main

import (
	"errors"
	"fmt"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// loadConfig is the one place mashctl loads its configuration: the owner, the server and the credentials come from
// config.Load. There is no stored group; an endpoint's address is given on the command line. lookup is what
// config.Load reads; main passes os.LookupEnv.
//
// mashctl acts as no endpoint (its owner comes from the owner part of MASH_ENDPOINT, nothing else of it is used), so
// a group-less address, several installed endpoints (mashctl never picks one) and a missing config directory for the
// endpoint's default creds path are not errors here, and the config is returned as loaded.
func loadConfig(env func(string) string, lookup config.Lookup) (config.Config, error) {
	cfg, err := config.Load(lookup, env("HOME"))
	if errors.Is(err, config.ErrGroupMissing) || errors.Is(err, config.ErrSeveralEndpoints) {
		err = nil // mashctl acts as no endpoint: several installed endpoints do not stop it
	}
	if errors.Is(err, config.ErrNoConfigDir) && cfg.URL != "" {
		err = nil // only the endpoint's default creds path needed the directory, and mashctl uses no endpoint creds
	}
	return cfg, err
}

// parseAddr reads an endpoint address argument: the dotted form owner.group.instance or the flat key
// owner_group_instance. Anything else is refused.
func parseAddr(arg string) (sap.Address, error) {
	if a, err := sap.ParseAddress(arg); err == nil {
		return a, nil
	}
	if a, err := sap.AddressFromFlat(arg); err == nil {
		return a, nil
	}
	return sap.Address{}, fmt.Errorf("not an endpoint address (the value is not shown): give owner.group.instance")
}
