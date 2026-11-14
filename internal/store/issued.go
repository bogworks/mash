package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// IssuedStreamName is the stream that backs the issue record's bucket.
const IssuedStreamName = "KV_" + IssuedBucket

// IssuedHistory is the most revisions the issue record keeps of one key: 64, the most a JetStream KV bucket allows
// (nats.go's KeyValueMaxHistory). A key is re-recorded only by a reissue that keeps it, so 64 is far more than a key
// sees; the cap is still a cap: a 65th revision of one key evicts the oldest, and the record holds the last 64.
const IssuedHistory = 64

// IssuedMinHistory is the smallest per-key history verification accepts. The first version of the record kept 8
// revisions of a key; such a bucket is accepted and `mash admin init` raises it to IssuedHistory (a raise loses
// nothing).
const IssuedMinHistory = 8

// IssuedMaxBytes caps the issue record's bucket. A record is a few hundred bytes. The stream discards new messages
// when full, never an old record, so a full bucket refuses new issues (fail closed) and nothing is lost.
const IssuedMaxBytes = 8 << 20

// The kinds of an issued user.
const (
	// IssuedEndpoint is a policy endpoint's user, issued by `mashctl add` or `mashctl reissue`.
	IssuedEndpoint = "endpoint"
	// IssuedAdmin is the admin user mash or the operator uses to administer the account.
	IssuedAdmin = "admin"
)

// The purposes of an issued user: why mash issued the credential. The set is closed; an unknown purpose is refused on
// write.
const (
	// PurposeIssue is an ordinary issue: `mashctl add`, a reissue that keeps the key, the admin user at init.
	PurposeIssue = "issue"
	// PurposeRotation is the new key of a reissue that rotates the user key.
	PurposeRotation = "rotation"
	// PurposeProbe is a probe credential of the policy gate. No mash path writes it yet.
	PurposeProbe = "probe"
)

// ValidPurpose reports whether p is one of the closed set of purposes.
func ValidPurpose(p string) bool {
	switch p {
	case PurposeIssue, PurposeRotation, PurposeProbe:
		return true
	}
	return false
}

// Issued is one entry of the issue record: a user JWT mash issued. It holds public information only, never a seed or
// a JWT. The bucket key is the user's public key.
type Issued struct {
	PublicKey string `json:"public_key"`
	// Address is the endpoint's directory name (for an admin user, the name in its JWT). Owner and Zone are the
	// owner the name lives under and the zone the signing key belongs to ("" when the user has none).
	Address string   `json:"address"`
	Owner   string   `json:"owner,omitempty"`
	Zone    string   `json:"zone,omitempty"`
	Kind    string   `json:"kind"`
	Tags    []string `json:"tags"`
	// Generation is the generation of the policy the JWT was issued under. mash has no policy generation yet, so it
	// is 0.
	Generation int `json:"generation"`
	// IssuedAt is RFC 3339, UTC.
	IssuedAt string `json:"issued_at"`
	// Purpose is why the credential was issued (PurposeIssue, PurposeRotation, PurposeProbe). A record from before the
	// field has none, which is read as PurposeIssue (see EffectivePurpose).
	Purpose string `json:"purpose,omitempty"`
	// RevokedAt is RFC 3339, UTC: when mash revoked the key. Omitted while unset; once set it is never cleared.
	RevokedAt string `json:"revoked_at,omitempty"`
}

// EffectivePurpose is Purpose, or PurposeIssue for a record written before the field existed. An unknown value is
// returned as it is.
func (i Issued) EffectivePurpose() string {
	if i.Purpose == "" {
		return PurposeIssue
	}
	return i.Purpose
}

// IssuedConfig is the issue record's bucket: user public key -> Issued, written only by the minting authority (the
// admin connection of mashctl). It has no TTL and a history of IssuedHistory.
func IssuedConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: IssuedBucket, Description: "mash issue record: every user JWT mash issued",
		History: IssuedHistory, Storage: jetstream.FileStorage, MaxBytes: IssuedMaxBytes}
}

// ErrIssuedBucket is returned when the issue record's stream is not configured as mash makes it.
var ErrIssuedBucket = errors.New("issue record bucket is not hardened")

// CreateIssuedBucket creates the issue record's bucket with its stream refusing rollup, delete and purge from the first
// moment, the way CreateLeaseBucket does for the lease bucket (the KV default allows rollup, and KeyValueConfig cannot
// say otherwise). If the stream already exists the existing bucket is verified, never altered. It is verified after
// creation and an error is returned unless it is as mash makes it (fail closed).
func CreateIssuedBucket(ctx context.Context, js jetstream.JetStream) (jetstream.KeyValue, error) {
	cfg := IssuedConfig()
	if _, err := js.AccountInfo(ctx); err != nil {
		return nil, err
	}
	scfg := jetstream.StreamConfig{
		Name:              IssuedStreamName,
		Description:       cfg.Description,
		Subjects:          []string{"$KV." + IssuedBucket + ".>"},
		MaxMsgsPerSubject: IssuedHistory,
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
	kv, err := js.KeyValue(ctx, IssuedBucket)
	if err != nil {
		return nil, err
	}
	if err := VerifyIssuedBucket(ctx, js); err != nil {
		return nil, err
	}
	return kv, nil
}

// VerifyIssuedBucket reads the issue record's stream and returns an error wrapping ErrIssuedBucket unless it refuses
// rollup, delete and purge, keeps no TTL, discards new messages when full and keeps at least IssuedMinHistory
// revisions of a key. It never changes the bucket.
func VerifyIssuedBucket(ctx context.Context, js jetstream.JetStream) error {
	s, err := js.Stream(ctx, IssuedStreamName)
	if err != nil {
		return fmt.Errorf("mash: issue record stream: %w", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: issue record stream: %w", err)
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
	case c.MaxMsgsPerSubject < IssuedMinHistory:
		bad = fmt.Sprintf("keeps fewer than %d revisions of a key", IssuedMinHistory)
	}
	if bad != "" {
		return fmt.Errorf("mash: %s %s: %w; recreate it as `mash admin init` makes it, copying its records", IssuedBucket, bad, ErrIssuedBucket)
	}
	return nil
}

// ErrKeyRevoked and ErrKeyBoundElsewhere are what a checked write of the issue record refuses with: the key's record has
// revoked_at set, or is bound to an address other than the one being written.
var (
	ErrKeyRevoked        = errors.New("mash: issue record: the public key has been revoked")
	ErrKeyBoundElsewhere = errors.New("mash: issue record: the public key is bound to another address")
)
