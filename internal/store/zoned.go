package store

import (
	"fmt"
)

// ZonePolicyAppliedMessage is the refusal of every command that would run over an applied zone policy (ADR 0091):
// `mashctl policy apply` and `mashctl unzone`. mash builds no migration for a policy deployment.
const ZonePolicyAppliedMessage = "a zone policy is applied: zones are removed (ADR 0091); stay on the last release that has zones until the label policy model ships"

// ErrZonePolicyApplied is ZonePolicyAppliedMessage as an error.
var ErrZonePolicyApplied = fmt.Errorf("%s", ZonePolicyAppliedMessage)

// ZonedRecordError is the refusal for a directory record that carries a zone, or nil when it carries none. Zones are
// removed (ADR 0091): the release that removes them stops instead of dropping mail for an address it can no longer form.
func ZonedRecordError(r Record) error {
	if r.Zone == "" {
		return nil
	}
	return fmt.Errorf("record %s carries zone %q: zones are removed (ADR 0091); run `mashctl unzone %s` to make it five-token, then upgrade", r.KeyOrName(), r.Zone, r.KeyOrName())
}

// FirstZonedRecord is ZonedRecordError for the first zoned record of recs (nil when none is zoned).
func FirstZonedRecord(recs []Record) error {
	for _, r := range recs {
		if err := ZonedRecordError(r); err != nil {
			return err
		}
	}
	return nil
}
