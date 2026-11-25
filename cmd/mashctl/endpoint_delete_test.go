package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
)

// terminal makes the delete command see a terminal whose user types input.
func terminal(t *testing.T, input string) {
	t.Helper()
	prevTerm, prevIn := stdinIsTerminal, confirmStdin
	stdinIsTerminal = func() bool { return true }
	confirmStdin = func() io.Reader { return strings.NewReader(input) }
	t.Cleanup(func() { stdinIsTerminal, confirmStdin = prevTerm, prevIn })
}

// noTerminal makes the delete command see standard input that is not a terminal.
func noTerminal(t *testing.T) {
	t.Helper()
	prev := stdinIsTerminal
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = prev })
}

// putMessages stores n messages in the inbox of a.
func (h *fleetEnv) putMessages(a sap.Address, n int) {
	h.t.Helper()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := js.Publish(context.Background(), a.MailSubject(), []byte(fmt.Sprintf("m%d", i))); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestEndpointDeleteAsksThenDeletes(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("q")
	h.seed(a)
	h.putMessages(a, 1)
	terminal(t, "o.g.q\n")
	out, errb, code := h.run("endpoint", "delete", "o.g.q")
	want := "This deletes o.g.q and the 1 message in its inbox.\nMail that arrives before it is gone is lost too. It cannot be undone.\nType the address to confirm: Deleted endpoint o.g.q\n"
	if code != 0 || out != want || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(h.records()) != 0 {
		t.Error("record left")
	}
}

func TestEndpointDeleteWrongTextDeletesNothing(t *testing.T) {
	h := newFleetEnv(t)
	s := stubRevoke(t)
	a := ad("q")
	h.seed(a)
	for _, typed := range []string{"o.g.other\n", "\n", "o.g.q extra\n", "O.G.Q\n", ""} {
		terminal(t, typed)
		out, errb, code := h.run("endpoint", "delete", "o.g.q")
		if code != 1 || !strings.HasSuffix(errb, "error: that was not the address; nothing was deleted\n") || strings.Contains(out, "Deleted") {
			t.Errorf("%q: %d %q %q", typed, code, out, errb)
		}
	}
	if len(h.records()) != 1 || len(s.calls) != 0 {
		t.Errorf("something changed: records %v calls %v", h.records(), s.calls)
	}
}

func TestEndpointDeleteNoTerminalAndNoYesIsUsage(t *testing.T) {
	h := newFleetEnv(t)
	s := stubRevoke(t)
	a := ad("q")
	h.seed(a)
	noTerminal(t)
	for _, extra := range [][]string{nil, {"--json"}} {
		out, errb, code := h.run(append([]string{"endpoint", "delete", "o.g.q"}, extra...)...)
		wantOut, wantErr := "", "error: mashctl endpoint delete needs a terminal to type the address\nhint: mashctl endpoint delete --help\n"
		if len(extra) > 0 {
			wantOut, wantErr = `{"error":"mashctl endpoint delete needs a terminal to type the address","hint":"mashctl endpoint delete --help"}`+"\n", ""
		}
		if code != 2 || out != wantOut || errb != wantErr {
			t.Errorf("%v: %d %q %q", extra, code, out, errb)
		}
		if strings.Contains(out+errb, "--yes") {
			t.Errorf("%v: the message advertises --yes: %q %q", extra, out, errb)
		}
	}
	if len(h.records()) != 1 || len(s.calls) != 0 {
		t.Errorf("something changed: records %v calls %v", h.records(), s.calls)
	}
	if st, _ := h.adminOf().RetireState(context.Background(), a); st != "" {
		t.Errorf("retired marker %q written", st)
	}
}

// The address is looked up before the question: an address that does not exist is that error, with or without a terminal.
func TestEndpointDeleteMissingAddressIsNotAskedAbout(t *testing.T) {
	h := newFleetEnv(t)
	noTerminal(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.nope")
	if code != 1 || out != "" || errb != "error: no endpoint o.g.nope\nhint: mashctl endpoint list\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestEndpointDeleteYesDoesNotAsk(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("q"))
	noTerminal(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.q", "--yes")
	if code != 0 || out != "Deleted endpoint o.g.q\n" || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestEndpointDeleteDryRunAsksNothingAndChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	s := stubRevoke(t)
	a := ad("q")
	h.seed(a)
	h.putMessages(a, 3)
	noTerminal(t)
	for _, extra := range [][]string{nil, {"--yes"}} {
		out, errb, code := h.run(append([]string{"endpoint", "delete", "o.g.q", "--dry-run"}, extra...)...)
		if code != 0 || out != "Would delete endpoint o.g.q and the 3 messages in its inbox\n" || errb != "" {
			t.Errorf("%v: %d %q %q", extra, code, out, errb)
		}
	}
	if len(h.records()) != 1 || len(s.calls) != 0 {
		t.Errorf("a dry run changed something: records %v calls %v", h.records(), s.calls)
	}
	if n, err := h.adminOf().InboxMessages(context.Background(), a); err != nil || n != 3 {
		t.Errorf("messages %d %v", n, err)
	}
	if st, _ := h.adminOf().RetireState(context.Background(), a); st != "" {
		t.Errorf("retired marker %q written", st)
	}
}

func TestEndpointDeleteMessageCountWords(t *testing.T) {
	atTerminal(t) // expects the terminal form of --json (#1043)
	for _, c := range []struct {
		n    int
		want string
	}{
		{0, "This deletes o.g.q and its empty inbox.\nMail that arrives before it is gone is lost too. It cannot be undone.\n"},
		{1, "This deletes o.g.q and the 1 message in its inbox.\nMail that arrives before it is gone is lost too. It cannot be undone.\n"},
		{3, "This deletes o.g.q and the 3 messages in its inbox.\nMail that arrives before it is gone is lost too. It cannot be undone.\n"},
	} {
		t.Run(fmt.Sprint(c.n), func(t *testing.T) {
			h := newFleetEnv(t)
			a := ad("q")
			h.seed(a)
			h.putMessages(a, c.n)
			terminal(t, "o.g.q\n")
			out, errb, code := h.run("endpoint", "delete", "o.g.q", "--json")
			// with --json the question goes to stderr and stdout stays one object
			if code != 0 || errb != c.want+"Type the address to confirm: " {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			if want := fmt.Sprintf("{\n  \"address\": \"o.g.q\",\n  \"deleted\": true,\n  \"messages_deleted\": %d\n}\n", c.n); out != want {
				t.Errorf("json %q, want %q", out, want)
			}
		})
	}
}

func TestEndpointDeleteJSONUsageErrors(t *testing.T) {
	h := newFleetEnv(t)
	for _, c := range []struct{ args, want string }{
		{"o.g.x --bogus", `{"error":"unknown flag --bogus"}`},
		{"", `{"error":"missing the endpoint address (owner.group.name)","hint":"mashctl endpoint delete --help"}`},
		{"o.g.x o.g.y", `{"error":"too many arguments","hint":"mashctl endpoint delete --help"}`},
	} {
		so, se, code := h.run(append([]string{"endpoint", "delete"}, append(strings.Fields(c.args), "--json")...)...)
		if code != 2 || se != "" || strings.TrimSpace(so) != c.want {
			t.Errorf("%q: %d %q %q", c.args, code, so, se)
		}
	}
	so, se, code := h.run("endpoint", "delete", "Not A Valid!", "--json")
	if code != 2 || se != "" || !strings.Contains(so, `"hint":"mashctl endpoint delete --help"`) {
		t.Errorf("bad address: %d %q %q", code, so, se)
	}
}

// A refusal of the guarded removal (here: the record's key changed meanwhile) is one error line, and nothing is deleted.
func TestEndpointDeleteRefusalIsOneErrorLine(t *testing.T) {
	h := newFleetEnv(t)
	first := h.provisionDeclared("b")
	afterRetireRead = func() {
		adm, _ := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
		_, other := h.e.Mint(t, ad("b2"))
		adm.SetUser(context.Background(), ad("b"), h.e.Account, first, other)
	}
	t.Cleanup(func() { afterRetireRead = nil })
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes", "--json")
	if code != 1 || errb != "" || !strings.HasPrefix(out, `{"error":"`) || !strings.Contains(out, "nothing was changed") || strings.Count(out, "\n") != 1 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}
