package main

import (
	"bytes"
	"github.com/bogworks/mash/internal/cmdcore"
	"strings"
	"testing"
)

// The tool's own help lists the commands by group, in the order Everyday, Setup, Integration, Raw SAP, each with its
// one-line description, in no more than 80 columns. The commands on the command table are generated from it.
func TestTopLevelHelpGolden(t *testing.T) {
	const want = `Trusted mail between agents. Speaks as one endpoint.

Everyday:
  mash send <address> [<message>] [options]    Send a message to an endpoint.
  mash inbox [options]                         List the messages this endpoint
                                               has received.
  mash read <number>                           Show one message in full.
  mash who [<prefix>] [options]                List the endpoints you can see,
                                               and which are online.
  mash status                                  Say in one line whether mail
                                               works for this endpoint, and
                                               whether it is online.
  mash doctor [options]                        Check everything mail depends on
                                               for this endpoint, and say how to
                                               fix what is wrong. Changes
                                               nothing.
  mash version                                 Print the version. It can also be
                                               run as --version.

Setup:
  mash join <file> [options]                   Install an endpoint's credentials
                                               on this machine from a join file.
  mash key create [options]                    Make your own key pair, for when
                                               the seed must never leave this
                                               machine.

Integration:
  mash serve [options]                         Run as an MCP server for an agent
                                               host. Started by the host, not by
                                               hand.
  mash hook                                    Print mail waiting for this
                                               session, in the form the agent
                                               host expects. Run by the host
                                               after each tool call and with
                                               each prompt.

Raw SAP:
  mash prompt <address> <text>                 Ask an endpoint a live question
                                               and wait for its answer. Not
                                               stored: if the recipient is not
                                               there, it fails.

Global options:
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
		if parent, _, sub := strings.Cut(name, " "); sub { // key create: the help lists its parent, key, under the same group
			h, ok := handCommands[parent]
			if !ok || seen[parent] != group || !strings.Contains(usage, "\n"+cmdcore.ListLine(h[0], h[1], helpCol)+"\n") {
				t.Errorf("%s: its parent %s is not listed under %s in the help", name, parent, group)
			}
			continue
		}
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
