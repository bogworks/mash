// Command egressproxy is the allowlist egress proxy of the guide-run harness (scripts/guide-run).
//
// The guide-run container has no network. In published mode it reaches the release files on GitHub through this
// proxy only: an HTTP proxy that speaks CONNECT and nothing else, listening on a UNIX socket (mode 0600) that the
// harness bind-mounts into the container. It lets a tunnel through only to port 443 of exactly github.com or
// objects.githubusercontent.com. It resolves the name itself, refuses any answer that is not a public unicast
// address (so a name pointing at loopback, the LAN or the tailnet gets nothing), and dials the vetted address, not
// the name again, so a second lookup cannot change the answer. Every request is logged: time, host, allowed or
// refused, the reason, and bytes each way.
//
//	egressproxy -sock PATH -log PATH
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// allowedHosts is the whole allowlist: exact names, no wildcard.
// GitHub serves release assets from release-assets.githubusercontent.com (the redirect target seen 2026-11-20) and,
// formerly and for some assets still, objects.githubusercontent.com.
var allowedHosts = []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

// blocked are the ranges that are not public unicast but that the net/netip predicates do not all cover.
var blocked = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"100.64.0.0/10",   // CGNAT: the tailnet
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"240.0.0.0/4",     // reserved, and the broadcast address
	"fc00::/7",        // unique local addresses
	"64:ff9b::/96",    // NAT64: embeds an IPv4 address
	"100::/64",        // discard-only
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4: embeds an IPv4 address
)

func mustPrefixes(s ...string) []netip.Prefix {
	var ps []netip.Prefix
	for _, x := range s {
		ps = append(ps, netip.MustParsePrefix(x))
	}
	return ps
}

// vetAddr returns nil only for a public unicast address.
func vetAddr(a netip.Addr) error {
	if !a.IsValid() {
		return errors.New("not an address")
	}
	if a.Zone() != "" {
		return errors.New("address has a zone")
	}
	a = a.Unmap() // ::ffff:10.0.0.1 is 10.0.0.1
	switch {
	case a.IsUnspecified():
		return errors.New("unspecified address")
	case a.IsLoopback():
		return errors.New("loopback address")
	case a.IsPrivate():
		return errors.New("private address")
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(), a.IsInterfaceLocalMulticast():
		return errors.New("link-local address")
	case a.IsMulticast():
		return errors.New("multicast address")
	case !a.IsGlobalUnicast():
		return errors.New("not a global unicast address")
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return fmt.Errorf("address in %s", p)
		}
	}
	return nil
}

// checkTarget parses a CONNECT authority and says whether the tunnel may be asked for: port 443 of an allowed name.
func checkTarget(authority string) (host string, err error) {
	h, port, err := net.SplitHostPort(authority)
	if err != nil {
		return authority, errors.New("not host:port")
	}
	if port != "443" {
		return h, errors.New("port is not 443")
	}
	for _, a := range allowedHosts {
		if h == a {
			return h, nil
		}
	}
	return h, errors.New("host is not on the allowlist")
}

type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type proxy struct {
	res  resolver
	dial dialFunc
	mu   sync.Mutex
	log  io.Writer
}

func (p *proxy) logf(host, verdict, reason string, up, down int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.log, "%s host=%s %s reason=%q up=%d down=%d\n",
		time.Now().UTC().Format(time.RFC3339), host, verdict, reason, up, down)
}

// connect resolves host, vets every answer, and dials a vetted address. Any bad answer refuses the whole name.
func (p *proxy) connect(ctx context.Context, host string) (net.Conn, error) {
	addrs, err := p.res.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return nil, errors.New("lookup returned no address")
	}
	for _, a := range addrs {
		if err := vetAddr(a); err != nil {
			return nil, fmt.Errorf("%s resolves to %s: %w", host, a, err)
		}
	}
	var last error
	for _, a := range addrs {
		c, err := p.dial(ctx, "tcp", net.JoinHostPort(a.Unmap().String(), "443"))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, fmt.Errorf("dial failed: %w", last)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		p.logf(r.Method, "refused", "only CONNECT is supported", 0, 0)
		w.Header().Set("Allow", "CONNECT")
		http.Error(w, "only CONNECT is supported", http.StatusMethodNotAllowed)
		return
	}
	host, err := checkTarget(r.Host)
	if err != nil {
		p.logf(host, "refused", err.Error(), 0, 0)
		http.Error(w, "refused: "+err.Error(), http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	up, err := p.connect(ctx, host)
	cancel()
	if err != nil {
		p.logf(host, "refused", err.Error(), 0, 0)
		code := http.StatusForbidden
		if strings.HasPrefix(err.Error(), "lookup") || strings.HasPrefix(err.Error(), "dial") {
			code = http.StatusBadGateway
		}
		http.Error(w, "refused: "+err.Error(), code)
		return
	}
	defer up.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		p.logf(host, "refused", "cannot hijack", 0, 0)
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		p.logf(host, "refused", "hijack: "+err.Error(), 0, 0)
		return
	}
	defer c.Close()
	if _, err := c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		p.logf(host, "refused", "reply: "+err.Error(), 0, 0)
		return
	}
	var sent, got atomic.Int64
	done := make(chan struct{}, 2)
	go func() { // client to server; bytes the client already sent after the headers sit in brw
		n, _ := io.Copy(up, brw)
		sent.Store(n)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(c, up)
		got.Store(n)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	// one direction ended: give the other a moment, then close both
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.Close()
	up.Close()
	p.logf(host, "allowed", "tunnel closed", sent.Load(), got.Load())
}

func main() {
	sock := flag.String("sock", "", "UNIX socket to listen on (mode 0600)")
	logPath := flag.String("log", "", "log file (appended)")
	flag.Parse()
	if *sock == "" || *logPath == "" {
		fmt.Fprintln(os.Stderr, "usage: egressproxy -sock PATH -log PATH")
		os.Exit(2)
	}
	lf, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "egressproxy:", err)
		os.Exit(1)
	}
	defer lf.Close()
	// the socket is created under a 0177 umask, so it is never visible with a wider mode
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", *sock)
	syscall.Umask(old)
	if err != nil {
		fmt.Fprintln(os.Stderr, "egressproxy:", err)
		os.Exit(1)
	}
	if err := os.Chmod(*sock, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "egressproxy:", err)
		os.Exit(1)
	}
	p := &proxy{res: net.DefaultResolver, dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, log: lf}
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		srv.Close() // closes the listener, which unlinks the socket
	}()
	fmt.Fprintf(os.Stderr, "egressproxy: listening on %s, allowing %s\n", *sock, strings.Join(allowedHosts, ", "))
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "egressproxy:", err)
		os.Exit(1)
	}
}
