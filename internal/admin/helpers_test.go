package admin_test

import (
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
)

// ep is the test endpoint address named name: owner testenv.Owner ("o"), group "dev", instance name. Its flat key is
// "o_dev_"+name and its inbox stream "inbox-o_dev_"+name.
func ep(t testing.TB, name string) sap.Address {
	t.Helper()
	return epIn(t, testenv.Owner, "dev", name)
}

// epIn is the test endpoint address owner.group.instance.
func epIn(t testing.TB, owner, group, instance string) sap.Address {
	t.Helper()
	a, err := sap.NewAddress(owner, group, instance)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
