package discovery

import (
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
)

const subject = "agents.prompt.acme.ops.bob"

func identity(t *testing.T, account string) sap.Identity {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	id, err := sap.NewIdentity(seed, account)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// reg is a registration signed by id for owner and subject.
func reg(t *testing.T, id sap.Identity, owner, subj string) Registration {
	t.Helper()
	sig, err := id.IDSig(sap.AgentName, owner, subj)
	if err != nil {
		t.Fatal(err)
	}
	return Registration{Subject: subj, Metadata: map[string]string{
		"owner": owner, "agent": sap.AgentName, "account": id.Account, "user_nkey": id.User, "id_sig": sig}}
}

func TestVerify(t *testing.T) {
	bob, carol := identity(t, "ACCT"), identity(t, "ACCT")
	dir := Directory{User: bob.User, HasRecord: true, Owner: "acme", Account: "ACCT"}
	good := reg(t, bob, "acme", subject)

	if name, ok := Verify(good, dir); !ok || name != "acme_ops_bob" {
		t.Fatalf("the genuine registration: %q, %v", name, ok)
	}

	unsigned := reg(t, bob, "acme", subject)
	delete(unsigned.Metadata, "id_sig")
	flipped := reg(t, bob, "acme", subject)
	// A different first character than the signature has: replacing it with a fixed "A" left the signature unchanged
	// (and valid) about one time in 64, when it already began with "A".
	first := "A"
	if flipped.Metadata["id_sig"][:1] == first {
		first = "B"
	}
	flipped.Metadata["id_sig"] = first + flipped.Metadata["id_sig"][1:]
	otherOwner := reg(t, bob, "someone-else", subject)            // validly signed, owner differs from the record
	otherAccount := reg(t, identity(t, "OTHER"), "acme", subject) // account is not the record's

	for _, c := range []struct {
		name string
		reg  Registration
		dir  Directory
	}{
		{"unsigned", unsigned, dir},
		{"forged id_sig", flipped, dir},
		{"wrong key (carol's key, answering for bob)", reg(t, carol, "acme", subject), Directory{User: bob.User, HasRecord: true, Owner: "acme", Account: "ACCT"}},
		{"another subject than the one signed", Registration{Subject: "agents.prompt.acme.ops.carol", Metadata: good.Metadata}, dir},
		{"owner differs from the record's", otherOwner, dir},
		{"account differs from the record's", otherAccount, Directory{User: bob.User, HasRecord: true, Owner: "acme", Account: "ACCT"}},
		{"no record for the address", good, Directory{User: bob.User, Owner: "acme", Account: "ACCT"}},
		{"the record holds another key", good, Directory{User: carol.User, HasRecord: true, Owner: "acme", Account: "ACCT"}},
		{"the record holds no key", good, Directory{HasRecord: true, Owner: "acme", Account: "ACCT"}},
		{"a signed status subject", reg(t, bob, "acme", "agents.status.acme.ops.bob"), dir},
		{"a signed heartbeat subject", reg(t, bob, "acme", "agents.hb.acme.ops.bob"), dir},
		{"a signed mail subject", reg(t, bob, "acme", "agents.mail.acme.ops.bob"), dir},
		{"not a prompt subject", Registration{Subject: "nonsense", Metadata: good.Metadata}, dir},
	} {
		if _, ok := Verify(c.reg, c.dir); ok {
			t.Errorf("%s: verified", c.name)
		}
	}
}

// A record with no account verifies nothing: it is not read as accepting any account.
func TestVerifyRecordWithoutAccount(t *testing.T) {
	bob := identity(t, "ACCT")
	dir := Directory{User: bob.User, HasRecord: true, Owner: "acme"}
	if _, ok := Verify(reg(t, bob, "acme", subject), dir); ok {
		t.Error("a record with no account accepted a registration that claims an account")
	}
	if _, ok := Verify(reg(t, identity(t, ""), "acme", subject), dir); ok {
		t.Error("a record with no account accepted a registration that claims none")
	}
}
