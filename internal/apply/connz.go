package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
)

// The system account's CONNZ requests: the ping reaches every server, the direct subject one server by its id.
const (
	connzPingSubject   = "$SYS.REQ.SERVER.PING.CONNZ"
	connzDirectSubject = "$SYS.REQ.SERVER.%s.CONNZ"

	// DefaultConnzPage is how many connections one CONNZ reply asks for. nats-server's own default is 1024, but it
	// honours a larger limit (up to the number of connections it has), so a typical scan is one page: one snapshot, no
	// offset to drift. The reply carries each connection's JWT (auth on), so the page is not made unbounded.
	DefaultConnzPage = 8192

	// connzScanTries is how many times one server's whole scan is made when its pages do not reconcile or two multi-page
	// scans do not agree (a connection closed or opened between two pages); after that the server is marked incomplete.
	connzScanTries = 3

	// connzTotal bounds the wait for the servers a ping should reach; connzSettle ends the wait when no set of
	// expected servers is known; connzPageTimeout bounds one follow-up page request.
	connzTotal       = 3 * time.Second
	connzSettle      = 500 * time.Millisecond
	connzPageTimeout = 5 * time.Second

	// maxConnzConns is the most connections Connz collects from one server. A server is not trusted to be small.
	maxConnzConns = 1 << 20
)

// ConnzConn is one connection of one server as CONNZ reports it with auth on. Key is the connected user's public key
// (CONNZ's authorized_user: for a JWT user the JWT's subject). The user's JWT is never read.
type ConnzConn struct {
	Key       string   `json:"public_key"`
	Account   string   `json:"account"`
	Name      string   `json:"name,omitempty"`       // the client's connection name, as it sent it
	IssuerKey string   `json:"issuer_key,omitempty"` // the key that signed the user's JWT
	Tags      []string `json:"tags,omitempty"`
	CID       uint64   `json:"cid"`
}

// ConnzServer is what one server said: its name and id and every connection it reported for the account.
type ConnzServer struct {
	Name  string
	ID    string
	Conns []ConnzConn
	// Total is the server's own count of matching connections; len(Conns) equals it when every page was read.
	Total int
	// Incomplete is non-empty when, after connzScanTries scans, the pages did not reconcile with the server's count or
	// (for a list of several pages) two consecutive scans did not collect the same connections: the server's
	// connections kept changing between pages. Conns then holds what the last
	// scan read, each connection once, and the list is NOT the whole list: a caller must treat the server like one
	// that did not answer.
	Incomplete string
}

