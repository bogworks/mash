package policyapply

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The seam with whatever writes list sets: one KV bucket per owner, which the administrator creates. List sets are
// written to listset.<account public key> there; mash writes one value per account at status.<account public key>
// and nothing else, and only mash writes status.* keys.
//
// The bucket name is configuration, not a built-in name, because the seam's design is not settled yet. The proposed
// pattern is "<prefix>_{owner}_policy", set by the administrator in PolicyBucketEnv; {owner} is replaced by the owner
// name. The key names below are the proposal too. A change of the key names is a one-line change in this file;
// nothing else in the package spells a key.

// PolicyBucketEnv is the environment variable a command reads for the bucket name pattern.
const PolicyBucketEnv = "MASH_POLICY_BUCKET"

const ownerPlaceholder = "{owner}"

var bucketRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PolicyBucket returns the name of the KV bucket of owner. pattern is a bucket name that may contain the
// placeholder {owner}, which is replaced by owner. It refuses an empty pattern, an empty owner when the pattern has the
// placeholder, and a result that is not a valid KV bucket name (letters, digits, '-' and '_').
func PolicyBucket(pattern, owner string) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("policy bucket: no bucket name pattern; set %s", PolicyBucketEnv)
	}
	name := pattern
	if strings.Contains(pattern, ownerPlaceholder) {
		if owner == "" {
			return "", errors.New("policy bucket: the pattern has {owner} but the owner name is empty")
		}
		name = strings.ReplaceAll(pattern, ownerPlaceholder, owner)
	}
	if !bucketRE.MatchString(name) {
		return "", fmt.Errorf("policy bucket: %q is not a valid KV bucket name (letters, digits, '-' and '_')", name)
	}
	return name, nil
}

// StatusKey is the key of the status value of account (its public key).
func StatusKey(account string) string { return "status." + account }

// PendingJWTKey is the key of the last account JWT pushed for the pending generation of account. It is a key of its
// own so the status value never holds a second account JWT (the server's payload limit is about one JWT's size).
func PendingJWTKey(account string) string { return StatusKey(account) + ".pending" }

// LeaseKey is the key of the run lease of account. It sits beside the status value, under the same prefix.
func LeaseKey(account string) string { return StatusKey(account) + ".lease" }
