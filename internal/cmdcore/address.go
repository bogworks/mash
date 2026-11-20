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

// ParsePrefix is the check `mash who` makes of its prefix: an owner (acme), an owner and group (acme.demo) or a full
// address (acme.demo.bob), each part lower-case letters, digits and single dashes. acme.* and acme.demo.* are accepted
// and mean acme and acme.demo; there is no other pattern. It returns the prefix without the ".*". Anything else, the
// flat acme_demo and the slashed acme/demo included, is a usage error (exit 2), worded as ParseAddress words it.
func ParsePrefix(s, hint string) (string, error) {
	if p, ok := validPrefix(s); ok {
		return p, nil
	}
	want := "owner.group.name"
	guess := strings.ToLower(strings.NewReplacer("_", ".", "/", ".").Replace(s))
	if p, ok := validPrefix(guess); ok && p == strings.TrimSuffix(guess, ".*") {
		want = p
	}
	e := cliframe.Usagef("%s is not an address; write it as %s", s, want)
	if hint != "" {
		e = e.WithHint(hint)
	}
	return "", e
}

// validPrefix is s without a trailing ".*", when what is left is one to three valid address tokens (and a ".*" only
// follows one or two of them).
func validPrefix(s string) (string, bool) {
	body, star := strings.CutSuffix(s, ".*")
	parts := strings.Split(body, ".")
	if len(parts) > 3 || star && len(parts) > 2 {
		return "", false
	}
	pad := append(append([]string{}, parts...), "x", "x", "x")[:3]
	if _, err := sap.NewAddress(pad[0], pad[1], pad[2]); err != nil {
		return "", false
	}
	return body, true
}
