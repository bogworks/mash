package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// AdoptionStreamName is the stream that backs the adoption marker's bucket.
const AdoptionStreamName = "KV_" + AdoptionBucket

// AdoptionKey is the one key of the adoption bucket. The bucket holds one marker for the account.
const AdoptionKey = "account"

// AdoptionHistory is the most revisions the marker keeps: 64, the most a JetStream KV bucket allows. The revisions
// are the audit trail of who adopted the account and when, and of each recovery rotation.
const AdoptionHistory = 64

// AdoptionMaxBytes caps the adoption bucket. A marker is a few hundred bytes. The stream discards new messages when
// full, never an old revision.
const AdoptionMaxBytes = 1 << 20

// AdoptionSchema is the version of the marker's value (its v field). A reader refuses any other.
const AdoptionSchema = 1

// The actions a marker revision records.
const (
	// ActionAdopt is the first adoption of the account.
	ActionAdopt = "adopt"
	// ActionRotate is a planned rotation of the submitter key, by a list set signed by the current submitter.
	ActionRotate = "rotate"
	// ActionRecover is a recovery rotation: the operator adopted again with a different key.
	ActionRecover = "recover"
)

// Marker is the adoption marker: the account's list sets come only from an outside system, through the one submitter
// it registered. It holds public keys only. NextSubmitter is written only by the list-set intake, never by the
// adopt command.
type Marker struct {
	V             int    `json:"v"`
	Account       string `json:"account"`
	Submitter     string `json:"submitter"`
	NextSubmitter string `json:"next_submitter,omitempty"`
	// AdoptedAt is RFC 3339, UTC: when this revision was written.
	AdoptedAt string `json:"adopted_at"`
	// By is the public key of the admin user that ran the command.
	By     string `json:"by"`
	Action string `json:"action"`
}

// AdoptionConfig is the adoption bucket: one key, written only over the admin connection, no TTL, a history of
// AdoptionHistory.
func AdoptionConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: AdoptionBucket, Description: "mash adoption marker: the account's list sets come from an outside system",
		History: AdoptionHistory, Storage: jetstream.FileStorage, MaxBytes: AdoptionMaxBytes}
}

// ErrAdoptionBucket is returned when the adoption bucket's stream is not configured as mash makes it.
var ErrAdoptionBucket = errors.New("adoption bucket is not hardened")

// CreateAdoptionBucket creates the adoption bucket with its stream refusing rollup, delete and purge from the first
// moment, as CreateIssuedBucket does for the issue record. If the stream already exists the existing bucket is
// verified, never altered. It is verified after creation and an error is returned unless it is as mash makes it
// (fail closed).
func CreateAdoptionBucket(ctx context.Context, js jetstream.JetStream) (jetstream.KeyValue, error) {
	cfg := AdoptionConfig()
	if _, err := js.AccountInfo(ctx); err != nil {
		return nil, err
	}
	scfg := jetstream.StreamConfig{
		Name:              AdoptionStreamName,
		Description:       cfg.Description,
		Subjects:          []string{"$KV." + AdoptionBucket + ".>"},
		MaxMsgsPerSubject: AdoptionHistory,
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
	kv, err := js.KeyValue(ctx, AdoptionBucket)
	if err != nil {
		return nil, err
	}
	if err := VerifyAdoptionBucket(ctx, js); err != nil {
		return nil, err
	}
	return kv, nil
}

// VerifyAdoptionBucket reads the adoption bucket's stream and returns an error wrapping ErrAdoptionBucket unless it
// refuses rollup, delete and purge, keeps no TTL, is on file storage, discards new messages when full and keeps
// AdoptionHistory revisions of a key. It never changes the bucket.
func VerifyAdoptionBucket(ctx context.Context, js jetstream.JetStream) error {
	s, err := js.Stream(ctx, AdoptionStreamName)
	if err != nil {
		return fmt.Errorf("mash: adoption stream: %w", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: adoption stream: %w", err)
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
	case c.MaxMsgsPerSubject < AdoptionHistory:
		bad = fmt.Sprintf("keeps fewer than %d revisions of a key", AdoptionHistory)
	}
	if bad != "" {
		return fmt.Errorf("mash: %s %s: %w; recreate it as `mash admin init` makes it, copying its revisions", AdoptionBucket, bad, ErrAdoptionBucket)
	}
	return nil
}
