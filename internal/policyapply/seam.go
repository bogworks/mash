package policyapply

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The policy bucket is mash's: mash creates it on the first apply (see KVLedgerOptions.Create) and owns it. It holds
// one value per account at status.<account public key>, with the pending JWT and the run lease beside it; only mash
// writes status.* keys. An outside system that wants to submit a list set may write listset.<account public key> into
// the bucket, with a credential limited to $KV.<bucket>.listset.>. The key names below are the interface of that
// submission channel; a change of them is a one-line change in this file, and nothing else in the package spells a key.
//
// The bucket is named DefaultPolicyBucket unless the operator sets PolicyBucketEnv to another name. The setting is a
// pattern: {range} is replaced by the range name, for an operator who keeps one bucket per range.

// DefaultPolicyBucket is the name of the policy bucket when PolicyBucketEnv is not set.
const DefaultPolicyBucket = "mash-policy"

// PolicyBucketEnv is the environment variable a command reads for the bucket name pattern. It is optional.
const PolicyBucketEnv = "MASH_POLICY_BUCKET"

const rangePlaceholder = "{range}"

var bucketRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PolicyBucket returns the name of the KV bucket of range rng. pattern is a bucket name that may contain the
// placeholder {range}, which is replaced by rng; an empty pattern means DefaultPolicyBucket. It refuses an empty rng when the pattern has the
// placeholder, and a result that is not a valid KV bucket name (letters, digits, '-' and '_').
func PolicyBucket(pattern, rng string) (string, error) {
	if pattern == "" {
		return DefaultPolicyBucket, nil
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

// PendingJWTKey is the key of the last account JWT pushed for the pending generation of account. It is a key of its
// own so the status value never holds a second account JWT (the server's payload limit is about one JWT's size).
func PendingJWTKey(account string) string { return StatusKey(account) + ".pending" }

// LeaseKey is the key of the run lease of account. It sits beside the status value, under the same prefix.
func LeaseKey(account string) string { return StatusKey(account) + ".lease" }
