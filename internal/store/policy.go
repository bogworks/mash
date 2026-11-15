package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// PolicyHistory is how many revisions of a key the policy bucket keeps. The revisions are the audit trail of what was
// in force and when.
const PolicyHistory = 16

// PolicyMaxBytes caps the policy bucket at 256 MiB. A value is at most the connection's max payload less headroom
// (1 MiB by default), and the ledger keeps up to PolicyHistory revisions of about three keys per account, so the cap
// leaves room for dozens of full-size values; no value the ledger writes is refused for being over it. The stream
// discards new messages when full, never an old revision.
const PolicyMaxBytes = 256 << 20

// PolicyStreamName is the stream that backs the policy bucket called bucket.
func PolicyStreamName(bucket string) string { return "KV_" + bucket }

// PolicyConfig is the policy bucket called name: file storage, PolicyHistory revisions, no TTL.
func PolicyConfig(name string) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: name, Description: "mash policy ledger: the list set in force, the pending record, the latest status and the run lease",
		History: PolicyHistory, Storage: jetstream.FileStorage, MaxBytes: PolicyMaxBytes}
}

// ErrPolicyBucket is returned when the policy bucket's stream is not configured as mash makes it.
var ErrPolicyBucket = errors.New("policy bucket is not configured as mash makes it")

// CreatePolicyBucket creates the policy bucket called name with its stream refusing rollup, delete and purge from the
// first moment: the ledger never deletes or purges a key (it releases a lease by writing an expired value). If the
// stream already exists it is verified, never altered: a bucket with another configuration is an error wrapping
// ErrPolicyBucket, and a caller that wants to use such a bucket as it is opens it with js.KeyValue instead. The
// bucket is verified after creation (fail closed).
func CreatePolicyBucket(ctx context.Context, js jetstream.JetStream, name string) (jetstream.KeyValue, error) {
	cfg := PolicyConfig(name)
	if _, err := js.AccountInfo(ctx); err != nil {
		return nil, err
	}
	scfg := jetstream.StreamConfig{
		Name:              PolicyStreamName(name),
		Description:       cfg.Description,
		Subjects:          []string{"$KV." + name + ".>"},
		MaxMsgsPerSubject: PolicyHistory,
		MaxBytes:          cfg.MaxBytes,
		MaxMsgSize:        -1,
		Storage:           jetstream.FileStorage,
		Replicas:          1,
		AllowRollup:       false,
		DenyDelete:        true,
		DenyPurge:         true,
		Duplicates:        leaseDuplicateWindow,
		MaxMsgs:           -1,
		MaxConsumers:      -1,
		AllowDirect:       true,
		Discard:           jetstream.DiscardNew,
	}
	if _, err := js.CreateStream(ctx, scfg); err != nil && !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return nil, err
	}
	if err := VerifyPolicyBucket(ctx, js, name); err != nil {
		return nil, err
	}
	return js.KeyValue(ctx, name)
}

// VerifyPolicyBucket reads the policy bucket's stream and returns an error wrapping ErrPolicyBucket unless it refuses
// rollup, delete and purge, keeps no TTL, is on file storage, discards new messages when full and keeps at least
// PolicyHistory revisions of a key. It never changes the bucket.
func VerifyPolicyBucket(ctx context.Context, js jetstream.JetStream, name string) error {
	s, err := js.Stream(ctx, PolicyStreamName(name))
	if err != nil {
		return fmt.Errorf("mash: policy stream: %w", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: policy stream: %w", err)
	}
	c := info.Config
	var bad string
	switch {
	case c.AllowRollup:
		bad = "allows rollup"
	case !c.DenyDelete:
		bad = "allows deleting messages"
	case !c.DenyPurge:
		bad = "allows purging"
	case c.MaxAge != 0:
		bad = "expires messages"
	case c.Storage != jetstream.FileStorage:
		bad = "is not on file storage"
	case c.Discard != jetstream.DiscardNew:
		bad = "discards old records when full"
	case c.MaxMsgsPerSubject < PolicyHistory:
		bad = fmt.Sprintf("keeps fewer than %d revisions of a key", PolicyHistory)
	}
	if bad != "" {
		return fmt.Errorf("mash: %s %s: %w", name, bad, ErrPolicyBucket)
	}
	return nil
}
