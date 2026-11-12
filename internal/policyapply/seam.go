package policyapply

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The seam with the range manager that writes list sets. Operations creates one KV bucket per range, shared with that
// manager, which writes listset.<account public key> there; mash writes one value per account at
// status.<account public key> and nothing else, and only mash writes status.* keys.
//
// The bucket name is configuration, not a built-in name, because the seam (decision D211) is pending. The proposed
// pattern is "<manager>_{range}_policy", set by the operator in PolicyBucketEnv; {range} is replaced by the range
// name. The key names below are the proposal too. A change of the key names is a one-line change in this file;
// nothing else in the package spells a key.

// PolicyBucketEnv is the environment variable a command reads for the bucket name pattern.
const PolicyBucketEnv = "MASH_POLICY_BUCKET"

const rangePlaceholder = "{range}"

var bucketRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PolicyBucket returns the name of the KV bucket of range rng. pattern is a bucket name that may contain the
// placeholder {range}, which is replaced by rng. It refuses an empty pattern, an empty rng when the pattern has the
// placeholder, and a result that is not a valid KV bucket name (letters, digits, '-' and '_').
func PolicyBucket(pattern, rng string) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("policy bucket: no bucket name pattern; set %s", PolicyBucketEnv)
	}
	name := pattern
	if strings.Contains(pattern, rangePlaceholder) {
		if rng == "" {
			return "", errors.New("policy bucket: the pattern has {range} but the range name is empty")
		}
		name = strings.ReplaceAll(pattern, rangePlaceholder, rng)
	}
	if !bucketRE.MatchString(name) {
		return "", fmt.Errorf("policy bucket: %q is not a valid KV bucket name (letters, digits, '-' and '_')", name)
	}
	return name, nil
}

// StatusKey is the key of the status value of account (its public key).
func StatusKey(account string) string { return "status." + account }

// LeaseKey is the key of the run lease of account. It sits beside the status value, under the same prefix.
func LeaseKey(account string) string { return StatusKey(account) + ".lease" }
