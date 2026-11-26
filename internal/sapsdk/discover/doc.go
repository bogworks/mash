// Package discover is mash's own caller side for the 0.6.0 SDK adoption (stage S5, REQ-008, REQ-015, REQ-016): discovery
// with the TO-12 count, and the live prompt, both on the connection the asker already has.
//
// It is kept in mash's own code (ruling A): discovery, presence and the prompt sender are mash's rules, and the SDK
// caller is not used. The SDK caller's Agent value can be had only from its own Discover, and its prompt sends an
// envelope and looks the sender's identity up on the wire, so it can neither take mash's verified registration nor send
// main's bare text byte for byte. The reply side (a client-id token, a route token per request, one subscription per
// request) is therefore built here on nats.go directly, with every random token read from crypto/rand when it is needed.
//
// It imports internal/sap and internal/sapsdk and never internal/endpoint (the wire is made in a test file of
// internal/endpoint, and the wiring PR will make it in non-test code). Nothing a 0.5.x binary links imports it: the
// release check's guard refuses internal/sapsdk/... in 0.5.x.
package discover
