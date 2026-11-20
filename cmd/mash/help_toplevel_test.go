package main

import (
	"bytes"
	"strings"
	"testing"
)

// The tool's own help lists the commands by group, in the order Everyday, Setup, Integration, Raw SAP, each with its
// one-line description, in no more than 80 columns. The commands on the command table are generated from it.
func TestTopLevelHelpGolden(t *testing.T) {
	const want = `mash: a messaging mesh for agents (SAP 0.3 + mail, on NATS JetStream)

Everyday:
  mash send <address> [<message>] [options]    Send a message to an endpoint.
  mash inbox [--limit N] [--after SEQ]         Recent messages, or the N after
                                               SEQ (oldest first)
  mash read <seq>                              Print one message from your inbox
  mash who [<prefix>] [options]                List the endpoints you can see,
                                               and which are online.
  mash status                                  Say in one line whether mail
                                               works for this endpoint, and
                                               whether it is online.
  mash doctor                                  Check everything mail depends on
                                               for this endpoint, and say how to
                                               fix what is wrong. Changes
                                               nothing.
  mash version                                 Print the version.

Setup:
  mash key create --out FILE [options]         Make a user key pair: seed to
                                               FILE (0600), public key to
                                               FILE.pub; needs no config
  mash credential create --seed FILE --jwt FILE [options]
                                               Join a seed and its user JWT into
                                               a credentials file; needs no
                                               config
  mash admin <verb> ...                        Deprecated; use mashctl

Integration:
  mash serve                                   The Claude Code channel server
                                               (started by the plugin)
  mash hook                                    The plugin's hook: shows the
                                               session its spooled mail
  mash plugin export <dir>                     Write this build's Claude Code
                                               plugin and a marketplace for it
                                               into <dir>

Raw SAP:
  mash prompt <to> <text...>                   A live, signed SAP prompt; prints
                                               the reply

`
	for _, args := range [][]string{{"--help"}, {"help"}, {"-h"}} {
		var out, errb bytes.Buffer
		if code := run(args, func(string) string { return "" }, strings.NewReader(""), &out, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("%v: %d %q", args, code, errb.String())
		}
		if !strings.HasPrefix(out.String(), want) {
			t.Errorf("%v: the command list is\n%s\nwant\n%s", args, out.String(), want)
		}
		for _, l := range strings.Split(out.String(), "\n") {
			if len([]rune(l)) > 80 {
				t.Errorf("%v: %d columns: %q", args, len([]rune(l)), l)
			}
		}
	}
}

// Every command on the table is listed under its own group, and every command of the layout is on one line of its
// own: the layout and the table agree.
func TestHelpLayoutAgreesWithTable(t *testing.T) {
	r := newCommands(nil)
	seen := map[string]string{}
	for g, names := range helpLayout {
		for _, n := range names {
			seen[n] = g
		}
	}
	for _, e := range commandEntries {
		name, group := e.CLIName(), e.Group()
		if group == "" {
			t.Errorf("%s has no group", name)
		}
		if seen[name] != group {
			t.Errorf("%s is in group %q on the table and %q in the help layout", name, group, seen[name])
		}
		if !strings.Contains(usage, "\n"+r.ListLine(name, helpCol)+"\n") {
			t.Errorf("%s is not listed in the help", name)
		}
	}
	for n := range handCommands {
		if _, ok := r.Lookup(n); ok {
			t.Errorf("%s is on the table and still has a hand-written line", n)
		}
	}
}
