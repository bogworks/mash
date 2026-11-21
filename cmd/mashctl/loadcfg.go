package main

import (
	"errors"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

// loadConfig is the one place mashctl loads its configuration: the owner, the server and the credentials come from
// config.Load. There is no stored group; an endpoint's address is given on the command line. lookup is what
// config.Load reads; main passes os.LookupEnv.
//
// mashctl acts as no endpoint (its owner is the owner part of an explicit endpoint, else config.json "owner"; never an installed endpoint's), so
// a group-less address, several installed endpoints (mashctl never picks one) and a missing config directory for the
// endpoint's default creds path are not errors here, and the config is returned as loaded.
func loadConfig(env func(string) string, lookup config.Lookup) (config.Config, error) {
	cfg, err := config.Load(lookup, env("HOME"))
	var skipErr *config.SkipEncryptionError
	if errors.As(err, &skipErr) {
		return cfg, cliframe.Usagef("%s", skipErr.Msg()) // the removed name or a bad value: a configuration error (exit 2)
	}
	if errors.Is(err, config.ErrNoURL) || errors.Is(err, config.ErrNoOwner) {
		err = nil // mashctl acts as no endpoint: the verbs that need a server say so themselves
	}
	if cfg.Source["endpoint"] == "installed creds" {
		// An installed endpoint's creds file is the endpoint tool's business: mashctl neither takes its owner from it nor
		// fails because of it.
		if errors.Is(err, config.ErrNoConfigDir) {
			err = nil
		}
		cfg.Owner, cfg.Group, cfg.Name = "", "", ""
	}
	cfg.Owner = cfg.ExplicitOwner // mashctl's owner: an explicit endpoint's, else config.json "owner"
	if errors.Is(err, config.ErrGroupMissing) || errors.Is(err, config.ErrSeveralEndpoints) {
		err = nil // mashctl acts as no endpoint: several installed endpoints do not stop it
	}
	if errors.Is(err, config.ErrNoConfigDir) && cfg.URL != "" {
		err = nil // only the endpoint's default creds path needed the directory, and mashctl uses no endpoint creds
	}
	return cfg, err
}
