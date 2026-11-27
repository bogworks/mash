package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVetAddr(t *testing.T) {
	refuse := []string{
		"127.0.0.1", "127.1.2.3", "::1", "0.0.0.0", "::",
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "fe80::1",
		"100.64.0.1", "100.100.100.100", "100.127.255.255", // CGNAT, the tailnet
		"fc00::1", "fd12:3456::1", // ULA
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff05::2", // multicast
		"255.255.255.255", "240.0.0.1", "0.1.2.3",
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::ffff:100.64.0.1", // v4-mapped private addresses
		"64:ff9b::a00:1", "2002:a00:1::1", "2001:db8::1",
		"192.0.2.1", "198.18.0.1",
	}
	for _, s := range refuse {
		if err := vetAddr(netip.MustParseAddr(s)); err == nil {
			t.Errorf("%s was accepted", s)
		}
	}
	if err := vetAddr(netip.MustParseAddr("fe80::1%eth0")); err == nil {
		t.Error("an address with a zone was accepted")
	}
	if err := vetAddr(netip.Addr{}); err == nil {
		t.Error("the zero address was accepted")
	}
	accept := []string{"140.82.112.3", "185.199.108.133", "8.8.8.8", "100.63.255.255", "100.128.0.1",
		"172.15.255.255", "172.32.0.1", "2606:50c0:8000::154", "::ffff:140.82.112.3"}
	for _, s := range accept {
		if err := vetAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s was refused: %v", s, err)
		}
	}
}

func TestCheckTarget(t *testing.T) {
	ok := []string{"github.com:443", "objects.githubusercontent.com:443", "release-assets.githubusercontent.com:443"}
	for _, s := range ok {
		if _, err := checkTarget(s); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	no := []string{
		"example.com:443", "github.com:80", "github.com", "github.com:8443", "github.com.:443",
		"GitHub.com:443", "api.github.com:443", "evilgithub.com:443", "github.com.evil.example:443",
		"raw.githubusercontent.com:443", "githubusercontent.com:443", "x.release-assets.githubusercontent.com:443", "127.0.0.1:443", "[::1]:443",
		"10.0.0.1:443", "github.com:443@evil.example:443", ":443", "",
	}
	for _, s := range no {
		if _, err := checkTarget(s); err == nil {
			t.Errorf("%q was allowed", s)
		}
	}
}

type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range f[host] {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// start runs the proxy on a unix socket in a temp folder; dialed records what the proxy dialed.
func start(t *testing.T, res resolver, dialed *[]string, upstream func() net.Conn) (sock string, logbuf *syncBuf) {
	t.Helper()
	logbuf = &syncBuf{}
	p := &proxy{res: res, log: logbuf, dial: func(_ context.Context, _, addr string) (net.Conn, error) {
		*dialed = append(*dialed, addr)
		if upstream == nil {
			return nil, io.ErrClosedPipe
		}
		return upstream(), nil
	}}
	// a UNIX socket path may not pass about 104 bytes on macOS, and t.TempDir() on
	// a runner is longer: use a short folder of our own, removed by its recorded path
	sdir, err := os.MkdirTemp("/tmp", "ep")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sdir) })
	sock = filepath.Join(sdir, "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: p}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock, logbuf
}

func connectVia(t *testing.T, sock, authority string) (*bufio.Reader, net.Conn, string) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "CONNECT "+authority+" HTTP/1.1\r\nHost: "+authority+"\r\n\r\n")
	br := bufio.NewReader(c)
	status, _ := br.ReadString('\n')
	return br, c, strings.TrimSpace(status)
}

func TestRefusedNeverDials(t *testing.T) {
	cases := []struct {
		name, authority string
		res             fakeResolver
		want            string
	}{
		{"not on the list", "example.com:443", fakeResolver{"example.com": {"93.184.216.34"}}, "403"},
		{"wrong port", "github.com:80", fakeResolver{"github.com": {"140.82.112.3"}}, "403"},
		{"allowed name, loopback answer", "github.com:443", fakeResolver{"github.com": {"127.0.0.1"}}, "403"},
		{"allowed name, private answer", "github.com:443", fakeResolver{"github.com": {"192.168.0.9"}}, "403"},
		{"allowed name, tailnet answer", "github.com:443", fakeResolver{"github.com": {"100.100.1.1"}}, "403"},
		{"rebinding: a public answer then a private one", "github.com:443",
			fakeResolver{"github.com": {"140.82.112.3", "10.1.2.3"}}, "403"},
		{"rebinding: v4-mapped private answer", "objects.githubusercontent.com:443",
			fakeResolver{"objects.githubusercontent.com": {"::ffff:172.16.0.1"}}, "403"},
		{"no answer", "github.com:443", fakeResolver{}, "502"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dialed []string
			sock, logbuf := start(t, c.res, &dialed, nil)
			_, conn, status := connectVia(t, sock, c.authority)
			conn.Close()
			if !strings.Contains(status, c.want) {
				t.Errorf("status %q, want %s", status, c.want)
			}
			if len(dialed) != 0 {
				t.Errorf("dialed %v", dialed)
			}
			if !strings.Contains(logbuf.String(), "refused") {
				t.Errorf("log lacks refused: %q", logbuf.String())
			}
			if h, _, _ := net.SplitHostPort(c.authority); !strings.Contains(logbuf.String(), " host="+h+" refused ") {
				t.Errorf("log lacks host=%s refused: %q", h, logbuf.String())
			}
		})
	}
}

func TestOtherMethodsRefused(t *testing.T) {
	var dialed []string
	sock, logbuf := start(t, fakeResolver{"github.com": {"140.82.112.3"}}, &dialed, nil)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "GET http://github.com/ HTTP/1.1\r\nHost: github.com\r\n\r\n")
	status, _ := bufio.NewReader(c).ReadString('\n')
	if !strings.Contains(status, "405") || len(dialed) != 0 || !strings.Contains(logbuf.String(), "refused") {
		t.Errorf("status %q dialed %v log %q", status, dialed, logbuf.String())
	}
}

// The allowed path dials the vetted address, never the name, and carries bytes both ways.
func TestAllowedDialsTheVettedAddress(t *testing.T) {
	var dialed []string
	far, near := net.Pipe()
	go func() { // the "server": echo upper-cased bytes once, then close
		buf := make([]byte, 5)
		io.ReadFull(near, buf)
		near.Write(bytes.ToUpper(buf))
		near.Close()
	}()
	sock, logbuf := start(t, fakeResolver{"objects.githubusercontent.com": {"185.199.108.133"}}, &dialed,
		func() net.Conn { return far })
	br, conn, status := connectVia(t, sock, "objects.githubusercontent.com:443")
	defer conn.Close()
	if !strings.Contains(status, "200") {
		t.Fatalf("status %q", status)
	}
	br.ReadString('\n') // the blank line ending the reply
	io.WriteString(conn, "hello")
	got := make([]byte, 5)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "HELLO" {
		t.Fatalf("got %q err %v", got, err)
	}
	if len(dialed) != 1 || dialed[0] != "185.199.108.133:443" {
		t.Errorf("dialed %v, want the vetted address and port, not the name", dialed)
	}
	conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logbuf.String(), "allowed") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logbuf.String(), "host=objects.githubusercontent.com allowed") {
		t.Errorf("log %q", logbuf.String())
	}
}
