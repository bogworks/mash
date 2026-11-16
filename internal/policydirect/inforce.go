package policydirect

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/nats-io/nats.go/jetstream"

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
	bucket, err := c.bucket()
	if err != nil {
		return nil, 0, err
	}
	js, err := jetstream.New(c.Admin.Conn())
	if err != nil {
		return nil, 0, err
	}
	l, err := policyapply.NewKVLedger(ctx, js, c.Account, policyapply.KVLedgerOptions{Bucket: bucket, Holder: Holder})
	if err != nil {
		if errors.Is(err, policyapply.ErrNoPolicyBucket) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	rec, ls, ok, err := l.InForceView()
	if err != nil {
		return nil, 0, fmt.Errorf("read the list set in force: %w", err)
	}
	if !ok || rec.Generation == 0 {
		return nil, 0, nil
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
