package main

import (
	"context"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
)

// checkServerSet confirms, before anything is changed, that the system account sees every server (the count of
// servers in the configuration) and that each holds the account JWT. A test replaces
// it. An error means a revocation could not be confirmed everywhere.
var checkServerSet = checkServerSetLive

func checkServerSetLive(ctx context.Context, cfg config.Config, env func(string) string, servers int) error {
	_, err := readAccount(ctx, cfg, env, servers, func(ctx context.Context, p apply.RevokeParams) (bool, error) {
		return apply.KeyRevoked(ctx, p, "")
	})
	return err
}
