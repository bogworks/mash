package endpoint

import (
	"context"
	"time"

	"github.com/bogworks/mash/internal/store"
)

// leaseRollupTimeout bounds the one server read behind the lease bucket warning: it is advice, so nothing waits long
// on a slow server for it.
const leaseRollupTimeout = 2 * time.Second

// LeaseRollupWarning is the lease bucket line of status, and the reason serve warns at start. It reports true when
// the lease bucket's stream allows rollup writes (a bucket `mash admin init` has not hardened). It never fails and
// changes nothing: when the server denies or cannot answer the stream info read it reports false, so a missing
// permission or a slow server stays silent.
func (e *Endpoint) LeaseRollupWarning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, leaseRollupTimeout)
	defer cancel()
	s, err := e.js.Stream(ctx, store.LeaseStreamName)
	if err != nil {
		return false
	}
	return store.LeaseRollupAllowed(s.CachedInfo().Config)
}

// LeaseRollupLine is the status tool's line for a lease bucket that allows rollup writes; ok is false when there is
// nothing to say.
func (e *Endpoint) LeaseRollupLine(ctx context.Context) (line string, ok bool) {
	if !e.LeaseRollupWarning(ctx) {
		return "", false
	}
	return "lease bucket: WARN - it allows rollup writes; run mash admin init", true
}

// LeaseRollupStartWarning is the stderr line serve prints once at start when the lease bucket allows rollup writes.
const LeaseRollupStartWarning = "mash: warning: the lease bucket allows rollup writes; run `mash admin init` to harden it (an endpoint credential could purge every lease)"
