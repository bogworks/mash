package shapes

import (
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

const subj = "agents.mail.acme.demo.alice"

// keys are a real account and user public key: the parser checks their form.
func keys(t *testing.T) (acct, user string) {
	t.Helper()
	a, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	u, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	acct, _ = a.PublicKey()
	user, _ = u.PublicKey()
	return acct, user
}

// Every case of the corpus is the shape it names: the SDK adapter classifies it as malformed with exactly that class.
func TestEveryCaseIsClassifiedAsItsClass(t *testing.T) {
	acct, user := keys(t)
	cs := Corpus(acct, user, subj)
	if len(cs) != 14 {
		t.Fatalf("%d cases, want the ten shapes and four candidates", len(cs))
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if seen[c.Token] {
			t.Errorf("token %s used twice", c.Token)
		}
		seen[c.Token] = true
		trust, class := sapsdk.Classify(subj, c.Values, []byte(c.Body()))
		if trust != sap.TrustMalformed || class != c.Class {
			t.Errorf("%s: classified %q %q, want malformed %q", c.Name, trust, class, c.Class)
		}
		if !strings.Contains(c.Body(), "[tok:"+c.Token+"]") {
			t.Errorf("%s: the body does not carry the token", c.Name)
		}
	}
	for i, want := range []string{"header too long", "time not in UTC", "control character in name", "name too long",
		"broken character in name", "a field appears twice", "space in subject", "signature unreadable",
		"empty sender header", "two sender headers"} {
		if cs[i].Class != want {
			t.Errorf("shape %d is %q, the contract's order says %q", i+1, cs[i].Class, want)
		}
	}
}
