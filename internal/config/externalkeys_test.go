package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func TestExternalKeys(t *testing.T) {
	u, _ := nkeys.CreateUser()
	upub, _ := u.PublicKey()
	a, _ := nkeys.CreateAccount()
	apub, _ := a.PublicKey()

	for _, raw := range []string{``, `,"external_keys":null`, `,"external_keys":[]`} {
		c, err := loadWith(t, idFile+raw+`}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
		if err != nil || len(c.ExternalKeys) != 0 {
			t.Fatalf("%q: %v %v", raw, c.ExternalKeys, err)
		}
	}
	c, err := loadWith(t, idFile+`,"external_keys":["`+upub+`"]}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
	if err != nil || !reflect.DeepEqual(c.ExternalKeys, []string{upub}) {
		t.Fatalf("%v %v", c.ExternalKeys, err)
	}

	for raw, named := range map[string]string{
		`"` + upub + `"`:   "must be an array",
		`{}`:               "must be an array",
		`[42]`:             "42",
		`[null]`:           "null",
		`["` + apub + `"]`: apub,
		`["garbage"]`:      `"garbage"`,
		`["*"]`:            `"*"`,
		`[""]`:             `""`,
		`[">"]`:            `">"`,
	} {
		_, err := loadWith(t, idFile+`,"external_keys":`+raw+`}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
		if err == nil || !strings.Contains(err.Error(), "external_keys") || !strings.Contains(err.Error(), named) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
