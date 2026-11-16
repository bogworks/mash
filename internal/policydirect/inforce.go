package policydirect

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// InForce is the list set the account runs under, compiled, and its generation: what the ledger of this Config's policy
// bucket holds, which is what Apply writes. It reads and writes nothing else and never creates the bucket. With nothing
// applied yet (no bucket, or no list set in force) it returns a nil list set and no error, so a caller that needs a
// policy refuses on its own terms; any other failure is an error. It is the issue path's list set in force when the
// policy was applied by this driver.
func (c Config) InForce(ctx context.Context) (*listset.Compiled, int, error) {
	if err := c.check(); err != nil {
		return nil, 0, err
	}
	rec, ls, ok, err := inForceView(ctx, c.Admin, c.Account, c.Bucket)
	if err != nil || !ok {
		return nil, 0, err
	}
	if rec.Generation > math.MaxInt {
		return nil, 0, fmt.Errorf("generation %d does not fit an int", rec.Generation)
	}
	c2, err := listset.Compile(ls)
	if err != nil {
		return nil, 0, fmt.Errorf("the list set in force (generation %d) does not compile: %w", rec.Generation, err)
	}
	return c2, int(rec.Generation), nil
}

// ListSetGeneration is the generation of the list set in force for account, read from the same ledger InForce reads,
// through the admin connection alone (no system account, key store or server count). It is 0 when none is in force: the
// policy bucket does not exist, or no list set was applied. bucket is the policy bucket, "" for the default. Any other
// failure is an error, so a caller that must refuse while a list set is in force fails closed.
func ListSetGeneration(ctx context.Context, a *admin.Admin, account, bucket string) (uint64, error) {
	if a == nil || account == "" {
		return 0, fmt.Errorf("%w: no admin connection or account", errConfig)
	}
	rec, _, ok, err := inForceView(ctx, a, account, bucket)
	if err != nil || !ok {
		return 0, err
	}
	return rec.Generation, nil
}

// inForceView reads the in-force record and list set of account's ledger; ok is false when none is in force.
func inForceView(ctx context.Context, a *admin.Admin, account, bucketName string) (rec listset.Record, ls listset.ListSet, ok bool, err error) {
	bucket := bucketName
	if bucket == "" {
		if bucket, err = policyapply.PolicyBucket(""); err != nil {
			return rec, ls, false, err
		}
	}
	js, err := jetstream.New(a.Conn())
	if err != nil {
		return rec, ls, false, err
	}
	l, err := policyapply.NewKVLedger(ctx, js, account, policyapply.KVLedgerOptions{Bucket: bucket, Holder: Holder})
	if err != nil {
		if errors.Is(err, policyapply.ErrNoPolicyBucket) {
			return rec, ls, false, nil
		}
		return rec, ls, false, err
	}
	rec, ls, ok, err = l.InForceView()
	if err != nil {
		return rec, ls, false, fmt.Errorf("read the list set in force: %w", err)
	}
	if !ok || rec.Generation == 0 {
		return rec, ls, false, nil
	}
	return rec, ls, true, nil
}
