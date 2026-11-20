// Command refgen generates the plain-markdown command reference under
// docs/reference from the spec files in spec/.
//
// Regenerate from the repository root with:
//
//	go run ./scripts/refgen
//
// Every generated page starts with a banner line; refgen deletes only
// banner-carrying files it no longer produces, and nothing else.
//
// Generate is the library entry: it returns every page in memory, keyed by
// slash-separated path relative to the reference folder. CheckDir compares a
// reference folder with what Generate would write, in both directions, and
// returns one line per difference. The test in this package uses it on the
// working tree, and the release gate reuses it on the tagged spec and the
// tagged docs/reference, so a release cannot ship a stale reference.
package main

//go:generate go run . -spec ../../spec -out ../../docs/reference
