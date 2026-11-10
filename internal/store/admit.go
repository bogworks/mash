package store

import (
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/sap"
)

// Admit is mash's own admission rule for a new endpoint identity against the directory records already present. It
// holds with or without any optional name-rule file. It returns nil, or an error naming the rule and the existing key
// involved. zone is the zone of the identity being admitted and applies to a legacy identity only.
//
// It refuses id when
//   - id overlaps (sap.Overlaps) the identity of an existing record, where at least one of the two is new-form
//     (two legacy identities are never compared: that is today's behaviour);
//   - a new-form identity and a legacy one have the same owner token, whichever of the two is being admitted: the
//     legacy wildcard mail filter would reach the new-form endpoint;
//   - a zoned legacy endpoint and a new-form one would coexist: a zoned legacy endpoint has a six-token subject that
//     Overlaps does not model, so disjointness cannot be proved;
//   - an existing record names no identity, since it cannot be modelled (fail closed). A legacy-keyed record (no
//     underscore in its key) is not modelled when id is legacy: the pair is never compared.
//
// A record whose key equals id's flat key is the identity being re-admitted and is skipped, so a retried provision of
// the same endpoint works.
func Admit(id sap.EndpointIdentity, zone string, existing []Record) error {
	return AdmitWithAlias(id, nil, zone, existing)
}

// AdmitWithAlias is Admit for a new-form id that also answers to the legacy identity alias (ADR 0076), or for any id
// when alias is nil. The alias's subjects count as part of the identity. With a non-nil alias (id must be new-form,
// alias legacy, owners different) it also refuses when
//   - no existing unzoned legacy record has exactly the alias's owner and name: the alias names no legacy endpoint;
//   - another new-form record already claims the same alias: one alias per legacy identity;
//   - the alias overlaps an existing new-form identity.
//
// For every id, with or without an alias, it refuses an id that overlaps the alias of an existing record, unless id is
// the legacy endpoint that alias names.
func AdmitWithAlias(id sap.EndpointIdentity, alias *sap.EndpointIdentity, zone string, existing []Record) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("mash: admit: %w", err)
	}
	if alias != nil {
		if id.IsLegacy() {
			return fmt.Errorf("mash: admit %s refused: a legacy identity cannot carry an alias", id)
		}
		if !alias.IsLegacy() {
			return fmt.Errorf("mash: admit %s refused: the alias %s is not a legacy identity", id, alias)
		}
		if err := alias.Validate(); err != nil {
			return fmt.Errorf("mash: admit %s refused: alias: %w", id, err)
		}
		if alias.Owner == id.Owner {
			return fmt.Errorf("mash: admit %s refused: alias owner equals the identity's owner (%q): the legacy wildcard filter would reach it", id, id.Owner)
		}
	}
	if !id.IsLegacy() {
		zone = ""
	}
	self := id.Flat()
	for _, r := range existing {
		key := r.KeyOrName()
		if key == self {
			continue
		}
		if id.IsLegacy() && !strings.Contains(key, "_") {
			// Two legacy identities are never compared, so an odd legacy record that has no modellable identity
			// cannot matter here and must not block a legacy-only directory.
			continue
		}
		other, err := r.Identity()
		if err != nil {
			return fmt.Errorf("mash: admit %s refused: unmodellable record %s in the directory: %w", id, key, err)
		}
		if !id.IsLegacy() && other.IsLegacy() && r.Zone != "" {
			return fmt.Errorf("mash: admit %s refused: zoned legacy endpoint present (%s, zone %s): cannot prove disjointness", id, key, r.Zone)
		}
		if id.IsLegacy() && !other.IsLegacy() && zone != "" {
			return fmt.Errorf("mash: admit %s refused: zoned legacy endpoint (zone %s) while new-form record %s exists: cannot prove disjointness", id, zone, key)
		}
		if (!id.IsLegacy() || !other.IsLegacy()) && sap.Overlaps(id, other) {
			return fmt.Errorf("mash: admit %s refused: overlap with existing endpoint %s (%s)", id, key, other)
		}
		if !id.IsLegacy() && other.IsLegacy() && id.Owner == other.Owner {
			return fmt.Errorf("mash: admit %s refused: owner equals a legacy owner (%q, existing endpoint %s): the legacy wildcard filter would reach it", id, id.Owner, key)
		}
		if id.IsLegacy() && !other.IsLegacy() && id.Owner == other.Owner {
			return fmt.Errorf("mash: admit %s refused: owner equals a new-form owner (%q, existing endpoint %s): this legacy wildcard filter would reach it", id, id.Owner, key)
		}
		// The alias of an existing record is part of that record's identity.
		oa, has, err := r.AliasIdentity()
		if err != nil {
			return fmt.Errorf("mash: admit %s refused: unmodellable alias on record %s: %w", id, key, err)
		}
		if has {
			if other.IsLegacy() {
				return fmt.Errorf("mash: admit %s refused: record %s is legacy and carries an alias", id, key)
			}
			if alias != nil && alias.Owner == oa.Owner && alias.Name == oa.Name {
				return fmt.Errorf("mash: admit %s refused: alias %s/%s is already claimed by %s", id, alias.Owner, alias.Name, key)
			}
			isNamed := id.IsLegacy() && id.Owner == oa.Owner && id.Name == oa.Name
			if !isNamed && sap.Overlaps(id, oa) {
				return fmt.Errorf("mash: admit %s refused: overlap with the alias %s/%s of existing endpoint %s", id, oa.Owner, oa.Name, key)
			}
		}
		if alias != nil && !other.IsLegacy() && sap.Overlaps(*alias, other) {
			return fmt.Errorf("mash: admit %s refused: its alias %s/%s overlaps existing endpoint %s (%s)", id, alias.Owner, alias.Name, key, other)
		}
	}
	if alias != nil {
		named := false
		for _, r := range existing {
			if r.Zone == "" && r.Name == alias.Name && r.Owner == alias.Owner && r.Key() == r.Name {
				named = true
				break
			}
		}
		if !named {
			return fmt.Errorf("mash: admit %s refused: the alias names no legacy endpoint (%s/%s): an unzoned legacy record with that owner and name must exist", id, alias.Owner, alias.Name)
		}
	}
	return nil
}
