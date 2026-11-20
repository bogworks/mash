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
