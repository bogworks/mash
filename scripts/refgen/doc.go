// Command refgen generates the plain-markdown command reference under
// docs/reference from the spec files in spec/. The work is done by
// internal/refgen, which the release gate also imports.
//
// Regenerate from the repository root with:
//
//	go run ./scripts/refgen
package main

//go:generate go run . -spec ../../spec -out ../../docs/reference
