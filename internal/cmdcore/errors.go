package cmdcore

import "github.com/bogworks/mash/internal/cliframe"

// NoSuchEndpoint is the failure for an address nothing answers to (exit 1).
func NoSuchEndpoint(addr string) *cliframe.Error {
	return cliframe.Failf("no endpoint has the address %s", addr).WithHint("mash who")
}

// NotSignedIn is the failure for an endpoint whose credentials the server refused (exit 1).
func NotSignedIn(addr string) *cliframe.Error {
	return cliframe.Failf("%s cannot sign in: the server refused its credentials", addr).WithHint("mash doctor")
}

// ServerNotAnswering is the failure for a server that could not be reached or did not answer (exit 1).
func ServerNotAnswering(url string) *cliframe.Error {
	return cliframe.Failf("the server did not answer: %s", url).WithHint("mash doctor")
}

// errNoEnv is the failure for a command run without an environment: a programming error, never a panic.
func errNoEnv(name string) *cliframe.Error {
	return cliframe.Failf("internal: mash %s has no environment to run in", name)
}
