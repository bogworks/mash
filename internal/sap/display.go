package sap

import (
	"fmt"
	"strings"
)

// Display is the human-facing form of a directory key. The key of a three-token endpoint is its flat form
// (agent_owner_name); people read the canonical agent.owner.name (ADR 0081). A legacy name, and anything else that is
// not a valid three-token flat key, is returned unchanged, so a legacy endpoint is shown exactly as it always was.
func Display(key string) string {
	if id, err := FromFlat(key); err == nil {
		return id.String()
	}
	return key
}

// ParseCanonical is the inverse of String for a new-form identity: exactly three dot-separated tokens, each valid under
// the new token rule, within the total bound. A legacy name never contains a dot (the legacy name rule allows letters,
// digits and hyphens), so a string with dots is never ambiguous with one.
func ParseCanonical(s string) (EndpointIdentity, error) {
	p := strings.Split(s, ".")
	if len(p) != 3 {
		return EndpointIdentity{}, fmt.Errorf("sap: %q has %d dot-separated parts: want exactly three (agent.owner.name)", s, len(p))
	}
	return New(p[0], p[1], p[2])
}

// KeyOf is the directory key a recipient argument names, for input that may be written in the canonical form: a valid
// agent.owner.name gives its flat key; anything else (a flat key, a legacy name, a string that is neither) is returned
// unchanged for the caller to accept or refuse on its own terms.
func KeyOf(arg string) string {
	if strings.Contains(arg, ".") {
		if id, err := ParseCanonical(arg); err == nil {
			return id.Flat()
		}
	}
	return arg
}
