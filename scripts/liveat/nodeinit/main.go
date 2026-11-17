// Command nodeinit is the only helper in an AT-005 node image. The image is FROM scratch: no shell, no sleep, no curl.
// `serve` keeps the container up until SIGTERM; the others are the file and network operations the harness needs
// inside a node (each acts on exactly what it is given, nothing is matched by pattern).
//
//	nodeinit serve
//	nodeinit mkdir DIR           make DIR (and parents), mode 0700
//	nodeinit chmod OCTAL PATH
//	nodeinit rm PATH             remove one file or one empty directory
//	nodeinit stat PATH           exit 0 if it exists
//	nodeinit dial HOST:PORT      exit 0 if a TCP connection is accepted
//	nodeinit get URL             print the body of an HTTP GET
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(a []string) int {
	need := func(n int) bool {
		if len(a) != n+1 {
			fmt.Fprintln(os.Stderr, "usage: nodeinit serve|mkdir DIR|chmod MODE PATH|rm PATH|stat PATH|dial HOST:PORT|get URL")
			return false
		}
		return true
	}
	var err error
	switch {
	case len(a) == 0:
		need(1)
		return 2
	case a[0] == "serve":
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
		<-c
	case a[0] == "mkdir" && need(1):
		err = os.MkdirAll(a[1], 0o700)
	case a[0] == "chmod" && need(2):
		var m uint64
		if m, err = strconv.ParseUint(a[1], 8, 32); err == nil {
			err = os.Chmod(a[2], os.FileMode(m))
		}
	case a[0] == "rm" && need(1):
		err = os.Remove(a[1])
	case a[0] == "stat" && need(1):
		_, err = os.Stat(a[1])
	case a[0] == "dial" && need(1):
		var c net.Conn
		if c, err = net.DialTimeout("tcp", a[1], 2*time.Second); err == nil {
			c.Close()
		}
	case a[0] == "get" && need(1):
		var resp *http.Response
		if resp, err = (&http.Client{Timeout: 3 * time.Second}).Get(a[1]); err == nil {
			defer resp.Body.Close()
			_, err = io.Copy(os.Stdout, resp.Body)
		}
	default:
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nodeinit:", err)
		return 1
	}
	return 0
}
