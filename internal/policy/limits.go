package policy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The limits a server puts on a credential, and the measure of what a user JWT costs on the wire.
const (
	// DefaultMaxControlLine is nats-server's default max_control_line: the most bytes of a CONNECT line the server
	// reads before it closes the connection.
	DefaultMaxControlLine = 4096
	// DefaultServer names the stand-in server of an offline measure, one that has no server to ask: `policy check`,
	// `policy show`, and a command with no system credentials. It is how the default limit is named in a message.
	DefaultServer = "default server limit"
)

// ServerLimits is what one server accepts of a credential.
type ServerLimits struct {
	// Server is the server's name as it reported it, or DefaultServer for the offline measure.
	Server string
	// MaxControlLine is the server's max_control_line: the most bytes of the CONNECT line a client may send it.
	MaxControlLine int
	// AccountJWT is the largest account JWT the server takes: the jwt decoder cap (AccountJWTLimit), lowered to the
	// server's max_payload when that is smaller, because the claims update travels as one message of that size.
	AccountJWT int
}

// DefaultServerLimits is the offline measure: one server with nats-server's defaults.
func DefaultServerLimits() []ServerLimits {
	return []ServerLimits{{Server: DefaultServer, MaxControlLine: DefaultMaxControlLine, AccountJWT: AccountJWTLimit}}
}

// The CONNECT line nats.go sends (conn.go connectProto): "CONNECT " + JSON + CRLF. nats-server limits the JSON
// (parser.go overMaxControlLineLimit counts the argument, not the verb or the line end), so ConnectLineBytes counts
// the JSON. The fields below are those of nats.go's connectInfo, in its order, for a user that authenticates with a
// JWT and the signature of the server's nonce.
const (
	// connectLang and connectVersion are what nats.go sends. The version is a placeholder as wide as the widest
	// version number the wire format is likely to see, so a newer client library does not make the measure low.
	connectLang    = "go"
	connectVersion = "000.000.000"
	// connectProtocol is nats.go's ClientProtocolInfo (1): one digit.
	connectProtocol = 1
	// signatureBytes is the length of the base64url (unpadded) ed25519 signature of the nonce: 64 bytes become 86.
	signatureBytes = 86
	// VersionSlack is how many bytes wider than the real nats.go version string the placeholder is, so how many bytes
	// ConnectLineBytes is high by, for the client library this module is built with ("1.54.0" is 6 characters), for a
	// client without TLS; a TLS client is one byte more (tls_required is spelled false in the measure). The
	// calibration test pins it.
	VersionSlack = len(connectVersion) - len("1.54.0")
)

// ConnectLineName is the client name a mash endpoint connects with (internal/endpoint: nats.Name("mash " + name)).
func ConnectLineName(endpoint string) string { return "mash " + endpoint }

// connectInfo mirrors nats.go's connectInfo, for a JWT user. tls_required is written as false, the longer of the two
// spellings: a TLS client is one byte shorter than the measure.
type connectInfo struct {
	Verbose      bool   `json:"verbose"`
	Pedantic     bool   `json:"pedantic"`
	UserJWT      string `json:"jwt,omitempty"`
	Signature    string `json:"sig,omitempty"`
	TLS          bool   `json:"tls_required"`
	Name         string `json:"name"`
	Lang         string `json:"lang"`
	Version      string `json:"version"`
	Protocol     int    `json:"protocol"`
	Echo         bool   `json:"echo"`
	Headers      bool   `json:"headers"`
	NoResponders bool   `json:"no_responders"`
}

// ConnectLineBytes is the size nats-server measures for the CONNECT line of a client that authenticates with
// userJWT and names itself clientName: the bytes of the JSON argument nats.go sends, worst case (tls_required spelled
// false, a signature of the full length, and a version number placeholder wider than the real one). It is never lower than the
// server's own count and, for the nats.go this module is built with, higher by exactly VersionSlack bytes (one more
// for a client that uses TLS): a measure that refuses a credential the server would take is wrong by at most that.
func ConnectLineBytes(userJWT, clientName string) int {
	b, err := json.Marshal(connectInfo{
		UserJWT: userJWT, Signature: strings.Repeat("A", signatureBytes), TLS: false, Name: clientName,
		Lang: connectLang, Version: connectVersion, Protocol: connectProtocol, Echo: true, Headers: true, NoResponders: true,
	})
	if err != nil { // a struct of strings, numbers and bools always marshals
		return len(userJWT) + len(clientName) + DefaultMaxControlLine
	}
	return len(b)
}

// withCommas writes n with thousands separators: 4096 as 4,096.
func withCommas(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Refusal is one credential that does not fit one server.
type Refusal struct {
	// Kind is "sender" or "account".
	Kind string
	// Name is the sender's name, or the account's.
	Name string
	// Zone is the sender's owner group ("" for an account).
	Zone string
	// Tags is the number of tags on the sender's user JWT (0 for an account), and Exceptions how many of them are
	// exceptions: an address on the sender's allow or deny list, counted once across verbs. Every sender also carries
	// the placeholder deny tag, so Tags is Exceptions + 1.
	Tags       int
	Exceptions int
	// Bytes is the measured size: the sender's connect line, or the account JWT.
	Bytes int
	// Limit is the server's limit for it, and Setting is the server setting that sets it.
	Limit   int
	Setting string
	// Server is the name of the server that would not take it.
	Server string
	// Note is more for the reader: for an account, which part of it is the biggest.
	Note string
}

// Error names the sender or account, its size, the limit and the server.
func (r Refusal) Error() string {
	if r.Kind == "account" {
		return fmt.Sprintf("account %q: its JWT would be %s bytes; server %q allows %s (%s); %s",
			r.Name, withCommas(r.Bytes), r.Server, withCommas(r.Limit), r.Setting, r.Note)
	}
	return fmt.Sprintf("sender %q (zone %q): its connect line would be %s bytes with %d exceptions (%d tags); server %q allows %s (%s)",
		r.Name, r.Zone, withCommas(r.Bytes), r.Exceptions, r.Tags, r.Server, withCommas(r.Limit), r.Setting)
}

// RefusalHint is the way out of a sender that does not fit.
const RefusalHint = "Use a shared verb instead of a long list of exceptions (docs/policy-file.md)."
