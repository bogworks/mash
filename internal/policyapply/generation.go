package policyapply

import (
	"encoding/json"
	"slices"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
)

// Candidate is a generation a server may hold: its number, compiled templates and the keys it revokes.
type Candidate struct {
	Generation  uint64
	Compiled    *listset.Compiled
	RevokedKeys []string
}

// GenerationOf says which candidate the account JWT carries. The JWT carries no marker: a scoped signing key whose
// role is a template's Group (the role is the owner group "range.owner", the name the template is filed under)
// carries that generation's Template.Permissions, and the revocation list holds every key the generation revokes. A
// scoped key for a group of another candidate that this one lacks must be absent. When several candidates match the
// first wins, so list the target first. ok is false when the JWT does not decode or no candidate matches.
func GenerationOf(accountJWT string, candidates []Candidate) (gen uint64, ok bool) {
	claims, err := jwt.DecodeAccountClaims(accountJWT)
	if err != nil {
		return 0, false
	}
	return generationOfClaims(claims, candidates)
}

func generationOfClaims(claims *jwt.AccountClaims, candidates []Candidate) (uint64, bool) {
	roles := map[string]string{} // role -> canonical permissions
	for k := range claims.SigningKeys {
		sc, _ := claims.SigningKeys.GetScope(k)
		if us, isUser := sc.(*jwt.UserScope); isUser && us != nil {
			roles[us.Role] = canonPerms(us.Template.Permissions)
		}
	}
	for _, c := range candidates {
		if c.Compiled != nil && matches(claims, roles, c, candidates) {
			return c.Generation, true
		}
	}
	return 0, false
}

func matches(claims *jwt.AccountClaims, roles map[string]string, c Candidate, all []Candidate) bool {
	own := map[string]bool{}
	for _, t := range c.Compiled.Templates {
		own[t.Group] = true
		if got, has := roles[t.Group]; !has || got != canonPerms(t.Permissions) {
			return false
		}
	}
	for _, o := range all {
		if o.Compiled == nil {
			continue
		}
		for _, t := range o.Compiled.Templates {
			if _, has := roles[t.Group]; has && !own[t.Group] {
				return false
			}
		}
	}
	for _, k := range c.RevokedKeys {
		if _, has := claims.Revocations[k]; !has {
			return false
		}
	}
	return true
}

// canonPerms is a canonical JSON form of a permission set, independent of line order and of nil versus empty.
func canonPerms(p jwt.Permissions) string {
	norm := func(l jwt.StringList) []string {
		out := slices.Clone([]string(l))
		if out == nil {
			out = []string{}
		}
		slices.Sort(out)
		return out
	}
	type perm struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	}
	v := struct {
		Pub  perm                    `json:"pub"`
		Sub  perm                    `json:"sub"`
		Resp *jwt.ResponsePermission `json:"resp"`
	}{
		Pub:  perm{norm(p.Pub.Allow), norm(p.Pub.Deny)},
		Sub:  perm{norm(p.Sub.Allow), norm(p.Sub.Deny)},
		Resp: p.Resp,
	}
	b, _ := json.Marshal(v)
	return string(b)
}
