package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
)

// serverFlagVerbs are the verbs that take --servers N and --sys-creds FILE among their own arguments.
var serverFlagVerbs = map[string]bool{"add": true, "reissue": true, "retire": true, "unzone": true, "deprovision": true}

// extractServerFlags removes --servers N and --sys-creds FILE (and the --servers=N and --sys-creds=FILE forms) from
// args and returns the rest with an environment in which MASH_SERVERS and MASH_SYS_CREDS answer with the flag values.
// A flag beats the environment variable. A --servers value that is not a whole number of 1 or more is an error, with
// the wording of parseServers. When a flag is given more than once the last one wins.
func extractServerFlags(args []string, env func(string) string) (rest []string, env2 func(string) string, err error) {
	var servers, sysCreds string
	var haveServers, haveCreds bool
	value := func(i *int, a, flag string) (string, error) {
		inline, hasInline := strings.CutPrefix(a, flag+"=")
		return flagValue(args, i, flag, inline, hasInline)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--servers" || strings.HasPrefix(a, "--servers="):
			v, err := value(&i, a, "--servers")
			if err != nil {
				return nil, nil, err
			}
			if n, aerr := strconv.Atoi(strings.TrimSpace(v)); aerr != nil || n < 1 {
				return nil, nil, fmt.Errorf("--servers must be a whole number of servers, 1 or more, got %q", v)
			}
			servers, haveServers = strings.TrimSpace(v), true
		case a == "--sys-creds" || strings.HasPrefix(a, "--sys-creds="):
			v, err := value(&i, a, "--sys-creds")
			if err != nil {
				return nil, nil, err
			}
			sysCreds, haveCreds = v, true
		default:
			rest = append(rest, a)
		}
	}
	return rest, func(k string) string {
		switch {
		case k == "MASH_SERVERS" && haveServers:
			return servers
		case k == "MASH_SYS_CREDS" && haveCreds:
			return sysCreds
		}
		return env(k)
	}, nil
}

// checkServerSet confirms, before anything is changed, that the system account sees every server (the count from
// --servers or MASH_SERVERS, else the routes the servers report) and that each holds the account JWT. A test replaces
// it. An error means a revocation could not be confirmed everywhere.
var checkServerSet = checkServerSetLive

func checkServerSetLive(ctx context.Context, cfg config.Config, env func(string) string, servers int) error {
	_, err := readAccount(ctx, cfg, env, servers, func(ctx context.Context, p apply.RevokeParams) (bool, error) {
		return apply.KeyRevoked(ctx, p, "")
	})
	return err
}
