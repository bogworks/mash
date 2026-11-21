package joinfile

import (
	"errors"
	"strings"
	"testing"
)

func TestEncodeParseRoundTrip(t *testing.T) {
	ca := "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
	for _, j := range []File{
		{MashJoin: Format, Address: "acme.demo.bob", Servers: []string{"tls://a:4222", "tls://b:4222"}, CA: &ca, Creds: "creds text\n"},
		{MashJoin: Format, Address: "acme.demo.bob", Servers: []string{"tls://a:4222"}, UserJWT: "jwt"},
	} {
		b, err := j.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(b), "{\n  \"mash_join\": 1,\n") || !strings.HasSuffix(string(b), "}\n") {
			t.Errorf("encoding: %q", b)
		}
		got, err := Parse(b)
		if err != nil || got.Address != j.Address || got.Creds != j.Creds || got.UserJWT != j.UserJWT || len(got.Servers) != len(j.Servers) {
			t.Errorf("round trip: %+v, %v", got, err)
		}
	}
}

func TestParseRefusesWithoutEchoingTheFile(t *testing.T) {
	for name, in := range map[string]string{
		"not json":      `SECRET`,
		"trailing":      `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET"} {}`,
		"version":       `{"mash_join":2,"address":"a.b.c","servers":["s"],"creds":"SECRET"}`,
		"no servers":    `{"mash_join":1,"address":"a.b.c","servers":[],"creds":"SECRET"}`,
		"neither":       `{"mash_join":1,"address":"a.b.c","servers":["s"]}`,
		"both":          `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET","user_jwt":"SECRET"}`,
		"unknown field": `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET","x":1}`,
	} {
		_, err := Parse([]byte(in))
		if !errors.Is(err, ErrNotJoinFile) || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseRefusesDuplicateKeysAndTrailingBytes(t *testing.T) {
	const ok = `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET"}`
	for name, in := range map[string]string{
		"dup creds":        `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET","creds":"x"}`,
		"dup address":      `{"mash_join":1,"address":"a.b.c","address":"d.e.f","servers":["s"],"creds":"SECRET"}`,
		"dup other case":   `{"mash_join":1,"address":"a.b.c","servers":["s"],"creds":"SECRET","Creds":"x"}`,
		"dup unicode fold": `{"mash_join":1,"address":"a.b.c","servers":["s"],"user_jwt":"j","cred\u017f":"SECRET","creds":""}`,
		"dup upper first":  `{"mash_join":1,"address":"a.b.c","servers":["s"],"CREDS":"SECRET","creds":"x"}`,
		"trailing brace":   ok + `}`,
		"trailing bracket": ok + `]`,
		"second object":    ok + `{}`,
		"garbage":          ok + ` SECRET`,
		"newline garbage":  ok + "\nx",
	} {
		_, err := Parse([]byte(in))
		if !errors.Is(err, ErrNotJoinFile) || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, in := range map[string]string{
		"newline":     ok + "\n",
		"white space": " \t" + ok + " \r\n\n",
	} {
		if _, err := Parse([]byte(in)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
