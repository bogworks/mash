// Package store holds mash's JetStream layout: per-endpoint inbox streams,
// their consumer, and the directory and lease KV buckets (spec section 5).
package store

// Bucket and consumer names.
const (
	DirectoryBucket = "mash-directory"
	LeaseBucket     = "mash-leases"
	RetiredBucket   = "mash-retired"
	IssuedBucket    = "mash-issued"
	Consumer        = "deliver"
)
