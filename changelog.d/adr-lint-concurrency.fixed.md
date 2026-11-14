The ADR lint check no longer cancels its own run when a label is added or removed on the same head commit, so Mergify
no longer waits forever on a cancelled check: label events queue behind the run, and a new head starts a new group.
