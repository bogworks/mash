// Package policyserve holds the production pieces of a policy apply: the signer over the key store, the server limits
// read through the system account, and the inbox and credential provisioning.
//
// Two steps of an apply depend on decisions that are not made yet, and they fail closed: a change that adds an address
// (the inbox and credential of a new receiver) and a change that narrows a sender's tags (the new credential has to
// reach the sender's host) are refused before anything is pushed, with the reason in the status. See Provisioner
// and FailClosedDeliverer.
package policyserve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/listset"
)

// The subject and waits of the server limits query.
const (
	varzSubject = "$SYS.REQ.SERVER.PING.VARZ"
	// varzTotal bounds the whole collection: it ends when every declared server has answered, or at this deadline.
	varzTotal = 4 * time.Second
	// maxVarzReplies is the most replies one query reads, maxVarzBytes the largest reply taken (a bigger one is
	// refused, never cut) and maxServerNameBytes the longest server name taken as an identity.
	maxVarzReplies     = 4096
	maxVarzBytes       = 1 << 20
	maxServerNameBytes = 4096
)

// ServerInfo is what one server reported about itself.
type ServerInfo struct {
	// Name is the server's name as the system account ping reports it.
	Name string
	// Host and Port are the client address the server reported; Host is empty when it reported none or a wildcard.
	Host string
	Port int
	// URL is a client URL for the server; ResolveURLs sets it.
	URL string
	// Limits are its size limits: max_control_line as the connect line limit, max_payload as the account JWT limit.
	Limits listset.ServerLimits
}

// ErrFewerServers is wrapped by ReadServers when fewer servers answered than are declared or known to exist.
var ErrFewerServers = errors.New("fewer servers answered than the deployment has")

// ReadServers asks every server for its limits and client address with a $SYS.REQ.SERVER.PING.VARZ request and
// returns what the answering servers reported, sorted by name. want is the declared number of servers (the servers in the
// configuration, 1 or more): the collection waits until that many have answered, or until the deadline. Fewer replies is an error wrapping
// ErrFewerServers, with the servers collected so far, because a server that is silent may have limits that make a list
// set unusable there: limits are never guessed, and a quiet interval never ends the wait early.
func ReadServers(ctx context.Context, nc *nats.Conn, want int) ([]ServerInfo, error) {
	if want < 1 {
		return nil, errors.New("the number of servers in the deployment is not declared (list every server under `servers` in the setup file and run mashctl init --config)")
	}
	inbox := nc.NewRespInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.PublishMsg(&nats.Msg{Subject: varzSubject, Reply: inbox, Data: []byte("{}")}); err != nil {
		return nil, err
	}
	if err := nc.Flush(); err != nil {
		return nil, err
	}
	seen := map[string]ServerInfo{}
	deadline := time.Now().Add(varzTotal)
	replies := 0
	for ctx.Err() == nil && len(seen) < want {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		msg, err := sub.NextMsg(left)
		if err != nil {
			break
		}
		if replies++; replies > maxVarzReplies {
			return nil, fmt.Errorf("more than %d replies to the server limits query: refusing", maxVarzReplies)
		}
		if len(msg.Data) > maxVarzBytes {
			return nil, fmt.Errorf("a reply to the server limits query is %d bytes, over %d: refusing", len(msg.Data), maxVarzBytes)
		}
		info, ok, err := parseVarz(msg.Data)
		if err != nil {
			return nil, err
		}
		if ok {
			seen[info.Name] = info
		}
	}
	out := make([]ServerInfo, 0, len(seen))
	for _, s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if len(out) < want {
		return out, fmt.Errorf("%w: %d of %d servers reported their limits", ErrFewerServers, len(out), want)
	}
	return out, nil
}

// wildcardHost reports whether a server's reported host names no address a client can dial.
func wildcardHost(h string) bool {
	switch h {
	case "", "0.0.0.0", "::", "[::]":
		return true
	}
	return false
}

// ResolveURLs sets the client URL of each server (as ReadServers returned them, sorted by name). configured is the
// configured server URL as the comma-separated list it may be, declared the declared server count. A server that
// reported a concrete host is reached there, with the scheme of the first configured URL. A server that reported a
// wildcard or no host takes the configured URL at its own position in the sorted server names, which is allowed only
// when the list has exactly declared entries and as many servers answered; otherwise it is refused, because no host is
// ever invented: the connection would go to the machine this service runs on and not to the deployment.
func ResolveURLs(servers []ServerInfo, configured string, declared int) ([]ServerInfo, error) {
	var entries []string
	for _, e := range strings.Split(configured, ",") {
		if e = strings.TrimSpace(e); e != "" {
			entries = append(entries, e)
		}
	}
	scheme := "nats"
	if len(entries) > 0 {
		if sch, _, ok := strings.Cut(entries[0], "://"); ok {
			scheme = strings.ToLower(sch)
		}
	}
	byOrder := len(entries) == declared && len(servers) == declared
	out := make([]ServerInfo, len(servers))
	for i, s := range servers {
		switch {
		case !wildcardHost(s.Host):
			u := url.URL{Scheme: scheme, Host: net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}
			s.URL = u.String()
		case byOrder:
			s.URL = entries[i]
			if !strings.Contains(s.URL, "://") {
				s.URL = "nats://" + s.URL
			}
		default:
			return nil, fmt.Errorf("server %.64s reports no address of its own (a wildcard host) and the configured URL list has %d entries for %d declared servers (%d answered): list one URL per server, in the order of the server names, or have each server advertise its host",
				s.Name, len(entries), declared, len(servers))
		}
		out[i] = s
	}
	return out, nil
}

// parseVarz reads one reply. ok is false for a reply that names no server or carries an error (such a server did not
// report its limits, so it does not count).
func parseVarz(data []byte) (info ServerInfo, ok bool, err error) {
	var r struct {
		Server struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"server"`
		Data *struct {
			Host           string `json:"host"`
			Port           int    `json:"port"`
			MaxPayload     int    `json:"max_payload"`
			MaxControlLine int    `json:"max_control_line"`
		} `json:"data"`
		Error *json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &r) != nil || r.Error != nil || r.Data == nil {
		return ServerInfo{}, false, nil
	}
	name := r.Server.Name
	if name == "" {
		name = r.Server.ID
	}
	if name == "" {
		return ServerInfo{}, false, nil
	}
	if len(name) > maxServerNameBytes {
		return ServerInfo{}, false, fmt.Errorf("a server name is over %d bytes: refusing", maxServerNameBytes)
	}
	if r.Data.MaxControlLine <= 0 || r.Data.MaxPayload <= 0 {
		return ServerInfo{}, false, nil // no limits reported: not proven
	}
	host := r.Data.Host
	if wildcardHost(host) {
		host = ""
	}
	return ServerInfo{
		Name: name, Host: host, Port: r.Data.Port,
		Limits: listset.ServerLimits{Server: name, MaxConnectLine: r.Data.MaxControlLine, MaxAccountJWT: r.Data.MaxPayload},
	}, true, nil
}

// Limits returns the servers' limits for listset.SizeCheck.
func Limits(servers []ServerInfo) []listset.ServerLimits {
	out := make([]listset.ServerLimits, 0, len(servers))
	for _, s := range servers {
		out = append(out, s.Limits)
	}
	return out
}

// URLs maps each server's name to its client URL.
func URLs(servers []ServerInfo) map[string]string {
	out := make(map[string]string, len(servers))
	for _, s := range servers {
		out[s.Name] = s.URL
	}
	return out
}
