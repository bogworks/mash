A drained removal (`Admin.DeprovisionDrained`) now accepts a new-form `agent_owner_name` key, where it used to refuse it as an
invalid name. It writes the retired marker pending before the user is revoked and final after the record is purged, finishes
a removal that stopped after the purge when it is run again, and so the name is never reassigned
([ADR 0088](docs/decisions/0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md)).
`mashctl suspend`, `resume`, `suspended` and `deprovision` (and `Admin.Suspend`, `Resume`, `Suspended`) now accept a new-form
`agent_owner_name` key too, so the drained removal can be reached by the supported commands; a legacy name works as before.
