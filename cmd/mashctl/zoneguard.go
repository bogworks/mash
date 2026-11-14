package main

import (
	"context"
	"fmt"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// zoneGuard is the applied-zone-policy refusal shared by unzone, add and reissue (ADR 0091). A policy of an older
// release leaves scoped signing keys in the account JWT, and the owner-wide permissions these commands mint would
// bypass its mail restrictions, so they refuse while one is applied. The check reads the account JWT, which needs the
// system account credentials and the key store. It fails closed: when either is not configured, or the JWT cannot be
// read or verified, it returns an error and the command changes nothing. There is no way to skip it. The error text
// has no command prefix and ends with "nothing was changed".
//
// It is a variable so the tests of a server that has no system account can stand it in; the tests that cover the
// check itself restore zoneGuardCheck.
var zoneGuard = zoneGuardCheck

func zoneGuardCheck(cfg config.Config, sysCreds string, env func(string) string) error {
	if _, serr := sysCredsPath(sysCreds, env, cfg); serr != nil {
		return fmt.Errorf("cannot check whether a zone policy is applied (%v); configure the system credentials; nothing was changed", serr)
	}
	keys, kerr := openKeys(cfg, env)
	if kerr != nil {
		return fmt.Errorf("cannot check whether a zone policy is applied (%v); configure the key store; nothing was changed", kerr)
	}
	servers, perr := parseServers(env)
	if perr != nil {
		return perr
	}
	nc, cerr := connectSys(cfg, sysCreds, env)
	if cerr != nil {
		return fmt.Errorf("cannot tell whether a zone policy is applied: %v; nothing was changed (configure --sys-creds or MASH_SYS_CREDS)", cerr)
	}
	pctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	rerr := apply.RefuseZonePolicy(pctx, apply.NATS{Conn: nc, Inventory: servers}, keys, servers, store.ErrZonePolicyApplied)
	cancel()
	nc.Close()
	if rerr != nil {
		return fmt.Errorf("refusing: %v (nothing was changed)", rerr)
	}
	return nil
}
