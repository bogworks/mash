package policy

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// calibrationServer is an in-process operator-mode nats-server with the given max_control_line and one account.
type calibrationServer struct {
	url  string
	acct nkeys.KeyPair
	pub  string
}

func startCalibrationServer(t *testing.T, maxControlLine int) *calibrationServer {
	t.Helper()
	opKP, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	opPub, _ := opKP.PublicKey()
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "calibration"
	opJWT, err := oc.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	op, err := jwt.DecodeOperatorClaims(opJWT)
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := nkeys.CreateAccount()
	pub, _ := acct.PublicKey()
	ac := jwt.NewAccountClaims(pub)
	ac.Name = "CAL"
	acctJWT, err := ac.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &server.MemAccResolver{}
	if err := resolver.Store(pub, acctJWT); err != nil {
		t.Fatal(err)
	}
	s, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true, MaxControlLine: int32(maxControlLine),
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	return &calibrationServer{url: s.ClientURL(), acct: acct, pub: pub}
}

// connectWith signs a user JWT that carries n tags and connects as the client name, returning whether the server
// accepted the CONNECT and the JWT (for the measure).
func (cs *calibrationServer) connectWith(t *testing.T, clientName string, n int) (bool, string) {
	t.Helper()
	uk, _ := nkeys.CreateUser()
	upub, _ := uk.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = "calibration-user"
	uc.IssuerAccount = cs.pub
	for i := 0; i < n; i++ {
		uc.Tags.Add(fmt.Sprintf("allow:zone.endpoint-%04d", i))
	}
	tok, err := uc.Encode(cs.acct)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(cs.url, nats.Name(clientName), nats.MaxReconnects(0), nats.Timeout(5*time.Second),
		nats.UserJWT(func() (string, error) { return tok, nil }, func(nonce []byte) ([]byte, error) { return uk.Sign(nonce) }))
	if err != nil {
		return false, tok
	}
	nc.Close()
	return true, tok
}

// TestConnectLineBytesCalibration finds, against a real nats-server, the largest tag count that connects at several
// max_control_line settings, and checks the measure against it. The server counts the JSON argument of CONNECT, and
// ConnectLineBytes is that JSON with a version placeholder VersionSlack bytes wider than nats.go's own, so the
// measure is high by exactly VersionSlack bytes: a credential the measure passes always connects, and one it refuses
// is over the limit by no more than VersionSlack bytes of the server's own count.
func TestConnectLineBytesCalibration(t *testing.T) {
	const clientName = "mash calibration-endpoint"
	for _, mcl := range []int{1024, 2048, DefaultMaxControlLine} {
		cs := startCalibrationServer(t, mcl)
		lo, hi := 0, 400
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if ok, _ := cs.connectWith(t, clientName, mid); ok {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		okConn, okJWT := cs.connectWith(t, clientName, lo)
		badConn, badJWT := cs.connectWith(t, clientName, lo+1)
		if !okConn || badConn {
			t.Fatalf("max_control_line %d: the search is not stable (%d connects: %v, %d connects: %v)", mcl, lo, okConn, lo+1, badConn)
		}
		okLine, badLine := ConnectLineBytes(okJWT, clientName), ConnectLineBytes(badJWT, clientName)
		t.Logf("max_control_line %d: %d tags connect (measure %d), %d tags do not (measure %d)", mcl, lo, okLine, lo+1, badLine)
		// The measure never passes what the server refuses ...
		if badLine <= mcl {
			t.Errorf("max_control_line %d: %d tags are refused by the server but the measure %d fits", mcl, lo+1, badLine)
		}
		// ... and is high by exactly VersionSlack: the server's own count is the measure less the slack.
		if got := okLine - VersionSlack; got > mcl {
			t.Errorf("max_control_line %d: %d tags connect but the measure %d less the slack %d is over the limit", mcl, lo, okLine, VersionSlack)
		}
		if got := badLine - VersionSlack; got <= mcl {
			t.Errorf("max_control_line %d: %d tags are refused but the measure %d less the slack %d fits", mcl, lo+1, badLine, VersionSlack)
		}
		// Over by at most VersionSlack: a measure that refuses a set the server would take is within the slack.
		if okLine > mcl+VersionSlack {
			t.Errorf("max_control_line %d: the measure %d of a connecting JWT is more than %d over the limit", mcl, okLine, VersionSlack)
		}
	}
}

// capturedConnect is the CONNECT argument a client wrote and the JWT it carried.
type capturedConnect struct {
	arg string
	jwt string
}

// captureConnect connects the real nats.go client, authenticating with a JWT, to a throwaway TCP listener that sends
// an INFO with a nonce and reads the CONNECT line the client answers with.
func captureConnect(t *testing.T, clientName string) capturedConnect {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- ""
			return
		}
		defer c.Close()
		fmt.Fprintf(c, "INFO {\"server_id\":\"x\",\"version\":\"2.10.0\",\"proto\":1,\"headers\":true,\"max_payload\":1048576,\"auth_required\":true,\"nonce\":\"abcdefgh\"}\r\n")
		line, _ := bufio.NewReader(c).ReadString('\n')
		got <- line
		fmt.Fprint(c, "-ERR 'Authorization Violation'\r\n")
	}()
	acct, _ := nkeys.CreateAccount()
	uk, _ := nkeys.CreateUser()
	upub, _ := uk.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = "capture-user"
	for i := 0; i < 20; i++ {
		uc.Tags.Add(fmt.Sprintf("allow:zone.endpoint-%04d", i))
	}
	tok, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect("nats://"+ln.Addr().String(), nats.Name(clientName), nats.MaxReconnects(0), nats.Timeout(2*time.Second),
		nats.UserJWT(func() (string, error) { return tok, nil }, func(nonce []byte) ([]byte, error) { return uk.Sign(nonce) }))
	if err == nil {
		nc.Close()
	}
	line := <-got
	arg, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r\n"), "CONNECT ")
	if !ok {
		t.Fatalf("no CONNECT line captured: %q", line)
	}
	return capturedConnect{arg: arg, jwt: tok}
}

// TestConnectLineBytesIsTheJSONNatsGoSends captures the CONNECT line a real client writes and checks the measure
// against it: same fields in the same order, the version differing only by the placeholder's width.
func TestConnectLineBytesIsTheJSONNatsGoSends(t *testing.T) {
	const name = "mash capture-endpoint"
	cap := captureConnect(t, name)
	got, want := ConnectLineBytes(cap.jwt, name), len(cap.arg)
	if got-want != VersionSlack {
		t.Fatalf("measure %d, wire %d: differ by %d, want exactly the version slack %d\nwire: %.300s", got, want, got-want, VersionSlack, cap.arg)
	}
	if !strings.Contains(cap.arg, `"version":"`+nats.Version+`"`) {
		t.Fatalf("the wire carries another version than nats.Version %s: %.300s", nats.Version, cap.arg)
	}
}
