package policyapply

import (
	"fmt"
	"regexp"
)

// The policy bucket is mash's: mash creates it on the first apply (see KVLedgerOptions.Create) and owns it. It holds
// one value per account at status.<account public key>, with the pending JWT and the run lease beside it; only mash
// writes status.* keys. An outside system that wants to submit a list set may write listset.<account public key> into
// the bucket, with a credential limited to $KV.<bucket>.listset.>. The key names below are the interface of that
// submission channel; a change of them is a one-line change in this file, and nothing else in the package spells a key.
//
// The bucket is named DefaultPolicyBucket unless the operator sets PolicyBucketEnv to another name.

// DefaultPolicyBucket is the name of the policy bucket when PolicyBucketEnv is not set.
const DefaultPolicyBucket = "mash-policy"

// PolicyBucketEnv is the environment variable a command reads for the bucket name pattern. It is optional.
const PolicyBucketEnv = "MASH_POLICY_BUCKET"

var bucketRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// PolicyBucket returns the name of the KV bucket: name, or DefaultPolicyBucket when name is empty. It refuses a name
// that is not a valid KV bucket name (letters, digits, '-' and '_').
func PolicyBucket(name string) (string, error) {
	if name == "" {
		return DefaultPolicyBucket, nil
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
