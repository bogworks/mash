package policyapply

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The seam with the prefix manager that writes list sets. Operations creates one KV bucket per prefix, shared with that
// manager, which writes listset.<account public key> there; mash writes one value per account at
// status.<account public key> and nothing else, and only mash writes status.* keys.
//
// The bucket name is configuration, not a built-in name, because the seam (decision D211) is pending. The proposed
// pattern is "<manager>_{prefix}_policy", set by the operator in PolicyBucketEnv; {prefix} is replaced by the address prefix
// name. The key names below are the proposal too. A change of the key names is a one-line change in this file;
// nothing else in the package spells a key.

// PolicyBucketEnv is the environment variable a command reads for the bucket name pattern.
const PolicyBucketEnv = "MASH_POLICY_BUCKET"

const prefixPlaceholder = "{prefix}"

var bucketRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PolicyBucket returns the name of the KV bucket of address prefix prefix. pattern is a bucket name that may contain the
// placeholder {prefix}, which is replaced by prefix. It refuses an empty pattern, an empty prefix when the pattern has the
// placeholder, and a result that is not a valid KV bucket name (letters, digits, '-' and '_').
func PolicyBucket(pattern, prefix string) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("policy bucket: no bucket name pattern; set %s", PolicyBucketEnv)
	}
	name := pattern
	if strings.Contains(pattern, prefixPlaceholder) {
		if prefix == "" {
			return "", errors.New("policy bucket: the pattern has {prefix} but the prefix is empty")
		}
		name = strings.ReplaceAll(pattern, prefixPlaceholder, prefix)
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
