package testenv

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// AckDropProxy is a TCP proxy that loses a publisher's acknowledgement: once the client has published to a given
// subject, nothing more flows from the server to that client connection. The server still stores the message, so the
// client sees a timeout for a send that did land, the case a retry must not turn into a second copy.
type AckDropProxy struct {
	ln      net.Listener
	target  string
	subject string
	dropped atomic.Int32
}

// NewAckDropProxy listens on a loopback port and forwards to the server at natsURL. It stops when the test ends.
func NewAckDropProxy(t *testing.T, natsURL, subject string) *AckDropProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &AckDropProxy{ln: ln, target: strings.TrimPrefix(natsURL, "nats://"), subject: subject}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return p
}

// URL is the address a client connects to in place of the server's.
func (p *AckDropProxy) URL() string { return "nats://" + p.ln.Addr().String() }

// Dropped is the number of connections whose server traffic was cut after a publish to the subject.
func (p *AckDropProxy) Dropped() int { return int(p.dropped.Load()) }

func (p *AckDropProxy) handle(cl net.Conn) {
	sv, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = cl.Close()
		return
	}
	var cut atomic.Bool
	done := make(chan struct{}, 2)
	go func() { // client to server: watch for the publish
		defer func() { done <- struct{}{} }()
		f := &framer{}
		buf := make([]byte, 64<<10)
		for {
			n, err := cl.Read(buf)
			if n > 0 {
				for _, fr := range f.feed(buf[:n]) {
					if (fr.verb == "PUB" || fr.verb == "HPUB") && fr.subject == p.subject && !cut.Swap(true) {
						p.dropped.Add(1)
					}
				}
				if _, werr := sv.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // server to client: forward until the publish, then swallow
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 64<<10)
		for {
			n, err := sv.Read(buf)
			if n > 0 && !cut.Load() {
				if _, werr := cl.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-done // either side ending ends the connection
	_ = cl.Close()
	_ = sv.Close()
	<-done
}
