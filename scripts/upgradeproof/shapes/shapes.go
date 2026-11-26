// Package shapes builds the stored-mail corpus of AT-011 step 2: one message in each of the ten shapes REQ-005 names
// as refused by the SDK's strict parser, the X2 and X3 candidates, each as the list of Agent-Sender header values to
// publish. The headers are written by hand: the parser refuses them before it checks a signature, so a signature of the
// right form that does not verify is enough. Nothing here connects to anything.
package shapes

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Case is one message to store: the header values (every Agent-Sender value; none for no header), the class the
// switched-on release must list it with, and the token that identifies it.
type Case struct {
	Name   string   // "1 header too long", the contract's order
	Class  string   // the frozen refusal class
	Values []string // each is one Agent-Sender value
	Token  string   // the message's unique token, written in its body
}

// Body is the text the message carries: its name and its token, so a view that shows a body can be matched to it.
func (c Case) Body() string { return c.Name + " [tok:" + c.Token + "]" }

// Corpus is the ten shapes, then X2 (invalid UTF-8; a sub with an empty token) and X3 (an unsigned claim with a bad time
// or message id), for the sender account and user, as mail for the subject subj.
func Corpus(account, user, subj string) []Case {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	zeroSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	const ts = "2026-11-15T12:00:00Z"
	member := func(k, v string) string { return q(k) + ":" + v }
	obj := func(ms ...string) string { return "{" + strings.Join(ms, ",") + "}" }
	// signed is a signed-shape header; over replaces members, extra adds raw members after them.
	signed := func(tok string, over map[string]string, extra ...string) string {
		f := map[string]string{"v": "1", "account": q(account), "user": q(user), "sub": q(subj), "ts": q(ts), "nonce": q("at011-" + tok), "sig": q(zeroSig)}
		for k, v := range over {
			f[k] = v
		}
		ms := []string{}
		for _, k := range []string{"v", "account", "user", "sub", "ts", "nonce", "sig"} {
			if f[k] != "" {
				ms = append(ms, member(k, f[k]))
			}
		}
		return obj(append(ms, extra...)...)
	}
	unsigned := func(ms ...string) string {
		return obj(append([]string{member("v", "1"), member("account", q(account)), member("user", q(user))}, ms...)...)
	}
	type spec struct {
		name, class string
		values      []string
	}
	specs := []spec{
		{"1 header too long", "header too long", []string{signed("s01", nil, member("pad", q(strings.Repeat("p", 2100))))}},
		{"2 time not in UTC", "time not in UTC", []string{signed("s02", map[string]string{"ts": q("2026-11-15T13:00:00+01:00")})}},
		{"3 control character in name", "control character in name", []string{unsigned(member("name", `"a\u0001b"`))}},
		{"4 name too long", "name too long", []string{unsigned(member("name", q(strings.Repeat("n", 65))))}},
		{"5 broken character in name", "broken character in name", []string{unsigned(member("name", `"a\ud800b"`))}},
		{"6 a field appears twice", "a field appears twice", []string{signed("s06", nil, member("user", q(user)))}},
		{"7 space in subject", "space in subject", []string{signed("s07", map[string]string{"sub": q("agents.mail.a b.o.n")})}},
		{"8 signature unreadable", "signature unreadable", []string{signed("s08", map[string]string{"sig": q("!!")})}},
		{"9 empty sender header", "empty sender header", []string{""}},
		{"10 two sender headers", "two sender headers", []string{unsigned(), unsigned()}},
		{"X2 invalid UTF-8", "unreadable sender header", []string{"{\"v\":1,\"account\":\"\xff\"}"}},
		{"X2 empty token in sub", "unreadable sender header", []string{signed("x2b", map[string]string{"sub": q("agents..a.o.n")})}},
		{"X3 time not valid", "time not valid", []string{unsigned(member("ts", q("yesterday")))}},
		{"X3 message id not valid", "message id not valid", []string{unsigned(member("nonce", q("bad nonce!")))}},
	}
	tokens := []string{"s01", "s02", "s03", "s04", "s05", "s06", "s07", "s08", "s09", "s10", "x2a", "x2b", "x3a", "x3b"}
	out := make([]Case, len(specs))
	for i, s := range specs {
		out[i] = Case{Name: s.name, Class: s.class, Values: s.values, Token: tokens[i]}
	}
	return out
}
