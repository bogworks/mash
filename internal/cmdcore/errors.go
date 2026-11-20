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
