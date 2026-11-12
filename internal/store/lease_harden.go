package store

import (
	"context"
	"errors"
	"fmt"

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
