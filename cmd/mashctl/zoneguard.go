package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/policydirect"
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

// governedCredentialCommand is the command text a refusal under a list set names as the way to get a governed
// credential (ADR 0138). The verb is the credential primitive's face; nothing here runs it. Tests pin the whole refusal.
const governedCredentialCommand = "mashctl credential issue --address OWNER.NAME --endpoint cc.OWNER.NAME --public-key <the endpoint's public key>"

// listSetRefusal is the refusal of verb ("add" or "reissue") while a list set of generation gen is in force: the
// owner-wide credential it would mint is untagged, so the list set does not govern it.
func listSetRefusal(verb string, gen uint64) string {
	return fmt.Sprintf("a list set is in force (generation %d): %s would mint an owner-wide credential the list set does not govern; get a governed one with: %s; nothing was changed", gen, verb, governedCredentialCommand)
}

// listSetGuard is the applied-list-set refusal of add and reissue (ADR 0138), made before any key, record or file is
// made. It reads the list set in force from the policy ledger, as policydirect.Config.InForce does: no policy bucket, or
// no list set applied, means none in force; any other read error refuses (fail closed). There is no way to skip it. It
// is a variable so a test can stand it in; the tests of the check restore listSetGuardCheck.
var listSetGuard = listSetGuardCheck

func listSetGuardCheck(ctx context.Context, adm *admin.Admin, accountPub, verb string) error {
	gen, err := policydirect.ListSetGeneration(ctx, adm, accountPub, "")
	if err != nil {
		return fmt.Errorf("cannot tell whether a list set is in force (%v); %s changed nothing", err, verb)
	}
	if gen > 0 {
		return errors.New(listSetRefusal(verb, gen))
	}
	return nil
}
