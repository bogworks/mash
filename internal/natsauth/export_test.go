package natsauth

import "net"

// SetBaseDial replaces the connection LoopbackDialer opens, to fake name resolution; it returns the restore function.
func SetBaseDial(f func(network, address string) (net.Conn, error)) (restore func()) {
	old := baseDial
	baseDial = f
	return func() { baseDial = old }
}
