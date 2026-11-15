package policyserve

import (
	"context"
	"fmt"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
)

// ConnScan is what every server's CONNZ said about the account's connections, and how much of the picture it is.
type ConnScan struct {
	Conns []apply.ConnzServer
	// Answered and Missing are the servers that answered the connection list and those that answered the ping only.
	Answered, Missing []string
	// Expected is how many servers there should be.
	Expected int
	// Incomplete says why the scan is not the whole picture ("" when it is): the server set is incomplete, a server did
	// not answer the connection list, or a list was not read whole.
	Incomplete string
}

// ScanConnections reads the connections of account from every server over nc (a system account connection). It is the
// gathering `issued check`, `policy reconcile` and the exempt-issuer source of an apply share. An error is a failure to
// ask at all; a partial answer is reported in Incomplete, never passed over.
func ScanConnections(ctx context.Context, nc *nats.Conn, account string, servers int) (ConnScan, error) {
	res := apply.NATS{Conn: nc, Inventory: servers}
	set, err := res.Servers(ctx)
	if err != nil {
		return ConnScan{}, err
	}
	conns, err := res.Connz(ctx, account, set.Names, 0)
	if err != nil {
		return ConnScan{}, err
	}
	sc := ConnScan{Conns: conns, Answered: []string{}, Missing: []string{}, Expected: max(set.Expected, servers, len(set.Names))}
	answered := map[string]bool{}
	var unreconciled []string
	for _, s := range conns {
		answered[s.Name] = true
		sc.Answered = append(sc.Answered, s.Name)
		if s.Incomplete != "" {
			unreconciled = append(unreconciled, s.Incomplete)
		}
	}
	for _, n := range set.Names {
		if !answered[n] {
			sc.Missing = append(sc.Missing, n)
		}
	}
	switch {
	case !set.Complete(servers):
		sc.Incomplete = set.Why(servers)
	case len(sc.Missing) > 0:
		sc.Incomplete = fmt.Sprintf("%d server(s) answered the ping but not the connection list: %s", len(sc.Missing), config.Clean(strings.Join(sc.Missing, ", ")))
	}
	if len(unreconciled) > 0 {
		why := strings.Join(unreconciled, "; ")
		if sc.Incomplete != "" {
			why = sc.Incomplete + "; " + why
		}
		sc.Incomplete = why
	}
	return sc, nil
}

// ExemptIssuerSource reads, on each call, the issuer of every connection of every connected user in exempt: the
// source an Issuer's ExemptIssuers needs. A scan that is not the whole picture is an error, never a partial answer:
// a connection on a server that did not answer could be under a key that is about to leave.
func ExemptIssuerSource(nc *nats.Conn, account string, servers int, exempt []string) func(context.Context) (map[string][]string, error) {
	return func(ctx context.Context) (map[string][]string, error) {
		scan, err := ScanConnections(ctx, nc, account, servers)
		if err != nil {
			return nil, err
		}
		if scan.Incomplete != "" {
			return nil, fmt.Errorf("the connection scan is incomplete: %s", scan.Incomplete)
		}
		isExempt := map[string]bool{}
		for _, k := range exempt {
			isExempt[k] = true
		}
		out := map[string][]string{}
		for _, s := range scan.Conns {
			for _, c := range s.Conns {
				if isExempt[c.Key] {
					out[c.Key] = append(out[c.Key], c.IssuerKey)
				}
			}
		}
		return out, nil
	}
}
