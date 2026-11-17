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
// A shell inside an endpoint session sets MASH_NAME without a group for mashctl: mashctl acts as no endpoint, so a name
// with no group is not an error here, and the config is returned as loaded.
func loadConfig(env func(string) string, lookup config.Lookup) (config.Config, error) {
	cfg, err := config.Load(lookup, env("HOME"))
	if errors.Is(err, config.ErrGroupMissing) {
		err = nil
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
