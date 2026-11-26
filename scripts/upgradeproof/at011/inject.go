package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/scripts/upgradeproof/inventory"
	"github.com/bogworks/mash/scripts/upgradeproof/shapes"
)

// serversOf is the server list of a configuration folder's config.json (a JSON array of strings, or one string with a
// comma list).
func serversOf(cfgDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if err != nil {
		return "", err
	}
	var c struct {
		Servers json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return "", err
	}
	var list []string
	if json.Unmarshal(c.Servers, &list) != nil {
		var one string
		if err := json.Unmarshal(c.Servers, &one); err != nil {
			return "", fmt.Errorf("config.json has no servers")
		}
		return one, nil
	}
	return strings.Join(list, ","), nil
}

// keysOfCreds is the account and user public keys in a NATS creds file's user JWT.
func keysOfCreds(path string) (account, user string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", "", err
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", "", err
	}
	account = uc.IssuerAccount
	if account == "" {
		account = uc.Issuer
	}
	return account, uc.Subject, nil
}

// inject stores the corpus in Alice's inbox as raw NATS messages published by the test identity, one per case, in order,
// and inventories each as expected-refused with its class. A stored message gets the next stream number, so the numbers
// are read from the stream's last number before and checked against the admin's listing after.
func (r *runner) inject(s *stepCtx, alice, mallory *persona) bool {
	creds := filepath.Join(mallory.cfg, mallory.addr+".creds")
	account, user, err := keysOfCreds(creds)
	if !s.need(err == nil, "read the test identity's keys", "%v", err) {
		return false
	}
	servers, err := serversOf(mallory.cfg)
	if !s.need(err == nil, "read the server list", "%v", err) {
		return false
	}
	var asyncMu sync.Mutex
	var asyncErr error
	lastErr := func() error { asyncMu.Lock(); defer asyncMu.Unlock(); return asyncErr }
	nc, err := nats.Connect(servers, nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
		asyncMu.Lock()
		asyncErr = e
		asyncMu.Unlock()
	}), nats.UserCredentials(creds), nats.RootCAs(filepath.Join(mallory.cfg, "ca.pem")),
		nats.Timeout(15*time.Second), nats.Name("at011 test identity"))
	if !s.need(err == nil, "connect as the test identity", "%v", err) {
		return false
	}
	defer nc.Close()
	rows, _, err := r.adminInbox(alice.addr)
	if !s.need(err == nil, "list Alice's inbox before injecting", "%v", err) {
		return false
	}
	start := maxSeq(rows)
	subj := "agents.mail." + alice.addr
	cases := shapes.Corpus(account, user, subj)
	for _, c := range cases {
		m := nats.NewMsg(subj)
		for _, v := range c.Values {
			m.Header.Add(sap.HeaderName, v)
		}
		m.Data = []byte(c.Body())
		err := nc.PublishMsg(m)
		if err == nil {
			err = nc.FlushTimeout(15 * time.Second)
		}
		if !s.need(err == nil, "store "+c.Name, "%v", err) {
			return false
		}
		if e := lastErr(); e != nil {
			s.need(false, "the server accepted "+c.Name, "%v", e)
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	after, _, err := r.adminInbox(alice.addr)
	if !s.need(err == nil, "list Alice's inbox after injecting", "%v", err) {
		return false
	}
	have := map[uint64]bool{}
	for _, row := range after {
		have[row.Seq] = true
	}
	for i, c := range cases {
		seq := start + uint64(i) + 1
		if !s.need(have[seq], "stored "+c.Name+" at number "+fmt.Sprint(seq), "") {
			return false
		}
		r.addEntry(alice.addr, inventory.Entry{ID: inventory.ID{Seq: seq, Sender: mallory.addr, Nonce: c.Token},
			Phase: "step2", Label: c.Name, Expect: inventory.Refused, Reason: c.Class})
	}
	s.need(maxSeq(after) == start+uint64(len(cases)), "nothing else was stored meanwhile", "last number %d, expected %d", maxSeq(after), start+uint64(len(cases)))
	return true
}
