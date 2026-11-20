package cmdcore

import (
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/sap"
)

// ParseAddress is the one check every command that takes an address makes. An address is owner.group.name: three dotted
// parts, each lower-case letters, digits and single dashes. It returns the address as it is shown (the same string).
// Anything else, the flat owner_group_name and the slashed owner/group/name included, is a usage error (exit 2):
// "<s> is not an address; write it as <the dotted form>", with the hint when it is not empty (the command's own help).
// When no dotted form can be made from s, the error says owner.group.name.
func ParseAddress(s, hint string) (string, error) {
	a, err := sap.ParseAddress(s)
	if err == nil {
		return a.String(), nil
	}
	want := "owner.group.name"
	guess := strings.ToLower(strings.NewReplacer("_", ".", "/", ".").Replace(s))
	if g, gerr := sap.ParseAddress(guess); gerr == nil {
		want = g.String()
	}
	e := cliframe.Usagef("%s is not an address; write it as %s", s, want)
	if hint != "" {
		e = e.WithHint(hint)
	}
	return "", e
}
