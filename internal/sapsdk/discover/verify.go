package discover

import (
	"encoding/json"

	"github.com/bogworks/mash/internal/sap"
)

// Record is what the directory holds for one address.
type Record struct {
	Address sap.Address
	User    string // user_nkey
	Account string
}

// Directory maps the flat key of every endpoint of the mesh to its record.
type Directory map[string]Record

// Registration is one registration accepted as the target's.
type Registration struct {
	Address       sap.Address
	PromptSubject string
	UserNKey      string
}

// Verdict is what one $SRV.INFO answer comes to.
type Verdict int

const (
	// NotARegistration is an answer that does not parse as a registration, or carries no prompt endpoint with a SAP
	// address subject. It is not counted: it vouches for nothing.
	NotARegistration Verdict = iota
	// Verified is a registration that passed every check (id_sig, and the directory record agrees).
	Verified
	// NotVerified is a registration that failed a check. It is counted (TO-12) and never attributed.
	NotVerified
)

// Judge is the verification of one answer, as a pure function. The address comes from the prompt subject alone
// (sap.AddressFromSubject). The registration is verified only when its id_sig verifies over (user_nkey, account, agent,
// owner, prompt subject) and the directory's record for that address agrees on all of: the address, the user_nkey,
// the account and the owner. An address the directory does not hold fails. The rules are those of the endpoint's
// presence check plus the owner and account comparisons REQ-016 adds. It fails closed: an answer with several prompt
// endpoints is verified only if every one is, and then yields them all.
func Judge(dir Directory, data []byte) (Verdict, []Registration) {
	var info struct {
		Metadata  map[string]string `json:"metadata"`
		Endpoints []struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
		} `json:"endpoints"`
	}
	if json.Unmarshal(data, &info) != nil {
		return NotARegistration, nil
	}
	md := info.Metadata
	var regs []Registration
	failed := false
	for _, ep := range info.Endpoints {
		if ep.Name != "prompt" {
			continue
		}
		a, _, ok := sap.AddressFromSubject(ep.Subject)
		if !ok {
			continue
		}
		rec, held := dir[a.Flat()]
		if !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], ep.Subject, md["id_sig"]) ||
			!held || rec.Address != a || rec.User != md["user_nkey"] || rec.Account != md["account"] || rec.Address.Owner != md["owner"] {
			failed = true
			continue
		}
		regs = append(regs, Registration{Address: a, PromptSubject: ep.Subject, UserNKey: md["user_nkey"]})
	}
	switch {
	case failed:
		return NotVerified, nil
	case len(regs) > 0:
		return Verified, regs
	}
	return NotARegistration, nil
}

// Collector gathers the answers of one discovery for one target. The order of the answers does not matter: an invalid
// answer is counted and set aside whether it comes before or after a valid one, and never ends or fails the discovery
// (SEC-12).
type Collector struct {
	dir    Directory
	target sap.Address

	// Accepted is every distinct verified registration of the target.
	Accepted []Registration
	// NotVerified is the number of answers rejected as not verified in this discovery, each counted once.
	NotVerified int
}

// NewCollector starts a discovery of target against dir.
func NewCollector(dir Directory, target sap.Address) *Collector {
	return &Collector{dir: dir, target: target}
}

// Add takes one answer and reports whether it was accepted as the target's registration.
func (c *Collector) Add(data []byte) (accepted bool) {
	v, regs := Judge(c.dir, data)
	switch v {
	case NotVerified:
		c.NotVerified++
	case Verified:
		for _, r := range regs {
			if r.Address != c.target {
				continue // a valid registration of another address: neither accepted nor counted
			}
			dup := false
			for _, have := range c.Accepted {
				dup = dup || have == r
			}
			if !dup {
				c.Accepted = append(c.Accepted, r)
			}
			accepted = true
		}
	}
	return accepted
}
