// Package policyserve is the production assembly of the list-set intake: the adapters the intake needs (decode,
// document in force, refusals, held keys, the write guard), the signer over the key store, the server limits read through
// the system account, and the Applier that builds everything policyapply.Apply needs for one verified document.
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
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/listset"
)

// The subject and waits of the server limits query.
const (
	varzSubject = "$SYS.REQ.SERVER.PING.VARZ"
	// varzFirst bounds the first server's answer; varzSettle ends the collection when no further server answered for
	// that long (skipped once every expected server has answered); varzTotal bounds the whole collection.
	varzFirst  = 2 * time.Second
	varzSettle = 500 * time.Millisecond
	varzTotal  = 4 * time.Second
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
	// URL is a client URL for the server, built from its own host and port.
	URL string
	// Limits are its size limits: max_control_line as the connect line limit, max_payload as the account JWT limit.
	Limits listset.ServerLimits
}

// ErrFewerServers is wrapped by ReadServers when fewer servers answered than are declared or known to exist.
var ErrFewerServers = errors.New("fewer servers answered than the deployment has")

// ReadServers asks every server for its limits and client address with a $SYS.REQ.SERVER.PING.VARZ request and
// returns what the answering servers reported, sorted by name. want is the number of servers that must answer (the
// declared --servers count, or the count the system account ping found, whichever is larger); fewer replies is an error
// wrapping ErrFewerServers, with the servers collected so far, because a server that is silent may have limits that make
// a list set unusable there: limits are never guessed. scheme and fallbackHost build a client URL for a server that
// reports a wildcard or no host.
func ReadServers(ctx context.Context, nc *nats.Conn, want int, scheme, fallbackHost string) ([]ServerInfo, error) {
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
	start := time.Now()
	wait := varzFirst
	replies := 0
	for ctx.Err() == nil && (want <= 0 || len(seen) < want) {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			left := varzTotal - time.Since(start)
			if len(seen) == 0 || left <= 0 || want <= 0 {
				break
			}
			wait = min(varzSettle, left)
			continue
		}
		wait = varzSettle
		if replies++; replies > maxVarzReplies {
			return nil, fmt.Errorf("more than %d replies to the server limits query: refusing", maxVarzReplies)
		}
		if len(msg.Data) > maxVarzBytes {
			return nil, fmt.Errorf("a reply to the server limits query is %d bytes, over %d: refusing", len(msg.Data), maxVarzBytes)
		}
		info, ok, err := parseVarz(msg.Data, scheme, fallbackHost)
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
	if len(out) < want || len(out) == 0 {
		return out, fmt.Errorf("%w: %d of %d servers reported their limits", ErrFewerServers, len(out), want)
	}
	return out, nil
}

// parseVarz reads one reply. ok is false for a reply that names no server or carries an error (such a server did not
// report its limits, so it does not count).
func parseVarz(data []byte, scheme, fallbackHost string) (info ServerInfo, ok bool, err error) {
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
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = fallbackHost
	}
	if scheme == "" {
		scheme = "nats"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(r.Data.Port))}
	return ServerInfo{
		Name: name, URL: u.String(),
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
