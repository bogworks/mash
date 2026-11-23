package config

import (
	"reflect"
	"strconv"
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

	op, _ := nkeys.CreateOperator()
	opub, _ := op.PublicKey()
	seed, _ := u.Seed()

	for raw, named := range map[string]string{
		`"` + upub + `"`:                  "must be an array",
		`{}`:                              "must be an array",
		`[42]`:                            "entry 1 (not shown)",
		`[null]`:                          "entry 1 (not shown)",
		`["` + apub + `"]`:                "entry 1 (not shown)",
		`["` + opub + `"]`:                "entry 1 (not shown)",
		`["` + string(seed) + `"]`:        "entry 1 (not shown)",
		`["` + upub + `","` + apub + `"]`: "entry 2 (not shown)",
		`["garbage"]`:                     "entry 1 (not shown)",
		`["*"]`:                           "entry 1 (not shown)",
		`[""]`:                            "entry 1 (not shown)",
		`[">"]`:                           "entry 1 (not shown)",
		`["Ugarbage"]`:                    `"Ugarbage"`,
		`["` + upub + `\n"]`:              strconv.Quote(upub + "\n"),
		`[" ` + upub + `"]`:               strconv.Quote(" " + upub),
		`["` + upub + `\r\n"]`:            strconv.Quote(upub + "\r\n"),
		`["` + upub + `","` + upub + `"]`: "lists " + upub + " twice",
	} {
		_, err := loadWith(t, idFile+`,"external_keys":`+raw+`}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
		if err == nil || !strings.Contains(err.Error(), "external_keys") || !strings.Contains(err.Error(), named) {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		for _, secret := range []string{string(seed), apub, opub} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the error echoes %q: %v", raw, secret, err)
			}
		}
	}
}
