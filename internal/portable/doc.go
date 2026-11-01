// Package portable holds the portable-code lint: a plain go test that walks the source tree and counts places where
// product code carries a personal convention (a person's or machine's name, a home or temp path, a foreign GitHub
// org, an inline long timeout). baseline.txt records the count allowed per rule and file; it can only shrink. See
// ADR 0045 and the "Portable code" section of CONTRIBUTING.md. The package has no non-test code.
package portable
