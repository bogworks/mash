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

// Directory is what the directory says about the registration: who holds its user_nkey, and the record, if any, of the
// address its prompt subject names.
type Directory struct {
	// Holder is the name of the endpoint that holds the registration's user_nkey, or empty.
	Holder string
	// HasRecord says whether a record exists for the subject's address.
	HasRecord bool
	// Owner and Account are the record's.
	Owner   string
	Account string
}

// Verify returns the address name the registration vouches for and whether it is verified. It is verified when
// the id_sig verifies over the user_nkey, account, agent, owner and prompt subject; a record exists for the subject's
// address; the user_nkey is held by that address; the account is the record's (when the record has one); and
// metadata.owner is the record's owner. An agent that is not in the directory has no record and is not verified here.
func Verify(reg Registration, d Directory) (name string, ok bool) {
	a, _, found := sap.AddressFromSubject(reg.Subject)
	if !found {
		return "", false
	}
	name = a.Flat()
	md := reg.Metadata
	if !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], reg.Subject, md["id_sig"]) {
		return name, false
	}
	if !d.HasRecord || d.Holder != name {
		return name, false
	}
	if d.Account != "" && md["account"] != d.Account {
		return name, false
	}
	if md["owner"] != d.Owner {
		return name, false
	}
	return name, true
}
