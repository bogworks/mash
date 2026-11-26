// Package discovery is the verify step of a discovery answer (S5, REQ-016). It is a pure function of the registration
// and the directory's view: it holds no NATS connection and calls nothing. Like the rest of internal/sapsdk it is not
// linked by a 0.5.x command (scripts/releasecheck refuses it), so 0.5.x discovery is main's own; the switch-on links it.
package discovery

import "github.com/bogworks/mash/internal/sap"

// Registration is one prompt endpoint of a $SRV.INFO answer: its prompt subject and the metadata of the answer.
type Registration struct {
	Subject  string
	Metadata map[string]string
}

// Directory is what the directory says about the registration: the record, if any, of the address its prompt subject
// names.
type Directory struct {
	// HasRecord says whether a record exists for the subject's address.
	HasRecord bool
	// User, Owner and Account are the record's own: the user key it holds now, not a key it held before it was replaced.
	User    string
	Owner   string
	Account string
}

// Verify returns the address name the registration vouches for and whether it is verified. It is verified when
// the id_sig verifies over the user_nkey, account, agent, owner and prompt subject; a record exists for the subject's
// address and its verb is prompt; the user_nkey is the record's own User key; the record has an account and it is the registration's (a record with no account verifies nothing); and
// metadata.owner is the record's owner. An agent that is not in the directory has no record and is not verified here.
func Verify(reg Registration, d Directory) (name string, ok bool) {
	a, verb, found := sap.AddressFromSubject(reg.Subject)
	if !found {
		return "", false
	}
	name = a.Flat()
	if verb != "prompt" { // a signed status, heartbeat or mail subject is not a prompt endpoint
		return name, false
	}
	md := reg.Metadata
	if !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], reg.Subject, md["id_sig"]) {
		return name, false
	}
	if !d.HasRecord || d.User != md["user_nkey"] {
		return name, false
	}
	if d.Account == "" || md["account"] != d.Account {
		return name, false
	}
	if md["owner"] != d.Owner {
		return name, false
	}
	return name, true
}
