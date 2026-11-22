package policyapply

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
)

// Candidate is a generation a server may hold: its number, compiled templates and the keys it revokes.
type Candidate struct {
	Generation  uint64
	Compiled    *listset.Compiled
	RevokedKeys []string
	// Signers maps each template's owner group to the public key of the scoped signing key expected for it. When
	// non-nil the JWT must carry exactly those scoped keys, each with its group as role and the template's
	// permissions, and no other signing key, scoped or plain. When nil, generations are told apart by role,
	// permissions and revocations alone, so a JWT with a different or an extra signer can be read as the generation:
	// that is for tests only. A caller in production MUST pass Signers; Run and Gate refuse a target without it.
	Signers map[string]string
	// Kept maps group -> public key of the scoped signing keys of other ranges that the account JWT keeps beside this
	// range's (the serve signer leaves them alone). When Kept is non-nil each must be present as a scoped signer
	// with its group as role, its group must not be a template of this candidate, and no key beyond Signers and Kept
	// may be present. A kept key missing, or with another role, fails the read.
	Kept map[string]string
	// Plain are the plain (unscoped) signing keys the account JWT carries (PlainKeys): the configured account signing
	// key and any other issuer's key. They may be present beside Signers and Kept and are not counted as keys beyond
	// them. A plain key not listed still fails the read.
	Plain []string
	// RequiredPlain are plain signing keys that MUST be in the account JWT, unscoped: the configured account signing
	// key (signing.nk). Plain is derived from the JWT itself, so only this field catches a JWT that lost the key.
	RequiredPlain []string
}

// GenerationOf says which candidate the account JWT carries. The JWT carries no marker: a scoped signing key whose
// role is a template's Group (the role is the owner group "range.owner", the name the template is filed under)
// carries that generation's Template.Permissions, and the revocation list holds every key the generation revokes. A
// scoped key for a group of another candidate that this one lacks must be absent. When several candidates match the
// first wins, so list the target first. A target equal in content to the previous generation therefore matches the
// previous JWT too: Gate tells them apart by the target's signed token (GateInput.TargetJWT). ok is false when the JWT does not decode or no candidate matches.
func GenerationOf(accountJWT string, candidates []Candidate) (gen uint64, ok bool) {
	claims, err := jwt.DecodeAccountClaims(accountJWT)
	if err != nil {
		return 0, false
	}
	return generationOfClaims(claims, candidates)
}

func generationOfClaims(claims *jwt.AccountClaims, candidates []Candidate) (uint64, bool) {
	roles := map[string]scoped{} // role -> scoped key
	for k := range claims.SigningKeys {
		sc, _ := claims.SigningKeys.GetScope(k)
		if us, isUser := sc.(*jwt.UserScope); isUser && us != nil {
			roles[us.Role] = scoped{key: k, perms: canonPerms(us.Template.Permissions)}
		}
	}
	for _, c := range candidates {
		if c.Compiled != nil && matches(claims, roles, c, candidates) {
			return c.Generation, true
		}
	}
	return 0, false
}

// scoped is a scoped signing key as found in the JWT.
type scoped struct{ key, perms string }

func matches(claims *jwt.AccountClaims, roles map[string]scoped, c Candidate, all []Candidate) bool {
	own := map[string]bool{}
	for _, t := range c.Compiled.Templates {
		own[t.Group] = true
		got, has := roles[t.Group]
		if !has || got.perms != canonPerms(t.Permissions) {
			return false
		}
		if c.Signers != nil && got.key != c.Signers[t.Group] {
			return false
		}
	}
	for g, pub := range c.Kept {
		if own[g] {
			return false // a kept key never stands for a group of this candidate
		}
		sc, _ := claims.SigningKeys.GetScope(pub)
		if us, isUser := sc.(*jwt.UserScope); !isUser || us == nil || us.Role != g {
			return false // a kept signer missing, plain, or with another role
		}
	}
	for _, k := range c.RequiredPlain {
		if _, has := claims.SigningKeys[k]; !has {
			return false // the configured signing key is missing
		}
		if sc, _ := claims.SigningKeys.GetScope(k); sc != nil {
			return false // present but scoped: no longer the plain key
		}
	}
	n := len(claims.SigningKeys)
	for _, k := range c.Plain {
		if sc, _ := claims.SigningKeys.GetScope(k); sc == nil {
			if _, has := claims.SigningKeys[k]; has {
				n--
			}
		}
	}
	if c.Signers != nil && n != len(own)+len(c.Kept) {
		return false // a plain, a duplicate-role or any other signing key beyond the expected ones
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

// PlainKeys is every plain (unscoped) signing key of claims, sorted: the keys mash carries unchanged.
func PlainKeys(claims *jwt.AccountClaims) []string {
	if claims == nil {
		return nil
	}
	var out []string
	for _, k := range claims.SigningKeys.Keys() {
		if sc, _ := claims.SigningKeys.GetScope(k); sc == nil {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