type connzReply struct {
	Server struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"server"`
	Data *struct {
		Total  int `json:"total"`
		Offset int `json:"offset"`
		Conns  []struct {
			User      string   `json:"authorized_user"`
			Account   string   `json:"account"`
			Name      string   `json:"name"`
			IssuerKey string   `json:"issuer_key"`
			Tags      []string `json:"tags"`
			CID       uint64   `json:"cid"`
		} `json:"connections"`
	} `json:"data"`
	Error *struct {
		Code        int    `json:"code"`
		Description string `json:"description"`
	} `json:"error"`
}

func connzOptions(account string, offset, limit int) []byte {
	b, _ := json.Marshal(map[string]any{"auth": true, "acc": account, "offset": offset, "limit": limit})
	return b
}

func (r connzReply) name() string {
	if r.Server.Name != "" {
		return r.Server.Name
	}
	return r.Server.ID
}

// Connz asks every server for its open connections of account and returns one entry per server that answered, sorted
// by name. It waits for the servers named in expect (the ping's set) and stops when all have answered or connzTotal has
// passed; a server that never answers is simply absent, and the caller compares the names. A server that answers with an
// error, or whose answer cannot be read, is an error (the check must not pass over a server it could not read). Each
// server's further pages (a reply carries at most page connections, DefaultConnzPage when page <= 0) are asked for on
// that server's own subject, so a server with more connections than one page is read whole. The connections' JWTs, which
// CONNZ returns with auth on, are not kept.
func (n NATS) Connz(ctx context.Context, account string, expect []string, page int) ([]ConnzServer, error) {
	if page <= 0 {
		page = DefaultConnzPage
	}
	inbox := n.Conn.NewRespInbox()
	sub, err := n.Conn.SubscribeSync(inbox)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	if err := n.Conn.PublishMsg(&nats.Msg{Subject: connzPingSubject, Reply: inbox, Data: connzOptions(account, 0, page)}); err != nil {
		return nil, err
	}
	if err := n.Conn.Flush(); err != nil {
		return nil, err
	}
	var out []ConnzServer
	seen := map[string]bool{}
	start := time.Now()
	wait := connzTotal
	for ctx.Err() == nil {
		msg, err := sub.NextMsg(wait)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			return out, err
		}
		var r connzReply
		if json.Unmarshal(msg.Data, &r) != nil {
			return out, errors.New("a server's CONNZ answer could not be read")
		}
		name := r.name()
		if name == "" || len(name) > maxNameBytes || (r.Server.ID != "" && len(r.Server.ID) > maxNameBytes) {
			return out, errors.New("a server's CONNZ answer names no usable server")
		}
		if r.Error != nil {
			return out, fmt.Errorf("server %s refused CONNZ: %s", config.Clean(name), config.Clean(r.Error.Description))
		}
		if r.Data == nil {
			return out, fmt.Errorf("server %s answered CONNZ with no data", config.Clean(name))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if len(seen) > maxServers {
			return out, fmt.Errorf("%w: more than %d servers answered CONNZ", ErrServerSetRefused, maxServers)
		}
		cs := ConnzServer{Name: name, ID: r.Server.ID}
		if err := n.connzScan(ctx, &cs, account, page, r); err != nil {
			return out, err
		}
		out = append(out, cs)
		if len(expect) > 0 {
			if allSeen(expect, seen) {
				break
			}
			wait = max(connzTotal-time.Since(start), time.Millisecond)
		} else {
			wait = connzSettle
		}
	}
	slices.SortFunc(out, func(a, b ConnzServer) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
	return out, ctx.Err()
}

func allSeen(expect []string, seen map[string]bool) bool {
	for _, e := range expect {
		if !seen[e] {
			return false
		}
	}
	return true
}

// connzScan reads one server's connections whole. The first reply (the ping's) is the first attempt's first page; the
// further pages, and the first page of a retry, are asked for on the server's own subject. A scan of one page is one
// consistent snapshot and is accepted when its count matches the server's total. A scan of several pages is not: every
// page is a fresh snapshot, so a connection that closes between two pages and another that opens can shift the list
// while the total stays the same, leaving the collected ids unique, as many as the total, and one live connection
// missing. A multi-page scan is therefore accepted only when it reconciles (as many unique ids as the total on its first
// and on its last page) AND the scan before it, also reconciled, collected exactly the same set of connection ids. A
// scan that does not agree is made again, up to connzScanTries scans in all, and then the server is marked Incomplete.
func (n NATS) connzScan(ctx context.Context, cs *ConnzServer, account string, page int, first connzReply) error {
	r := first
	var prev map[uint64]bool // the set of the last multi-page scan that reconciled
	for attempt := 1; ; attempt++ {
		ok, multi, err := n.connzScanOnce(ctx, cs, account, page, r)
		if err != nil {
			return err
		}
		if ok {
			if !multi {
				return nil
			}
			cur := make(map[uint64]bool, len(cs.Conns))
			for _, c := range cs.Conns {
				cur[c.CID] = true
			}
			if prev != nil && maps.Equal(prev, cur) {
				return nil
			}
			prev = cur
		} else {
			prev = nil
		}
		if attempt == connzScanTries {
			cs.Incomplete = fmt.Sprintf("server %s's connection list changed while it was read (%d scans did not agree with each other or with its own count of %d): the list is not complete", config.Clean(cs.Name), connzScanTries, cs.Total)
			return nil
		}
		if r, err = n.connzFetch(ctx, cs, account, 0, page); err != nil {
			return err
		}
	}
}

func (n NATS) connzFetch(ctx context.Context, cs *ConnzServer, account string, offset, page int) (connzReply, error) {
	var r connzReply
	if cs.ID == "" {
		return r, fmt.Errorf("server %s has more connections than one CONNZ page and gave no id to ask again", config.Clean(cs.Name))
	}
	rctx, cancel := context.WithTimeout(ctx, connzPageTimeout)
	defer cancel()
	msg, err := n.Conn.RequestWithContext(rctx, fmt.Sprintf(connzDirectSubject, cs.ID), connzOptions(account, offset, page))
	if err != nil {
		return r, fmt.Errorf("server %s: the next CONNZ page: %w", config.Clean(cs.Name), err)
	}
	if json.Unmarshal(msg.Data, &r) != nil || r.Data == nil || r.Error != nil {
		return r, fmt.Errorf("server %s: the next CONNZ page could not be read", config.Clean(cs.Name))
	}
	return r, nil
}

// connzScanOnce is one scan from the first page r. It reports whether the pages reconciled with the server's counts and
// whether the scan took more than one page.
func (n NATS) connzScanOnce(ctx context.Context, cs *ConnzServer, account string, page int, r connzReply) (ok, multi bool, err error) {
	cs.Total, cs.Conns = r.Data.Total, nil
	if cs.Total > maxConnzConns {
		return false, false, fmt.Errorf("server %s reports %d connections: refusing to read more than %d", config.Clean(cs.Name), cs.Total, maxConnzConns)
	}
	seen := map[uint64]bool{}
	dup := false
	add := func(r connzReply) {
		for _, c := range r.Data.Conns {
			if seen[c.CID] {
				dup = true
				continue
			}
			seen[c.CID] = true
			cs.Conns = append(cs.Conns, ConnzConn{Key: c.User, Account: c.Account, Name: c.Name, IssuerKey: c.IssuerKey, Tags: c.Tags, CID: c.CID})
		}
	}
	add(r)
	received, last := len(r.Data.Conns), r.Data.Total
	for received < cs.Total && !dup {
		next, err := n.connzFetch(ctx, cs, account, received, page)
		if err != nil {
			return false, multi, err
		}
		multi = true
		if len(next.Data.Conns) == 0 {
			break // the server's list shrank under the scan
		}
		received += len(next.Data.Conns)
		last = next.Data.Total
		add(next)
	}
	return !dup && len(cs.Conns) == cs.Total && last == cs.Total, multi, nil
}
