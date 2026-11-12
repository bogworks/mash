package ctl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// The three-token form of provision and deprovision (naming plan 1.6, ADR 0074).
//
//	provision   NAME --agent A [--owner O] [--alias OWNER/NAME] --user U --account A ...
//	provision   agent.owner.name [--alias OWNER/NAME] --user U --account A ...
//	deprovision NAME --agent A [--owner O] [...]      deprovision agent.owner.name [...]
//
// --owner defaults to the configured owner. The flat key (agent_owner_name) is accepted in place of the canonical
// form. The form needs the agent_token setting (config.json "agent_token" or MASH_AGENT_TOKEN); without it the verb is
// refused and nothing is touched. A legacy name takes none of these and behaves as before.
//
// The setting gates creating and naming three-token endpoints (provision, the dotted agent.owner.name form, and the
// --agent/--owner/--alias flags). An endpoint that already exists stays operable by its flat storage key regardless
// of the setting (suspend, resume, suspended, and deprovision <flat key> --drained-to ... --suspension ...), so an
// operator who turns the setting off can still clean up endpoints created while it was on.

// newForm is a three-token target and the optional legacy alias of a provision.
type newForm struct {
	id    sap.EndpointIdentity
	alias *store.Alias
}

// usageError marks a misuse of the verb (exit 2), as against a refusal (exit 1).
type usageError struct{ error }

func (u usageError) Unwrap() error { return u.error }

func isUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

func newFormFlag(a string) bool { return a == "--agent" || a == "--owner" || a == "--alias" }

// positionalIdentity returns the identity a canonical (agent.owner.name) or flat (agent_owner_name) argument names,
// and whether the argument is written in a three-token form at all. A legacy name has neither a dot nor an underscore.
func positionalIdentity(arg string) (sap.EndpointIdentity, bool, error) {
	switch {
	case strings.Contains(arg, "."):
		id, err := sap.ParseCanonical(arg)
		return id, true, err
	case strings.Contains(arg, "_"):
		id, err := sap.FromFlat(arg)
		return id, true, err
	}
	return sap.EndpointIdentity{}, false, nil
}

// NewFormRequested reports whether args (a verb and its arguments) ask provision or deprovision for the three-token
// form: a --agent, --owner or --alias flag, or a canonical or flat positional. Flags are read in pairs, like the verbs'
// own parsers, so a value that looks like a flag is a value.
func NewFormRequested(args []string) bool {
	if len(args) < 2 || (args[0] != "provision" && args[0] != "deprovision") {
		return false
	}
	if _, nf, _ := positionalIdentity(args[1]); nf {
		return true
	}
	for i := 2; i < len(args); i++ {
		if newFormFlag(args[i]) {
			return true
		}
		if args[i] != "--no-revoke" {
			i++ // the flag's value
		}
	}
	return false
}

// parseNewForm takes the three-token flags out of args. It returns args with them removed (args[0] and args[1] kept),
// and the target, or nil for a legacy request. A usage error is wrapped as usageError; the setting being off is not.
func parseNewForm(cfg config.Config, args []string) ([]string, *newForm, error) {
	if !NewFormRequested(args) {
		return args, nil, nil
	}
	verb := args[0]
	rest := []string{args[0], args[1]}
	vals := map[string]string{}
	for i := 2; i < len(args); i++ {
		if args[i] == "--no-revoke" {
			rest = append(rest, args[i])
			continue
		}
		if i+1 >= len(args) {
			return nil, nil, usageError{fmt.Errorf("%s needs a value", args[i])}
		}
		if newFormFlag(args[i]) {
			if _, dup := vals[args[i]]; dup {
				return nil, nil, usageError{fmt.Errorf("%s given twice", args[i])}
			}
			vals[args[i]] = args[i+1]
		} else {
			rest = append(rest, args[i], args[i+1])
		}
		i++
	}
	var nf newForm
	id, positional, err := positionalIdentity(args[1])
	// A bare flat key to deprovision, with no three-token flag and the setting off, is the admin's directory key (the
	// scripted form of ADR 0081, as suspend, resume and suspended take it): it keeps its legacy-key path.
	if verb == "deprovision" && cfg.AgentToken == "" && positional && err == nil && !strings.Contains(args[1], ".") &&
		len(vals) == 0 {
		return args, nil, nil
	}
	switch {
	case positional:
		if err != nil {
			return nil, nil, usageError{fmt.Errorf("%s: %w", args[1], err)}
		}
		if _, ok := vals["--agent"]; ok {
			return nil, nil, usageError{errors.New("--agent and --owner go with a bare name, not with agent.owner.name")}
		}
		if _, ok := vals["--owner"]; ok {
			return nil, nil, usageError{errors.New("--agent and --owner go with a bare name, not with agent.owner.name")}
		}
		nf.id = id
	default:
		agent, ok := vals["--agent"]
		if !ok {
			return nil, nil, usageError{errors.New("--owner and --alias need --agent: the three-token form is --agent A [--owner O] NAME")}
		}
		owner, ok := vals["--owner"]
		if !ok {
			owner = cfg.Owner
		}
		if nf.id, err = sap.New(agent, owner, args[1]); err != nil {
			return nil, nil, usageError{err}
		}
	}
	if a, ok := vals["--alias"]; ok {
		if verb != "provision" {
			return nil, nil, usageError{errors.New("--alias belongs to provision")}
		}
		o, n, ok := strings.Cut(a, "/")
		if !ok || o == "" || n == "" || strings.Contains(n, "/") {
			return nil, nil, usageError{errors.New("--alias wants OWNER/NAME, the legacy endpoint the new one answers to")}
		}
		nf.alias = &store.Alias{Owner: o, Name: n}
	}
	if cfg.AgentToken == "" {
		return nil, nil, fmt.Errorf("mash: %s %s: the three-token form needs the agent_token setting (agent_token in config.json or MASH_AGENT_TOKEN), which is not set; nothing was changed", verb, nf.id)
	}
	return rest, &nf, nil
}
