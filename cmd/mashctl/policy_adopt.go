package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/store"
)

const policyAdoptUsage = `usage: mashctl policy adopt --submitter NKEY_PUBLIC_KEY
       mashctl policy adopt --show [--json]
       (adopt registers the one key allowed to hand in list sets and marks the account as owned by an outside system;
       running it again with a different key is the recovery rotation; there is no un-adopt command)
`

// runPolicyAdopt is `mashctl policy adopt`: it needs the account's admin credentials and, to write, the key store.
func runPolicyAdopt(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	var submitter string
	var show, asJSON bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--show":
			show = true
		case a == "--json":
			asJSON = true
		case a == "--submitter" || strings.HasPrefix(a, "--submitter="):
			if v, has := strings.CutPrefix(a, "--submitter="); has {
				submitter = v
			} else if i+1 < len(args) {
				i++
				submitter = args[i]
			}
			if submitter == "" {
				fmt.Fprintf(stderr, "mashctl: policy adopt: --submitter needs a key\n%s", policyAdoptUsage)
				return 2
			}
		default: // a mistyped argument may be a secret: it is named, never printed
			fmt.Fprintf(stderr, "mashctl: policy adopt: unknown %s\n%s", config.RejectedArg(a), policyAdoptUsage)
			return 2
		}
	}
	if show == (submitter != "") || (asJSON && !show) { // exactly one of --show and --submitter; --json only with --show
		fmt.Fprint(stderr, policyAdoptUsage)
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: policy adopt: %s\n", fmt.Sprintf(format, a...))
		return 1
	}
	if show {
		return showAdoption(cfg, asJSON, env, stdout, stderr)
	}

	// Everything that can be refused without the server is refused before it is reached.
	if _, err := nkeys.FromPublicKey(submitter); err != nil { // a seed or a mistyped key: never echoed
		fmt.Fprintf(stderr, "mashctl: policy adopt: --submitter is not an nkey public key (the value is not shown)\n%s", policyAdoptUsage)
		return 2
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	account, held, err := heldKeys(keys)
	if err != nil {
		return fail("%v", err)
	}
	by, err := ctl.AdminUserKey(cfg)
	if err != nil {
		return fail("%v", err)
	}
	held[by] = "the admin user's key"
	if what, bad := held[submitter]; bad {
		return fail("the submitter must be a key mash does not hold, and this is %s (the value is not shown); nothing was changed", what)
	}

	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	prev, wasAdopted, err := adm.Adoption(ctx)
	if err != nil {
		return fail("%v; nothing was changed", err)
	}
	m, action, err := adm.Adopt(ctx, account, submitter, by)
	switch {
	case errors.Is(err, admin.ErrSubmitterInvalid), errors.Is(err, admin.ErrSubmitterIsAccount):
		return fail("%v; nothing was changed", err)
	case err != nil:
		return fail("%v", err)
	}
	switch action {
	case admin.AdoptUnchanged:
		fmt.Fprintf(stdout, "this account is already adopted with that submitter %s: nothing was changed\n", m.Submitter)
		return 0
	case store.ActionRecover:
		if wasAdopted {
			fmt.Fprintf(stdout, "recovery rotation: the submitter was %s and is now %s; the old key is refused from now on\n", prev.Submitter, m.Submitter)
		}
	default:
		fmt.Fprintf(stdout, "adopted this account: the one submitter allowed to hand in list sets is %s\n", m.Submitter)
	}
	fmt.Fprintln(stdout, adoptedNote)
	return 0
}

// adoptedNote says what an adopted account cannot do yet, so nobody is surprised by it.
const adoptedNote = "note: from now on mashctl refuses a direct policy apply, add, reissue, label, set-meta labels, set-zone and retire on this account. " +
	"This version has no list-set intake and no un-adopt command (un-adoption is not supported yet), so the account's policy cannot be changed until the intake exists."

// heldKeys is the account's public key and every public key the key store holds (operator, system, account and every
// zone key), each with what it is. A submitter must not be one of them: it is a key the outside system holds. Keys the
// store does not have (a store of a server mash did not stand up has no operator or system key) are skipped.
func heldKeys(ks *ctlkeys.Authority) (account string, held map[string]string, err error) {
	held = map[string]string{}
	add := func(kp nkeys.KeyPair, pub, what string) {
		kp.Wipe()
		held[pub] = what
	}
	if kp, pub, err := ks.Operator(); err == nil {
		add(kp, pub, "the operator key")
	} else if !errors.Is(err, ctlkeys.ErrNoOperatorKey) {
		return "", nil, err
	}
	if kp, pub, err := ks.System(); err == nil {
		add(kp, pub, "the system account key")
	} else if !errors.Is(err, ctlkeys.ErrNoSystemKey) {
		return "", nil, err
	}
	kp, account, err := ks.Account()
	if err != nil {
		return "", nil, err
	}
	add(kp, account, "the account's own key")
	zones, err := ks.Zones()
	if err != nil {
		return "", nil, err
	}
	for _, z := range zones {
		pub, err := ks.ZonePublic(z)
		if err != nil {
			return "", nil, err
		}
		held[pub] = "the signing key of zone " + z
	}
	return account, held, nil
}

// adoptionView is `policy adopt --show --json`.
type adoptionView struct {
	Adopted bool          `json:"adopted"`
	Marker  *store.Marker `json:"marker,omitempty"`
}

// showAdoption prints the marker, or "not adopted". It only reads. A marker that cannot be read is an error (exit 1),
// never "not adopted".
func showAdoption(cfg config.Config, asJSON bool, env func(string) string, stdout, stderr io.Writer) int {
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy adopt: %v\n", err)
		return 1
	}
	defer closeAdmin()
	m, found, err := adm.Adoption(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy adopt: cannot tell whether this account is adopted: %v\n", err)
		return 1
	}
	v := adoptionView{Adopted: found}
	if found {
		v.Marker = &m
	}
	if asJSON {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if !found {
		fmt.Fprintln(stdout, "not adopted")
		return 0
	}
	fmt.Fprintf(stdout, "adopted: list sets come only from the submitter %s\n  account %s\n  action %s at %s by %s\n", m.Submitter, m.Account, m.Action, m.AdoptedAt, m.By)
	if m.NextSubmitter != "" {
		fmt.Fprintf(stdout, "  next submitter %s\n", m.NextSubmitter)
	}
	return 0
}
