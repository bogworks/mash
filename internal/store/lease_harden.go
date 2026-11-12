package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrLeaseBucketRollup is returned when the lease bucket's stream allows rollup and that could not be changed.
var ErrLeaseBucketRollup = errors.New("lease bucket allows rollup; run `mash admin init`")

// LeaseStreamName is the stream that backs the lease bucket.
const LeaseStreamName = "KV_" + LeaseBucket

// HardenLeaseBucket makes the lease bucket's stream refuse rollup. The lease bucket does not allow rollup. The KV default stream allows rollup, and KeyValueConfig cannot say otherwise, so the setting is made on the
// stream. It reads the stream, updates it only if AllowRollup is set, then reads it again and returns an error unless
// the effective setting is off (fail closed). It is idempotent. DenyPurge is left as it is (the server refuses
// DenyPurge together with AllowRollup).
func HardenLeaseBucket(ctx context.Context, js jetstream.JetStream) error {
	cfg, err := leaseStreamConfig(ctx, js)
	if err != nil {
		return err
	}
	if cfg.AllowRollup {
		cfg.AllowRollup = false
		if _, err := js.UpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("%w: %v", ErrLeaseBucketRollup, err)
		}
	}
	return VerifyLeaseBucket(ctx, js)
}

// VerifyLeaseBucket reads the lease bucket's stream and returns ErrLeaseBucketRollup unless it refuses rollup.
func VerifyLeaseBucket(ctx context.Context, js jetstream.JetStream) error {
	cfg, err := leaseStreamConfig(ctx, js)
	if err != nil {
		return err
	}
	if cfg.AllowRollup {
		return ErrLeaseBucketRollup
	}
	return nil
}

// LeaseStreamConfig is the current configuration of the lease bucket's stream.
func LeaseStreamConfig(ctx context.Context, js jetstream.JetStream) (jetstream.StreamConfig, error) {
	return leaseStreamConfig(ctx, js)
}

func leaseStreamConfig(ctx context.Context, js jetstream.JetStream) (jetstream.StreamConfig, error) {
	s, err := js.Stream(ctx, LeaseStreamName)
	if err != nil {
		return jetstream.StreamConfig{}, fmt.Errorf("mash: lease bucket stream: %w", err)
	}
	// Info, not CachedInfo: the setting must be the server's current one.
	info, err := s.Info(ctx)
	if err != nil {
		return jetstream.StreamConfig{}, fmt.Errorf("mash: lease bucket stream: %w", err)
	}
	return info.Config, nil
}

// CreateLeaseBucket creates the lease bucket with its stream refusing rollup from the first moment. It builds the
// stream configuration the way nats.go's CreateKeyValue does (name, subjects, history, size cap, TTL, duplicate
// window, discard policy and the rest) with AllowRollup off, so a bucket made here never allows rollup, not even
// briefly. cfg.Bucket must be LeaseBucket; a mirror, sources or a limit marker TTL are not supported. If the stream
// already exists (another caller created it first) the existing bucket is hardened. The result is verified, and an
// error is returned unless the stream refuses rollup (fail closed).
func CreateLeaseBucket(ctx context.Context, js jetstream.JetStream, cfg jetstream.KeyValueConfig) (jetstream.KeyValue, error) {
	if cfg.Bucket != LeaseBucket {
		return nil, fmt.Errorf("mash: lease bucket: unexpected bucket name %q", cfg.Bucket)
	}
	if cfg.Mirror != nil || len(cfg.Sources) > 0 || cfg.LimitMarkerTTL != 0 {
		return nil, errors.New("mash: lease bucket: mirror, sources and limit marker TTL are not supported")
	}
	if _, err := js.AccountInfo(ctx); err != nil {
		return nil, err
	}
	history := int64(1)
	if cfg.History > 0 {
		if cfg.History > jetstream.KeyValueMaxHistory {
			return nil, jetstream.ErrHistoryTooLarge
		}
		history = int64(cfg.History)
	}
	replicas := cfg.Replicas
	if replicas == 0 {
		replicas = 1
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = -1
	}
	maxMsgSize := cfg.MaxValueSize
	if maxMsgSize == 0 {
		maxMsgSize = -1
	}
	duplicateWindow := 2 * time.Minute
	if cfg.TTL > 0 && cfg.TTL < duplicateWindow {
		duplicateWindow = cfg.TTL
	}
	var compression jetstream.StoreCompression
	if cfg.Compression {
		compression = jetstream.S2Compression
	}
	scfg := jetstream.StreamConfig{
		Name:              LeaseStreamName,
		Description:       cfg.Description,
		Subjects:          []string{"$KV." + cfg.Bucket + ".>"},
		MaxMsgsPerSubject: history,
		MaxBytes:          maxBytes,
		MaxAge:            cfg.TTL,
		MaxMsgSize:        maxMsgSize,
		Storage:           cfg.Storage,
		Replicas:          replicas,
		Placement:         cfg.Placement,
		AllowRollup:       false,
		DenyDelete:        true,
		Duplicates:        duplicateWindow,
		MaxMsgs:           -1,
		MaxConsumers:      -1,
		AllowDirect:       true,
		RePublish:         cfg.RePublish,
		Compression:       compression,
		Discard:           jetstream.DiscardNew,
		Metadata:          cfg.Metadata,
	}
	if _, err := js.CreateStream(ctx, scfg); err != nil {
		if !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			return nil, err
		}
		// Lost a create race (or the bucket exists): bring the existing one to the same setting.
		if err := HardenLeaseBucket(ctx, js); err != nil {
			return nil, err
		}
	}
	kv, err := js.KeyValue(ctx, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	if err := VerifyLeaseBucket(ctx, js); err != nil {
		return nil, err
	}
	return kv, nil
}
