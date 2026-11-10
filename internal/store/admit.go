package store

import (
	"fmt"

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
//   - an existing record names no identity, since it cannot be modelled (fail closed).
//
// A record whose key equals id's flat key is the identity being re-admitted and is skipped, so a retried provision of
// the same endpoint works.
func Admit(id sap.EndpointIdentity, zone string, existing []Record) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("mash: admit: %w", err)
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
	}
	return nil
}
