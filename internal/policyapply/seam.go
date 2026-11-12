package policyapply

// The seam with homerange. Operations creates one KV bucket per range, shared with homerange, which writes
// listset.<account public key> there; mash writes one value per account at status.<account public key> and nothing
// else, and only mash writes status.* keys.
//
// DECISION D211 IS PENDING: the bucket name and the key names below are the proposal, not an agreement. A change of
// the seam is a one-line change in the functions of this file; nothing else in the package spells a bucket or key.

// PolicyBucket is the name of the KV bucket of range rng.
func PolicyBucket(rng string) string { return "homerange_" + rng + "_policy" }

// StatusKey is the key of the status value of account (its public key).
func StatusKey(account string) string { return "status." + account }

// LeaseKey is the key of the run lease of account. It sits beside the status value, under the same prefix.
func LeaseKey(account string) string { return StatusKey(account) + ".lease" }
