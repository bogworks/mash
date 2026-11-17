package sap

import "strings"

// Display is the human-facing form of a directory key. The key of an endpoint is its flat form
// (owner_group_instance); people read the dotted owner.group.instance. Anything that is not a valid flat key is
// returned unchanged.
func Display(key string) string {
	if a, err := AddressFromFlat(key); err == nil {
		return a.String()
	}
	return key
}

// KeyOf is the directory key a recipient argument names, for input that may be written in the dotted form: a valid
// owner.group.instance gives its flat key; anything else (a flat key, a string that is neither) is returned unchanged
// for the caller to accept or refuse on its own terms.
func KeyOf(arg string) string {
	if strings.Contains(arg, ".") {
		if a, err := ParseAddress(arg); err == nil {
			return a.Flat()
		}
	}
	return arg
}
